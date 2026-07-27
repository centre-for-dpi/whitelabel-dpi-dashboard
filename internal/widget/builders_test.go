package widget_test

import (
	"strings"
	"testing"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/query"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/widget"
)

// --- registry --------------------------------------------------------------

func TestEveryRegisteredWidgetIsComplete(t *testing.T) {
	r := widget.Default()
	for _, name := range r.Types() {
		d, ok := r.Lookup(name)
		if !ok {
			t.Fatalf("Types() listed %q but Lookup could not find it", name)
		}
		if d.Template == "" {
			t.Errorf("%s has no template", name)
		}
		if d.Build == nil {
			t.Errorf("%s has no builder", name)
		}
		// The layout reference is generated from these; a widget with no
		// description is one a deployment cannot discover.
		if d.Doc == "" {
			t.Errorf("%s has no documentation", name)
		}
		for opt, f := range d.Schema {
			if f.Doc == "" {
				t.Errorf("%s option %q has no documentation", name, opt)
			}
		}
	}
}

func TestBuildingAnUnknownWidgetFails(t *testing.T) {
	_, err := widget.Default().Build("invented", ctx(nil), nil)
	if err == nil {
		t.Fatal("an unknown widget type was built")
	}
	if !strings.Contains(err.Error(), "invented") {
		t.Errorf("error does not name the type: %v", err)
	}
}

func TestRegisteringAnIncompleteDefinitionPanics(t *testing.T) {
	// A programming error in this repository, not something a deployment can
	// cause, so it fails loudly and immediately.
	for _, tc := range []struct {
		name string
		def  widget.Definition
	}{
		{"no type", widget.Definition{Template: "t", Build: func(widget.Context, widget.Options) (any, error) { return nil, nil }}},
		{"no template", widget.Definition{Type: "x", Build: func(widget.Context, widget.Options) (any, error) { return nil, nil }}},
		{"no builder", widget.Definition{Type: "x", Template: "t"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("an incomplete definition was accepted")
				}
			}()
			widget.NewRegistry().Register(tc.def)
		})
	}
}

func TestRegisterReplacesAnEarlierDefinition(t *testing.T) {
	r := widget.NewRegistry()
	build := func(widget.Context, widget.Options) (any, error) { return "second", nil }
	r.Register(widget.Definition{Type: "x", Template: "a", Build: func(widget.Context, widget.Options) (any, error) { return "first", nil }})
	r.Register(widget.Definition{Type: "x", Template: "b", Build: build})

	got, err := r.Build("x", ctx(nil), nil)
	if err != nil || got != "second" {
		t.Errorf("got %v, %v; want the later definition", got, err)
	}
	if len(r.Types()) != 1 {
		t.Errorf("types = %v, want one", r.Types())
	}
}

// --- heading ---------------------------------------------------------------

func TestHeading(t *testing.T) {
	v := build[widget.HeadingView](t, "heading", ctx(nil), widget.Options{
		"termId": "verdict.h1", "eyebrow": "lb.eyebrow", "level": 1, "class": "serif", "id": "h",
	})

	if v.Title != "verdict.h1" || v.Eyebrow != "lb.eyebrow" {
		t.Errorf("got %+v", v)
	}
	if v.Level != 1 || v.Class != "serif" || v.ID != "h" {
		t.Errorf("got %+v", v)
	}
}

func TestHeadingVariesByScope(t *testing.T) {
	// One heading, read differently in the national and sub-national views.
	c := ctx(nil)
	c.State.Scope = "state"

	v := build[widget.HeadingView](t, "heading", c, widget.Options{
		"termId": "verdict.h1", "scoped": true,
	})

	if v.Title != "verdict.h1.state" {
		t.Errorf("title = %q, want the scope appended", v.Title)
	}
}

func TestHeadingDefaultsToLevelTwo(t *testing.T) {
	v := build[widget.HeadingView](t, "heading", ctx(nil), widget.Options{"termId": "x"})
	if v.Level != 2 {
		t.Errorf("level = %d, want 2", v.Level)
	}
}

func TestHeadingClampsAnImpossibleLevel(t *testing.T) {
	// Validation rejects this at startup; the clamp is the hot-reload safety
	// net, because invalid HTML is worse than a wrong level.
	v := build[widget.HeadingView](t, "heading", ctx(nil), widget.Options{"termId": "x", "level": 9})
	if v.Level != 2 {
		t.Errorf("level = %d, want it clamped to 2", v.Level)
	}
}

// --- status summary --------------------------------------------------------

func TestStatusSummaryTalliesEachState(t *testing.T) {
	v := build[widget.StatusSummaryView](t, "status-summary", ctx([]model.Service{
		svc("a"), svc("b"),
		svc("c", status(model.StatusMajor)),
	}), widget.Options{})

	if !strings.Contains(v.Text, "2 status.operational") {
		t.Errorf("summary = %q", v.Text)
	}
	if !strings.Contains(v.Text, "1 status.major") {
		t.Errorf("summary = %q", v.Text)
	}
	if strings.Contains(v.Text, "status.unknown") {
		t.Errorf("summary names a status with no services: %q", v.Text)
	}
}

func TestStatusSummarySaysAllClearPlainly(t *testing.T) {
	// Worth saying outright rather than making the reader infer it from a row
	// of numbers.
	v := build[widget.StatusSummaryView](t, "status-summary",
		ctx([]model.Service{svc("a"), svc("b")}),
		widget.Options{"allWellTermId": "verdict.allClear"})

	if !v.AllWell {
		t.Error("not marked all-well despite every service being operational")
	}
	if v.Text != "verdict.allClear" {
		t.Errorf("text = %q", v.Text)
	}
}

