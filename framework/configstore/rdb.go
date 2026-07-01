package configstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	bifrost "github.com/maximhq/bifrost/core"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/encrypt"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/framework/queryscope"
	"github.com/maximhq/bifrost/framework/vectorstore"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RDBConfigStore represents a configuration store that uses a relational database.
//
// The runtime *gorm.DB is held behind an atomic.Pointer so RefreshConnectionPool
// can swap it out without tearing callers down. migrateOnFreshFn and refreshPoolFn
// are backend-specific hooks installed by the constructor (postgres vs sqlite).
type RDBConfigStore struct {
	db               atomic.Pointer[gorm.DB]
	logger           schemas.Logger
	migrateOnFreshFn func(ctx context.Context, fn func(context.Context, *gorm.DB) error) error
	refreshPoolFn    func(ctx context.Context) error
}

// getWeight safely dereferences a *float64 weight pointer, returning 1.0 as default if nil.
// This allows distinguishing between "not set" (nil -> 1.0) and "explicitly set to 0" (0.0).
func getWeight(w *float64) float64 {
	if w == nil {
		return 1.0
	}
	return *w
}

// sortedProviderNames returns provider names in deterministic order for write paths.
func sortedProviderNames(providers map[schemas.ModelProvider]ProviderConfig) []schemas.ModelProvider {
	names := make([]schemas.ModelProvider, 0, len(providers))
	for provider := range providers {
		names = append(names, provider)
	}
	sort.Slice(names, func(i, j int) bool {
		return string(names[i]) < string(names[j])
	})
	return names
}

// sortedUintCopy returns a sorted copy of ids without mutating the caller's slice.
func sortedUintCopy(ids []uint) []uint {
	sorted := append([]uint(nil), ids...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted
}

// sortTableKeysByID sorts table keys by stable database identity for deterministic writes.
func sortTableKeysByID(keys []tables.TableKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ID == keys[j].ID {
			return keys[i].KeyID < keys[j].KeyID
		}
		return keys[i].ID < keys[j].ID
	})
}

// dbForUpdate adds a PostgreSQL row-level update lock to the query.
func dbForUpdate(db *gorm.DB) *gorm.DB {
	if db.Dialector.Name() != "postgres" {
		return db
	}
	return db.Clauses(clause.Locking{Strength: "UPDATE"})
}

// lockBudgetOwner locks the owning governance parent before mutating a budget row.
func lockBudgetOwner(ctx context.Context, txDB *gorm.DB, budget tables.TableBudget) error {
	switch {
	case budget.VirtualKeyID != nil && *budget.VirtualKeyID != "":
		var vk tables.TableVirtualKey
		if err := dbForUpdate(txDB.WithContext(ctx)).First(&vk, "id = ?", *budget.VirtualKeyID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
	case budget.ProviderConfigID != nil:
		var providerConfig tables.TableVirtualKeyProviderConfig
		if err := dbForUpdate(txDB.WithContext(ctx)).First(&providerConfig, "id = ?", *budget.ProviderConfigID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
	case budget.TeamID != nil && *budget.TeamID != "":
		var team tables.TableTeam
		if err := dbForUpdate(txDB.WithContext(ctx)).First(&team, "id = ?", *budget.TeamID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
	case budget.CustomerID != nil && *budget.CustomerID != "":
		var customer tables.TableCustomer
		if err := dbForUpdate(txDB.WithContext(ctx)).First(&customer, "id = ?", *budget.CustomerID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
	}
	return nil
}

func toolSyncIntervalDurationToStoredSeconds(interval time.Duration) (int, error) {
	if interval < 0 {
		return 0, fmt.Errorf("tool_sync_interval must be non-negative, got %q", interval.String())
	}
	if interval%time.Second != 0 {
		return 0, fmt.Errorf("tool_sync_interval must be a whole number of seconds, got %q", interval.String())
	}
	return int(interval / time.Second), nil
}

// schemaKeyFromTableKey converts a database key to a schema key.
func schemaKeyFromTableKey(dbKey tables.TableKey) schemas.Key {
	return schemas.Key{
		ID:                 dbKey.KeyID,
		Name:               dbKey.Name,
		Value:              dbKey.Value,
		Models:             dbKey.Models,
		BlacklistedModels:  dbKey.BlacklistedModels,
		Weight:             getWeight(dbKey.Weight),
		Enabled:            dbKey.Enabled,
		UseForBatchAPI:     dbKey.UseForBatchAPI,
		AzureKeyConfig:     dbKey.AzureKeyConfig,
		VertexKeyConfig:    dbKey.VertexKeyConfig,
		BedrockKeyConfig:   dbKey.BedrockKeyConfig,
		Aliases:            dbKey.Aliases,
		VLLMKeyConfig:      dbKey.VLLMKeyConfig,
		ReplicateKeyConfig: dbKey.ReplicateKeyConfig,
		OllamaKeyConfig:    dbKey.OllamaKeyConfig,
		SGLKeyConfig:       dbKey.SGLKeyConfig,
		ConfigHash:         dbKey.ConfigHash,
		Status:             schemas.KeyStatusType(dbKey.Status),
		Description:        dbKey.Description,
	}
}

// tableKeyFromSchemaKey converts a schema key to a database key.
func tableKeyFromSchemaKey(provider tables.TableProvider, key schemas.Key) (tables.TableKey, error) {
	dbKey := tables.TableKey{
		Provider:           provider.Name,
		ProviderID:         provider.ID,
		KeyID:              key.ID,
		Name:               key.Name,
		Value:              key.Value,
		Models:             key.Models,
		BlacklistedModels:  key.BlacklistedModels,
		Weight:             &key.Weight,
		Enabled:            key.Enabled,
		UseForBatchAPI:     key.UseForBatchAPI,
		AzureKeyConfig:     key.AzureKeyConfig,
		VertexKeyConfig:    key.VertexKeyConfig,
		BedrockKeyConfig:   key.BedrockKeyConfig,
		Aliases:            key.Aliases,
		VLLMKeyConfig:      key.VLLMKeyConfig,
		ReplicateKeyConfig: key.ReplicateKeyConfig,
		OllamaKeyConfig:    key.OllamaKeyConfig,
		SGLKeyConfig:       key.SGLKeyConfig,
		ConfigHash:         key.ConfigHash,
		Status:             string(key.Status),
		Description:        key.Description,
	}

	if key.AzureKeyConfig != nil {
		dbKey.AzureEndpoint = &key.AzureKeyConfig.Endpoint
	}

	if key.VertexKeyConfig != nil {
		dbKey.VertexProjectID = &key.VertexKeyConfig.ProjectID
		dbKey.VertexProjectNumber = &key.VertexKeyConfig.ProjectNumber
		dbKey.VertexRegion = &key.VertexKeyConfig.Region
		dbKey.VertexAuthCredentials = &key.VertexKeyConfig.AuthCredentials
	}

	if key.BedrockKeyConfig != nil {
		dbKey.BedrockAccessKey = &key.BedrockKeyConfig.AccessKey
		dbKey.BedrockSecretKey = &key.BedrockKeyConfig.SecretKey
		dbKey.BedrockSessionToken = key.BedrockKeyConfig.SessionToken
		dbKey.BedrockRegion = key.BedrockKeyConfig.Region
		dbKey.BedrockARN = key.BedrockKeyConfig.ARN
		dbKey.BedrockRoleARN = key.BedrockKeyConfig.RoleARN
		dbKey.BedrockExternalID = key.BedrockKeyConfig.ExternalID
		dbKey.BedrockRoleSessionName = key.BedrockKeyConfig.RoleSessionName
		if key.BedrockKeyConfig.BatchS3Config != nil {
			data, err := sonic.Marshal(key.BedrockKeyConfig.BatchS3Config)
			if err != nil {
				return tables.TableKey{}, err
			}
			s := string(data)
			dbKey.BedrockBatchS3ConfigJSON = &s
		}
	}

	return dbKey, nil
}

// UpdateClientConfig updates the client configuration in the database.
func (s *RDBConfigStore) UpdateClientConfig(ctx context.Context, config *ClientConfig) error {
	var ttfbRoutingJSON string
	var providerScoringJSON string
	if config.TTFBRouting != nil {
		data, err := sonic.Marshal(config.TTFBRouting)
		if err != nil {
			return fmt.Errorf("failed to serialize ttfb routing config: %w", err)
		}
		ttfbRoutingJSON = string(data)
	}
	if config.ProviderScoring != nil {
		data, err := sonic.Marshal(config.ProviderScoring)
		if err != nil {
			return fmt.Errorf("failed to serialize provider scoring config: %w", err)
		}
		providerScoringJSON = string(data)
	}

	dbConfig := tables.TableClientConfig{
		DropExcessRequests:                    config.DropExcessRequests,
		InitialPoolSize:                       config.InitialPoolSize,
		EnableLogging:                         config.EnableLogging,
		DisableContentLogging:                 config.DisableContentLogging,
		DisableDBPingsInHealth:                config.DisableDBPingsInHealth,
		DumpErrorsInConsoleLogs:               config.DumpErrorsInConsoleLogs,
		LogRetentionDays:                      config.LogRetentionDays,
		EnforceAuthOnInference:                config.EnforceAuthOnInference,
		EnforceGovernanceHeader:               config.EnforceGovernanceHeader,
		EnforceSCIMAuth:                       config.EnforceSCIMAuth,
		PrometheusLabels:                      config.PrometheusLabels,
		AllowedOrigins:                        config.AllowedOrigins,
		AllowedHeaders:                        config.AllowedHeaders,
		MaxRequestBodySizeMB:                  config.MaxRequestBodySizeMB,
		CompatConvertTextToChat:               config.Compat.ConvertTextToChat,
		CompatConvertChatToResponses:          config.Compat.ConvertChatToResponses,
		CompatShouldDropParams:                config.Compat.ShouldDropParams,
		CompatShouldConvertParams:             config.Compat.ShouldConvertParams,
		RequiredHeaders:                       config.RequiredHeaders,
		LoggingHeaders:                        config.LoggingHeaders,
		WhitelistedRoutes:                     config.WhitelistedRoutes,
		HideDeletedVirtualKeysInFilters:       config.HideDeletedVirtualKeysInFilters,
		RoutingChainMaxDepth:                  config.RoutingChainMaxDepth,
		TTFBRoutingJSON:                       ttfbRoutingJSON,
		ProviderScoringJSON:                   providerScoringJSON,
		HeaderFilterConfig:                    config.HeaderFilterConfig,
		AllowPerRequestContentStorageOverride: config.AllowPerRequestContentStorageOverride,
		AllowPerRequestRawOverride:            config.AllowPerRequestRawOverride,
		AllowDirectKeys:                       config.AllowDirectKeys,
		ConfigHash:                            config.ConfigHash,
	}
	// Delete existing client config and create new one in a transaction.
	// MetadataJSON is preserved here because Metadata is a UI/admin-preferences
	// blob that is NOT part of the API-facing ClientConfig (so config.json sync
	// can never set it). Reading it inside the transaction before DELETE keeps
	// callers from clobbering UI prefs on every config write.
	return s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing tables.TableClientConfig
		if err := dbForUpdate(tx.Select("metadata_json")).First(&existing).Error; err == nil {
			dbConfig.MetadataJSON = existing.MetadataJSON
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&tables.TableClientConfig{}).Error; err != nil {
			return err
		}
		return tx.Create(&dbConfig).Error
	})
}

// Ping checks if the database is reachable.
func (s *RDBConfigStore) Ping(ctx context.Context) error {
	return s.DB().WithContext(ctx).Exec("SELECT 1").Error
}

// DB returns the current runtime database connection. The returned pointer is
// only valid for the duration of the caller's operation — after a
// RefreshConnectionPool call, future DB() calls return a fresh *gorm.DB backed
// by a different *sql.DB pool. Callers that issue multiple operations should
// call DB() per operation rather than caching the pointer.
func (s *RDBConfigStore) DB() *gorm.DB {
	return s.db.Load()
}

// ScopedDB returns the DB bound to ctx with any QueryScope on ctx
// pre-applied. Use this in read paths that should respect caller-
// driven row visibility. Use DB().WithContext(ctx) for writes and for
// internal lookups (e.g. inference VK auth) that must bypass scoping.
func (s *RDBConfigStore) ScopedDB(ctx context.Context) *gorm.DB {
	db := s.DB().WithContext(ctx)
	if scope := queryscope.FromContext(ctx); scope != nil {
		db = scope(db)
	}
	return db
}

// RunMigration opens a throwaway connection against the same
// backing database, invokes fn with it, and closes the connection. Use this
// for DDL that must not leave cached prepared-statement plans on the runtime
// pool. After fn returns, callers should invoke RefreshConnectionPool if the
// migration altered tables the runtime pool has already queried.
//
// For SQLite, the throwaway concept doesn't apply (no server-side plan cache,
// single-writer file lock), so this runs fn against the existing *gorm.DB.
//
// Returns an error if the store was constructed without a migration hook
// wired — e.g. a direct `&RDBConfigStore{}` literal that skipped the
// newPostgresConfigStore / newSqliteConfigStore constructor. An explicit
// error is safer than a silent fallback to the runtime pool: running DDL
// on the runtime pool would reintroduce SQLSTATE 0A000.
func (s *RDBConfigStore) RunMigration(ctx context.Context, fn func(context.Context, *gorm.DB) error) error {
	if s.migrateOnFreshFn == nil {
		return fmt.Errorf("configstore: migration hook is not configured; construct the store via newPostgresConfigStore or newSqliteConfigStore")
	}
	return s.migrateOnFreshFn(ctx, fn)
}

// RefreshConnectionPool closes the runtime pool and opens a fresh one against
// the same configuration. In-flight queries on the old pool complete before
// it closes; subsequent DB() calls return the new pool, whose connections
// carry no cached plans. SQLite is a no-op.
//
// Returns an error if the store was constructed without a refresh hook wired
// (same rationale as RunMigration).
func (s *RDBConfigStore) RefreshConnectionPool(ctx context.Context) error {
	if s.refreshPoolFn == nil {
		return fmt.Errorf("configstore: refresh hook is not configured; construct the store via newPostgresConfigStore or newSqliteConfigStore")
	}
	return s.refreshPoolFn(ctx)
}

// parseGormError parses GORM errors to provide user-friendly error messages.
// Currently handles unique constraint violations and is designed to be extended
// for other error types in the future (e.g., foreign key violations, not null constraints).
func (s *RDBConfigStore) parseGormError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	errMsg := err.Error()
	// Check for unique constraint violations
	// SQLite format: "UNIQUE constraint failed: table_name.column_name"
	// PostgreSQL format: "ERROR: duplicate key value violates unique constraint"

	if strings.Contains(errMsg, "UNIQUE constraint failed") ||
		strings.Contains(errMsg, "duplicate key value violates unique constraint") {

		// Extract column name from error message
		var columnName string

		// SQLite: extract from "UNIQUE constraint failed: table.column"
		if strings.Contains(errMsg, "UNIQUE constraint failed") {
			parts := strings.Split(errMsg, "UNIQUE constraint failed:")
			if len(parts) > 1 {
				tableColumn := strings.TrimSpace(parts[1])
				// Extract column name after the last dot
				if dotIndex := strings.LastIndex(tableColumn, "."); dotIndex != -1 {
					columnName = tableColumn[dotIndex+1:]
				} else {
					columnName = tableColumn
				}
			}
		} else if strings.Contains(errMsg, "duplicate key value violates unique constraint") {
			// PostgreSQL: try to extract from constraint name or detail
			// Example: duplicate key value violates unique constraint "idx_key_name"
			// Detail: Key (name)=(value) already exists.

			// First try to extract from Detail
			if strings.Contains(errMsg, "Key (") {
				startIdx := strings.Index(errMsg, "Key (")
				if startIdx != -1 {
					rest := errMsg[startIdx+5:]
					endIdx := strings.Index(rest, ")")
					if endIdx != -1 {
						columnName = rest[:endIdx]
					}
				}
			}
			// If not found, try to parse from constraint name
			if columnName == "" {
				// Extract constraint name
				if strings.Contains(errMsg, `"`) {
					parts := strings.Split(errMsg, `"`)
					if len(parts) >= 2 {
						constraintName := parts[1]
						// Remove idx_ prefix and try to extract column name
						if strings.HasPrefix(constraintName, "idx_") {
							constraintName = constraintName[4:]
							// Find the last underscore to get column name
							if lastUnderscore := strings.LastIndex(constraintName, "_"); lastUnderscore != -1 {
								columnName = constraintName[lastUnderscore+1:]
							} else {
								columnName = constraintName
							}
						}
					}
				}
			}
		}
		// Clean up column name (remove underscores, convert to readable format)
		if columnName != "" {
			// Convert snake_case to space-separated words
			columnName = strings.ReplaceAll(columnName, "_", " ")
			// For config_keys.name uniqueness violations, give a more specific error message.
			// Scope to config_keys specifically (SQLite: "config_keys.name",
			// PostgreSQL: constraint "idx_key_name") to avoid matching other tables like
			// governance_teams.name or config_plugins.name.
			if strings.Contains(errMsg, "config_keys.name") || strings.Contains(errMsg, "idx_key_name") {
				return fmt.Errorf("API key names must be unique across providers. A key with this name %w. Rename it in the UI or config.json", ErrAlreadyExists)
			}
			return fmt.Errorf("a record with this %s %w. Please use a different value", columnName, ErrAlreadyExists)
		}
		// Fallback message if we couldn't parse the column name
		return fmt.Errorf("a record with this value %w. Please use a different value", ErrAlreadyExists)
	}

	// For other errors, return the original error
	// Future: add handling for foreign key violations, not null constraints, etc.
	return err
}

// UpdateFrameworkConfig updates the framework configuration in the database.
func (s *RDBConfigStore) UpdateFrameworkConfig(ctx context.Context, config *tables.TableFrameworkConfig) error {
	// Update the framework configuration
	return s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&tables.TableFrameworkConfig{}).Error; err != nil {
			return err
		}
		return tx.Create(config).Error
	})
}

// GetFrameworkConfig retrieves the framework configuration from the database.
func (s *RDBConfigStore) GetFrameworkConfig(ctx context.Context) (*tables.TableFrameworkConfig, error) {
	var dbConfig tables.TableFrameworkConfig
	if err := s.DB().WithContext(ctx).First(&dbConfig).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &dbConfig, nil
}

// GetClientConfig retrieves the client configuration from the database.
func (s *RDBConfigStore) GetClientConfig(ctx context.Context) (*ClientConfig, error) {
	var dbConfig tables.TableClientConfig
	if err := s.DB().WithContext(ctx).First(&dbConfig).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var ttfbRouting *TTFBRoutingConfig
	var providerScoring *ProviderScoringConfig
	if dbConfig.TTFBRoutingJSON != "" {
		var config TTFBRoutingConfig
		if err := sonic.Unmarshal([]byte(dbConfig.TTFBRoutingJSON), &config); err != nil {
			return nil, fmt.Errorf("failed to parse ttfb routing config: %w", err)
		}
		ttfbRouting = &config
	}
	if dbConfig.ProviderScoringJSON != "" {
		var config ProviderScoringConfig
		if err := sonic.Unmarshal([]byte(dbConfig.ProviderScoringJSON), &config); err != nil {
			return nil, fmt.Errorf("failed to parse provider scoring config: %w", err)
		}
		providerScoring = &config
	}

	return &ClientConfig{
		DropExcessRequests:      dbConfig.DropExcessRequests,
		InitialPoolSize:         dbConfig.InitialPoolSize,
		PrometheusLabels:        dbConfig.PrometheusLabels,
		EnableLogging:           dbConfig.EnableLogging,
		DisableContentLogging:   dbConfig.DisableContentLogging,
		DisableDBPingsInHealth:  dbConfig.DisableDBPingsInHealth,
		DumpErrorsInConsoleLogs: dbConfig.DumpErrorsInConsoleLogs,
		LogRetentionDays:        dbConfig.LogRetentionDays,
		EnforceAuthOnInference:  dbConfig.EnforceAuthOnInference,
		EnforceGovernanceHeader: dbConfig.EnforceGovernanceHeader,
		EnforceSCIMAuth:         dbConfig.EnforceSCIMAuth,
		AllowedOrigins:          dbConfig.AllowedOrigins,
		AllowedHeaders:          dbConfig.AllowedHeaders,
		MaxRequestBodySizeMB:    dbConfig.MaxRequestBodySizeMB,
		Compat: CompatConfig{
			ConvertTextToChat:      dbConfig.CompatConvertTextToChat,
			ConvertChatToResponses: dbConfig.CompatConvertChatToResponses,
			ShouldDropParams:       dbConfig.CompatShouldDropParams,
			ShouldConvertParams:    dbConfig.CompatShouldConvertParams,
		},
		RequiredHeaders:                       dbConfig.RequiredHeaders,
		LoggingHeaders:                        dbConfig.LoggingHeaders,
		WhitelistedRoutes:                     dbConfig.WhitelistedRoutes,
		HideDeletedVirtualKeysInFilters:       dbConfig.HideDeletedVirtualKeysInFilters,
		RoutingChainMaxDepth:                  dbConfig.RoutingChainMaxDepth,
		TTFBRouting:                           ttfbRouting,
		ProviderScoring:                       providerScoring,
		HeaderFilterConfig:                    dbConfig.HeaderFilterConfig,
		AllowPerRequestContentStorageOverride: dbConfig.AllowPerRequestContentStorageOverride,
		AllowPerRequestRawOverride:            dbConfig.AllowPerRequestRawOverride,
		AllowDirectKeys:                       dbConfig.AllowDirectKeys,
		ConfigHash:                            dbConfig.ConfigHash,
	}, nil
}

