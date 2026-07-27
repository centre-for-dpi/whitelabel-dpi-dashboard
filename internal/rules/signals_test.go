package rules_test

import (
	"testing"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/rules"
)

var now = time.Date(2026, time.July, 27, 15, 0, 0, 0, time.UTC)

func target(v float64) *float64 { return &v }

// domain returns a configuration with all four rules enabled.
func domain() config.Domain {
	return config.Domain{
		Metrics: []config.Metric{
			{ID: "metric.availability", Field: config.FieldAvailability, Target: target(99.5)},
		},
		Signals: []config.Signal{
			{ID: "sig.belowTarget", Kind: config.SignalBelowTargetDays, Days: 7,
				TitleTermID: "sig.belowTarget.title", RuleTermID: "sig.belowTarget.rule",
				IconKey: "status.partial", Tone: "partial",
				Filter: &config.SignalFilter{Status: []string{"partial", "major"}}},
			{ID: "sig.errorRising", Kind: config.SignalErrorRisingCategories, MinCategory: 3,
				TitleTermID: "sig.errorRising.title", RuleTermID: "sig.errorRising.rule",
				IconKey: "trend.up", Tone: "major"},
			{ID: "sig.longestIncident", Kind: config.SignalLongestOpenIncident,
				TitleTermID: "sig.longestIncident.title", RuleTermID: "sig.longestIncident.rule",
				IconKey: "status.major", Tone: "major"},
			{ID: "sig.maintenance", Kind: config.SignalMaintenanceActive,
				TitleTermID: "sig.maintenance.title", RuleTermID: "sig.maintenance.rule",
				IconKey: "status.maintenance", Tone: "maintenance",
				Filter: &config.SignalFilter{Status: []string{"maintenance"}}},
		},
		SignalsEmpty: config.SignalEmpty{TermID: "sig.empty", IconKey: "ui.check", Tone: "ok"},
	}
}

// only returns a domain with a single rule enabled, so a test exercises one
// rule without the others firing alongside it.
func only(kind string) config.Domain {
	d := domain()
	var keep []config.Signal
	for _, s := range d.Signals {
		if s.Kind == kind {
			keep = append(keep, s)
		}
	}
	d.Signals = keep
	return d
}

// svc builds a service whose availability history is the given daily values.
func svc(id, category string, daily ...float64) model.Service {
	s := model.Service{ID: id, CategoryID: category, Status: model.StatusOperational}
	if len(daily) > 0 {
		s.Metrics.Availability = model.Float(daily[len(daily)-1])
	}
	day := time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC)
	for i, v := range daily {
		s.History = append(s.History, model.HistoryPoint{
			Day:          day.AddDate(0, 0, i),
			Availability: model.Float(v),
		})
	}
	return s
}

