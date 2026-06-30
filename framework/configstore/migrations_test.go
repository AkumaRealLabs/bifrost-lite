package configstore

import (
	"context"
	"testing"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var testMigrationLogger = bifrost.NewDefaultLogger(schemas.LogLevelInfo)

const postgresDSN = "host=localhost user=bifrost password=bifrost_password dbname=bifrost port=5432 sslmode=disable"

func TestLiteMigrationCreatesCurrentSchema(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	require.NoError(t, triggerMigrations(context.Background(), db, testMigrationLogger))
	require.True(t, db.Migrator().HasTable(&tables.TableProvider{}))
	require.True(t, db.Migrator().HasTable(&tables.TableVirtualKey{}))
	require.True(t, db.Migrator().HasTable(&tables.TableModelPricing{}))
	require.False(t, db.Migrator().HasTable("config_mcp_clients"))
	require.False(t, db.Migrator().HasTable("oauth_configs"))
	require.False(t, db.Migrator().HasTable("temp_tokens"))

	require.NoError(t, triggerMigrations(context.Background(), db, testMigrationLogger))
}