func TestStatusSummaryFallsBackToTheTallyWithNoAllClearTerm(t *testing.T) {
	v := build[widget.StatusSummaryView](t, "status-summary",
		ctx([]model.Service{svc("a")}), widget.Options{})

	if v.AllWell {
		t.Error("claimed all-well with no term configured to say it")
	}
	if !strings.Contains(v.Text, "status.operational") {
		t.Errorf("text = %q", v.Text)
	}
}

func TestStatusSummaryOfNothing(t *testing.T) {
	v := build[widget.StatusSummaryView](t, "status-summary", ctx(nil),
		widget.Options{"allWellTermId": "verdict.allClear"})

	// No services is not the same as no problems.
	if v.AllWell {
		t.Error("an empty dashboard claimed all-well")
	}
}

// --- segmented bar ---------------------------------------------------------

func TestSegmentedBarProportions(t *testing.T) {
	v := build[widget.SegmentedBarView](t, "segmented-bar", ctx([]model.Service{
		svc("a"), svc("b"), svc("c"), svc("d", status(model.StatusMajor)),
	}), widget.Options{"hideBelow": "480px", "labelTermId": "verdict.barLabel"})

	if v.Total != 4 {
		t.Errorf("total = %d, want 4", v.Total)
	}
	if len(v.Segments) != 2 {
		t.Fatalf("segments = %d, want only the two states present", len(v.Segments))
	}
	if v.Segments[0].Percent != 75 {
		t.Errorf("first segment = %v%%, want 75", v.Segments[0].Percent)
	}
	// Templates reference --status-ok, never a colour.
	if v.Segments[0].Tone != "ok" {
		t.Errorf("operational tone = %q, want ok", v.Segments[0].Tone)
	}
	if v.HideBelow != "480px" || v.Label != "verdict.barLabel" {
		t.Errorf("got %+v", v)
	}
}

func TestSegmentedBarOmitsEmptyStates(t *testing.T) {
	// A band of no width is not informative; the legend covers the zeroes.
	v := build[widget.SegmentedBarView](t, "segmented-bar",
		ctx([]model.Service{svc("a")}), widget.Options{})

	for _, s := range v.Segments {
		if s.Percent == 0 {
			t.Errorf("a zero-width segment was emitted: %+v", s)
		}
	}
}

func TestSegmentedBarOfNothing(t *testing.T) {
	v := build[widget.SegmentedBarView](t, "segmented-bar", ctx(nil), widget.Options{})

	if len(v.Segments) != 0 || v.Total != 0 {
		t.Errorf("got %+v, want an empty bar", v)
	}
}

// --- legend ----------------------------------------------------------------

func TestLegendKeepsStatusesWithNone(t *testing.T) {
	// The legend shows the complete picture rather than only what is present
	// today, so a reader learns the vocabulary even on a quiet day.
	v := build[widget.LegendView](t, "legend", ctx([]model.Service{svc("a")}), widget.Options{})

	if len(v.Items) != len(config.Statuses) {
		t.Fatalf("items = %d, want one per status", len(v.Items))
	}
	var sawZero bool
	for _, i := range v.Items {
		if i.Count == "0" {
			sawZero = true
		}
		if i.Icon.Glyph == "" {
			t.Errorf("%s has no icon", i.Status)
		}
	}
	if !sawZero {
		t.Error("no status reported zero, so the legend is only showing what is present")
	}
}

// --- coverage --------------------------------------------------------------

func TestCoverageStatesWhatIsNotWatched(t *testing.T) {
	// A dashboard watching a fifth of the estate and not saying so overstates
	// what it knows.
	v := build[widget.CoverageView](t, "coverage",
		ctx([]model.Service{svc("a"), svc("b")}),
		widget.Options{"termId": "verdict.coverage"})

	if !strings.Contains(v.Text, "onboarded=2") {
		t.Errorf("text = %q, want the onboarded count", v.Text)
	}
	if !strings.Contains(v.Text, "total=812") {
		t.Errorf("text = %q, want the addressable total", v.Text)
	}
}

// --- timestamp -------------------------------------------------------------

func TestTimestampCarriesBothFormats(t *testing.T) {
	v := build[widget.TimestampView](t, "timestamp", ctx(nil),
		widget.Options{"termId": "verdict.updated"})

	if v.ISO != now.Add(-4*time.Minute).Format(time.RFC3339) {
		t.Errorf("ISO = %q", v.ISO)
	}
	if !strings.Contains(v.Relative, "ago") {
		t.Errorf("relative = %q", v.Relative)
	}
	if v.Absolute == "" {
		t.Error("no absolute time, so hovering the relative one shows nothing")
	}
}

func TestStaleDataIsCalledOut(t *testing.T) {
	// Data quietly going stale is the failure a status dashboard can least
	// afford, so it is announced rather than left to be noticed.
	c := ctx(nil)
	c.Snapshot.GeneratedAt = now.Add(-2 * time.Hour)

	v := build[widget.TimestampView](t, "timestamp", c, widget.Options{"staleAfter": 900})
	if !v.Stale {
		t.Error("two-hour-old data was not marked stale")
	}

	c.Snapshot.GeneratedAt = now.Add(-1 * time.Minute)
	if v := build[widget.TimestampView](t, "timestamp", c, widget.Options{"staleAfter": 900}); v.Stale {
		t.Error("one-minute-old data was marked stale")
	}
	// With no threshold configured, nothing is ever stale.
	c.Snapshot.GeneratedAt = now.Add(-10 * time.Hour)
	if v := build[widget.TimestampView](t, "timestamp", c, widget.Options{}); v.Stale {
		t.Error("staleness was asserted with no threshold configured")
	}
}

// --- disclosure ------------------------------------------------------------

