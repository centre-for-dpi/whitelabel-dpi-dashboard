package seed_test

import (
	"os"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/source/seed"
)

var now = time.Date(2026, time.July, 27, 15, 0, 0, 0, time.UTC)

func domain() config.Domain {
	return config.Domain{
		Thresholds: config.Thresholds{
			EvaluationOrder: []string{"maintenance", "unknown", "major", "partial", "operational"},
			Values: config.ThresholdValues{
				MajorAvailBelow:   99.0,
				MajorErrAbove:     2.0,
				PartialAvailBelow: 99.5,
				PartialErrAbove:   1.0,
				StaleSecondsAbove: 900,
			},
		},
	}
}

// shipped loads the catalogue the repository actually ships, so these tests
// exercise the real demo rather than a convenient fiction.
func shipped(t *testing.T) seed.Catalogue {
	t.Helper()
	raw, err := os.ReadFile("../../../examples/seed-catalogue.yaml")
	if err != nil {
		t.Fatalf("reading the shipped catalogue: %v", err)
	}
	var cat seed.Catalogue
	if err := yaml.Unmarshal(raw, &cat); err != nil {
		t.Fatalf("parsing the shipped catalogue: %v", err)
	}
	return cat
}

func generate(t *testing.T) model.Snapshot {
	t.Helper()
	return seed.Generate(shipped(t), domain(), seed.DefaultOptions(now))
}

func TestGenerationIsReproducible(t *testing.T) {
	// Two people looking at the same demo must see the same thing, and a
	// screenshot taken today must still match tomorrow.
	cat, d := shipped(t), domain()

	first := seed.Generate(cat, d, seed.DefaultOptions(now))
	for range 5 {
		again := seed.Generate(cat, d, seed.DefaultOptions(now))

		if len(again.Services) != len(first.Services) {
			t.Fatal("service count varies between runs")
		}
		for i := range first.Services {
			a, b := first.Services[i], again.Services[i]
			if a.ID != b.ID || a.Status != b.Status || a.Metrics != b.Metrics {
				t.Fatalf("service %s differs between runs:\n %+v\n %+v", a.ID, a.Metrics, b.Metrics)
			}
		}
	}
}

func TestEveryDeclaredCategoryIsRepresented(t *testing.T) {
	// The prototype truncated its expansion to a round number, which silently
	// dropped agriculture and records entirely — their filter chips were present
	// and always returned nothing. This is the regression test for that.
	cat := shipped(t)
	snap := generate(t)

	declared := map[string]bool{}
	for _, e := range cat.Services {
		declared[e.Category] = true
	}
	present := map[string]int{}
	for _, sv := range snap.Services {
		present[sv.CategoryID]++
	}

	for c := range declared {
		if present[c] == 0 {
			t.Errorf("category %q is declared in the catalogue but no service was generated for it", c)
		}
	}
}

func TestStateServicesExpandAcrossEveryRegion(t *testing.T) {
	cat := shipped(t)
	snap := generate(t)

	byRegion := map[string]int{}
	for _, sv := range snap.Services {
		byRegion[sv.RegionID]++
	}

	for _, region := range cat.StateExpansion {
		if byRegion[region] == 0 {
			t.Errorf("region %q has no services", region)
		}
	}
	if byRegion["reg.national"] == 0 {
		t.Error("no national services were generated")
	}
}

func TestServiceIdentifiersAreUnique(t *testing.T) {
	// Ids are the upsert key and the drawer's address. A collision would make
	// two services share a page.
	seen := map[string]bool{}
	for _, sv := range generate(t).Services {
		if seen[sv.ID] {
			t.Errorf("duplicate service id %q", sv.ID)
		}
		seen[sv.ID] = true
	}
}

func TestStatusComesFromTheEvaluatorNotTheGenerator(t *testing.T) {
	// The whole point of the demo is to demonstrate the published rule. If the
	// generator asserted statuses directly, the dashboard could show a service
	// as healthy while its own numbers said otherwise.
	d := domain()

	for _, sv := range generate(t).Services {
		want := evaluateByHand(sv, d.Thresholds.Values)
		if sv.Status != want {
			t.Errorf("%s: status %q does not follow from its own metrics (%+v); want %q",
				sv.ID, sv.Status, sv.Metrics, want)
		}
	}
}

