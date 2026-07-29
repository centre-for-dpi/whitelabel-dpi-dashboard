package query_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/query"
)

// labels is a stand-in for the i18n resolver. Keeping it this simple is the
// point: query never needs to know what a locale is.
type labels struct{}

func (labels) Name(s model.Service) string { return s.NameTermID }
func (labels) Haystack(s model.Service) string {
	return s.NameTermID + " " + s.DescTermID + " " + s.CategoryID + " " + s.RegionID
}
func (labels) Compare(a, b string) int { return strings.Compare(a, b) }

var l = labels{}

func view(days int) query.View {
	return query.View{Domain: domain(), Labeler: l, Days: days}
}

// v is the default 30-day frame most tests use.
var v = func() query.View { return view(30) }

func domain() config.Domain {
	return config.Domain{
		DefaultScope: "national",
		Taxonomy: config.Taxonomy{Regions: []config.Region{
			{ID: "reg.national", Scope: "national"},
			{ID: "reg.mh", Scope: "state"},
			{ID: "reg.ka", Scope: "state"},
		}},
		Metrics: []config.Metric{
			{ID: "metric.availability", Field: config.FieldAvailability},
			{ID: "metric.errorRate", Field: config.FieldErrorRate},
			{ID: "metric.latencyP50", Field: config.FieldLatencyP50},
			{ID: "metric.volume", Field: config.FieldVolume},
		},
		StatusModel: config.StatusModel{
			Order:    config.Statuses,
			Severity: map[string]int{"major": 4, "partial": 3, "unknown": 2, "maintenance": 1, "operational": 0},
		},
	}
}

func svc(id string, opts ...func(*model.Service)) model.Service {
	s := model.Service{
		ID: id, NameTermID: id, DescTermID: "desc." + id,
		CategoryID: "cat.identity", RegionID: "reg.national",
		Scope: "national", Status: model.StatusOperational,
		Metrics: model.Metrics{Availability: model.Float(99.9), ErrorRate: 0.1},
	}
	for _, o := range opts {
		o(&s)
	}
	return s
}

func avail(v float64) func(*model.Service) {
	return func(s *model.Service) { s.Metrics.Availability = model.Float(v) }
}
func noAvail() func(*model.Service) {
	return func(s *model.Service) { s.Metrics.Availability = model.NoFloat() }
}
func errRate(v float64) func(*model.Service) {
	return func(s *model.Service) { s.Metrics.ErrorRate = v }
}
func status(v model.Status) func(*model.Service) {
	return func(s *model.Service) { s.Status = v }
}
func scope(sc, region string) func(*model.Service) {
	return func(s *model.Service) { s.Scope, s.RegionID = sc, region }
}
func category(c string) func(*model.Service) {
	return func(s *model.Service) { s.CategoryID = c }
}

func idsOf(list []model.Service) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = s.ID
	}
	return out
}

func assertIDs(t *testing.T, got []model.Service, want ...string) {
	t.Helper()
	g := idsOf(got)
	if len(g) != len(want) {
		t.Fatalf("got %v, want %v", g, want)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Fatalf("got %v, want %v", g, want)
		}
	}
}

// --- scope -----------------------------------------------------------------

func TestScopeSeparatesNationalFromSubNational(t *testing.T) {
	// The two views answer different questions, so a service belongs to one or
	// the other rather than appearing in both.
	all := []model.Service{
		svc("national-1"),
		svc("mh-1", scope("state", "reg.mh")),
		svc("ka-1", scope("state", "reg.ka")),
	}
	assertIDs(t, v().Scope(all, "national", ""), "national-1")
	assertIDs(t, v().Scope(all, "state", ""), "mh-1", "ka-1")
}

func TestScopeNarrowsToOneRegion(t *testing.T) {
	all := []model.Service{
		svc("mh-1", scope("state", "reg.mh")),
		svc("ka-1", scope("state", "reg.ka")),
	}

	assertIDs(t, v().Scope(all, "state", "reg.mh"), "mh-1")
}