func TestDisclosurePrintsTheRulesInEvaluationOrder(t *testing.T) {
	// The reader is being told how the verdict was reached, so the order they
	// are applied in is the order to show them.
	v := build[widget.DisclosureView](t, "disclosure", ctx(nil),
		widget.Options{"summaryTermId": "legend.title"})

	if v.Summary != "legend.title" {
		t.Errorf("summary = %q", v.Summary)
	}
	if len(v.Items) != 5 {
		t.Fatalf("items = %d, want one per rule", len(v.Items))
	}
	if !strings.HasPrefix(v.Items[0], "rule.maintenance") {
		t.Errorf("first rule = %q, want maintenance, which is evaluated first", v.Items[0])
	}
	// The live threshold numbers, so the published rule cannot drift from the
	// applied one.
	if !strings.Contains(v.Items[0], "partialAvail=99.5") {
		t.Errorf("rule text carries no threshold values: %q", v.Items[0])
	}
}

func TestDisclosureSkipsRulesWithNoProse(t *testing.T) {
	c := ctx(nil)
	delete(c.Config.Domain.Thresholds.Prose, "unknown")

	v := build[widget.DisclosureView](t, "disclosure", c,
		widget.Options{"summaryTermId": "legend.title"})

	if len(v.Items) != 4 {
		t.Errorf("items = %d, want the undocumented rule omitted", len(v.Items))
	}
}

// --- call to action --------------------------------------------------------

func TestCTAButton(t *testing.T) {
	v := build[widget.CTAView](t, "cta-button", ctx(nil), widget.Options{
		"termId": "verdict.findService", "target": "#leaderboard", "iconKey": "ui.arrow",
	})

	if v.Label != "verdict.findService" || v.Target != "#leaderboard" {
		t.Errorf("got %+v", v)
	}
	if v.Icon.Glyph != "[ui.arrow]" {
		t.Errorf("icon = %+v", v.Icon)
	}
}

// --- signal cards ----------------------------------------------------------

func signalDomain(c *widget.Context) {
	c.Config.Domain.Signals = []config.Signal{
		{ID: "sig.maintenance", Kind: config.SignalMaintenanceActive,
			TitleTermID: "sig.maintenance.title", RuleTermID: "sig.maintenance.rule",
			IconKey: "status.maintenance", Tone: "maintenance",
			Filter: &config.SignalFilter{Status: []string{"maintenance"}}},
		{ID: "sig.longestIncident", Kind: config.SignalLongestOpenIncident,
			TitleTermID: "sig.longestIncident.title", RuleTermID: "sig.longestIncident.rule",
			IconKey: "status.major", Tone: "major"},
	}
}

func TestSignalCardsCarryTheirRule(t *testing.T) {
	// A card stating a finding without its basis is an assertion the reader
	// cannot check.
	c := ctx([]model.Service{svc("a", status(model.StatusMaintenance))}, signalDomain)

	v := build[widget.SignalCardsView](t, "signal-cards", c,
		widget.Options{"actionTermId": "sig.viewAffected"})

	if len(v.Cards) != 1 {
		t.Fatalf("cards = %d, want one", len(v.Cards))
	}
	card := v.Cards[0]
	if card.Rule == "" {
		t.Error("the card states a finding with no rule")
	}
	if !strings.Contains(card.Title, "count=1") {
		t.Errorf("title = %q, want the count it found", card.Title)
	}
	if card.Tone != "maintenance" || card.Icon.Glyph != "[status.maintenance]" {
		t.Errorf("presentation = %+v", card)
	}
	if card.ActionLabel != "sig.viewAffected" {
		t.Errorf("action = %q", card.ActionLabel)
	}
	if len(card.FilterStatuses) != 1 || card.FilterStatuses[0] != "maintenance" {
		t.Errorf("filter = %v", card.FilterStatuses)
	}
}

func TestSignalCardCanFocusOneService(t *testing.T) {
	// A finding about a single service opens that service rather than narrowing
	// the whole board.
	inc := svc("a")
	inc.Incidents = []model.Incident{{ID: "i", Open: true, OpenedAt: now.Add(-3 * time.Hour)}}
	c := ctx([]model.Service{inc}, signalDomain)

	v := build[widget.SignalCardsView](t, "signal-cards", c, widget.Options{})

	var found bool
	for _, card := range v.Cards {
		if card.ServiceID == "a" {
			found = true
			if card.ServiceTab != "incidents" {
				t.Errorf("tab = %q, want the tab showing the evidence", card.ServiceTab)
			}
			if len(card.FilterStatuses) != 0 {
				t.Error("a single-service card also carries a board filter")
			}
		}
	}
	if !found {
		t.Errorf("no card focused the service with the open incident: %+v", v.Cards)
	}
}

func TestEmptySignalCardHasNoAction(t *testing.T) {
	v := build[widget.SignalCardsView](t, "signal-cards",
		ctx([]model.Service{svc("a")}, signalDomain),
		widget.Options{"actionTermId": "sig.viewAffected"})

	if len(v.Cards) != 1 || !v.Cards[0].Empty {
		t.Fatalf("cards = %+v, want the single empty card", v.Cards)
	}
	// "Nothing to report" has nothing to view.
	if v.Cards[0].ActionLabel != "" {
		t.Errorf("the empty card offers an action: %q", v.Cards[0].ActionLabel)
	}
	if v.Cards[0].Tone != "ok" || v.Cards[0].Icon.Glyph != "[ui.check]" {
		t.Errorf("empty card presentation = %+v", v.Cards[0])
	}
}

// --- filter bar ------------------------------------------------------------

