// Package balancechecker provides a self-contained balance query module for OpenAI-compatible providers.
package balancechecker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// calculateNextCycleTimestamp returns the next cycle boundary timestamp based on the given hour interval.
// For example, if hours=5 and current time is 3:30, it returns 5:00 today.
// If current time is 22:00 and hours=5, it returns 0:00 tomorrow.
func calculateNextCycleTimestamp(hours int) string {
	now := time.Now()
	currentHour := now.Hour()
	nextBoundary := ((currentHour / hours) + 1) * hours
	var nextTime time.Time
	if nextBoundary >= 24 {
		nextTime = time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	} else {
		nextTime = time.Date(now.Year(), now.Month(), now.Day(), nextBoundary, 0, 0, 0, now.Location())
	}
	return fmt.Sprintf("%d", nextTime.Unix())
}

const defaultTimeout = 5 * time.Second

// BalanceResult represents the result of a balance query.
type BalanceResult struct {
	Provider    string  `json:"provider"`
	Balance     float64 `json:"balance"`
	UsedBalance float64 `json:"used_balance"`
	ResetCycle  string  `json:"reset_cycle,omitempty"`
	Error       string  `json:"error,omitempty"`
	Success     bool    `json:"success"`
	RawResponse string  `json:"raw_response,omitempty"`
}

// Checker performs balance queries for providers.
type Checker struct {
	httpClient *http.Client
}

// NewChecker creates a new balance checker.
func NewChecker() *Checker {
	return &Checker{
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}
}

// QueryBalance performs a balance query for the given provider configuration.
func (c *Checker) QueryBalance(provider BalanceProviderConfig, apiKey string) BalanceResult {
	result := BalanceResult{
		Provider: provider.Name,
		Success:  false,
	}

	if !provider.Enabled {
		result.Error = "provider is disabled"
		return result
	}

	if provider.URL == "" {
		result.Error = "url is empty"
		return result
	}

	// Replace {api_key} placeholder in headers
	headersStr := strings.ReplaceAll(provider.Headers, "{api_key}", apiKey)

	// Build headers map
	headers, err := parseHeaders(headersStr)
	if err != nil {
		result.Error = fmt.Sprintf("failed to parse headers: %v", err)
		return result
	}

	// Create request
	var body io.Reader
	if provider.Method == "POST" && provider.Body != "" {
		body = bytes.NewBufferString(provider.Body)
	}

	req, err := http.NewRequestWithContext(context.Background(), provider.Method, provider.URL, body)
	if err != nil {
		result.Error = fmt.Sprintf("failed to create request: %v", err)
		return result
	}

	// Set headers
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		result.Error = fmt.Sprintf("request failed: %v", err)
		return result
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		result.Error = fmt.Sprintf("failed to read response: %v", err)
		return result
	}

	rawResponse := string(respBody)
	result.RawResponse = rawResponse

	if resp.StatusCode != http.StatusOK {
		result.Error = fmt.Sprintf("non-200 status code: %d, body: %s", resp.StatusCode, truncateString(rawResponse, 200))
		return result
	}

	// Parse balance
	balance, err := c.parseBalance(respBody, provider.BalancePath, provider.BalanceType, provider.BalanceMultiplier)
	if err != nil {
		result.Error = fmt.Sprintf("failed to parse balance: %v", err)
		return result
	}
	result.Balance = balance

	// Parse used balance (supports formula syntax, apply multiplier)
	usedBalance, err := c.parseUsedBalance(respBody, provider.UsedBalancePath, provider.BalanceMultiplier)
	if err != nil {
		result.UsedBalance = 0
	} else {
		result.UsedBalance = usedBalance
	}

	// Parse reset cycle
	if provider.ResetCyclePath != "" {
		// Special case: {current_time} returns current timestamp in seconds (10 digit)
		if provider.ResetCyclePath == "{current_time}" {
			result.ResetCycle = fmt.Sprintf("%d", time.Now().Unix())
		} else if strings.HasPrefix(provider.ResetCyclePath, "{cycle:") && strings.HasSuffix(provider.ResetCyclePath, "}") {
			// Cycle hours mode: {cycle:24} means next 24-hour boundary
			hoursStr := strings.TrimPrefix(strings.TrimSuffix(provider.ResetCyclePath, "}"), "{cycle:")
			hours, err := strconv.Atoi(hoursStr)
			if err == nil && hours > 0 {
				result.ResetCycle = calculateNextCycleTimestamp(hours)
			}
		} else {
			// Check if it's a formula (contains operators)
			tokens := splitFormulaTokens(provider.ResetCyclePath)
			if len(tokens) > 1 {
				// Evaluate as formula
				resetVal, err := evaluateFormula(rawResponse, tokens)
				if err == nil {
					result.ResetCycle = fmt.Sprintf("%d", int64(resetVal))
				}
			} else {
				// Single path - normalize and extract
				rp := normalizePath(provider.ResetCyclePath)
				resetCycle := gjson.Get(rawResponse, rp)
				if resetCycle.Exists() {
					result.ResetCycle = resetCycle.String()
				}
			}
		}
	}

	result.Success = true
	return result
}