func find(t *testing.T, got []rules.Signal, id string) rules.Signal {
	t.Helper()
	for _, s := range got {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no signal %q in %v", id, ids(got))
	return rules.Signal{}
}

func ids(got []rules.Signal) []string {
	out := make([]string, len(got))
	for i, s := range got {
		out[i] = s.ID
	}
	return out
}

func absent(t *testing.T, got []rules.Signal, id string) {
	t.Helper()
	for _, s := range got {
		if s.ID == id {
			t.Fatalf("signal %q fired but should not have", id)
		}
	}
}

// --- below target ----------------------------------------------------------

func TestBelowTargetNeedsEveryDayUnderTarget(t *testing.T) {
	// Every day, not the average. A service that was perfect for six days and
	// down for one has a different problem from one quietly under target all
	// week, and only the second is what this rule looks for.
	under := svc("under", "cat.a", 99.1, 99.2, 99.0, 99.3, 99.1, 99.2, 99.4)
	mostly := svc("mostly", "cat.a", 99.1, 99.2, 99.9, 99.3, 99.1, 99.2, 99.4)

	got := rules.Signals(only(config.SignalBelowTargetDays), []model.Service{under, mostly}, now)

	s := find(t, got, "sig.belowTarget")
	if s.Params["count"] != 1 {
		t.Errorf("count = %v, want 1; only the consistently-under service qualifies", s.Params["count"])
	}
	if s.Params["days"] != 7 {
		t.Errorf("days = %v, want 7", s.Params["days"])
	}
}

func TestBelowTargetLooksOnlyAtTheRecentWindow(t *testing.T) {
	// The ordinary case: 90 days on record, of which only the last 7 matter. A
	// service that recovered a month ago and has been under target all week must
	// fire; one that was bad a month ago and is fine now must not.
	recentlyBad := svc("recentlyBad", "cat.a")
	nowFine := svc("nowFine", "cat.a")
	day := time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC)
	for i := range 90 {
		healthy := model.HistoryPoint{Day: day.AddDate(0, 0, i), Availability: model.Float(99.9)}
		sick := model.HistoryPoint{Day: day.AddDate(0, 0, i), Availability: model.Float(99.1)}

		if i >= 83 { // the last seven days
			recentlyBad.History = append(recentlyBad.History, sick)
			nowFine.History = append(nowFine.History, healthy)
		} else {
			recentlyBad.History = append(recentlyBad.History, healthy)
			nowFine.History = append(nowFine.History, sick)
		}
	}
	recentlyBad.Metrics.Availability = model.Float(99.1)
	nowFine.Metrics.Availability = model.Float(99.9)

	got := rules.Signals(only(config.SignalBelowTargetDays), []model.Service{recentlyBad, nowFine}, now)

	s := find(t, got, "sig.belowTarget")
	if s.Params["count"] != 1 {
		t.Errorf("count = %v, want 1; only the service under target this week qualifies", s.Params["count"])
	}
}

func TestBelowTargetIsSkippedWhenTheWindowIsNotPositive(t *testing.T) {
	// Config rejects this, but a hot reload races validation, and a window of
	// zero would otherwise silently mean "every day on record".
	d := only(config.SignalBelowTargetDays)
	d.Signals[0].Days = 0

	got := rules.Signals(d, []model.Service{svc("under", "cat.a", 99.1, 99.1)}, now)

	absent(t, got, "sig.belowTarget")
}

func TestBelowTargetIgnoresServicesWithoutEnoughHistory(t *testing.T) {
	// Three days under target is not seven days under target, and claiming
	// otherwise would overstate what the data supports.
	short := svc("short", "cat.a", 99.1, 99.2, 99.0)

	got := rules.Signals(only(config.SignalBelowTargetDays), []model.Service{short}, now)

	absent(t, got, "sig.belowTarget")
}

func TestBelowTargetIgnoresUnreportedServices(t *testing.T) {
	// A service with no reading cannot be shown to have missed anything.
	silent := svc("silent", "cat.a", 99.1, 99.1, 99.1, 99.1, 99.1, 99.1, 99.1)
	silent.Metrics.Availability = model.NoFloat()

	got := rules.Signals(only(config.SignalBelowTargetDays), []model.Service{silent}, now)

	absent(t, got, "sig.belowTarget")
}

func TestBelowTargetIgnoresGapsInHistory(t *testing.T) {
	// A day with no reading breaks the run: it is not evidence of a miss.
	gappy := svc("gappy", "cat.a", 99.1, 99.2, 99.0, 99.3, 99.1, 99.2, 99.4)
	gappy.History[3].Availability = model.NoFloat()

	got := rules.Signals(only(config.SignalBelowTargetDays), []model.Service{gappy}, now)

	absent(t, got, "sig.belowTarget")
}

func TestBelowTargetIsSkippedWhenNoTargetIsConfigured(t *testing.T) {
	// Removing the target removes the rule, rather than leaving a rule silently
	// measuring against zero and never firing.
	d := only(config.SignalBelowTargetDays)
	d.Metrics[0].Target = nil

	got := rules.Signals(d, []model.Service{svc("under", "cat.a", 99.1, 99.1, 99.1, 99.1, 99.1, 99.1, 99.1)}, now)

	absent(t, got, "sig.belowTarget")
	if !got[0].Empty {
		t.Error("expected the empty card when no rule can fire")
	}
}

