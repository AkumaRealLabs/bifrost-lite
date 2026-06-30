package governance

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeTTFBStatsProvider struct {
	result *logstore.TTFBStatsResult
	err    error
}

func (f fakeTTFBStatsProvider) GetTTFBStats(context.Context, logstore.SearchFilters, time.Duration, int) (*logstore.TTFBStatsResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func newPreRequestRoutingPlugin(t *testing.T, vk *configstoreTables.TableVirtualKey) *GovernancePlugin {
	t.Helper()
	logger := NewMockLogger()
	store, err := NewLocalGovernanceStore(context.Background(), logger, nil, &configstore.GovernanceConfig{
		VirtualKeys: []configstoreTables.TableVirtualKey{*vk},
	}, nil)
	require.NoError(t, err)
	return &GovernancePlugin{
		logger:   logger,
		store:    store,
		resolver: NewBudgetResolver(store, nil, logger, nil),
	}
}

// TestRunPreRequestRouting_ExplicitProviderPrefixSkipsLoadBalancing covers the
// large-payload path: metadata.Model arrives provider-prefixed and unparsed, and
// the explicit prefix must win over VK load balancing even when multiple weighted
// providers allow the model.
func TestRunPreRequestRouting_ExplicitProviderPrefixSkipsLoadBalancing(t *testing.T) {
	vk := buildVirtualKeyWithProviders("vk1", "sk-bf-lb", "LB VK", []configstoreTables.TableVirtualKeyProviderConfig{
		buildProviderConfig("openai", []string{"*"}),
		buildProviderConfig("anthropic", []string{"*"}),
	})
	p := newPreRequestRoutingPlugin(t, vk)

	for range 20 {
		ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
		got, err := p.runPreRequestRouting(ctx, vk, false, "openai/gpt-4o", schemas.ChatCompletionRequest)
		require.NoError(t, err)
		assert.Equal(t, "openai/gpt-4o", got)
	}
}

// TestRunPreRequestRouting_UnprefixedModelLoadBalances verifies that a bare model
// string still goes through VK load balancing and comes back provider-prefixed.
func TestRunPreRequestRouting_UnprefixedModelLoadBalances(t *testing.T) {
	vk := buildVirtualKeyWithProviders("vk1", "sk-bf-lb", "LB VK", []configstoreTables.TableVirtualKeyProviderConfig{
		buildProviderConfig("openai", []string{"*"}),
	})
	p := newPreRequestRoutingPlugin(t, vk)
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)

	got, err := p.runPreRequestRouting(ctx, vk, false, "gpt-4o", schemas.ChatCompletionRequest)
	require.NoError(t, err)
	assert.Equal(t, "openai/gpt-4o", got)
}

// TestRunPreRequestRouting_UnknownPrefixIsTreatedAsModelNamespace verifies that a
// "/" prefix that is not a known provider (e.g. a HuggingFace-style namespace) is
// kept as part of the model name and load balancing still applies.
func TestRunPreRequestRouting_UnknownPrefixIsTreatedAsModelNamespace(t *testing.T) {
	vk := buildVirtualKeyWithProviders("vk1", "sk-bf-lb", "LB VK", []configstoreTables.TableVirtualKeyProviderConfig{
		buildProviderConfig("groq", []string{"*"}),
	})
	p := newPreRequestRoutingPlugin(t, vk)
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)

	got, err := p.runPreRequestRouting(ctx, vk, false, "meta-llama/llama-3.1-8b-instant", schemas.ChatCompletionRequest)
	require.NoError(t, err)
	assert.Equal(t, "groq/meta-llama/llama-3.1-8b-instant", got)
}

