package sqlstore_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/store"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/store/sqlstore"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/store/storetest"
)

// clock is fixed so migration timestamps do not vary between runs.
func clock() time.Time { return storetest.Anchor }

// TestSQLite runs the full contract against SQLite.
//
// It needs no environment and no container, so it runs on every `go test` and
// is the backend that actually guards the SQL on a normal build. Postgres and
// MySQL run the identical suite when their DSNs are set.
func TestSQLite(t *testing.T) {
	storetest.Suite(t, func(t *testing.T) store.Store {
		return openSQLite(t)
	})
}

func openSQLite(t *testing.T) store.Store {
	t.Helper()

	// A file rather than :memory:. An in-memory SQLite database is per
	// connection, so database/sql's pool would hand different queries different
	// empty databases — a failure that looks like data vanishing at random.
	dsn := "file:" + filepath.Join(t.TempDir(), "test.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"

	s, err := sqlstore.Open(sqlstore.Config{Driver: "sqlite", DSN: dsn, Clock: clock})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

// TestPostgres runs the identical contract against Postgres.
//
// Gated on a DSN because it needs a server. `make test-db` brings one up. The
// gate is a skip rather than a failure so a clone with no Docker still gets a
// green build — and the CI job that sets the DSN is what makes the swappability
// claim true rather than merely intended.
func TestPostgres(t *testing.T) {
	dsn := os.Getenv("DPI_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set DPI_TEST_POSTGRES_DSN to run; `make test-db` starts one")
	}
	storetest.Suite(t, func(t *testing.T) store.Store {
		return openServer(t, "postgres", dsn)
	})
}

// TestMySQL runs the identical contract against MySQL, and covers MariaDB with
// it — they share a driver and a dialect.
func TestMySQL(t *testing.T) {
	dsn := os.Getenv("DPI_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set DPI_TEST_MYSQL_DSN to run; `make test-db` starts one")
	}
	storetest.Suite(t, func(t *testing.T) store.Store {
		return openServer(t, "mysql", dsn)
	})
}

func TestMariaDB(t *testing.T) {
	dsn := os.Getenv("DPI_TEST_MARIADB_DSN")
	if dsn == "" {
		t.Skip("set DPI_TEST_MARIADB_DSN to run; `make test-db` starts one")
	}
	storetest.Suite(t, func(t *testing.T) store.Store {
		return openServer(t, "mariadb", dsn)
	})
}

// openServer connects to a shared server and gives the test an empty schema.
//
// Unlike SQLite, these back onto one database that every test in the run
// shares, so each test truncates first. Truncating beats dropping and
// recreating: it is faster, and it keeps the migration path under test on every
// single case rather than only the first.
func openServer(t *testing.T, driver, dsn string) store.Store {
	t.Helper()

	s, err := sqlstore.Open(sqlstore.Config{Driver: driver, DSN: dsn, Clock: clock})
	if err != nil {
		t.Fatalf("Open %s: %v", driver, err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.Ping(t.Context()); err != nil {
		t.Fatalf("connecting to %s: %v", driver, err)
	}
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate %s: %v", driver, err)
	}
	if err := s.Truncate(t.Context()); err != nil {
		t.Fatalf("Truncate %s: %v", driver, err)
	}
	return s
}
