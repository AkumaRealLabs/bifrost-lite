package logstore

import (
	"context"
	"fmt"
	"sort"
	"time"
)

func (s *RDBLogStore) GetProviderReliabilityStats(ctx context.Context, filters SearchFilters, window time.Duration, minSamples int) (*ProviderReliabilityStatsResult, error) {
	if window <= 0 {
		window = 2 * time.Minute
	}
	if minSamples <= 0 {
		minSamples = 5
	}

	end := time.Now().UTC()
	if filters.EndTime != nil && !filters.EndTime.IsZero() {
		end = filters.EndTime.UTC()
	}
	start := end.Add(-window)
	if filters.StartTime == nil || filters.StartTime.Before(start) {
		filters.StartTime = &start
	}
	if filters.EndTime == nil {
		filters.EndTime = &end
	}

	query := s.ScopedDB(ctx).Model(&Log{})
	query = s.applyFilters(query, filters)
	query = query.Where("status IN ?", []string{"success", "error"})
	query = query.Where("provider IS NOT NULL AND provider != ''")

	result := &ProviderReliabilityStatsResult{
		WindowSeconds: int64(window.Seconds()),
		MinSamples:    minSamples,
		Stats:         []ProviderReliabilityStatsEntry{},
	}

	switch s.db.Dialector.Name() {
	case "postgres":
		var rows []struct {
			Provider    string `gorm:"column:provider"`
			SampleCount int64  `gorm:"column:sample_count"`
			ErrorCount  int64  `gorm:"column:error_count"`
		}
		selectClause := `
			provider,
			COUNT(*) as sample_count,
			SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END) as error_count
		`
		if err := query.Select(selectClause).Group("provider").Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("failed to get provider reliability stats: %w", err)
		}
		for _, row := range rows {
			entry := providerReliabilityFromCounts(row.Provider, row.SampleCount, row.ErrorCount, minSamples)
			entry.ConsecutiveFailures = s.countConsecutiveProviderFailures(ctx, filters, row.Provider)
			result.Stats = append(result.Stats, entry)
		}
		return result, nil
	default:
		var rows []struct {
			Provider string `gorm:"column:provider"`
			Status   string `gorm:"column:status"`
		}
		if err := query.Select("provider, status").Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("failed to get provider reliability stats: %w", err)
		}
		type agg struct {
			total int64
			errs  int64
		}
		byProvider := map[string]*agg{}
		for _, row := range rows {
			a := byProvider[row.Provider]
			if a == nil {
				a = &agg{}
				byProvider[row.Provider] = a
			}
			a.total++
			if row.Status == "error" {
				a.errs++
			}
		}
		for provider, a := range byProvider {
			entry := providerReliabilityFromCounts(provider, a.total, a.errs, minSamples)
			entry.ConsecutiveFailures = s.countConsecutiveProviderFailures(ctx, filters, provider)
			result.Stats = append(result.Stats, entry)
		}
		sort.Slice(result.Stats, func(i, j int) bool {
			return result.Stats[i].Provider < result.Stats[j].Provider
		})
		return result, nil
	}
}

func providerReliabilityFromCounts(provider string, sampleCount, errorCount int64, minSamples int) ProviderReliabilityStatsEntry {
	var errorRate float64
	if sampleCount > 0 {
		errorRate = float64(errorCount) / float64(sampleCount)
	}
	return ProviderReliabilityStatsEntry{
		Provider:      provider,
		SampleCount:   sampleCount,
		ErrorCount:    errorCount,
		ErrorRate:     errorRate,
		HasMinSamples: sampleCount >= int64(minSamples),
	}
}

func (s *RDBLogStore) countConsecutiveProviderFailures(ctx context.Context, filters SearchFilters, provider string) int {
	end := time.Now().UTC()
	if filters.EndTime != nil && !filters.EndTime.IsZero() {
		end = filters.EndTime.UTC()
	}
	start := end.Add(-2 * time.Minute)
	if filters.StartTime != nil && !filters.StartTime.IsZero() {
		start = filters.StartTime.UTC()
	}

	q := s.ScopedDB(ctx).Model(&Log{})
	local := filters
	local.StartTime = &start
	local.EndTime = &end
	q = s.applyFilters(q, local)
	q = q.Where("provider = ?", provider)
	q = q.Where("status IN ?", []string{"success", "error"})

	var rows []struct {
		Status    string    `gorm:"column:status"`
		Timestamp time.Time `gorm:"column:timestamp"`
	}
	if err := q.Select("status, timestamp").Order("timestamp DESC").Limit(200).Find(&rows).Error; err != nil {
		return 0
	}
	count := 0
	for _, row := range rows {
		if row.Status == "error" {
			count++
			continue
		}
		break
	}
	return count
}
