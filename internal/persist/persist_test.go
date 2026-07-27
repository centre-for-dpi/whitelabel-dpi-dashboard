package persist_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/persist"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/store"
)

var anchor = time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)

func clock() time.Time { return anchor }

// fakeSource is a driver stand-in whose snapshot the test controls.
type fakeSource struct {
	mu   sync.Mutex
	snap model.Snapshot
}

func (f *fakeSource) Snapshot() model.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap
}

func (f *fakeSource) Store(s model.Snapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snap = s
}

// failingStore reports an error from whichever method the test names.
type failingStore struct {
	store.Store
	failLoad, failSave, failRollup, failPrune bool
}

func (f failingStore) Rollup(ctx context.Context, through time.Time) error {
	if f.failRollup {
		return errors.New("database is down")
	}
	return f.Store.Rollup(ctx, through)
}

func (f failingStore) Prune(ctx context.Context, sampleCutoff, dayCutoff time.Time) error {
	if f.failPrune {
		return errors.New("database is down")
	}
	return f.Store.Prune(ctx, sampleCutoff, dayCutoff)
}

func (f failingStore) Load(ctx context.Context, days int) (model.Snapshot, error) {
	if f.failLoad {
		return model.Snapshot{}, errors.New("database is down")
	}
	return f.Store.Load(ctx, days)
}

func (f failingStore) Save(ctx context.Context, snap model.Snapshot) error {
	if f.failSave {
		return errors.New("database is down")
	}
	return f.Store.Save(ctx, snap)
}

func svc(id string, availability float64) model.Service {
	return model.Service{
		ID: id, Key: id, NameTermID: "svc." + id,
		CategoryID: "cat.identity", RegionID: "reg.national", Scope: "national",
		Metrics:    model.Metrics{Availability: model.Float(availability), Volume: model.Volume{Total: 1000}},
		ObservedAt: anchor,
	}
}

func point(offset int, availability float64) model.HistoryPoint {
	return model.HistoryPoint{
		Day:          store.Day(anchor).AddDate(0, 0, offset),
		Availability: model.Float(availability),
		Volume:       1000,
	}
}

func newRecorder(t *testing.T, src persist.Source, st store.Store) *persist.Recorder {
	t.Helper()
	return persist.New(persist.Options{
		Source:  src,
		Store:   st,
		Storage: config.Storage{Driver: "memory", History: config.History{RetentionDays: 90}},
		Domain:  testDomain(),
		Clock:   clock,
	})
}

func TestSnapshotBeforeFirstSyncFallsBackToTheSource(t *testing.T) {
	// A dashboard with no history is worth more than a blank one, so the raw
	// source is served until the first merge happens.
	src := &fakeSource{snap: model.Snapshot{Services: []model.Service{svc("aadhaar", 99.9)}, GeneratedAt: anchor}}

	got := newRecorder(t, src, store.NewMemory()).Snapshot()

	if len(got.Services) != 1 {
		t.Fatalf("got %d services before the first sync, want the source's", len(got.Services))
	}
}

func TestSyncPersistsAndMerges(t *testing.T) {
	src := &fakeSource{snap: model.Snapshot{Services: []model.Service{svc("aadhaar", 99.9)}, GeneratedAt: anchor}}
	st := store.NewMemory()
	r := newRecorder(t, src, st)

	r.SyncOnce(t.Context())

	stored, err := st.Load(t.Context(), 90)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(stored.Services) != 1 {
		t.Errorf("got %d services in the store, want the snapshot persisted", len(stored.Services))
	}
	if len(r.Snapshot().Services) != 1 {
		t.Errorf("the recorder serves %d services", len(r.Snapshot().Services))
	}
}

