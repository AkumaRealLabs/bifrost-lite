package governance

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
)

type providerScoringConfig = configstore.ProviderScoringConfig

func normalizeProviderScoringConfig(config *providerScoringConfig) providerScoringConfig {
	return configstore.NormalizeProviderScoringConfig(config)
}

type ProviderReliabilityStatsProvider interface {
	GetProviderReliabilityStats(ctx context.Context, filters logstore.SearchFilters, window time.Duration, minSamples int) (*logstore.ProviderReliabilityStatsResult, error)
}

func parseProviderPriceRMBPerDao(description string) (float64, bool) {
	description = strings.TrimSpace(description)
	if description == "" {
		return 0, false
	}
	var meta struct {
		PriceRMBPerDao float64 `json:"price_rmb_per_dao"`
	}
	if err := json.Unmarshal([]byte(description), &meta); err != nil {
		return 0, false
	}
	if meta.PriceRMBPerDao <= 0 {
		return 0, false
	}
	return meta.PriceRMBPerDao, true
}

func clampScore(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func consecutiveFailurePenalty(count int) float64 {
	if count <= 0 {
		return 0
	}
	return math.Min(0.5, float64(count)*0.1)
}

func (p *GovernancePlugin) providerPriceRMB(providerName string, providers map[schemas.ModelProvider]configstore.ProviderConfig) (float64, bool) {
	if p.providerPriceOverride != nil {
		return p.providerPriceOverride(providerName)
	}
	if cfg, ok := providers[schemas.ModelProvider(providerName)]; ok {
		if price, ok := parseProviderPriceRMBPerDao(cfg.Description); ok {
			return price, true
		}
	}
	if p.configStore == nil {
		return 0, false
	}
	row, err := p.configStore.GetProvider(context.Background(), schemas.ModelProvider(providerName))
	if err != nil || row == nil {
		return 0, false
	}
	return parseProviderPriceRMBPerDao(row.Description)
}

func (p *GovernancePlugin) applyProviderScoring(
	ctx *schemas.BifrostContext,
	configs []configstoreTables.TableVirtualKeyProviderConfig,
	virtualKey *configstoreTables.TableVirtualKey,
	model string,
	weighted []weightedProviderConfig,
) []weightedProviderConfig {
	if len(weighted) == 0 || !p.providerScoring.Enabled {
		return weighted
	}

	cfg := p.providerScoring
	windowSeconds := *cfg.WindowSeconds
	minSamples := *cfg.MinSamples
	errorRateThreshold := *cfg.ErrorRateThreshold
	consecutiveThreshold := *cfg.ConsecutiveFailuresThreshold
	cooldownSeconds := *cfg.CooldownSeconds
	ttftThresholdMs := *cfg.TTFTThresholdMs
	weights := *cfg.Weights

	now := time.Now().UTC()
	cooled := map[string]bool{}
	getCooldowns := func() ([]configstore.ProviderCooldownState, error) {
		if p.testCooldownGet != nil {
			return p.testCooldownGet(ctx, now)
		}
		if p.configStore != nil {
			return p.configStore.GetActiveProviderCooldowns(ctx, now)
		}
		return nil, nil
	}
	upsertCooldown := func(state configstore.ProviderCooldownState) error {
		if p.testCooldownUpsert != nil {
			return p.testCooldownUpsert(ctx, state)
		}
		if p.configStore != nil {
			return p.configStore.UpsertProviderCooldown(ctx, state)
		}
		return nil
	}
	active, err := getCooldowns()
	if err != nil {
		ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelWarn, fmt.Sprintf("Provider scoring: cooldown lookup failed: %v", err))
	} else {
		for _, c := range active {
			cooled[c.Provider] = true
		}
	}

	reliabilityByProvider := map[string]logstore.ProviderReliabilityStatsEntry{}
	if p.reliabilityStats != nil {
		stats, err := p.reliabilityStats.GetProviderReliabilityStats(ctx, logstore.SearchFilters{}, time.Duration(windowSeconds)*time.Second, minSamples)
		if err != nil {
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelWarn, fmt.Sprintf("Provider scoring: reliability stats unavailable: %v", err))
		} else {
			for _, e := range stats.Stats {
				reliabilityByProvider[e.Provider] = e
				if e.HasMinSamples && e.ErrorRate >= errorRateThreshold {
					until := now.Add(time.Duration(cooldownSeconds) * time.Second)
					_ = upsertCooldown(configstore.ProviderCooldownState{
						Provider:            e.Provider,
						CooldownUntil:       until,
						Reason:              "error_rate_threshold",
						ErrorRate:           e.ErrorRate,
						ConsecutiveFailures: e.ConsecutiveFailures,
						WindowSeconds:       windowSeconds,
						UpdatedAt:           now,
					})
					cooled[e.Provider] = true
					ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelInfo, fmt.Sprintf("Provider scoring: provider %s cooled down until %s (error rate %.2f)", e.Provider, until.Format(time.RFC3339), e.ErrorRate))
				}
				if e.ConsecutiveFailures >= consecutiveThreshold {
					until := now.Add(time.Duration(cooldownSeconds) * time.Second)
					_ = upsertCooldown(configstore.ProviderCooldownState{
						Provider:            e.Provider,
						CooldownUntil:       until,
						Reason:              "consecutive_failures",
						ErrorRate:           e.ErrorRate,
						ConsecutiveFailures: e.ConsecutiveFailures,
						WindowSeconds:       windowSeconds,
						UpdatedAt:           now,
					})
					cooled[e.Provider] = true
					ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelInfo, fmt.Sprintf("Provider scoring: provider %s cooled down until %s (%d consecutive failures)", e.Provider, until.Format(time.RFC3339), e.ConsecutiveFailures))
				}
			}
		}
	}

	ttftByProvider := map[string]logstore.TTFTStatsEntry{}
	if p.ttftStats != nil {
		filters := logstore.SearchFilters{Models: []string{model}}
		if virtualKey != nil && virtualKey.ID != "" {
			filters.VirtualKeyIDs = []string{virtualKey.ID}
		}
		stats, err := p.ttftStats.GetTTFTStats(ctx, filters, time.Duration(windowSeconds)*time.Second, minSamples)
		if err == nil {
			for _, entry := range stats.Stats {
				if entry.Model != model {
					continue
				}
				current, ok := ttftByProvider[entry.Provider]
				if !ok || entry.SampleCount > current.SampleCount {
					ttftByProvider[entry.Provider] = entry
				}
			}
		}
	}

	priceByProvider := map[string]float64{}
	var cheapest float64
	hasCheapest := false
	providers := map[schemas.ModelProvider]configstore.ProviderConfig{}
	if p.inMemoryStore != nil {
		providers = p.inMemoryStore.GetConfiguredProviders()
	}
	for _, w := range weighted {
		price, ok := p.providerPriceRMB(w.config.Provider, providers)
		if ok {
			priceByProvider[w.config.Provider] = price
			if !hasCheapest || price < cheapest {
				cheapest = price
				hasCheapest = true
			}
		}
	}

	type scored struct {
		idx   int
		score float64
	}
	scores := make([]scored, 0, len(weighted))
	neutralAvailability := 0.5
	neutralTTFT := 0.5
	minCostScore := 0.05

	for i, w := range weighted {
		provider := w.config.Provider
		rel, hasRel := reliabilityByProvider[provider]
		var availabilityScore float64 = neutralAvailability
		if hasRel && rel.HasMinSamples {
			availabilityScore = (1.0 - rel.ErrorRate) - consecutiveFailurePenalty(rel.ConsecutiveFailures)
			availabilityScore = clampScore(availabilityScore, 0.05, 1.0)
		}

		var ttftScore float64 = neutralTTFT
		if entry, ok := ttftByProvider[provider]; ok && entry.HasMinSamples && entry.P95TTFTMs > 0 {
			ttftScore = clampScore(ttftThresholdMs/entry.P95TTFTMs, 0.05, 1.0)
		}

		var costScore float64 = minCostScore
		if price, ok := priceByProvider[provider]; ok && hasCheapest && price > 0 {
			costScore = clampScore(cheapest/price, minCostScore, 1.0)
		}

		final := availabilityScore*weights.Availability + ttftScore*weights.TTFT + costScore*weights.Cost
		weighted[i].effectiveWeight = w.originalWeight * final
		weighted[i].penaltyFactor = final
		scores = append(scores, scored{idx: i, score: final})
		ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelInfo, fmt.Sprintf(
			"Provider scoring %s model %s: avail=%.2f ttft=%.2f cost=%.2f final=%.2f weight %.2f -> %.2f",
			provider, model, availabilityScore, ttftScore, costScore, final, w.originalWeight, weighted[i].effectiveWeight,
		))
	}

	notCooled := make([]int, 0, len(weighted))
	for i, w := range weighted {
		if !cooled[w.config.Provider] {
			notCooled = append(notCooled, i)
		} else {
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelInfo, fmt.Sprintf("Provider scoring: skipping cooled provider %s for auto routing", w.config.Provider))
		}
	}

	if len(notCooled) == 0 && len(weighted) > 0 {
		if virtualKey != nil && IsSystemPoolVirtualKeyName(virtualKey.Name) {
			ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelWarn, "Provider scoring: all system pool providers cooled down; fail-closed")
			setSystemPoolUnavailable(ctx, virtualKey.Name, model)
			return nil
		}
		ctx.AppendRoutingEngineLog(schemas.RoutingEngineGovernance, schemas.LogLevelWarn, "Provider scoring: all providers cooled down; fail-open using composite scores")
		return weighted
	}

	if len(notCooled) < len(weighted) {
		filtered := make([]weightedProviderConfig, 0, len(notCooled))
		for _, idx := range notCooled {
			filtered = append(filtered, weighted[idx])
		}
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].effectiveWeight > filtered[j].effectiveWeight
		})
		return filtered
	}

	sort.Slice(weighted, func(i, j int) bool {
		return weighted[i].effectiveWeight > weighted[j].effectiveWeight
	})
	return weighted
}