// parseBalance extracts and converts the balance value from the response.
func (c *Checker) parseBalance(data []byte, path, balanceType string, multiplier float64) (float64, error) {
	if path == "" {
		return 0, fmt.Errorf("balance_path is empty")
	}

	// Check if path contains formula operators
	tokens := splitFormulaTokens(path)
	if len(tokens) > 1 {
		// It's a formula, evaluate it
		result, err := evaluateFormula(string(data), tokens)
		if err != nil {
			return 0, fmt.Errorf("failed to parse balance: %v", err)
		}
		if multiplier != 0 {
			result = result * multiplier
		}
		return result, nil
	}

	// Single path - normalize and extract
	path = normalizePath(path)
	value := gjson.Get(string(data), path)
	if !value.Exists() {
		return 0, fmt.Errorf("path not found: %s", path)
	}

	var balance float64
	switch balanceType {
	case "int":
		balance = float64(value.Int())
	default:
		balance = value.Float()
	}

	if multiplier != 0 {
		balance = balance * multiplier
	}

	return balance, nil
}

// parseUsedBalance extracts the used balance, supporting formula syntax.
// Supports gjson paths with #(...) hash scan syntax and math operators +-*/()
func (c *Checker) parseUsedBalance(data []byte, usedBalancePath string, multiplier float64) (float64, error) {
	if usedBalancePath == "" {
		return 0, nil
	}

	// Split formula into tokens (gjson paths and operators)
	// Each path token will be normalized in evaluateFormula
	tokens := splitFormulaTokens(usedBalancePath)
	if len(tokens) == 0 {
		return 0, fmt.Errorf("invalid formula: %s", usedBalancePath)
	}

	// Evaluate formula
	result, err := evaluateFormula(string(data), tokens)
	if err != nil {
		return 0, err
	}

	// Apply multiplier
	if multiplier != 0 {
		result = result * multiplier
	}

	return result, nil
}

// normalizePath normalizes a gjson-style path by removing $. prefix
// and converting [n] to .n for gjson
// Also fixes common escape issues like \" -> " and \' -> '
func normalizePath(path string) string {
	// Remove leading $. if present
	path = strings.TrimPrefix(path, "$.")
	// Convert [n] to .n for gjson compatibility (handles multi-digit numbers)
	// Match patterns like [0], [1], [10], [123], etc.
	result := []byte{}
	for i := 0; i < len(path); i++ {
		if path[i] == '[' {
			j := i + 1
			for j < len(path) && path[j] >= '0' && path[j] <= '9' {
				j++
			}
			if j > i+1 && j < len(path) && path[j] == ']' {
				result = append(result, '.')
				result = append(result, path[i+1:j]...)
				i = j
				continue
			}
		}
		result = append(result, path[i])
	}
	path = string(result)
	// Fix escaped quotes: \" -> " (order matters - handle single backslash-quote first)
	path = strings.Replace(path, "\\\"", "\"", -1)
	path = strings.Replace(path, "\\'", "'", -1)
	// Handle escaped backslash followed by quote: \\\" -> " (double escaped)
	path = strings.Replace(path, "\\\\\"", "\"", -1)
	return path
}

// splitFormulaTokens splits a formula into gjson expressions and operators.
// For "rows.#(tokenNo%\"bundle_848\").tokensMagnitude-rows.#(tokenNo%\"bundle_848\").tokenBalance" it returns:
// ["rows.#(tokenNo%\"bundle_848\").tokensMagnitude", "-", "rows.#(tokenNo%\"bundle_848\").tokenBalance"]
func splitFormulaTokens(formula string) []string {
	var tokens []string
	var current []rune
	depth := 0

	for i := 0; i <= len(formula); i++ {
		ch := rune(0)
		if i < len(formula) {
			ch = rune(formula[i])
		}
		// Entering #(...)
		if depth == 0 && ch == '#' && i+1 < len(formula) && rune(formula[i+1]) == '(' {
			current = append(current, ch, '(')
			depth = 1
			i++
			continue
		}
		// Inside #(...): track nested parens
		if depth > 0 {
			current = append(current, ch)
			if ch == '(' {
				depth++
			} else if ch == ')' {
				depth--
			}
			continue
		}
		// depth == 0: check for mathematical operators only
		if ch == '+' || ch == '-' || ch == '*' || ch == '/' || ch == '(' || ch == ')' || ch == '=' || i == len(formula) {
			if len(current) > 0 {
				tokens = append(tokens, strings.TrimSpace(string(current)))
				current = nil
			}
			// emit + - * / as operators; ( ) and end are not separate tokens
			if (ch == '+' || ch == '-' || ch == '*' || ch == '/') && i < len(formula) {
				tokens = append(tokens, string(ch))
			}
			continue
		}
		// dot is part of gjson paths, not a separator — don't split on it
		// Whitespace: skip entirely (don't split tokens)
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			continue
		}
		current = append(current, ch)
	}
	if len(current) > 0 {
		tokens = append(tokens, strings.TrimSpace(string(current)))
	}
	return tokens
}

