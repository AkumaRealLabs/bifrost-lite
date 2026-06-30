package server

import (
	"context"
	"fmt"
	"math"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/plugins/compat"
	"github.com/maximhq/bifrost/plugins/governance"
	"github.com/maximhq/bifrost/plugins/logging"
	"github.com/maximhq/bifrost/plugins/modelcatalogresolver"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
)

// InferPluginTypes determines which interface types a plugin implements
func InferPluginTypes(plugin schemas.BasePlugin) []schemas.PluginType {
	var types []schemas.PluginType
	if _, ok := plugin.(schemas.LLMPlugin); ok {
		types = append(types, schemas.PluginTypeLLM)
	}
	if _, ok := plugin.(schemas.HTTPTransportPlugin); ok {
		types = append(types, schemas.PluginTypeHTTP)
	}
	return types
}

// Single-plugin methods used plugin create/update

// InstantiatePlugin creates a plugin instance but does NOT register it
// Registration is done separately via Config.RegisterPlugin()
func InstantiatePlugin(ctx context.Context, name string, path *string, pluginConfig any, bifrostConfig *lib.Config) (schemas.BasePlugin, error) {
	// Custom plugin (has path)
	if path != nil {
		return loadCustomPlugin(ctx, path, pluginConfig, bifrostConfig)
	}

	// Built-in plugin (by name)
	return loadBuiltinPlugin(ctx, name, pluginConfig, bifrostConfig)
}

// loadBuiltinPlugin instantiates a built-in plugin by name
func loadBuiltinPlugin(ctx context.Context, name string, pluginConfig any, bifrostConfig *lib.Config) (schemas.BasePlugin, error) {
	switch name {
	case logging.PluginName:
		loggingConfig, err := MarshalPluginConfig[logging.Config](pluginConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal logging plugin config: %w", err)
		}
		return logging.Init(ctx, loggingConfig, logger, bifrostConfig.LogsStore,
			bifrostConfig.ModelCatalog)

	case governance.PluginName:
		governanceConfig, err := MarshalPluginConfig[governance.Config](pluginConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal governance plugin config: %w", err)
		}
		inMemoryStore := &GovernanceInMemoryStore{Config: bifrostConfig}
		return governance.Init(ctx, governanceConfig, logger, bifrostConfig.ConfigStore,
			bifrostConfig.GovernanceConfig, bifrostConfig.ModelCatalog,
			inMemoryStore, bifrostConfig.LogsStore)

	case compat.PluginName:
		compatConfig, err := MarshalPluginConfig[compat.Config](pluginConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal compat plugin config: %w", err)
		}
		return compat.Init(*compatConfig, logger, bifrostConfig.ModelCatalog)

	case modelcatalogresolver.PluginName:
		return modelcatalogresolver.Init(bifrostConfig.ModelCatalog, logger)

	default:
		return nil, fmt.Errorf("unknown built-in plugin: %s", name)
	}
}

// loadCustomPlugin loads a plugin from a shared object file
func loadCustomPlugin(ctx context.Context, path *string, pluginConfig any, bifrostConfig *lib.Config) (schemas.BasePlugin, error) {
	logger.Info("loading custom plugin from path %s", *path)

	plugin, err := bifrostConfig.PluginLoader.LoadPlugin(*path, pluginConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to load custom plugin: %w", err)
	}
	return plugin, nil
}

// LoadPlugins loads the plugins for the server.
func (s *BifrostHTTPServer) LoadPlugins(ctx context.Context) error {
	// Load built-in plugins first (order matters)
	if err := s.loadBuiltinPlugins(ctx); err != nil {
		return err
	}
	// Sort all plugins by placement group and order
	s.Config.SortAndRebuildPlugins()
	return nil
}

// getPluginConfig retrieves a plugin's config from PluginConfigs by name
func (s *BifrostHTTPServer) getPluginConfig(name string) *schemas.PluginConfig {
	for _, cfg := range s.Config.PluginConfigs {
		if cfg.Name == name {
			return cfg
		}
	}
	return nil
}