func TestTheNationalRegionMeansAllRegions(t *testing.T) {
	// The region belonging to the default scope doubles as the "all" choice,
	// since a sub-national view has no use for the national bucket.
	all := []model.Service{
		svc("mh-1", scope("state", "reg.mh")),
		svc("ka-1", scope("state", "reg.ka")),
	}

	assertIDs(t, v().Scope(all, "state", "reg.national"), "mh-1", "ka-1")
}

func TestUnknownRegionMatchesNothing(t *testing.T) {
	all := []model.Service{svc("mh-1", scope("state", "reg.mh"))}

	if got := v().Scope(all, "state", "reg.invented"); len(got) != 0 {
		t.Errorf("got %v, want nothing", idsOf(got))
	}
}

// --- ranking ---------------------------------------------------------------

// Rank 1 is the best performing service. It is a leaderboard: the reader is
// being shown who is doing well, and the trouble is marked by status chips
// wherever it appears rather than by being hoisted to the top.
func TestRankOrdersByPerformance(t *testing.T) {
	list := []model.Service{
		// score = downtime + error rate, lowest first
		svc("mid", avail(99.5), errRate(0.5)),        // 0.5 + 0.5 = 1.0
		svc("healthiest", avail(99.9), errRate(0.0)), // 0.1 + 0.0 = 0.1
		svc("worst", avail(99.5), errRate(0.9)),      // 0.5 + 0.9 = 1.4
	}

	assertIDs(t, v().Rank(list), "healthiest", "mid", "worst")
}

// Downtime and error rate are both percentages of the same request population, so
// they add: a service down for 2% of the window and erroring on 1% of what got
// through is failing 3% of the people who came to it. That is why a service with
// perfect availability and heavy errors can rank below one with mild downtime.
func TestErrorsAndDowntimeAreAddedNotRankedInTurn(t *testing.T) {
	list := []model.Service{
		svc("erroring", avail(100.0), errRate(2.0)), // 0.0 + 2.0 = 2.0
		svc("flaky", avail(99.0), errRate(0.1)),     // 1.0 + 0.1 = 1.1
	}

	assertIDs(t, v().Rank(list), "flaky", "erroring")
}

// Two services failing the same proportion of their traffic are separated by the
// half of it a reader can act on today, before falling back to the name.
func TestRankBreaksTiesByErrorRateBeforeName(t *testing.T) {
	list := []model.Service{
		svc("alpha", avail(99.0), errRate(1.0)), // 1.0 + 1.0 = 2.0
		svc("zulu", avail(98.0), errRate(0.0)),  // 2.0 + 0.0 = 2.0
	}

	assertIDs(t, v().Rank(list), "zulu", "alpha")
}

func TestRankIgnoresStatus(t *testing.T) {
	// Status is right now; rank is the window. A service having a bad afternoon
	// does not jump the queue ahead of one that has been failing all month.
	list := []model.Service{
		svc("bad-afternoon", avail(99.99), status(model.StatusMajor)),
		svc("bad-month", avail(99.10), status(model.StatusOperational)),
	}

	assertIDs(t, v().Rank(list), "bad-afternoon", "bad-month")
}

// The load-bearing case. A service with no reading ranks LAST, behind every
// measured failure — not because it is necessarily worse, but because nobody can
// say whether citizens are being served by it, so it cannot be credited with a
// standing it has not demonstrated. Scoring it zero would put it at rank 1,
// which is the mistake model.OptFloat exists to prevent.
func TestUnreportedServicesRankLast(t *testing.T) {
	list := []model.Service{
		svc("silent", noAvail()),
		svc("measured-failure", avail(50.0), errRate(9.0)),
	}

	assertIDs(t, v().Rank(list), "measured-failure", "silent")
	// Order-independent: the rule is the score, not the input order.
	assertIDs(t, v().Rank([]model.Service{
		svc("measured-failure", avail(50.0), errRate(9.0)),
		svc("silent", noAvail()),
	}), "measured-failure", "silent")
}