// GetClientMetadata returns the UI/admin-preferences blob stored on config_client.
// Returns an empty (non-nil) map if no row exists yet or the blob is unset, so
// callers can read keys without nil-checking.
func (s *RDBConfigStore) GetClientMetadata(ctx context.Context) (map[string]any, error) {
	var dbConfig tables.TableClientConfig
	if err := s.DB().WithContext(ctx).First(&dbConfig).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	if dbConfig.Metadata == nil {
		return map[string]any{}, nil
	}
	return dbConfig.Metadata, nil
}

// mergeMetadataPatch applies patch into dst following JSON Merge Patch
// semantics (RFC 7386): a nil patch value deletes the key; when both the
// existing value and the patch value are objects they are merged recursively;
// any other value replaces the existing one. dst is mutated in place.
func mergeMetadataPatch(dst, patch map[string]any) {
	for k, v := range patch {
		if v == nil {
			delete(dst, k)
			continue
		}
		patchObj, patchIsObj := v.(map[string]any)
		dstObj, dstIsObj := dst[k].(map[string]any)
		if patchIsObj && dstIsObj {
			mergeMetadataPatch(dstObj, patchObj)
			continue
		}
		dst[k] = v
	}
}

// UpdateClientMetadata merges patch into the existing metadata blob and writes
// it back via a targeted UPDATE on metadata_json only — no DELETE+CREATE, no
// risk of clobbering other ClientConfig columns. The merge follows JSON Merge
// Patch semantics (RFC 7386): nested objects are merged recursively, and keys
// with a nil value in patch are removed from the blob (callers can pass
// {"key": nil} to clear, including nested keys).
func (s *RDBConfigStore) UpdateClientMetadata(ctx context.Context, patch map[string]any) error {
	return s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing tables.TableClientConfig
		if err := dbForUpdate(tx).First(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: client config must be initialized before metadata can be updated", ErrNotFound)
			}
			return err
		}
		merged := existing.Metadata
		if merged == nil {
			merged = map[string]any{}
		}
		mergeMetadataPatch(merged, patch)
		data, mErr := providerUtils.MarshalSorted(merged)
		if mErr != nil {
			return mErr
		}
		result := tx.Model(&tables.TableClientConfig{}).Where("id = ?", existing.ID).Update("metadata_json", string(data))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("client config metadata update affected no rows")
		}
		return nil
	})
}

// UpdateProvidersConfig updates the client configuration in the database.
func (s *RDBConfigStore) UpdateProvidersConfig(ctx context.Context, providers map[schemas.ModelProvider]ProviderConfig, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	// Pre-fetch governance FK references for all existing providers in one query.
	// ProviderConfig carries no governance fields, so without this the upsert
	// below would write NULL into budget_id/rate_limit_id on every startup.
	// If the columns don't exist yet, the fetch simply returns nothing
	governanceFKs := make(map[string]tables.TableProvider)
	var existingProviders []tables.TableProvider
	providerTableName := tables.TableProvider{}.TableName()

	if s.doesColumnExist(ctx, providerTableName, "budget_id") &&
		s.doesColumnExist(ctx, providerTableName, "rate_limit_id") {
		if err := txDB.WithContext(ctx).
			Select("name", "budget_id", "rate_limit_id").
			Find(&existingProviders).Error; err != nil {
			return fmt.Errorf("failed to prefetch provider governance fks: %w", err)
		}
		for _, p := range existingProviders {
			governanceFKs[p.Name] = p
		}
	}

	for _, providerName := range sortedProviderNames(providers) {
		providerConfig := providers[providerName]
		dbProvider := tables.TableProvider{
			Name:                     string(providerName),
			NetworkConfig:            providerConfig.NetworkConfig,
			ConcurrencyAndBufferSize: providerConfig.ConcurrencyAndBufferSize,
			SendBackRawRequest:       providerConfig.SendBackRawRequest,
			SendBackRawResponse:      providerConfig.SendBackRawResponse,
			StoreRawRequestResponse:  providerConfig.StoreRawRequestResponse,
			CustomProviderConfig:     providerConfig.CustomProviderConfig,
			OpenAIConfig:             providerConfig.OpenAIConfig,
			ConfigHash:               providerConfig.ConfigHash,
			Status:                   providerConfig.Status,
			Description:              providerConfig.Description,
			StatusDescription:        providerConfig.StatusDescription,
		}

		// Carry over governance FKs from the existing row so UpdateAll never
		// overwrites them with NULL. New providers (not in governanceFKs) correctly
		// start with nil governance — governance is never set via the file sync path.
		if existing, ok := governanceFKs[string(providerName)]; ok {
			dbProvider.BudgetID = existing.BudgetID
			dbProvider.RateLimitID = existing.RateLimitID
		}

		// Upsert provider (create or update if exists).
		if err := txDB.WithContext(ctx).Clauses(
			clause.OnConflict{
				Columns:   []clause.Column{{Name: "name"}},
				UpdateAll: true,
			},
			clause.Returning{Columns: []clause.Column{{Name: "id"}}},
		).Create(&dbProvider).Error; err != nil {
			return s.parseGormError(err)
		}

		// Create keys for this provider
		dbKeys := make([]tables.TableKey, 0, len(providerConfig.Keys))
		for _, key := range providerConfig.Keys {
			// Use existing ConfigHash if set (came from reconciliation with DB),
			// otherwise generate new hash (new key from config.json)
			keyHash := key.ConfigHash
			if keyHash == "" {
				var err error
				keyHash, err = GenerateKeyHash(key)
				if err != nil {
					return fmt.Errorf("failed to generate key hash: %w", err)
				}
			}
			dbKey := tables.TableKey{
				Provider:           dbProvider.Name,
				ProviderID:         dbProvider.ID,
				KeyID:              key.ID,
				Name:               key.Name,
				Value:              key.Value,
				Models:             key.Models,
				BlacklistedModels:  key.BlacklistedModels,
				Weight:             &key.Weight,
				Enabled:            key.Enabled,
				UseForBatchAPI:     key.UseForBatchAPI,
				AzureKeyConfig:     key.AzureKeyConfig,
				VertexKeyConfig:    key.VertexKeyConfig,
				BedrockKeyConfig:   key.BedrockKeyConfig,
				Aliases:            key.Aliases,
				VLLMKeyConfig:      key.VLLMKeyConfig,
				ReplicateKeyConfig: key.ReplicateKeyConfig,
				OllamaKeyConfig:    key.OllamaKeyConfig,
				SGLKeyConfig:       key.SGLKeyConfig,
				ConfigHash:         keyHash,
				Status:             string(key.Status),
				Description:        key.Description,
			}

			// Handle Azure config
			if key.AzureKeyConfig != nil {
				dbKey.AzureEndpoint = &key.AzureKeyConfig.Endpoint
			}

			// Handle Vertex config
			if key.VertexKeyConfig != nil {
				dbKey.VertexProjectID = &key.VertexKeyConfig.ProjectID
				dbKey.VertexProjectNumber = &key.VertexKeyConfig.ProjectNumber
				dbKey.VertexRegion = &key.VertexKeyConfig.Region
				dbKey.VertexAuthCredentials = &key.VertexKeyConfig.AuthCredentials
			}

			// Handle Bedrock config
			if key.BedrockKeyConfig != nil {
				dbKey.BedrockAccessKey = &key.BedrockKeyConfig.AccessKey
				dbKey.BedrockSecretKey = &key.BedrockKeyConfig.SecretKey
				dbKey.BedrockSessionToken = key.BedrockKeyConfig.SessionToken
				dbKey.BedrockRegion = key.BedrockKeyConfig.Region
				dbKey.BedrockARN = key.BedrockKeyConfig.ARN
				dbKey.BedrockRoleARN = key.BedrockKeyConfig.RoleARN
				dbKey.BedrockExternalID = key.BedrockKeyConfig.ExternalID
				dbKey.BedrockRoleSessionName = key.BedrockKeyConfig.RoleSessionName
				if key.BedrockKeyConfig.BatchS3Config != nil {
					data, err := sonic.Marshal(key.BedrockKeyConfig.BatchS3Config)
					if err != nil {
						return err
					}
					s := string(data)
					dbKey.BedrockBatchS3ConfigJSON = &s
				}
			} else {
				dbKey.BedrockBatchS3ConfigJSON = nil
			}

			dbKeys = append(dbKeys, dbKey)
		}

		// Upsert keys to handle duplicates properly
		for _, dbKey := range dbKeys {
			// First try to find existing key by KeyID
			var existingKey tables.TableKey
			result := txDB.WithContext(ctx).Where("key_id = ?", dbKey.KeyID).First(&existingKey)

			if result.Error == nil {
				// Update existing key with new data
				dbKey.ID = existingKey.ID                             // Keep the same database ID
				dbKey.ProviderID = existingKey.ProviderID             // Preserve the existing ProviderID
				dbKey.Enabled = existingKey.Enabled                   // Preserve the existing Enabled status
				dbKey.Status = existingKey.Status                     // Preserve status (UI-managed)
				dbKey.Description = existingKey.Description           // Preserve description (UI-managed)
				dbKey.EncryptionStatus = existingKey.EncryptionStatus // Preserve encryption status
				dbKey.CreatedAt = existingKey.CreatedAt               // Preserve original creation timestamp
				if err := txDB.WithContext(ctx).Save(&dbKey).Error; err != nil {
					return s.parseGormError(err)
				}
			} else if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				// KeyID not found, try fallback lookup by Name (handles config reload with new UUID)
				result = txDB.WithContext(ctx).Where("name = ?", dbKey.Name).First(&existingKey)
				if result.Error == nil {
					// Found by name - update existing key, preserve original KeyID
					dbKey.ID = existingKey.ID                             // Keep the same database ID
					dbKey.KeyID = existingKey.KeyID                       // Preserve original KeyID
					dbKey.ProviderID = existingKey.ProviderID             // Preserve the existing ProviderID
					dbKey.Enabled = existingKey.Enabled                   // Preserve the existing Enabled status
					dbKey.Status = existingKey.Status                     // Preserve status (UI-managed)
					dbKey.Description = existingKey.Description           // Preserve description (UI-managed)
					dbKey.EncryptionStatus = existingKey.EncryptionStatus // Preserve encryption status
					dbKey.CreatedAt = existingKey.CreatedAt               // Preserve original creation timestamp
					if err := txDB.WithContext(ctx).Save(&dbKey).Error; err != nil {
						return s.parseGormError(err)
					}
				} else if errors.Is(result.Error, gorm.ErrRecordNotFound) {
					// Neither KeyID nor Name found - create new key
					if err := txDB.WithContext(ctx).Create(&dbKey).Error; err != nil {
						return s.parseGormError(err)
					}
				} else {
					// Other error occurred during name lookup
					return result.Error
				}
			} else {
				// Other error occurred
				return result.Error
			}
		}
	}
	return nil
}

// deleteJoinRowsForRemovedProviderKeys removes join-table entries that reference keys
// that are being deleted by UpdateProvider. The caller MUST have already locked the
// supplied VKPC rows (FOR UPDATE) before calling, so this helper performs no locking
// of its own. This keeps the resource order config_providers -> VKPC -> config_keys
// consistent with DeleteProvider and UpdateVirtualKeyProviderConfig.
func (s *RDBConfigStore) deleteJoinRowsForRemovedProviderKeys(ctx context.Context, txDB *gorm.DB, lockedVKPCs []tables.TableVirtualKeyProviderConfig, removedKeyIDs []uint) error {
	if len(removedKeyIDs) == 0 || len(lockedVKPCs) == 0 {
		return nil
	}

	for _, providerConfig := range lockedVKPCs {
		if err := txDB.WithContext(ctx).
			Table("governance_virtual_key_provider_config_keys").
			Where("table_virtual_key_provider_config_id = ? AND table_key_id IN ?", providerConfig.ID, removedKeyIDs).
			Delete(nil).Error; err != nil {
			return err
		}
	}

	return nil
}

func (s *RDBConfigStore) cleanupVirtualKeyProviderConfigsForDeletedProvider(ctx context.Context, txDB *gorm.DB, provider string) error {
	var providerConfigIDs []uint
	if err := dbForUpdate(txDB.WithContext(ctx)).
		Model(&tables.TableVirtualKeyProviderConfig{}).
		Where("provider = ?", provider).
		Order("id ASC").
		Pluck("id", &providerConfigIDs).Error; err != nil {
		return err
	}

	for _, providerConfigID := range sortedUintCopy(providerConfigIDs) {
		if err := s.DeleteVirtualKeyProviderConfig(ctx, providerConfigID, txDB); err != nil {
			return err
		}
	}

	return nil
}

// UpdateProvider updates a single provider configuration in the database without deleting/recreating.
func (s *RDBConfigStore) UpdateProvider(ctx context.Context, provider schemas.ModelProvider, config ProviderConfig, tx ...*gorm.DB) error {
	if len(tx) == 0 {
		return s.DB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return s.UpdateProvider(ctx, provider, config, transaction)
		})
	}

	var txDB *gorm.DB
	txDB = tx[0]
	// Find the existing provider
	var dbProvider tables.TableProvider
	if err := dbForUpdate(txDB.WithContext(ctx)).Where("name = ?", string(provider)).First(&dbProvider).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}

	// Create a deep copy of the config to avoid modifying the original
	configCopy, err := deepCopy(config)
	if err != nil {
		return err
	}
	// Preserve ConfigHash (it has json:"-" tag so deepCopy via JSON doesn't copy it)
	configCopy.ConfigHash = config.ConfigHash
	// Update provider fields
	dbProvider.NetworkConfig = configCopy.NetworkConfig
	dbProvider.ConcurrencyAndBufferSize = configCopy.ConcurrencyAndBufferSize
	dbProvider.SendBackRawRequest = configCopy.SendBackRawRequest
	dbProvider.SendBackRawResponse = configCopy.SendBackRawResponse
	dbProvider.StoreRawRequestResponse = configCopy.StoreRawRequestResponse
	dbProvider.CustomProviderConfig = configCopy.CustomProviderConfig
	dbProvider.OpenAIConfig = configCopy.OpenAIConfig
	dbProvider.ConfigHash = configCopy.ConfigHash
	dbProvider.Description = configCopy.Description

	// Save the updated provider
	if err := txDB.WithContext(ctx).Save(&dbProvider).Error; err != nil {
		return s.parseGormError(err)
	}

	// Lock VKPC rows for this provider BEFORE locking config_keys so that the
	// resource order matches DeleteProvider and concurrent UpdateVirtualKeyProviderConfig
	// (which holds a VKPC row and then needs FK locks on config_keys via the join table).
	// Without this pre-lock the two paths invert on config_keys vs. VKPC and deadlock (40P01).
	var providerVKPCs []tables.TableVirtualKeyProviderConfig
	if err := dbForUpdate(txDB.WithContext(ctx)).
		Where("provider = ?", dbProvider.Name).
		Order("id ASC").
		Find(&providerVKPCs).Error; err != nil {
		return err
	}

	// Get existing keys for this provider
	var existingKeys []tables.TableKey
	if err := dbForUpdate(txDB.WithContext(ctx)).Where("provider_id = ?", dbProvider.ID).Order("id ASC").Find(&existingKeys).Error; err != nil {
		return err
	}

	// Create a map of existing keys by KeyID for quick lookup
	existingKeysMap := make(map[string]tables.TableKey)
	for _, key := range existingKeys {
		existingKeysMap[key.KeyID] = key
	}

	// Process each key in the new config
	for _, key := range configCopy.Keys {
		// Generate key hash
		keyHash, err := GenerateKeyHash(key)
		if err != nil {
			return fmt.Errorf("failed to generate key hash: %w", err)
		}
		dbKey := tables.TableKey{
			Provider:           dbProvider.Name,
			ProviderID:         dbProvider.ID,
			KeyID:              key.ID,
			Name:               key.Name,
			Value:              key.Value,
			Models:             key.Models,
			BlacklistedModels:  key.BlacklistedModels,
			Weight:             &key.Weight,
			Enabled:            key.Enabled,
			UseForBatchAPI:     key.UseForBatchAPI,
			AzureKeyConfig:     key.AzureKeyConfig,
			VertexKeyConfig:    key.VertexKeyConfig,
			BedrockKeyConfig:   key.BedrockKeyConfig,
			Aliases:            key.Aliases,
			VLLMKeyConfig:      key.VLLMKeyConfig,
			ReplicateKeyConfig: key.ReplicateKeyConfig,
			OllamaKeyConfig:    key.OllamaKeyConfig,
			SGLKeyConfig:       key.SGLKeyConfig,
			ConfigHash:         keyHash,
			Status:             string(key.Status),
			Description:        key.Description,
		}

		// Handle Azure config
		if key.AzureKeyConfig != nil {
			dbKey.AzureEndpoint = &key.AzureKeyConfig.Endpoint
		}

		// Handle Vertex config
		if key.VertexKeyConfig != nil {
			dbKey.VertexProjectID = &key.VertexKeyConfig.ProjectID
			dbKey.VertexProjectNumber = &key.VertexKeyConfig.ProjectNumber
			dbKey.VertexRegion = &key.VertexKeyConfig.Region
			dbKey.VertexAuthCredentials = &key.VertexKeyConfig.AuthCredentials
		}

		// Handle Bedrock config
		if key.BedrockKeyConfig != nil {
			dbKey.BedrockAccessKey = &key.BedrockKeyConfig.AccessKey
			dbKey.BedrockSecretKey = &key.BedrockKeyConfig.SecretKey
			dbKey.BedrockSessionToken = key.BedrockKeyConfig.SessionToken
			dbKey.BedrockRegion = key.BedrockKeyConfig.Region
			dbKey.BedrockARN = key.BedrockKeyConfig.ARN
			dbKey.BedrockRoleARN = key.BedrockKeyConfig.RoleARN
			dbKey.BedrockExternalID = key.BedrockKeyConfig.ExternalID
			dbKey.BedrockRoleSessionName = key.BedrockKeyConfig.RoleSessionName
			if key.BedrockKeyConfig.BatchS3Config != nil {
				data, err := sonic.Marshal(key.BedrockKeyConfig.BatchS3Config)
				if err != nil {
					return err
				}
				s := string(data)
				dbKey.BedrockBatchS3ConfigJSON = &s
			} else {
				dbKey.BedrockBatchS3ConfigJSON = nil
			}
		}

		// Check if this key already exists
		if existingKey, exists := existingKeysMap[key.ID]; exists {
			dbKey.ID = existingKey.ID                             // Keep the same database ID
			dbKey.ConfigHash = existingKey.ConfigHash             // Preserve config hash
			dbKey.Status = existingKey.Status                     // Preserve status (UI-managed)
			dbKey.Description = existingKey.Description           // Preserve description (UI-managed)
			dbKey.EncryptionStatus = existingKey.EncryptionStatus // Preserve encryption status
			dbKey.CreatedAt = existingKey.CreatedAt               // Preserve original creation timestamp
			if err := txDB.WithContext(ctx).Save(&dbKey).Error; err != nil {
				return s.parseGormError(err)
			}
			delete(existingKeysMap, key.ID)
		} else {
			if err := txDB.WithContext(ctx).Create(&dbKey).Error; err != nil {
				return s.parseGormError(err)
			}
		}
	}

	removedProviderKeyIDs := make([]uint, 0, len(existingKeysMap))
	for _, keyToDelete := range existingKeysMap {
		removedProviderKeyIDs = append(removedProviderKeyIDs, keyToDelete.ID)
	}
	removedProviderKeyIDs = sortedUintCopy(removedProviderKeyIDs)
	if err := s.deleteJoinRowsForRemovedProviderKeys(ctx, txDB, providerVKPCs, removedProviderKeyIDs); err != nil {
		return err
	}

	// Delete keys that are no longer in the new config
	removedKeys := make([]tables.TableKey, 0, len(existingKeysMap))
	for _, keyToDelete := range existingKeysMap {
		removedKeys = append(removedKeys, keyToDelete)
	}
	sortTableKeysByID(removedKeys)
	for _, keyToDelete := range removedKeys {
		if err := txDB.WithContext(ctx).Delete(&keyToDelete).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
	}

	return nil
}

