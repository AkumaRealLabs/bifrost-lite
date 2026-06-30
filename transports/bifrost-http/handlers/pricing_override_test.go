package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/modelcatalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func TestUpdatePricingOverride_ReplacesFullBody(t *testing.T) {
	SetLogger(&mockLogger{})
	store := setupPricingOverrideHandlerStore(t)
	handler := &GovernanceHandler{
		configStore:       store,
		governanceManager: pricingOverrideTestGovernanceManager{},
	}

	now := time.Now().UTC()
	override := configstoreTables.TablePricingOverride{
		ID:               "override-1",
		Name:             "Original",
		ScopeKind:        string(modelcatalog.ScopeKindGlobal),
		MatchType:        string(modelcatalog.MatchTypeExact),
		Pattern:          "gpt-4.1",
		CreatedAt:        now,
		UpdatedAt:        now,
		PricingPatchJSON: `{"input_cost_per_token":1,"output_cost_per_token":2}`,
		RequestTypes:     []schemas.RequestType{schemas.ChatCompletionRequest},
	}
	require.NoError(t, store.CreatePricingOverride(context.Background(), &override))

	// Patch replaces in full: omitted fields are cleared, not merged.
	body := `{
		"name":"Updated",
		"scope_kind":"global",
		"match_type":"exact",
		"pattern":"gpt-4.1",
		"request_types":["chat_completion"],
		"patch":{"input_cost_per_token":1.5}
	}`
	ctx := newTestRequestCtx(body)
	ctx.SetUserValue("id", override.ID)

	handler.updatePricingOverride(ctx)

	require.Equal(t, fasthttp.StatusOK, ctx.Response.StatusCode(), string(ctx.Response.Body()))

	stored, err := store.GetPricingOverrideByID(context.Background(), override.ID)
	require.NoError(t, err)
	assert.Equal(t, "Updated", stored.Name)

	var patch modelcatalog.PricingOptions
	require.NoError(t, json.Unmarshal([]byte(stored.PricingPatchJSON), &patch))
	require.NotNil(t, patch.InputCostPerToken)
	assert.Equal(t, 1.5, *patch.InputCostPerToken)
	assert.Nil(t, patch.OutputCostPerToken)
	assert.Empty(t, stored.ConfigHash)
}
