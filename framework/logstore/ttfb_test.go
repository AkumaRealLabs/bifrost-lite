package logstore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTTFBLog(t *testing.T, store *RDBLogStore, id, provider, model string, ts time.Time, ttfb *float64) {
	createTimingLog(t, store, id, provider, model, ts, ttfb, nil)
}

func createTimingLog(t *testing.T, store *RDBLogStore, id, provider, model string, ts time.Time, ttfb, ttft *float64) {
	t.Helper()
	latency := 1000.0
	virtualKeyID := "vk-gpt"
	createTimingLogWithVK(t, store, id, provider, model, virtualKeyID, ts, ttfb, ttft, latency)
}

func createTimingLogWithVK(t *testing.T, store *RDBLogStore, id, provider, model, virtualKeyID string, ts time.Time, ttfb, ttft *float64, latency float64) {
	t.Helper()
	err := store.Create(context.Background(), &Log{
		ID:           id,
		Timestamp:    ts,
		Object:       "chat.completion",
		Provider:     provider,
		Model:        model,
		VirtualKeyID: &virtualKeyID,
		Latency:      &latency,
		TTFBMs:       ttfb,
		TTFTMs:       ttft,
		Status:       "success",
		Stream:       ttfb != nil || ttft != nil,
	})
	require.NoError(t, err)
}

func TestTTFBSearchFiltersAndSortingSQLite(t *testing.T) {
	store := newTestSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	createTTFBLog(t, store, "fast", "openai", "gpt-4o", now.Add(-3*time.Minute), floatPtr(500))
	createTTFBLog(t, store, "medium", "openai", "gpt-4o", now.Add(-2*time.Minute), floatPtr(1500))
	createTTFBLog(t, store, "slow", "anthropic", "gpt-4o", now.Add(-1*time.Minute), floatPtr(3500))
	createTTFBLog(t, store, "non-stream", "openai", "gpt-4o", now, nil)

	minTTFB := 1000.0
	maxTTFB := 3000.0
	result, err := store.SearchLogs(context.Background(), SearchFilters{
		MinTTFBMs: &minTTFB,
		MaxTTFBMs: &maxTTFB,
	}, PaginationOptions{
		Limit:  10,
		SortBy: string(SortByTTFB),
		Order:  "asc",
	})
	require.NoError(t, err)
	require.Len(t, result.Logs, 1)
	assert.Equal(t, "medium", result.Logs[0].ID)
	assert.Equal(t, 1500.0, *result.Logs[0].TTFBMs)
}

func TestTTFTSearchFiltersAndSortingSQLite(t *testing.T) {
	store := newTestSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	createTimingLog(t, store, "fast-byte-slow-token", "openai", "gpt-4o", now.Add(-3*time.Minute), floatPtr(100), floatPtr(2500))
	createTimingLog(t, store, "medium-token", "openai", "gpt-4o", now.Add(-2*time.Minute), floatPtr(900), floatPtr(1500))
	createTimingLog(t, store, "slow-token", "anthropic", "gpt-4o", now.Add(-1*time.Minute), floatPtr(100), floatPtr(3500))
	createTimingLog(t, store, "no-token", "openai", "gpt-4o", now, floatPtr(50), nil)

	minTTFT := 1000.0
	maxTTFT := 3000.0
	result, err := store.SearchLogs(context.Background(), SearchFilters{
		MinTTFTMs: &minTTFT,
		MaxTTFTMs: &maxTTFT,
	}, PaginationOptions{
		Limit:  10,
		SortBy: string(SortByTTFT),
		Order:  "asc",
	})
	require.NoError(t, err)
	require.Len(t, result.Logs, 2)
	assert.Equal(t, "medium-token", result.Logs[0].ID)
	assert.Equal(t, "fast-byte-slow-token", result.Logs[1].ID)
	assert.Equal(t, 1500.0, *result.Logs[0].TTFTMs)
}

func TestTTFBHistogramsAndStatsSQLite(t *testing.T) {
	store := newTestSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-10 * time.Minute)
	end := now.Add(time.Minute)

	createTTFBLog(t, store, "openai-1", "openai", "gpt-4o", now.Add(-8*time.Minute), floatPtr(1000))
	createTTFBLog(t, store, "openai-2", "openai", "gpt-4o", now.Add(-7*time.Minute), floatPtr(2000))
	createTTFBLog(t, store, "anthropic-1", "anthropic", "gpt-4o", now.Add(-6*time.Minute), floatPtr(4000))
	createTTFBLog(t, store, "non-stream", "openai", "gpt-4o", now.Add(-5*time.Minute), nil)

	filters := SearchFilters{
		StartTime: &start,
		EndTime:   &end,
	}

	hist, err := store.GetTTFBHistogram(context.Background(), filters, 3600)
	require.NoError(t, err)
	var total int64
	for _, bucket := range hist.Buckets {
		total += bucket.TotalRequests
	}
	assert.Equal(t, int64(3), total)

	byProvider, err := store.GetProviderTTFBHistogram(context.Background(), filters, 3600)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"anthropic", "openai"}, byProvider.Providers)
	var openaiRequests int64
	var anthropicRequests int64
	for _, bucket := range byProvider.Buckets {
		openaiRequests += bucket.ByProvider["openai"].TotalRequests
		anthropicRequests += bucket.ByProvider["anthropic"].TotalRequests
	}
	assert.Equal(t, int64(2), openaiRequests)
	assert.Equal(t, int64(1), anthropicRequests)

	stats, err := store.GetTTFBStats(context.Background(), filters, 15*time.Minute, 2)
	require.NoError(t, err)
	require.Len(t, stats.Stats, 2)
	byProviderStats := map[string]TTFBStatsEntry{}
	for _, entry := range stats.Stats {
		byProviderStats[entry.Provider] = entry
	}
	assert.Equal(t, int64(2), byProviderStats["openai"].SampleCount)
	assert.True(t, byProviderStats["openai"].HasMinSamples)
	assert.Equal(t, int64(1), byProviderStats["anthropic"].SampleCount)
	assert.False(t, byProviderStats["anthropic"].HasMinSamples)
}