// AddProvider creates a new provider configuration in the database.
func (s *RDBConfigStore) AddProvider(ctx context.Context, provider schemas.ModelProvider, config ProviderConfig, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	// Create a deep copy of the config to avoid modifying the original
	configCopy, err := deepCopy(config)
	if err != nil {
		return err
	}
	// Preserve ConfigHash (it has json:"-" tag so deepCopy via JSON doesn't copy it)
	configCopy.ConfigHash = config.ConfigHash
	// Create new provider
	dbProvider := tables.TableProvider{
		Name:                     string(provider),
		NetworkConfig:            configCopy.NetworkConfig,
		ConcurrencyAndBufferSize: configCopy.ConcurrencyAndBufferSize,
		SendBackRawRequest:       configCopy.SendBackRawRequest,
		SendBackRawResponse:      configCopy.SendBackRawResponse,
		StoreRawRequestResponse:  configCopy.StoreRawRequestResponse,
		CustomProviderConfig:     configCopy.CustomProviderConfig,
		OpenAIConfig:             configCopy.OpenAIConfig,
		ConfigHash:               configCopy.ConfigHash,
	}
	// Create the provider
	if err := txDB.WithContext(ctx).Create(&dbProvider).Error; err != nil {
		return s.parseGormError(err)
	}
	// Create keys for this provider
	for _, key := range configCopy.Keys {
		dbKey := tables.TableKey{
			Provider:           dbProvider.Name,
			ProviderID:         dbProvider.ID,
			KeyID:              key.ID,
			Name:               key.Name,
			Value:              key.Value,
			Models:             key.Models,
			BlacklistedModels:  key.BlacklistedModels,
			Weight:             &key.Weight,
			Enabled:            key.Enabled,
			UseForBatchAPI:     key.UseForBatchAPI,
			AzureKeyConfig:     key.AzureKeyConfig,
			VertexKeyConfig:    key.VertexKeyConfig,
			BedrockKeyConfig:   key.BedrockKeyConfig,
			Aliases:            key.Aliases,
			VLLMKeyConfig:      key.VLLMKeyConfig,
			ReplicateKeyConfig: key.ReplicateKeyConfig,
			OllamaKeyConfig:    key.OllamaKeyConfig,
			SGLKeyConfig:       key.SGLKeyConfig,
			ConfigHash:         key.ConfigHash,
			Status:             string(key.Status),
			Description:        key.Description,
		}
		// Handle Azure config
		if key.AzureKeyConfig != nil {
			dbKey.AzureEndpoint = &key.AzureKeyConfig.Endpoint
		}
		// Handle Vertex config
		if key.VertexKeyConfig != nil {
			dbKey.VertexProjectID = &key.VertexKeyConfig.ProjectID
			dbKey.VertexProjectNumber = &key.VertexKeyConfig.ProjectNumber
			dbKey.VertexRegion = &key.VertexKeyConfig.Region
			dbKey.VertexAuthCredentials = &key.VertexKeyConfig.AuthCredentials
		}
		// Handle Bedrock config
		if key.BedrockKeyConfig != nil {
			dbKey.BedrockAccessKey = &key.BedrockKeyConfig.AccessKey
			dbKey.BedrockSecretKey = &key.BedrockKeyConfig.SecretKey
			dbKey.BedrockSessionToken = key.BedrockKeyConfig.SessionToken
			dbKey.BedrockRegion = key.BedrockKeyConfig.Region
			dbKey.BedrockARN = key.BedrockKeyConfig.ARN
			dbKey.BedrockRoleARN = key.BedrockKeyConfig.RoleARN
			dbKey.BedrockExternalID = key.BedrockKeyConfig.ExternalID
			dbKey.BedrockRoleSessionName = key.BedrockKeyConfig.RoleSessionName
			if key.BedrockKeyConfig.BatchS3Config != nil {
				data, err := sonic.Marshal(key.BedrockKeyConfig.BatchS3Config)
				if err != nil {
					return err
				}
				s := string(data)
				dbKey.BedrockBatchS3ConfigJSON = &s
			} else {
				dbKey.BedrockBatchS3ConfigJSON = nil
			}
		}

		// Create the key
		if err := txDB.WithContext(ctx).Create(&dbKey).Error; err != nil {
			return s.parseGormError(err)
		}
	}

	return nil
}

// DeleteProvider deletes a single provider and all its associated keys from the database.
func (s *RDBConfigStore) DeleteProvider(ctx context.Context, provider schemas.ModelProvider, tx ...*gorm.DB) error {
	if len(tx) == 0 {
		return s.DB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return s.DeleteProvider(ctx, provider, transaction)
		})
	}

	var txDB *gorm.DB
	txDB = tx[0]
	// Find the existing provider
	var dbProvider tables.TableProvider
	if err := dbForUpdate(txDB.WithContext(ctx)).Where("name = ?", string(provider)).First(&dbProvider).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}

	if err := s.cleanupVirtualKeyProviderConfigsForDeletedProvider(ctx, txDB, dbProvider.Name); err != nil {
		return err
	}

	// Store the budget and rate limit IDs before deleting
	budgetID := dbProvider.BudgetID
	rateLimitID := dbProvider.RateLimitID

	// Delete the provider first (keys will be deleted due to CASCADE constraint)
	if err := txDB.WithContext(ctx).Delete(&dbProvider).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}

	// Delete the budget if it exists
	if budgetID != nil {
		if err := txDB.WithContext(ctx).Delete(&tables.TableBudget{}, "id = ?", *budgetID).Error; err != nil {
			return err
		}
	}
	// Delete the rate limit if it exists
	if rateLimitID != nil {
		if err := txDB.WithContext(ctx).Delete(&tables.TableRateLimit{}, "id = ?", *rateLimitID).Error; err != nil {
			return err
		}
	}

	// Clean up model configs scoped to this provider (and their owned budgets/rate-limits).
	if err := s.deleteModelConfigsWhere(ctx, txDB, "provider = ?", string(provider)); err != nil {
		return err
	}

	return nil
}

// GetProvidersConfig retrieves the provider configuration from the database.
func (s *RDBConfigStore) GetProvidersConfig(ctx context.Context) (map[schemas.ModelProvider]ProviderConfig, error) {
	var dbProviders []tables.TableProvider
	if err := s.DB().WithContext(ctx).Preload("Keys").Find(&dbProviders).Error; err != nil {
		return nil, err
	}
	if len(dbProviders) == 0 {
		// No providers in database, auto-detect from environment
		return nil, nil
	}
	processedProviders := make(map[schemas.ModelProvider]ProviderConfig)
	for _, dbProvider := range dbProviders {
		provider := schemas.ModelProvider(dbProvider.Name)
		// Convert database keys to schemas.Key
		keys := make([]schemas.Key, len(dbProvider.Keys))
		for i, dbKey := range dbProvider.Keys {
			keys[i] = schemaKeyFromTableKey(dbKey)
		}
		providerConfig := ProviderConfig{
			Keys:                     keys,
			NetworkConfig:            dbProvider.NetworkConfig,
			ConcurrencyAndBufferSize: dbProvider.ConcurrencyAndBufferSize,
			SendBackRawRequest:       dbProvider.SendBackRawRequest,
			SendBackRawResponse:      dbProvider.SendBackRawResponse,
			StoreRawRequestResponse:  dbProvider.StoreRawRequestResponse,
			CustomProviderConfig:     dbProvider.CustomProviderConfig,
			OpenAIConfig:             dbProvider.OpenAIConfig,
			ConfigHash:               dbProvider.ConfigHash,
			Status:                   dbProvider.Status,
			Description:              dbProvider.Description,
			StatusDescription:        dbProvider.StatusDescription,
		}
		processedProviders[provider] = providerConfig
	}
	return processedProviders, nil
}

// GetProviderConfig retrieves the provider configuration from the database.
func (s *RDBConfigStore) GetProviderConfig(ctx context.Context, provider schemas.ModelProvider) (*ProviderConfig, error) {
	var dbProvider tables.TableProvider
	if err := s.DB().WithContext(ctx).Preload("Keys").Where("name = ?", string(provider)).First(&dbProvider).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	keys := make([]schemas.Key, len(dbProvider.Keys))
	for i, dbKey := range dbProvider.Keys {
		keys[i] = schemaKeyFromTableKey(dbKey)
	}
	return &ProviderConfig{
		Keys:                     keys,
		NetworkConfig:            dbProvider.NetworkConfig,
		ConcurrencyAndBufferSize: dbProvider.ConcurrencyAndBufferSize,
		SendBackRawRequest:       dbProvider.SendBackRawRequest,
		SendBackRawResponse:      dbProvider.SendBackRawResponse,
		StoreRawRequestResponse:  dbProvider.StoreRawRequestResponse,
		CustomProviderConfig:     dbProvider.CustomProviderConfig,
		OpenAIConfig:             dbProvider.OpenAIConfig,
		ConfigHash:               dbProvider.ConfigHash,
		Status:                   dbProvider.Status,
		Description:              dbProvider.Description,
		StatusDescription:        dbProvider.StatusDescription,
	}, nil
}

// GetProviderKeys retrieves all keys for a provider ordered by creation time.
func (s *RDBConfigStore) GetProviderKeys(ctx context.Context, provider schemas.ModelProvider) ([]schemas.Key, error) {
	var dbKeys []tables.TableKey
	result := s.DB().WithContext(ctx).
		Table("config_providers").
		Select("config_keys.*").
		Joins("LEFT JOIN config_keys ON config_keys.provider_id = config_providers.id").
		Where("config_providers.name = ?", string(provider)).
		Order("config_keys.created_at ASC").
		Scan(&dbKeys)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, ErrNotFound
	}
	if len(dbKeys) == 1 && dbKeys[0].ID == 0 && dbKeys[0].KeyID == "" {
		return []schemas.Key{}, nil
	}

	keys := make([]schemas.Key, 0, len(dbKeys))
	for _, dbKey := range dbKeys {
		if dbKey.ID == 0 && dbKey.KeyID == "" {
			continue
		}
		if err := dbKey.AfterFind(nil); err != nil {
			return nil, err
		}
		keys = append(keys, schemaKeyFromTableKey(dbKey))
	}

	return keys, nil
}

func (s *RDBConfigStore) getProviderKeyByName(ctx context.Context, txDB *gorm.DB, provider schemas.ModelProvider, keyID string) (*tables.TableKey, error) {
	var dbKey tables.TableKey
	if err := dbForUpdate(txDB.WithContext(ctx)).
		Table("config_keys").
		Select("config_keys.*").
		Joins("JOIN config_providers ON config_providers.id = config_keys.provider_id").
		Where("config_providers.name = ? AND config_keys.key_id = ?", string(provider), keyID).
		First(&dbKey).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &dbKey, nil
}

// GetProviderKey retrieves a single key for a provider.
func (s *RDBConfigStore) GetProviderKey(ctx context.Context, provider schemas.ModelProvider, keyID string) (*schemas.Key, error) {
	dbKey, err := s.getProviderKeyByName(ctx, s.DB(), provider, keyID)
	if err != nil {
		return nil, err
	}

	key := schemaKeyFromTableKey(*dbKey)
	return &key, nil
}

// CreateProviderKey creates a new key for an existing provider.
func (s *RDBConfigStore) CreateProviderKey(ctx context.Context, provider schemas.ModelProvider, key schemas.Key, tx ...*gorm.DB) error {
	if len(tx) == 0 {
		return s.DB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return s.CreateProviderKey(ctx, provider, key, transaction)
		})
	}

	var txDB *gorm.DB
	txDB = tx[0]
	var dbProvider tables.TableProvider
	if err := dbForUpdate(txDB.WithContext(ctx)).Where("name = ?", string(provider)).First(&dbProvider).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	dbKey, err := tableKeyFromSchemaKey(dbProvider, key)
	if err != nil {
		return err
	}
	if err := txDB.WithContext(ctx).Create(&dbKey).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// UpdateProviderKey updates a single key for an existing provider.
func (s *RDBConfigStore) UpdateProviderKey(ctx context.Context, provider schemas.ModelProvider, keyID string, key schemas.Key, tx ...*gorm.DB) error {
	if len(tx) == 0 {
		return s.DB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return s.UpdateProviderKey(ctx, provider, keyID, key, transaction)
		})
	}

	var txDB *gorm.DB
	txDB = tx[0]

	existingKey, err := s.getProviderKeyByName(ctx, txDB, provider, keyID)
	if err != nil {
		return err
	}

	dbKey, err := tableKeyFromSchemaKey(tables.TableProvider{
		ID:   existingKey.ProviderID,
		Name: existingKey.Provider,
	}, key)
	if err != nil {
		return err
	}
	dbKey.ID = existingKey.ID
	dbKey.KeyID = existingKey.KeyID
	dbKey.ProviderID = existingKey.ProviderID
	dbKey.Provider = existingKey.Provider
	dbKey.ConfigHash = existingKey.ConfigHash
	dbKey.EncryptionStatus = existingKey.EncryptionStatus
	dbKey.CreatedAt = existingKey.CreatedAt // Preserve original creation timestamp

	if err := txDB.WithContext(ctx).Save(&dbKey).Error; err != nil {
		return s.parseGormError(err)
	}

	return nil
}

// DeleteProviderKey deletes a single key for an existing provider.
func (s *RDBConfigStore) DeleteProviderKey(ctx context.Context, provider schemas.ModelProvider, keyID string, tx ...*gorm.DB) error {
	if len(tx) == 0 {
		return s.DB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return s.DeleteProviderKey(ctx, provider, keyID, transaction)
		})
	}

	var txDB *gorm.DB
	txDB = tx[0]

	var dbProvider tables.TableProvider
	if err := dbForUpdate(txDB.WithContext(ctx)).Where("name = ?", string(provider)).First(&dbProvider).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	var dbKey tables.TableKey
	if err := dbForUpdate(txDB.WithContext(ctx)).
		Where("provider_id = ? AND key_id = ?", dbProvider.ID, keyID).
		First(&dbKey).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	if err := txDB.WithContext(ctx).
		Table("governance_virtual_key_provider_config_keys").
		Where("table_key_id = ?", dbKey.ID).
		Delete(nil).Error; err != nil {
		return err
	}

	result := txDB.WithContext(ctx).Delete(&dbKey)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}

	return nil
}

// GetProviders retrieves all providers from the database with their governance relationships.
func (s *RDBConfigStore) GetProviders(ctx context.Context) ([]tables.TableProvider, error) {
	var providers []tables.TableProvider
	if err := s.DB().WithContext(ctx).Preload("Budget").Preload("RateLimit").Find(&providers).Error; err != nil {
		return nil, err
	}
	return providers, nil
}

// GetProvider retrieves a provider by name from the database with governance relationships.
func (s *RDBConfigStore) GetProvider(ctx context.Context, provider schemas.ModelProvider) (*tables.TableProvider, error) {
	var providerInfo tables.TableProvider
	if err := s.DB().WithContext(ctx).Preload("Budget").Preload("RateLimit").Where("name = ?", string(provider)).First(&providerInfo).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &providerInfo, nil
}

// GetProviderByName retrieves a provider by name from the database with governance relationships.
func (s *RDBConfigStore) GetProviderByName(ctx context.Context, name string) (*tables.TableProvider, error) {
	var provider tables.TableProvider
	if err := s.DB().WithContext(ctx).Preload("Budget").Preload("RateLimit").Where("name = ?", name).First(&provider).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &provider, nil
}

// UpdateStatus updates the status for either a key or provider.
// - If keyID is non-empty: updates the key's status (for keyed providers)
// - If keyID is empty and provider is non-empty: updates the provider's status (for keyless providers)
func (s *RDBConfigStore) UpdateStatus(ctx context.Context, provider schemas.ModelProvider, keyID string, status, description string) error {
	// Update key-level status (for keyed providers)
	if keyID != "" {
		result := s.DB().WithContext(ctx).
			Model(&tables.TableKey{}).
			Where("key_id = ?", keyID).
			Updates(map[string]interface{}{
				"status":      status,
				"description": description,
			})
		if result.Error != nil {
			return s.parseGormError(result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	}

	// Update provider-level status (for keyless providers)
	if provider != "" {
		result := s.DB().WithContext(ctx).
			Model(&tables.TableProvider{}).
			Where("name = ?", string(provider)).
			Updates(map[string]interface{}{
				"status":             status,
				"status_description": description,
			})
		if result.Error != nil {
			return s.parseGormError(result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	}

	return fmt.Errorf("either keyID or provider must be non-empty")
}

// GetVectorStoreConfig retrieves the vector store configuration from the database.
func (s *RDBConfigStore) GetVectorStoreConfig(ctx context.Context) (*vectorstore.Config, error) {
	var vectorStoreTableConfig tables.TableVectorStoreConfig
	if err := s.DB().WithContext(ctx).First(&vectorStoreTableConfig).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Return default cache configuration
			return nil, nil
		}
		return nil, err
	}
	return &vectorstore.Config{
		Enabled: vectorStoreTableConfig.Enabled,
		Config:  vectorStoreTableConfig.Config,
		Type:    vectorstore.VectorStoreType(vectorStoreTableConfig.Type),
	}, nil
}

// UpdateVectorStoreConfig updates the vector store configuration in the database.
func (s *RDBConfigStore) UpdateVectorStoreConfig(ctx context.Context, config *vectorstore.Config) error {
	return s.DB().Transaction(func(tx *gorm.DB) error {
		// Delete existing cache config
		if err := tx.WithContext(ctx).Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&tables.TableVectorStoreConfig{}).Error; err != nil {
			return err
		}
		jsonConfig, err := marshalToStringPtr(config.Config)
		if err != nil {
			return err
		}
		record := &tables.TableVectorStoreConfig{
			Type:    string(config.Type),
			Enabled: config.Enabled,
			Config:  jsonConfig,
		}
		// Create new cache config
		return tx.WithContext(ctx).Create(record).Error
	})
}

// GetLogsStoreConfig retrieves the logs store configuration from the database.
func (s *RDBConfigStore) GetLogsStoreConfig(ctx context.Context) (*logstore.Config, error) {
	var dbConfig tables.TableLogStoreConfig
	if err := s.DB().WithContext(ctx).First(&dbConfig).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if dbConfig.Config == nil || *dbConfig.Config == "" {
		return &logstore.Config{Enabled: dbConfig.Enabled}, nil
	}
	var logStoreConfig logstore.Config
	if err := json.Unmarshal([]byte(*dbConfig.Config), &logStoreConfig); err != nil {
		return nil, err
	}
	return &logStoreConfig, nil
}

// UpdateLogsStoreConfig updates the logs store configuration in the database.
func (s *RDBConfigStore) UpdateLogsStoreConfig(ctx context.Context, config *logstore.Config) error {
	return s.DB().Transaction(func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&tables.TableLogStoreConfig{}).Error; err != nil {
			return err
		}
		jsonConfig, err := marshalToStringPtr(config)
		if err != nil {
			return err
		}
		record := &tables.TableLogStoreConfig{
			Enabled: config.Enabled,
			Type:    string(config.Type),
			Config:  jsonConfig,
		}
		return tx.WithContext(ctx).Create(record).Error
	})
}

