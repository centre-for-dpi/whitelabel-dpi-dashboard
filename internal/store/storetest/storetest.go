// Package storetest is one contract suite, run against every backend.
//
// This is what makes "swappable by one config key" a claim rather than a hope.
// The identical assertions run against the in-memory store, SQLite, Postgres
// and MySQL; a behaviour that differs between them is a failure in whichever
// one differs, not a footnote in the documentation.
//
// It lives in its own package so the SQL backends can import it without a test
// dependency cycle.
package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/store"
)

// Factory opens a fresh, empty store. Called once per test, so no test can see
// another's data.
type Factory func(t *testing.T) store.Store

// Anchor is the moment every fixture is relative to. Fixed, so a test asserting
// on a bucket boundary cannot flake at midnight.
var Anchor = time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)

// Suite runs the whole contract.
func Suite(t *testing.T, open Factory) {
	t.Helper()

	for _, tc := range []struct {
		name string
		run  func(*testing.T, Factory)
	}{
		{"an empty store loads empty", emptyLoad},
		{"a saved snapshot survives a reload", saveAndLoad},
		{"absence is preserved, not zeroed", absencePreserved},
		{"upstream history is stored", historyStored},
		{"history accumulates across saves", historyAccumulates},
		{"a later reading for a day wins", laterReadingWins},
		{"history is not erased when an upstream stops sending it", historyNotErased},
		{"services are updated in place", servicesUpdated},
		{"samples roll up into daily buckets", rollupWorks},
		{"a rolled-up bucket does not overwrite the upstream's own", rollupYieldsToUpstream},
		{"rollup skips days with no reading", rollupSkipsAbsentReadings},
		{"rollup only folds days that are complete", rollupRespectsThrough},
		{"pruning discards expired data", pruneWorks},
		{"retention bounds what is loaded", retentionBounds},
		{"migrating twice is safe", migrateIsIdempotent},
		{"incidents and errors survive", richFieldsSurvive},
	} {
		t.Run(tc.name, func(t *testing.T) { tc.run(t, open) })
	}
}

func ctx() context.Context { return context.Background() }

// svc builds a service with a current reading and no history.
func svc(id string, availability float64) model.Service {
	return model.Service{
		ID: id, Key: id, NameTermID: "svc." + id, CategoryID: "cat.identity",
		RegionID: "reg.national", Scope: "national", Status: model.StatusOperational,
		Metrics: model.Metrics{
			Availability: model.Float(availability),
			ErrorRate:    0.4,
			LatencyP50:   231,
			StaleSeconds: 45,
			Volume:       model.Volume{Total: 1000, Success: 996},
		},
		ObservedAt: Anchor,
	}
}

func day(offset int) time.Time { return store.Day(Anchor).AddDate(0, 0, offset) }

func point(offset int, availability float64) model.HistoryPoint {
	return model.HistoryPoint{
		Day:          day(offset),
		Availability: model.Float(availability),
		ErrorRate:    0.4,
		LatencyP50:   231,
		Volume:       1000,
		Samples:      96,
	}
}

func snapshot(services ...model.Service) model.Snapshot {
	return model.Snapshot{Services: services, GeneratedAt: Anchor}
}

