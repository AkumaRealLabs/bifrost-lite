// Package governance provides comprehensive governance plugin for Bifrost
package governance

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/framework/modelcatalog"
	"github.com/maximhq/bifrost/plugins/governance/complexity"
)

// PluginName is the name of the governance plugin
const PluginName = "governance"

const (
	governanceRejectedContextKey schemas.BifrostContextKey = "bf-governance-rejected"

	VirtualKeyPrefix = "sk-bf-"

	accountConcurrencyCooldownReason  = "account_concurrency_limit"
	accountConcurrencyCooldownSeconds = 30
)

// Config is the configuration for the governance plugin
type Config struct {
	IsVkMandatory         *bool                              `json:"is_vk_mandatory"`
	RequiredHeaders       *[]string                          `json:"required_headers"` // Pointer to live config slice; changes are reflected immediately without restart
	IsEnterprise          bool                               `json:"is_enterprise"`
	DisableAutoToolInject *bool                              `json:"disable_auto_tool_inject"`
	RoutingChainMaxDepth  *int                               `json:"routing_chain_max_depth"` // Pointer to live config value; changes are reflected immediately without restart
	TTFBRouting           *TTFBRoutingConfig                 `json:"ttfb_routing"`
	ProviderScoring       *configstore.ProviderScoringConfig `json:"provider_scoring"`
}

type TTFBRoutingConfig = configstore.TTFBRoutingConfig

type TTFBStatsProvider interface {
	GetTTFBStats(ctx context.Context, filters logstore.SearchFilters, window time.Duration, minSamples int) (*logstore.TTFBStatsResult, error)
}

type weightedProviderConfig struct {
	config          configstoreTables.TableVirtualKeyProviderConfig
	originalWeight  float64
	effectiveWeight float64
	penaltyFactor   float64
}

func normalizeTTFBRoutingConfig(config *TTFBRoutingConfig) TTFBRoutingConfig {
	resolved := TTFBRoutingConfig{
		Enabled: false,
	}
	windowSeconds := 900
	minSamples := 20
	thresholdMs := 2500.0
	minPenaltyFactor := 0.2
	if config != nil {
		resolved.Enabled = config.Enabled
		if config.WindowSeconds != nil && *config.WindowSeconds > 0 {
			windowSeconds = *config.WindowSeconds
		}
		if config.MinSamples != nil && *config.MinSamples > 0 {
			minSamples = *config.MinSamples
		}
		if config.ThresholdMs != nil && *config.ThresholdMs > 0 {
			thresholdMs = *config.ThresholdMs
		}
		if config.MinPenaltyFactor != nil && *config.MinPenaltyFactor > 0 && *config.MinPenaltyFactor <= 1 {
			minPenaltyFactor = *config.MinPenaltyFactor
		}
	}
	resolved.WindowSeconds = &windowSeconds
	resolved.MinSamples = &minSamples
	resolved.ThresholdMs = &thresholdMs
	resolved.MinPenaltyFactor = &minPenaltyFactor
	return resolved
}

type InMemoryStore interface {
	GetConfiguredProviders() map[schemas.ModelProvider]configstore.ProviderConfig
}

type BaseGovernancePlugin interface {
	GetName() string
	EvaluateGovernanceRequest(ctx *schemas.BifrostContext, evaluationRequest *EvaluationRequest, requestType schemas.RequestType) (*EvaluationResult, *schemas.BifrostError)
	HTTPTransportPreHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest) (*schemas.HTTPResponse, error)
	HTTPTransportPostHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest, resp *schemas.HTTPResponse) error
	PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error)
	PostLLMHook(ctx *schemas.BifrostContext, result *schemas.BifrostResponse, err *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error)
	Cleanup() error
	GetGovernanceStore() GovernanceStore
}

// GovernancePlugin implements the main governance plugin with hierarchical budget system
type GovernancePlugin struct {
	ctx         context.Context
	cancelFunc  context.CancelFunc
	wg          sync.WaitGroup // Track active goroutines
	cleanupOnce sync.Once      // Ensure cleanup happens only once

	// Core components with clear separation of concerns
	store    GovernanceStore // Pure data access layer
	resolver *BudgetResolver // Pure decision engine for hierarchical governance
	tracker  *UsageTracker   // Business logic owner (updates, resets, persistence)
	engine   *RoutingEngine  // Routing engine for dynamic routing

	// Dependencies
	configStore  configstore.ConfigStore
	modelCatalog *modelcatalog.ModelCatalog
	logger       schemas.Logger

	// Transport dependencies
	inMemoryStore         InMemoryStore
	ttfbStats             TTFBStatsProvider
	reliabilityStats      ProviderReliabilityStatsProvider
	providerPriceOverride func(providerName string) (float64, bool) // tests only
	testCooldownGet       func(context.Context, time.Time) ([]configstore.ProviderCooldownState, error)
	testCooldownUpsert    func(context.Context, configstore.ProviderCooldownState) error

	cfgMutex sync.RWMutex

	isVkMandatory         *bool
	requiredHeaders       *[]string // pointer to live config slice; lowercased at check time
	isEnterprise          bool
	disableAutoToolInject *bool
	ttfbRouting           TTFBRoutingConfig
	providerScoring       providerScoringConfig

	complexityAnalyzer atomic.Pointer[complexity.ComplexityAnalyzer]
}