// --- error rising ----------------------------------------------------------

func withErrorRates(s model.Service, past, current float64) model.Service {
	s.History = make([]model.HistoryPoint, 31)
	for i := range s.History {
		s.History[i].ErrorRate = past
	}
	s.History[30].ErrorRate = current
	return s
}

func TestErrorRisingNeedsEnoughCategories(t *testing.T) {
	rising := []model.Service{
		withErrorRates(svc("a", "cat.a"), 0.5, 1.5),
		withErrorRates(svc("b", "cat.b"), 0.5, 1.5),
	}

	// Two categories, minimum of three.
	got := rules.Signals(only(config.SignalErrorRisingCategories), rising, now)
	absent(t, got, "sig.errorRising")

	rising = append(rising, withErrorRates(svc("c", "cat.c"), 0.5, 1.5))
	got = rules.Signals(only(config.SignalErrorRisingCategories), rising, now)

	s := find(t, got, "sig.errorRising")
	if s.Params["count"] != 3 {
		t.Errorf("count = %v, want 3", s.Params["count"])
	}
}

func TestErrorRisingIgnoresOrdinaryDrift(t *testing.T) {
	// A margin, so ordinary wobble does not read as a trend.
	drifting := []model.Service{
		withErrorRates(svc("a", "cat.a"), 0.50, 0.52),
		withErrorRates(svc("b", "cat.b"), 0.50, 0.53),
		withErrorRates(svc("c", "cat.c"), 0.50, 0.51),
	}

	got := rules.Signals(only(config.SignalErrorRisingCategories), drifting, now)

	absent(t, got, "sig.errorRising")
}

func TestErrorRisingAveragesWithinACategory(t *testing.T) {
	// One service worsening inside an otherwise steady category is that
	// service's problem, not the category's.
	services := []model.Service{
		withErrorRates(svc("a1", "cat.a"), 0.5, 3.0),
		withErrorRates(svc("a2", "cat.a"), 0.5, 0.5),
		withErrorRates(svc("a3", "cat.a"), 0.5, 0.5),
		withErrorRates(svc("a4", "cat.a"), 0.5, 0.5),
		withErrorRates(svc("a5", "cat.a"), 0.5, 0.5),
		withErrorRates(svc("a6", "cat.a"), 0.5, 0.5),
	}

	// Average moves 0.5 -> ~0.92, which is above the margin, so it does fire —
	// but with one category, not six.
	got := rules.Signals(only(config.SignalErrorRisingCategories), services, now)
	absent(t, got, "sig.errorRising") // one category is below the minimum of three
}

func TestErrorRisingFiltersToTheCategoriesItFound(t *testing.T) {
	// The action narrows the leaderboard to exactly what the rule found, rather
	// than to a fixed list written in config.
	services := []model.Service{
		withErrorRates(svc("a", "cat.a"), 0.5, 1.5),
		withErrorRates(svc("b", "cat.b"), 0.5, 1.5),
		withErrorRates(svc("c", "cat.c"), 0.5, 1.5),
		withErrorRates(svc("d", "cat.steady"), 0.5, 0.5),
	}

	s := find(t, rules.Signals(only(config.SignalErrorRisingCategories), services, now), "sig.errorRising")

	if s.Filter == nil {
		t.Fatal("no filter on the rising-errors card")
	}
	want := []string{"cat.a", "cat.b", "cat.c"}
	if len(s.Filter.Category) != len(want) {
		t.Fatalf("filter categories = %v, want %v", s.Filter.Category, want)
	}
	for i := range want {
		if s.Filter.Category[i] != want[i] {
			t.Errorf("filter categories = %v, want %v (sorted, since map order is not stable)", s.Filter.Category, want)
		}
	}
}