func TestBuildEffectiveProviderWeights_TTFBRoutingDisabledPreservesOriginalWeights(t *testing.T) {
	openaiWeight := 0.7
	anthropicWeight := 0.3
	configs := []configstoreTables.TableVirtualKeyProviderConfig{
		{Provider: "openai", Weight: &openaiWeight},
		{Provider: "anthropic", Weight: &anthropicWeight},
	}
	p := &GovernancePlugin{
		logger:      NewMockLogger(),
		ttfbRouting: normalizeTTFBRoutingConfig(nil),
		ttfbStats: fakeTTFBStatsProvider{result: &logstore.TTFBStatsResult{
			Stats: []logstore.TTFBStatsEntry{
				{Provider: "openai", Model: "gpt-4o", SampleCount: 100, P95TTFBMs: 6000, HasMinSamples: true},
			},
		}},
	}

	got := p.buildEffectiveProviderWeights(schemas.NewBifrostContext(context.Background(), schemas.NoDeadline), configs, nil, "gpt-4o")

	require.Len(t, got, 2)
	assert.Equal(t, openaiWeight, got[0].effectiveWeight)
	assert.Equal(t, anthropicWeight, got[1].effectiveWeight)
	assert.Equal(t, 1.0, got[0].penaltyFactor)
	assert.Equal(t, 1.0, got[1].penaltyFactor)
}

func TestBuildEffectiveProviderWeights_TTFBRoutingPenalizesOnlySlowProviders(t *testing.T) {
	openaiWeight := 0.7
	anthropicWeight := 0.3
	configs := []configstoreTables.TableVirtualKeyProviderConfig{
		{Provider: "openai", Weight: &openaiWeight},
		{Provider: "anthropic", Weight: &anthropicWeight},
	}
	minSamples := 20
	threshold := 2500.0
	minFactor := 0.2
	p := &GovernancePlugin{
		logger: NewMockLogger(),
		ttfbRouting: TTFBRoutingConfig{
			Enabled:          true,
			WindowSeconds:    schemas.Ptr(900),
			MinSamples:       &minSamples,
			ThresholdMs:      &threshold,
			MinPenaltyFactor: &minFactor,
		},
		ttfbStats: fakeTTFBStatsProvider{result: &logstore.TTFBStatsResult{
			Stats: []logstore.TTFBStatsEntry{
				{Provider: "openai", Model: "gpt-4o", SampleCount: 100, P95TTFBMs: 5000, HasMinSamples: true},
				{Provider: "anthropic", Model: "gpt-4o", SampleCount: 100, P95TTFBMs: 1200, HasMinSamples: true},
			},
		}},
	}

	got := p.buildEffectiveProviderWeights(schemas.NewBifrostContext(context.Background(), schemas.NoDeadline), configs, nil, "gpt-4o")

	require.Len(t, got, 2)
	assert.InDelta(t, 0.35, got[0].effectiveWeight, 0.0001)
	assert.InDelta(t, 0.5, got[0].penaltyFactor, 0.0001)
	assert.Equal(t, anthropicWeight, got[1].effectiveWeight)
	assert.Equal(t, 1.0, got[1].penaltyFactor)
}

func TestBuildEffectiveProviderWeights_TTFBRoutingSampleShortfallFallsBack(t *testing.T) {
	openaiWeight := 0.7
	anthropicWeight := 0.3
	configs := []configstoreTables.TableVirtualKeyProviderConfig{
		{Provider: "openai", Weight: &openaiWeight},
		{Provider: "anthropic", Weight: &anthropicWeight},
	}
	minSamples := 20
	threshold := 2500.0
	minFactor := 0.2
	p := &GovernancePlugin{
		logger: NewMockLogger(),
		ttfbRouting: TTFBRoutingConfig{
			Enabled:          true,
			WindowSeconds:    schemas.Ptr(900),
			MinSamples:       &minSamples,
			ThresholdMs:      &threshold,
			MinPenaltyFactor: &minFactor,
		},
		ttfbStats: fakeTTFBStatsProvider{result: &logstore.TTFBStatsResult{
			Stats: []logstore.TTFBStatsEntry{
				{Provider: "openai", Model: "gpt-4o", SampleCount: 5, P95TTFBMs: 7000, HasMinSamples: false},
			},
		}},
	}

	got := p.buildEffectiveProviderWeights(schemas.NewBifrostContext(context.Background(), schemas.NoDeadline), configs, nil, "gpt-4o")

	require.Len(t, got, 2)
	assert.Equal(t, openaiWeight, got[0].effectiveWeight)
	assert.Equal(t, anthropicWeight, got[1].effectiveWeight)
	assert.Equal(t, 1.0, got[0].penaltyFactor)
	assert.Equal(t, 1.0, got[1].penaltyFactor)
}