// Init initializes and returns a governance plugin instance.
//
// It wires the core components (store, resolver, tracker), performs a best-effort
// startup reset of expired limits when a persistent `configstore.ConfigStore` is
// provided, and establishes a cancellable plugin context used by background work.
//
// Behavior and defaults:
//   - Enables all governance features with optimized defaults.
//   - If `configStore` is nil, the plugin will use an in-memory LocalGovernanceStore
//     (no persistence). Init constructs a LocalGovernanceStore internally when
//     configStore is nil.
//   - If `modelCatalog` is nil, cost calculation is skipped.
//   - `config.IsVkMandatory` controls whether `x-bf-vk` is required in PreLLMHook.
//   - `inMemoryStore` is used by TransportInterceptor to validate configured providers
//     and build provider-prefixed models; it may be nil. When nil, transport-level
//     provider validation/routing is skipped and existing model strings are left
//     unchanged. This is safe and recommended when using the plugin directly from
//     the Go SDK without the HTTP transport.
//
// Parameters:
//   - ctx: base context for the plugin; a child context with cancel is created.
//   - config: plugin flags; may be nil.
//   - logger: logger used by all subcomponents.
//   - configStore: configuration store used for persistence; may be nil.
//   - governanceConfig: initial/seed governance configuration for the store.
//   - modelCatalog: optional model catalog to compute request cost.
//   - inMemoryStore: provider registry used for routing/validation in transports.
//
// Returns:
//   - *GovernancePlugin on success.
//   - error if the governance store fails to initialize.
//
// Side effects:
//   - Logs warnings when optional dependencies are missing.
//   - May perform startup resets via the usage tracker when `configStore` is non-nil.
//
// Alternative entry point:
//   - Use InitFromStore to inject a custom GovernanceStore implementation instead
//     of constructing a LocalGovernanceStore internally.
func Init(
	ctx context.Context,
	config *Config,
	logger schemas.Logger,
	configStore configstore.ConfigStore,
	governanceConfig *configstore.GovernanceConfig,
	modelCatalog *modelcatalog.ModelCatalog,
	inMemoryStore InMemoryStore,
	ttfbStatsProviders ...TTFBStatsProvider,
) (*GovernancePlugin, error) {
	if configStore == nil {
		logger.Warn("governance plugin requires config store to persist data, running in memory only mode")
	}
	if modelCatalog == nil {
		logger.Warn("governance plugin requires model catalog to calculate cost, all LLM cost calculations will be skipped.")
	}
	var ttfbStats TTFBStatsProvider
	var reliabilityStats ProviderReliabilityStatsProvider
	if len(ttfbStatsProviders) > 0 {
		ttfbStats = ttfbStatsProviders[0]
		if rs, ok := ttfbStats.(ProviderReliabilityStatsProvider); ok {
			reliabilityStats = rs
		}
	}

	// Handle nil config - use safe defaults
	var isVkMandatory *bool
	var requiredHeaders *[]string
	var disableAutoToolInject *bool
	var routingChainMaxDepth *int
	if config != nil {
		isVkMandatory = config.IsVkMandatory
		requiredHeaders = config.RequiredHeaders
		disableAutoToolInject = config.DisableAutoToolInject
		routingChainMaxDepth = config.RoutingChainMaxDepth
	}
	ttfbRouting := normalizeTTFBRoutingConfig(nil)
	providerScoring := normalizeProviderScoringConfig(nil)
	if config != nil {
		ttfbRouting = normalizeTTFBRoutingConfig(config.TTFBRouting)
		providerScoring = normalizeProviderScoringConfig(config.ProviderScoring)
	}
	if routingChainMaxDepth == nil {
		defaultDepth := DefaultRoutingChainMaxDepth
		routingChainMaxDepth = &defaultDepth
	}

	newStoreStart := time.Now()
	governanceStore, err := NewLocalGovernanceStore(ctx, logger, configStore, governanceConfig, modelCatalog)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize governance store: %w", err)
	}
	logger.Info("[startup-timing] NewLocalGovernanceStore took %v", time.Since(newStoreStart))
	// Initialize components in dependency order with fixed, optimal settings
	// Resolver (pure decision engine for hierarchical governance, depends only on store)
	resolver := NewBudgetResolver(governanceStore, modelCatalog, logger, inMemoryStore)

	// 3. Tracker (business logic owner, depends on store and resolver)
	tracker := NewUsageTracker(ctx, governanceStore, resolver, configStore, logger)

	// 4. Perform startup reset check for any expired limits from downtime
	// Use distributed lock to prevent race condition when multiple instances boot simultaneously
	if configStore != nil {
		lockManager := configstore.NewDistributedLockManager(configStore, logger, configstore.WithDefaultTTL(30*time.Second))
		lock, err := lockManager.NewLock("governance_startup_reset")
		if err != nil {
			logger.Warn("failed to create governance startup reset lock: %v", err)
		} else {
			// Acquire the lock
			lockAcquired := true
			lockWaitStart := time.Now()
			if err := lock.LockWithRetry(ctx, 10); err != nil {
				logger.Warn("failed to acquire governance startup reset lock, skipping startup reset: %v", err)
				lockAcquired = false
			}
			logger.Info("[startup-timing] governance_startup_reset lock acquisition took %v (acquired=%t)", time.Since(lockWaitStart), lockAcquired)
			// Only run startup resets if we successfully acquired the lock
			if lockAcquired {
				defer func() {
					if err := lock.Unlock(ctx); err != nil && !errors.Is(err, configstore.ErrLockNotHeld) {
						logger.Warn("failed to release governance startup reset lock: %v", err)
					}
				}()
				resetStart := time.Now()
				if err := tracker.PerformStartupResets(ctx); err != nil {
					logger.Warn("startup reset failed: %v", err)
					// Continue initialization even if startup reset fails (non-critical)
				}
				logger.Info("[startup-timing] PerformStartupResets took %v", time.Since(resetStart))
			}
		}
	}

	// 5. Routing engine (dynamically routing requests based on routing rules)
	engine, err := NewRoutingEngine(governanceStore, logger, routingChainMaxDepth)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize routing engine: %w", err)
	}

	ctx, cancelFunc := context.WithCancel(ctx)
	plugin := &GovernancePlugin{
		ctx:                   ctx,
		cancelFunc:            cancelFunc,
		store:                 governanceStore,
		resolver:              resolver,
		tracker:               tracker,
		engine:                engine,
		configStore:           configStore,
		modelCatalog:          modelCatalog,
		logger:                logger,
		isVkMandatory:         isVkMandatory,
		cfgMutex:              sync.RWMutex{},
		requiredHeaders:       requiredHeaders,
		isEnterprise:          config != nil && config.IsEnterprise,
		disableAutoToolInject: disableAutoToolInject,
		inMemoryStore:         inMemoryStore,
		ttfbStats:             ttfbStats,
		reliabilityStats:      reliabilityStats,
		ttfbRouting:           ttfbRouting,
		providerScoring:       providerScoring,
	}
	plugin.storeComplexityAnalyzerConfig(resolveAnalyzerConfigFromStoreOrArg(ctx, logger, configStore, governanceConfig))
	return plugin, nil
}

// InitFromStore initializes and returns a governance plugin instance with a custom store.
//
// This constructor allows providing a custom GovernanceStore implementation instead of
// creating a new LocalGovernanceStore. Use this when you need to:
//   - Inject a custom store implementation for testing
//   - Use a pre-configured store instance
//   - Integrate with non-standard storage backends
//
// Parameters are the same as Init, except governanceConfig is replaced by governanceStore.
// The governanceStore must not be nil, or an error is returned.
//
// See Init documentation for details on other parameters and behavior.
func InitFromStore(
	ctx context.Context,
	config *Config,
	logger schemas.Logger,
	governanceStore GovernanceStore,
	configStore configstore.ConfigStore,
	modelCatalog *modelcatalog.ModelCatalog,
	inMemoryStore InMemoryStore,
	ttfbStatsProviders ...TTFBStatsProvider,
) (*GovernancePlugin, error) {
	if configStore == nil {
		logger.Warn("governance plugin requires config store to persist data, running in memory only mode")
	}
	if modelCatalog == nil {
		logger.Warn("governance plugin requires model catalog to calculate cost, all cost calculations will be skipped.")
	}
	if governanceStore == nil {
		return nil, fmt.Errorf("governance store is nil")
	}
	var ttfbStats TTFBStatsProvider
	var reliabilityStats ProviderReliabilityStatsProvider
	if len(ttfbStatsProviders) > 0 {
		ttfbStats = ttfbStatsProviders[0]
		if rs, ok := ttfbStats.(ProviderReliabilityStatsProvider); ok {
			reliabilityStats = rs
		}
	}
	// Handle nil config - use safe defaults
	var isVkMandatory *bool
	var requiredHeaders *[]string
	var disableAutoToolInject *bool
	var routingChainMaxDepth *int
	if config != nil {
		isVkMandatory = config.IsVkMandatory
		requiredHeaders = config.RequiredHeaders
		disableAutoToolInject = config.DisableAutoToolInject
		routingChainMaxDepth = config.RoutingChainMaxDepth
	}
	ttfbRouting := normalizeTTFBRoutingConfig(nil)
	providerScoring := normalizeProviderScoringConfig(nil)
	if config != nil {
		ttfbRouting = normalizeTTFBRoutingConfig(config.TTFBRouting)
		providerScoring = normalizeProviderScoringConfig(config.ProviderScoring)
	}
	if routingChainMaxDepth == nil {
		defaultDepth := DefaultRoutingChainMaxDepth
		routingChainMaxDepth = &defaultDepth
	}
	resolver := NewBudgetResolver(governanceStore, modelCatalog, logger, inMemoryStore)
	tracker := NewUsageTracker(ctx, governanceStore, resolver, configStore, logger)
	engine, err := NewRoutingEngine(governanceStore, logger, routingChainMaxDepth)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize routing engine: %w", err)
	}
	// Perform startup reset check for any expired limits from downtime
	// Use distributed lock to prevent race condition when multiple instances boot simultaneously
	if configStore != nil {
		lockManager := configstore.NewDistributedLockManager(configStore, logger, configstore.WithDefaultTTL(30*time.Second))
		lock, err := lockManager.NewLock("governance_startup_reset")
		if err != nil {
			logger.Warn("failed to create governance startup reset lock: %v", err)
		} else if err := lock.Lock(ctx); err != nil {
			logger.Warn("failed to acquire governance startup reset lock, skipping startup reset: %v", err)
		} else {
			defer lock.Unlock(ctx)
			if err := tracker.PerformStartupResets(ctx); err != nil {
				logger.Warn("startup reset failed: %v", err)
				// Continue initialization even if startup reset fails (non-critical)
			}
		}
	}
	ctx, cancelFunc := context.WithCancel(ctx)
	plugin := &GovernancePlugin{
		ctx:                   ctx,
		cancelFunc:            cancelFunc,
		store:                 governanceStore,
		resolver:              resolver,
		tracker:               tracker,
		engine:                engine,
		configStore:           configStore,
		modelCatalog:          modelCatalog,
		logger:                logger,
		inMemoryStore:         inMemoryStore,
		isVkMandatory:         isVkMandatory,
		cfgMutex:              sync.RWMutex{},
		requiredHeaders:       requiredHeaders,
		isEnterprise:          config != nil && config.IsEnterprise,
		disableAutoToolInject: disableAutoToolInject,
		ttfbStats:             ttfbStats,
		reliabilityStats:      reliabilityStats,
		ttfbRouting:           ttfbRouting,
		providerScoring:       providerScoring,
	}
	plugin.storeComplexityAnalyzerConfig(resolveAnalyzerConfigFromStoreOrArg(ctx, logger, configStore, nil))
	return plugin, nil
}