func TestRankBreaksTiesByName(t *testing.T) {
	list := []model.Service{
		svc("zulu", avail(99.5), errRate(0.5)),
		svc("alpha", avail(99.5), errRate(0.5)),
	}

	assertIDs(t, v().Rank(list), "alpha", "zulu")
}

func TestRankDoesNotMutateItsInput(t *testing.T) {
	list := []model.Service{svc("b", avail(99.1)), svc("a", avail(99.9))}

	v().Rank(list)

	if list[0].ID != "b" {
		t.Error("Rank reordered the caller's slice")
	}
}

func TestRanksArePositions(t *testing.T) {
	got := query.Ranks(v().Rank([]model.Service{
		svc("second", avail(99.5)), // more downtime
		svc("first", avail(99.9)),  // less
	}))

	if got["first"] != 1 || got["second"] != 2 {
		t.Errorf("ranks = %v, want first=1 second=2", got)
	}
}

// --- filtering -------------------------------------------------------------

func TestFilterByStatus(t *testing.T) {
	list := []model.Service{
		svc("ok"),
		svc("bad", status(model.StatusMajor)),
		svc("meh", status(model.StatusPartial)),
	}

	got := v().Apply(list, query.Filter{Statuses: []string{"major", "partial"}})

	assertIDs(t, got, "bad", "meh")
}

func TestFilterByCategory(t *testing.T) {
	list := []model.Service{svc("a"), svc("b", category("cat.money"))}

	assertIDs(t, v().Apply(list, query.Filter{Categories: []string{"cat.money"}}), "b")
}

func TestFilterBySearchIsCaseInsensitiveAndSpansFields(t *testing.T) {
	list := []model.Service{
		svc("aadhaar"),
		svc("passport", category("cat.travel")),
	}

	// Matches the name.
	assertIDs(t, v().Apply(list, query.Filter{Search: "AADH"}), "aadhaar")
	// And the category, which is why the haystack is more than the name.
	assertIDs(t, v().Apply(list, query.Filter{Search: "travel"}), "passport")
	// And the description.
	assertIDs(t, v().Apply(list, query.Filter{Search: "desc.aadhaar"}), "aadhaar")
}

func TestSearchIgnoresSurroundingSpace(t *testing.T) {
	list := []model.Service{svc("aadhaar")}

	assertIDs(t, v().Apply(list, query.Filter{Search: "  aadhaar  "}), "aadhaar")
	// And whitespace alone is not a search at all.
	assertIDs(t, v().Apply(list, query.Filter{Search: "   "}), "aadhaar")
}

func TestFiltersCombine(t *testing.T) {
	list := []model.Service{
		svc("a", status(model.StatusMajor), category("cat.money")),
		svc("b", status(model.StatusMajor), category("cat.identity")),
		svc("c", status(model.StatusOperational), category("cat.money")),
	}

	got := v().Apply(list, query.Filter{
		Statuses:   []string{"major"},
		Categories: []string{"cat.money"},
	})

	assertIDs(t, got, "a")
}

func TestEmptyFilterKeepsEverything(t *testing.T) {
	list := []model.Service{svc("a"), svc("b")}

	assertIDs(t, v().Apply(list, query.Filter{}), "a", "b")
}

func TestFilterActivityAndCount(t *testing.T) {
	tests := []struct {
		name  string
		f     query.Filter
		activ bool
		count int
	}{
		{"empty", query.Filter{}, false, 0},
		{"blank search only", query.Filter{Search: "   "}, false, 0},
		{"one status", query.Filter{Statuses: []string{"major"}}, true, 1},
		{"status and search", query.Filter{Statuses: []string{"major"}, Search: "x"}, true, 2},
		{"all three", query.Filter{Statuses: []string{"major"}, Categories: []string{"c"}, Search: "x"}, true, 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.f.Active(); got != tc.activ {
				t.Errorf("Active() = %v, want %v", got, tc.activ)
			}
			if got := tc.f.Count(); got != tc.count {
				t.Errorf("Count() = %d, want %d", got, tc.count)
			}
		})
	}
}

