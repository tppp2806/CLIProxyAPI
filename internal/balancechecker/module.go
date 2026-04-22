// Package balancechecker provides a self-contained balance query module for OpenAI-compatible providers.
package balancechecker

import (
	_ "embed"
	"path/filepath"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/api/modules"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	log "github.com/sirupsen/logrus"
)

//go:embed assets/balance-checker.html
var balanceCheckerHTML string

// BalanceCheckerModule implements RouteModuleV2 for the balance checker feature.
type BalanceCheckerModule struct {
	name         string
	registerOnce sync.Once
	handler      *Handler
	configDir    string
}

// New creates a new BalanceCheckerModule instance.
func New(configDir string) *BalanceCheckerModule {
	return &BalanceCheckerModule{
		name:      "balance-checker",
		configDir: configDir,
	}
}

// Name returns the module name.
func (m *BalanceCheckerModule) Name() string {
	return m.name
}

// Register sets up routes and handlers for the balance checker module.
func (m *BalanceCheckerModule) Register(ctx modules.Context) error {
	var regErr error
	m.registerOnce.Do(func() {
		// Initialize config manager
		configManager := NewConfigManager(m.configDir)
		if err := configManager.Load(); err != nil {
			log.Errorf("Failed to load balance checker config: %v", err)
		}

		// Create handler
		m.handler = NewHandler(configManager, nil)
		m.handler.SetConfig(ctx.Config)

		// Register routes - no auth required, API keys come from config
		balance := ctx.Engine.Group("/v0/balance")
		{
			balance.GET("/providers", m.handler.GetProviders)
			balance.PUT("/providers", m.handler.PutProviders)
			balance.POST("/query/:provider", m.handler.QueryBalance)
			balance.POST("/query-all", m.handler.QueryAllBalances)
			balance.GET("/api-keys", m.handler.GetAPIKeys)
			balance.GET("/openai-providers", m.handler.GetOpenAIProviders)
		}

		// Register HTML page (public, no auth required)
		ctx.Engine.GET("/balance-checker.html", func(c *gin.Context) {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(200, balanceCheckerHTML)
		})

		log.Info("Balance checker module registered")
	})
	return regErr
}

// OnConfigUpdated handles configuration updates.
func (m *BalanceCheckerModule) OnConfigUpdated(cfg *config.Config) error {
	// Balance checker uses its own config file, so no action needed for main config updates
	return nil
}

// GetHandler returns the handler for testing purposes.
func (m *BalanceCheckerModule) GetHandler() *Handler {
	return m.handler
}

// GetConfigManager returns the config manager.
func (m *BalanceCheckerModule) GetConfigManager() *ConfigManager {
	if m.handler != nil {
		return m.handler.configManager
	}
	return nil
}

// ResolveConfigDir resolves the configuration directory path.
func ResolveConfigDir(configFilePath string) string {
	if configFilePath == "" {
		return "."
	}
	return filepath.Dir(configFilePath)
}