// evaluateFormula evaluates a formula split into tokens.
func evaluateFormula(data string, tokens []string) (float64, error) {
	if len(tokens) == 0 {
		return 0, fmt.Errorf("empty formula")
	}

	var exprStr strings.Builder
	for _, token := range tokens {
		trimmed := strings.TrimSpace(token)
		if trimmed == "" {
			continue
		}

		if len(trimmed) == 1 && strings.ContainsAny(trimmed, "+-*/=()") {
			exprStr.WriteString(trimmed)
		} else if isNumericLiteral(trimmed) {
			// It's a numeric literal, write directly
			exprStr.WriteString(trimmed)
		} else {
			// Normalize the gjson path (remove $. prefix and convert [n] to .n)
			normalizedPath := normalizePath(trimmed)
			value := gjson.Get(data, normalizedPath)
			if !value.Exists() {
				return 0, fmt.Errorf("path not found: %s", normalizedPath)
			}
			exprStr.WriteString(fmt.Sprintf("%f", value.Float()))
		}
	}

	return evalExpr(exprStr.String())
}

// evalExpr evaluates a simple arithmetic expression with + - * / and ()
func evalExpr(expr string) (float64, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return 0, nil
	}
	x, _, err := parseExpr(expr, 0)
	return x, err
}

// isNumericLiteral checks if a string is a numeric literal (int or float)
func isNumericLiteral(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func parseExpr(s string, pos int) (float64, int, error) {
	x, n, err := parseTerm(s, pos)
	if err != nil {
		return 0, pos, err
	}
	for n < len(s) && (s[n] == '+' || s[n] == '-') {
		op := s[n]
		y, m, err := parseTerm(s, n+1)
		if err != nil {
			return 0, m, err
		}
		if op == '+' {
			x += y
		} else {
			x -= y
		}
		n = m
	}
	return x, n, nil
}

func parseTerm(s string, pos int) (float64, int, error) {
	x, n, err := parseFactor(s, pos)
	if err != nil {
		return 0, pos, err
	}
	for n < len(s) && (s[n] == '*' || s[n] == '/') {
		op := s[n]
		y, m, err := parseFactor(s, n+1)
		if err != nil {
			return 0, m, err
		}
		if op == '*' {
			x *= y
		} else {
			if y == 0 {
				return 0, m, fmt.Errorf("division by zero")
			}
			x /= y
		}
		n = m
	}
	return x, n, nil
}

func parseFactor(s string, pos int) (float64, int, error) {
	pos = skipWhitespace(s, pos)
	if pos >= len(s) {
		return 0, pos, fmt.Errorf("unexpected end of expression")
	}

	if s[pos] == '(' {
		x, n, err := parseExpr(s, pos+1)
		if err != nil {
			return 0, n, err
		}
		if n >= len(s) || s[n] != ')' {
			return 0, n, fmt.Errorf("missing closing parenthesis")
		}
		return x, n + 1, nil
	}

	if s[pos] == '-' {
		x, n, err := parseFactor(s, pos+1)
		if err != nil {
			return 0, n, err
		}
		return -x, n, nil
	}

	return parseNumber(s, pos)
}

func skipWhitespace(s string, pos int) int {
	for pos < len(s) && (s[pos] == ' ' || s[pos] == '\t' || s[pos] == '\n' || s[pos] == '\r') {
		pos++
	}
	return pos
}

func parseNumber(s string, pos int) (float64, int, error) {
	start := pos

	// Handle integer part
	if pos < len(s) && s[pos] >= '0' && s[pos] <= '9' {
		for pos < len(s) && s[pos] >= '0' && s[pos] <= '9' {
			pos++
		}
	}

	// Handle decimal part
	if pos < len(s) && s[pos] == '.' {
		pos++
		for pos < len(s) && s[pos] >= '0' && s[pos] <= '9' {
			pos++
		}
	}

	if start == pos {
		return 0, pos, fmt.Errorf("expected number at position %d", pos)
	}

	numStr := s[start:pos]
	var result float64
	_, err := fmt.Sscanf(numStr, "%f", &result)
	if err != nil {
		return 0, pos, err
	}
	return result, pos, nil
}

// parseHeaders parses a JSON string into a map of headers.
func parseHeaders(jsonStr string) (map[string]string, error) {
	if jsonStr == "" {
		return make(map[string]string), nil
	}

	result := make(map[string]string)
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		var kv []string
		if err := json.Unmarshal([]byte(jsonStr), &kv); err != nil {
			return nil, err
		}
		result = make(map[string]string)
		for i := 0; i < len(kv)-1; i += 2 {
			result[kv[i]] = kv[i+1]
		}
	}
	return result, nil
}

// truncateString truncates a string to maxLen characters.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