// --- ordering --------------------------------------------------------------

func ordered(t *testing.T, list []model.Service, key, dir string) []model.Service {
	t.Helper()
	ranks := query.Ranks(v().Rank(list))
	return v().Order(list, key, dir, ranks)
}

func TestOrderByName(t *testing.T) {
	list := []model.Service{svc("zulu"), svc("alpha"), svc("mike")}

	assertIDs(t, ordered(t, list, query.SortName, query.Asc), "alpha", "mike", "zulu")
	assertIDs(t, ordered(t, list, query.SortName, query.Desc), "zulu", "mike", "alpha")
}

func TestOrderByStatusUsesConfiguredSeverity(t *testing.T) {
	// Severity is a deployment's judgement about which news is worse, so the
	// column follows config rather than a fixed list.
	list := []model.Service{
		svc("fine", status(model.StatusOperational)),
		svc("broken", status(model.StatusMajor)),
		svc("degraded", status(model.StatusPartial)),
	}

	assertIDs(t, ordered(t, list, query.SortStatus, query.Desc), "broken", "degraded", "fine")
}

func TestOrderByAnyConfiguredMetric(t *testing.T) {
	// The sortable columns follow whatever the deployment configured, rather
	// than a list fixed in code.
	list := []model.Service{
		svc("slow", func(s *model.Service) { s.Metrics.LatencyP50 = 900 }),
		svc("quick", func(s *model.Service) { s.Metrics.LatencyP50 = 90 }),
	}

	assertIDs(t, ordered(t, list, "metric.latencyP50", query.Asc), "quick", "slow")

	list = []model.Service{
		svc("busy", func(s *model.Service) { s.Metrics.Volume.Total = 900000 }),
		svc("quiet", func(s *model.Service) { s.Metrics.Volume.Total = 900 }),
	}
	assertIDs(t, ordered(t, list, "metric.volume", query.Desc), "busy", "quiet")
}

func TestOrderByErrorRate(t *testing.T) {
	list := []model.Service{svc("noisy", errRate(3.0)), svc("clean", errRate(0.1))}

	assertIDs(t, ordered(t, list, "metric.errorRate", query.Asc), "clean", "noisy")
}

func TestAscendingAvailabilityPutsUnreportedAtTheBottom(t *testing.T) {
	// Sorting an absent reading as zero would put "we cannot tell" at the top
	// of a worst-first list, implying a total outage.
	list := []model.Service{
		svc("silent", noAvail()),
		svc("worst", avail(90.0)),
		svc("best", avail(99.9)),
	}

	assertIDs(t, ordered(t, list, "metric.availability", query.Asc), "silent", "worst", "best")
}

func TestMetricColumnTiesKeepTheirExistingOrder(t *testing.T) {
	// Ties on a measurement are constant — dozens of services sit at the same
	// rounded availability — so the order must not shuffle between requests.
	list := []model.Service{
		svc("a", avail(99.90)),
		svc("b", avail(99.90)),
		svc("c", avail(99.90)),
	}

	for range 20 {
		assertIDs(t, ordered(t, list, "metric.availability", query.Desc), "a", "b", "c")
	}
}

func TestMetricWithAnUnrecognisedFieldSortsAsEqual(t *testing.T) {
	// Config validation rejects an unknown field, but a hot reload races
	// validation. Treating every service as equal leaves the existing order
	// intact, which is a far better failure than an arbitrary reshuffle.
	d := domain()
	d.Metrics = append(d.Metrics, config.Metric{ID: "metric.invented", Field: "somethingElse"})
	w := query.View{Domain: d, Labeler: l, Days: 30}

	list := []model.Service{svc("a", avail(99.1)), svc("b", avail(99.9))}
	ranks := query.Ranks(w.Rank(list))

	assertIDs(t, w.Order(list, "metric.invented", query.Asc, ranks), "a", "b")
}

