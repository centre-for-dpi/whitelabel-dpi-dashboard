package store_test

import (
	"testing"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/store"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/store/storetest"
)

func TestMemory(t *testing.T) {
	storetest.Suite(t, func(*testing.T) store.Store { return store.NewMemory() })
}

func TestDay(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   time.Time
		want time.Time
	}{
		{
			name: "midnight is its own day",
			in:   time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
			want: time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "a moment before midnight is the same day",
			in:   time.Date(2026, 7, 27, 23, 59, 59, 0, time.UTC),
			want: time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
		},
		{
			// The reason Day exists. A collector in Kolkata and one in UTC must
			// agree on which bucket a reading belongs to, or the same day gets
			// two rows depending on who reported it.
			name: "a local time is bucketed by its UTC day",
			in: time.Date(2026, 7, 28, 2, 30, 0, 0,
				time.FixedZone("IST", 5*3600+1800)),
			want: time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := store.Day(tc.in); !got.Equal(tc.want) {
				t.Errorf("Day(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestDialect(t *testing.T) {
	for _, tc := range []struct{ driver, want string }{
		{store.DriverSQLite, "sqlite"},
		{store.DriverPostgres, "postgres"},
		{store.DriverMySQL, "mysql"},
		// MariaDB is wire-compatible with MySQL, so it shares the SQL rather
		// than duplicating a near-identical set of migrations.
		{store.DriverMariaDB, "mysql"},
		{store.DriverMemory, "memory"},
	} {
		t.Run(tc.driver, func(t *testing.T) {
			if got := store.Dialect(tc.driver); got != tc.want {
				t.Errorf("Dialect(%q) = %q, want %q", tc.driver, got, tc.want)
			}
		})
	}
}

func TestUnsupportedDriverNamesTheAlternatives(t *testing.T) {
	// The error a deployment sees after a typo in one config key, so it should
	// say what the legal values are rather than only that this one is wrong.
	err := store.ErrUnsupportedDriver{Driver: "postgress"}

	msg := err.Error()
	for _, want := range []string{"postgress", "sqlite", "postgres", "mysql", "mariadb", "memory"} {
		if !contains(msg, want) {
			t.Errorf("error message does not mention %q: %s", want, msg)
		}
	}
}

func TestRetentionOfZeroKeepsEverything(t *testing.T) {
	// Trimming to nothing would silently empty every chart; a missing setting
	// should mean "no limit", not "no history".
	sv := model.Service{ID: "aadhaar", History: []model.HistoryPoint{
		{Day: storetest.Anchor.AddDate(0, 0, -900), Availability: model.Float(99)},
	}}

	s := store.NewMemory()
	if err := s.Save(t.Context(), model.Snapshot{Services: []model.Service{sv}, GeneratedAt: storetest.Anchor}); err != nil {
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

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