func TestErrorRisingIgnoresServicesWithNoHistory(t *testing.T) {
	services := []model.Service{
		{ID: "empty", CategoryID: "cat.a"},
		withErrorRates(svc("b", "cat.b"), 0.5, 1.5),
	}

	// Should not panic on the empty history, and cat.a should not be counted.
	got := rules.Signals(only(config.SignalErrorRisingCategories), services, now)
	absent(t, got, "sig.errorRising")
}

// --- longest open incident -------------------------------------------------

func TestLongestOpenIncidentPicksTheOldest(t *testing.T) {
	// The oldest open incident is the one most likely to have stopped being
	// actively worked on, which is why it beats the newest.
	older := model.Service{ID: "older", Incidents: []model.Incident{
		{ID: "i1", Open: true, OpenedAt: now.Add(-9 * time.Hour)},
	}}
	newer := model.Service{ID: "newer", Incidents: []model.Incident{
		{ID: "i2", Open: true, OpenedAt: now.Add(-2 * time.Hour)},
	}}

	s := find(t, rules.Signals(only(config.SignalLongestOpenIncident), []model.Service{newer, older}, now), "sig.longestIncident")

	if s.Focus == nil {
		t.Fatal("no focus on the incident card")
	}
	if s.Focus.ServiceID != "older" {
		t.Errorf("focus service = %q, want older", s.Focus.ServiceID)
	}
	if s.Focus.Tab != "incidents" {
		t.Errorf("focus tab = %q, want incidents", s.Focus.Tab)
	}
	if got := s.Params["seconds"]; got != int64(9*3600) {
		t.Errorf("seconds = %v, want %v", got, int64(9*3600))
	}
}

func TestLongestOpenIncidentIgnoresClosedOnes(t *testing.T) {
	resolved := model.Service{ID: "resolved", Incidents: []model.Incident{
		{ID: "i1", Open: false, OpenedAt: now.Add(-40 * time.Hour)},
	}}

	got := rules.Signals(only(config.SignalLongestOpenIncident), []model.Service{resolved}, now)

	absent(t, got, "sig.longestIncident")
}

func TestLongestOpenIncidentTakesNoFilter(t *testing.T) {
	// The card is about one service, so its action opens that service rather
	// than narrowing the whole board.
	s := find(t, rules.Signals(only(config.SignalLongestOpenIncident), []model.Service{
		{ID: "x", Incidents: []model.Incident{{Open: true, OpenedAt: now.Add(-time.Hour)}}},
	}, now), "sig.longestIncident")

	if s.Filter != nil {
		t.Errorf("filter = %v, want none", s.Filter)
	}
}

// --- maintenance -----------------------------------------------------------

func TestMaintenanceCountsServicesInAWindow(t *testing.T) {
	services := []model.Service{
		{ID: "a", Status: model.StatusMaintenance},
		{ID: "b", Status: model.StatusMaintenance},
		{ID: "c", Status: model.StatusOperational},
	}

	s := find(t, rules.Signals(only(config.SignalMaintenanceActive), services, now), "sig.maintenance")

	if s.Params["count"] != 2 {
		t.Errorf("count = %v, want 2", s.Params["count"])
	}
	if s.Filter == nil || len(s.Filter.Status) != 1 || s.Filter.Status[0] != "maintenance" {
		t.Errorf("filter = %v, want the configured maintenance filter", s.Filter)
	}
}

func TestMaintenanceStaysQuietWhenNothingIsScheduled(t *testing.T) {
	got := rules.Signals(only(config.SignalMaintenanceActive), []model.Service{{ID: "a", Status: model.StatusOperational}}, now)

	absent(t, got, "sig.maintenance")
}

// --- the set as a whole ----------------------------------------------------