func TestUnknownSortKeyFallsBackToRank(t *testing.T) {
	// "first" is the best performer, since rank 1 is the top of the board.
	list := []model.Service{svc("second", avail(99.1)), svc("first", avail(99.9))}

	assertIDs(t, ordered(t, list, "invented", query.Asc), "first", "second")
	assertIDs(t, ordered(t, list, query.SortRank, query.Asc), "first", "second")
}

func TestOrderIsStableOnTies(t *testing.T) {
	// Services tying on the chosen column keep the order they already had,
	// rather than shuffling between otherwise identical requests.
	list := []model.Service{
		svc("a", status(model.StatusOperational)),
		svc("b", status(model.StatusOperational)),
		svc("c", status(model.StatusOperational)),
	}

	for range 20 {
		assertIDs(t, ordered(t, list, query.SortStatus, query.Asc), "a", "b", "c")
	}
}

func TestOrderDoesNotMutateItsInput(t *testing.T) {
	list := []model.Service{svc("b"), svc("a")}

	ordered(t, list, query.SortName, query.Asc)

	if list[0].ID != "b" {
		t.Error("Order reordered the caller's slice")
	}
}

func TestDefaultDirectionPerColumn(t *testing.T) {
	// Rank and name read naturally forwards; a measurement is nearly always
	// being examined for its extreme.
	for _, tc := range []struct{ key, want string }{
		{query.SortRank, query.Asc},
		{query.SortName, query.Asc},
		{query.SortStatus, query.Desc},
		{"metric.availability", query.Desc},
	} {
		if got := query.DefaultDirection(tc.key); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.key, got, tc.want)
		}
	}
}

// --- counts ----------------------------------------------------------------

func TestCountsIncludeStatusesWithNone(t *testing.T) {
	// The legend shows a complete picture rather than only what happens to be
	// present today, so a status with no services still reports zero.
	list := []model.Service{
		svc("a"),
		svc("b", status(model.StatusMajor)),
		svc("c", status(model.StatusMajor)),
	}

	got := v().Counts(list)

	if got["operational"] != 1 || got["major"] != 2 {
		t.Errorf("counts = %v", got)
	}
	for _, s := range config.Statuses {
		if _, ok := got[s]; !ok {
			t.Errorf("counts omit %q entirely", s)
		}
	}
	if got["maintenance"] != 0 {
		t.Errorf("maintenance = %d, want an explicit zero", got["maintenance"])
	}
}

// --- ranks are computed over scope, not over the filtered view ---------------

func TestRankIsIndependentOfFiltering(t *testing.T) {
	// "Rank 4" has to mean the same thing however the reader has narrowed the
	// view. A rank that shuffled as you typed would be useless.
	scoped := []model.Service{
		svc("first", avail(99.90), status(model.StatusPartial)),
		svc("second", avail(99.50), status(model.StatusPartial)),
		svc("third", avail(99.10)),
	}
	ranks := query.Ranks(v().Rank(scoped))

	filtered := v().Apply(scoped, query.Filter{Statuses: []string{"partial"}})
	got := v().Order(filtered, query.SortRank, query.Asc, ranks)

	assertIDs(t, got, "first", "second")
	if ranks["first"] != 1 || ranks["second"] != 2 {
		t.Errorf("ranks changed under filtering: %v", ranks)
	}
}

// --- the window ------------------------------------------------------------

// history attaches daily availability readings, oldest first.
func history(vals ...float64) func(*model.Service) {
	return func(s *model.Service) {
		day := time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC)
		s.History = nil
		for i, av := range vals {
			s.History = append(s.History, model.HistoryPoint{
				Day:          day.AddDate(0, 0, i),
				Availability: model.Float(av),
				ErrorRate:    (100 - av) / 10,
				Volume:       100,
				LatencyP50:   int32(av),
			})
		}
	}
}

