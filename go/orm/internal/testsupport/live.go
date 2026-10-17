package testsupport

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"
	"time"
)

// LiveServer is the probe behind every live (non-SQLite) test leg
// (spec/dialects.md "Testing contract"): the admin connection string comes from
// an environment variable or the dialect's conventional default, is tried once
// per process, and a leg skips as a group where nothing answers — so
// `go test ./...` still needs only the Go toolchain. Mirrors the C#
// [PostgresFact]/[SqlServerFact] pattern; the dialect packages' own test
// fixtures (create and drop a `simpleorm_test_<hex>` database) build on it.
type LiveServer struct {
	// EnvironmentVariable names the admin connection string; empty or unset falls back to Default.
	EnvironmentVariable string
	// Default is the conventional local server's admin connection string.
	Default string
	// Open opens the driver pool for a connection string (the dialect's CreateConnection).
	Open func(connectionString string) (*sql.DB, error)

	once      sync.Once
	adminConn string
	available bool
}

// AdminConnectionString is the admin connection string where the server
// answers, and "" where it does not (no driver, no server, wrong credentials).
// Probed once; a five-second budget keeps an absent server from stalling the run.
func (s *LiveServer) AdminConnectionString() string {
	s.once.Do(func() {
		s.adminConn = os.Getenv(s.EnvironmentVariable)
		if s.adminConn == "" {
			s.adminConn = s.Default
		}
		pool, err := s.Open(s.adminConn)
		if err != nil {
			return
		}
		defer pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.available = pool.PingContext(ctx) == nil
	})
	if !s.available {
		return ""
	}
	return s.adminConn
}

// Available reports whether the server answers.
func (s *LiveServer) Available() bool { return s.AdminConnectionString() != "" }

// SkipUnlessAvailable skips the calling test (testing's skip, never a silent
// pass) where the server is absent.
func (s *LiveServer) SkipUnlessAvailable(t testing.TB, what string) {
	t.Helper()
	if !s.Available() {
		t.Skip(what)
	}
}

// DatabaseName is a fresh per-fixture database name (spec/dialects.md: simpleorm_test_<random hex>).
func DatabaseName(t testing.TB) string {
	t.Helper()
	return "simpleorm_test_" + randomHex(t, 8)
}