// GetConfig retrieves a specific config from the database.
func (s *RDBConfigStore) GetConfig(ctx context.Context, key string) (*tables.TableGovernanceConfig, error) {
	var config tables.TableGovernanceConfig
	if err := s.DB().WithContext(ctx).First(&config, "key = ?", key).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &config, nil
}

// UpdateConfig updates a specific config in the database.
func (s *RDBConfigStore) UpdateConfig(ctx context.Context, config *tables.TableGovernanceConfig, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	return txDB.WithContext(ctx).Save(config).Error
}

// GetModelPrices retrieves all model pricing records from the database.
func (s *RDBConfigStore) GetModelPrices(ctx context.Context) ([]tables.TableModelPricing, error) {
	var modelPrices []tables.TableModelPricing
	if err := s.DB().WithContext(ctx).Find(&modelPrices).Error; err != nil {
		return nil, err
	}
	return modelPrices, nil
}

// pricingSyncUpdateColumns is the explicit set of governance_model_pricing
// columns the pricing sync is allowed to overwrite via ON CONFLICT. Mirrors
// every column on TableModelPricing except `id` (the primary key) and
// `additional_attributes` (editorial metadata that must survive sync).
// Keep this list in lockstep with the table definition in
// framework/configstore/tables/modelpricing.go.
var pricingSyncUpdateColumns = []string{
	"model",
	"base_model",
	"provider",
	"mode",
	"context_length",
	"max_input_tokens",
	"max_output_tokens",
	"architecture",
	// Costs - Text
	"input_cost_per_token",
	"output_cost_per_token",
	"input_cost_per_token_batches",
	"output_cost_per_token_batches",
	"input_cost_per_token_priority",
	"output_cost_per_token_priority",
	"input_cost_per_token_flex",
	"output_cost_per_token_flex",
	"input_cost_per_token_fast",
	"output_cost_per_token_fast",
	"input_cost_per_character",
	// Costs - 128k Tier
	"input_cost_per_token_above_128k_tokens",
	"input_cost_per_image_above_128k_tokens",
	"input_cost_per_video_per_second_above_128k_tokens",
	"input_cost_per_audio_per_second_above_128k_tokens",
	"output_cost_per_token_above_128k_tokens",
	// Costs - 200k Tier
	"input_cost_per_token_above_200k_tokens",
	"input_cost_per_token_above_200k_tokens_priority",
	"output_cost_per_token_above_200k_tokens",
	"output_cost_per_token_above_200k_tokens_priority",
	// Costs - 272k Tier
	"input_cost_per_token_above_272k_tokens",
	"input_cost_per_token_above_272k_tokens_priority",
	"output_cost_per_token_above_272k_tokens",
	"output_cost_per_token_above_272k_tokens_priority",
	// Costs - Cache
	"cache_creation_input_token_cost",
	"cache_read_input_token_cost",
	"cache_creation_input_token_cost_above_200k_tokens",
	"cache_read_input_token_cost_above_200k_tokens",
	"cache_read_input_token_cost_above_200k_tokens_priority",
	"cache_creation_input_token_cost_above_1hr",
	"cache_creation_input_token_cost_above_1hr_above_200k_tokens",
	"cache_creation_input_audio_token_cost",
	"cache_read_input_token_cost_priority",
	"cache_read_input_token_cost_flex",
	"cache_read_input_image_token_cost",
	"cache_read_input_token_cost_above_272k_tokens",
	"cache_read_input_token_cost_above_272k_tokens_priority",
	// Costs - Image
	"input_cost_per_image",
	"input_cost_per_pixel",
	"output_cost_per_image",
	"output_cost_per_pixel",
	"output_cost_per_image_premium_image",
	"output_cost_per_image_above_512_and_512_pixels",
	"output_cost_per_image_above_512x512_pixels_premium",
	"output_cost_per_image_above_1024_and_1024_pixels",
	"output_cost_per_image_above_1024x1024_pixels_premium",
	"output_cost_per_image_above_2048_and_2048_pixels",
	"output_cost_per_image_above_4096_and_4096_pixels",
	"output_cost_per_image_low_quality",
	"output_cost_per_image_medium_quality",
	"output_cost_per_image_high_quality",
	"output_cost_per_image_auto_quality",
	"input_cost_per_image_token",
	"output_cost_per_image_token",
	// Costs - Audio/Video
	"input_cost_per_audio_token",
	"input_cost_per_audio_per_second",
	"input_cost_per_second",
	"input_cost_per_video_per_second",
	"output_cost_per_audio_token",
	"output_cost_per_video_per_second",
	"output_cost_per_second",
	// Costs - Other
	"search_context_cost_per_query",
	"code_interpreter_cost_per_session",
	// Costs - OCR
	"ocr_cost_per_page",
	"annotation_cost_per_page",
}

// UpsertModelPrices creates or updates a model pricing record in the database.
// Uses a single atomic ON CONFLICT statement to avoid deadlocks in multinode deployments
// where multiple nodes may attempt concurrent upserts for the same model on startup.
//
// The update list is intentionally explicit (pricingSyncUpdateColumns) rather
// than UpdateAll: every datasheet-sourced column is enumerated, but
// `additional_attributes` is omitted so the 24-hour pricing sync never
// overwrites editorial metadata set via UpsertModelPricingAttributes.
func (s *RDBConfigStore) UpsertModelPrices(ctx context.Context, pricing *tables.TableModelPricing, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	db := txDB.WithContext(ctx)

	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "model"}, {Name: "provider"}, {Name: "mode"}},
		DoUpdates: clause.AssignmentColumns(pricingSyncUpdateColumns),
	}).Create(pricing).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// UpsertModelPricingAttributes writes only the additional_attributes column
// for the pricing row keyed by (model, provider). The row must already exist
// — callers may not seed pricing rows through this path; the management API
// enforces that. A nil/empty attrs map clears the column to an empty JSON object.
func (s *RDBConfigStore) UpsertModelPricingAttributes(ctx context.Context, model, provider string, attrs map[string]string, tx ...*gorm.DB) (int64, error) {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	db := txDB.WithContext(ctx)

	var value string
	if len(attrs) == 0 {
		value = "{}"
	} else {
		encoded, err := json.Marshal(attrs)
		if err != nil {
			return 0, fmt.Errorf("marshal additional_attributes: %w", err)
		}
		value = string(encoded)
	}

	res := db.Model(&tables.TableModelPricing{}).
		Where("model = ? AND provider = ?", model, provider).
		Update("additional_attributes", value)
	if res.Error != nil {
		return 0, s.parseGormError(res.Error)
	}
	return res.RowsAffected, nil
}

// DeleteModelPrices deletes all model pricing records from the database.
func (s *RDBConfigStore) DeleteModelPrices(ctx context.Context, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	return txDB.WithContext(ctx).Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&tables.TableModelPricing{}).Error
}

func (s *RDBConfigStore) GetPricingOverrides(ctx context.Context, filters PricingOverrideFilters) ([]tables.TablePricingOverride, error) {
	var overrides []tables.TablePricingOverride
	q := s.DB().WithContext(ctx).Model(&tables.TablePricingOverride{})
	if filters.ScopeKind != nil {
		q = q.Where("scope_kind = ?", *filters.ScopeKind)
	}
	if filters.VirtualKeyID != nil {
		q = q.Where("virtual_key_id = ?", *filters.VirtualKeyID)
	}
	if filters.ProviderID != nil {
		q = q.Where("provider_id = ?", *filters.ProviderID)
	}
	if filters.ProviderKeyID != nil {
		q = q.Where("provider_key_id = ?", *filters.ProviderKeyID)
	}
	if err := q.Order("created_at ASC").Find(&overrides).Error; err != nil {
		return nil, s.parseGormError(err)
	}
	return overrides, nil
}

func (s *RDBConfigStore) GetPricingOverridesPaginated(ctx context.Context, params PricingOverridesQueryParams) ([]tables.TablePricingOverride, int64, error) {
	baseQuery := s.DB().WithContext(ctx).Model(&tables.TablePricingOverride{})

	if params.Search != "" {
		search := "%" + strings.ToLower(params.Search) + "%"
		baseQuery = baseQuery.Where("LOWER(name) LIKE ?", search)
	}
	if params.ScopeKind != nil {
		baseQuery = baseQuery.Where("scope_kind = ?", *params.ScopeKind)
	}
	if params.VirtualKeyID != nil {
		baseQuery = baseQuery.Where("virtual_key_id = ?", *params.VirtualKeyID)
	}
	if params.ProviderID != nil {
		baseQuery = baseQuery.Where("provider_id = ?", *params.ProviderID)
	}
	if params.ProviderKeyID != nil {
		baseQuery = baseQuery.Where("provider_key_id = ?", *params.ProviderKeyID)
	}

	var totalCount int64
	if err := baseQuery.Count(&totalCount).Error; err != nil {
		return nil, 0, err
	}

	limit := params.Limit
	offset := params.Offset

	if limit <= 0 {
		limit = 25
	} else if limit > 100 {
		limit = 100
	}

	if offset < 0 {
		offset = 0
	}

	var overrides []tables.TablePricingOverride
	if err := baseQuery.
		Order("created_at ASC").
		Offset(offset).
		Limit(limit).
		Find(&overrides).Error; err != nil {
		return nil, 0, s.parseGormError(err)
	}
	return overrides, totalCount, nil
}

func (s *RDBConfigStore) GetPricingOverrideByID(ctx context.Context, id string) (*tables.TablePricingOverride, error) {
	var override tables.TablePricingOverride
	if err := s.DB().WithContext(ctx).First(&override, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, s.parseGormError(err)
	}
	return &override, nil
}

func (s *RDBConfigStore) CreatePricingOverride(ctx context.Context, override *tables.TablePricingOverride, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	if err := txDB.WithContext(ctx).Create(override).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

func (s *RDBConfigStore) UpdatePricingOverride(ctx context.Context, override *tables.TablePricingOverride, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	if err := txDB.WithContext(ctx).Save(override).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

func (s *RDBConfigStore) DeletePricingOverride(ctx context.Context, id string, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	res := txDB.WithContext(ctx).Delete(&tables.TablePricingOverride{}, "id = ?", id)
	if res.Error != nil {
		return s.parseGormError(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// MODEL PARAMETERS METHODS

// GetModelParameters returns all stored model parameter rows.
func (s *RDBConfigStore) GetModelParameters(ctx context.Context) ([]tables.TableModelParameters, error) {
	var rows []tables.TableModelParameters
	if err := s.DB().WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// GetModelParametersByModel retrieves model parameters for a specific model.
func (s *RDBConfigStore) GetModelParametersByModel(ctx context.Context, model string) (*tables.TableModelParameters, error) {
	var params tables.TableModelParameters
	if err := s.DB().WithContext(ctx).Where("model = ?", model).First(&params).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &params, nil
}

// UpsertModelParameters inserts or updates model parameters for a specific model.
// Uses a single atomic ON CONFLICT statement to avoid deadlocks in multinode deployments
// where multiple nodes may attempt concurrent upserts for the same model on startup.
func (s *RDBConfigStore) UpsertModelParameters(ctx context.Context, params *tables.TableModelParameters, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	db := txDB.WithContext(ctx)

	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "model"}},
		UpdateAll: true,
	}).Create(params).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// PLUGINS METHODS

func (s *RDBConfigStore) GetPlugins(ctx context.Context) ([]*tables.TablePlugin, error) {
	var plugins []*tables.TablePlugin
	if err := s.DB().WithContext(ctx).Find(&plugins).Error; err != nil {
		return nil, err
	}
	return plugins, nil
}

func (s *RDBConfigStore) GetPlugin(ctx context.Context, name string) (*tables.TablePlugin, error) {
	var plugin tables.TablePlugin
	if err := s.DB().WithContext(ctx).First(&plugin, "name = ?", name).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &plugin, nil
}

// CreatePlugin creates a new plugin in the database.
func (s *RDBConfigStore) CreatePlugin(ctx context.Context, plugin *tables.TablePlugin, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	// Mark plugin as custom if path is not empty
	if plugin.Path != nil && strings.TrimSpace(*plugin.Path) != "" {
		plugin.IsCustom = true
	} else {
		plugin.IsCustom = false
	}
	if err := txDB.WithContext(ctx).Create(plugin).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// UpsertPlugin creates a new plugin in the database if it doesn't exist, otherwise updates it.
func (s *RDBConfigStore) UpsertPlugin(ctx context.Context, plugin *tables.TablePlugin, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	// Mark plugin as custom if path is not empty
	if plugin.Path != nil && strings.TrimSpace(*plugin.Path) != "" {
		plugin.IsCustom = true
	} else {
		plugin.IsCustom = false
	}
	// Check if plugin exists and compare versions
	// If the plugin exists and the version is lower, do nothing
	var existing tables.TablePlugin
	err := txDB.WithContext(ctx).Where("name = ?", plugin.Name).First(&existing).Error
	if err == nil {
		// Plugin exists, check version
		if plugin.Version < existing.Version {
			return nil
		}
	}
	// Upsert plugin (create or update if exists based on unique name)
	if err := txDB.WithContext(ctx).Clauses(
		clause.OnConflict{
			Columns:   []clause.Column{{Name: "name"}},
			UpdateAll: true,
		},
	).Create(plugin).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// UpdatePlugin updates an existing plugin in the database.
func (s *RDBConfigStore) UpdatePlugin(ctx context.Context, plugin *tables.TablePlugin, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	var localTx bool

	if len(tx) > 0 {
		txDB = tx[0]
		localTx = false
	} else {
		txDB = s.DB().Begin()
		localTx = true
	}
	// Mark plugin as custom if path is not empty
	if plugin.Path != nil && strings.TrimSpace(*plugin.Path) != "" {
		plugin.IsCustom = true
	} else {
		plugin.IsCustom = false
	}
	var existing tables.TablePlugin
	if err := txDB.WithContext(ctx).Where("name = ?", plugin.Name).First(&existing).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			if localTx {
				txDB.Rollback()
			}
			return err
		}
		// not found — nothing to delete
	} else {
		if err := txDB.WithContext(ctx).Delete(&existing).Error; err != nil {
			if localTx {
				txDB.Rollback()
			}
			return err
		}
	}
	if err := txDB.WithContext(ctx).Create(plugin).Error; err != nil {
		if localTx {
			txDB.Rollback()
		}
		return s.parseGormError(err)
	}
	if localTx {
		return txDB.Commit().Error
	}
	return nil
}

// DeletePlugin deletes a plugin from the database.
func (s *RDBConfigStore) DeletePlugin(ctx context.Context, name string, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	var plugin tables.TablePlugin
	if err := txDB.WithContext(ctx).Where("name = ?", name).First(&plugin).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	return txDB.WithContext(ctx).Delete(&plugin).Error
}

// GOVERNANCE METHODS

// GetRedactedVirtualKeys retrieves redacted virtual keys from the database.
func (s *RDBConfigStore) GetRedactedVirtualKeys(ctx context.Context, ids []string) ([]tables.TableVirtualKey, error) {
	var virtualKeys []tables.TableVirtualKey

	if len(ids) > 0 {
		err := s.DB().WithContext(ctx).Select("id, name, description, is_active").Where("id IN ?", ids).Find(&virtualKeys).Error
		if err != nil {
			return nil, err
		}
	} else {
		err := s.DB().WithContext(ctx).Select("id, name, description, is_active").Find(&virtualKeys).Error
		if err != nil {
			return nil, err
		}
	}
	return virtualKeys, nil
}

// preloadCustomerRelations preloads the customer relations for a virtual key.
func preloadCustomerRelations(db *gorm.DB, prefix string) *gorm.DB {
	relation := func(name string) string {
		if prefix == "" {
			return name
		}
		return prefix + name
	}
	return db.
		Preload(relation("Teams")).
		Preload(relation("Teams.Budgets")).
		Preload(relation("Budgets")).
		Preload(relation("RateLimit")).
		Preload(relation("VirtualKeys"))
}

// preloadVirtualKeyBaseRelations preloads the base relationships for a virtual key.
func preloadVirtualKeyBaseRelations(db *gorm.DB) *gorm.DB {
	return db.
		Preload("Team").
		Preload("Team.Customer").
		Preload("Customer").
		Preload("Budgets").
		Preload("RateLimit").
		Preload("ProviderConfigs").
		Preload("ProviderConfigs.Budgets").
		Preload("ProviderConfigs.RateLimit").
		Preload("ProviderConfigs.Keys", func(db *gorm.DB) *gorm.DB {
			return db.Select("id, name, key_id, models_json, provider")
		})
}

// preloadVirtualKeyDetailRelations preloads the detail relationships for a virtual key.
func preloadVirtualKeyDetailRelations(db *gorm.DB) *gorm.DB {
	return preloadCustomerRelations(preloadVirtualKeyBaseRelations(db), "Customer.")
}

// virtualKeyInternalPageSize is the bounded page size used when loading every
// virtual key with preloaded relationships. Keeping each page small avoids
// PostgreSQL's extended protocol parameter limit during GORM preloads.
const virtualKeyInternalPageSize = 1000

// modelConfigInternalPageSize bounds the page size when loading every model config
// with preloaded relationships, for the same reason as virtualKeyInternalPageSize:
// a single un-paginated Find with preloads generates an IN(...) clause with one
// bind parameter per row and exceeds PostgreSQL's 65535-parameter limit at scale.
const modelConfigInternalPageSize = 1000

// GetVirtualKeys retrieves all virtual keys from the database.
func (s *RDBConfigStore) GetVirtualKeys(ctx context.Context) ([]tables.TableVirtualKey, error) {
	var allVirtualKeys []tables.TableVirtualKey
	var lastCreatedAt time.Time
	var lastID string
	hasCursor := false

	start := time.Now()
	pageCount := 0
	defer func() {
		if s.logger != nil {
			s.logger.Info("[startup-timing] GetVirtualKeys loaded %d keys across %d pages in %v", len(allVirtualKeys), pageCount, time.Since(start))
		}
	}()

	for {
		virtualKeys, err := s.getVirtualKeysPage(ctx, virtualKeyInternalPageSize, lastCreatedAt, lastID, hasCursor)
		if err != nil {
			return nil, err
		}
		pageCount++
		if len(virtualKeys) == 0 {
			return allVirtualKeys, nil
		}

		allVirtualKeys = append(allVirtualKeys, virtualKeys...)
		last := virtualKeys[len(virtualKeys)-1]
		lastCreatedAt = last.CreatedAt
		lastID = last.ID
		hasCursor = true
		if len(virtualKeys) < virtualKeyInternalPageSize {
			return allVirtualKeys, nil
		}
	}
}

// getVirtualKeysPage retrieves one unfiltered page of virtual keys without a
// COUNT query for internal all-key loading paths.
func (s *RDBConfigStore) getVirtualKeysPage(ctx context.Context, limit int, lastCreatedAt time.Time, lastID string, hasCursor bool) ([]tables.TableVirtualKey, error) {
	var virtualKeys []tables.TableVirtualKey
	query := preloadVirtualKeyBaseRelations(s.ScopedDB(ctx))
	if hasCursor {
		query = query.Where(
			"(governance_virtual_keys.created_at > ? OR (governance_virtual_keys.created_at = ? AND governance_virtual_keys.id > ?))",
			lastCreatedAt,
			lastCreatedAt,
			lastID,
		)
	}
	if err := query.
		Order("governance_virtual_keys.created_at ASC, governance_virtual_keys.id ASC").
		Limit(limit).
		Find(&virtualKeys).Error; err != nil {
		return nil, err
	}
	return virtualKeys, nil
}

// getGovernanceConfigVirtualKeys loads every virtual key with the preloads needed
// by GetGovernanceConfig (ProviderConfigs and their Keys). It pages with a
// cursor so each preload's generated IN(...) clause stays within PostgreSQL's
// 65535 bind-parameter limit. A single un-paginated Find with these preloads
// fails once the key count exceeds ~65535 ("extended protocol limited to 65535
// parameters"). Mirrors GetVirtualKeys, which is paginated for the same reason.
func (s *RDBConfigStore) getGovernanceConfigVirtualKeys(ctx context.Context) ([]tables.TableVirtualKey, error) {
	var allVirtualKeys []tables.TableVirtualKey
	var lastCreatedAt time.Time
	var lastID string
	hasCursor := false

	for {
		var page []tables.TableVirtualKey
		query := s.DB().WithContext(ctx).
			Preload("ProviderConfigs").
			Preload("ProviderConfigs.Keys", func(db *gorm.DB) *gorm.DB {
				return db.Select("id, name, key_id, models_json, provider")
			})
		if hasCursor {
			query = query.Where(
				"(governance_virtual_keys.created_at > ? OR (governance_virtual_keys.created_at = ? AND governance_virtual_keys.id > ?))",
				lastCreatedAt,
				lastCreatedAt,
				lastID,
			)
		}
		if err := query.
			Order("governance_virtual_keys.created_at ASC, governance_virtual_keys.id ASC").
			Limit(virtualKeyInternalPageSize).
			Find(&page).Error; err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return allVirtualKeys, nil
		}
		allVirtualKeys = append(allVirtualKeys, page...)
		last := page[len(page)-1]
		lastCreatedAt = last.CreatedAt
		lastID = last.ID
		hasCursor = true
		if len(page) < virtualKeyInternalPageSize {
			return allVirtualKeys, nil
		}
	}
}

// GetVirtualKeysPaginated retrieves virtual keys with pagination, filtering, and search support.
func (s *RDBConfigStore) GetVirtualKeysPaginated(ctx context.Context, params VirtualKeyQueryParams) ([]tables.TableVirtualKey, int64, error) {
	// Build base query with filters
	// ScopedDB applies any caller-supplied row visibility before
	// per-call filters so the total count and the page result agree
	// on what the caller is allowed to see.
	baseQuery := s.ScopedDB(ctx).Model(&tables.TableVirtualKey{})

	// Virtual keys are either customer-scoped or team-scoped, never both.
	// When both filters are provided, use OR to match keys belonging to either.
	if params.CustomerID != "" && params.TeamID != "" {
		baseQuery = baseQuery.Where("(customer_id = ? OR team_id = ?)", params.CustomerID, params.TeamID)
	} else if params.CustomerID != "" {
		baseQuery = baseQuery.Where("customer_id = ?", params.CustomerID)
	} else if params.TeamID != "" {
		baseQuery = baseQuery.Where("team_id = ?", params.TeamID)
	}
	if params.Search != "" {
		search := "%" + strings.ToLower(params.Search) + "%"
		baseQuery = baseQuery.Where("LOWER(name) LIKE ?", search)
	}

	// Get total count before pagination
	var totalCount int64
	if err := baseQuery.Count(&totalCount).Error; err != nil {
		return nil, 0, err
	}

	// Apply pagination defaults
	limit := params.Limit
	if params.Export {
		// Export mode: allow large fetches, cap at 10000 as a safety net
		if limit <= 0 {
			limit = 10000
		}
		if limit > 10000 {
			limit = 10000
		}
	} else {
		if limit <= 0 {
			limit = 25
		}
		if limit > 100 {
			limit = 100
		}
	}

	offset := params.Offset
	if offset < 0 {
		offset = 0
	}

	// Determine sort order
	orderClause := "governance_virtual_keys.created_at ASC, governance_virtual_keys.id ASC"
	if params.SortBy != "" {
		dir := "ASC"
		if strings.EqualFold(params.Order, "desc") {
			dir = "DESC"
		}
		switch params.SortBy {
		case "name":
			orderClause = fmt.Sprintf("governance_virtual_keys.name %s, governance_virtual_keys.id ASC", dir)
		case "budget_spent":
			orderClause = fmt.Sprintf("COALESCE(vk_budget_totals.total_usage, 0) %s, governance_virtual_keys.id ASC", dir)
		case "created_at":
			orderClause = fmt.Sprintf("governance_virtual_keys.created_at %s, governance_virtual_keys.id ASC", dir)
		case "status":
			orderClause = fmt.Sprintf("governance_virtual_keys.is_active %s, governance_virtual_keys.id ASC", dir)
		}
	}

	// Fetch with preloads and pagination
	query := preloadVirtualKeyBaseRelations(baseQuery)
	if params.SortBy == "budget_spent" {
		// A virtual key can have multiple budgets (different reset intervals); take MAX so the
		// highest-spending budget drives the sort without duplicating rows.
		query = query.Joins(`LEFT JOIN (
			SELECT virtual_key_id, MAX(current_usage) AS total_usage
			FROM governance_budgets
			WHERE virtual_key_id IS NOT NULL
			GROUP BY virtual_key_id
		) AS vk_budget_totals ON vk_budget_totals.virtual_key_id = governance_virtual_keys.id`)
	}
	var virtualKeys []tables.TableVirtualKey
	if err := query.
		Order(orderClause).
		Offset(offset).
		Limit(limit).
		Find(&virtualKeys).Error; err != nil {
		return nil, 0, err
	}
	return virtualKeys, totalCount, nil
}

// GetVirtualKey retrieves a virtual key from the database.
//
// When ctx carries a QueryScope, the query is narrowed to rows the
// caller is allowed to see. A row that exists but falls outside the
// scope returns ErrNotFound, the same response a genuinely-missing
// row produces, so URL guessing cannot distinguish "hidden" from
// "absent".
func (s *RDBConfigStore) GetVirtualKey(ctx context.Context, id string) (*tables.TableVirtualKey, error) {
	var virtualKey tables.TableVirtualKey
	q := preloadVirtualKeyDetailRelations(s.ScopedDB(ctx))
	if err := q.First(&virtualKey, "governance_virtual_keys.id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &virtualKey, nil
}

// GetVirtualKeyByValue retrieves a virtual key by its value using hash-based lookup.
func (s *RDBConfigStore) GetVirtualKeyByValue(ctx context.Context, value string) (*tables.TableVirtualKey, error) {
	valueHash := encrypt.HashSHA256(value)
	var virtualKey tables.TableVirtualKey
	query := preloadVirtualKeyBaseRelations(s.DB().WithContext(ctx))
	// Use hash-based lookup if hash column is populated, fall back to plaintext for backward compat
	if err := query.Where("value_hash = ?", valueHash).First(&virtualKey).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Fallback: try plaintext lookup for rows not yet migrated
			if err := query.Where("value = ?", value).First(&virtualKey).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil, ErrNotFound
				}
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	return &virtualKey, nil
}

// GetVirtualKeyQuotaByValue retrieves budget, rate limit, and provider-level limit data for a virtual key.
// This is a lean query that avoids loading Team, Customer, and provider Keys.
func (s *RDBConfigStore) GetVirtualKeyQuotaByValue(ctx context.Context, value string) (*tables.TableVirtualKey, error) {
	valueHash := encrypt.HashSHA256(value)
	var virtualKey tables.TableVirtualKey
	baseQuery := s.DB().WithContext(ctx).
		Preload("Budgets").
		Preload("RateLimit").
		Preload("ProviderConfigs").
		Preload("ProviderConfigs.Budgets").
		Preload("ProviderConfigs.RateLimit")
	if err := baseQuery.Session(&gorm.Session{}).Where("value_hash = ?", valueHash).First(&virtualKey).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Fallback: try plaintext lookup for rows not yet migrated
			if err := baseQuery.Session(&gorm.Session{}).Where("value = ?", value).First(&virtualKey).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil, ErrNotFound
				}
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	return &virtualKey, nil
}

// CreateVirtualKey creates a new virtual key in the database.
func (s *RDBConfigStore) CreateVirtualKey(ctx context.Context, virtualKey *tables.TableVirtualKey, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	if err := txDB.WithContext(ctx).Create(virtualKey).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// UpdateVirtualKey updates an existing virtual key in the database.
func (s *RDBConfigStore) UpdateVirtualKey(ctx context.Context, virtualKey *tables.TableVirtualKey, tx ...*gorm.DB) error {
	if len(tx) == 0 {
		return s.DB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return s.UpdateVirtualKey(ctx, virtualKey, transaction)
		})
	}

	txDB := tx[0]

	// Check if record exists by ID or Name
	var existing tables.TableVirtualKey
	err := dbForUpdate(txDB.WithContext(ctx)).
		Where("id = ? OR name = ?", virtualKey.ID, virtualKey.Name).
		First(&existing).Error

	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return s.parseGormError(err)
	}

	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err := txDB.WithContext(ctx).Create(virtualKey).Error; err != nil {
			return s.parseGormError(err)
		}
	} else {
		virtualKey.ID = existing.ID
		if err := txDB.WithContext(ctx).
			Select("name", "description", "value", "is_active", "team_id", "customer_id", "rate_limit_id", "calendar_aligned", "config_hash", "updated_at", "encryption_status", "value_hash").
			Updates(virtualKey).Error; err != nil {
			return s.parseGormError(err)
		}
	}
	return nil
}

