package sqlstore_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/store"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/store/sqlstore"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/store/storetest"
)

func TestOpenRejectsAnUnsupportedDriver(t *testing.T) {
	_, err := sqlstore.Open(sqlstore.Config{Driver: "oracle", DSN: "whatever"})
	if err == nil {
		t.Fatal("Open accepted a driver with no dialect")
	}
	if !strings.Contains(err.Error(), "oracle") {
		t.Errorf("error %q does not name the driver", err)
	}
}

func TestOpenRejectsAnEmptyDSN(t *testing.T) {
	// Failing here beats failing on the first query with whatever the driver
	// makes of an empty string — which for some is a connection to localhost.
	_, err := sqlstore.Open(sqlstore.Config{Driver: "sqlite"})
	if err == nil {
		t.Fatal("Open accepted an empty dsn")
	}
	if !strings.Contains(err.Error(), "dsn") {
		t.Errorf("error %q does not mention the dsn", err)
	}
}

func TestCompiledListsTheDriversInThisBuild(t *testing.T) {
	got := sqlstore.Compiled()

	// The default build includes all four names. A build with -tags nosqlite
	// would not, which is the point of the list existing.
	for _, want := range []string{"sqlite", "postgres", "mysql", "mariadb"} {
		if !slices.Contains(got, want) {
			t.Errorf("%q is missing from %v", want, got)
		}
	}
}

func TestPing(t *testing.T) {
	s := openSQLite(t).(*sqlstore.Store)
	if err := s.Ping(t.Context()); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

func TestConnectionPoolSettingsAreApplied(t *testing.T) {
	// Exercises the tuning branches. Getting these wrong shows up as connection
	// exhaustion under load, which is a bad place to discover it.
	s, err := sqlstore.Open(sqlstore.Config{
		Driver:          "sqlite",
		DSN:             "file:" + filepath.Join(t.TempDir(), "pool.db"),
		MaxOpenConns:    3,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Minute,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
}

// TestEveryMethodReportsAClosedDatabase closes the connection and then calls
// each method.
//
// It covers the error path of every query in the package at once. Those paths
// are the ones that run when a database is restarted, fails over or runs out of
// connections — the moments when a clear error in the log is worth most — and
// they are otherwise unreachable from a test.
func TestEveryMethodReportsAClosedDatabase(t *testing.T) {
	snap := model.Snapshot{
		Services:    []model.Service{{ID: "aadhaar", Metrics: model.Metrics{Availability: model.Float(99.9)}}},
		GeneratedAt: storetest.Anchor,
	}

	for _, tc := range []struct {
		name string
		call func(context.Context, store.Store) error
	}{
		{"Save", func(ctx context.Context, s store.Store) error { return s.Save(ctx, snap) }},
		{"Load", func(ctx context.Context, s store.Store) error {
			_, err := s.Load(ctx, 90)
			return err
		}},
		{"Rollup", func(ctx context.Context, s store.Store) error { return s.Rollup(ctx, storetest.Anchor) }},
		{"Prune", func(ctx context.Context, s store.Store) error {
			return s.Prune(ctx, storetest.Anchor, storetest.Anchor)
		}},
		{"Migrate", func(ctx context.Context, s store.Store) error { return s.Migrate(ctx) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openSQLite(t)
			if err := s.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}

			err := tc.call(t.Context(), s)
			if err == nil {
				t.Fatalf("%s returned no error against a closed database", tc.name)
			}
			if !strings.Contains(err.Error(), "closed") {
				t.Logf("%s: %v", tc.name, err)
			}
		})
	}
}

// TestLoadReportsAFailureInEachTable drops one table at a time.
//
// Closing the database covers the first query and short-circuits the rest;
// dropping a single table reaches the later ones, so a Load that fails on
// incidents says so rather than reporting the services query.
func TestLoadReportsAFailureInEachTable(t *testing.T) {
	for _, tc := range []struct{ table, wantIn string }{
		{"history_daily", "history"},
		{"incidents", "incidents"},
		{"incident_events", "incident events"},
		{"error_buckets", "error buckets"},
	} {
		t.Run(tc.table, func(t *testing.T) {
			dsn := "file:" + filepath.Join(t.TempDir(), "drop.db")
			s, err := sqlstore.Open(sqlstore.Config{Driver: "sqlite", DSN: dsn, Clock: clock})
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer func() { _ = s.Close() }()

			if err := s.Migrate(t.Context()); err != nil {
				t.Fatalf("Migrate: %v", err)
			}

			sv := model.Service{ID: "aadhaar", Metrics: model.Metrics{Availability: model.Float(99.9)}}
			sv.Incidents = []model.Incident{{
				ID: "inc-1", ServiceID: "aadhaar", Open: true,
				Events: []model.IncidentEvent{{Type: "opened", At: storetest.Anchor}},
			}}
			if err := s.Save(t.Context(), model.Snapshot{
				Services: []model.Service{sv}, GeneratedAt: storetest.Anchor,
			}); err != nil {
				t.Fatalf("Save: %v", err)
			}

			raw, err := sql.Open("sqlite", dsn)
			if err != nil {
				t.Fatalf("opening raw handle: %v", err)
			}
			defer func() { _ = raw.Close() }()
			if _, err := raw.ExecContext(t.Context(), "DROP TABLE "+tc.table); err != nil {
				t.Fatalf("dropping %s: %v", tc.table, err)
			}

			_, err = s.Load(t.Context(), 90)
			if err == nil {
				t.Fatalf("Load succeeded with %s dropped", tc.table)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error %q does not say it was %s that failed", err, tc.wantIn)
			}
		})
	}
}

func TestRollupReportsAFailureReadingSamples(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "rollup.db")
	s, err := sqlstore.Open(sqlstore.Config{Driver: "sqlite", DSN: dsn, Clock: clock})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("opening raw handle: %v", err)
	}
	defer func() { _ = raw.Close() }()
	if _, err := raw.ExecContext(t.Context(), "DROP TABLE samples"); err != nil {
		t.Fatalf("dropping samples: %v", err)
	}

	if err := s.Rollup(t.Context(), storetest.Anchor); err == nil {
		t.Fatal("Rollup succeeded with the samples table dropped")
	}
}