// GetName returns the name of the plugin
func (p *GovernancePlugin) GetName() string {
	return PluginName
}

// ReloadComplexityAnalyzerConfig swaps the analyzer used by complexity_tier routing.
func (p *GovernancePlugin) ReloadComplexityAnalyzerConfig(config *complexity.AnalyzerConfig) {
	p.storeComplexityAnalyzerConfig(config)
}

func (p *GovernancePlugin) storeComplexityAnalyzerConfig(config *complexity.AnalyzerConfig) {
	resolved, err := complexity.ValidateAndNormalize(config)
	if err != nil {
		if p.logger != nil {
			p.logger.Warn("invalid complexity analyzer config, using defaults: %v", err)
		}
		defaults := complexity.DefaultAnalyzerConfig()
		resolved = &defaults
	}
	p.complexityAnalyzer.Store(complexity.NewComplexityAnalyzerWithConfig(resolved))
}

func resolveAnalyzerConfigFromStoreOrArg(
	ctx context.Context,
	logger schemas.Logger,
	configStore configstore.ConfigStore,
	governanceConfig *configstore.GovernanceConfig,
) *complexity.AnalyzerConfig {
	if governanceConfig != nil && governanceConfig.ComplexityAnalyzerConfig != nil {
		cfg, err := complexity.ValidateAndNormalize(governanceConfig.ComplexityAnalyzerConfig)
		if err != nil {
			if logger != nil {
				logger.Warn("invalid complexity analyzer config from governance config: %v", err)
			}
		} else if cfg != nil {
			return cfg
		}
	}
	if configStore != nil {
		cfg, err := configStore.GetComplexityAnalyzerConfig(ctx)
		if err != nil {
			if logger != nil {
				logger.Warn("failed to load complexity analyzer config from store: %v", err)
			}
		} else if cfg != nil {
			return cfg
		}
	}
	return nil
}

// UpdateEnforceAuthOnInference updates the enforce auth on inference config
func (p *GovernancePlugin) UpdateEnforceAuthOnInference(enforceAuthOnInference bool) {
	p.cfgMutex.Lock()
	defer p.cfgMutex.Unlock()
	p.isVkMandatory = new(enforceAuthOnInference)
}

// HTTPTransportPreHook is retained as a no-op so governance still satisfies the
// HTTPTransportPlugin interface (used by the enterprise wrapper's 503 gate delegation).
// All routing now flows through PreRequestHook: body-having requests via handleRequest,
// large-payload requests via PreRequestHook reading LargePayloadMetadata.
func (p *GovernancePlugin) HTTPTransportPreHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest) (*schemas.HTTPResponse, error) {
	return nil, nil
}

// runPreRequestRouting wraps a model string in a synthetic BifrostRequest, runs the same
// applyRoutingRules + loadBalanceProvider helpers used by the main PreRequestHook path, and
// returns the resolved model (provider-prefixed when a provider was selected, plain model
// otherwise). Used by PreRequestHook's large-payload branch where req.Model is empty because
// the body wasn't parsed.
func (p *GovernancePlugin) runPreRequestRouting(ctx *schemas.BifrostContext, virtualKey *configstoreTables.TableVirtualKey, hasRoutingRules bool, modelIn string, requestType schemas.RequestType) (string, error) {
	// Parse a provider-prefixed model string the same way the transport does for
	// body-having requests, so an explicit prefix like "openai/gpt-4o" lands in
	// ChatRequest.Provider and load balancing honors the caller's routing intent.
	providerIn, parsedModel := schemas.ParseModelString(modelIn, "")
	synthetic := &schemas.BifrostRequest{
		RequestType: requestType,
		ChatRequest: &schemas.BifrostChatRequest{Provider: providerIn, Model: parsedModel},
	}

	if hasRoutingRules {
		if _, err := p.applyRoutingRules(ctx, synthetic, virtualKey); err != nil {
			return modelIn, err
		}
	}

	if virtualKey != nil {
		if err := p.loadBalanceProvider(ctx, synthetic, virtualKey); err != nil {
			return modelIn, err
		}
	}

	provider, model, _ := synthetic.GetRequestFields()
	if provider != "" {
		return string(provider) + "/" + model, nil
	}
	return model, nil
}

// HTTPTransportPostHook intercepts requests after they are processed (governance decision point)
// It modifies the response in-place and returns nil to continue
func (p *GovernancePlugin) HTTPTransportPostHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest, resp *schemas.HTTPResponse) error {
	return nil
}

// HTTPTransportStreamChunkHook passes through streaming chunks unchanged
func (p *GovernancePlugin) HTTPTransportStreamChunkHook(ctx *schemas.BifrostContext, req *schemas.HTTPRequest, chunk *schemas.BifrostStreamChunk) (*schemas.BifrostStreamChunk, error) {
	return chunk, nil
}

