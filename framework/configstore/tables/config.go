package tables

const (
	ConfigAdminUsernameKey = "admin_username"
	ConfigAdminPasswordKey = "admin_password"
	ConfigIsAuthEnabledKey = "is_auth_enabled"
	// ConfigComplexityAnalyzerConfigKey stores the persisted analyzer config JSON.
	ConfigComplexityAnalyzerConfigKey = "complexity_analyzer_config"
	ConfigRestartRequiredKey          = "restart_required"
	ConfigHeaderFilterKey             = "header_filter_config"
)

// Keys for the ClientConfig.MetadataJSON blob.
// These live inside the metadata JSON map on config_client, not as governance_config rows.
const (
	MetadataKeyOnboardingDismissed = "onboarding_dismissed"
)

// RestartRequiredConfig represents the restart required configuration
// This is set when a config change requires a server restart to take effect
type RestartRequiredConfig struct {
	Required bool   `json:"required"`
	Reason   string `json:"reason,omitempty"`
}

// GlobalHeaderFilterConfig represents global header filtering configuration
// for headers forwarded to LLM providers via the x-bf-eh-* prefix.
// Filter logic:
// - If allowlist is non-empty, only headers in the allowlist are forwarded
// - If denylist is non-empty, headers in the denylist are dropped
// - If both are non-empty, allowlist takes precedence first, then denylist filters the result
type GlobalHeaderFilterConfig struct {
	Allowlist []string `json:"allowlist,omitempty"` // If non-empty, only these headers are allowed
	Denylist  []string `json:"denylist,omitempty"`  // Headers to always block
}

// TableGovernanceConfig represents generic configuration key-value pairs
type TableGovernanceConfig struct {
	Key   string `gorm:"primaryKey;type:varchar(255)" json:"key"`
	Value string `gorm:"type:text" json:"value"`
}

// TableName sets the table name for each model
func (TableGovernanceConfig) TableName() string { return "governance_config" }