// GetKeysByIDs retrieves multiple keys by their IDs
func (s *RDBConfigStore) GetKeysByIDs(ctx context.Context, ids []string) ([]tables.TableKey, error) {
	if len(ids) == 0 {
		return []tables.TableKey{}, nil
	}
	var keys []tables.TableKey
	if err := s.DB().WithContext(ctx).Where("key_id IN ?", ids).Find(&keys).Error; err != nil {
		return nil, err
	}
	return keys, nil
}

// GetKeysByProvider retrieves all keys for a specific provider
func (s *RDBConfigStore) GetKeysByProvider(ctx context.Context, provider string) ([]tables.TableKey, error) {
	var keys []tables.TableKey
	if err := s.DB().WithContext(ctx).Where("provider = ?", provider).Find(&keys).Error; err != nil {
		return nil, err
	}
	return keys, nil
}

// GetAllRedactedKeys retrieves all redacted keys from the database.
func (s *RDBConfigStore) GetAllRedactedKeys(ctx context.Context, ids []string) ([]schemas.Key, error) {
	var keys []tables.TableKey
	if len(ids) > 0 {
		err := s.DB().WithContext(ctx).Select("id, key_id, name, models_json, blacklisted_models_json, weight").Where("key_id IN ?", ids).Find(&keys).Error
		if err != nil {
			return nil, err
		}
	} else {
		err := s.DB().WithContext(ctx).Select("id, key_id, name, models_json, blacklisted_models_json, weight").Find(&keys).Error
		if err != nil {
			return nil, err
		}
	}
	redactedKeys := make([]schemas.Key, len(keys))
	for i, key := range keys {
		models := key.Models
		if models == nil {
			models = []string{} // Ensure models is never nil in JSON response
		}
		blacklisted := key.BlacklistedModels
		if blacklisted == nil {
			blacklisted = []string{}
		}
		redactedKeys[i] = schemas.Key{
			ID:                key.KeyID,
			Name:              key.Name,
			Models:            models,
			BlacklistedModels: blacklisted,
			Weight:            getWeight(key.Weight),
		}
	}
	return redactedKeys, nil
}

// DeleteVirtualKey deletes a virtual key from the database.
func (s *RDBConfigStore) DeleteVirtualKey(ctx context.Context, id string, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	if err := txDB.WithContext(ctx).Transaction(func(txDB *gorm.DB) error {
		var virtualKey tables.TableVirtualKey
		if err := dbForUpdate(txDB.WithContext(ctx)).Preload("ProviderConfigs").First(&virtualKey, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}

		// Delete provider config resources before deleting the configs themselves
		var providerConfigRateLimitIDs []string
		sort.Slice(virtualKey.ProviderConfigs, func(i, j int) bool {
			return virtualKey.ProviderConfigs[i].ID < virtualKey.ProviderConfigs[j].ID
		})
		for _, pc := range virtualKey.ProviderConfigs {
			// Delete the keys join table entries
			if err := txDB.WithContext(ctx).Exec("DELETE FROM governance_virtual_key_provider_config_keys WHERE table_virtual_key_provider_config_id = ?", pc.ID).Error; err != nil {
				return err
			}
			// Delete budgets owned by this provider config
			if err := txDB.WithContext(ctx).Where("provider_config_id = ?", pc.ID).Delete(&tables.TableBudget{}).Error; err != nil {
				return err
			}
			if pc.RateLimitID != nil {
				providerConfigRateLimitIDs = append(providerConfigRateLimitIDs, *pc.RateLimitID)
			}
		}

		// Delete all provider configs associated with the virtual key
		if err := txDB.WithContext(ctx).Delete(&tables.TableVirtualKeyProviderConfig{}, "virtual_key_id = ?", id).Error; err != nil {
			return err
		}
		sort.Strings(providerConfigRateLimitIDs)
		for _, rateLimitID := range providerConfigRateLimitIDs {
			if err := txDB.WithContext(ctx).Delete(&tables.TableRateLimit{}, "id = ?", rateLimitID).Error; err != nil {
				return err
			}
		}
		// Delete budgets owned by this virtual key
		if err := txDB.WithContext(ctx).Where("virtual_key_id = ?", id).Delete(&tables.TableBudget{}).Error; err != nil {
			return err
		}
		// Delete model configs scoped to this virtual key, along with their owned
		// budgets/rate-limits. scope_id has no FK constraint, so this cleanup must be
		// explicit; otherwise per-VK model limits would orphan and leak budget/rate-limit rows.
		if err := s.DeleteModelConfigsForScope(ctx, txDB, tables.ModelConfigScopeVirtualKey, id); err != nil {
			return err
		}
		rateLimitID := virtualKey.RateLimitID
		// Delete the virtual key (use hydrated struct so AfterDelete vault cleanup fires correctly)
		if err := txDB.WithContext(ctx).Delete(&virtualKey).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		// Delete the rate limit associated with the virtual key
		if rateLimitID != nil {
			if err := txDB.WithContext(ctx).Delete(&tables.TableRateLimit{}, "id = ?", *rateLimitID).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// GetVirtualKeyProviderConfigs retrieves all virtual key provider configs from the database.
func (s *RDBConfigStore) GetVirtualKeyProviderConfigs(ctx context.Context, virtualKeyID string) ([]tables.TableVirtualKeyProviderConfig, error) {
	var virtualKey tables.TableVirtualKey
	if err := s.DB().WithContext(ctx).First(&virtualKey, "id = ?", virtualKeyID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return []tables.TableVirtualKeyProviderConfig{}, nil
		}
		return nil, err
	}
	if virtualKey.ID == "" {
		return nil, nil
	}
	var providerConfigs []tables.TableVirtualKeyProviderConfig
	if err := s.DB().WithContext(ctx).Where("virtual_key_id = ?", virtualKey.ID).Find(&providerConfigs).Error; err != nil {
		return nil, err
	}
	return providerConfigs, nil
}

// CreateVirtualKeyProviderConfig creates a new virtual key provider config in the database.
func (s *RDBConfigStore) CreateVirtualKeyProviderConfig(ctx context.Context, virtualKeyProviderConfig *tables.TableVirtualKeyProviderConfig, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	// Store keys before create
	keysToAssociate := virtualKeyProviderConfig.Keys

	// Resolve keys by name/key_id if they don't have database IDs
	// This handles config file inputs that only specify name
	if len(keysToAssociate) > 0 {
		resolvedKeys := make([]tables.TableKey, 0, len(keysToAssociate))
		var unresolvedKeys []string
		for i, k := range keysToAssociate {
			// If key already has a database ID (from UI), use it directly
			if k.ID > 0 {
				resolvedKeys = append(resolvedKeys, k)
				continue
			}
			// Otherwise resolve by KeyID or Name (from config file)
			var dbKey tables.TableKey
			var resolved bool
			if k.KeyID != "" {
				if err := txDB.WithContext(ctx).Where("key_id = ?", k.KeyID).First(&dbKey).Error; err == nil {
					resolvedKeys = append(resolvedKeys, dbKey)
					resolved = true
				}
			}
			if !resolved && k.Name != "" {
				if err := txDB.WithContext(ctx).Where("name = ? AND provider = ?", k.Name, virtualKeyProviderConfig.Provider).First(&dbKey).Error; err == nil {
					resolvedKeys = append(resolvedKeys, dbKey)
					resolved = true
				}
			}
			if !resolved {
				// Collect identifier for unresolved key
				if k.KeyID != "" {
					unresolvedKeys = append(unresolvedKeys, fmt.Sprintf("key_id=%s", k.KeyID))
				} else if k.Name != "" {
					unresolvedKeys = append(unresolvedKeys, fmt.Sprintf("name=%s", k.Name))
				} else {
					unresolvedKeys = append(unresolvedKeys, fmt.Sprintf("key[%d]", i))
				}
			}
		}
		if len(unresolvedKeys) > 0 {
			return &ErrUnresolvedKeys{Identifiers: unresolvedKeys}
		}
		keysToAssociate = resolvedKeys
	}
	sortTableKeysByID(keysToAssociate)

	// Clear Keys before Create to prevent GORM from auto-associating unresolved keys (with ID=0)
	// We'll manually associate the resolved keys after Create
	virtualKeyProviderConfig.Keys = nil

	if err := txDB.WithContext(ctx).Create(virtualKeyProviderConfig).Error; err != nil {
		return s.parseGormError(err)
	}

	// Associate keys after the provider config has an ID
	if len(keysToAssociate) > 0 {
		if err := txDB.WithContext(ctx).Model(virtualKeyProviderConfig).Association("Keys").Append(keysToAssociate); err != nil {
			return err
		}
	}
	return nil
}

// UpdateVirtualKeyProviderConfig updates a virtual key provider config in the database.
func (s *RDBConfigStore) UpdateVirtualKeyProviderConfig(ctx context.Context, virtualKeyProviderConfig *tables.TableVirtualKeyProviderConfig, tx ...*gorm.DB) error {
	if len(tx) == 0 {
		return s.DB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return s.UpdateVirtualKeyProviderConfig(ctx, virtualKeyProviderConfig, transaction)
		})
	}

	var txDB *gorm.DB
	txDB = tx[0]
	if virtualKeyProviderConfig.ID != 0 {
		var existing tables.TableVirtualKeyProviderConfig
		if err := dbForUpdate(txDB.WithContext(ctx)).First(&existing, "id = ?", virtualKeyProviderConfig.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
	}

	// Store keys before save
	keysToAssociate := virtualKeyProviderConfig.Keys

	// Resolve keys by name/key_id if they don't have database IDs
	// This handles config file inputs that only specify name
	if len(keysToAssociate) > 0 {
		resolvedKeys := make([]tables.TableKey, 0, len(keysToAssociate))
		var unresolvedKeys []string
		for i, k := range keysToAssociate {
			// If key already has a database ID (from UI), use it directly
			if k.ID > 0 {
				resolvedKeys = append(resolvedKeys, k)
				continue
			}
			// Otherwise resolve by KeyID or Name (from config file)
			var dbKey tables.TableKey
			var resolved bool
			if k.KeyID != "" {
				if err := txDB.WithContext(ctx).Where("key_id = ?", k.KeyID).First(&dbKey).Error; err == nil {
					resolvedKeys = append(resolvedKeys, dbKey)
					resolved = true
				}
			}
			if !resolved && k.Name != "" {
				if err := txDB.WithContext(ctx).Where("name = ? AND provider = ?", k.Name, virtualKeyProviderConfig.Provider).First(&dbKey).Error; err == nil {
					resolvedKeys = append(resolvedKeys, dbKey)
					resolved = true
				}
			}
			if !resolved {
				// Collect identifier for unresolved key
				if k.KeyID != "" {
					unresolvedKeys = append(unresolvedKeys, fmt.Sprintf("key_id=%s", k.KeyID))
				} else if k.Name != "" {
					unresolvedKeys = append(unresolvedKeys, fmt.Sprintf("name=%s", k.Name))
				} else {
					unresolvedKeys = append(unresolvedKeys, fmt.Sprintf("key[%d]", i))
				}
			}
		}
		if len(unresolvedKeys) > 0 {
			return &ErrUnresolvedKeys{Identifiers: unresolvedKeys}
		}
		keysToAssociate = resolvedKeys
	}
	sortTableKeysByID(keysToAssociate)

	// Clear Keys before Save to prevent GORM from auto-associating unresolved keys (with ID=0)
	// We'll manually manage the association after Save
	virtualKeyProviderConfig.Keys = nil

	if err := txDB.WithContext(ctx).Save(virtualKeyProviderConfig).Error; err != nil {
		return s.parseGormError(err)
	}

	// Clear existing key associations and set new ones
	if err := txDB.WithContext(ctx).Model(virtualKeyProviderConfig).Association("Keys").Clear(); err != nil {
		return err
	}
	if len(keysToAssociate) > 0 {
		if err := txDB.WithContext(ctx).Model(virtualKeyProviderConfig).Association("Keys").Append(keysToAssociate); err != nil {
			return err
		}
	}
	return nil
}

// DeleteVirtualKeyProviderConfig deletes a virtual key provider config from the database.
func (s *RDBConfigStore) DeleteVirtualKeyProviderConfig(ctx context.Context, id uint, tx ...*gorm.DB) error {
	if len(tx) == 0 {
		return s.DB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return s.DeleteVirtualKeyProviderConfig(ctx, id, transaction)
		})
	}

	var txDB *gorm.DB
	txDB = tx[0]
	// First fetch the provider config to get budget and rate limit IDs
	var providerConfig tables.TableVirtualKeyProviderConfig
	if err := dbForUpdate(txDB.WithContext(ctx)).First(&providerConfig, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	if err := txDB.WithContext(ctx).Exec("DELETE FROM governance_virtual_key_provider_config_keys WHERE table_virtual_key_provider_config_id = ?", id).Error; err != nil {
		return err
	}
	// Store the rate limit ID before deleting
	rateLimitID := providerConfig.RateLimitID
	// Delete budgets owned by this provider config
	if err := txDB.WithContext(ctx).Where("provider_config_id = ?", id).Delete(&tables.TableBudget{}).Error; err != nil {
		return err
	}
	// Delete the provider config
	if err := txDB.WithContext(ctx).Delete(&tables.TableVirtualKeyProviderConfig{}, "id = ?", id).Error; err != nil {
		return err
	}
	// Delete the rate limit if it exists
	if rateLimitID != nil {
		if err := txDB.WithContext(ctx).Delete(&tables.TableRateLimit{}, "id = ?", *rateLimitID).Error; err != nil {
			return err
		}
	}
	return nil
}

const teamSelectWithVKCount = "governance_teams.*, (SELECT COUNT(*) FROM governance_virtual_keys WHERE team_id = governance_teams.id) AS virtual_key_count"

// GetTeams retrieves all teams from the database.
//
// When ctx carries a QueryScope, the query is narrowed to teams the
// caller is allowed to see.
func (s *RDBConfigStore) GetTeams(ctx context.Context, customerID string) ([]tables.TableTeam, error) {
	// Preload relationships for complete information
	query := s.ScopedDB(ctx).
		Select(teamSelectWithVKCount).
		Preload("Customer").Preload("Budgets").Preload("RateLimit")
	// Optional filtering by customer
	if customerID != "" {
		query = query.Where("customer_id = ?", customerID)
	}
	var teams []tables.TableTeam
	if err := query.Order("created_at ASC").Find(&teams).Error; err != nil {
		return nil, err
	}
	return teams, nil
}

// GetTeamsPaginated retrieves teams with pagination, filtering, and search support.
//
// When ctx carries a QueryScope, the query is narrowed to teams the
// caller is allowed to see.
func (s *RDBConfigStore) GetTeamsPaginated(ctx context.Context, params TeamsQueryParams) ([]tables.TableTeam, int64, error) {
	baseQuery := s.ScopedDB(ctx).Model(&tables.TableTeam{})

	if params.CustomerID != "" {
		baseQuery = baseQuery.Where("customer_id = ?", params.CustomerID)
	}
	if params.Search != "" {
		search := "%" + strings.ToLower(params.Search) + "%"
		baseQuery = baseQuery.Where("LOWER(name) LIKE ?", search)
	}

	var totalCount int64
	if err := baseQuery.Count(&totalCount).Error; err != nil {
		return nil, 0, err
	}

	limit := params.Limit
	offset := params.Offset
	if limit <= 0 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}

	var teams []tables.TableTeam
	if err := baseQuery.
		Select(teamSelectWithVKCount).
		Preload("Customer").Preload("Budgets").Preload("RateLimit").
		Order("created_at ASC, id ASC").
		Offset(offset).Limit(limit).
		Find(&teams).Error; err != nil {
		return nil, 0, err
	}

	return teams, totalCount, nil
}

