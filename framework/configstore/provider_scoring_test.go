package configstore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeProviderScoringConfigDefaults(t *testing.T) {
	got := NormalizeProviderScoringConfig(nil)
	assert.False(t, got.Enabled)
	require.NotNil(t, got.WindowSeconds)
	assert.Equal(t, 120, *got.WindowSeconds)
	require.NotNil(t, got.MinSamples)
	assert.Equal(t, 5, *got.MinSamples)
	require.NotNil(t, got.ErrorRateThreshold)
	assert.InDelta(t, 0.30, *got.ErrorRateThreshold, 0.0001)
	require.NotNil(t, got.ConsecutiveFailuresThreshold)
	assert.Equal(t, 3, *got.ConsecutiveFailuresThreshold)
	require.NotNil(t, got.CooldownSeconds)
	assert.Equal(t, 300, *got.CooldownSeconds)
	require.NotNil(t, got.TTFTThresholdMs)
	assert.InDelta(t, 2500, *got.TTFTThresholdMs, 0.01)
	require.NotNil(t, got.Weights)
	assert.InDelta(t, 0.70, got.Weights.Availability, 0.0001)
	assert.InDelta(t, 0.20, got.Weights.TTFT, 0.0001)
	assert.InDelta(t, 0.10, got.Weights.Cost, 0.0001)
}

func TestProviderScoringConfigReadsLegacyTTFBFields(t *testing.T) {
	var got ProviderScoringConfig
	err := got.UnmarshalJSON([]byte(`{"ttfb_threshold_ms":1800,"weights":{"availability":0.6,"ttfb":0.3,"cost":0.1}}`))
	require.NoError(t, err)
	require.NotNil(t, got.TTFTThresholdMs)
	assert.InDelta(t, 1800, *got.TTFTThresholdMs, 0.01)
	require.NotNil(t, got.Weights)
	assert.InDelta(t, 0.3, got.Weights.TTFT, 0.0001)
}

func TestGenerateClientConfigHashIncludesProviderScoring(t *testing.T) {
	enabled := true
	window := 120
	cfg := &ClientConfig{
		ProviderScoring: &ProviderScoringConfig{
			Enabled:       enabled,
			WindowSeconds: &window,
		},
	}
	h1, err := cfg.GenerateClientConfigHash()
	require.NoError(t, err)
	cfg.ProviderScoring.Enabled = false
	h2, err := cfg.GenerateClientConfigHash()
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2)
}

func TestNormalizeProviderScoringConfigZeroSecondaryWeights(t *testing.T) {
	zero := 0.0
	got := NormalizeProviderScoringConfig(&ProviderScoringConfig{
		Weights: &ProviderScoringWeights{Availability: 1.0, TTFT: zero, Cost: zero},
	})
	require.NotNil(t, got.Weights)
	assert.InDelta(t, 0, got.Weights.TTFT, 0.0001)
	assert.InDelta(t, 0, got.Weights.Cost, 0.0001)
}
