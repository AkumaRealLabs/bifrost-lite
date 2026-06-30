package configstore

import "time"

// ProviderCooldownState is the active cooldown record for a provider.
type ProviderCooldownState struct {
	Provider            string    `json:"provider"`
	CooldownUntil       time.Time `json:"cooldown_until"`
	Reason              string    `json:"reason"`
	ErrorRate           float64   `json:"error_rate"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	WindowSeconds       int       `json:"window_seconds"`
	UpdatedAt           time.Time `json:"updated_at"`
}