// loadBuiltinPlugins loads required built-in plugins in specific order
func (s *BifrostHTTPServer) loadBuiltinPlugins(ctx context.Context) error {
	builtinPlacement := schemas.Ptr(schemas.PluginPlacementBuiltin)

	// 1. Logging (if enabled)
	if (s.Config.ClientConfig.EnableLogging == nil || *s.Config.ClientConfig.EnableLogging) && s.Config.LogsStore != nil {
		config := &logging.Config{
			DisableContentLogging: &s.Config.ClientConfig.DisableContentLogging,
			LoggingHeaders:        &s.Config.ClientConfig.LoggingHeaders,
		}
		if s.Config.LogsStoreConfig != nil {
			config.Writer = s.Config.LogsStoreConfig.Writer
		}
		s.registerPluginWithStatus(ctx, logging.PluginName, nil, config, false)
	} else {
		s.markPluginDisabled(logging.PluginName)
	}
	s.Config.SetPluginOrderInfo(logging.PluginName, builtinPlacement, schemas.Ptr(1))

	// 2. Governance keeps virtual keys, provider weighting, fallback policy, and usage.
	if ctx.Value(schemas.BifrostContextKeyIsEnterprise) == nil {
		config := &governance.Config{
			IsVkMandatory:        &s.Config.ClientConfig.EnforceAuthOnInference,
			RequiredHeaders:      &s.Config.ClientConfig.RequiredHeaders,
			RoutingChainMaxDepth: &s.Config.ClientConfig.RoutingChainMaxDepth,
		}
		if s.Config.ClientConfig.TTFBRouting != nil {
			config.TTFBRouting = s.Config.ClientConfig.TTFBRouting
		} else if pluginCfg := s.getPluginConfig(governance.PluginName); pluginCfg != nil && pluginCfg.Config != nil {
			fileConfig, err := MarshalPluginConfig[governance.Config](pluginCfg.Config)
			if err != nil {
				logger.Warn("failed to parse governance plugin config: %v", err)
			} else if fileConfig.TTFBRouting != nil {
				config.TTFBRouting = fileConfig.TTFBRouting
			}
		}
		s.registerPluginWithStatus(ctx, governance.PluginName, nil, config, false)
	} else {
		s.markPluginDisabled(governance.PluginName)
	}
	s.Config.SetPluginOrderInfo(governance.PluginName, builtinPlacement, schemas.Ptr(2))

	// 3. Compat preserves upstream's OpenAI-compatible request conversion behavior.
	cc := s.Config.ClientConfig.Compat
	compatCfg := &compat.Config{
		ConvertTextToChat:      cc.ConvertTextToChat,
		ConvertChatToResponses: cc.ConvertChatToResponses,
		ShouldDropParams:       cc.ShouldDropParams,
		ShouldConvertParams:    cc.ShouldConvertParams,
	}
	s.registerPluginWithStatus(ctx, compat.PluginName, nil, compatCfg, false)
	s.Config.SetPluginOrderInfo(compat.PluginName, builtinPlacement, schemas.Ptr(3))

	// 4. ModelCatalogResolver (last routing layer — fills req.Provider from catalog only when
	// no earlier routing plugin (governance routing rules, governance VK LB, enterprise LB)
	// already set one. CEL rules can still match on provider == "" because this runs last.
	// Requires a model catalog; only register when one is configured.
	if s.Config.ModelCatalog != nil {
		s.registerPluginWithStatus(ctx, modelcatalogresolver.PluginName, nil, nil, false)
	} else {
		s.markPluginDisabled(modelcatalogresolver.PluginName)
	}
	// Place it in post_builtin with a max order so it runs after every other routing plugin,
	// including post_builtin ones like the enterprise load balancer (which would otherwise run
	// after this builtin and never get a chance to pick the provider first).
	s.Config.SetPluginOrderInfo(modelcatalogresolver.PluginName, schemas.Ptr(schemas.PluginPlacementPostBuiltin), schemas.Ptr(math.MaxInt))

	return nil
}

// loadCustomPlugins loads plugins from PluginConfigs
func (s *BifrostHTTPServer) loadCustomPlugins(ctx context.Context) error {
	// Lite physically removes the runtime plugin-management surface. Keep the
	// method as a no-op so stale DB plugin rows cannot load deleted modules.
	return nil
}
