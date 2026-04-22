// Package balancechecker provides a self-contained balance query module for OpenAI-compatible providers.
package balancechecker

import (
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// BalanceProviderConfig defines the configuration for a single provider's balance query.
type BalanceProviderConfig struct {
	// Name is the identifier for this provider.
	Name string `yaml:"name" json:"name"`

	// Enabled indicates whether this provider's balance check is active.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Method is the HTTP method (GET or POST).
	Method string `yaml:"method" json:"method"`

	// URL is the endpoint to query for balance information.
	URL string `yaml:"url" json:"url"`

	// Headers is a JSON string containing HTTP headers.
	// Supports {api_key} placeholder which will be replaced with the actual API key.
	Headers string `yaml:"headers" json:"headers"`

	// Body is the request body for POST requests.
	Body string `yaml:"body" json:"body"`

	// BalancePath is a gjson path to extract the balance value from the response.
	BalancePath string `yaml:"balance_path" json:"balance_path"`

	// BalanceType specifies the type of the balance value ("float" or "int").
	BalanceType string `yaml:"balance_type" json:"balance_type"`

	// BalanceMultiplier is applied to the extracted balance value.
	BalanceMultiplier float64 `yaml:"balance_multiplier" json:"balance_multiplier"`

	// UsedBalancePath is a gjson path to extract the used balance value.
	// Supports subtraction syntax: "path1-path2" to calculate difference.
	UsedBalancePath string `yaml:"used_balance_path" json:"used_balance_path"`

	// ResetCyclePath is a gjson path to extract the reset cycle timestamp.
	ResetCyclePath string `yaml:"reset_cycle_path" json:"reset_cycle_path"`

	// EnableProxy controls whether to use proxy for this request.
	EnableProxy bool `yaml:"enable_proxy" json:"enable_proxy"`
}

// BalanceCheckerConfig holds all provider configurations.
type BalanceCheckerConfig struct {
	Providers []BalanceProviderConfig `yaml:"providers" json:"providers"`
}

// ConfigManager manages the balance checker configuration with file persistence.
type ConfigManager struct {
	configPath string
	config     *BalanceCheckerConfig
	mu         sync.RWMutex
}

// NewConfigManager creates a new configuration manager.
func NewConfigManager(configDir string) *ConfigManager {
	configPath := filepath.Join(configDir, "balance-checker.yaml")
	return &ConfigManager{
		configPath: configPath,
		config:     &BalanceCheckerConfig{},
	}
}

// Load reads the configuration from disk.
func (cm *ConfigManager) Load() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	data, err := os.ReadFile(cm.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			cm.config = &BalanceCheckerConfig{Providers: []BalanceProviderConfig{}}
			return nil
		}
		return err
	}

	var cfg BalanceCheckerConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return err
	}

	cm.config = &cfg
	return nil
}

// Save writes the configuration to disk.
func (cm *ConfigManager) Save() error {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	data, err := yaml.Marshal(cm.config)
	if err != nil {
		return err
	}

	return os.WriteFile(cm.configPath, data, 0644)
}

// GetConfig returns a copy of the current configuration.
func (cm *ConfigManager) GetConfig() BalanceCheckerConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	cfg := BalanceCheckerConfig{
		Providers: make([]BalanceProviderConfig, len(cm.config.Providers)),
	}
	copy(cfg.Providers, cm.config.Providers)
	return cfg
}

// UpdateConfig replaces the configuration with the provided one.
func (cm *ConfigManager) UpdateConfig(cfg BalanceCheckerConfig) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.config = &cfg
}
