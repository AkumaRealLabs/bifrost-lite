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

type fakeReliabilityStats struct {
	result *logstore.ProviderReliabilityStatsResult
}

func (f fakeReliabilityStats) GetProviderReliabilityStats(context.Context, logstore.SearchFilters, time.Duration, int) (*logstore.ProviderReliabilityStatsResult, error) {
	return f.result, nil
}

type fakeCooldownConfigStore struct {
	cooldowns []configstore.ProviderCooldownState
	upserts   []configstore.ProviderCooldownState
}

func (f *fakeCooldownConfigStore) GetActiveProviderCooldowns(_ context.Context, now time.Time) ([]configstore.ProviderCooldownState, error) {
	out := make([]configstore.ProviderCooldownState, 0)
	for _, c := range f.cooldowns {
		if c.CooldownUntil.After(now) {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeCooldownConfigStore) UpsertProviderCooldown(_ context.Context, state configstore.ProviderCooldownState) error {
	f.upserts = append(f.upserts, state)
	f.cooldowns = append(f.cooldowns, state)
	return nil
}

func scoringPlugin(t *testing.T, rel fakeReliabilityStats, store *fakeCooldownConfigStore, prices map[string]string) *GovernancePlugin {
	if store == nil {
		store = &fakeCooldownConfigStore{}
	}
	t.Helper()
	window := 120
	minSamples := 5
	errThreshold := 0.30
	consec := 3
	cooldown := 300
	ttfbMs := 2500.0
	p := &GovernancePlugin{
		logger: NewMockLogger(),
		providerScoring: providerScoringConfig{
			Enabled:                      true,
			WindowSeconds:                &window,
			MinSamples:                   &minSamples,
			ErrorRateThreshold:           &errThreshold,
			ConsecutiveFailuresThreshold: &consec,
			CooldownSeconds:              &cooldown,
			TTFBThresholdMs:              &ttfbMs,
			Weights:                      &configstore.ProviderScoringWeights{Availability: 0.70, TTFB: 0.20, Cost: 0.10},
		},
		reliabilityStats: rel,
		ttfbStats: fakeTTFBStatsProvider{result: &logstore.TTFBStatsResult{
			Stats: []logstore.TTFBStatsEntry{
				{Provider: "fast", Model: "gpt-4o", SampleCount: 10, P95TTFBMs: 800, HasMinSamples: true},
				{Provider: "slow", Model: "gpt-4o", SampleCount: 10, P95TTFBMs: 4000, HasMinSamples: true},
			},
		}},
	}
	if store != nil {
		p.testCooldownGet = store.GetActiveProviderCooldowns
		p.testCooldownUpsert = store.UpsertProviderCooldown
	}
	p.providerPriceOverride = func(providerName string) (float64, bool) {
		if prices != nil {
			if desc, ok := prices[providerName]; ok {
				return parseProviderPriceRMBPerDao(desc)
			}
		}
		return 0, false
	}
	return p
}

func TestApplyProviderScoring_TTFBBeatsCost(t *testing.T) {
	wFast, wSlow := 1.0, 1.0
	configs := []configstoreTables.TableVirtualKeyProviderConfig{
		{Provider: "fast", Weight: &wFast},
		{Provider: "slow", Weight: &wSlow},
	}
	rel := fakeReliabilityStats{result: &logstore.ProviderReliabilityStatsResult{
		Stats: []logstore.ProviderReliabilityStatsEntry{
			{Provider: "fast", SampleCount: 10, ErrorRate: 0, HasMinSamples: true},
			{Provider: "slow", SampleCount: 10, ErrorRate: 0, HasMinSamples: true},
		},
	}}
	p := scoringPlugin(t, rel, &fakeCooldownConfigStore{}, map[string]string{
		"fast": `{"price_rmb_per_dao":0.10}`,
		"slow": `{"price_rmb_per_dao":0.05}`,
	})
	weighted := []weightedProviderConfig{
		{config: configs[0], originalWeight: 1, effectiveWeight: 1, penaltyFactor: 1},
		{config: configs[1], originalWeight: 1, effectiveWeight: 1, penaltyFactor: 1},
	}
	got := p.applyProviderScoring(schemas.NewBifrostContext(context.Background(), schemas.NoDeadline), configs, nil, "gpt-4o", weighted)
	require.Len(t, got, 2)
	assert.Greater(t, got[0].effectiveWeight, got[1].effectiveWeight)
	assert.Equal(t, "fast", got[0].config.Provider)
}

func TestApplyProviderScoring_SkipsCooledUnlessAllCooled(t *testing.T) {
	wA, wB := 1.0, 1.0
	configs := []configstoreTables.TableVirtualKeyProviderConfig{
		{Provider: "a", Weight: &wA},
		{Provider: "b", Weight: &wB},
	}
	store := &fakeCooldownConfigStore{
		cooldowns: []configstore.ProviderCooldownState{{
			Provider:      "a",
			CooldownUntil: time.Now().Add(5 * time.Minute),
		}},
	}
	rel := fakeReliabilityStats{result: &logstore.ProviderReliabilityStatsResult{Stats: []logstore.ProviderReliabilityStatsEntry{}}}
	p := scoringPlugin(t, rel, store, nil)
	weighted := []weightedProviderConfig{
		{config: configs[0], originalWeight: 1, effectiveWeight: 0.5, penaltyFactor: 0.5},
		{config: configs[1], originalWeight: 1, effectiveWeight: 0.9, penaltyFactor: 0.9},
	}
	got := p.applyProviderScoring(schemas.NewBifrostContext(context.Background(), schemas.NoDeadline), configs, nil, "gpt-4o", weighted)
	require.Len(t, got, 1)
	assert.Equal(t, "b", got[0].config.Provider)
}

func TestApplyProviderScoring_FailOpenWhenAllCooled(t *testing.T) {
	wA, wB := 1.0, 1.0
	configs := []configstoreTables.TableVirtualKeyProviderConfig{
		{Provider: "a", Weight: &wA},
		{Provider: "b", Weight: &wB},
	}
	until := time.Now().Add(5 * time.Minute)
	store := &fakeCooldownConfigStore{
		cooldowns: []configstore.ProviderCooldownState{
			{Provider: "a", CooldownUntil: until},
			{Provider: "b", CooldownUntil: until},
		},
	}
	rel := fakeReliabilityStats{result: &logstore.ProviderReliabilityStatsResult{Stats: []logstore.ProviderReliabilityStatsEntry{}}}
	p := scoringPlugin(t, rel, store, nil)
	weighted := []weightedProviderConfig{
		{config: configs[0], originalWeight: 1, effectiveWeight: 0.2, penaltyFactor: 0.2},
		{config: configs[1], originalWeight: 1, effectiveWeight: 0.8, penaltyFactor: 0.8},
	}
	got := p.applyProviderScoring(schemas.NewBifrostContext(context.Background(), schemas.NoDeadline), configs, nil, "gpt-4o", weighted)
	assert.Len(t, got, 2)
}

func TestParseProviderPriceRMBPerDao(t *testing.T) {
	v, ok := parseProviderPriceRMBPerDao(`{"price_rmb_per_dao":0.045}`)
	assert.True(t, ok)
	assert.InDelta(t, 0.045, v, 0.0001)
	_, ok = parseProviderPriceRMBPerDao("")
	assert.False(t, ok)
}