// evaluateByHand re-implements the published rule independently, so this test
// fails if the generator and the evaluator ever disagree.
func evaluateByHand(sv model.Service, v config.ThresholdValues) model.Status {
	switch {
	case sv.Maintenance.Active:
		return model.StatusMaintenance
	case !sv.Metrics.Availability.Valid, sv.Metrics.StaleSeconds > v.StaleSecondsAbove:
		return model.StatusUnknown
	case sv.Metrics.Availability.Value < v.MajorAvailBelow, sv.Metrics.ErrorRate > v.MajorErrAbove:
		return model.StatusMajor
	case sv.Metrics.Availability.Value < v.PartialAvailBelow, sv.Metrics.ErrorRate > v.PartialErrAbove:
		return model.StatusPartial
	default:
		return model.StatusOperational
	}
}

func TestEveryStatusAppearsInTheDemo(t *testing.T) {
	// A demo in which everything is healthy shows the reader nothing about what
	// an unhealthy service looks like.
	counts := map[model.Status]int{}
	for _, sv := range generate(t).Services {
		counts[sv.Status]++
	}

	for _, s := range []model.Status{
		model.StatusOperational,
		model.StatusPartial,
		model.StatusMajor,
		model.StatusUnknown,
		model.StatusMaintenance,
	} {
		if counts[s] == 0 {
			t.Errorf("no service is %q; the demo does not exercise that state", s)
		}
	}
	t.Logf("status mix: %v", counts)
}

func TestUnhealthyServicesAreSpreadThroughTheList(t *testing.T) {
	// Evenly, rather than in a block, so that any view — one category, one
	// region, the first screenful — contains some variety.
	services := generate(t).Services
	if len(services) < 40 {
		t.Skipf("catalogue too small to test spread (%d services)", len(services))
	}

	quarter := len(services) / 4
	for q := range 4 {
		lo := q * quarter
		hi := min(lo+quarter, len(services))

		healthy := 0
		for _, sv := range services[lo:hi] {
			if sv.Status == model.StatusOperational {
				healthy++
			}
		}
		if healthy == hi-lo {
			t.Errorf("services %d-%d are all healthy; the unhealthy ones are not spread evenly", lo, hi)
		}
	}
}

func TestHistoryIsGeneratedForEveryService(t *testing.T) {
	opts := seed.DefaultOptions(now)

	for _, sv := range generate(t).Services {
		if len(sv.History) != opts.HistoryDays {
			t.Fatalf("%s has %d days of history, want %d", sv.ID, len(sv.History), opts.HistoryDays)
		}
		// Ascending, oldest first, which is the order the charts plot.
		for i := 1; i < len(sv.History); i++ {
			if !sv.History[i].Day.After(sv.History[i-1].Day) {
				t.Fatalf("%s: history is not in ascending date order at index %d", sv.ID, i)
			}
		}
		if last := sv.History[len(sv.History)-1].Day; last.After(now) {
			t.Errorf("%s: history runs into the future, to %v", sv.ID, last)
		}
	}
}

func TestUnreportedServicesHaveNoAvailabilityHistory(t *testing.T) {
	// A service that is not reporting now was not reporting then either.
	// Inventing a history for it would contradict its own status.
	for _, sv := range generate(t).Services {
		if sv.Metrics.Availability.Valid {
			continue
		}
		for i, p := range sv.History {
			if p.Availability.Valid {
				t.Fatalf("%s reports no availability but its history has a reading on day %d", sv.ID, i)
			}
		}
	}
}

func TestSuccessCountNeverExceedsTotal(t *testing.T) {
	// The two numbers are rendered as "X of Y succeeded", so they have to agree.
	for _, sv := range generate(t).Services {
		v := sv.Metrics.Volume
		if v.Success > v.Total {
			t.Errorf("%s: %d successes out of %d requests", sv.ID, v.Success, v.Total)
		}
		if v.Total <= 0 {
			t.Errorf("%s: no traffic at all", sv.ID)
		}
	}
}

func TestErrorSharesAreConsistent(t *testing.T) {
	for _, sv := range generate(t).Services {
		var sum float64
		for _, e := range sv.Errors {
			if e.Count <= 0 {
				t.Errorf("%s: error bucket %q has count %d", sv.ID, e.Code, e.Count)
			}
			sum += e.Share
		}
		// Buckets rounding to zero are dropped, so the shares need not total
		// exactly 100 — but they must not wildly exceed it.
		if sum > 101 {
			t.Errorf("%s: error shares total %.1f%%", sv.ID, sum)
		}
		// Largest first: the reader wants to know what is failing most.
		for i := 1; i < len(sv.Errors); i++ {
			if sv.Errors[i].Count > sv.Errors[i-1].Count {
				t.Errorf("%s: error buckets are not ordered by count", sv.ID)
				break
			}
		}
	}
}