func TestFilterBarOffersEveryChoice(t *testing.T) {
	c := ctx([]model.Service{svc("a"), svc("b", status(model.StatusMajor))})
	c.State.Statuses = []string{"major"}
	c.State.Search = "aadh"

	v := build[widget.FilterBarView](t, "filter-bar", c, widget.Options{
		"searchTermId": "flt.search", "resultTermId": "flt.results",
	})

	if len(v.Statuses) != len(config.Statuses) {
		t.Errorf("status chips = %d, want one per status", len(v.Statuses))
	}
	if len(v.Categories) != 2 {
		t.Errorf("category chips = %d, want one per category", len(v.Categories))
	}
	if v.Search != "aadh" || v.SearchLabel != "flt.search" {
		t.Errorf("search = %+v", v)
	}

	var majorChip widget.Chip
	for _, ch := range v.Statuses {
		if ch.Value == "major" {
			majorChip = ch
		}
	}
	if !majorChip.Active {
		t.Error("the applied status chip is not marked active")
	}
	if majorChip.Count != "1" {
		t.Errorf("major count = %q, want 1", majorChip.Count)
	}
	if v.AppliedCount != 2 || !v.ClearVisible {
		t.Errorf("applied = %d, clear = %v; want both filters counted", v.AppliedCount, v.ClearVisible)
	}
}

func TestRegionSelectorOnlyMeansSomethingBelowTheDefaultScope(t *testing.T) {
	// Offering it in the national view invites a choice that does nothing.
	c := ctx(nil)
	if v := build[widget.FilterBarView](t, "filter-bar", c, widget.Options{}); v.RegionEnabled {
		t.Error("the region selector is offered in the national view")
	}

	c.State.Scope = "state"
	if v := build[widget.FilterBarView](t, "filter-bar", c, widget.Options{}); !v.RegionEnabled {
		t.Error("the region selector is not offered in the sub-national view")
	}
}

func TestRegionSelectorCanBeTurnedOff(t *testing.T) {
	c := ctx(nil)
	c.State.Scope = "state"

	v := build[widget.FilterBarView](t, "filter-bar", c, widget.Options{"showRegions": false})

	if v.RegionEnabled || len(v.Regions) != 0 {
		t.Errorf("regions = %+v, want none", v.Regions)
	}
}

func TestUnfilteredBarOffersNothingToClear(t *testing.T) {
	v := build[widget.FilterBarView](t, "filter-bar", ctx([]model.Service{svc("a")}), widget.Options{})

	if v.ClearVisible || v.AppliedCount != 0 {
		t.Errorf("got applied=%d clear=%v, want neither", v.AppliedCount, v.ClearVisible)
	}
}

// --- leaderboard -----------------------------------------------------------

const cols = "columns"

// metricCells returns only the measurement cells of a row, which is what most
// assertions are about. Rank, name and status cells are interleaved among them
// in whatever order the columns declared.
func metricCells(r widget.Row) []widget.Cell {
	var out []widget.Cell
	for _, c := range r.Cells {
		if c.Kind == widget.CellMetric {
			out = append(out, c.Cell)
		}
	}
	return out
}

// cellKind returns the first cell of a given kind.
func cellKind(r widget.Row, kind widget.CellKind) widget.RowCell {
	for _, c := range r.Cells {
		if c.Kind == kind {
			return c
		}
	}
	return widget.RowCell{}
}

// cellOf returns the nth measurement cell.
func cellOf(r widget.Row, n int) widget.Cell {
	cells := metricCells(r)
	if n >= len(cells) {
		return widget.Cell{}
	}
	return cells[n]
}

func leaderboard(t *testing.T, c widget.Context, columns ...string) widget.LeaderboardView {
	t.Helper()
	list := make([]any, len(columns))
	for i, s := range columns {
		list[i] = s
	}
	return build[widget.LeaderboardView](t, "leaderboard-table", c, widget.Options{
		cols:            list,
		"captionTermId": "lb.caption",
		"showingTermId": "lb.showing",
		"emptyTermId":   "flt.empty",
	})
}

func TestLeaderboardColumnsFollowConfiguration(t *testing.T) {
	// Naming a metric in the layout gains a column; the set is not fixed in Go.
	v := leaderboard(t, ctx([]model.Service{svc("a")}),
		"rank", "name", "status", "metric.availability", "metric.latencyP50")

	if len(v.Columns) != 5 {
		t.Fatalf("columns = %d", len(v.Columns))
	}
	if v.Columns[3].Label != "metric.availability" {
		t.Errorf("metric column label = %q", v.Columns[3].Label)
	}
	// Measurements read right-aligned so their digits line up.
	if v.Columns[3].Align != "end" || v.Columns[1].Align != "start" {
		t.Errorf("alignment = %q / %q", v.Columns[3].Align, v.Columns[1].Align)
	}
}

func TestLeaderboardMarksTheSortedColumn(t *testing.T) {
	c := ctx([]model.Service{svc("a")})
	c.State.Sort = "metric.availability"
	c.State.Dir = query.Desc

	v := leaderboard(t, c, "rank", "metric.availability")

	if !v.Columns[1].Sorted || v.Columns[1].Dir != query.Desc {
		t.Errorf("sorted column = %+v", v.Columns[1])
	}
	// Clicking again reverses it.
	if v.Columns[1].NextDir != query.Asc {
		t.Errorf("next direction = %q, want asc", v.Columns[1].NextDir)
	}
	// An unsorted measurement opens at its worst end.
	if v.Columns[0].Sorted {
		t.Error("rank is marked sorted")
	}
	if v.Columns[0].NextDir != query.Asc {
		t.Errorf("rank next direction = %q, want asc", v.Columns[0].NextDir)
	}
}