// GetTeam retrieves a specific team from the database.
//
// When ctx carries a QueryScope, a team that doesn't satisfy the scope
// returns ErrNotFound; the caller cannot distinguish "doesn't exist"
// from "not visible," matching the leak-prevention contract used by
// the other governance entities.
func (s *RDBConfigStore) GetTeam(ctx context.Context, id string) (*tables.TableTeam, error) {
	var team tables.TableTeam
	if err := s.ScopedDB(ctx).
		Select(teamSelectWithVKCount).
		Preload("Customer").Preload("Budgets").Preload("RateLimit").
		First(&team, "governance_teams.id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &team, nil
}

// GetTeamByName retrieves a team by name. When customerID is non-empty the lookup is scoped to that customer
func (s *RDBConfigStore) GetTeamByName(ctx context.Context, name string, customerID string) (*tables.TableTeam, error) {
	var team tables.TableTeam
	q := s.DB().WithContext(ctx).
		Select(teamSelectWithVKCount).
		Preload("Customer").Preload("Budgets").Preload("RateLimit").
		Where("name = ?", name)
	if customerID != "" {
		q = q.Where("customer_id = ?", customerID)
	}

	if err := q.First(&team).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &team, nil
}

// GetTeamBySourceID retrieves a team by its source ID.
func (s *RDBConfigStore) GetTeamBySourceID(ctx context.Context, sourceID string) (*tables.TableTeam, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return nil, ErrNotFound
	}
	var team tables.TableTeam
	if err := s.DB().WithContext(ctx).
		Select(teamSelectWithVKCount).
		Preload("Customer").Preload("Budgets").Preload("RateLimit").
		Where("source_id = ?", sourceID).
		First(&team).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &team, nil
}

