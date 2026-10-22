package conformance

import (
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/postgres"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlserver"
)

// The live legs of migrations-cases/ (ADR-0043, spec/migrations.md "Conformance cases"): the postgres/ and
// sqlserver/ subfolders hold the same format in that dialect's SQL with stamped snapshots, and run the same
// runner against a real engine — each case in a throwaway database the dialect's shadow host provisions.
// They skip as a group where no server answers (spec/dialects.md "Testing contract"), so `go test ./...`
// still needs only the Go toolchain.

var (
	livePostgres = &testsupport.LiveServer{
		EnvironmentVariable: "SIMPLEORM_POSTGRES",
		Default:             "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable",
		Open:                postgres.New().CreateConnection,
	}
	liveSQLServer = &testsupport.LiveServer{
		EnvironmentVariable: "SIMPLEORM_SQLSERVER",
		Default:             "sqlserver://localhost:1433?database=master&encrypt=disable",
		Open:                sqlserver.New().CreateConnection,
	}
)

func TestMigrationsCases_PostgresLive(t *testing.T) {
	livePostgres.SkipUnlessAvailable(t, "no PostgreSQL server answered (set SIMPLEORM_POSTGRES to an admin connection string)")
	runMigrationsCases(t, "migrations-cases/postgres", migrationsHarness{
		dialect: postgres.New(), host: postgres.ShadowHost{}, admin: livePostgres.AdminConnectionString(),
	})
}

func TestMigrationsCases_SQLServerLive(t *testing.T) {
	liveSQLServer.SkipUnlessAvailable(t, "no SQL Server answered (set SIMPLEORM_SQLSERVER to a go-mssqldb admin connection string)")
	runMigrationsCases(t, "migrations-cases/sqlserver", migrationsHarness{
		dialect: sqlserver.New(), host: sqlserver.ShadowHost{}, admin: liveSQLServer.AdminConnectionString(),
	})
}
