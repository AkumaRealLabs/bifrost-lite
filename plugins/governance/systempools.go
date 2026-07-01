package governance

import (
	"context"
	"fmt"
	"sort"
	"strings"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
)

const (
	SystemPoolLow    = "gpt_low"
	SystemPoolStable = "gpt_stable"

	systemPoolLowMaxRMBPerDao    = 0.10
	systemPoolStableMaxRMBPerDao = 0.25
)

var systemPoolNames = []string{SystemPoolLow, SystemPoolStable}

func IsSystemPoolVirtualKeyName(name string) bool {
	switch strings.TrimSpace(name) {
	case SystemPoolLow, SystemPoolStable:
		return true
	default:
		return false
	}
}

func SystemPoolRule(name string) string {
	switch name {
	case SystemPoolLow:
		return "0 < price_rmb_per_dao <= 0.1"
	case SystemPoolStable:
		return "0 < price_rmb_per_dao <= 0.25"
	default:
		return ""
	}
}

func SystemPoolForPrice(price float64) string {
	switch {
	case price > 0 && price <= systemPoolLowMaxRMBPerDao:
		return SystemPoolLow
	case price > 0 && price <= systemPoolStableMaxRMBPerDao:
		return SystemPoolStable
	default:
		return ""
	}
}

func systemPoolMaxPrice(name string) (float64, bool) {
	switch name {
	case SystemPoolLow:
		return systemPoolLowMaxRMBPerDao, true
	case SystemPoolStable:
		return systemPoolStableMaxRMBPerDao, true
	default:
		return 0, false
	}
}

func providerHasEnabledKey(provider configstore.ProviderConfig) bool {
	for _, key := range provider.Keys {
		if key.Enabled == nil || *key.Enabled {
			return true
		}
	}
	return false
}

func providerAllowsText(provider configstore.ProviderConfig) bool {
	allowed := (*schemas.AllowedRequests)(nil)
	if provider.CustomProviderConfig != nil {
		allowed = provider.CustomProviderConfig.AllowedRequests
	}
	return allowed == nil ||
		allowed.TextCompletion ||
		allowed.TextCompletionStream ||
		allowed.ChatCompletion ||
		allowed.ChatCompletionStream ||
		allowed.Responses ||
		allowed.ResponsesStream
}

func buildSystemPoolProviderConfigs(poolName string, providers map[schemas.ModelProvider]configstore.ProviderConfig) []configstoreTables.TableVirtualKeyProviderConfig {
	maxPrice, ok := systemPoolMaxPrice(poolName)
	if !ok {
		return nil
	}
	weight := 1.0
	out := make([]configstoreTables.TableVirtualKeyProviderConfig, 0)
	for providerName, provider := range providers {
		price, ok := parseProviderPriceRMBPerDao(provider.Description)
		if !ok || price > maxPrice || !providerHasEnabledKey(provider) || !providerAllowsText(provider) {
			continue
		}
		out = append(out, configstoreTables.TableVirtualKeyProviderConfig{
			Provider:      string(providerName),
			Weight:        &weight,
			AllowedModels: schemas.WhiteList{"*"},
			AllowAllKeys:  true,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out
}

func BuildSystemPoolProviderConfigsForAPI(poolName string, providers map[schemas.ModelProvider]configstore.ProviderConfig) []configstoreTables.TableVirtualKeyProviderConfig {
	return buildSystemPoolProviderConfigs(poolName, providers)
}

func applySystemPoolProviderConfigs(vk *configstoreTables.TableVirtualKey, providers map[schemas.ModelProvider]configstore.ProviderConfig) *configstoreTables.TableVirtualKey {
	if vk == nil || !IsSystemPoolVirtualKeyName(vk.Name) {
		return vk
	}
	clone := *vk
	clone.ProviderConfigs = buildSystemPoolProviderConfigs(vk.Name, providers)
	return &clone
}

func (p *GovernancePlugin) systemPoolProviderConfigs(vk *configstoreTables.TableVirtualKey) []configstoreTables.TableVirtualKeyProviderConfig {
	if vk == nil || !IsSystemPoolVirtualKeyName(vk.Name) || p.inMemoryStore == nil {
		return nil
	}
	return buildSystemPoolProviderConfigs(vk.Name, p.inMemoryStore.GetConfiguredProviders())
}

func (p *GovernancePlugin) virtualKeyForRouting(vk *configstoreTables.TableVirtualKey) *configstoreTables.TableVirtualKey {
	configs := p.systemPoolProviderConfigs(vk)
	if configs == nil {
		return vk
	}
	clone := *vk
	clone.ProviderConfigs = configs
	return &clone
}

func (r *BudgetResolver) virtualKeyForEvaluation(vk *configstoreTables.TableVirtualKey) *configstoreTables.TableVirtualKey {
	if vk == nil || !IsSystemPoolVirtualKeyName(vk.Name) || r.governanceInMemoryStore == nil {
		return vk
	}
	return applySystemPoolProviderConfigs(vk, r.governanceInMemoryStore.GetConfiguredProviders())
}

func setSystemPoolUnavailable(ctx *schemas.BifrostContext, poolName, model string) {
	if ctx == nil {
		return
	}
	statusCode := 503
	errType := "system_pool_unavailable"
	ctx.SetValue(schemas.BifrostContextKeyPreRequestShortCircuitError, &schemas.BifrostError{
		Type:       &errType,
		StatusCode: &statusCode,
		Error: &schemas.ErrorField{
			Message: fmt.Sprintf("No eligible providers in system pool %s for model %s", poolName, model),
		},
	})
}

func ensureSystemPoolVirtualKeys(ctx context.Context, store configstore.ConfigStore, config *configstore.GovernanceConfig) error {
	if store != nil {
		existing, err := store.GetVirtualKeys(ctx)
		if err != nil {
			return err
		}
		byName := make(map[string]bool, len(existing))
		for _, vk := range existing {
			byName[vk.Name] = true
		}
		for _, name := range systemPoolNames {
			if byName[name] {
				continue
			}
			vk := &configstoreTables.TableVirtualKey{
				ID:          fmt.Sprintf("system-%s", name),
				Name:        name,
				Value:       GenerateVirtualKey(),
				Description: fmt.Sprintf("System managed auto pool: %s", SystemPoolRule(name)),
				IsActive:    bifrost.Ptr(true),
			}
			if err := store.CreateVirtualKey(ctx, vk); err != nil {
				return err
			}
		}
		return nil
	}
	if config == nil {
		return nil
	}
	byName := make(map[string]bool, len(config.VirtualKeys))
	for _, vk := range config.VirtualKeys {
		byName[vk.Name] = true
	}
	for _, name := range systemPoolNames {
		if byName[name] {
			continue
		}
		config.VirtualKeys = append(config.VirtualKeys, configstoreTables.TableVirtualKey{
			ID:          fmt.Sprintf("system-%s", name),
			Name:        name,
			Value:       GenerateVirtualKey(),
			Description: fmt.Sprintf("System managed auto pool: %s", SystemPoolRule(name)),
			IsActive:    bifrost.Ptr(true),
		})
	}
	return nil
}