func TestLeaderboardRows(t *testing.T) {
	v := leaderboard(t, ctx([]model.Service{
		svc("aadhaar", avail(99.99)),
		svc("pan", avail(99.10), status(model.StatusPartial)),
	}), "rank", "name", "status", "metric.availability")

	if len(v.Rows) != 2 {
		t.Fatalf("rows = %d", len(v.Rows))
	}
	first := v.Rows[0]
	if first.ID != "aadhaar" || first.Rank != "1" {
		t.Errorf("first row = %+v", first)
	}
	name := cellKind(first, widget.CellName)
	if first.Name != "svc.aadhaar.name" || name.Description != "svc.aadhaar.desc" {
		t.Errorf("first row names = %+v / %+v", first, name)
	}
	if name.Href != "/service/aadhaar" {
		t.Errorf("name href = %q; the name must stay a real focusable link", name.Href)
	}
	if first.StatusTone != "ok" || first.StatusIcon.Glyph != "[status.operational]" {
		t.Errorf("status presentation = %+v", first)
	}
	if name.CategoryIcon.Glyph != "[cat.identity]" {
		t.Errorf("category icon = %+v", name.CategoryIcon)
	}
	if got := len(metricCells(first)); got != 1 {
		t.Fatalf("cells = %d, want one per metric column", got)
	}
	if cellOf(first, 0).Value != "99.99%" {
		t.Errorf("availability = %q", cellOf(first, 0).Value)
	}
	if !strings.Contains(cellOf(first, 0).Target, "99.50") {
		t.Errorf("target = %q, want the configured 99.5", cellOf(first, 0).Target)
	}
}

func TestLeaderboardShowsTheWindowItsFiguresCover(t *testing.T) {
	// The reader has to know the numbers beside a rank are the numbers the rank
	// was computed from.
	c := ctx([]model.Service{svc("a")})
	c.State.Period = "7d"

	v := leaderboard(t, c, "rank", "metric.availability")

	if v.PeriodLabel != "period.7d" {
		t.Errorf("period label = %q, want the selected window named", v.PeriodLabel)
	}
}

func TestLeaderboardFiguresFollowTheWindow(t *testing.T) {
	// A service that was poor a fortnight ago and excellent this week reads
	// differently at 7 days and 30.
	sv := svc("recovering")
	for i := range sv.History {
		if i < len(sv.History)-7 {
			sv.History[i].Availability = model.Float(97.0)
		} else {
			sv.History[i].Availability = model.Float(99.99)
		}
	}

	c := ctx([]model.Service{sv})
	c.View.Days = 7
	short := leaderboard(t, c, "rank", "metric.availability")

	c.View.Days = 30
	long := leaderboard(t, c, "rank", "metric.availability")

	if cellOf(short.Rows[0], 0).Value == cellOf(long.Rows[0], 0).Value {
		t.Errorf("the figure did not change with the window: %q", cellOf(short.Rows[0], 0).Value)
	}
}

func TestUnreportedAvailabilityReadsAsUnknownNotZero(t *testing.T) {
	// "Not reported" and "zero" are different claims, and only one is true.
	v := leaderboard(t, ctx([]model.Service{svc("silent", noAvail())}),
		"rank", "metric.availability")

	cell := cellOf(v.Rows[0], 0)
	if !cell.Unknown {
		t.Error("an unreported service rendered a number")
	}
	if cell.Value != "" {
		t.Errorf("value = %q, want nothing", cell.Value)
	}
	if cell.Note == "" {
		t.Error("no note explaining why there is no figure")
	}
}

func TestTrendToneFollowsTheMetricNotTheArrow(t *testing.T) {
	// Whether a rise is good news depends on the metric. A climbing error rate
	// and a climbing availability both point up and mean opposite things.
	rising := svc("rising")
	for i := range rising.History {
		rising.History[i].ErrorRate = 0.1 + float64(i)*0.05
		rising.History[i].Availability = model.Float(99.0 + float64(i)*0.02)
	}
	rising.Metrics.ErrorRate = rising.History[len(rising.History)-1].ErrorRate

	v := leaderboard(t, ctx([]model.Service{rising}),
		"rank", "metric.availability", "metric.errorRate")

	availCell, errCell := cellOf(v.Rows[0], 0), cellOf(v.Rows[0], 1)
	if availCell.TrendTone != "ok" {
		t.Errorf("rising availability toned %q, want ok", availCell.TrendTone)
	}
	if errCell.TrendTone != "major" {
		t.Errorf("rising error rate toned %q, want major", errCell.TrendTone)
	}
	if availCell.TrendIcon.Glyph != "[trend.up]" || errCell.TrendIcon.Glyph != "[trend.up]" {
		t.Error("both trends should point up; only the colour differs")
	}
}

func TestRankMovementIsDescribed(t *testing.T) {
	up := svc("up")
	up.RankMovement = 3
	down := svc("down")
	down.RankMovement = -2
	same := svc("same")

	v := leaderboard(t, ctx([]model.Service{up, down, same}), "rank", "name")

	byID := map[string]widget.Row{}
	for _, r := range v.Rows {
		byID[r.ID] = r
	}
	upCell := cellKind(byID["up"], widget.CellRank)
	if upCell.RankMove.Glyph != "[rank.up]" || !strings.Contains(upCell.RankMoveLabel, "n=3") {
		t.Errorf("upward movement = %+v", upCell)
	}
	downCell := cellKind(byID["down"], widget.CellRank)
	if downCell.RankMove.Glyph != "[rank.down]" || !strings.Contains(downCell.RankMoveLabel, "n=2") {
		t.Errorf("downward movement = %+v", downCell)
	}
	if same := cellKind(byID["same"], widget.CellRank); same.RankMove.Glyph != "[rank.same]" {
		t.Errorf("unchanged movement = %+v", same)
	}
}