func TestEverySignalCarriesItsRuleAndPresentation(t *testing.T) {
	// A card that states a finding without stating its basis is an assertion the
	// reader cannot check, so every firing card must carry a rule term.
	services := []model.Service{
		svc("under", "cat.a", 99.1, 99.2, 99.0, 99.3, 99.1, 99.2, 99.4),
		{ID: "maint", Status: model.StatusMaintenance},
		{ID: "inc", Incidents: []model.Incident{{Open: true, OpenedAt: now.Add(-3 * time.Hour)}}},
	}

	got := rules.Signals(domain(), services, now)

	if len(got) < 3 {
		t.Fatalf("expected at least three findings, got %v", ids(got))
	}
	for _, s := range got {
		if s.RuleTermID == "" {
			t.Errorf("%s has no rule term", s.ID)
		}
		if s.TitleTermID == "" {
			t.Errorf("%s has no title term", s.ID)
		}
		if s.IconKey == "" || s.Tone == "" {
			t.Errorf("%s has no presentation: icon %q tone %q", s.ID, s.IconKey, s.Tone)
		}
	}
}

func TestSignalsPreserveConfiguredOrder(t *testing.T) {
	// The order cards appear in is the deployment's editorial choice.
	services := []model.Service{
		svc("under", "cat.a", 99.1, 99.2, 99.0, 99.3, 99.1, 99.2, 99.4),
		{ID: "maint", Status: model.StatusMaintenance},
	}

	got := rules.Signals(domain(), services, now)

	first, second := -1, -1
	for i, s := range got {
		switch s.ID {
		case "sig.belowTarget":
			first = i
		case "sig.maintenance":
			second = i
		}
	}
	if first < 0 || second < 0 || first > second {
		t.Errorf("cards are out of configured order: %v", ids(got))
	}
}

func TestNothingToReportProducesTheConfiguredCard(t *testing.T) {
	// "Nothing to report" is a statement the deployment makes, in its own words.
	got := rules.Signals(domain(), []model.Service{{ID: "fine", Status: model.StatusOperational}}, now)

	if len(got) != 1 {
		t.Fatalf("got %v, want a single empty card", ids(got))
	}
	if !got[0].Empty {
		t.Error("the card is not marked empty")
	}
	if got[0].TitleTermID != "sig.empty" || got[0].IconKey != "ui.check" || got[0].Tone != "ok" {
		t.Errorf("empty card = %+v, want the configured term, icon and tone", got[0])
	}
}

func TestUnknownSignalKindIsIgnored(t *testing.T) {
	// Config validation rejects these, but the evaluator must not panic if one
	// reaches it — a hot reload races against validation.
	d := domain()
	d.Signals = []config.Signal{{ID: "sig.invented", Kind: "vibesCheck"}}

	got := rules.Signals(d, []model.Service{{ID: "a", Status: model.StatusMaintenance}}, now)

	if len(got) != 1 || !got[0].Empty {
		t.Errorf("got %v, want only the empty card", ids(got))
	}
}

func TestSignalsAreDeterministic(t *testing.T) {
	// The rising-errors rule walks a map, so without sorting the filter it
	// applies would differ between identical requests.
	services := []model.Service{
		withErrorRates(svc("a", "cat.a"), 0.5, 1.5),
		withErrorRates(svc("b", "cat.b"), 0.5, 1.5),
		withErrorRates(svc("c", "cat.c"), 0.5, 1.5),
		withErrorRates(svc("d", "cat.d"), 0.5, 1.5),
	}

	first := rules.Signals(domain(), services, now)
	for range 25 {
		again := rules.Signals(domain(), services, now)
		if len(again) != len(first) {
			t.Fatal("the number of findings varies between identical calls")
		}
		for i := range first {
			a, b := first[i].Filter, again[i].Filter
			if (a == nil) != (b == nil) {
				t.Fatal("a filter appeared or vanished between identical calls")
			}
			if a != nil && len(a.Category) == len(b.Category) {
				for j := range a.Category {
					if a.Category[j] != b.Category[j] {
						t.Fatalf("filter order varies between calls: %v vs %v", a.Category, b.Category)
					}
				}
			}
		}
	}
}