// loadBalanceProvider picks a weighted provider from the VK's configs for req.Model
// and mutates req.Provider/req.Model with the refined provider/model. Also populates req.Fallbacks
// from the remaining weighted providers if no fallbacks were configured by the caller.
func (p *GovernancePlugin) loadBalanceProvider(ctx *schemas.BifrostContext, req *schemas.BifrostRequest, virtualKey *configstoreTables.TableVirtualKey) error {
	provider, modelStr, existingFallbacks := req.GetRequestFields()
	if modelStr == "" {
		return nil
	}

	if provider != "" {
		ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelInfo, fmt.Sprintf("Skipping load balancing for model %s: provider %s already set", modelStr, provider))
		return nil
	}

	ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelInfo, fmt.Sprintf("Load balancing provider for model %s", modelStr))

	// Get provider configs for this virtual key
	providerConfigs := virtualKey.ProviderConfigs
	if len(providerConfigs) == 0 {
		ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelWarn, fmt.Sprintf("No provider configs on virtual key %s for model %s, skipping load balancing", virtualKey.Name, modelStr))
		// No provider configs, continue without modification
		return nil
	}

	var configuredProviders []string
	for _, pc := range providerConfigs {
		configuredProviders = append(configuredProviders, pc.Provider)
	}
	p.logger.Debug("[Governance] Virtual key has %d provider configs: %v", len(providerConfigs), configuredProviders)
	ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelInfo, fmt.Sprintf("Load balancing model %s across %d configured providers: %v", modelStr, len(providerConfigs), configuredProviders))

	// Pre-pass: if any config for a provider blacklists the model, that provider is fully blocked.
	blacklistedProviders := make(map[string]bool)
	for _, config := range providerConfigs {
		if config.BlacklistedModels.IsBlocked(modelStr) {
			blacklistedProviders[config.Provider] = true
		}
	}

	allowedProviderConfigs := make([]configstoreTables.TableVirtualKeyProviderConfig, 0)
	for _, config := range providerConfigs {
		// Blacklist check wins over allowlist (same as provider-key enforcement)
		if blacklistedProviders[config.Provider] {
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelInfo, fmt.Sprintf("Provider %s excluded: model %s is blacklisted", config.Provider, modelStr))
			continue
		}

		// Delegate model allowance check to model catalog
		// This handles all cross-provider logic (OpenRouter, Vertex, Groq, Bedrock)
		// and provider-prefixed allowed_models entries
		isProviderAllowed := false
		if p.modelCatalog != nil && p.inMemoryStore != nil {
			provider := schemas.ModelProvider(config.Provider)
			providerConfig, ok := p.inMemoryStore.GetConfiguredProviders()[provider]
			providerConfigPtr := &providerConfig
			if !ok {
				providerConfigPtr = nil
			}
			isProviderAllowed = p.modelCatalog.IsModelAllowedForProvider(provider, modelStr, providerConfigPtr, config.AllowedModels)
		} else {
			// Fallback when model catalog is not available: simple string matching
			// ["*"] = allow all models; [] = deny all models
			isProviderAllowed = config.AllowedModels.IsAllowed(modelStr)
		}

		if isProviderAllowed {
			// Check if the provider's budget or rate limits are violated using resolver helper methods
			if p.resolver.isProviderBudgetViolated(ctx, virtualKey, config) {
				ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelInfo, fmt.Sprintf("Provider %s excluded: budget limit violated", config.Provider))
				continue
			}
			if p.resolver.isProviderRateLimitViolated(ctx, virtualKey, config) {
				ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelInfo, fmt.Sprintf("Provider %s excluded: rate limit violated", config.Provider))
				continue
			}
			allowedProviderConfigs = append(allowedProviderConfigs, config)
		} else {
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelInfo, fmt.Sprintf("Provider %s excluded: model %s not in allowed models list", config.Provider, modelStr))
		}
	}

	var allowedProviders []string
	for _, pc := range allowedProviderConfigs {
		allowedProviders = append(allowedProviders, pc.Provider)
	}
	p.logger.Debug("[Governance] Allowed providers after filtering: %v", allowedProviders)
	ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelInfo, fmt.Sprintf("Allowed providers after filtering: %v", allowedProviders))

	if len(allowedProviderConfigs) == 0 {
		ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelInfo, fmt.Sprintf("No eligible providers remaining after filtering for model %s, skipping load balancing", modelStr))
		// TODO: Send proper error if (overall VK budget/rate limit) or (all provider budgets/rate limits) are violated
		// No allowed provider configs, continue without modification
		return nil
	}

	weightedConfigs := p.buildEffectiveProviderWeights(ctx, allowedProviderConfigs, virtualKey, modelStr)

	if len(weightedConfigs) == 0 {
		// All allowed configs survived the model-allowance / budget / rate-limit filters,
		// but none of them have a Weight set — there's nothing to feed weighted selection.
		// Emit an explicit log so the routing trail explains why governance stops here
		// instead of trailing off after "Allowed providers after filtering: [...]".
		ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelInfo, fmt.Sprintf("No weighted configs for model %s — none of the allowed VK provider configs have a weight assigned; skipping load balancing", modelStr))
		return nil
	}

	var selectedProvider schemas.ModelProvider
	totalWeight := 0.0
	for _, config := range weightedConfigs {
		totalWeight += config.effectiveWeight
	}
	if totalWeight <= 0 {
		ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelWarn, fmt.Sprintf("All weighted provider configs for model %s have zero effective weight; selecting first configured provider", modelStr))
		totalWeight = weightedConfigs[0].effectiveWeight
		if totalWeight <= 0 {
			totalWeight = 1
			weightedConfigs[0].effectiveWeight = 1
		}
	}
	// Generate random number between 0 and totalWeight
	randomValue := rand.Float64() * totalWeight
	// Select provider based on weighted random selection
	currentWeight := 0.0
	for _, config := range weightedConfigs {
		currentWeight += config.effectiveWeight
		if randomValue <= currentWeight {
			selectedProvider = schemas.ModelProvider(config.config.Provider)
			break
		}
	}
	// Fallback: if no provider was selected (shouldn't happen but guard against FP issues)
	if selectedProvider == "" {
		selectedProvider = schemas.ModelProvider(weightedConfigs[0].config.Provider)
	}

	p.logger.Debug("[governance] Selected provider: %s", selectedProvider)
	ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelInfo, fmt.Sprintf("Selected provider %s for model %s (from %d eligible: %v)", selectedProvider, modelStr, len(allowedProviderConfigs), allowedProviders))

	refinedModel := modelStr
	// Refine the model for the selected provider
	if p.modelCatalog != nil {
		var err error
		refinedModel, err = p.modelCatalog.RefineModelForProvider(selectedProvider, modelStr)
		if err != nil {
			return err
		}
	}

	req.SetProvider(selectedProvider)
	req.SetModel(refinedModel)

	schemas.AppendToContextList(ctx, schemas.BifrostContextKeyRoutingEnginesUsed, schemas.RoutingEngineGovernance)

	if len(existingFallbacks) == 0 && len(weightedConfigs) > 1 {
		fallbackConfigs := append([]weightedProviderConfig(nil), weightedConfigs...)
		sort.Slice(fallbackConfigs, func(i, j int) bool {
			return fallbackConfigs[i].effectiveWeight > fallbackConfigs[j].effectiveWeight
		})

		// Filter out the selected provider and create fallbacks array
		fallbacks := make([]schemas.Fallback, 0, len(fallbackConfigs)-1)
		for _, weightedConfig := range fallbackConfigs {
			config := weightedConfig.config
			if config.Provider == string(selectedProvider) {
				continue
			}
			fbProvider := schemas.ModelProvider(config.Provider)
			fbModel := modelStr
			if p.modelCatalog != nil {
				refined, err := p.modelCatalog.RefineModelForProvider(fbProvider, modelStr)
				if err != nil {
					p.logger.Warn("failed to refine model for fallback, skipping fallback in governance plugin: %v", err)
					ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelWarn, fmt.Sprintf("Fallback provider %s skipped: failed to refine model %s for this provider", fbProvider, modelStr))
					continue
				}
				fbModel = refined
			}
			fallbacks = append(fallbacks, schemas.Fallback{Provider: fbProvider, Model: fbModel})
		}
		req.SetFallbacks(fallbacks)
		ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelInfo, fmt.Sprintf("Added %d fallback providers", len(fallbacks)))
	}

	return nil
}

func (p *GovernancePlugin) buildEffectiveProviderWeights(ctx *schemas.BifrostContext, configs []configstoreTables.TableVirtualKeyProviderConfig, virtualKey *configstoreTables.TableVirtualKey, model string) []weightedProviderConfig {
	weighted := make([]weightedProviderConfig, 0, len(configs))
	for _, config := range configs {
		if config.Weight == nil {
			continue
		}
		original := getWeight(config.Weight)
		weighted = append(weighted, weightedProviderConfig{
			config:          config,
			originalWeight:  original,
			effectiveWeight: original,
			penaltyFactor:   1,
		})
	}

	if len(weighted) == 0 {
		return weighted
	}
	if p.providerScoring.Enabled {
		return p.applyProviderScoring(ctx, configs, virtualKey, model, weighted)
	}
	if !p.ttfbRouting.Enabled {
		return weighted
	}
	if p.ttfbStats == nil {
		ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelWarn, "TTFB routing enabled but logstore stats provider is unavailable; using original provider weights")
		return weighted
	}

	windowSeconds := 900
	if p.ttfbRouting.WindowSeconds != nil && *p.ttfbRouting.WindowSeconds > 0 {
		windowSeconds = *p.ttfbRouting.WindowSeconds
	}
	minSamples := 20
	if p.ttfbRouting.MinSamples != nil && *p.ttfbRouting.MinSamples > 0 {
		minSamples = *p.ttfbRouting.MinSamples
	}
	thresholdMs := 2500.0
	if p.ttfbRouting.ThresholdMs != nil && *p.ttfbRouting.ThresholdMs > 0 {
		thresholdMs = *p.ttfbRouting.ThresholdMs
	}
	minFactor := 0.2
	if p.ttfbRouting.MinPenaltyFactor != nil && *p.ttfbRouting.MinPenaltyFactor > 0 && *p.ttfbRouting.MinPenaltyFactor <= 1 {
		minFactor = *p.ttfbRouting.MinPenaltyFactor
	}

	filters := logstore.SearchFilters{Models: []string{model}}
	if virtualKey != nil && virtualKey.ID != "" {
		filters.VirtualKeyIDs = []string{virtualKey.ID}
	}
	stats, err := p.ttfbStats.GetTTFBStats(ctx, filters, time.Duration(windowSeconds)*time.Second, minSamples)
	if err != nil {
		ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelWarn, fmt.Sprintf("TTFB routing stats unavailable for model %s: %v; using original provider weights", model, err))
		return weighted
	}

	statsByProvider := make(map[string]logstore.TTFBStatsEntry, len(stats.Stats))
	for _, entry := range stats.Stats {
		if entry.Model != model {
			continue
		}
		current, exists := statsByProvider[entry.Provider]
		if !exists || entry.SampleCount > current.SampleCount {
			statsByProvider[entry.Provider] = entry
		}
	}

	for i := range weighted {
		entry, ok := statsByProvider[weighted[i].config.Provider]
		if !ok {
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelInfo, fmt.Sprintf("TTFB routing fallback for provider %s model %s: no TTFB samples; using original weight %.2f", weighted[i].config.Provider, model, weighted[i].originalWeight))
			continue
		}
		if !entry.HasMinSamples {
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelInfo, fmt.Sprintf("TTFB routing fallback for provider %s model %s: %d samples below minimum %d; using original weight %.2f", weighted[i].config.Provider, model, entry.SampleCount, minSamples, weighted[i].originalWeight))
			continue
		}
		if entry.P95TTFBMs <= thresholdMs {
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelInfo, fmt.Sprintf("TTFB routing kept provider %s model %s at original weight %.2f: p95 %.0fms <= %.0fms", weighted[i].config.Provider, model, weighted[i].originalWeight, entry.P95TTFBMs, thresholdMs))
			continue
		}
		factor := thresholdMs / entry.P95TTFBMs
		if factor < minFactor {
			factor = minFactor
		}
		if factor > 1 {
			factor = 1
		}
		weighted[i].penaltyFactor = factor
		weighted[i].effectiveWeight = weighted[i].originalWeight * factor
		ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelInfo, fmt.Sprintf("TTFB routing penalized provider %s model %s: p95 %.0fms > %.0fms, weight %.2f -> %.2f", weighted[i].config.Provider, model, entry.P95TTFBMs, thresholdMs, weighted[i].originalWeight, weighted[i].effectiveWeight))
	}

	return weighted
}