func TestLeaderboardEmptyState(t *testing.T) {
	c := ctx([]model.Service{svc("a")})
	c.Filtered, c.Ordered = nil, nil

	v := leaderboard(t, c, "rank", "name")

	if !v.Empty || len(v.Rows) != 0 {
		t.Errorf("got %+v, want an empty table", v)
	}
	// The reader needs to know how many they filtered away from.
	if !strings.Contains(v.EmptyText, "total=1") {
		t.Errorf("empty text = %q", v.EmptyText)
	}
	if !strings.Contains(v.ShowingText, "shown=0") || !strings.Contains(v.ShowingText, "total=1") {
		t.Errorf("showing text = %q", v.ShowingText)
	}
}

func TestLeaderboardOmitsOptionalCopy(t *testing.T) {
	v := build[widget.LeaderboardView](t, "leaderboard-table", ctx([]model.Service{svc("a")}),
		widget.Options{cols: []any{"rank"}})

	if v.Caption != "" || v.EmptyText != "" || v.ShowingText != "" {
		t.Errorf("got %+v, want no copy where none was configured", v)
	}
}

func TestUnknownColumnKeyIsLabelledAsItself(t *testing.T) {
	// Validation rejects this at startup; here it must not panic or render a
	// blank heading a reader cannot interpret.
	v := leaderboard(t, ctx([]model.Service{svc("a")}), "rank", "invented")

	if v.Columns[1].Label != "invented" {
		t.Errorf("label = %q, want the key itself", v.Columns[1].Label)
	}
	if got := len(metricCells(v.Rows[0])); got != 0 {
		t.Errorf("cells = %d, want none for a column that is not a metric", got)
	}
}

func TestWorstCellIsWhatANarrowScreenShows(t *testing.T) {
	// A card with room for one number should spend it on the bad news.
	v := leaderboard(t, ctx([]model.Service{svc("silent", noAvail())}),
		"rank", "metric.availability", "metric.errorRate")

	if !v.Rows[0].Worst.Unknown {
		t.Errorf("worst cell = %+v, want the unknown availability", v.Rows[0].Worst)
	}
}

func TestWorstCellFallsBackToTheFirst(t *testing.T) {
	v := leaderboard(t, ctx([]model.Service{svc("fine")}), "rank", "metric.availability")
	if v.Rows[0].Worst.Value == "" {
		t.Error("no worst cell chosen despite there being one to choose")
	}

	// A row with no metric columns has nothing to fall back to.
	bare := leaderboard(t, ctx([]model.Service{svc("fine")}), "rank", "name")
	if bare.Rows[0].Worst.Value != "" {
		t.Errorf("worst = %+v, want empty", bare.Rows[0].Worst)
	}
}

// --- stat tile -------------------------------------------------------------

func drawerCtx(sv model.Service, bind widget.Bind) widget.Context {
	c := ctx([]model.Service{sv})
	c.Service = &sv
	c.Bind = bind
	return c
}

func TestStatTile(t *testing.T) {
	c := drawerCtx(svc("a", avail(99.42)), widget.Bind{
		Source: widget.SourceServiceMetric, Metric: "metric.availability",
	})

	v := build[widget.StatTileView](t, "stat-tile", c, widget.Options{})

	if v.Label != "metric.availability" {
		t.Errorf("label = %q", v.Label)
	}
	if v.Cell.Value != "99.42%" {
		t.Errorf("value = %q", v.Cell.Value)
	}
}

func TestStatTileFramesACountWithNoTarget(t *testing.T) {
	// Traffic has no target — more requests is neither good nor bad — so it
	// earns its place by framing the success count.
	c := drawerCtx(svc("a"), widget.Bind{
		Source: widget.SourceServiceMetric, Metric: "metric.volume",
	})

	v := build[widget.StatTileView](t, "stat-tile", c, widget.Options{})

	if !strings.Contains(v.Frame, "success=999") || !strings.Contains(v.Frame, "total=1000") {
		t.Errorf("frame = %q, want both figures", v.Frame)
	}
}

func TestStatTileWithoutAServiceOrMetric(t *testing.T) {
	// Both are reachable while a drawer is closing or after a hot reload.
	if v := build[widget.StatTileView](t, "stat-tile", ctx(nil), widget.Options{}); v.Label != "" {
		t.Errorf("got %+v, want an empty tile", v)
	}

	c := drawerCtx(svc("a"), widget.Bind{Source: widget.SourceServiceMetric, Metric: "metric.invented"})
	if v := build[widget.StatTileView](t, "stat-tile", c, widget.Options{}); v.Label != "" {
		t.Errorf("got %+v, want an empty tile", v)
	}
}

// --- sparkline -------------------------------------------------------------

func TestSparkline(t *testing.T) {
	c := drawerCtx(svc("a"), widget.Bind{
		Source: widget.SourceServiceHistory, Field: config.FieldAvailability,
	})

	v := build[widget.SparklineView](t, "sparkline", c,
		widget.Options{"summaryTermId": "dr.spark.summary"})

	if v.Empty || v.Path == "" || v.Area == "" {
		t.Fatalf("got %+v, want geometry", v)
	}
	if v.ViewBox != "0 0 300 64" {
		t.Errorf("viewBox = %q", v.ViewBox)
	}
	if v.MinLabel == "" || v.MaxLabel == "" {
		t.Error("no axis labels")
	}
	if !strings.Contains(v.Summary, "days=30") {
		t.Errorf("summary = %q, want the window named", v.Summary)
	}
}

func TestSparklineHonoursAnExplicitWindow(t *testing.T) {
	c := drawerCtx(svc("a"), widget.Bind{
		Source: widget.SourceServiceHistory, Field: config.FieldAvailability, Days: 7,
	})

	v := build[widget.SparklineView](t, "sparkline", c, widget.Options{})

	if v.Days != 7 || len(v.Points) != 7 {
		t.Errorf("days = %d, points = %d; want the binding's window", v.Days, len(v.Points))
	}
}

