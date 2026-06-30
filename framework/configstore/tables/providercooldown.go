package tables

import "time"

// TableProviderCooldown records a global provider-level routing cooldown.
type TableProviderCooldown struct {
	Provider             string    `gorm:"primaryKey;type:varchar(255)" json:"provider"`
	CooldownUntil        time.Time `gorm:"index;not null" json:"cooldown_until"`
	Reason               string    `gorm:"type:varchar(255)" json:"reason"`
	ErrorRate            float64   `gorm:"default:0" json:"error_rate"`
	ConsecutiveFailures  int       `gorm:"default:0" json:"consecutive_failures"`
	WindowSeconds        int       `gorm:"default:0" json:"window_seconds"`
	UpdatedAt            time.Time `gorm:"index;not null" json:"updated_at"`
}

func (TableProviderCooldown) TableName() string { return "config_provider_cooldown" }