func TestOpenIncidentsExistAndAreConsistent(t *testing.T) {
	open := 0
	for _, sv := range generate(t).Services {
		for _, inc := range sv.Incidents {
			if inc.ServiceID != sv.ID {
				t.Errorf("%s: incident %q is attributed to %q", sv.ID, inc.ID, inc.ServiceID)
			}
			if len(inc.Events) == 0 {
				t.Errorf("%s: incident %q has no timeline", sv.ID, inc.ID)
			}
			if inc.Open {
				open++
				if !inc.ClosedAt.IsZero() {
					t.Errorf("%s: incident %q is open but has a closing time", sv.ID, inc.ID)
				}
			} else if inc.ClosedAt.Before(inc.OpenedAt) {
				t.Errorf("%s: incident %q closed before it opened", sv.ID, inc.ID)
			}
			if inc.OpenedAt.After(now) {
				t.Errorf("%s: incident %q opens in the future", sv.ID, inc.ID)
			}
		}
		// Newest first, which is the order the drawer reads them in.
		for i := 1; i < len(sv.Incidents); i++ {
			if sv.Incidents[i].OpenedAt.After(sv.Incidents[i-1].OpenedAt) {
				t.Errorf("%s: incidents are not newest-first", sv.ID)
				break
			}
		}
	}

	// The longest-open-incident signal has nothing to say without one.
	if open == 0 {
		t.Error("no open incidents were generated, so the incident signal can never fire")
	}
}

func TestTrendsAreComputedForEveryMetric(t *testing.T) {
	for _, sv := range generate(t).Services {
		for _, field := range []string{
			config.FieldAvailability,
			config.FieldErrorRate,
			config.FieldLatencyP50,
			config.FieldVolume,
		} {
			tr, ok := sv.Trends[field]
			if !ok {
				t.Fatalf("%s has no trend for %q", sv.ID, field)
			}
			if tr.Direction == "" {
				t.Errorf("%s: trend for %q has no direction", sv.ID, field)
			}
		}
	}
}

func TestKnownServicesGetTheirConfiguredTraffic(t *testing.T) {
	// Real services vary hugely in traffic, and a chart where everything looks
	// equally busy teaches the reader nothing.
	var busiest, quietest int64 = 0, 1 << 62
	for _, sv := range generate(t).Services {
		busiest = max(busiest, sv.Metrics.Volume.Total)
		quietest = min(quietest, sv.Metrics.Volume.Total)
	}

	if busiest < quietest*20 {
		t.Errorf("traffic spans only %dx (from %d to %d); the demo has no sense of scale",
			busiest/max(quietest, 1), quietest, busiest)
	}
}

func TestEmptyCatalogueProducesNothingRatherThanPanicking(t *testing.T) {
	snap := seed.Generate(seed.Catalogue{}, domain(), seed.DefaultOptions(now))

	if len(snap.Services) != 0 {
		t.Errorf("got %d services from an empty catalogue", len(snap.Services))
	}
	if !snap.GeneratedAt.Equal(now) {
		t.Errorf("generatedAt = %v, want %v", snap.GeneratedAt, now)
	}
}

func TestMixLargerThanTheCatalogueIsClamped(t *testing.T) {
	cat := seed.Catalogue{
		Mix:            seed.Mix{Major: 50, Partial: 50, Maintenance: 50, Unknown: 50},
		DefaultTraffic: 1000,
		Services: []seed.Entry{
			{Key: "a", Category: "cat.a", Provider: "p", Scope: "national"},
			{Key: "b", Category: "cat.b", Provider: "p", Scope: "national"},
		},
	}

	snap := seed.Generate(cat, domain(), seed.DefaultOptions(now))

	if len(snap.Services) != 2 {
		t.Fatalf("got %d services, want 2", len(snap.Services))
	}
}

func TestZeroHistoryDaysIsHandled(t *testing.T) {
	opts := seed.DefaultOptions(now)
	opts.HistoryDays = 0

	snap := seed.Generate(shipped(t), domain(), opts)

	for _, sv := range snap.Services {
		if len(sv.History) != 0 {
			t.Fatalf("%s got history despite a zero window", sv.ID)
		}
	}
}

func TestDifferentSeedsProduceDifferentData(t *testing.T) {
	// Otherwise the seed parameter would be decorative.
	cat, d := shipped(t), domain()

	a := seed.Generate(cat, d, seed.Options{Seed: 1000, HistoryDays: 30, Now: now})
	b := seed.Generate(cat, d, seed.Options{Seed: 2000, HistoryDays: 30, Now: now})

	same := 0
	for i := range a.Services {
		if a.Services[i].Metrics == b.Services[i].Metrics {
			same++
		}
	}
	if same == len(a.Services) {
		t.Error("changing the seed changed nothing")
	}
}