func TestSparklineWithNothingToDraw(t *testing.T) {
	if v := build[widget.SparklineView](t, "sparkline", ctx(nil), widget.Options{}); !v.Empty {
		t.Error("a closed drawer produced a chart")
	}

	c := drawerCtx(svc("a", noHistory()), widget.Bind{Source: widget.SourceServiceHistory})
	if v := build[widget.SparklineView](t, "sparkline", c, widget.Options{}); !v.Empty {
		t.Error("a service with no history produced a chart")
	}
}

// --- bar chart -------------------------------------------------------------

func TestBarChart(t *testing.T) {
	c := drawerCtx(svc("a"), widget.Bind{
		Source: widget.SourceServiceHistory, Field: config.FieldVolume,
		Overlay: config.FieldErrorRate, Days: 30,
	})

	v := build[widget.BarChartView](t, "bar-chart", c, widget.Options{
		"peakTermId": "dr.traffic.peak", "lowTermId": "dr.traffic.trough",
		"overlayTermId": "metric.errorRate",
	})

	if v.Empty || len(v.Bars) != 30 {
		t.Fatalf("bars = %d, want 30", len(v.Bars))
	}
	if v.ViewBox != "0 0 300 80" {
		t.Errorf("viewBox = %q", v.ViewBox)
	}
	// {v} and {d}, matching what the shipped dr.traffic.peak declares. The
	// names have to agree exactly: a parameter the message does not name is
	// substituted as nothing, so a mismatch renders "Peak · " with both values
	// silently missing.
	if !strings.Contains(v.PeakText, "v=") || !strings.Contains(v.PeakText, "d=") {
		t.Errorf("peak = %q", v.PeakText)
	}
	if v.OverlayLabel != "metric.errorRate" {
		t.Errorf("overlay label = %q", v.OverlayLabel)
	}
}

func TestBarChartFallsBackToTheReadersWindow(t *testing.T) {
	c := drawerCtx(svc("a"), widget.Bind{Source: widget.SourceServiceHistory})
	c.View.Days = 7

	v := build[widget.BarChartView](t, "bar-chart", c, widget.Options{})

	if v.Days != 7 || len(v.Bars) != 7 {
		t.Errorf("days = %d, bars = %d", v.Days, len(v.Bars))
	}
}

func TestBarChartWithNothingToDraw(t *testing.T) {
	if v := build[widget.BarChartView](t, "bar-chart", ctx(nil), widget.Options{}); !v.Empty {
		t.Error("a closed drawer produced a chart")
	}

	c := drawerCtx(svc("a", noHistory()), widget.Bind{Source: widget.SourceServiceHistory})
	v := build[widget.BarChartView](t, "bar-chart", c,
		widget.Options{"peakTermId": "dr.traffic.peak"})
	if !v.Empty {
		t.Error("a service with no history produced a chart")
	}
	if v.PeakText != "" {
		t.Errorf("peak = %q, want nothing to describe", v.PeakText)
	}
}

// --- bar list --------------------------------------------------------------

func withErrors(s *model.Service) {
	s.Errors = []model.ErrorBucket{
		{Code: "503", TermID: "err.503", Class: model.ErrorClassServer, Count: 100, Share: 50, Trend: model.DirectionUp},
		{Code: "429", TermID: "err.429", Class: model.ErrorClassClient, Count: 60, Share: 30, Trend: model.DirectionFlat},
		{Code: "timeout", TermID: "err.timeout", Class: model.ErrorClassNetwork, Count: 40, Share: 20, Trend: model.DirectionDown},
	}
}

func TestBarList(t *testing.T) {
	c := drawerCtx(svc("a", withErrors), widget.Bind{Source: widget.SourceServiceErrors})

	v := build[widget.BarListView](t, "bar-list", c, widget.Options{})

	if v.Empty || len(v.Rows) != 3 {
		t.Fatalf("rows = %d", len(v.Rows))
	}
	// Widths are relative to the largest row, so the smaller codes stay visible
	// rather than becoming slivers.
	if v.Rows[0].Percent != 100 || v.Rows[1].Percent != 60 {
		t.Errorf("widths = %v / %v", v.Rows[0].Percent, v.Rows[1].Percent)
	}
	// Colour by who is at fault, which is what the reader is working out.
	if v.Rows[0].Tone != "major" || v.Rows[1].Tone != "partial" || v.Rows[2].Tone != "unknown" {
		t.Errorf("tones = %q / %q / %q", v.Rows[0].Tone, v.Rows[1].Tone, v.Rows[2].Tone)
	}
	if v.Total != "200" {
		t.Errorf("total = %q", v.Total)
	}
}

func TestBarListRespectsItsLimit(t *testing.T) {
	c := drawerCtx(svc("a", withErrors), widget.Bind{Source: widget.SourceServiceErrors})

	v := build[widget.BarListView](t, "bar-list", c, widget.Options{"limit": 2})

	if len(v.Rows) != 2 {
		t.Errorf("rows = %d, want 2", len(v.Rows))
	}
}

func TestBarListWithNothingToShow(t *testing.T) {
	if v := build[widget.BarListView](t, "bar-list", ctx(nil), widget.Options{}); !v.Empty {
		t.Error("a closed drawer produced rows")
	}
	c := drawerCtx(svc("a"), widget.Bind{Source: widget.SourceServiceErrors})
	if v := build[widget.BarListView](t, "bar-list", c, widget.Options{}); !v.Empty {
		t.Error("a service with no errors produced rows")
	}
}

// --- timeline --------------------------------------------------------------