// publishRoutingAllowlist records, for downstream routing layers, which of the VK's configured
// providers permit modelStr according to the VK's own allowed_models / blocked_models. It is a
// coarse provider gate (BifrostContextKeyRoutingAllowedProviders) layered on top of the model
// catalog checks those layers already run — its purpose is to stop a later routing layer (load
// balancing, model-catalog resolution) from selecting a provider the VK forbids for this model,
// even when governance itself couldn't pick one. An empty slice means "no provider is permitted"
// (fail-closed via the empty-provider validation in handleRequest); a nil VK publishes nothing.
//
// Provider prefixes on the request model are already split into req.Provider + bare model at the
// HTTP layer (resolveModelAndProvider), so VK allowed_models / blocked_models are matched against
// bare names and plain membership checks are sufficient here.
func (p *GovernancePlugin) publishRoutingAllowlist(ctx *schemas.BifrostContext, virtualKey *configstoreTables.TableVirtualKey, modelStr string) {
	if virtualKey == nil {
		return
	}
	allowed := make([]schemas.ModelProvider, 0, len(virtualKey.ProviderConfigs))
	for _, pc := range virtualKey.ProviderConfigs {
		// No model to filter on → keep the provider so we don't over-restrict.
		if modelStr == "" ||
			(pc.AllowedModels.IsAllowed(modelStr) && !pc.BlacklistedModels.IsBlocked(modelStr)) {
			allowed = append(allowed, schemas.ModelProvider(pc.Provider))
		}
	}
	ctx.SetValue(schemas.BifrostContextKeyRoutingAllowedProviders, allowed)
}

// applyRoutingRules evaluates routing rules against req and mutates
// req.Provider/req.Model/req.Fallbacks when a rule matches. Returns the matched RoutingDecision
// (nil if no rule matched). Integrations normalize req.Model (and Provider when applicable) before
// the BifrostRequest reaches this point.
func (p *GovernancePlugin) applyRoutingRules(ctx *schemas.BifrostContext, req *schemas.BifrostRequest, virtualKey *configstoreTables.TableVirtualKey) (*RoutingDecision, error) {
	provider, model, _ := req.GetRequestFields()
	if model == "" {
		return nil, nil
	}

	requestType := string(req.RequestType)
	headers, _ := ctx.Value(schemas.BifrostContextKeyRequestHeaders).(map[string]string)
	queryParams, _ := ctx.Value(schemas.BifrostContextKeyRequestQuery).(map[string]string)

	// Set up lazy complexity computation; only runs if a rule references complexity_tier.
	var computeComplexity func() *complexity.ComplexityResult
	if analyzer := p.complexityAnalyzer.Load(); analyzer != nil {
		computeComplexity = func() *complexity.ComplexityResult {
			input, ok := buildComplexityInput(req)
			if !ok {
				if p.logger != nil {
					p.logger.Debug("[Governance] Complexity analysis skipped: unsupported request type")
				}
				ctx.AppendRoutingEngineLog(schemas.RoutingEngineRoutingRule, schemas.LogLevelInfo, "Complexity analysis skipped: no supported text-bearing input detected")
				return nil
			}

			result := analyzer.Analyze(input)
			if p.logger != nil {
				p.logger.Debug(
					"[Governance] Complexity analysis details: tier=%s score=%.2f words=%d",
					result.Tier,
					result.Score,
					result.WordCount,
				)
			}
			ctx.AppendRoutingEngineLog(
				schemas.RoutingEngineRoutingRule,
				schemas.LogLevelInfo,
				fmt.Sprintf("Complexity: tier=%s score=%.2f words=%d", result.Tier, result.Score, result.WordCount),
			)
			return result
		}
	}

	routingCtx := &RoutingContext{
		VirtualKey:               virtualKey,
		Provider:                 provider,
		Model:                    model,
		RequestType:              requestType,
		Headers:                  headers,
		QueryParams:              queryParams,
		BudgetAndRateLimitStatus: p.store.GetBudgetAndRateLimitStatus(ctx, model, provider, virtualKey, nil, nil, nil),
		computeComplexity:        computeComplexity,
	}

	p.logger.Debug("[PreRequestHook] Built routing context: provider=%s, model=%s, requestType=%s, vk=%v",
		provider, model, requestType, virtualKey != nil)

	// Evaluate routing rules
	decision, err := p.engine.EvaluateRoutingRules(ctx, routingCtx)
	if err != nil {
		p.logger.Error("failed to evaluate routing rules: %v", err)
		ctx.AppendRoutingEngineLog(schemas.RoutingEngineRoutingRule, schemas.LogLevelError, fmt.Sprintf("Routing rule evaluation error: %v", err))
		return nil, nil
	}
	if decision == nil {
		return nil, nil
	}

	p.logger.Debug("[Governance] Routing rule matched: %s", decision.MatchedRuleName)

	if decision.Provider != "" {
		req.SetProvider(schemas.ModelProvider(decision.Provider))
	}
	if decision.Model != "" {
		req.SetModel(decision.Model)
	}

	schemas.AppendToContextList(ctx, schemas.BifrostContextKeyRoutingEnginesUsed, schemas.RoutingEngineRoutingRule)

	// Add fallbacks if present; fill in the incoming model for fallbacks that omit it
	if len(decision.Fallbacks) > 0 {
		resolvedFallbacks := make([]schemas.Fallback, 0, len(decision.Fallbacks))
		for _, fb := range decision.Fallbacks {
			fbProvider, fbModel := schemas.ParseModelString(fb, "")
			trimmedFbProvider := strings.TrimSpace(string(fbProvider))
			trimmedFbModel := strings.TrimSpace(fbModel)
			if trimmedFbProvider == "" {
				continue
			}
			if trimmedFbModel == "" && model != "" {
				trimmedFbModel = model
			}
			resolvedFallbacks = append(resolvedFallbacks, schemas.Fallback{
				Provider: schemas.ModelProvider(trimmedFbProvider),
				Model:    trimmedFbModel,
			})
		}
		req.SetFallbacks(resolvedFallbacks)
	}

	// Pin specific API key by ID if the routing rule specifies one. This uses a dedicated,
	// non-reserved context key (not BifrostContextKeyAPIKeyID): routing runs inside
	// PreRequestHook, where core blocks writes to reserved key-selection keys, so a write to
	// the caller-pin key would be silently dropped. Key selection reads this routing pin first
	// and resolves it against the configured key pool.
	if decision.KeyID != "" {
		ctx.SetValue(schemas.BifrostContextKeyRoutingPinnedAPIKeyID, decision.KeyID)
	}

	p.logger.Debug("[Governance] Applied routing decision: provider=%s, model=%s, keyID=%s, fallbacks=%v", decision.Provider, decision.Model, decision.KeyID, decision.Fallbacks)
	return decision, nil
}

