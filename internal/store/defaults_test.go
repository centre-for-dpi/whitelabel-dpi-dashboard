package store_test

import (
	"testing"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/store"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/store/storetest"
)

func TestMemoryCloseIsANoOp(t *testing.T) {
	// The Store contract requires it, and a deployment switching to memory
	// should not have to special-case shutdown.
	if err := store.NewMemory().Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestPruningEverythingRemovesTheServiceEntirely(t *testing.T) {
	// Leaving an empty slice behind would grow the map forever on a deployment
	// whose service catalogue churns.
	s := store.NewMemory()

	sv := model.Service{ID: "retired", ObservedAt: storetest.Anchor.AddDate(0, 0, -200)}
	sv.History = []model.HistoryPoint{{
		Day:          store.Day(storetest.Anchor).AddDate(0, 0, -200),
		Availability: model.Float(99),
	}}
	if err := s.Save(t.Context(), model.Snapshot{
		Services: []model.Service{sv}, GeneratedAt: storetest.Anchor,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := s.Prune(t.Context(), storetest.Anchor, storetest.Anchor); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	got, err := s.Load(t.Context(), 90)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Services) != 1 {
		t.Fatalf("got %d services; the record itself should survive a prune", len(got.Services))
	}
	if n := len(got.Services[0].History); n != 0 {
		t.Errorf("got %d history points, want all of them expired", n)
	}
}

func TestPruningKeepsWhatIsStillInsideTheWindow(t *testing.T) {
	s := store.NewMemory()

	recent := model.Service{ID: "aadhaar", ObservedAt: storetest.Anchor}
	recent.History = []model.HistoryPoint{
		{Day: store.Day(storetest.Anchor), Availability: model.Float(99.9)},
	}
	if err := s.Save(t.Context(), model.Snapshot{
		Services: []model.Service{recent}, GeneratedAt: storetest.Anchor,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// A cutoff before everything stored: nothing should be discarded.
	if err := s.Prune(t.Context(),
		storetest.Anchor.AddDate(0, 0, -7),
		storetest.Anchor.AddDate(0, 0, -90),
	); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	got, err := s.Load(t.Context(), 90)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if n := len(got.Services[0].History); n != 1 {
		t.Errorf("got %d history points, want the recent one kept", n)
	}
}

func TestRollupSamplesOnAnEmptySliceIsZero(t *testing.T) {
	if got := store.RollupSamples(nil); !got.Day.IsZero() || got.Samples != 0 {
		t.Errorf("got %+v for no samples, want a zero point", got)
	}
}

func TestRollupSamplesAveragesRatesAndSumsTraffic(t *testing.T) {
	// The distinction the whole windowed-metric model rests on: a rate over a
	// period is a mean, traffic over a period is a total.
	day := store.Day(storetest.Anchor)
	got := store.RollupSamples([]model.Sample{
		{At: day, Availability: model.Float(98), ErrorRate: 1, LatencyP50: 100, Volume: 10},
		{At: day.Add(time.Hour), Availability: model.Float(100), ErrorRate: 3, LatencyP50: 300, Volume: 20},
	})

	if v := got.Availability; !v.Valid || v.Value != 99 {
		t.Errorf("availability = %+v, want the mean 99", v)
	}
	if got.ErrorRate != 2 {
		t.Errorf("errorRate = %v, want the mean 2", got.ErrorRate)
	}
	if got.LatencyP50 != 200 {
		t.Errorf("latencyP50 = %v, want the mean 200", got.LatencyP50)
	}
	if got.Volume != 30 {
		t.Errorf("volume = %d, want the sum 30", got.Volume)
	}
	if got.Samples != 2 {
		t.Errorf("samples = %d, want 2", got.Samples)
	}
}

func TestRollupSamplesWithNoReadingAtAllLeavesAvailabilityAbsent(t *testing.T) {
	// Not 0%. A day in which nothing reported is a day we cannot speak to, and
	// the chart should show a gap rather than a cliff.
	day := store.Day(storetest.Anchor)
	got := store.RollupSamples([]model.Sample{
		{At: day, ErrorRate: 1, Volume: 10},
		{At: day.Add(time.Hour), ErrorRate: 1, Volume: 10},
	})

	if got.Availability.Valid {
		t.Errorf("availability = %v, want it absent", got.Availability.Value)
	}
	// Traffic still counts: the collector saw requests, it just could not
	// compute an availability from them.
	if got.Volume != 20 {
		t.Errorf("volume = %d, want 20", got.Volume)
	}
}

func TestMergeHistoryOrdersAscendingAndDeduplicates(t *testing.T) {
	day := func(o int) time.Time { return store.Day(storetest.Anchor).AddDate(0, 0, o) }

	got := store.MergeHistory(
		[]model.HistoryPoint{
			{Day: day(-1), Availability: model.Float(90)},
			{Day: day(-3), Availability: model.Float(93)},
		},
		[]model.HistoryPoint{
			{Day: day(-1), Availability: model.Float(99)}, // corrects the same day
			{Day: day(-2), Availability: model.Float(92)},
		},
	)

	if len(got) != 3 {
		t.Fatalf("got %d points, want 3 distinct days", len(got))
	}
	for i := 1; i < len(got); i++ {
		if !got[i].Day.After(got[i-1].Day) {
			t.Fatalf("not ascending: %v", got)
		}
	}
	if got[2].Availability.Value != 99 {
		t.Errorf("the corrected day is %v, want the incoming value to win",
			got[2].Availability.Value)
	}
}
