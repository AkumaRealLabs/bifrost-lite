package governance

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func providerWithPrice(price string) configstore.ProviderConfig {
	return configstore.ProviderConfig{
		Description: `{"price_rmb_per_dao":` + price + `}`,
		Keys:        []schemas.Key{{ID: "key-" + price, Models: schemas.WhiteList{"*"}}},
		CustomProviderConfig: &schemas.CustomProviderConfig{
			AllowedRequests: &schemas.AllowedRequests{ChatCompletion: true, Responses: true},
		},
	}
}

func TestBuildSystemPoolProviderConfigsPriceBoundaries(t *testing.T) {
	disabled := false
	providers := map[schemas.ModelProvider]configstore.ProviderConfig{
		"p005":         providerWithPrice("0.05"),
		"p010":         providerWithPrice("0.10"),
		"p015":         providerWithPrice("0.15"),
		"p025":         providerWithPrice("0.25"),
		"p_zero":       providerWithPrice("0"),
		"p_high":       providerWithPrice("0.26"),
		"p_empty":      {Keys: []schemas.Key{{ID: "empty", Models: schemas.WhiteList{"*"}}}},
		"p_bad_json":   {Description: "{", Keys: []schemas.Key{{ID: "bad-json", Models: schemas.WhiteList{"*"}}}},
		"p_disabled":   {Description: `{"price_rmb_per_dao":0.05}`, Keys: []schemas.Key{{ID: "disabled", Enabled: &disabled, Models: schemas.WhiteList{"*"}}}},
		"p_image_only": {Description: `{"price_rmb_per_dao":0.05}`, Keys: []schemas.Key{{ID: "image", Models: schemas.WhiteList{"*"}}}, CustomProviderConfig: &schemas.CustomProviderConfig{AllowedRequests: &schemas.AllowedRequests{ImageGeneration: true}}},
	}

	low := BuildSystemPoolProviderConfigsForAPI(SystemPoolLow, providers)
	stable := BuildSystemPoolProviderConfigsForAPI(SystemPoolStable, providers)

	assert.Equal(t, []string{"p005", "p010"}, providerConfigNames(low))
	assert.Equal(t, []string{"p005", "p010", "p015", "p025"}, providerConfigNames(stable))
}

func TestSystemPoolIgnoresManualProviderConfigs(t *testing.T) {
	vk := buildVirtualKeyWithProviders("vk-low", "sk-bf-low", SystemPoolLow, []configstoreTables.TableVirtualKeyProviderConfig{
		buildProviderConfig("manual_expensive", []string{"*"}),
	})
	p := &GovernancePlugin{
		inMemoryStore: fakeProviderConfigStore{providers: map[schemas.ModelProvider]configstore.ProviderConfig{
			"auto_low":         providerWithPrice("0.05"),
			"auto_too_costly":  providerWithPrice("0.15"),
			"manual_expensive": providerWithPrice("0.20"),
		}},
	}

	got := p.virtualKeyForRouting(vk)
	require.NotSame(t, vk, got)
	assert.Equal(t, []string{"auto_low"}, providerConfigNames(got.ProviderConfigs))
}

func TestSystemPoolExplicitProviderOutsidePoolReturns503(t *testing.T) {
	vk := buildVirtualKeyWithProviders("vk-low", "sk-bf-low", SystemPoolLow, []configstoreTables.TableVirtualKeyProviderConfig{
		buildProviderConfig("manual_expensive", []string{"*"}),
	})
	store, err := NewLocalGovernanceStore(context.Background(), NewMockLogger(), nil, &configstore.GovernanceConfig{
		VirtualKeys: []configstoreTables.TableVirtualKey{*vk},
	}, nil)
	require.NoError(t, err)
	p := &GovernancePlugin{
		store:         store,
		resolver:      NewBudgetResolver(store, nil, NewMockLogger(), fakeProviderConfigStore{providers: map[schemas.ModelProvider]configstore.ProviderConfig{"expensive": providerWithPrice("0.15")}}),
		inMemoryStore: fakeProviderConfigStore{providers: map[schemas.ModelProvider]configstore.ProviderConfig{"expensive": providerWithPrice("0.15")}},
	}

	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	_, got := p.EvaluateGovernanceRequest(ctx, &EvaluationRequest{
		VirtualKey: "sk-bf-low",
		Provider:   "expensive",
		Model:      "gpt-4o",
	}, schemas.ChatCompletionRequest)

	require.NotNil(t, got)
	require.NotNil(t, got.StatusCode)
	assert.Equal(t, 503, *got.StatusCode)
}

func TestSystemPoolNoCandidatesSets503ShortCircuit(t *testing.T) {
	vk := buildVirtualKeyWithProviders("vk-low", "sk-bf-low", SystemPoolLow, nil)
	p := &GovernancePlugin{
		inMemoryStore: fakeProviderConfigStore{providers: map[schemas.ModelProvider]configstore.ProviderConfig{
			"expensive": providerWithPrice("0.15"),
		}},
		resolver: &BudgetResolver{},
		logger:   NewMockLogger(),
	}
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	req := &schemas.BifrostRequest{
		RequestType: schemas.ChatCompletionRequest,
		ChatRequest: &schemas.BifrostChatRequest{Model: "gpt-4o"},
	}

	require.NoError(t, p.loadBalanceProvider(ctx, req, vk))
	err, ok := ctx.Value(schemas.BifrostContextKeyPreRequestShortCircuitError).(*schemas.BifrostError)
	require.True(t, ok)
	require.NotNil(t, err)
	require.NotNil(t, err.StatusCode)
	assert.Equal(t, 503, *err.StatusCode)
}

func providerConfigNames(configs []configstoreTables.TableVirtualKeyProviderConfig) []string {
	names := make([]string, 0, len(configs))
	for _, pc := range configs {
		names = append(names, pc.Provider)
	}
	return names
}