// EvaluateGovernanceRequest is a common function that handles virtual key validation
// and governance evaluation logic. It returns the evaluation result and a BifrostError
// if the request should be rejected, or nil if allowed.
//
// Parameters:
//   - ctx: The Bifrost context
//   - evaluationRequest: The evaluation request with VirtualKey, Provider, Model, and RequestID
//
// Returns:
//   - *EvaluationResult: The governance evaluation result
//   - *schemas.BifrostError: The error to return if request is not allowed, nil if allowed
func (p *GovernancePlugin) EvaluateGovernanceRequest(ctx *schemas.BifrostContext, evaluationRequest *EvaluationRequest, requestType schemas.RequestType) (*EvaluationResult, *schemas.BifrostError) {
	// Check if authentication is mandatory (either VK or user auth)
	// Checking if the virtual key is valid or not
	isVirtualKeyValid := false
	if evaluationRequest.VirtualKey != "" {
		_, exists := p.store.GetVirtualKey(ctx, evaluationRequest.VirtualKey)
		if exists {
			isVirtualKeyValid = true
		} else {
			// VK was provided but does not exist in the store — reject regardless of mandatory setting
			return nil, &schemas.BifrostError{
				Type:       new("virtual_key_not_found"),
				StatusCode: new(401),
				Error: &schemas.ErrorField{
					Message: "virtual key not found. The provided virtual key does not exist or has been revoked.",
				},
			}
		}
	}
	p.cfgMutex.RLock()
	if !isVirtualKeyValid && evaluationRequest.UserID == "" && p.isVkMandatory != nil && *p.isVkMandatory {
		message := "virtual key is required. Provide a virtual key via the x-bf-vk header."
		if p.isEnterprise {
			message = "authentication is required. Provide a virtual key (x-bf-vk), API key, or user token."
		}
		p.cfgMutex.RUnlock()
		return nil, &schemas.BifrostError{
			Type:       new("virtual_key_required"),
			StatusCode: new(401),
			Error: &schemas.ErrorField{
				Message: message,
			},
		}
	}
	p.cfgMutex.RUnlock()

	// First evaluate model and provider checks (applies even when virtual keys are disabled or not present)
	result := p.resolver.EvaluateModelAndProviderRequest(ctx, evaluationRequest.Provider, evaluationRequest.Model)

	// The flow for governance checks is:
	//   VK (identity + VK-level budget/rate-limit) -> Customer -> Team -> User
	// VK identity runs FIRST so that revoked, provider-disallowed, or model-disallowed
	// keys are blocked before any hierarchy state is consulted. Running Customer/Team/
	// User ahead of VK would leak topology: a revoked key attached to an over-budget
	// team would return 429 team-budget-exceeded instead of 403 VK-blocked, telling
	// an attacker the key's team structure was validated.

	// Resolve the VK once; it feeds both the VK evaluation and hierarchy-ID extraction.
	var hierarchyVK *configstoreTables.TableVirtualKey
	if evaluationRequest.VirtualKey != "" {
		if vk, ok := p.store.GetVirtualKey(ctx, evaluationRequest.VirtualKey); ok && vk != nil {
			hierarchyVK = vk
		}
	}

	// Read-only metadata calls (e.g. list models) set this flag to skip budget/rate-limit
	// checks while still enforcing VK identity (existence, active status, provider/model filtering).
	skipBudgetsAndRateLimits := bifrost.GetBoolFromContext(ctx, schemas.BifrostContextKeySkipBudgetAndRateLimits)

	// Step 1: Evaluate virtual key (identity + VK-level budget/rate-limit hierarchy).
	// Short-circuits with VirtualKeyBlocked / ProviderBlocked / ModelBlocked before
	// we touch Customer / Team / User.
	if result.Decision == DecisionAllow && evaluationRequest.VirtualKey != "" {
		skipVKBudgetLimit := evaluationRequest.UserID != "" || skipBudgetsAndRateLimits
		result = p.resolver.EvaluateVirtualKeyRequest(ctx, evaluationRequest.VirtualKey, evaluationRequest.Provider, evaluationRequest.Model, requestType, skipVKBudgetLimit)
	}

	// Step 2: Customer-level budget (customer attached directly to VK, or via the VK's team).
	// Fall back to the loaded relation IDs so VKs populated via joins without FK
	// pointer columns still participate in customer-level enforcement.
	if !skipBudgetsAndRateLimits && result.Decision == DecisionAllow && hierarchyVK != nil {
		var customerID string
		customerFromTeam := false
		switch {
		case hierarchyVK.CustomerID != nil:
			customerID = *hierarchyVK.CustomerID
		case hierarchyVK.Customer != nil:
			customerID = hierarchyVK.Customer.ID
		case hierarchyVK.Team != nil && hierarchyVK.Team.CustomerID != nil:
			customerID = *hierarchyVK.Team.CustomerID
			customerFromTeam = true
		case hierarchyVK.Team != nil && hierarchyVK.Team.Customer != nil:
			customerID = hierarchyVK.Team.Customer.ID
			customerFromTeam = true
		}
		// When the request is scoped to a specific customer (header-driven, team-VK
		// path; stamped by the enterprise plugin), skip enforcing the scalar
		// team.CustomerID customer if it is not the scoped one — the enterprise layer
		// enforces the scoped customer instead. Mirrors collectBudgetsFromHierarchy.
		scopedCustomerID, _ := ctx.Value(schemas.BifrostContextKeyGovernanceScopedCustomerID).(string)
		scopedAway := customerFromTeam && scopedCustomerID != "" && scopedCustomerID != customerID
		if customerID != "" && !scopedAway {
			result = p.resolver.EvaluateCustomerRequest(ctx, customerID, evaluationRequest)
		}
	}

	// Step 3: Team-level budget. Fall back to vk.Team.ID when the FK pointer is nil
	// but the relation is populated.
	if !skipBudgetsAndRateLimits && result.Decision == DecisionAllow && hierarchyVK != nil {
		var teamID string
		switch {
		case hierarchyVK.TeamID != nil:
			teamID = *hierarchyVK.TeamID
		case hierarchyVK.Team != nil:
			teamID = hierarchyVK.Team.ID
		}
		if teamID != "" {
			result = p.resolver.EvaluateTeamRequest(ctx, teamID, evaluationRequest)
		}
	}

	// Step 4: User-level governance (enterprise-only).
	if !skipBudgetsAndRateLimits && result.Decision == DecisionAllow {
		result = p.resolver.EvaluateUserRequest(ctx, evaluationRequest.UserID, evaluationRequest)
	}

	// Mark request as rejected in context if not allowed
	if result.Decision != DecisionAllow {
		if ctx != nil {
			if _, ok := ctx.Value(governanceRejectedContextKey).(bool); !ok {
				ctx.SetValue(governanceRejectedContextKey, true)
			}
		}
	}

	// Handle decision
	switch result.Decision {
	case DecisionAllow:
		// Clear any prior rejection flag (e.g. from a failed primary attempt
		// before a fallback retry). Without this, PostLLMHook would see the
		// stale flag and skip budget/rate-limit ID collection for the
		// successful fallback attempt.
		if ctx != nil {
			ctx.ClearValue(governanceRejectedContextKey)
		}
		return result, nil

	case DecisionVirtualKeyNotFound, DecisionVirtualKeyBlocked, DecisionModelBlocked, DecisionProviderBlocked:
		return result, &schemas.BifrostError{
			Type:       new(string(result.Decision)),
			StatusCode: new(403),
			Error: &schemas.ErrorField{
				Message: result.Reason,
			},
		}

	case DecisionRateLimited, DecisionTokenLimited, DecisionRequestLimited:
		return result, &schemas.BifrostError{
			Type:       new(string(result.Decision)),
			StatusCode: new(429),
			Error: &schemas.ErrorField{
				Message: result.Reason,
			},
		}

	case DecisionBudgetExceeded:
		return result, &schemas.BifrostError{
			Type:       new(string(result.Decision)),
			StatusCode: new(402),
			Error: &schemas.ErrorField{
				Message: result.Reason,
			},
		}

	default:
		// Fallback to deny for unknown decisions
		return result, &schemas.BifrostError{
			Type: new(string(result.Decision)),
			Error: &schemas.ErrorField{
				Message: "Governance decision error",
			},
		}
	}
}

