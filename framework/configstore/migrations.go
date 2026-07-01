package configstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/migrator"
	"gorm.io/gorm"
)

const (
	migrationAdvisoryLockKey  = 1000001
	advisoryLockRetryInterval = 5 * time.Second
	advisoryLockTimeout       = 1 * time.Minute
)

type migrationLock struct {
	conn *sql.Conn
}

func acquireMigrationLock(ctx context.Context, db *gorm.DB, logger schemas.Logger) (*migrationLock, error) {
	if db.Dialector.Name() != "postgres" {
		return &migrationLock{}, nil
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get sql.DB: %w", err)
	}

	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dedicated connection: %w", err)
	}

	logger.Info("[configstore] attempting to get migration lock %d", migrationAdvisoryLockKey)
	deadline := time.Now().Add(advisoryLockTimeout)
	maxAttempts := int(advisoryLockTimeout / advisoryLockRetryInterval)
	attempt := 0

	for {
		attempt++
		attemptTimeout := time.Until(deadline)
		if attemptTimeout <= 0 {
			attemptTimeout = advisoryLockRetryInterval
		}
		attemptCtx, attemptCancel := context.WithTimeout(ctx, attemptTimeout)
		var acquired bool
		err = conn.QueryRowContext(attemptCtx, "SELECT pg_try_advisory_lock($1)", migrationAdvisoryLockKey).Scan(&acquired)
		attemptCancel()
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("failed to attempt migration advisory lock: %w", err)
		}
		if acquired {
			if attempt > 1 {
				logger.Info("[configstore] migration lock acquired after %d attempts", attempt)
			}
			return &migrationLock{conn: conn}, nil
		}
		if time.Now().After(deadline) {
			conn.Close()
			return nil, fmt.Errorf(
				"failed to acquire configstore migration lock (key=%d) after %d attempts over %s\n\n"+
					"This usually means another Bifrost pod, or a previous crashed pod's lingering database session, is still holding the lock.\n\n"+
					"Find the holder:\n"+
					"SELECT pid, usename, application_name, client_addr, backend_start, state, query FROM pg_stat_activity WHERE pid IN (SELECT pid FROM pg_locks WHERE locktype = 'advisory' AND objid = %d AND granted = true);\n\n"+
					"If it belongs to a dead pod, terminate it:\n"+
					"SELECT pg_terminate_backend(<pid_from_query>);",
				migrationAdvisoryLockKey, attempt, advisoryLockTimeout, migrationAdvisoryLockKey,
			)
		}
		logger.Info("[configstore] waiting for migration lock (attempt %d/%d), retrying in %s", attempt, maxAttempts, advisoryLockRetryInterval)
		select {
		case <-ctx.Done():
			conn.Close()
			return nil, fmt.Errorf("context cancelled while waiting for migration lock: %w", ctx.Err())
		case <-time.After(advisoryLockRetryInterval):
		}
	}
}

func (l *migrationLock) release(ctx context.Context) {
	if l.conn == nil {
		return
	}
	_, _ = l.conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", migrationAdvisoryLockKey)
	l.conn.Close()
}

func RunSingleMigration(ctx context.Context, options *migrator.Options, db *gorm.DB, logger schemas.Logger, migration *migrator.Migration) error {
	if db == nil {
		return fmt.Errorf("db cannot be nil")
	}
	if migration == nil {
		return fmt.Errorf("migration cannot be nil")
	}
	migrationOpts := migrator.DefaultOptions
	if options != nil {
		migrationOpts = options
	}
	m := migrator.New(db.WithContext(ctx), migrationOpts, []*migrator.Migration{migration})
	return m.Migrate()
}

func triggerMigrations(ctx context.Context, db *gorm.DB, logger schemas.Logger) error {
	migrations := liteSchemaMigrations(ctx, db, logger)
	ids := make([]string, 0, len(migrations))
	for _, migration := range migrations {
		ids = append(ids, migration.ID)
	}

	pending, err := migrator.PendingIDs(ctx, db, migrator.DefaultOptions, ids)
	if err != nil {
		logger.Warn("[configstore] migration preflight failed; acquiring migration lock and running migrations: %v", err)
	} else if len(pending) == 0 {
		logger.Info("[configstore] no pending migrations; skipping migration run")
		return nil
	}

	lock, err := acquireMigrationLock(ctx, db, logger)
	if err != nil {
		return err
	}
	defer lock.release(ctx)

	pending, err = migrator.PendingIDs(ctx, db, migrator.DefaultOptions, ids)
	if err == nil && len(pending) == 0 {
		logger.Info("[configstore] migrations completed by another node; skipping migration run")
		return nil
	}
	if err != nil {
		logger.Warn("[configstore] migration preflight after lock failed; running migrations: %v", err)
	}

	logger.Info("[configstore] starting migrations")
	defer logger.Info("[configstore] finished migrations")

	return migrator.New(db.WithContext(ctx), migrator.DefaultOptions, migrations).Migrate()
}

func liteSchemaMigrations(ctx context.Context, db *gorm.DB, logger schemas.Logger) []*migrator.Migration {
	return []*migrator.Migration{
		{
			ID: "lite_schema_init",
			Migrate: func(tx *gorm.DB) error {
				tx = tx.WithContext(ctx)
				if err := tx.SetupJoinTable(&tables.TableVirtualKeyProviderConfig{}, "Keys", &tables.TableVirtualKeyProviderConfigKey{}); err != nil {
					return err
				}
				return tx.AutoMigrate(liteSchemaModels()...)
			},
		},
		{
			ID: "remove_ttfb_routing_config",
			Migrate: func(tx *gorm.DB) error {
				tx = tx.WithContext(ctx)
				return migrator.DropColumnIfExists(tx, logger, &tables.TableClientConfig{}, "ttfb_routing_json")
			},
		},
	}
}

func liteSchemaModels() []interface{} {
	return []interface{}{
		&tables.TableConfigHash{},
		&tables.TableBudget{},
		&tables.TableRateLimit{},
		&tables.TableProvider{},
		&tables.TableKey{},
		&tables.TableModel{},
		&tables.TableClientConfig{},
		&tables.TableEnvKey{},
		&tables.TableVectorStoreConfig{},
		&tables.TableLogStoreConfig{},
		&tables.TableCustomer{},
		&tables.TableTeam{},
		&tables.TableVirtualKey{},
		&tables.TableVirtualKeyProviderConfig{},
		&tables.TableVirtualKeyProviderConfigKey{},
		&tables.TableGovernanceConfig{},
		&tables.TableModelPricing{},
		&tables.TablePricingOverride{},
		&tables.TablePlugin{},
		&tables.TableFrameworkConfig{},
		&tables.TableRoutingRule{},
		&tables.TableRoutingTarget{},
		&tables.TableModelConfig{},
		&tables.TableModelParameters{},
		&tables.SessionsTable{},
		&tables.TableDistributedLock{},
		&tables.TableProviderCooldown{},
	}
}
