// Package balancechecker provides a self-contained balance query module for OpenAI-compatible providers.
package balancechecker

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	log "github.com/sirupsen/logrus"
)

// Handler handles HTTP requests for balance checking.
type Handler struct {
	configManager *ConfigManager
	checker       *Checker
	authManager   AuthProvider
	config       *config.Config
	mu           sync.RWMutex
}

// AuthProvider defines the interface for getting API keys.
type AuthProvider interface {
	GetAPIKeys() []string
}

// NewHandler creates a new balance checker handler.
func NewHandler(configManager *ConfigManager, authManager AuthProvider) *Handler {
	return &Handler{
		configManager: configManager,
		checker:       NewChecker(),
		authManager:   authManager,
	}
}

// SetConfig sets the main config for API key access.
func (h *Handler) SetConfig(cfg *config.Config) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.config = cfg
}

// GetProviders returns all configured providers.
func (h *Handler) GetProviders(c *gin.Context) {
	cfg := h.configManager.GetConfig()
	c.JSON(http.StatusOK, gin.H{
		"providers": cfg.Providers,
	})
}

// PutProviders saves the provider configurations.
func (h *Handler) PutProviders(c *gin.Context) {
	var cfg BalanceCheckerConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.configManager.UpdateConfig(cfg)
	if err := h.configManager.Save(); err != nil {
		log.Errorf("Failed to save balance checker config: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save configuration"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// QueryBalance queries the balance for a specific provider.
func (h *Handler) QueryBalance(c *gin.Context) {
	providerName := c.Param("provider")
	if providerName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider name is required"})
		return
	}

	// Check if this is a test request with config in body
	var testConfig *BalanceProviderConfig
	if c.Request.ContentLength > 0 {
		var req struct {
			Config *BalanceProviderConfig `json:"config"`
		}
		if err := c.ShouldBindJSON(&req); err == nil && req.Config != nil {
			testConfig = req.Config
		}
	}

	var provider BalanceProviderConfig
	var channelAPIKey string
	var found bool

	if testConfig != nil {
		// Test mode: use the provided config directly
		provider = *testConfig
		found = true
	} else {
		// Normal mode: look up from config
		cfg := h.configManager.GetConfig()

		// First check balance checker config (custom configs)
		for _, p := range cfg.Providers {
			if p.Name == providerName {
				provider = p
				// channelAPIKey will be set from OpenAI compat config below if empty
				found = true
				break
			}
		}

		// Also check OpenAI compatibility config to get channel API key
		h.mu.RLock()
		if h.config != nil {
			for _, openai := range h.config.OpenAICompatibility {
				if openai.Name == providerName {
					// If balance config didn't set channelAPIKey, try to get from OpenAI compat
					if channelAPIKey == "" && len(openai.APIKeyEntries) > 0 {
						channelAPIKey = openai.APIKeyEntries[0].APIKey
					}
					// If provider wasn't found in balance checker config, use OpenAI compat settings
					if !found {
						provider = BalanceProviderConfig{
							Name:    openai.Name,
							Enabled: true,
							Method:  "GET",
							URL:     openai.BaseURL + "/balance",
							Headers: `{"Authorization": "Bearer {api_key}"}`,
						}
						found = true
					}
					break
				}
			}
		}
		h.mu.RUnlock()
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "provider not found"})
		return
	}

	// Use request API key if provided, otherwise fall back to channel API key
	apiKey := c.GetHeader("X-API-Key")
	if apiKey == "" {
		authHeader := c.GetHeader("Authorization")
		if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			apiKey = authHeader[7:]
		}
	}
	if apiKey == "" {
		apiKey = channelAPIKey
	}

	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "API key is required"})
		return
	}

	result := h.checker.QueryBalance(provider, apiKey)
	c.JSON(http.StatusOK, result)
}