// PreRequestHook is the per-request governance phase. It runs for both normal body-having
// requests (route on req.Model) and large-payload streaming requests (route on
// LargePayloadMetadata.Model from ctx — the body is opaque mid-stream, so routing is
// constrained to same-protocol-family targets that the upstream provider can hydrate
// from the rewritten metadata).
//
// Generic streaming bypasses handleRequest (see core/bifrost.go RunStreamPreHooks)
// and is still handled at HTTPTransportPreHook.
func (p *GovernancePlugin) PreRequestHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) error {
	if req.RequestType == schemas.PassthroughRequest || req.RequestType == schemas.PassthroughStreamRequest {
		return nil
	}

	virtualKeyValue := bifrost.GetStringFromContext(ctx, schemas.BifrostContextKeyVirtualKey)
	hasRoutingRules := p.store.HasRoutingRules(ctx)
	if virtualKeyValue == "" && !hasRoutingRules {
		return nil
	}

	var virtualKey *configstoreTables.TableVirtualKey
	if virtualKeyValue != "" {
		var ok bool
		virtualKey, ok = p.store.GetVirtualKey(ctx, virtualKeyValue)
		if !ok || virtualKey == nil || !virtualKey.IsActiveValue() {
			return nil
		}
	}

	stampGovernanceCtxFromVK(ctx, virtualKey)

	// Large-payload mode: the body streams to the provider unparsed, so req.Model is
	// empty for routes where the model lives in the body (OpenAI/Anthropic chat,
	// responses, etc.). Route on LargePayloadMetadata.Model — the provider's
	// streaming body rewriter (ApplyLargePayloadRequestBodyWithModelNormalization)
	// reads metadata.Model when it rewrites the model field in the body prefix, so
	// mutating it here is what propagates the routing decision to the upstream call.
	if metadata, _ := ctx.Value(schemas.BifrostContextKeyLargePayloadMetadata).(*schemas.LargePayloadMetadata); metadata != nil && metadata.Model != "" {
		newModel, err := p.runPreRequestRouting(ctx, virtualKey, hasRoutingRules, metadata.Model, req.RequestType)
		if err != nil {
			return err
		}
		if newModel != "" && newModel != metadata.Model {
			metadata.Model = newModel
		}
		_, routedModel := schemas.ParseModelString(metadata.Model, "")
		p.publishRoutingAllowlist(ctx, virtualKey, routedModel)
		return nil
	}

	if hasRoutingRules {
		if _, err := p.applyRoutingRules(ctx, req, virtualKey); err != nil {
			return err
		}
	}

	// Publish the VK provider allowlist for the (post routing-rules) model so downstream routing
	// layers (load balancing, model-catalog resolution) and core enforcement intersect their
	// candidates with it — a later layer must not select a provider the VK forbids for this model.
	_, routedModel, _ := req.GetRequestFields()
	p.publishRoutingAllowlist(ctx, virtualKey, routedModel)

	if virtualKey != nil {
		if err := p.loadBalanceProvider(ctx, req, virtualKey); err != nil {
			return err
		}
	}

	return nil
}

// PreLLMHook intercepts requests before they are processed (governance decision point)
// Parameters:
//   - ctx: The Bifrost context
//   - req: The Bifrost request to be processed
//
// Returns:
//   - *schemas.BifrostRequest: The processed request
//   - *schemas.LLMPluginShortCircuit: The plugin short circuit if the request is not allowed
//   - error: Any error that occurred during processing
func (p *GovernancePlugin) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	// Validate required headers are present
	if headerErr := p.validateRequiredHeaders(ctx); headerErr != nil {
		return req, &schemas.LLMPluginShortCircuit{Error: headerErr}, nil
	}

	// Extract virtual key using utility functions
	virtualKeyValue := bifrost.GetStringFromContext(ctx, schemas.BifrostContextKeyVirtualKey)

	// Extract user ID for enterprise user-level governance
	userID := bifrost.GetStringFromContext(ctx, schemas.BifrostContextKeyUserID)
	// Getting provider and mode from the request
	provider, model, _ := req.GetRequestFields()
	// Create request context for evaluation
	evaluationRequest := &EvaluationRequest{
		VirtualKey: virtualKeyValue,
		Provider:   provider,
		Model:      model,
		UserID:     userID,
	}
	// Evaluate governance using common function
	_, bifrostError := p.EvaluateGovernanceRequest(ctx, evaluationRequest, req.RequestType)
	// Convert BifrostError to LLMPluginShortCircuit if needed
	if bifrostError != nil {
		return req, &schemas.LLMPluginShortCircuit{
			Error: bifrostError,
		}, nil
	}

	return req, nil, nil
}

// PostLLMHook processes the response and updates usage tracking (business logic execution)
// Parameters:
//   - ctx: The Bifrost context
//   - result: The Bifrost response to be processed
//   - err: The Bifrost error to be processed
//
// Returns:
//   - *schemas.BifrostResponse: The processed response
//   - *schemas.BifrostError: The processed error
//   - error: Any error that occurred during processing
func (p *GovernancePlugin) PostLLMHook(ctx *schemas.BifrostContext, result *schemas.BifrostResponse, err *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	if _, ok := ctx.Value(governanceRejectedContextKey).(bool); ok {
		return result, err, nil
	}

	// Extract request type, provider, and model
	requestType, provider, requestedModel, _ := bifrost.GetResponseFields(result, err)
	p.cooldownProviderOnAccountConcurrencyLimit(ctx, provider, err)

	// Extract governance information
	virtualKey := bifrost.GetStringFromContext(ctx, schemas.BifrostContextKeyVirtualKey)
	requestID := bifrost.GetStringFromContext(ctx, schemas.BifrostContextKeyRequestID)
	// Extract user ID for enterprise user-level governance
	userID := bifrost.GetStringFromContext(ctx, schemas.BifrostContextKeyUserID)

	if requestType == schemas.ListModelsRequest && result != nil && result.ListModelsResponse != nil && virtualKey != "" {
		// filter models which are not supported on this virtual key
		result.ListModelsResponse.Data = p.filterModelsForVirtualKey(ctx, result.ListModelsResponse.Data, virtualKey)
	}

	isFinalChunk := bifrost.IsFinalChunk(ctx)

	// Build pricing scopes from context using the governance VK ID (not the raw VK token)
	pricingScopes := modelcatalog.PricingLookupScopesFromContext(ctx, string(provider))

	// Always process usage tracking. When both virtual key and user are present,
	// track both scopes; callers that intentionally want user-only accounting can
	// set BifrostContextKeySkipVirtualKeyUsageTracking.
	effectiveVK := virtualKey
	if bifrost.GetBoolFromContext(ctx, schemas.BifrostContextKeySkipVirtualKeyUsageTracking) {
		effectiveVK = ""
	}
	// If effectiveVK is empty, it will be passed as empty string to postHookWorker
	// The tracker will handle empty virtual keys gracefully by only updating provider-level and model-level usage
	if requestedModel != "" {
		// Collect the affected budget and rate-limit IDs synchronously (fast in-memory
		// lookups) and attach them to the context. The logging plugin reads these keys
		// when building the log entry, enabling ghost-node usage reconciliation to
		// attribute cost/tokens to the correct governance entities.
		budgetIDs, rateLimitIDs := p.store.CollectApplicableGovernanceIDs(ctx, effectiveVK, userID, provider, requestedModel)
		if len(budgetIDs) > 0 {
			ctx.SetValue(schemas.BifrostContextKeyGovernanceBudgetIDs, budgetIDs)
		}
		if len(rateLimitIDs) > 0 {
			ctx.SetValue(schemas.BifrostContextKeyGovernanceRateLimitIDs, rateLimitIDs)
		}

		// Attempt number distinguishes physical provider calls within one
		// logical request so each token-consuming attempt bills exactly once.
		// Set by core on every retry iteration.
		attemptNumber := bifrost.GetIntFromContext(ctx, schemas.BifrostContextKeyNumberOfRetries)

		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			// Recover so a billing panic (e.g. an unexpected nil deref) can never
			// crash the process and lose in-memory counters.
			defer func() {
				if r := recover(); r != nil {
					p.logger.Error("recovered from panic in governance postHookWorker: %v", r)
				}
			}()
			// Use the requested model for usage tracking
			p.postHookWorker(result, err, provider, requestedModel, requestType, effectiveVK, requestID, userID, isFinalChunk, attemptNumber, pricingScopes)
		}()
	}

	return result, err, nil
}

