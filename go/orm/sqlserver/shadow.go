package sqlserver

// The SQL Server shadow host (ADR-0043, spec/dialects.md "Migration tooling"): the throwaway database is
// `simpleorm_shadow_<hex>`, created beside the admin connection's database and dropped afterwards after
// `alter database … set single_user with rollback immediate`. There is no trigger read-back on SQL Server: the
// stored text renders `{new: cols}` as `inserted`, so the declaration is not recoverable and the shadow
// snapshots a trigger from the step.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
)

// ShadowHost provisions a `simpleorm_shadow_<hex>` database on the admin connection.
type ShadowHost struct{}

var _ migrations.ShadowHost = ShadowHost{}

// Provision creates the database and returns the connection string that opens it with the function that drops it.
func (ShadowHost) Provision(ctx context.Context, admin string) (string, func(context.Context) error, error) {
	dialect := New()
	name := "simpleorm_shadow_" + randomHex(8)
	quoted := dialect.QuoteIdentifier(name)

	adminPool, err := dialect.CreateConnection(admin)
	if err != nil {
		return "", nil, err
	}
	if _, err := adminPool.ExecContext(ctx, "create database "+quoted); err != nil {
		_ = adminPool.Close()
		return "", nil, err
	}
	drop := func(ctx context.Context) error {
		defer adminPool.Close()
		// Open connections on the database are closed by the caller first; this evicts any straggler.
		_, _ = adminPool.ExecContext(ctx, "alter database "+quoted+" set single_user with rollback immediate")
		_, err := adminPool.ExecContext(ctx, "drop database "+quoted)
		return err
	}
	return connectionStringForDatabase(admin, name), drop, nil
}

// connectionStringForDatabase is the admin connection string pointed at one database: the `database` query
// parameter of a URL, else the `database=` key of an ADO-style string.
func connectionStringForDatabase(admin, database string) string {
	if strings.Contains(admin, "://") {
		if u, err := url.Parse(admin); err == nil {
			query := u.Query()
			query.Set("database", database)
			u.RawQuery = query.Encode()
			return u.String()
		}
	}
	return strings.TrimRight(admin, "; ") + ";database=" + database
}

func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf) // crypto/rand only fails when the OS entropy source is broken
	return hex.EncodeToString(buf)
}