// QueryAllBalances queries balances for all enabled providers.
func (h *Handler) QueryAllBalances(c *gin.Context) {
	cfg := h.configManager.GetConfig()

	// Get fallback API key from request
	var fallbackAPIKey string
	fallbackAPIKey = c.GetHeader("X-API-Key")
	if fallbackAPIKey == "" {
		authHeader := c.GetHeader("Authorization")
		if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			fallbackAPIKey = authHeader[7:]
		}
	}

	var results []BalanceResult

	// Query from balance checker config (custom configs)
	for _, provider := range cfg.Providers {
		if provider.Enabled {
			result := h.checker.QueryBalance(provider, fallbackAPIKey)
			results = append(results, result)
		}
	}

	// Query from OpenAI compatibility config (use channel API keys)
	h.mu.RLock()
	if h.config != nil {
		for _, openai := range h.config.OpenAICompatibility {
			provider := BalanceProviderConfig{
				Name:    openai.Name,
				Enabled: true,
				Method:  "GET",
				URL:     openai.BaseURL + "/balance",
				Headers: `{"Authorization": "Bearer {api_key}"}`,
			}
			// Use channel's own API key
			apiKey := fallbackAPIKey
			if len(openai.APIKeyEntries) > 0 {
				apiKey = openai.APIKeyEntries[0].APIKey
			}
			result := h.checker.QueryBalance(provider, apiKey)
			result.Provider = openai.Name // Ensure name is set
			results = append(results, result)
		}
	}
	h.mu.RUnlock()

	c.JSON(http.StatusOK, gin.H{"results": results})
}

// APIKeyInfo represents an API key with its source info.
type APIKeyInfo struct {
	Key  string `json:"key"`
	Type string `json:"type"`
}

// GetAPIKeys returns available API keys from the config.
func (h *Handler) GetAPIKeys(c *gin.Context) {
	var apiKeys []APIKeyInfo

	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.config != nil {
		// Collect API keys from various providers
		for _, key := range h.config.APIKeys {
			apiKeys = append(apiKeys, APIKeyInfo{Key: key, Type: "global"})
		}
		for _, provider := range h.config.OpenAICompatibility {
			for _, entry := range provider.APIKeyEntries {
				apiKeys = append(apiKeys, APIKeyInfo{Key: entry.APIKey, Type: "openai-compat:" + provider.Name})
			}
		}
		for _, key := range h.config.GeminiKey {
			if key.APIKey != "" {
				apiKeys = append(apiKeys, APIKeyInfo{Key: key.APIKey, Type: "gemini"})
			}
		}
		for _, key := range h.config.CodexKey {
			if key.APIKey != "" {
				apiKeys = append(apiKeys, APIKeyInfo{Key: key.APIKey, Type: "codex"})
			}
		}
		for _, key := range h.config.ClaudeKey {
			if key.APIKey != "" {
				apiKeys = append(apiKeys, APIKeyInfo{Key: key.APIKey, Type: "claude"})
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"api_keys": apiKeys})
}

// GetOpenAIProviders returns OpenAI compatible providers from config with their API keys.
func (h *Handler) GetOpenAIProviders(c *gin.Context) {
	var providers []OpenAIProviderInfo

	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.config != nil {
		for _, p := range h.config.OpenAICompatibility {
			apiKeys := make([]string, 0)
			for _, entry := range p.APIKeyEntries {
				if entry.APIKey != "" {
					apiKeys = append(apiKeys, entry.APIKey)
				}
			}
			info := OpenAIProviderInfo{
				Name:       p.Name,
				BaseURL:    p.BaseURL,
				HasAPIKey: len(apiKeys) > 0,
				APIKeys:    apiKeys,
			}
			providers = append(providers, info)
		}
	}

	c.JSON(http.StatusOK, gin.H{"providers": providers})
}

// OpenAIProviderInfo represents an OpenAI compatible provider.
type OpenAIProviderInfo struct {
	Name       string   `json:"name"`
	BaseURL    string   `json:"base_url"`
	HasAPIKey  bool     `json:"has_api_key"`
	APIKeys    []string `json:"api_keys"`
}