func (p *GovernancePlugin) cooldownProviderOnAccountConcurrencyLimit(ctx *schemas.BifrostContext, provider schemas.ModelProvider, err *schemas.BifrostError) {
	if ctx == nil || err == nil || err.Error == nil || !isAccountConcurrencyLimitError(err) {
		return
	}
	if provider == "" {
		provider = err.ExtraFields.RoutingInfo.Provider
	}
	if provider == "" {
		return
	}

	now := time.Now().UTC()
	state := configstore.ProviderCooldownState{
		Provider:      string(provider),
		CooldownUntil: now.Add(accountConcurrencyCooldownSeconds * time.Second),
		Reason:        accountConcurrencyCooldownReason,
		WindowSeconds: accountConcurrencyCooldownSeconds,
		UpdatedAt:     now,
	}
	if p.testCooldownUpsert != nil {
		if err := p.testCooldownUpsert(ctx, state); err != nil {
			p.logger.Warn("failed to record provider account concurrency cooldown: %v", err)
		}
		return
	}
	if p.configStore != nil {
		if err := p.configStore.UpsertProviderCooldown(ctx, state); err != nil {
			p.logger.Warn("failed to record provider account concurrency cooldown: %v", err)
		}
	}
}

func isAccountConcurrencyLimitError(err *schemas.BifrostError) bool {
	if err == nil || err.Error == nil {
		return false
	}
	return isAccountConcurrencyLimitText(err.Error.Message) ||
		(err.Error.Type != nil && isAccountConcurrencyLimitText(*err.Error.Type)) ||
		(err.Error.Code != nil && isAccountConcurrencyLimitText(*err.Error.Code))
}

func isAccountConcurrencyLimitText(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "concurrency limit exceeded for account") ||
		strings.Contains(lower, "account_concurrency")
}

// Cleanup shuts down all components gracefully
func (p *GovernancePlugin) Cleanup() error {
	var cleanupErr error
	p.cleanupOnce.Do(func() {
		if p.cancelFunc != nil {
			p.cancelFunc()
		}
		p.wg.Wait() // Wait for all background workers to complete
		if err := p.tracker.Cleanup(); err != nil {
			cleanupErr = err
		}
	})
	return cleanupErr
}

// postHookWorker is a worker function that processes the response and updates usage tracking
// It is used to avoid blocking the main thread when updating usage tracking
// Handles both cases: with virtual key and without virtual key (empty string)
// When virtualKey is empty, the tracker will only update provider-level and model-level usage
// Parameters:
//   - result: The Bifrost response to be processed
//   - provider: The provider of the request
//   - model: The model of the request
//   - requestType: The type of the request
//   - virtualKey: The raw virtual key token of the request (empty string if not present)
//   - selectedKeyID: The selected provider key ID used for scoped pricing overrides
//   - requestID: The request ID
//   - userID: The user ID for enterprise user-level governance (empty string if not present)
//   - isCacheRead: Whether the request is a cache read
//   - isBatch: Whether the request is a batch request
//   - isFinalChunk: Whether the request is the final chunk
//   - pricingScopes: Prebuilt pricing lookup scopes using governance VK ID (nil if not applicable)
func (p *GovernancePlugin) postHookWorker(result *schemas.BifrostResponse, bifrostErr *schemas.BifrostError, provider schemas.ModelProvider, model string, requestType schemas.RequestType, virtualKey, requestID, userID string, isFinalChunk bool, attemptNumber int, pricingScopes *modelcatalog.PricingLookupScopes) {
	// Determine if request was successful
	success := (result != nil)
	billedReason := "success"

	// Streaming detection
	isStreaming := bifrost.IsStreamRequestType(requestType)

	if !isStreaming || (isStreaming && isFinalChunk) {
		var cost float64
		if p.modelCatalog != nil && result != nil {
			cost = p.modelCatalog.CalculateCost(result, pricingScopes)
		}
		tokensUsed := 0
		// The request failed/was cancelled but the provider still
		// processed tokens (carried on BifrostError.ExtraFields.BilledUsage).
		// Bill those tokens — Anthropic charges us for them regardless.
		if result == nil && bifrostErr != nil && bifrostErr.ExtraFields.BilledUsage != nil {
			billedUsage := bifrostErr.ExtraFields.BilledUsage
			tokensUsed = billedUsage.TotalTokens
			billedReason = "partial_usage_on_error"
			if p.modelCatalog != nil {
				cost = p.modelCatalog.CalculateCostForUsage(billedUsage, provider, model, requestType, pricingScopes)
			}
		}
		if result != nil {
			switch {
			case result.TextCompletionResponse != nil && result.TextCompletionResponse.Usage != nil:
				tokensUsed = result.TextCompletionResponse.Usage.TotalTokens
			case result.ChatResponse != nil && result.ChatResponse.Usage != nil:
				tokensUsed = result.ChatResponse.Usage.TotalTokens
			case result.ResponsesResponse != nil && result.ResponsesResponse.Usage != nil:
				tokensUsed = result.ResponsesResponse.Usage.TotalTokens
			case result.ResponsesStreamResponse != nil && result.ResponsesStreamResponse.Response != nil && result.ResponsesStreamResponse.Response.Usage != nil:
				tokensUsed = result.ResponsesStreamResponse.Response.Usage.TotalTokens
			case result.EmbeddingResponse != nil && result.EmbeddingResponse.Usage != nil:
				tokensUsed = result.EmbeddingResponse.Usage.TotalTokens
			case result.SpeechResponse != nil && result.SpeechResponse.Usage != nil:
				tokensUsed = result.SpeechResponse.Usage.TotalTokens
			case result.SpeechStreamResponse != nil && result.SpeechStreamResponse.Usage != nil:
				tokensUsed = result.SpeechStreamResponse.Usage.TotalTokens
			case result.TranscriptionResponse != nil && result.TranscriptionResponse.Usage != nil && result.TranscriptionResponse.Usage.TotalTokens != nil:
				tokensUsed = *result.TranscriptionResponse.Usage.TotalTokens
			case result.TranscriptionStreamResponse != nil && result.TranscriptionStreamResponse.Usage != nil && result.TranscriptionStreamResponse.Usage.TotalTokens != nil:
				tokensUsed = *result.TranscriptionStreamResponse.Usage.TotalTokens
			case result.PassthroughResponse != nil:
				if su := result.PassthroughResponse.PassthroughUsage; su != nil && su.LLMUsage != nil {
					tokensUsed = su.LLMUsage.TotalTokens
				}
			}
		}

		// Create usage update for tracker (business logic)
		usageUpdate := &UsageUpdate{
			VirtualKey:    virtualKey,
			Provider:      provider,
			Model:         model,
			Success:       success,
			TokensUsed:    int64(tokensUsed),
			Cost:          cost,
			RequestID:     requestID,
			UserID:        userID,
			IsStreaming:   isStreaming,
			IsFinalChunk:  isFinalChunk,
			HasUsageData:  tokensUsed > 0 || cost > 0,
			AttemptNumber: attemptNumber,
			BilledReason:  billedReason,
		}

		// Queue usage update asynchronously using tracker
		// UpdateUsage handles empty virtual keys gracefully by only updating provider-level and model-level usage
		p.tracker.UpdateUsage(p.ctx, usageUpdate)
	}
}

// GetGovernanceStore returns the governance store
func (p *GovernancePlugin) GetGovernanceStore() GovernanceStore {
	return p.store
}

// GenerateVirtualKey is a helper function
func GenerateVirtualKey() string {
	return VirtualKeyPrefix + uuid.NewString()
}