func TestTTFTHistogramsAndStatsSQLite(t *testing.T) {
	store := newTestSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-10 * time.Minute)
	end := now.Add(time.Minute)

	createTimingLog(t, store, "openai-1", "openai", "gpt-4o", now.Add(-8*time.Minute), floatPtr(50), floatPtr(1000))
	createTimingLog(t, store, "openai-2", "openai", "gpt-4o", now.Add(-7*time.Minute), floatPtr(60), floatPtr(2000))
	createTimingLog(t, store, "anthropic-1", "anthropic", "gpt-4o", now.Add(-6*time.Minute), floatPtr(70), floatPtr(4000))
	createTimingLog(t, store, "metadata-only", "openai", "gpt-4o", now.Add(-5*time.Minute), floatPtr(80), nil)

	filters := SearchFilters{
		StartTime: &start,
		EndTime:   &end,
	}

	hist, err := store.GetTTFTHistogram(context.Background(), filters, 3600)
	require.NoError(t, err)
	var total int64
	for _, bucket := range hist.Buckets {
		total += bucket.TotalRequests
	}
	assert.Equal(t, int64(3), total)

	byProvider, err := store.GetProviderTTFTHistogram(context.Background(), filters, 3600)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"anthropic", "openai"}, byProvider.Providers)
	var openaiRequests int64
	var anthropicRequests int64
	for _, bucket := range byProvider.Buckets {
		openaiRequests += bucket.ByProvider["openai"].TotalRequests
		anthropicRequests += bucket.ByProvider["anthropic"].TotalRequests
	}
	assert.Equal(t, int64(2), openaiRequests)
	assert.Equal(t, int64(1), anthropicRequests)

	stats, err := store.GetTTFTStats(context.Background(), filters, 15*time.Minute, 2)
	require.NoError(t, err)
	require.Len(t, stats.Stats, 2)
	byProviderStats := map[string]TTFTStatsEntry{}
	for _, entry := range stats.Stats {
		byProviderStats[entry.Provider] = entry
	}
	assert.Equal(t, int64(2), byProviderStats["openai"].SampleCount)
	assert.True(t, byProviderStats["openai"].HasMinSamples)
	assert.Equal(t, int64(1), byProviderStats["anthropic"].SampleCount)
	assert.False(t, byProviderStats["anthropic"].HasMinSamples)
}

func TestTTFTStatsAggregateAcrossVirtualKeysWhenUnfiltered(t *testing.T) {
	store := newTestSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-10 * time.Minute)
	end := now.Add(time.Minute)

	createTimingLogWithVK(t, store, "openai-vk-a", "openai", "gpt-4o", "vk-a", now.Add(-8*time.Minute), floatPtr(50), floatPtr(1000), 1000)
	createTimingLogWithVK(t, store, "openai-vk-b", "openai", "gpt-4o", "vk-b", now.Add(-7*time.Minute), floatPtr(60), floatPtr(5000), 1000)
	createTimingLogWithVK(t, store, "anthropic-vk-a", "anthropic", "gpt-4o", "vk-c", now.Add(-6*time.Minute), floatPtr(70), floatPtr(4000), 1000)

	stats, err := store.GetTTFTStats(context.Background(), SearchFilters{StartTime: &start, EndTime: &end}, 15*time.Minute, 2)
	require.NoError(t, err)
	require.Len(t, stats.Stats, 2)

	byProviderStats := map[string]TTFTStatsEntry{}
	for _, entry := range stats.Stats {
		byProviderStats[entry.Provider] = entry
	}

	require.Contains(t, byProviderStats, "openai")
	assert.Empty(t, byProviderStats["openai"].VirtualKeyID)
	assert.Equal(t, int64(2), byProviderStats["openai"].SampleCount)
	assert.True(t, byProviderStats["openai"].HasMinSamples)
	assert.InDelta(t, 4800, byProviderStats["openai"].P95TTFTMs, 0.1)
}

func TestTTFTStatsKeepVirtualKeySplitWhenFiltered(t *testing.T) {
	store := newTestSQLiteStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-10 * time.Minute)
	end := now.Add(time.Minute)

	createTimingLogWithVK(t, store, "openai-vk-a", "openai", "gpt-4o", "vk-a", now.Add(-8*time.Minute), floatPtr(50), floatPtr(1000), 1000)
	createTimingLogWithVK(t, store, "openai-vk-b", "openai", "gpt-4o", "vk-b", now.Add(-7*time.Minute), floatPtr(60), floatPtr(5000), 1000)

	stats, err := store.GetTTFTStats(context.Background(), SearchFilters{StartTime: &start, EndTime: &end, VirtualKeyIDs: []string{"vk-b"}}, 15*time.Minute, 1)
	require.NoError(t, err)
	require.Len(t, stats.Stats, 1)
	assert.Equal(t, "vk-b", stats.Stats[0].VirtualKeyID)
	assert.Equal(t, int64(1), stats.Stats[0].SampleCount)
	assert.InDelta(t, 5000, stats.Stats[0].P95TTFTMs, 0.1)
}

func floatPtr(v float64) *float64 {
	return &v
}