func TestRestoreSplicesStoredHistoryIntoALiveSnapshot(t *testing.T) {
	// The whole point of the package: an upstream that reports only a current
	// reading gets its charts back after a restart.
	st := store.NewMemory()

	past := svc("aadhaar", 99.5)
	past.History = []model.HistoryPoint{point(-3, 99.1), point(-2, 99.4), point(-1, 99.6)}
	if err := st.Save(t.Context(), model.Snapshot{Services: []model.Service{past}, GeneratedAt: anchor}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// A fresh process: the source has a reading but no history at all.
	src := &fakeSource{snap: model.Snapshot{Services: []model.Service{svc("aadhaar", 99.9)}, GeneratedAt: anchor}}
	r := newRecorder(t, src, st)

	r.Restore(t.Context())

	got := r.Snapshot()
	if len(got.Services) != 1 {
		t.Fatalf("got %d services", len(got.Services))
	}
	if n := len(got.Services[0].History); n != 3 {
		t.Errorf("got %d history points after restore, want the stored series spliced in", n)
	}
	// The live reading wins over the stored one — restore supplies history, not
	// the current status.
	if v := got.Services[0].Metrics.Availability; !v.Valid || v.Value != 99.9 {
		t.Errorf("availability = %+v, want the live reading", v)
	}
}

func TestLiveHistoryWinsPerDay(t *testing.T) {
	// An upstream correcting a day it already reported should be able to.
	st := store.NewMemory()

	stale := svc("aadhaar", 99.5)
	stale.History = []model.HistoryPoint{point(-1, 90.0)}
	if err := st.Save(t.Context(), model.Snapshot{Services: []model.Service{stale}, GeneratedAt: anchor}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	live := svc("aadhaar", 99.9)
	live.History = []model.HistoryPoint{point(-1, 99.8)}
	src := &fakeSource{snap: model.Snapshot{Services: []model.Service{live}, GeneratedAt: anchor}}

	r := newRecorder(t, src, st)
	r.Restore(t.Context())

	got := r.Snapshot().Services[0]
	if len(got.History) != 1 {
		t.Fatalf("got %d points, want the day recorded once", len(got.History))
	}
	if got.History[0].Availability.Value != 99.8 {
		t.Errorf("availability = %v, want the live reading to win",
			got.History[0].Availability.Value)
	}
}

func TestRestoreServesStoredStateWhenNothingIsLiveYet(t *testing.T) {
	// A push deployment before its first payload arrives. What was stored is
	// the best account of the world available.
	st := store.NewMemory()
	if err := st.Save(t.Context(), model.Snapshot{
		Services: []model.Service{svc("aadhaar", 99.9)}, GeneratedAt: anchor,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	r := newRecorder(t, &fakeSource{}, st)
	r.Restore(t.Context())

	if n := len(r.Snapshot().Services); n != 1 {
		t.Errorf("got %d services, want the stored state served", n)
	}
}

func TestAFailedRestoreDoesNotStopTheDashboard(t *testing.T) {
	// The live status — the thing the page exists to answer — does not depend
	// on stored history, so an unreachable database must not blank the page.
	src := &fakeSource{snap: model.Snapshot{Services: []model.Service{svc("aadhaar", 99.9)}, GeneratedAt: anchor}}
	r := newRecorder(t, src, failingStore{Store: store.NewMemory(), failLoad: true})

	r.Restore(t.Context())

	if n := len(r.Snapshot().Services); n != 1 {
		t.Errorf("got %d services after a failed restore, want the live data still served", n)
	}
}

func TestAFailedSaveStillUpdatesWhatReadersSee(t *testing.T) {
	// Dropping the merge because one write failed would freeze the charts on
	// whatever they last showed, which is worse than losing durability.
	src := &fakeSource{snap: model.Snapshot{Services: []model.Service{svc("aadhaar", 99.9)}, GeneratedAt: anchor}}
	r := newRecorder(t, src, failingStore{Store: store.NewMemory(), failSave: true})

	r.SyncOnce(t.Context())

	if n := len(r.Snapshot().Services); n != 1 {
		t.Errorf("got %d services after a failed save, want the snapshot still published", n)
	}
}

func TestAnUnchangedSourceIsNotRewritten(t *testing.T) {
	// Rewriting on every tick appends a duplicate sample per service, which
	// inflates the rollup's denominator and makes a stalled upstream look busy.
	counting := &countingStore{Store: store.NewMemory()}
	src := &fakeSource{snap: model.Snapshot{Services: []model.Service{svc("aadhaar", 99.9)}, GeneratedAt: anchor}}
	r := newRecorder(t, src, counting)

	r.SyncOnce(t.Context())
	r.SyncOnce(t.Context())
	r.SyncOnce(t.Context())

	if counting.saves != 1 {
		t.Errorf("got %d saves for one unchanged snapshot, want 1", counting.saves)
	}

	// A genuinely new snapshot is written.
	src.Store(model.Snapshot{Services: []model.Service{svc("aadhaar", 97.0)}, GeneratedAt: anchor.Add(time.Minute)})
	r.SyncOnce(t.Context())

	if counting.saves != 2 {
		t.Errorf("got %d saves after a change, want 2", counting.saves)
	}
}

func TestAnEmptySourceIsNotPersisted(t *testing.T) {
	// A poller that has not succeeded yet, or a push deployment before its
	// first payload. Writing an empty snapshot would erase what is stored.
	counting := &countingStore{Store: store.NewMemory()}
	r := newRecorder(t, &fakeSource{}, counting)

	r.SyncOnce(t.Context())

	if counting.saves != 0 {
		t.Errorf("got %d saves for an empty source, want none", counting.saves)
	}
}

func TestRollupSealsYesterdayNotToday(t *testing.T) {
	// Today's bucket is still filling; sealing it now would freeze a partial
	// day as though it were complete.
	st := store.NewMemory()
	src := &fakeSource{snap: model.Snapshot{Services: []model.Service{svc("aadhaar", 99.9)}, GeneratedAt: anchor}}
	r := newRecorder(t, src, st)

	r.SyncOnce(t.Context())
	r.RollupOnce(t.Context())

	got := r.Snapshot()
	if len(got.Services) != 1 {
		t.Fatalf("got %d services", len(got.Services))
	}
	if n := len(got.Services[0].History); n != 0 {
		t.Errorf("got %d history points, want today left unsealed", n)
	}
}

func TestRollupSealsACompletedDay(t *testing.T) {
	st := store.NewMemory()

	yesterday := svc("aadhaar", 99.9)
	yesterday.ObservedAt = store.Day(anchor).AddDate(0, 0, -1).Add(3 * time.Hour)
	src := &fakeSource{snap: model.Snapshot{Services: []model.Service{yesterday}, GeneratedAt: anchor}}
	r := newRecorder(t, src, st)

	r.SyncOnce(t.Context())
	r.RollupOnce(t.Context())

	got := r.Snapshot()
	if n := len(got.Services[0].History); n != 1 {
		t.Fatalf("got %d history points, want yesterday sealed", n)
	}
	if v := got.Services[0].History[0].Availability; !v.Valid || v.Value != 99.9 {
		t.Errorf("availability = %+v", v)
	}
}

func TestPushIsPersistedImmediately(t *testing.T) {
	// A collector that gets a 200 back has been told the data was accepted.
	// Waiting for the next tick to make that true would lose a payload to a
	// restart.
	src := &fakeSource{}
	st := store.NewMemory()

	r := persist.NewSink(persist.Options{
		Source:  src,
		Store:   st,
		Storage: config.Storage{Driver: "memory", History: config.History{RetentionDays: 90}},
		Domain:  testDomain(),
		Clock:   clock,
	}, src)

	r.Store(model.Snapshot{Services: []model.Service{svc("aadhaar", 99.9)}, GeneratedAt: anchor})

	stored, err := st.Load(t.Context(), 90)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(stored.Services) != 1 {
		t.Errorf("got %d services in the store, want the push persisted before the call returned",
			len(stored.Services))
	}
}

func TestRunPersistsAndStopsOnCancel(t *testing.T) {
	src := &fakeSource{snap: model.Snapshot{Services: []model.Service{svc("aadhaar", 99.9)}, GeneratedAt: anchor}}
	st := store.NewMemory()

	r := persist.New(persist.Options{
		Source:       src,
		Store:        st,
		Storage:      config.Storage{Driver: "memory", History: config.History{RetentionDays: 90, RollupIntervalMinutes: 1}},
		Domain:       testDomain(),
		Clock:        clock,
		SyncInterval: time.Millisecond,
	})

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	// Wait for the first sync to land rather than sleeping a fixed interval.
	deadline := time.After(2 * time.Second)
	for {
		stored, err := st.Load(context.Background(), 90)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(stored.Services) > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Run never persisted the snapshot")
		case <-time.After(time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop when its context was cancelled")
	}
}

func TestDefaultsAreUsableWithNothingConfigured(t *testing.T) {
	// A Recorder built with no clock, no logger and no interval must still
	// work: main supplies a logger, but a test or an embedding caller may not,
	// and a nil logger would panic on the first error rather than at
	// construction.
	src := &fakeSource{snap: model.Snapshot{
		Services: []model.Service{svc("aadhaar", 99.9)}, GeneratedAt: anchor,
	}}
	r := persist.New(persist.Options{Source: src, Store: store.NewMemory()})

	r.SyncOnce(t.Context())

	if n := len(r.Snapshot().Services); n != 1 {
		t.Errorf("got %d services from a default-configured recorder", n)
	}
}

func TestZeroHistorySettingsFallBackRatherThanDeletingEverything(t *testing.T) {
	// A retention of zero reaching Prune as a cutoff of "now" would discard
	// every bucket. Unset means unconfigured, so the defaults apply.
	st := store.NewMemory()

	yesterday := svc("aadhaar", 99.9)
	yesterday.ObservedAt = store.Day(anchor).AddDate(0, 0, -1).Add(time.Hour)
	yesterday.History = []model.HistoryPoint{point(-1, 99.9)}
	src := &fakeSource{snap: model.Snapshot{Services: []model.Service{yesterday}, GeneratedAt: anchor}}

	r := persist.New(persist.Options{
		Source: src,
		Store:  st,
		// Deliberately empty: no retention, no raw retention, no interval.
		Storage: config.Storage{Driver: "memory"},
		Domain:  testDomain(),
		Clock:   clock,
	})

	r.SyncOnce(t.Context())
	r.RollupOnce(t.Context())

	got := r.Snapshot()
	if len(got.Services) != 1 {
		t.Fatalf("got %d services", len(got.Services))
	}
	if n := len(got.Services[0].History); n != 1 {
		t.Errorf("got %d history points, want the default retention to have kept yesterday", n)
	}
}

func TestRollupSurvivesAFailingStore(t *testing.T) {
	// A rollup that cannot reach the database should log and move on. The live
	// status does not depend on it, and taking the dashboard down would turn a
	// maintenance-window database restart into a reported outage.
	src := &fakeSource{snap: model.Snapshot{
		Services: []model.Service{svc("aadhaar", 99.9)}, GeneratedAt: anchor,
	}}

	for _, tc := range []struct {
		name string
		st   store.Store
	}{
		{"rollup fails", failingStore{Store: store.NewMemory(), failRollup: true}},
		{"prune fails", failingStore{Store: store.NewMemory(), failPrune: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRecorder(t, src, tc.st)
			r.SyncOnce(t.Context())
			r.RollupOnce(t.Context())

			if n := len(r.Snapshot().Services); n != 1 {
				t.Errorf("got %d services after a failed rollup, want the dashboard still serving", n)
			}
		})
	}
}

// countingStore records how many times Save was called.
type countingStore struct {
	store.Store
	saves int
}

func (c *countingStore) Save(ctx context.Context, snap model.Snapshot) error {
	c.saves++
	return c.Store.Save(ctx, snap)
}

// testDomain is the minimum config the derived-field pass needs.
func testDomain() config.Domain {
	return config.Domain{
		Thresholds: config.Thresholds{
			EvaluationOrder: []string{"maintenance", "unknown", "major", "partial", "operational"},
			Values: config.ThresholdValues{
				MajorAvailBelow:   99,
				MajorErrAbove:     2,
				PartialAvailBelow: 99.9,
				PartialErrAbove:   1,
				StaleSecondsAbove: 3600,
			},
		},
	}
}