func save(t *testing.T, s store.Store, snap model.Snapshot) {
	t.Helper()
	if err := s.Save(ctx(), snap); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func load(t *testing.T, s store.Store) model.Snapshot {
	t.Helper()
	got, err := s.Load(ctx(), 90)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return got
}

func find(t *testing.T, snap model.Snapshot, id string) model.Service {
	t.Helper()
	for _, sv := range snap.Services {
		if sv.ID == id {
			return sv
		}
	}
	t.Fatalf("no service %q in %d services", id, len(snap.Services))
	return model.Service{}
}

// --- the contract -----------------------------------------------------------

func emptyLoad(t *testing.T, open Factory) {
	// A first start reads before anything has been written.
	got := load(t, open(t))

	if len(got.Services) != 0 {
		t.Errorf("got %d services from an empty store", len(got.Services))
	}
}

func saveAndLoad(t *testing.T, open Factory) {
	s := open(t)
	save(t, s, snapshot(svc("aadhaar", 99.91), svc("pan", 97.2)))

	got := load(t, s)

	if len(got.Services) != 2 {
		t.Fatalf("got %d services", len(got.Services))
	}
	sv := find(t, got, "aadhaar")
	if !sv.Metrics.Availability.Valid || sv.Metrics.Availability.Value != 99.91 {
		t.Errorf("availability = %+v", sv.Metrics.Availability)
	}
	if sv.Metrics.ErrorRate != 0.4 || sv.Metrics.LatencyP50 != 231 {
		t.Errorf("metrics = %+v", sv.Metrics)
	}
	if sv.Metrics.Volume.Total != 1000 || sv.Metrics.Volume.Success != 996 {
		t.Errorf("volume = %+v", sv.Metrics.Volume)
	}
	if sv.CategoryID != "cat.identity" || sv.RegionID != "reg.national" || sv.Scope != "national" {
		t.Errorf("taxonomy = %+v", sv)
	}
}

func absencePreserved(t *testing.T, open Factory) {
	// The invariant the whole status model rests on. A store that round-trips a
	// null as a zero turns "we cannot tell" into "it is completely down".
	sv := svc("silent", 0)
	sv.Metrics.Availability = model.NoFloat()

	s := open(t)
	save(t, s, snapshot(sv))

	got := find(t, load(t, s), "silent")
	if got.Metrics.Availability.Valid {
		t.Errorf("an absent availability came back as %v", got.Metrics.Availability.Value)
	}
}

func historyStored(t *testing.T, open Factory) {
	sv := svc("aadhaar", 99.91)
	sv.History = []model.HistoryPoint{point(-2, 99.5), point(-1, 99.7), point(0, 99.9)}

	s := open(t)
	save(t, s, snapshot(sv))

	got := find(t, load(t, s), "aadhaar")
	if len(got.History) != 3 {
		t.Fatalf("got %d history points", len(got.History))
	}
	// Oldest first, which is the order the charts plot.
	for i := 1; i < len(got.History); i++ {
		if !got.History[i].Day.After(got.History[i-1].Day) {
			t.Fatalf("history is not in ascending order: %v", got.History)
		}
	}
	if !got.History[0].Availability.Valid || got.History[0].Availability.Value != 99.5 {
		t.Errorf("first point = %+v", got.History[0])
	}
	if got.History[0].Volume != 1000 {
		t.Errorf("volume = %d", got.History[0].Volume)
	}
}

func historyAccumulates(t *testing.T, open Factory) {
	// The reason the store exists: successive polls build a series that outlives
	// any one of them.
	s := open(t)

	first := svc("aadhaar", 99.5)
	first.History = []model.HistoryPoint{point(-2, 99.5)}
	save(t, s, snapshot(first))

	second := svc("aadhaar", 99.9)
	second.History = []model.HistoryPoint{point(-1, 99.9)}
	save(t, s, snapshot(second))

	got := find(t, load(t, s), "aadhaar")
	if len(got.History) != 2 {
		t.Errorf("got %d history points, want both saves accumulated", len(got.History))
	}
}

func laterReadingWins(t *testing.T, open Factory) {
	// An upstream correcting yesterday's figure should be able to.
	s := open(t)

	first := svc("aadhaar", 99.5)
	first.History = []model.HistoryPoint{point(-1, 99.0)}
	save(t, s, snapshot(first))

	second := svc("aadhaar", 99.5)
	second.History = []model.HistoryPoint{point(-1, 99.8)}
	save(t, s, snapshot(second))

	got := find(t, load(t, s), "aadhaar")
	if len(got.History) != 1 {
		t.Fatalf("got %d points, want the day recorded once", len(got.History))
	}
	if got.History[0].Availability.Value != 99.8 {
		t.Errorf("availability = %v, want the corrected value", got.History[0].Availability.Value)
	}
}

func historyNotErased(t *testing.T, open Factory) {
	// An upstream that stops serving history — or a mapping that drops the
	// block — must not wipe what is already stored.
	s := open(t)

	withHistory := svc("aadhaar", 99.5)
	withHistory.History = []model.HistoryPoint{point(-1, 99.5)}
	save(t, s, snapshot(withHistory))

	save(t, s, snapshot(svc("aadhaar", 99.9))) // no history at all

	got := find(t, load(t, s), "aadhaar")
	if len(got.History) != 1 {
		t.Errorf("got %d history points; the stored series was erased", len(got.History))
	}
}

func servicesUpdated(t *testing.T, open Factory) {
	s := open(t)
	save(t, s, snapshot(svc("aadhaar", 99.5)))
	save(t, s, snapshot(svc("aadhaar", 97.2)))

	got := load(t, s)
	if len(got.Services) != 1 {
		t.Fatalf("got %d services, want the service updated rather than duplicated", len(got.Services))
	}
	if got.Services[0].Metrics.Availability.Value != 97.2 {
		t.Errorf("availability = %v, want the later reading", got.Services[0].Metrics.Availability.Value)
	}
}

func rollupWorks(t *testing.T, open Factory) {
	// What a deployment gets when its upstream reports only a current reading:
	// the dashboard builds the history itself.
	s := open(t)

	for i, availability := range []float64{99.0, 99.5, 100.0} {
		sv := svc("aadhaar", availability)
		sv.ObservedAt = day(-1).Add(time.Duration(i) * time.Hour)
		save(t, s, model.Snapshot{Services: []model.Service{sv}, GeneratedAt: Anchor})
	}

	if err := s.Rollup(ctx(), Anchor); err != nil {
		t.Fatalf("Rollup: %v", err)
	}

	got := find(t, load(t, s), "aadhaar")
	if len(got.History) != 1 {
		t.Fatalf("got %d history points, want the three samples folded into one day", len(got.History))
	}
	p := got.History[0]
	if !p.Availability.Valid || p.Availability.Value < 99.49 || p.Availability.Value > 99.51 {
		t.Errorf("availability = %+v, want the mean of the three samples", p.Availability)
	}
	// Traffic is summed rather than averaged: a day's traffic is the total of
	// its samples.
	if p.Volume != 3000 {
		t.Errorf("volume = %d, want the three samples summed", p.Volume)
	}
	if p.Samples != 3 {
		t.Errorf("samples = %d, want 3", p.Samples)
	}
}

func rollupYieldsToUpstream(t *testing.T, open Factory) {
	// An upstream reporting its own history knows more about its own day than
	// the dashboard's sparse sampling of it does.
	s := open(t)

	sv := svc("aadhaar", 50.0) // a deliberately wrong current reading
	sv.ObservedAt = day(-1).Add(time.Hour)
	sv.History = []model.HistoryPoint{point(-1, 99.9)}
	save(t, s, snapshot(sv))

	if err := s.Rollup(ctx(), Anchor); err != nil {
		t.Fatalf("Rollup: %v", err)
	}

	got := find(t, load(t, s), "aadhaar")
	if len(got.History) != 1 {
		t.Fatalf("got %d points", len(got.History))
	}
	if got.History[0].Availability.Value != 99.9 {
		t.Errorf("availability = %v; the rollup overwrote what the upstream reported",
			got.History[0].Availability.Value)
	}
}

func rollupSkipsAbsentReadings(t *testing.T, open Factory) {
	// A gap in reporting is not an outage, and averaging it in as zero would
	// punish a service for its collector having been offline.
	s := open(t)

	reported := svc("aadhaar", 100.0)
	reported.ObservedAt = day(-1)
	save(t, s, model.Snapshot{Services: []model.Service{reported}, GeneratedAt: Anchor})

	silent := svc("aadhaar", 0)
	silent.Metrics.Availability = model.NoFloat()
	silent.ObservedAt = day(-1).Add(time.Hour)
	save(t, s, model.Snapshot{Services: []model.Service{silent}, GeneratedAt: Anchor})

	if err := s.Rollup(ctx(), Anchor); err != nil {
		t.Fatalf("Rollup: %v", err)
	}

	got := find(t, load(t, s), "aadhaar")
	if len(got.History) != 1 {
		t.Fatalf("got %d points", len(got.History))
	}
	if v := got.History[0].Availability; !v.Valid || v.Value != 100 {
		t.Errorf("availability = %+v, want 100 from the one reported sample", v)
	}
}

func rollupRespectsThrough(t *testing.T, open Factory) {
	// Today's bucket is still filling; folding it now would freeze a partial
	// day as if it were complete.
	s := open(t)

	today := svc("aadhaar", 99.9)
	today.ObservedAt = Anchor
	save(t, s, snapshot(today))

	if err := s.Rollup(ctx(), Anchor.AddDate(0, 0, -1)); err != nil {
		t.Fatalf("Rollup: %v", err)
	}

	got := find(t, load(t, s), "aadhaar")
	if len(got.History) != 0 {
		t.Errorf("got %d points; a day past the cutoff was rolled up", len(got.History))
	}
}

func pruneWorks(t *testing.T, open Factory) {
	s := open(t)

	old := svc("aadhaar", 99.5)
	old.History = []model.HistoryPoint{point(-100, 99.5), point(-1, 99.9)}
	old.ObservedAt = day(-100)
	save(t, s, snapshot(old))

	if err := s.Prune(ctx(), day(-2), day(-90)); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	got := find(t, load(t, s), "aadhaar")
	if len(got.History) != 1 {
		t.Errorf("got %d points, want the expired one discarded", len(got.History))
	}
	if len(got.History) > 0 && got.History[0].Day.Before(day(-90)) {
		t.Errorf("an expired bucket survived: %v", got.History[0].Day)
	}
}

func retentionBounds(t *testing.T, open Factory) {
	// A deployment shortening its retention should see shorter charts on the
	// next load, without waiting for a prune.
	sv := svc("aadhaar", 99.5)
	sv.History = []model.HistoryPoint{point(-10, 99.1), point(-1, 99.9)}

	s := open(t)
	save(t, s, snapshot(sv))

	got, err := s.Load(ctx(), 3)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if n := len(find(t, got, "aadhaar").History); n != 1 {
		t.Errorf("got %d points within a three-day window, want 1", n)
	}
}

func migrateIsIdempotent(t *testing.T, open Factory) {
	// Called on every start, including after a rollback that left the schema
	// ahead of the binary.
	s := open(t)
	for range 3 {
		if err := s.Migrate(ctx()); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
	}
	save(t, s, snapshot(svc("aadhaar", 99.9)))
	if len(load(t, s).Services) != 1 {
		t.Error("the store is unusable after repeated migration")
	}
}

func richFieldsSurvive(t *testing.T, open Factory) {
	sv := svc("aadhaar", 99.9)
	sv.Status = model.StatusPartial
	sv.Maintenance = model.Maintenance{Active: true, Until: Anchor.Add(2 * time.Hour), ReasonTermID: "maint.scheduled"}
	sv.Incidents = []model.Incident{{
		ID: "inc-1", ServiceID: "aadhaar", Severity: model.StatusMajor,
		OpenedAt: Anchor.Add(-3 * time.Hour), Open: true, NoteTermID: "incident.note.errorSpike",
		Events: []model.IncidentEvent{{Type: "opened", At: Anchor.Add(-3 * time.Hour)}},
	}}
	sv.Errors = []model.ErrorBucket{
		{Code: "503", TermID: "err.503", Class: model.ErrorClassServer, Count: 100, Share: 60, Trend: model.DirectionUp},
	}

	s := open(t)
	save(t, s, snapshot(sv))

	got := find(t, load(t, s), "aadhaar")
	if got.Status != model.StatusPartial {
		t.Errorf("status = %q", got.Status)
	}
	if !got.Maintenance.Active || got.Maintenance.ReasonTermID != "maint.scheduled" {
		t.Errorf("maintenance = %+v", got.Maintenance)
	}
	if len(got.Incidents) != 1 || got.Incidents[0].ID != "inc-1" {
		t.Errorf("incidents = %+v", got.Incidents)
	}
	if len(got.Incidents) == 1 {
		if !got.Incidents[0].Open {
			t.Error("an open incident came back closed")
		}
		if len(got.Incidents[0].Events) != 1 {
			t.Errorf("incident events = %+v", got.Incidents[0].Events)
		}
	}
	if len(got.Errors) != 1 || got.Errors[0].Code != "503" {
		t.Errorf("errors = %+v", got.Errors)
	}
}