func TestChangingThePeriodReordersTheBoard(t *testing.T) {
	// This is the whole point of a period selector on a leaderboard. A service
	// that was poor last month and excellent this week should lead over seven
	// days and trail over fourteen; otherwise the control is decorative.
	recovering := svc("recovering", history(
		97.0, 97.0, 97.0, 97.0, 97.0, 97.0, 97.0, // the older week
		99.99, 99.99, 99.99, 99.99, 99.99, 99.99, 99.99, // the recent week
	))
	declining := svc("declining", history(
		99.99, 99.99, 99.99, 99.99, 99.99, 99.99, 99.99,
		99.20, 99.20, 99.20, 99.20, 99.20, 99.20, 99.20,
	))

	list := []model.Service{declining, recovering}

	assertIDs(t, view(7).Rank(list), "recovering", "declining")
	assertIDs(t, view(14).Rank(list), "declining", "recovering")
}

func TestStandingAveragesRatesAndSumsTraffic(t *testing.T) {
	// Each is what the quantity means over a period: "99.4% available across the
	// month" and "2.9 million requests during it".
	s := view(4).Standing(svc("x", history(98, 99, 100, 99)))

	if !s.Availability.Valid || s.Availability.Value != 99 {
		t.Errorf("availability = %+v, want a mean of 99", s.Availability)
	}
	if s.Volume != 400 {
		t.Errorf("volume = %d, want the four days summed", s.Volume)
	}
	if s.Samples != 4 {
		t.Errorf("samples = %d, want 4", s.Samples)
	}
	if s.Current {
		t.Error("marked as a current reading despite having history")
	}
}

func TestStandingUsesOnlyTheWindow(t *testing.T) {
	sv := svc("x", history(90, 90, 90, 100, 100))

	if got := view(2).Standing(sv).Availability.Value; got != 100 {
		t.Errorf("two-day standing = %v, want 100", got)
	}
	if got := view(5).Standing(sv).Availability.Value; got != 94 {
		t.Errorf("five-day standing = %v, want 94", got)
	}
	// A window longer than the history uses everything there is.
	if got := view(90).Standing(sv).Availability.Value; got != 94 {
		t.Errorf("over-long window = %v, want 94", got)
	}
}

func TestStandingSkipsUnreportedDays(t *testing.T) {
	// A gap in reporting is not an outage. Averaging it in as zero would
	// quietly punish a service for its collector having been offline.
	sv := svc("x", history(100, 100, 100))
	sv.History[1].Availability = model.NoFloat()

	s := view(3).Standing(sv)

	if !s.Availability.Valid || s.Availability.Value != 100 {
		t.Errorf("availability = %+v, want 100 from the two reported days", s.Availability)
	}
}

func TestStandingWithNoReadingsAtAllIsAbsent(t *testing.T) {
	sv := svc("x", history(100, 100))
	for i := range sv.History {
		sv.History[i].Availability = model.NoFloat()
	}

	if got := view(2).Standing(sv).Availability; got.Valid {
		t.Errorf("availability = %+v, want absent", got)
	}
}

func TestStandingFallsBackToTheLatestReading(t *testing.T) {
	// A deployment pushing bare snapshots supplies no history. It should get the
	// current figures rather than nothing, and the interface should be able to
	// tell the reader that is what it is looking at.
	sv := svc("x", avail(99.42), errRate(0.7))
	sv.History = nil

	s := view(30).Standing(sv)

	if !s.Availability.Valid || s.Availability.Value != 99.42 {
		t.Errorf("availability = %+v, want the latest reading", s.Availability)
	}
	if s.ErrorRate != 0.7 {
		t.Errorf("errorRate = %v, want the latest reading", s.ErrorRate)
	}
	if !s.Current {
		t.Error("not marked as a current reading, so the interface would imply a window it does not have")
	}
	if s.Samples != 0 {
		t.Errorf("samples = %d, want 0", s.Samples)
	}
}