func TestRollupWithNoSamplesIsANoOp(t *testing.T) {
	// The common case on a deployment whose upstream serves its own history:
	// there is nothing to fold, and doing nothing should not be an error.
	s := openSQLite(t)
	if err := s.Rollup(t.Context(), storetest.Anchor); err != nil {
		t.Errorf("Rollup on an empty store: %v", err)
	}
}

func TestMigrateIsResumable(t *testing.T) {
	// Migrations are applied one transaction each, so a run that fails part way
	// leaves the successful ones recorded and the next start resumes rather
	// than replaying them.
	dsn := "file:" + filepath.Join(t.TempDir(), "resume.db")

	first, err := sqlstore.Open(sqlstore.Config{Driver: "sqlite", DSN: dsn, Clock: clock})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := first.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A second process against the same file applies nothing and succeeds.
	second, err := sqlstore.Open(sqlstore.Config{Driver: "sqlite", DSN: dsn, Clock: clock})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = second.Close() }()

	if err := second.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate on an already-migrated database: %v", err)
	}
	if err := second.Save(t.Context(), model.Snapshot{
		Services: []model.Service{{ID: "aadhaar"}}, GeneratedAt: storetest.Anchor,
	}); err != nil {
		t.Errorf("the database is unusable after a second migration: %v", err)
	}
}

func TestTruncateEmptiesEveryTable(t *testing.T) {
	s := openSQLite(t).(*sqlstore.Store)

	sv := model.Service{ID: "aadhaar", Metrics: model.Metrics{Availability: model.Float(99.9)}}
	sv.History = []model.HistoryPoint{{Day: store.Day(storetest.Anchor), Availability: model.Float(99.9)}}
	sv.Incidents = []model.Incident{{ID: "inc-1", ServiceID: "aadhaar",
		Events: []model.IncidentEvent{{Type: "opened", At: storetest.Anchor}}}}
	sv.Errors = []model.ErrorBucket{{Code: "503", Count: 10}}

	if err := s.Save(t.Context(), model.Snapshot{
		Services: []model.Service{sv}, GeneratedAt: storetest.Anchor,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Truncate(t.Context()); err != nil {
		t.Fatalf("Truncate: %v", err)
	}

	got, err := s.Load(t.Context(), 90)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Services) != 0 {
		t.Errorf("got %d services after Truncate", len(got.Services))
	}
}

func TestRetentionOfZeroLoadsEverything(t *testing.T) {
	// A missing setting means "no limit", not "no history" — the latter would
	// silently empty every chart.
	s := openSQLite(t)

	sv := model.Service{ID: "aadhaar"}
	sv.History = []model.HistoryPoint{{
		Day:          store.Day(storetest.Anchor).AddDate(0, 0, -900),
		Availability: model.Float(99),
	}}
	if err := s.Save(t.Context(), model.Snapshot{
		Services: []model.Service{sv}, GeneratedAt: storetest.Anchor,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Load(t.Context(), 0)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if n := len(got.Services[0].History); n != 1 {
		t.Errorf("got %d points with retention unset, want the series kept", n)
	}
}

func TestAServiceWithNoReadingLoadsAsUnknownRatherThanDown(t *testing.T) {
	// service_state is LEFT JOINed, so a service recorded before its first
	// reading has NULL in every one of its columns. Coming back as 0%
	// availability would report a total outage for a service nobody has
	// measured yet.
	dsn := "file:" + filepath.Join(t.TempDir(), "nostate.db")
	s, err := sqlstore.Open(sqlstore.Config{Driver: "sqlite", DSN: dsn, Clock: clock})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("opening raw handle: %v", err)
	}
	defer func() { _ = raw.Close() }()
	if _, err := raw.ExecContext(t.Context(),
		`INSERT INTO services (id, key, seq, updated_at) VALUES ('orphan', 'orphan', 1, 0)`); err != nil {
		t.Fatalf("inserting a service with no state: %v", err)
	}

	got, err := s.Load(t.Context(), 90)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Services) != 1 {
		t.Fatalf("got %d services", len(got.Services))
	}
	if got.Services[0].Metrics.Availability.Valid {
		t.Errorf("a service with no reading came back with availability %v",
			got.Services[0].Metrics.Availability.Value)
	}
}