// CreateTeam creates a new team in the database.
func (s *RDBConfigStore) CreateTeam(ctx context.Context, team *tables.TableTeam, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	if err := txDB.WithContext(ctx).Create(team).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// UpdateTeam updates an existing team in the database.
func (s *RDBConfigStore) UpdateTeam(ctx context.Context, team *tables.TableTeam, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	if err := txDB.WithContext(ctx).Save(team).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// DeleteTeam deletes a team from the database.
// Owned budgets cascade via the governance_budgets.team_id FK.
// Rate limit is a sibling row (team holds a FK to it) — deleted explicitly.
func (s *RDBConfigStore) DeleteTeam(ctx context.Context, id string, tx ...*gorm.DB) error {
	if len(tx) == 0 || tx[0] == nil {
		return s.DB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return s.DeleteTeam(ctx, id, transaction)
		})
	}

	txDB := tx[0]
	var team tables.TableTeam
	if err := dbForUpdate(txDB.WithContext(ctx)).Preload("RateLimit").First(&team, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	// Set team_id to null for all virtual keys associated with the team
	if err := txDB.WithContext(ctx).Model(&tables.TableVirtualKey{}).Where("team_id = ?", id).Update("team_id", nil).Error; err != nil {
		return err
	}
	rateLimitID := team.RateLimitID
	// Delete the team - owned budgets cascade via FK on governance_budgets.team_id
	if err := txDB.WithContext(ctx).Delete(&tables.TableTeam{}, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	// Delete the team's rate limit if it exists
	if rateLimitID != nil {
		if err := txDB.WithContext(ctx).Delete(&tables.TableRateLimit{}, "id = ?", *rateLimitID).Error; err != nil {
			return err
		}
	}
	return nil
}

// GetCustomers retrieves all customers from the database.
//
// When ctx carries a QueryScope, the query is narrowed to customers
// the caller is allowed to see.
func (s *RDBConfigStore) GetCustomers(ctx context.Context) ([]tables.TableCustomer, error) {
	var customers []tables.TableCustomer
	if err := preloadCustomerRelations(s.ScopedDB(ctx), "").
		Order("created_at ASC").
		Find(&customers).Error; err != nil {
		return nil, err
	}
	return customers, nil
}

// GetCustomersPaginated retrieves customers with pagination and optional
// search filtering.
//
// When ctx carries a QueryScope, the query is narrowed to customers
// the caller is allowed to see.
func (s *RDBConfigStore) GetCustomersPaginated(ctx context.Context, params CustomersQueryParams) ([]tables.TableCustomer, int64, error) {
	baseQuery := s.ScopedDB(ctx).Model(&tables.TableCustomer{})
	if params.Search != "" {
		search := "%" + strings.ToLower(params.Search) + "%"
		baseQuery = baseQuery.Where("LOWER(name) LIKE ?", search)
	}
	var totalCount int64
	if err := baseQuery.Count(&totalCount).Error; err != nil {
		return nil, 0, err
	}
	limit := params.Limit
	offset := params.Offset
	if limit <= 0 {
		limit = 25
	} else if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	var customers []tables.TableCustomer
	if err := preloadCustomerRelations(baseQuery, "").
		Order("created_at ASC, id ASC").
		Offset(offset).Limit(limit).
		Find(&customers).Error; err != nil {
		return nil, 0, err
	}
	return customers, totalCount, nil
}

// GetCustomer retrieves a specific customer from the database.
//
// When ctx carries a QueryScope, a customer that doesn't satisfy the
// scope returns ErrNotFound; the caller cannot distinguish "doesn't
// exist" from "not visible," matching the leak-prevention contract
// used by the other governance entities.
func (s *RDBConfigStore) GetCustomer(ctx context.Context, id string) (*tables.TableCustomer, error) {
	var customer tables.TableCustomer
	if err := preloadCustomerRelations(s.ScopedDB(ctx), "").
		First(&customer, "governance_customers.id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &customer, nil
}

// CreateCustomer creates a new customer in the database.
func (s *RDBConfigStore) CreateCustomer(ctx context.Context, customer *tables.TableCustomer, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	if err := txDB.WithContext(ctx).Create(customer).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// UpdateCustomer updates an existing customer in the database.
func (s *RDBConfigStore) UpdateCustomer(ctx context.Context, customer *tables.TableCustomer, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	if err := txDB.WithContext(ctx).Save(customer).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// DeleteCustomer deletes a customer from the database.
func (s *RDBConfigStore) DeleteCustomer(ctx context.Context, id string, tx ...*gorm.DB) error {
	if len(tx) == 0 || tx[0] == nil {
		return s.DB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return s.DeleteCustomer(ctx, id, transaction)
		})
	}

	txDB := tx[0]
	var customer tables.TableCustomer
	if err := dbForUpdate(txDB.WithContext(ctx)).Preload("RateLimit").First(&customer, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	// Null out customer_id on associated VKs and teams before deleting the customer row.
	if err := txDB.WithContext(ctx).Model(&tables.TableVirtualKey{}).Where("customer_id = ?", id).Update("customer_id", nil).Error; err != nil {
		return err
	}
	// Set customer_id to null for all teams associated with the customer
	if err := txDB.WithContext(ctx).Model(&tables.TableTeam{}).Where("customer_id = ?", id).Update("customer_id", nil).Error; err != nil {
		return err
	}
	rateLimitID := customer.RateLimitID
	// Explicitly delete owned budgets before the customer row. FK cascades cannot
	// be relied on across all dialects for constraints added to pre-existing tables.
	if err := txDB.WithContext(ctx).Where("customer_id = ?", id).Delete(&tables.TableBudget{}).Error; err != nil {
		return err
	}
	if err := txDB.WithContext(ctx).Delete(&tables.TableCustomer{}, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	if rateLimitID != nil {
		if err := txDB.WithContext(ctx).Delete(&tables.TableRateLimit{}, "id = ?", *rateLimitID).Error; err != nil {
			return err
		}
	}
	return nil
}

// GetRateLimits retrieves all rate limits from the database.
func (s *RDBConfigStore) GetRateLimits(ctx context.Context) ([]tables.TableRateLimit, error) {
	var rateLimits []tables.TableRateLimit
	if err := s.DB().WithContext(ctx).Order("created_at ASC").Find(&rateLimits).Error; err != nil {
		return nil, err
	}
	return rateLimits, nil
}

// GetRateLimit retrieves a specific rate limit from the database.
func (s *RDBConfigStore) GetRateLimit(ctx context.Context, id string, tx ...*gorm.DB) (*tables.TableRateLimit, error) {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	var rateLimit tables.TableRateLimit
	if err := txDB.WithContext(ctx).First(&rateLimit, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &rateLimit, nil
}

// CreateRateLimit creates a new rate limit in the database.
func (s *RDBConfigStore) CreateRateLimit(ctx context.Context, rateLimit *tables.TableRateLimit, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	if err := txDB.WithContext(ctx).Create(rateLimit).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// UpdateRateLimit updates a rate limit in the database.
func (s *RDBConfigStore) UpdateRateLimit(ctx context.Context, rateLimit *tables.TableRateLimit, tx ...*gorm.DB) error {
	if len(tx) == 0 {
		return s.DB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return s.UpdateRateLimit(ctx, rateLimit, transaction)
		})
	}

	txDB := tx[0]
	if rateLimit.ID != "" {
		var existing tables.TableRateLimit
		if err := dbForUpdate(txDB.WithContext(ctx)).First(&existing, "id = ?", rateLimit.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
	}
	if err := txDB.WithContext(ctx).Save(rateLimit).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// UpdateRateLimits updates multiple rate limits in the database.
func (s *RDBConfigStore) UpdateRateLimits(ctx context.Context, rateLimits []*tables.TableRateLimit, tx ...*gorm.DB) error {
	if len(tx) == 0 {
		return s.DB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return s.UpdateRateLimits(ctx, rateLimits, transaction)
		})
	}

	txDB := tx[0]
	sortedRateLimits := append([]*tables.TableRateLimit(nil), rateLimits...)
	sort.Slice(sortedRateLimits, func(i, j int) bool { return sortedRateLimits[i].ID < sortedRateLimits[j].ID })
	for _, rl := range sortedRateLimits {
		if err := s.UpdateRateLimit(ctx, rl, txDB); err != nil {
			return err
		}
	}
	return nil
}

// DeleteRateLimit deletes a rate limit from the database.
func (s *RDBConfigStore) DeleteRateLimit(ctx context.Context, id string, tx ...*gorm.DB) error {
	if len(tx) == 0 {
		return s.DB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return s.DeleteRateLimit(ctx, id, transaction)
		})
	}

	txDB := tx[0]
	var existing tables.TableRateLimit
	if err := dbForUpdate(txDB.WithContext(ctx)).First(&existing, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	if err := txDB.WithContext(ctx).Delete(&tables.TableRateLimit{}, "id = ?", id).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// GetBudgets retrieves all budgets from the database.
func (s *RDBConfigStore) GetBudgets(ctx context.Context) ([]tables.TableBudget, error) {
	var budgets []tables.TableBudget
	if err := s.DB().WithContext(ctx).Order("created_at ASC").Find(&budgets).Error; err != nil {
		return nil, err
	}
	return budgets, nil
}

// GetBudget retrieves a specific budget from the database.
func (s *RDBConfigStore) GetBudget(ctx context.Context, id string, tx ...*gorm.DB) (*tables.TableBudget, error) {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	var budget tables.TableBudget
	if err := txDB.WithContext(ctx).First(&budget, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &budget, nil
}

// CreateBudget creates a new budget in the database.
func (s *RDBConfigStore) CreateBudget(ctx context.Context, budget *tables.TableBudget, tx ...*gorm.DB) error {
	var txDB *gorm.DB
	if len(tx) > 0 {
		txDB = tx[0]
	} else {
		txDB = s.DB()
	}
	if err := txDB.WithContext(ctx).Create(budget).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// UpdateBudgets updates multiple budgets in the database.
func (s *RDBConfigStore) UpdateBudgets(ctx context.Context, budgets []*tables.TableBudget, tx ...*gorm.DB) error {
	if len(tx) == 0 {
		return s.DB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return s.UpdateBudgets(ctx, budgets, transaction)
		})
	}

	txDB := tx[0]
	sortedBudgets := append([]*tables.TableBudget(nil), budgets...)
	sort.Slice(sortedBudgets, func(i, j int) bool { return sortedBudgets[i].ID < sortedBudgets[j].ID })
	for _, b := range sortedBudgets {
		if err := s.UpdateBudget(ctx, b, txDB); err != nil {
			return err
		}
	}
	return nil
}

// UpdateBudget updates a budget in the database.
func (s *RDBConfigStore) UpdateBudget(ctx context.Context, budget *tables.TableBudget, tx ...*gorm.DB) error {
	if len(tx) == 0 {
		return s.DB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return s.UpdateBudget(ctx, budget, transaction)
		})
	}

	txDB := tx[0]
	if budget.ID != "" {
		var existing tables.TableBudget
		if err := txDB.WithContext(ctx).First(&existing, "id = ?", budget.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		ownerBudget := *budget
		if ownerBudget.VirtualKeyID == nil {
			ownerBudget.VirtualKeyID = existing.VirtualKeyID
		}
		if ownerBudget.ProviderConfigID == nil {
			ownerBudget.ProviderConfigID = existing.ProviderConfigID
		}
		if ownerBudget.TeamID == nil {
			ownerBudget.TeamID = existing.TeamID
		}
		if ownerBudget.CustomerID == nil {
			ownerBudget.CustomerID = existing.CustomerID
		}
		if err := lockBudgetOwner(ctx, txDB, ownerBudget); err != nil {
			return err
		}
		if err := dbForUpdate(txDB.WithContext(ctx)).First(&existing, "id = ?", budget.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
	}
	if err := txDB.WithContext(ctx).Save(budget).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// DeleteBudget deletes a budget from the database.
func (s *RDBConfigStore) DeleteBudget(ctx context.Context, id string, tx ...*gorm.DB) error {
	if len(tx) == 0 {
		return s.DB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return s.DeleteBudget(ctx, id, transaction)
		})
	}

	txDB := tx[0]
	var existing tables.TableBudget
	if err := txDB.WithContext(ctx).First(&existing, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	if err := lockBudgetOwner(ctx, txDB, existing); err != nil {
		return err
	}
	if err := dbForUpdate(txDB.WithContext(ctx)).First(&existing, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	if err := txDB.WithContext(ctx).Delete(&tables.TableBudget{}, "id = ?", id).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// UpdateBudgetUsage updates only the current_usage field of a budget.
// Uses SkipHooks to avoid triggering BeforeSave validation since we're only updating usage.
func (s *RDBConfigStore) UpdateBudgetUsage(ctx context.Context, id string, currentUsage float64, tx ...*gorm.DB) error {
	db := s.DB()
	if len(tx) > 0 && tx[0] != nil {
		db = tx[0]
	}
	result := db.WithContext(ctx).
		Session(&gorm.Session{SkipHooks: true}).
		Model(&tables.TableBudget{}).
		Where("id = ?", id).
		Update("current_usage", currentUsage)
	if result.Error != nil {
		return s.parseGormError(result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateRateLimitUsage updates only the usage fields of a rate limit.
// Uses SkipHooks to avoid triggering BeforeSave validation since we're only updating usage.
func (s *RDBConfigStore) UpdateRateLimitUsage(ctx context.Context, id string, tokenCurrentUsage int64, requestCurrentUsage int64, tx ...*gorm.DB) error {
	db := s.DB()
	if len(tx) > 0 && tx[0] != nil {
		db = tx[0]
	}
	result := db.WithContext(ctx).
		Session(&gorm.Session{SkipHooks: true}).
		Model(&tables.TableRateLimit{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"token_current_usage":   tokenCurrentUsage,
			"request_current_usage": requestCurrentUsage,
		})
	if result.Error != nil {
		return s.parseGormError(result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// loadRoutingRulesOrdered loads routing rules with Targets preloaded, using consistent ordering:
// rules by priority ASC, created_at DESC, id ASC; targets by weight DESC for deterministic ordering.
func (s *RDBConfigStore) loadRoutingRulesOrdered(ctx context.Context, dest *[]tables.TableRoutingRule, scopes ...func(*gorm.DB) *gorm.DB) error {
	q := s.DB().WithContext(ctx).
		Preload("Targets", func(db *gorm.DB) *gorm.DB {
			return db.Order("weight DESC").
				Order("COALESCE(provider, '') ASC").
				Order("COALESCE(model, '') ASC").
				Order("COALESCE(key_id, '') ASC")
		}).
		Order("priority ASC, created_at DESC, id ASC")
	for _, scope := range scopes {
		q = scope(q)
	}
	return q.Find(dest).Error
}

// GetRoutingRules retrieves all routing rules from the database.
func (s *RDBConfigStore) GetRoutingRules(ctx context.Context) ([]tables.TableRoutingRule, error) {
	var rules []tables.TableRoutingRule
	if err := s.loadRoutingRulesOrdered(ctx, &rules); err != nil {
		return nil, err
	}
	return rules, nil
}

// GetRoutingRulesPaginated retrieves routing rules with pagination and optional search filtering.
//
// When ctx carries a QueryScope, the query is narrowed to rules the
// caller is allowed to see; rules with scope='global' are always
// included by the scope builder.
func (s *RDBConfigStore) GetRoutingRulesPaginated(ctx context.Context, params RoutingRulesQueryParams) ([]tables.TableRoutingRule, int64, error) {
	baseQuery := s.ScopedDB(ctx).Model(&tables.TableRoutingRule{})

	if params.Search != "" {
		search := "%" + strings.ToLower(params.Search) + "%"
		baseQuery = baseQuery.Where("LOWER(name) LIKE ? OR LOWER(cel_expression) LIKE ?", search, search)
	}

	var totalCount int64
	if err := baseQuery.Count(&totalCount).Error; err != nil {
		return nil, 0, err
	}

	limit := params.Limit
	offset := params.Offset

	if limit <= 0 {
		limit = 25
	} else if limit > 100 {
		limit = 100
	}

	if offset < 0 {
		offset = 0
	}

	var rules []tables.TableRoutingRule
	if err := baseQuery.
		Preload("Targets", func(db *gorm.DB) *gorm.DB {
			return db.Order("weight DESC").
				Order("COALESCE(provider, '') ASC").
				Order("COALESCE(model, '') ASC").
				Order("COALESCE(key_id, '') ASC")
		}).
		Order("priority ASC, created_at DESC, id ASC").
		Offset(offset).
		Limit(limit).
		Find(&rules).Error; err != nil {
		return nil, 0, err
	}
	return rules, totalCount, nil
}

// GetRoutingRulesByScope retrieves routing rules by scope and scope ID, ordered by priority ASC.
func (s *RDBConfigStore) GetRoutingRulesByScope(ctx context.Context, scope string, scopeID string) ([]tables.TableRoutingRule, error) {
	if scope != "global" && scopeID == "" {
		return nil, fmt.Errorf("scopeID is required for non-global scope %q", scope)
	}
	var rules []tables.TableRoutingRule
	scopeFilter := func(q *gorm.DB) *gorm.DB {
		if scope == "global" {
			return q.Where("scope = ?", "global")
		}
		return q.Where("scope = ? AND scope_id = ?", scope, scopeID)
	}
	if err := s.loadRoutingRulesOrdered(ctx, &rules, scopeFilter, func(q *gorm.DB) *gorm.DB {
		return q.Where("enabled = ?", true)
	}); err != nil {
		return nil, err
	}
	return rules, nil
}

// GetRoutingRule retrieves a specific routing rule by ID.
func (s *RDBConfigStore) GetRoutingRule(ctx context.Context, id string) (*tables.TableRoutingRule, error) {
	var rules []tables.TableRoutingRule
	if err := s.loadRoutingRulesOrdered(ctx, &rules, func(q *gorm.DB) *gorm.DB {
		return q.Where("id = ?", id)
	}); err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return nil, ErrNotFound
	}
	return &rules[0], nil
}

// GetRedactedRoutingRules retrieves redacted routing rules from the database.
func (s *RDBConfigStore) GetRedactedRoutingRules(ctx context.Context, ids []string) ([]tables.TableRoutingRule, error) {
	var routingRules []tables.TableRoutingRule

	if len(ids) > 0 {
		err := s.DB().WithContext(ctx).Select("id, name, description, enabled").Where("id IN ?", ids).Find(&routingRules).Error
		if err != nil {
			return nil, err
		}
	} else {
		err := s.DB().WithContext(ctx).Select("id, name, description, enabled").Find(&routingRules).Error
		if err != nil {
			return nil, err
		}
	}
	return routingRules, nil
}

// CreateRoutingRule creates a new routing rule in the database.
func (s *RDBConfigStore) CreateRoutingRule(ctx context.Context, rule *tables.TableRoutingRule, tx ...*gorm.DB) error {
	database := s.DB()
	if len(tx) > 0 && tx[0] != nil {
		database = tx[0]
	}

	// Validate scopeID is required for non-global scope
	if rule.Scope != "" && rule.Scope != "global" && rule.ScopeID == nil {
		return fmt.Errorf("scopeID is required for non-global scope '%s'", rule.Scope)
	}

	// Check if there is already a routing rule with the same priority for the same scope+scopeID
	var count int64
	query := database.WithContext(ctx).Where("scope = ? AND priority = ? AND id != ?", rule.Scope, rule.Priority, rule.ID)
	if rule.ScopeID != nil {
		query = query.Where("scope_id = ?", *rule.ScopeID)
	} else {
		query = query.Where("scope_id IS NULL")
	}
	if err := query.Model(&tables.TableRoutingRule{}).Count(&count).Error; err != nil {
		return s.parseGormError(err)
	}
	if count > 0 {
		if rule.ScopeID != nil {
			return fmt.Errorf("routing rule with priority %d already exists for scope '%s' with scopeID '%v'", rule.Priority, rule.Scope, rule.ScopeID)
		}
		return fmt.Errorf("routing rule with priority %d already exists for scope '%s'", rule.Priority, rule.Scope)
	}

	return s.parseGormError(database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		targets := rule.Targets
		rule.Targets = nil
		if err := tx.Omit("Targets").Create(rule).Error; err != nil {
			return err
		}
		rule.Targets = targets

		for i := range rule.Targets {
			rule.Targets[i].RuleID = rule.ID
			if err := tx.Create(&rule.Targets[i]).Error; err != nil {
				return err
			}
		}
		return nil
	}))
}

// UpdateRoutingRule updates an existing routing rule in the database.
// It enforces the same unique-priority-per-scope invariant as CreateRoutingRule.
func (s *RDBConfigStore) UpdateRoutingRule(ctx context.Context, rule *tables.TableRoutingRule, tx ...*gorm.DB) error {
	database := s.DB()
	if len(tx) > 0 && tx[0] != nil {
		database = tx[0]
	}

	// Validate scopeID is required for non-global scope
	if rule.Scope != "" && rule.Scope != "global" && rule.ScopeID == nil {
		return fmt.Errorf("scopeID is required for non-global scope '%s'", rule.Scope)
	}

	return s.parseGormError(database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing tables.TableRoutingRule
		if err := dbForUpdate(tx).First(&existing, "id = ?", rule.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}

		// Check for another tables.TableRoutingRule with same scope (Scope + ScopeID) and Priority but different ID
		var count int64
		query := tx.Where("scope = ? AND priority = ? AND id != ?", rule.Scope, rule.Priority, rule.ID)
		if rule.ScopeID != nil {
			query = query.Where("scope_id = ?", *rule.ScopeID)
		} else {
			query = query.Where("scope_id IS NULL")
		}
		if err := query.Model(&tables.TableRoutingRule{}).Count(&count).Error; err != nil {
			return s.parseGormError(err)
		}
		if count > 0 {
			if rule.ScopeID != nil {
				return fmt.Errorf("routing rule with priority %d already exists for scope '%s' with scopeID '%v'", rule.Priority, rule.Scope, rule.ScopeID)
			}
			return fmt.Errorf("routing rule with priority %d already exists for scope '%s'", rule.Priority, rule.Scope)
		}

		targets := rule.Targets
		rule.Targets = nil
		if err := tx.Omit("Targets").Save(rule).Error; err != nil {
			return err
		}
		rule.Targets = targets

		if err := tx.Where("rule_id = ?", rule.ID).Delete(&tables.TableRoutingTarget{}).Error; err != nil {
			return err
		}
		for i := range rule.Targets {
			rule.Targets[i].RuleID = rule.ID
			if err := tx.Create(&rule.Targets[i]).Error; err != nil {
				return err
			}
		}
		return nil
	}))
}

// DeleteRoutingRule deletes a routing rule and its targets from the database.
func (s *RDBConfigStore) DeleteRoutingRule(ctx context.Context, id string, tx ...*gorm.DB) error {
	database := s.DB()
	if len(tx) > 0 && tx[0] != nil {
		database = tx[0]
	}

	return s.parseGormError(database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing tables.TableRoutingRule
		if err := dbForUpdate(tx).First(&existing, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if err := tx.Where("rule_id = ?", id).Delete(&tables.TableRoutingTarget{}).Error; err != nil {
			return err
		}
		result := tx.Delete(&tables.TableRoutingRule{}, "id = ?", id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	}))
}

// GetModelConfigs retrieves all model configs from the database.
func (s *RDBConfigStore) GetModelConfigs(ctx context.Context) ([]tables.TableModelConfig, error) {
	var allModelConfigs []tables.TableModelConfig
	lastID := ""
	hasCursor := false

	for {
		var page []tables.TableModelConfig
		query := s.DB().WithContext(ctx).Preload("Budgets").Preload("Budget").Preload("RateLimit")
		if hasCursor {
			query = query.Where("governance_model_configs.id > ?", lastID)
		}
		if err := query.
			Order("governance_model_configs.id ASC").
			Limit(modelConfigInternalPageSize).
			Find(&page).Error; err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return allModelConfigs, nil
		}
		allModelConfigs = append(allModelConfigs, page...)
		lastID = page[len(page)-1].ID
		hasCursor = true
		if len(page) < modelConfigInternalPageSize {
			return allModelConfigs, nil
		}
	}
}

// GetModelConfigsByScopeAndScopeIDs retrieves model configs for a specific scope limited to the given scope IDs.
func (s *RDBConfigStore) GetModelConfigsByScopeAndScopeIDs(ctx context.Context, scope string, scopeIDs []string) ([]tables.TableModelConfig, error) {
	if len(scopeIDs) == 0 {
		return nil, nil
	}
	var modelConfigs []tables.TableModelConfig
	if err := s.DB().WithContext(ctx).Preload("Budgets").Preload("Budget").Preload("RateLimit").
		Where("scope = ? AND scope_id IN ?", scope, scopeIDs).
		Find(&modelConfigs).Error; err != nil {
		return nil, err
	}
	return modelConfigs, nil
}

// GetProviderGovernanceModelConfigs retrieves the wildcard "all models on a provider" configs
func (s *RDBConfigStore) GetProviderGovernanceModelConfigs(ctx context.Context) ([]tables.TableModelConfig, error) {
	var modelConfigs []tables.TableModelConfig
	if err := s.DB().WithContext(ctx).
		Preload("Budgets").Preload("Budget").Preload("RateLimit").
		Where("scope = ? AND model_name = ? AND provider IS NOT NULL", tables.ModelConfigScopeGlobal, tables.ModelConfigAllModels).
		Find(&modelConfigs).Error; err != nil {
		return nil, err
	}
	return modelConfigs, nil
}

// GetModelConfigsPaginated retrieves model configs with pagination, filtering, and search support.
func (s *RDBConfigStore) GetModelConfigsPaginated(ctx context.Context, params ModelConfigsQueryParams) ([]tables.TableModelConfig, int64, error) {
	baseQuery := s.DB().WithContext(ctx).Model(&tables.TableModelConfig{})

	if params.Search != "" {
		search := "%" + strings.ToLower(params.Search) + "%"
		baseQuery = baseQuery.Where("LOWER(model_name) LIKE ?", search)
	}
	if params.Scope != "" {
		baseQuery = baseQuery.Where("scope = ?", params.Scope)
	}
	if params.Provider != "" {
		baseQuery = baseQuery.Where("provider = ?", params.Provider)
	}

	var totalCount int64
	if err := baseQuery.Count(&totalCount).Error; err != nil {
		return nil, 0, err
	}

	limit := params.Limit
	offset := params.Offset

	if limit <= 0 {
		limit = 25
	} else if limit > 100 {
		limit = 100
	}

	if offset < 0 {
		offset = 0
	}

	var modelConfigs []tables.TableModelConfig
	if err := baseQuery.
		Preload("Budgets").
		Preload("Budget").
		Preload("RateLimit").
		Order("created_at DESC, id DESC").
		Offset(offset).
		Limit(limit).
		Find(&modelConfigs).Error; err != nil {
		return nil, 0, err
	}
	return modelConfigs, totalCount, nil
}

// GetModelConfig retrieves a specific model config from the database by its identity:
// scope, optional scope ID, model name, and optional provider.
func (s *RDBConfigStore) GetModelConfig(ctx context.Context, scope string, scopeID *string, modelName string, provider *string) (*tables.TableModelConfig, error) {
	var modelConfig tables.TableModelConfig
	query := s.DB().WithContext(ctx).Where("model_name = ?", modelName).Where("scope = ?", scope)
	if scopeID != nil {
		query = query.Where("scope_id = ?", *scopeID)
	} else {
		query = query.Where("scope_id IS NULL")
	}
	if provider != nil {
		query = query.Where("provider = ?", *provider)
	} else {
		query = query.Where("provider IS NULL")
	}
	if err := query.Preload("Budgets").Preload("Budget").Preload("RateLimit").First(&modelConfig).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &modelConfig, nil
}

// GetModelConfigByID retrieves a specific model config from the database by ID.
func (s *RDBConfigStore) GetModelConfigByID(ctx context.Context, id string) (*tables.TableModelConfig, error) {
	var modelConfig tables.TableModelConfig
	if err := s.DB().WithContext(ctx).Preload("Budgets").Preload("Budget").Preload("RateLimit").First(&modelConfig, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &modelConfig, nil
}

// deleteModelConfigsWhere deletes every model config matching the given condition,
// along with the budgets and rate-limits those configs own. It is the single source
// of truth for tearing down model configs when their owner (a virtual key, provider,
// user, …) is removed — every owner-delete path funnels through here so the cleanup,
// including the easy-to-forget preload of multi-budget rows, lives in exactly one place.
//
// Owned budgets are gathered from BOTH the active Budgets slice (owned via
// ModelConfigID) and the legacy single BudgetID column. Deletion happens by
// snapshotted ID rather than re-running the WHERE clause, so a concurrent
// CreateModelConfig that lands between the snapshot and the delete can't leave its
// owned budget/rate-limit rows dangling. The snapshot is taken FOR UPDATE
// (mirroring DeleteModelConfig) so a concurrent UpdateModelConfig can't swap
// BudgetID/RateLimitID after the IDs are collected; rows are locked in stable id
// order to keep concurrent deleters deadlock-free. Configs are removed before
// their owned rows, matching DeleteModelConfig's order.
func (s *RDBConfigStore) deleteModelConfigsWhere(ctx context.Context, txDB *gorm.DB, query string, args ...any) error {
	var modelConfigs []tables.TableModelConfig
	if err := dbForUpdate(txDB.WithContext(ctx)).Preload("Budgets").Order("id").Where(query, args...).Find(&modelConfigs).Error; err != nil {
		return err
	}
	if len(modelConfigs) == 0 {
		return nil
	}

	mcIDs := make([]string, 0, len(modelConfigs))
	budgetIDs := make([]string, 0, len(modelConfigs))
	rateLimitIDs := make([]string, 0, len(modelConfigs))
	for i := range modelConfigs {
		mcIDs = append(mcIDs, modelConfigs[i].ID)
		for j := range modelConfigs[i].Budgets {
			budgetIDs = append(budgetIDs, modelConfigs[i].Budgets[j].ID)
		}
		if modelConfigs[i].BudgetID != nil {
			budgetIDs = append(budgetIDs, *modelConfigs[i].BudgetID)
		}
		if modelConfigs[i].RateLimitID != nil {
			rateLimitIDs = append(rateLimitIDs, *modelConfigs[i].RateLimitID)
		}
	}

	if err := txDB.WithContext(ctx).Where("id IN ?", mcIDs).Delete(&tables.TableModelConfig{}).Error; err != nil {
		return err
	}
	if len(budgetIDs) > 0 {
		if err := txDB.WithContext(ctx).Delete(&tables.TableBudget{}, "id IN ?", budgetIDs).Error; err != nil {
			return err
		}
	}
	if len(rateLimitIDs) > 0 {
		if err := txDB.WithContext(ctx).Delete(&tables.TableRateLimit{}, "id IN ?", rateLimitIDs).Error; err != nil {
			return err
		}
	}
	return nil
}

// DeleteModelConfigsForScope removes all model configs targeting a given scope owner
// (e.g. scope=virtual_key, scopeID=<vk id>) along with their owned budgets/rate-limits.
// Thin wrapper over deleteModelConfigsWhere for the scope/scope_id axis. Exported so
// out-of-package owner-delete paths (e.g. the enterprise user-deletion flow cleaning up
// scope=user configs) funnel through the same cleanup instead of reimplementing it.
func (s *RDBConfigStore) DeleteModelConfigsForScope(ctx context.Context, txDB *gorm.DB, scope, scopeID string) error {
	// The tx is required (not variadic) on purpose: this cleanup must be atomic
	// with the owner's delete. Guard against nil rather than falling back to
	// s.DB(), which would silently run the cleanup outside that transaction.
	if txDB == nil {
		return fmt.Errorf("DeleteModelConfigsForScope requires the owner-delete transaction, got nil tx")
	}
	return s.deleteModelConfigsWhere(ctx, txDB, "scope = ? AND scope_id = ?", scope, scopeID)
}

// CreateModelConfig creates a new model config in the database.
func (s *RDBConfigStore) CreateModelConfig(ctx context.Context, modelConfig *tables.TableModelConfig, tx ...*gorm.DB) error {
	// Locking the scope owner and inserting the config must be atomic, so wrap in a
	// transaction when the caller didn't supply one.
	if len(tx) == 0 || tx[0] == nil {
		return s.DB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return s.CreateModelConfig(ctx, modelConfig, transaction)
		})
	}
	txDB := tx[0]

	// Serialize against deletion of the scope owner. A scoped config's scope_id carries
	// no FK, so without this a CreateModelConfig for scope=virtual_key could commit just
	// after a concurrent DeleteVirtualKey, leaving the config (and its owned budgets/
	// rate-limits) pointing at a virtual key that no longer exists. Locking the owner row
	// makes the two transactions mutually exclusive and surfaces an already-deleted owner
	// as ErrNotFound. Callers that create owner-scoped configs must create the owner first.
	if err := s.lockModelConfigScopeOwner(ctx, txDB, modelConfig); err != nil {
		return err
	}

	if err := txDB.WithContext(ctx).Create(modelConfig).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// lockModelConfigScopeOwner takes a FOR UPDATE lock on the row a scoped model config
// targets and confirms it still exists, returning ErrNotFound when the owner is gone.
// Global configs (no scope owner) are a no-op. Only scopes whose owner table lives in
// this store are locked; other scopes (e.g. the enterprise "user" scope, whose owner
// table is out of package) are the responsibility of their own create path. The lock is
// FOR UPDATE on Postgres and a plain existence check on SQLite (whose writer
// serialization already prevents the interleave), mirroring lockBudgetOwner.
//
// Provider-bound configs (any scope) are additionally serialized against
// DeleteProvider, which tears down every config matching the provider column. The
// provider row is optional — providers may be env-configured with no DB row — so
// absence is tolerated rather than treated as a missing owner.
func (s *RDBConfigStore) lockModelConfigScopeOwner(ctx context.Context, txDB *gorm.DB, mc *tables.TableModelConfig) error {
	if mc == nil {
		return nil
	}
	if mc.Provider != nil && *mc.Provider != "" {
		var provider tables.TableProvider
		if err := dbForUpdate(txDB.WithContext(ctx)).First(&provider, "name = ?", *mc.Provider).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	if mc.ScopeID == nil || *mc.ScopeID == "" {
		return nil
	}
	switch mc.Scope {
	case tables.ModelConfigScopeVirtualKey:
		var vk tables.TableVirtualKey
		if err := dbForUpdate(txDB.WithContext(ctx)).First(&vk, "id = ?", *mc.ScopeID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
	}
	return nil
}

// UpdateModelConfig updates a model config in the database.
func (s *RDBConfigStore) UpdateModelConfig(ctx context.Context, modelConfig *tables.TableModelConfig, tx ...*gorm.DB) error {
	if len(tx) == 0 {
		return s.DB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return s.UpdateModelConfig(ctx, modelConfig, transaction)
		})
	}

	txDB := tx[0]
	if modelConfig.ID != "" {
		var existing tables.TableModelConfig
		if err := dbForUpdate(txDB.WithContext(ctx)).First(&existing, "id = ?", modelConfig.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
	}
	// Omit associations: budgets (has-many via ModelConfigID) and rate-limit are managed
	// explicitly by callers. A cascading Save would otherwise clobber their usage counters.
	if err := txDB.WithContext(ctx).Omit(clause.Associations).Save(modelConfig).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// UpdateModelConfigs updates multiple model configs in the database.
func (s *RDBConfigStore) UpdateModelConfigs(ctx context.Context, modelConfigs []*tables.TableModelConfig, tx ...*gorm.DB) error {
	if len(tx) == 0 {
		return s.DB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return s.UpdateModelConfigs(ctx, modelConfigs, transaction)
		})
	}

	txDB := tx[0]
	sortedModelConfigs := append([]*tables.TableModelConfig(nil), modelConfigs...)
	sort.Slice(sortedModelConfigs, func(i, j int) bool { return sortedModelConfigs[i].ID < sortedModelConfigs[j].ID })
	for _, mc := range sortedModelConfigs {
		if err := s.UpdateModelConfig(ctx, mc, txDB); err != nil {
			return err
		}
	}
	return nil
}

// DeleteModelConfig deletes a model config from the database.
func (s *RDBConfigStore) DeleteModelConfig(ctx context.Context, id string, tx ...*gorm.DB) error {
	if len(tx) == 0 || tx[0] == nil {
		return s.DB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
			return s.DeleteModelConfig(ctx, id, transaction)
		})
	}

	txDB := tx[0]
	// Fetch the model config with its owned budgets to collect all IDs to clean up.
	var modelConfig tables.TableModelConfig
	if err := dbForUpdate(txDB.WithContext(ctx)).Preload("Budgets").First(&modelConfig, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	// Collect budget IDs from both the Budgets slice (active path, owned via ModelConfigID)
	// and the legacy single BudgetID column.
	budgetIDs := make([]string, 0, len(modelConfig.Budgets)+1)
	for i := range modelConfig.Budgets {
		budgetIDs = append(budgetIDs, modelConfig.Budgets[i].ID)
	}
	if modelConfig.BudgetID != nil {
		budgetIDs = append(budgetIDs, *modelConfig.BudgetID)
	}
	rateLimitID := modelConfig.RateLimitID
	// Delete the model config first
	if err := txDB.WithContext(ctx).Delete(&tables.TableModelConfig{}, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return s.parseGormError(err)
	}
	// Delete the owned budgets (don't rely on FK cascade — it isn't applied to
	// pre-existing tables on all dialects).
	if len(budgetIDs) > 0 {
		if err := txDB.WithContext(ctx).Delete(&tables.TableBudget{}, "id IN ?", budgetIDs).Error; err != nil {
			return err
		}
	}
	// Delete the rate limit if it exists
	if rateLimitID != nil {
		if err := txDB.WithContext(ctx).Delete(&tables.TableRateLimit{}, "id = ?", *rateLimitID).Error; err != nil {
			return err
		}
	}
	return nil
}

// GetGovernanceConfig retrieves the governance configuration from the database.
func (s *RDBConfigStore) GetGovernanceConfig(ctx context.Context) (*GovernanceConfig, error) {
	var virtualKeys []tables.TableVirtualKey
	var teams []tables.TableTeam
	var customers []tables.TableCustomer
	var budgets []tables.TableBudget
	var rateLimits []tables.TableRateLimit
	var modelConfigs []tables.TableModelConfig
	var providers []tables.TableProvider
	var routingRules []tables.TableRoutingRule
	var pricingOverrides []tables.TablePricingOverride
	var governanceConfigs []tables.TableGovernanceConfig

	loadedVKs, err := s.getGovernanceConfigVirtualKeys(ctx)
	if err != nil {
		return nil, err
	}
	virtualKeys = loadedVKs
	if err := s.DB().WithContext(ctx).
		Select(teamSelectWithVKCount).
		Find(&teams).Error; err != nil {
		return nil, err
	}
	if err := s.DB().WithContext(ctx).Find(&customers).Error; err != nil {
		return nil, err
	}
	if err := s.DB().WithContext(ctx).Find(&budgets).Error; err != nil {
		return nil, err
	}
	if err := s.DB().WithContext(ctx).Find(&rateLimits).Error; err != nil {
		return nil, err
	}
	if err := s.DB().WithContext(ctx).Find(&modelConfigs).Error; err != nil {
		return nil, err
	}
	if err := s.DB().WithContext(ctx).Find(&providers).Error; err != nil {
		return nil, err
	}
	if err := s.loadRoutingRulesOrdered(ctx, &routingRules); err != nil {
		return nil, err
	}
	if err := s.DB().WithContext(ctx).Find(&pricingOverrides).Error; err != nil {
		return nil, err
	}
	// Fetching governance config for username and password
	if err := s.DB().WithContext(ctx).Find(&governanceConfigs).Error; err != nil {
		return nil, err
	}
	// Check if any config is present
	if len(virtualKeys) == 0 && len(teams) == 0 && len(customers) == 0 && len(budgets) == 0 && len(rateLimits) == 0 && len(modelConfigs) == 0 && len(providers) == 0 && len(governanceConfigs) == 0 && len(routingRules) == 0 && len(pricingOverrides) == 0 {
		return nil, nil
	}
	var authConfig *AuthConfig
	var complexityAnalyzerConfig *ComplexityAnalyzerConfig
	if len(governanceConfigs) > 0 {
		// Checking if username and password is present
		var username *string
		var password *string
		var isEnabled bool
		for _, entry := range governanceConfigs {
			switch entry.Key {
			case tables.ConfigAdminUsernameKey:
				username = bifrost.Ptr(entry.Value)
			case tables.ConfigAdminPasswordKey:
				password = bifrost.Ptr(entry.Value)
			case tables.ConfigIsAuthEnabledKey:
				isEnabled = entry.Value == "true"
			case tables.ConfigComplexityAnalyzerConfigKey:
				if strings.TrimSpace(entry.Value) == "" {
					continue
				}
				decoded, err := DecodeComplexityAnalyzerConfig([]byte(entry.Value))
				if err != nil {
					if s.logger != nil {
						s.logger.Warn("failed to load complexity analyzer config from governance_config: %v", err)
					}
					continue
				}
				complexityAnalyzerConfig = decoded
			}
		}
		if username != nil && password != nil {
			authConfig = &AuthConfig{
				AdminUserName: schemas.NewSecretVar(*username),
				AdminPassword: schemas.NewSecretVar(*password),
				IsEnabled:     isEnabled,
			}
		}
	}
	return &GovernanceConfig{
		VirtualKeys:              virtualKeys,
		Teams:                    teams,
		Customers:                customers,
		Budgets:                  budgets,
		RateLimits:               rateLimits,
		ModelConfigs:             modelConfigs,
		Providers:                providers,
		RoutingRules:             routingRules,
		PricingOverrides:         pricingOverrides,
		AuthConfig:               authConfig,
		ComplexityAnalyzerConfig: complexityAnalyzerConfig,
	}, nil
}

// GetComplexityAnalyzerConfig retrieves the typed complexity analyzer config.
func (s *RDBConfigStore) GetComplexityAnalyzerConfig(ctx context.Context) (*ComplexityAnalyzerConfig, error) {
	return s.getComplexityAnalyzerConfigWithDB(ctx, s.DB())
}

func (s *RDBConfigStore) getComplexityAnalyzerConfigWithDB(ctx context.Context, db *gorm.DB) (*ComplexityAnalyzerConfig, error) {
	if db == nil {
		db = s.DB()
	}

	var configEntry tables.TableGovernanceConfig
	err := db.WithContext(ctx).First(&configEntry, "key = ?", tables.ConfigComplexityAnalyzerConfigKey).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if strings.TrimSpace(configEntry.Value) == "" {
		return nil, nil
	}
	decoded, err := DecodeComplexityAnalyzerConfig([]byte(configEntry.Value))
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

// UpdateComplexityAnalyzerConfig normalizes, validates, and persists the typed analyzer config.
func (s *RDBConfigStore) UpdateComplexityAnalyzerConfig(ctx context.Context, config *ComplexityAnalyzerConfig, tx ...*gorm.DB) error {
	if config == nil {
		return fmt.Errorf("complexity analyzer config is nil")
	}

	normalized := config.Normalized()
	if err := normalized.Validate(); err != nil {
		return err
	}

	txDB := s.DB()
	if len(tx) > 0 && tx[0] != nil {
		txDB = tx[0]
	}

	if normalized.ConfigHashes.Empty() {
		existing, err := s.getComplexityAnalyzerConfigWithDB(ctx, txDB)
		if err != nil {
			return err
		}
		if existing != nil {
			normalized.ConfigHashes = existing.ConfigHashes
		}
	}

	raw, err := encodeComplexityAnalyzerConfig(normalized)
	if err != nil {
		return err
	}
	return s.UpdateConfig(ctx, &tables.TableGovernanceConfig{
		Key:   tables.ConfigComplexityAnalyzerConfigKey,
		Value: string(raw),
	}, tx...)
}

// GetAuthConfig retrieves the auth configuration from the database.
func (s *RDBConfigStore) GetAuthConfig(ctx context.Context) (*AuthConfig, error) {
	var username *string
	var password *string
	var isEnabled bool
	if err := s.DB().WithContext(ctx).First(&tables.TableGovernanceConfig{}, "key = ?", tables.ConfigAdminUsernameKey).Select("value").Scan(&username).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	if err := s.DB().WithContext(ctx).First(&tables.TableGovernanceConfig{}, "key = ?", tables.ConfigAdminPasswordKey).Select("value").Scan(&password).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	if err := s.DB().WithContext(ctx).First(&tables.TableGovernanceConfig{}, "key = ?", tables.ConfigIsAuthEnabledKey).Select("value").Scan(&isEnabled).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	if username == nil || password == nil {
		return nil, nil
	}
	return &AuthConfig{
		AdminUserName: schemas.NewSecretVar(*username),
		AdminPassword: schemas.NewSecretVar(*password),
		IsEnabled:     isEnabled,
	}, nil
}

// UpdateAuthConfig updates the auth configuration in the database.
func (s *RDBConfigStore) UpdateAuthConfig(ctx context.Context, config *AuthConfig) error {
	return s.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&tables.TableGovernanceConfig{
			Key:   tables.ConfigAdminUsernameKey,
			Value: config.AdminUserName.GetValue(),
		}).Error; err != nil {
			return err
		}
		if err := tx.Save(&tables.TableGovernanceConfig{
			Key:   tables.ConfigAdminPasswordKey,
			Value: config.AdminPassword.GetValue(),
		}).Error; err != nil {
			return err
		}
		if err := tx.Save(&tables.TableGovernanceConfig{
			Key:   tables.ConfigIsAuthEnabledKey,
			Value: fmt.Sprintf("%t", config.IsEnabled),
		}).Error; err != nil {
			return err
		}
		return nil
	})
}

// GetRestartRequiredConfig retrieves the restart required configuration from the database.
func (s *RDBConfigStore) GetRestartRequiredConfig(ctx context.Context) (*tables.RestartRequiredConfig, error) {
	var configEntry tables.TableGovernanceConfig
	if err := s.DB().WithContext(ctx).First(&configEntry, "key = ?", tables.ConfigRestartRequiredKey).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if configEntry.Value == "" {
		return nil, nil
	}
	var restartConfig tables.RestartRequiredConfig
	if err := json.Unmarshal([]byte(configEntry.Value), &restartConfig); err != nil {
		return nil, fmt.Errorf("failed to unmarshal restart required config: %w", err)
	}
	return &restartConfig, nil
}

// SetRestartRequiredConfig sets the restart required configuration in the database.
func (s *RDBConfigStore) SetRestartRequiredConfig(ctx context.Context, config *tables.RestartRequiredConfig) error {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal restart required config: %w", err)
	}
	return s.DB().WithContext(ctx).Save(&tables.TableGovernanceConfig{
		Key:   tables.ConfigRestartRequiredKey,
		Value: string(configJSON),
	}).Error
}

// ClearRestartRequiredConfig clears the restart required configuration in the database.
func (s *RDBConfigStore) ClearRestartRequiredConfig(ctx context.Context) error {
	return s.DB().WithContext(ctx).Save(&tables.TableGovernanceConfig{
		Key:   tables.ConfigRestartRequiredKey,
		Value: `{"required":false,"reason":""}`,
	}).Error
}

// GetSession retrieves a session from the database.
func (s *RDBConfigStore) GetSession(ctx context.Context, token string) (*tables.SessionsTable, error) {
	var session tables.SessionsTable
	tokenHash := encrypt.HashSHA256(token)
	err := s.DB().WithContext(ctx).First(&session, "token_hash = ?", tokenHash).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Fall back to plaintext lookup for backward compatibility
			if err := s.DB().WithContext(ctx).First(&session, "token = ?", token).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil, nil
				}
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	return &session, nil
}

// CreateSession creates a new session in the database.
func (s *RDBConfigStore) CreateSession(ctx context.Context, session *tables.SessionsTable) error {
	return s.DB().WithContext(ctx).Create(session).Error
}

// DeleteSession deletes a session from the database.
func (s *RDBConfigStore) DeleteSession(ctx context.Context, token string) error {
	tokenHash := encrypt.HashSHA256(token)
	var session tables.SessionsTable
	if err := s.DB().WithContext(ctx).First(&session, "token_hash = ?", tokenHash).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Fall back to plaintext lookup for backward compatibility
			return s.DB().WithContext(ctx).Delete(&tables.SessionsTable{}, "token = ?", token).Error // vault token is saved via tokenHash, so this case will not hit the vault scenario, but we keep it for backward compatibility with any existing plaintext tokens
		}
		return err
	}
	result := s.DB().WithContext(ctx).Delete(&session)
	if result.Error != nil {
		return result.Error
	}
	return nil
}