func withIncidents(s *model.Service) {
	s.Incidents = []model.Incident{
		{
			ID: "open", ServiceID: s.ID, Severity: model.StatusMajor, Open: true,
			OpenedAt: now.Add(-3 * time.Hour), NoteTermID: "incident.note.errorSpike",
			Events: []model.IncidentEvent{
				{Type: "opened", At: now.Add(-3 * time.Hour)},
				{Type: "acknowledged", At: now.Add(-150 * time.Minute)},
			},
		},
		{
			ID: "closed", ServiceID: s.ID, Severity: model.StatusPartial,
			OpenedAt: now.Add(-30 * time.Hour), ClosedAt: now.Add(-28 * time.Hour),
			NoteTermID: "incident.note.rateLimited",
			Events: []model.IncidentEvent{
				{Type: "opened", At: now.Add(-30 * time.Hour)},
				{Type: "resolved", At: now.Add(-28 * time.Hour)},
			},
		},
	}
}

func TestTimeline(t *testing.T) {
	c := drawerCtx(svc("a", withIncidents), widget.Bind{Source: widget.SourceServiceIncidents})

	v := build[widget.TimelineView](t, "timeline", c,
		widget.Options{"ongoingTermId": "dr.inc.ongoing"})

	if v.Empty || len(v.Incidents) != 2 {
		t.Fatalf("incidents = %d", len(v.Incidents))
	}
	open := v.Incidents[0]
	if !open.Open || open.Tone != "major" {
		t.Errorf("open incident = %+v", open)
	}
	// An open incident is measured to now; a closed one to when it closed.
	if !strings.Contains(open.Duration, "3h0m0s") {
		t.Errorf("open duration = %q", open.Duration)
	}
	if !strings.Contains(v.Incidents[1].Duration, "2h0m0s") {
		t.Errorf("closed duration = %q", v.Incidents[1].Duration)
	}
	// Stage names resolve as inc.<type>, so a deployment adds a stage by adding
	// a locale key.
	if open.Events[0].Label != "inc.opened" {
		t.Errorf("first event = %q", open.Events[0].Label)
	}
	if !open.Events[len(open.Events)-1].Last {
		t.Error("the final event is not marked, so the timeline cannot terminate its line")
	}
	if open.Events[0].ISO == "" {
		t.Error("no machine-readable time on an event")
	}
}

func TestTimelineEmptyState(t *testing.T) {
	c := drawerCtx(svc("a"), widget.Bind{Source: widget.SourceServiceIncidents})

	v := build[widget.TimelineView](t, "timeline", c, widget.Options{
		"emptyTermId": "dr.inc.none", "emptyIconKey": "ui.check",
	})

	if !v.Empty {
		t.Fatal("a service with no incidents was not marked empty")
	}
	// "No incidents" is good news and worth saying, not a blank panel.
	if v.EmptyText != "dr.inc.none" || v.EmptyIcon.Glyph != "[ui.check]" {
		t.Errorf("empty state = %+v", v)
	}

	if v := build[widget.TimelineView](t, "timeline", ctx(nil), widget.Options{}); !v.Empty {
		t.Error("a closed drawer produced incidents")
	}
}

// --- data table ------------------------------------------------------------

func TestDataTableBehindTheTrafficChart(t *testing.T) {
	// Every chart has one: a line on a screen is not accessible to a screen
	// reader, not copyable, and not checkable.
	c := drawerCtx(svc("a"), widget.Bind{Source: widget.SourceServiceHistory, Days: 30})

	v := build[widget.DataTableView](t, "data-table", c,
		widget.Options{"summaryTermId": "dr.traffic.table", "every": 5})

	if v.Empty {
		t.Fatal("no rows")
	}
	if v.Summary != "dr.traffic.table" {
		t.Errorf("summary = %q", v.Summary)
	}
	if len(v.Headers) != 3 {
		t.Errorf("headers = %v", v.Headers)
	}
	// Sampling every fifth day, but never at the cost of the most recent one:
	// dropping today would be actively misleading.
	if len(v.Rows) != 7 {
		t.Errorf("rows = %d, want six samples plus the final day", len(v.Rows))
	}
	last := v.Rows[len(v.Rows)-1][0]
	if last != c.Service.History[len(c.Service.History)-1].Day.Format("2006-01-02") {
		t.Errorf("last row is %q, not the most recent day", last)
	}
}

func TestDataTableBehindTheErrorBreakdown(t *testing.T) {
	c := drawerCtx(svc("a", withErrors), widget.Bind{Source: widget.SourceServiceErrors})

	v := build[widget.DataTableView](t, "data-table", c, widget.Options{})

	if len(v.Rows) != 3 {
		t.Fatalf("rows = %d", len(v.Rows))
	}
	if v.Rows[0][0] != "503" {
		t.Errorf("first row = %v", v.Rows[0])
	}
	if len(v.Headers) != 4 {
		t.Errorf("headers = %v", v.Headers)
	}
}

func TestDataTableWithNothingToShow(t *testing.T) {
	if v := build[widget.DataTableView](t, "data-table", ctx(nil), widget.Options{}); !v.Empty {
		t.Error("a closed drawer produced rows")
	}

	c := drawerCtx(svc("a", noHistory()), widget.Bind{Source: widget.SourceServiceHistory})
	if v := build[widget.DataTableView](t, "data-table", c, widget.Options{}); !v.Empty {
		t.Error("a service with no history produced rows")
	}
}

func TestDataTableSamplesEveryRowByDefault(t *testing.T) {
	c := drawerCtx(svc("a"), widget.Bind{Source: widget.SourceServiceHistory, Days: 5})

	v := build[widget.DataTableView](t, "data-table", c, widget.Options{})

	if len(v.Rows) != 5 {
		t.Errorf("rows = %d, want every day", len(v.Rows))
	}
}
