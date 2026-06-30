package configstore

import (
	"context"
	"time"

	"github.com/maximhq/bifrost/framework/configstore/tables"
)

func (s *RDBConfigStore) GetActiveProviderCooldowns(ctx context.Context, now time.Time) ([]ProviderCooldownState, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var rows []tables.TableProviderCooldown
	if err := s.DB().WithContext(ctx).
		Where("cooldown_until > ?", now).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]ProviderCooldownState, 0, len(rows))
	for _, row := range rows {
		out = append(out, ProviderCooldownState{
			Provider:            row.Provider,
			CooldownUntil:       row.CooldownUntil,
			Reason:              row.Reason,
			ErrorRate:           row.ErrorRate,
			ConsecutiveFailures: row.ConsecutiveFailures,
			WindowSeconds:       row.WindowSeconds,
			UpdatedAt:           row.UpdatedAt,
		})
	}
	return out, nil
}

func (s *RDBConfigStore) UpsertProviderCooldown(ctx context.Context, state ProviderCooldownState) error {
	row := tables.TableProviderCooldown{
		Provider:            state.Provider,
		CooldownUntil:       state.CooldownUntil,
		Reason:              state.Reason,
		ErrorRate:           state.ErrorRate,
		ConsecutiveFailures: state.ConsecutiveFailures,
		WindowSeconds:       state.WindowSeconds,
		UpdatedAt:           state.UpdatedAt,
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = time.Now().UTC()
	}
	return s.DB().WithContext(ctx).Save(&row).Error
}