func TestAZeroWindowUsesTheWholeHistory(t *testing.T) {
	sv := svc("x", history(90, 100))

	if got := view(0).Standing(sv).Availability.Value; got != 95 {
		t.Errorf("got %v, want the whole history averaged", got)
	}
}

func TestMetricColumnsSortOnTheWindow(t *testing.T) {
	// The order must agree with the figures displayed beside it, or the reader
	// concludes the sort is broken.
	recovering := svc("recovering", history(97, 97, 100, 100))
	steady := svc("steady", history(99, 99, 99, 99))
	list := []model.Service{steady, recovering}

	ranks := query.Ranks(view(2).Rank(list))
	assertIDs(t, view(2).Order(list, "metric.availability", query.Desc, ranks), "recovering", "steady")

	ranks = query.Ranks(view(4).Rank(list))
	assertIDs(t, view(4).Order(list, "metric.availability", query.Desc, ranks), "steady", "recovering")
}

func TestStatusIsNotWindowed(t *testing.T) {
	// Status answers "is this working at the moment". Averaging it would produce
	// a verdict bar that answers a question nobody asked.
	list := []model.Service{
		svc("a", status(model.StatusMajor), history(100, 100, 100, 100)),
		svc("b", status(model.StatusOperational), history(90, 90, 90, 90)),
	}

	for _, days := range []int{1, 7, 30, 90} {
		got := view(days).Counts(list)
		if got["major"] != 1 || got["operational"] != 1 {
			t.Errorf("%d-day window changed the status counts: %v", days, got)
		}
	}
}

func TestDaysForResolvesConfiguredPeriods(t *testing.T) {
	d := domain()
	d.Periods = []config.Period{
		{ID: "24h", Days: 1},
		{ID: "7d", Days: 7},
		{ID: "30d", Days: 30},
		{ID: "90d", Days: 89},
	}
	d.DefaultPeriod = "30d"

	for _, tc := range []struct {
		id   string
		want int
	}{
		{"24h", 1},
		{"7d", 7},
		{"30d", 30},
		{"90d", 89},
		// A stale bookmark or a hand-edited URL should still produce a sensible
		// page rather than a zero-day window.
		{"invented", 30},
		{"", 30},
	} {
		if got := query.DaysFor(d, tc.id); got != tc.want {
			t.Errorf("DaysFor(%q) = %d, want %d", tc.id, got, tc.want)
		}
	}
}

func TestDaysForWithNoPeriodsConfigured(t *testing.T) {
	if got := query.DaysFor(config.Domain{}, "30d"); got != 0 {
		t.Errorf("got %d, want 0 — meaning the whole history", got)
	}
}

// The published ranking rule, stated as arithmetic. If this changes, the prose in
// the interface that claims to describe it has to change with it.
func TestScoreIsDowntimePlusErrorRate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		avail float64
		errs  float64
		want  float64
	}{
		{"perfect", 100, 0, 0},
		{"half a per cent down", 99.5, 0, 0.5},
		{"errors only", 100, 1.25, 1.25},
		{"both", 99, 2, 3},
		{"total outage", 0, 0, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := v().Standing(svc("x", avail(tc.avail), errRate(tc.errs)))
			if got := st.Score(); math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("Score() = %v, want %v", got, tc.want)
			}
		})
	}
}

// Absence is not zero. This is the invariant model.OptFloat exists for, asserted
// at the point where getting it wrong would sort an unmonitored service in among
// the healthy ones.
func TestScoreOfAnUnreadServiceIsInfinite(t *testing.T) {
	st := v().Standing(svc("silent", noAvail()))
	if got := st.Score(); !math.IsInf(got, 1) {
		t.Errorf("Score() = %v, want +Inf; a service that cannot be verified cannot claim a standing", got)
	}
	// And specifically not zero, which would rank it as perfect.
	if st.Score() == 0 {
		t.Error("an unread service scored zero, which ranks it as the best performer")
	}
}