// FlushSessions flushes all sessions from the database.
func (s *RDBConfigStore) FlushSessions(ctx context.Context) error {
	return s.DB().WithContext(ctx).Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&tables.SessionsTable{}).Error
}

// ExecuteTransaction executes a transaction.
func (s *RDBConfigStore) ExecuteTransaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return s.DB().WithContext(ctx).Transaction(fn)
}

// RetryOnNotFound retries a function up to 3 times with 1-second delays if it returns ErrNotFound
func (s *RDBConfigStore) RetryOnNotFound(ctx context.Context, fn func(ctx context.Context) (any, error), maxRetries int, retryDelay time.Duration) (any, error) {
	var lastErr error
	for attempt := range maxRetries {
		result, err := fn(ctx)
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, ErrNotFound) && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}

		lastErr = err

		// Don't wait after the last attempt
		if attempt < maxRetries-1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryDelay):
				// Continue to next retry
			}
		}
	}
	return nil, lastErr
}

// doesTableExist checks if a table exists in the database.
func (s *RDBConfigStore) doesTableExist(ctx context.Context, tableName string) bool {
	return s.DB().WithContext(ctx).Migrator().HasTable(tableName)
}

func (s *RDBConfigStore) doesColumnExist(ctx context.Context, tableName, columnName string) bool {
	return s.DB().WithContext(ctx).Migrator().HasColumn(tableName, columnName)
}

// removeNullKeys removes null keys from the database.
func (s *RDBConfigStore) removeNullKeys(ctx context.Context) error {
	return s.DB().WithContext(ctx).Exec("DELETE FROM config_keys WHERE key_id IS NULL OR value IS NULL").Error
}

// removeDuplicateKeysAndNullKeys removes duplicate keys based on key_id and value combination
// Keeps the record with the smallest ID (oldest record) and deletes duplicates
func (s *RDBConfigStore) removeDuplicateKeysAndNullKeys(ctx context.Context) error {
	s.logger.Debug("removing duplicate keys and null keys from the database")
	// Check if the config_keys table exists first
	if !s.doesTableExist(ctx, "config_keys") {
		return nil
	}
	s.logger.Debug("removing null keys from the database")
	// First, remove null keys
	if err := s.removeNullKeys(ctx); err != nil {
		return fmt.Errorf("failed to remove null keys: %w", err)
	}
	s.logger.Debug("deleting duplicate keys from the database")
	// Find and delete duplicate keys, keeping only the one with the smallest ID
	// This query deletes all records except the one with the minimum ID for each (key_id, value) pair
	result := s.DB().WithContext(ctx).Exec(`
		DELETE FROM config_keys
		WHERE id NOT IN (
			SELECT MIN(id)
			FROM config_keys
			GROUP BY key_id, value
		)
	`)

	if result.Error != nil {
		return fmt.Errorf("failed to remove duplicate keys: %w", result.Error)
	}
	s.logger.Debug("migration complete")
	return nil
}

// Close closes the SQLite config store.
func (s *RDBConfigStore) Close(ctx context.Context) error {
	sqlDB, err := s.DB().DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// TryAcquireLock attempts to insert a lock row. Returns true if the lock was acquired.
// Uses INSERT ... ON CONFLICT DO NOTHING for atomic lock acquisition.
func (s *RDBConfigStore) TryAcquireLock(ctx context.Context, lock *tables.TableDistributedLock) (bool, error) {
	// Set CreatedAt if not already set
	if lock.CreatedAt.IsZero() {
		lock.CreatedAt = time.Now().UTC()
	}

	// Use GORM clause-based insert for dialect-appropriate SQL
	result := s.DB().WithContext(ctx).Clauses(
		clause.OnConflict{
			Columns:   []clause.Column{{Name: "lock_key"}},
			DoNothing: true,
		},
	).Create(lock)

	if result.Error != nil {
		return false, fmt.Errorf("failed to acquire lock: %w", result.Error)
	}

	// If RowsAffected is 1, the lock was acquired
	return result.RowsAffected == 1, nil
}

// GetLock retrieves a lock by its key. Returns nil if the lock doesn't exist.
func (s *RDBConfigStore) GetLock(ctx context.Context, lockKey string) (*tables.TableDistributedLock, error) {
	var lock tables.TableDistributedLock
	result := s.DB().WithContext(ctx).Where("lock_key = ?", lockKey).First(&lock)

	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get lock: %w", result.Error)
	}

	return &lock, nil
}

// UpdateLockExpiry updates the expiration time for an existing lock.
// Only succeeds if the holder ID matches the current lock holder.
func (s *RDBConfigStore) UpdateLockExpiry(ctx context.Context, lockKey, holderID string, expiresAt time.Time) error {
	result := s.DB().WithContext(ctx).Model(&tables.TableDistributedLock{}).
		Where("lock_key = ? AND holder_id = ? AND expires_at > ?", lockKey, holderID, time.Now().UTC()).
		Update("expires_at", expiresAt)

	if result.Error != nil {
		return fmt.Errorf("failed to update lock expiry: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return ErrLockNotHeld
	}

	return nil
}

// ReleaseLock deletes a lock if the holder ID matches.
// Returns true if the lock was released, false if it wasn't held by the given holder.
func (s *RDBConfigStore) ReleaseLock(ctx context.Context, lockKey, holderID string) (bool, error) {
	result := s.DB().WithContext(ctx).
		Where("lock_key = ? AND holder_id = ?", lockKey, holderID).
		Delete(&tables.TableDistributedLock{})

	if result.Error != nil {
		return false, fmt.Errorf("failed to release lock: %w", result.Error)
	}

	return result.RowsAffected > 0, nil
}

// CleanupExpiredLocks removes all locks that have expired.
// Returns the number of locks cleaned up.
func (s *RDBConfigStore) CleanupExpiredLocks(ctx context.Context) (int64, error) {
	result := s.DB().WithContext(ctx).
		Where("expires_at < ?", time.Now().UTC()).
		Delete(&tables.TableDistributedLock{})

	if result.Error != nil {
		return 0, fmt.Errorf("failed to cleanup expired locks: %w", result.Error)
	}

	return result.RowsAffected, nil
}

// CleanupExpiredLockByKey atomically deletes a specific lock only if it has expired.
// Returns true if an expired lock was deleted, false if the lock doesn't exist or hasn't expired.
func (s *RDBConfigStore) CleanupExpiredLockByKey(ctx context.Context, lockKey string) (bool, error) {
	result := s.DB().WithContext(ctx).
		Where("lock_key = ? AND expires_at < ?", lockKey, time.Now().UTC()).
		Delete(&tables.TableDistributedLock{})

	if result.Error != nil {
		return false, fmt.Errorf("failed to cleanup expired lock: %w", result.Error)
	}

	return result.RowsAffected > 0, nil
}
