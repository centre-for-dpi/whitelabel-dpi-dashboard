package widget

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/chart"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/prose"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/query"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/rules"
)

// Default returns a registry with every widget this build provides.
//
// One place lists them, and it is additive: a new widget is a definition here,
// a builder, and a template. Nothing else in the engine needs to know.
func Default() *Registry {
	r := NewRegistry()
	for _, d := range []Definition{
		headingDef(), statusSummaryDef(), segmentedBarDef(), legendDef(),
		coverageDef(), timestampDef(), disclosureDef(), ctaButtonDef(),
		signalCardsDef(), filterBarDef(), leaderboardDef(),
		statTileDef(), sparklineDef(), barChartDef(), barListDef(),
		timelineDef(), dataTableDef(), proseDef(),
	} {
		r.Register(d)
	}
	return r
}

// tone maps a status to its semantic colour name, so templates reference
// --status-ok rather than a colour and a deployment restyles from theme.yaml.
func tone(status string) string {
	if status == string(model.StatusOperational) {
		return "ok"
	}
	return status
}

// --- heading ---------------------------------------------------------------

// HeadingView is a section title.
type HeadingView struct {
	Level   int
	Class   string
	Eyebrow string
	Title   string
	ID      string
}

func headingDef() Definition {
	schema := OptionSchema{
		"termId":  {Kind: KindString, Required: true, Doc: "Term id for the title."},
		"eyebrow": {Kind: KindString, Doc: "Term id for the small label above the title."},
		"scoped":  {Kind: KindBool, Doc: "Append the current scope to the term id, so the heading reads differently per scope."},
		"level":   {Kind: KindInt, Default: 2, Doc: "HTML heading level, 1 to 6."},
		"class":   {Kind: KindString, Doc: "Extra class, e.g. \"serif\"."},
		"id":      {Kind: KindString, Doc: "DOM id, for aria-labelledby."},
	}
	return Definition{
		Type: "heading", Template: "heading", Schema: schema,
		Doc: "A section title, optionally varying by scope.",
		Validate: func(o Options, _ Bind, _ ValidationContext) []error {
			// Caught here rather than quietly clamped. A heading level is a
			// document-structure decision, and silently rewriting it would
			// leave the author believing they had changed something.
			if lvl := o.Int(schema, "level"); lvl < 1 || lvl > 6 {
				return []error{fmt.Errorf(
					"heading level %d is not an HTML heading level; expected 1 to 6", lvl)}
			}
			return nil
		},
		Build: func(c Context, o Options) (any, error) {
			term := o.String(schema, "termId")
			if o.Bool(schema, "scoped") {
				term += "." + c.State.Scope
			}
			// Clamped as a last resort: validation rejects this at startup, but
			// a hot reload races validation and invalid HTML is worse than a
			// wrong level.
			level := o.Int(schema, "level")
			if level < 1 || level > 6 {
				level = 2
			}
			v := HeadingView{
				Level: level,
				Class: o.String(schema, "class"),
				Title: c.Text.Text(term, nil),
				ID:    o.String(schema, "id"),
			}
			if e := o.String(schema, "eyebrow"); e != "" {
				v.Eyebrow = c.Text.Text(e, nil)
			}
			return v, nil
		},
	}
}

// --- status summary --------------------------------------------------------

// StatusSummaryView is the one-line verdict: "77 Operational · 15 Partial …".
type StatusSummaryView struct {
	Parts   []string
	AllWell bool
	Text    string
}

func statusSummaryDef() Definition {
	schema := OptionSchema{
		"allWellTermId": {Kind: KindString, Doc: "Term shown when every service is operational."},
		"separator":     {Kind: KindString, Default: " · ", Doc: "Text between counts."},
	}
	return Definition{
		Type: "status-summary", Template: "status-summary", Schema: schema,
		Sources: []string{SourceStatusCounts},
		Doc:     "A one-line tally of how many services are in each state.",
		Build: func(c Context, o Options) (any, error) {
			counts := c.View.Counts(c.Scoped)
			sm := c.Config.Domain.StatusModel

			var v StatusSummaryView
			troubled := 0
			for _, s := range sm.Order {
				n := counts[s]
				if n == 0 {
					continue
				}
				if s != string(model.StatusOperational) {
					troubled += n
				}
				v.Parts = append(v.Parts, fmt.Sprintf("%s %s",
					c.Text.Number(float64(n), 0), c.Text.Text(sm.LabelTermID[s], nil)))
			}

			// "All clear" is worth saying plainly rather than making the reader
			// infer it from a row of numbers.
			if troubled == 0 && len(c.Scoped) > 0 {
				if term := o.String(schema, "allWellTermId"); term != "" {
					v.AllWell = true
					v.Text = c.Text.Text(term, nil)
					return v, nil
				}
			}
			v.Text = joinWith(v.Parts, o.String(schema, "separator"))
			return v, nil
		},
	}
}

func joinWith(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

// --- segmented bar ---------------------------------------------------------

// Segment is one band of the stacked status bar.
type Segment struct {
	Status  string
	Tone    string
	Icon    Icon
	Count   string
	Label   string
	Percent float64
}

// SegmentedBarView is the stacked bar under the verdict.
type SegmentedBarView struct {
	Segments  []Segment
	Total     int
	HideBelow string
	Label     string
}

func segmentedBarDef() Definition {
	schema := OptionSchema{
		"hideBelow":   {Kind: KindString, Doc: "Viewport width below which the bar is hidden in favour of the legend."},
		"labelTermId": {Kind: KindString, Doc: "Accessible name for the bar as a whole."},
	}
	return Definition{
		Type: "segmented-bar", Template: "segmented-bar", Schema: schema,
		Sources: []string{SourceStatusCounts},
		Doc:     "A single stacked bar showing the proportion of services in each state.",
		Build: func(c Context, o Options) (any, error) {
			counts := c.View.Counts(c.Scoped)
			sm := c.Config.Domain.StatusModel

			total := 0
			for _, s := range sm.Order {
				total += counts[s]
			}

			v := SegmentedBarView{
				Total:     total,
				HideBelow: o.String(schema, "hideBelow"),
			}
			if term := o.String(schema, "labelTermId"); term != "" {
				v.Label = c.Text.Text(term, nil)
			}
			if total == 0 {
				return v, nil
			}

			// Zero-count statuses are omitted from the bar but kept in the
			// legend: a band of no width is not informative, while a legend
			// entry reading "0" is.
			for _, s := range sm.Order {
				n := counts[s]
				if n == 0 {
					continue
				}
				v.Segments = append(v.Segments, Segment{
					Status:  s,
					Tone:    tone(s),
					Icon:    c.Icons.Icon(sm.IconKey[s]),
					Count:   c.Text.Number(float64(n), 0),
					Label:   c.Text.Text(sm.LabelTermID[s], nil),
					Percent: float64(n) / float64(total) * 100,
				})
			}
			return v, nil
		},
	}
}

// --- legend ----------------------------------------------------------------

// LegendView lists every status with its count, including the ones at zero.
type LegendView struct {
	Items []Segment
}

func legendDef() Definition {
	return Definition{
		Type: "legend", Template: "legend", Schema: OptionSchema{},
		Sources: []string{SourceStatusCounts},
		Doc:     "Every status with its count, including statuses with none.",
		Build: func(c Context, _ Options) (any, error) {
			counts := c.View.Counts(c.Scoped)
			sm := c.Config.Domain.StatusModel

			v := LegendView{}
			for _, s := range sm.Order {
				v.Items = append(v.Items, Segment{
					Status: s,
					Tone:   tone(s),
					Icon:   c.Icons.Icon(sm.IconKey[s]),
					Count:  c.Text.Number(float64(counts[s]), 0),
					Label:  c.Text.Text(sm.LabelTermID[s], nil),
				})
			}
			return v, nil
		},
	}
}

// --- coverage --------------------------------------------------------------

// CoverageView states how much of the estate is being watched at all.
type CoverageView struct{ Text string }

func coverageDef() Definition {
	schema := OptionSchema{
		"termId": {Kind: KindString, Required: true, Doc: "Term receiving {onboarded} and {total}."},
	}
	return Definition{
		Type: "coverage", Template: "coverage", Schema: schema,
		Sources: []string{SourceCoverage},
		Doc:     "How many services are onboarded out of the total addressable estate.",
		Build: func(c Context, o Options) (any, error) {
			// Said plainly because a dashboard that watches a fifth of the
			// estate and does not say so overstates what it knows.
			return CoverageView{Text: c.Text.Text(o.String(schema, "termId"), map[string]any{
				"onboarded": len(c.Snapshot.Services),
				"total":     c.Config.Domain.OnboardedDenominator,
			})}, nil
		},
	}
}

// --- timestamp -------------------------------------------------------------

// TimestampView is the "updated N minutes ago" line.
type TimestampView struct {
	ISO      string
	Absolute string
	Relative string
	Stale    bool
}

func timestampDef() Definition {
	schema := OptionSchema{
		"termId":     {Kind: KindString, Doc: "Term receiving {when}."},
		"staleAfter": {Kind: KindInt, Doc: "Seconds after which the timestamp is marked stale."},
	}
	return Definition{
		Type: "timestamp", Template: "timestamp", Schema: schema,
		Sources: []string{SourceUpdatedAt},
		Doc:     "When the data was last refreshed, in words and as a machine-readable time.",
		Build: func(c Context, o Options) (any, error) {
			at := c.Snapshot.GeneratedAt
			v := TimestampView{
				ISO:      at.UTC().Format(time.RFC3339),
				Absolute: c.Text.DateTime(at),
				Relative: c.Text.RelativeTime(at, c.Now),
			}
			// Data quietly going stale is the failure mode a status dashboard
			// can least afford, so it is called out rather than left to be
			// noticed.
			if after := o.Int(schema, "staleAfter"); after > 0 {
				v.Stale = c.Now.Sub(at) > time.Duration(after)*time.Second
			}
			return v, nil
		},
	}
}

// --- disclosure ------------------------------------------------------------

// DisclosureView is the collapsible "how status is decided" block.
type DisclosureView struct {
	Summary string
	Items   []string
	Open    bool
}

func disclosureDef() Definition {
	schema := OptionSchema{
		"summaryTermId": {Kind: KindString, Required: true, Doc: "Term for the clickable summary line."},
		"open":          {Kind: KindBool, Doc: "Start expanded."},
	}
	return Definition{
		Type: "disclosure", Template: "disclosure", Schema: schema,
		Sources: []string{SourceConfigThresholdProse},
		Doc:     "The published rules, with the live threshold numbers filled in.",
		Build: func(c Context, o Options) (any, error) {
			d := c.Config.Domain
			params := rules.ProseParams(d.Thresholds.Values)

			v := DisclosureView{
				Summary: c.Text.Text(o.String(schema, "summaryTermId"), nil),
				Open:    o.Bool(schema, "open"),
			}
			// In evaluation order, because that is the order they are applied
			// and the reader is being told how the verdict was reached.
			for _, status := range d.Thresholds.EvaluationOrder {
				term, ok := d.Thresholds.Prose[status]
				if !ok {
					continue
				}
				v.Items = append(v.Items, c.Text.Text(term, params))
			}
			return v, nil
		},
	}
}

// --- call to action --------------------------------------------------------

// CTAView is a prominent link into another part of the page.
type CTAView struct {
	Label  string
	Target string
	Icon   Icon
}

func ctaButtonDef() Definition {
	schema := OptionSchema{
		"termId":  {Kind: KindString, Required: true, Doc: "Term for the button label."},
		"target":  {Kind: KindString, Required: true, Doc: "Fragment id to scroll to, e.g. \"#leaderboard\"."},
		"iconKey": {Kind: KindString, Doc: "Icon rendered after the label."},
	}
	return Definition{
		Type: "cta-button", Template: "cta-button", Schema: schema,
		Doc: "A prominent link to another section.",
		Build: func(c Context, o Options) (any, error) {
			return CTAView{
				Label:  c.Text.Text(o.String(schema, "termId"), nil),
				Target: o.String(schema, "target"),
				Icon:   c.Icons.Icon(o.String(schema, "iconKey")),
			}, nil
		},
	}
}

// --- signal cards ----------------------------------------------------------

// SignalCard is one finding, with the rule that produced it.
type SignalCard struct {
	ID          string
	Tone        string
	Icon        Icon
	Title       string
	Rule        string
	ActionLabel string
	// Filter and Service are the two shapes an action takes: narrow the board,
	// or open one service.
	FilterStatuses   []string
	FilterCategories []string
	ServiceID        string
	ServiceTab       string
	Empty            bool
}

// SignalCardsView is the row of findings.
type SignalCardsView struct{ Cards []SignalCard }

func signalCardsDef() Definition {
	schema := OptionSchema{
		"actionTermId": {Kind: KindString, Doc: "Term for the card's action link."},
	}
	return Definition{
		Type: "signal-cards", Template: "signal-cards", Schema: schema,
		Sources: []string{SourceSignals},
		Doc:     "Findings worth attention, each printing the rule it applied.",
		Build: func(c Context, o Options) (any, error) {
			action := ""
			if term := o.String(schema, "actionTermId"); term != "" {
				action = c.Text.Text(term, nil)
			}

			v := SignalCardsView{}
			for _, s := range c.Signals() {
				// rules reports a raw span because it has no formatter and
				// should not acquire one; turning it into "3 hours" in the
				// reader's language is presentation, and belongs here.
				params := s.Params
				if secs, ok := params["seconds"].(int64); ok {
					params = withDuration(params, c.Text.Duration(time.Duration(secs)*time.Second))
				}

				card := SignalCard{
					ID:    s.ID,
					Tone:  s.Tone,
					Icon:  c.Icons.Icon(s.IconKey),
					Title: c.Text.Text(s.TitleTermID, params),
					Empty: s.Empty,
				}
				// A card that states a finding without its basis is an
				// assertion the reader cannot check.
				if s.RuleTermID != "" {
					card.Rule = c.Text.Text(s.RuleTermID, params)
				}
				if !s.Empty {
					card.ActionLabel = action
				}
				if s.Filter != nil {
					card.FilterStatuses = s.Filter.Status
					card.FilterCategories = s.Filter.Category
				}
				if s.Focus != nil {
					card.ServiceID = s.Focus.ServiceID
					card.ServiceTab = s.Focus.Tab
				}
				v.Cards = append(v.Cards, card)
			}
			return v, nil
		},
	}
}

// --- filter bar ------------------------------------------------------------

// Chip is one toggleable filter.
type Chip struct {
	Value  string
	Label  string
	Icon   Icon
	Tone   string
	Count  string
	Active bool
}

// FilterBarView is the leaderboard's controls.
type FilterBarView struct {
	Search        string
	SearchLabel   string
	Statuses      []Chip
	Categories    []Chip
	Regions       []Chip
	RegionEnabled bool
	AppliedCount  int
	ClearVisible  bool
	ResultCount   int
	ResultText    string
	ApplyLabel    string
	ClearLabel    string
	// The fieldset legends, which are visually hidden and read aloud. They were
	// hardcoded English for as long as they were, because nothing that appears
	// on screen shows them — an Arabic screen-reader user heard "status".
	StatusLegend   string
	CategoryLegend string
	// RegionLabel names the region selector. It was the literal string "region",
	// in English, on a page translated into eight languages — and it is an
	// accessible name, so it is the one string on that control anybody hears.
	RegionLabel string
	// Expanded is whether the narrow-screen panel is showing.
	Expanded bool
}

func filterBarDef() Definition {
	schema := OptionSchema{
		"searchTermId": {Kind: KindString, Doc: "Placeholder term for the search box."},
		"resultTermId": {Kind: KindString, Doc: "Term receiving {n}, announced to screen readers."},
		"showRegions":  {Kind: KindBool, Default: true, Doc: "Offer the region selector."},
		// Both controls exist only without JavaScript, which is exactly why
		// their labels were still hardcoded English: nothing that renders in a
		// normal browser session shows them.
		"applyTermId":          {Kind: KindString, Default: "chrome.apply", Doc: "Label for the no-JavaScript submit button."},
		"clearTermId":          {Kind: KindString, Default: "flt.clear", Doc: "Label for the clear-filters link."},
		"statusLegendTermId":   {Kind: KindString, Default: "flt.status", Doc: "Screen-reader name for the status chip group."},
		"categoryLegendTermId": {Kind: KindString, Default: "flt.category", Doc: "Screen-reader name for the category chip group."},
		"regionLabelTermId":    {Kind: KindString, Default: "flt.region", Doc: "Screen-reader name for the region selector."},
	}
	return Definition{
		Type: "filter-bar", Template: "filter-bar", Schema: schema,
		Doc: "Search, status chips, category chips and the region selector.",
		Build: func(c Context, o Options) (any, error) {
			d := c.Config.Domain
			counts := c.View.Counts(c.Scoped)

			v := FilterBarView{
				Search:      c.State.Search,
				ResultCount: len(c.Filtered),
				Expanded:    c.State.FiltersOpen,
			}
			if term := o.String(schema, "regionLabelTermId"); term != "" {
				v.RegionLabel = c.Text.Text(term, nil)
			}
			if term := o.String(schema, "searchTermId"); term != "" {
				v.SearchLabel = c.Text.Text(term, nil)
			}
			if term := o.String(schema, "resultTermId"); term != "" {
				// {n}, not {count}: the shipped term is a plural whose argument
				// is n, and a name the message does not declare is substituted
				// as nothing — leaving a bare "#" on the page.
				v.ResultText = c.Text.Text(term, map[string]any{"n": len(c.Filtered)})
			}
			if term := o.String(schema, "applyTermId"); term != "" {
				v.ApplyLabel = c.Text.Text(term, nil)
			}
			if term := o.String(schema, "clearTermId"); term != "" {
				v.ClearLabel = c.Text.Text(term, nil)
			}
			if term := o.String(schema, "statusLegendTermId"); term != "" {
				v.StatusLegend = c.Text.Text(term, nil)
			}
			if term := o.String(schema, "categoryLegendTermId"); term != "" {
				v.CategoryLegend = c.Text.Text(term, nil)
			}

			for _, s := range d.StatusModel.Order {
				v.Statuses = append(v.Statuses, Chip{
					Value:  s,
					Label:  c.Text.Text(d.StatusModel.LabelTermID[s], nil),
					Icon:   c.Icons.Icon(d.StatusModel.IconKey[s]),
					Tone:   tone(s),
					Count:  c.Text.Number(float64(counts[s]), 0),
					Active: slices.Contains(c.State.Statuses, s),
				})
			}
			for _, cat := range d.Taxonomy.Categories {
				v.Categories = append(v.Categories, Chip{
					Value:  cat.ID,
					Label:  c.Text.Text(cat.TermID, nil),
					Icon:   c.Icons.Icon(cat.IconKey),
					Active: slices.Contains(c.State.Categories, cat.ID),
				})
			}

			// The region selector only means something in a sub-national view;
			// offering it otherwise invites a choice that does nothing.
			v.RegionEnabled = o.Bool(schema, "showRegions") && c.State.Scope != d.DefaultScope
			if o.Bool(schema, "showRegions") {
				for _, r := range d.Taxonomy.Regions {
					if r.Scope != c.State.Scope && r.Scope != d.DefaultScope {
						continue
					}
					v.Regions = append(v.Regions, Chip{
						Value:  r.ID,
						Label:  c.Text.Text(r.TermID, nil),
						Active: c.State.Region == r.ID,
					})
				}
			}

			f := query.Filter{
				Statuses:   c.State.Statuses,
				Categories: c.State.Categories,
				Search:     c.State.Search,
			}
			v.AppliedCount = f.Count()
			v.ClearVisible = f.Active()
			return v, nil
		},
	}
}

// --- leaderboard -----------------------------------------------------------

// Column is one sortable heading.
type Column struct {
	Key     string
	Label   string
	Align   string
	Sorted  bool
	Dir     string
	NextDir string
	// SortIcon shows which way the sorted column is sorted. aria-sort tells a
	// screen reader; nothing was telling anyone looking at the screen, so the
	// two glyphs icons.yaml has always declared for this went unused.
	SortIcon Icon
}

// Cell is one measurement in a row.
type Cell struct {
	Value     string
	Target    string
	Trend     string
	TrendTone string
	TrendIcon Icon
	Unknown   bool
	Note      string
}

// CellKind is what a table cell holds. It exists so the template can render
// columns in whatever order the layout declares, rather than assuming rank
// comes first and measurements come last.
type CellKind string

const (
	CellRank   CellKind = "rank"
	CellName   CellKind = "name"
	CellStatus CellKind = "status"
	CellMetric CellKind = "metric"
)

// RowCell is one cell, tagged with what it holds.
type RowCell struct {
	Kind  CellKind
	Align string

	// Rank.
	Rank          string
	RankMove      Icon
	RankMoveLabel string

	// Name.
	Name string
	// Href is the service's own address, so the name is a real focusable link
	// inside a row that is otherwise only clickable. FragmentHref is the same
	// service as a drawer fragment: the link carries both, so a pointer and a
	// keyboard open the drawer by the same route and a reader without script
	// still gets the full page.
	Href         string
	FragmentHref string
	Description  string
	Category     string
	CategoryIcon Icon
	Region       string

	// Status.
	StatusLabel string
	StatusIcon  Icon
	StatusTone  string

	// Metric.
	Cell Cell
}

// Row is one service in the table.
//
// Cells follow the configured column order exactly, so a deployment that puts
// availability before the service name gets that, rather than a template's
// opinion of how a table should be arranged.
type Row struct {
	ID          string
	Name        string
	StatusLabel string
	StatusIcon  Icon
	StatusTone  string
	Rank        string
	Cells       []RowCell

	// Worst is the single figure a screen too narrow for a table shows.
	Worst Cell
}

// LeaderboardView is the sortable comparison table.
type LeaderboardView struct {
	Columns   []Column
	Rows      []Row
	Caption   string
	Empty     bool
	EmptyText string
	// PeriodLabel names the window the figures cover, so the reader knows the
	// numbers beside a rank are the numbers the rank was computed from.
	PeriodLabel string
	Total       int
	Shown       int
	ShowingText string
}

func leaderboardDef() Definition {
	schema := OptionSchema{
		"columns":       {Kind: KindStringList, Required: true, Doc: "Column keys, in order: rank, name, status, or a metric id."},
		"captionTermId": {Kind: KindString, Doc: "Term describing the table, for screen readers."},
		"emptyTermId":   {Kind: KindString, Doc: "Term shown when the filter matches nothing."},
		"showingTermId": {Kind: KindString, Doc: "Term receiving {shown} and {total}."},
		"mobileCards":   {Kind: KindBool, Default: true, Doc: "Fall back to cards on a narrow screen."},
	}
	return Definition{
		Type: "leaderboard-table", Template: "leaderboard-table", Schema: schema,
		Sources: []string{SourceServicesFiltered, SourceServicesScoped, SourceServicesAll},
		Doc:     "The sortable comparison table.",
		Validate: func(o Options, _ Bind, vc ValidationContext) []error {
			var errs []error
			var metricIDs []string
			for _, m := range vc.Domain.Metrics {
				metricIDs = append(metricIDs, m.ID)
			}
			for _, key := range o.StringList(OptionSchema{
				"columns": {Kind: KindStringList},
			}, "columns") {
				switch key {
				case query.SortRank, query.SortName, query.SortStatus:
				default:
					if !slices.Contains(metricIDs, key) {
						errs = append(errs, fmt.Errorf(
							"column %q is neither rank, name, status nor a declared metric; this deployment declares %v",
							key, metricIDs))
					}
				}
			}
			return errs
		},
		Build: func(c Context, o Options) (any, error) {
			d := c.Config.Domain
			keys := o.StringList(schema, "columns")

			v := LeaderboardView{
				Total: len(c.Scoped),
				Shown: len(c.Ordered),
				Empty: len(c.Ordered) == 0,
			}
			if term := o.String(schema, "captionTermId"); term != "" {
				v.Caption = c.Text.Text(term, nil)
			}
			if term := o.String(schema, "emptyTermId"); term != "" {
				v.EmptyText = c.Text.Text(term, map[string]any{"total": len(c.Scoped)})
			}
			if term := o.String(schema, "showingTermId"); term != "" {
				v.ShowingText = c.Text.Text(term, map[string]any{
					"shown": len(c.Ordered), "total": len(c.Scoped),
				})
			}
			for _, p := range d.Periods {
				if p.ID == c.State.Period {
					v.PeriodLabel = c.Text.Text(p.TermID, nil)
				}
			}

			for _, key := range keys {
				col := Column{Key: key, Label: columnLabel(c, key), Align: "start"}
				if metricByID(d, key) != nil {
					col.Align = "end"
				}
				if c.State.Sort == key {
					col.Sorted = true
					col.Dir = c.State.Dir
					col.NextDir = query.Asc
					if c.State.Dir == query.Asc {
						col.NextDir = query.Desc
					}
					// The icon depicts the current order, not the order clicking
					// would produce: it is a status, and aria-sort beside it says
					// the same thing to a reader who cannot see it.
					if col.Dir == query.Asc {
						col.SortIcon = c.Icons.Icon("ui.sortAsc")
					} else {
						col.SortIcon = c.Icons.Icon("ui.sortDesc")
					}
				} else {
					col.NextDir = query.DefaultDirection(key)
				}
				v.Columns = append(v.Columns, col)
			}

			for _, sv := range c.Ordered {
				v.Rows = append(v.Rows, buildRow(c, sv, keys))
			}
			return v, nil
		},
	}
}

func columnLabel(c Context, key string) string {
	switch key {
	case query.SortRank:
		return c.Text.Text("lb.col.rank", nil)
	case query.SortName:
		return c.Text.Text("lb.col.name", nil)
	case query.SortStatus:
		return c.Text.Text("lb.col.status", nil)
	}
	if m := metricByID(c.Config.Domain, key); m != nil {
		return c.Text.Text(m.TermID, nil)
	}
	return key
}

func metricByID(d config.Domain, id string) *config.Metric {
	for i := range d.Metrics {
		if d.Metrics[i].ID == id {
			return &d.Metrics[i]
		}
	}
	return nil
}

func buildRow(c Context, sv model.Service, keys []string) Row {
	d := c.Config.Domain
	sm := d.StatusModel
	status := string(sv.Status)

	r := Row{
		ID:          sv.ID,
		Name:        c.Text.Text(sv.NameTermID, nil),
		StatusLabel: c.Text.Text(sm.LabelTermID[status], nil),
		StatusIcon:  c.Icons.Icon(sm.IconKey[status]),
		StatusTone:  tone(status),
		Rank:        c.Text.Number(float64(c.Ranks[sv.ID]), 0),
	}

	var metrics []Cell
	for _, key := range keys {
		switch key {
		case query.SortRank:
			r.Cells = append(r.Cells, rankCell(c, sv, r.Rank))
		case query.SortName:
			r.Cells = append(r.Cells, RowCell{
				Kind:         CellName,
				Align:        "start",
				Name:         r.Name,
				Href:         "/service/" + sv.ID,
				FragmentHref: "/fragments/service/" + sv.ID,
				Description:  c.Text.Text(sv.DescTermID, nil),
				Category:     c.Text.Text(sv.CategoryID, nil),
				CategoryIcon: c.Icons.Icon(categoryIcon(d, sv.CategoryID)),
				Region:       c.Text.Text(sv.RegionID, nil),
			})
		case query.SortStatus:
			r.Cells = append(r.Cells, RowCell{
				Kind:        CellStatus,
				Align:       "start",
				StatusLabel: r.StatusLabel,
				StatusIcon:  r.StatusIcon,
				StatusTone:  r.StatusTone,
			})
		default:
			if m := metricByID(d, key); m != nil {
				cell := buildCell(c, sv, *m)
				metrics = append(metrics, cell)
				r.Cells = append(r.Cells, RowCell{Kind: CellMetric, Align: "end", Cell: cell})
			}
		}
	}

	// The single figure a narrow screen shows: whichever reads worst, since a
	// card with room for one number should spend it on the bad news.
	r.Worst = worstCell(metrics)
	return r
}

func rankCell(c Context, sv model.Service, rank string) RowCell {
	cell := RowCell{Kind: CellRank, Align: "start", Rank: rank}

	switch {
	case sv.RankMovement > 0:
		cell.RankMove = c.Icons.Icon("rank.up")
		cell.RankMoveLabel = c.Text.Text("lb.rank.up", map[string]any{"n": int(sv.RankMovement)})
	case sv.RankMovement < 0:
		cell.RankMove = c.Icons.Icon("rank.down")
		cell.RankMoveLabel = c.Text.Text("lb.rank.down", map[string]any{"n": int(-sv.RankMovement)})
	default:
		cell.RankMove = c.Icons.Icon("rank.same")
		cell.RankMoveLabel = c.Text.Text("lb.rank.same", nil)
	}
	return cell
}

func buildCell(c Context, sv model.Service, m config.Metric) Cell {
	st := c.View.Standing(sv)

	cell := Cell{}
	if m.Field == config.FieldAvailability && !st.Availability.Valid {
		// "Not reported" and "zero" are different claims, and only one of them
		// is true here.
		cell.Unknown = true
		cell.Note = c.Text.Text("metric.noReference", nil)
		return cell
	}

	cell.Value = formatMetric(c, m, st)
	if m.Target != nil {
		cell.Target = c.Text.Text("metric.target", map[string]any{
			"v": formatValue(c, m, *m.Target),
		})
	}

	tr := rules.Trend(sv.History, m.Field, c.View.Days)
	if tr.Direction != model.DirectionFlat {
		cell.Trend = c.Text.Text("metric.trend", map[string]any{
			"delta": c.Text.Number(tr.Delta, m.Precision),
			"days":  int(tr.PeriodDays),
		})
		cell.TrendIcon = c.Icons.Icon("trend." + string(tr.Direction))
		// Whether a rise is good news depends on the metric, so the colour
		// comes from its configured direction rather than from the arrow.
		cell.TrendTone = trendTone(m.Direction, tr.Direction)
	}
	return cell
}

func trendTone(metricDirection string, moved model.Direction) string {
	switch metricDirection {
	case config.DirectionHigherIsBetter:
		if moved == model.DirectionUp {
			return "ok"
		}
		return "major"
	case config.DirectionLowerIsBetter:
		if moved == model.DirectionDown {
			return "ok"
		}
		return "major"
	default:
		return "neutral"
	}
}

func formatMetric(c Context, m config.Metric, st query.Standing) string {
	switch m.Field {
	case config.FieldAvailability:
		return formatValue(c, m, st.Availability.Value)
	case config.FieldErrorRate:
		return formatValue(c, m, st.ErrorRate)
	case config.FieldLatencyP50:
		return formatValue(c, m, float64(st.LatencyP50))
	case config.FieldVolume:
		return formatValue(c, m, float64(st.Volume))
	default:
		return ""
	}
}

func formatValue(c Context, m config.Metric, v float64) string {
	switch m.Unit {
	case config.UnitPercent:
		return c.Text.Percent(v, m.Precision)
	case config.UnitMillisecond:
		return c.Text.Unit(v, "millisecond", m.Precision)
	default:
		return c.Text.Number(v, m.Precision)
	}
}

func worstCell(cells []Cell) Cell {
	for _, c := range cells {
		if c.Unknown || c.TrendTone == "major" {
			return c
		}
	}
	if len(cells) > 0 {
		return cells[0]
	}
	return Cell{}
}

func categoryIcon(d config.Domain, id string) string {
	for _, c := range d.Taxonomy.Categories {
		if c.ID == id {
			return c.IconKey
		}
	}
	return ""
}

// --- stat tile -------------------------------------------------------------

// StatTileView is one headline figure in the drawer.
type StatTileView struct {
	Label string
	Cell  Cell
	Frame string
}

func statTileDef() Definition {
	schema := OptionSchema{}
	return Definition{
		Type: "stat-tile", Template: "stat-tile", Schema: schema,
		Sources: []string{SourceServiceMetric},
		Doc:     "One metric for the open service, with its target and trend.",
		Build: func(c Context, _ Options) (any, error) {
			if c.Service == nil {
				return StatTileView{}, nil
			}
			m := metricByID(c.Config.Domain, c.Bind.Metric)
			if m == nil {
				return StatTileView{}, nil
			}

			v := StatTileView{
				Label: c.Text.Text(m.TermID, nil),
				Cell:  buildCell(c, *c.Service, *m),
			}
			// A count with no target earns its place by framing another figure:
			// "2,863,170 of 2,900,000 succeeded".
			if m.Framing == "denominator" {
				vol := c.Service.Metrics.Volume
				v.Frame = c.Text.Text("metric.volumeOf", map[string]any{
					"success": c.Text.Number(float64(vol.Success), 0),
					"total":   c.Text.Number(float64(vol.Total), 0),
				})
			}
			return v, nil
		},
	}
}

// --- sparkline -------------------------------------------------------------

// SparklineView is the availability trace.
type SparklineView struct {
	chart.Sparkline
	ViewBox  string
	MinLabel string
	MaxLabel string
	Summary  string
	Days     int

	// PointData is the plotted positions as "x,y;x,y", for the crosshair.
	//
	// Sent alongside the path so the browser can find the nearest point without
	// re-deriving the scale, and far smaller than the data it came from — the
	// alternative is shipping ninety days of history to draw one dot.
	PointData string
}

func sparklineDef() Definition {
	schema := OptionSchema{
		"summaryTermId": {Kind: KindString, Doc: "Term describing the chart, for screen readers."},
	}
	return Definition{
		Type: "sparkline", Template: "sparkline", Schema: schema,
		Sources: []string{SourceServiceHistory},
		Doc:     "A line chart of one measurement over the window.",
		Build: func(c Context, o Options) (any, error) {
			if c.Service == nil {
				return SparklineView{Sparkline: chart.Sparkline{Empty: true}}, nil
			}
			days := c.Bind.Days
			if days == 0 {
				days = c.View.Days
			}
			history := lastN(c.Service.History, days)
			s := chart.Spark(history, chart.DefaultSparkOptions(), chart.SparkViewport)

			v := SparklineView{
				Sparkline: s,
				ViewBox: fmt.Sprintf("0 0 %g %g",
					chart.SparkViewport.Width, chart.SparkViewport.Height),
				Days: days,
			}
			if !s.Empty {
				v.MinLabel = c.Text.Percent(s.Min, 2)
				v.MaxLabel = c.Text.Percent(s.Max, 2)
				v.PointData = encodePoints(s.Points)
			}
			if term := o.String(schema, "summaryTermId"); term != "" {
				v.Summary = c.Text.Text(term, map[string]any{
					"min": v.MinLabel, "max": v.MaxLabel, "days": days,
				})
			}
			return v, nil
		},
	}
}

func lastN(h []model.HistoryPoint, n int) []model.HistoryPoint {
	if n <= 0 || len(h) <= n {
		return h
	}
	return h[len(h)-n:]
}

// --- bar chart -------------------------------------------------------------

// BarChartView is daily traffic with an error overlay.
type BarChartView struct {
	chart.BarChart
	ViewBox      string
	PeakText     string
	LowText      string
	Days         int
	OverlayLabel string
	PointData    string
}

func barChartDef() Definition {
	schema := OptionSchema{
		"peakTermId":    {Kind: KindString, Doc: "Term receiving {v} and {d}."},
		"lowTermId":     {Kind: KindString, Doc: "Term receiving {v} and {d}."},
		"overlayTermId": {Kind: KindString, Doc: "Label for the overlaid line."},
	}
	return Definition{
		Type: "bar-chart", Template: "bar-chart", Schema: schema,
		Sources: []string{SourceServiceHistory},
		Doc:     "Daily volume with a second measurement drawn over it.",
		Build: func(c Context, o Options) (any, error) {
			if c.Service == nil {
				return BarChartView{BarChart: chart.BarChart{Empty: true}}, nil
			}
			days := c.Bind.Days
			if days == 0 {
				days = c.View.Days
			}
			opts := chart.DefaultBarOptions()
			opts.Days = days

			b := chart.Bars(c.Service.History, opts, chart.TrafficViewport)
			v := BarChartView{
				BarChart: b,
				ViewBox: fmt.Sprintf("0 0 %g %g",
					chart.TrafficViewport.Width, chart.TrafficViewport.Height),
				Days: days,
			}
			if !b.Empty {
				v.PointData = encodePoints(b.Points)
				// {v} and {d}, which is what the shipped terms declare. Passing
				// "value" and "date" substituted nothing at all, so the label
				// read "Peak · " in every locale — a placeholder that resolves
				// to empty leaves no trace to notice.
				if term := o.String(schema, "peakTermId"); term != "" {
					v.PeakText = c.Text.Text(term, map[string]any{
						"v": c.Text.Number(float64(b.Max), 0),
						"d": c.Text.Date(b.Peak.Day),
					})
				}
				if term := o.String(schema, "lowTermId"); term != "" {
					v.LowText = c.Text.Text(term, map[string]any{
						"v": c.Text.Number(float64(b.Min), 0),
						"d": c.Text.Date(b.Low.Day),
					})
				}
			}
			if term := o.String(schema, "overlayTermId"); term != "" {
				v.OverlayLabel = c.Text.Text(term, nil)
			}
			return v, nil
		},
	}
}

// --- bar list --------------------------------------------------------------

// BarRow is one proportional row of the error breakdown.
type BarRow struct {
	Code    string
	Meaning string
	Tone    string
	Count   string
	Share   string
	Percent float64
	Trend   Icon
}

// BarListView is the error breakdown.
type BarListView struct {
	Rows  []BarRow
	Empty bool
	Total string
}

func barListDef() Definition {
	schema := OptionSchema{
		"limit": {Kind: KindInt, Default: 7, Doc: "How many rows to show."},
	}
	return Definition{
		Type: "bar-list", Template: "bar-list", Schema: schema,
		Sources: []string{SourceServiceErrors},
		Doc:     "A proportional breakdown, largest first.",
		Build: func(c Context, o Options) (any, error) {
			if c.Service == nil || len(c.Service.Errors) == 0 {
				return BarListView{Empty: true}, nil
			}

			buckets := c.Service.Errors
			if limit := o.Int(schema, "limit"); limit > 0 && len(buckets) > limit {
				buckets = buckets[:limit]
			}

			counts := make([]int64, len(buckets))
			var total int64
			for i, b := range buckets {
				counts[i] = b.Count
				total += b.Count
			}
			shares := chart.Shares(counts)

			v := BarListView{Total: c.Text.Number(float64(total), 0)}
			for i, b := range buckets {
				v.Rows = append(v.Rows, BarRow{
					Code:    b.Code,
					Meaning: c.Text.Text(b.TermID, nil),
					Tone:    errorTone(b.Class),
					Count:   c.Text.Number(float64(b.Count), 0),
					Share:   c.Text.Percent(b.Share, 1),
					Percent: shares[i].Percent,
					Trend:   c.Icons.Icon("trend." + string(b.Trend)),
				})
			}
			return v, nil
		},
	}
}

// errorTone colours a code by who is at fault, which is what the reader is
// trying to work out.
func errorTone(class model.ErrorClass) string {
	switch class {
	case model.ErrorClassServer:
		return "major"
	case model.ErrorClassClient:
		return "partial"
	default:
		return "unknown"
	}
}

// --- timeline --------------------------------------------------------------

// TimelineEvent is one stage of an incident.
type TimelineEvent struct {
	Label string
	When  string
	ISO   string
	Last  bool
}

// TimelineIncident is one incident with its stages.
type TimelineIncident struct {
	ID       string
	Severity string
	Tone     string
	Icon     Icon
	Note     string
	Open     bool
	Duration string
	Events   []TimelineEvent
}

// TimelineView is the incident history.
type TimelineView struct {
	Incidents []TimelineIncident
	Empty     bool
	EmptyText string
	EmptyIcon Icon
}

func timelineDef() Definition {
	schema := OptionSchema{
		"emptyTermId":   {Kind: KindString, Doc: "Term shown when there are no incidents."},
		"emptyIconKey":  {Kind: KindString, Doc: "Icon shown when there are no incidents."},
		"ongoingTermId": {Kind: KindString, Doc: "Term receiving {duration} for an open incident."},
	}
	return Definition{
		Type: "timeline", Template: "timeline", Schema: schema,
		Sources: []string{SourceServiceIncidents},
		Doc:     "Incidents with their stages, newest first.",
		Build: func(c Context, o Options) (any, error) {
			v := TimelineView{}
			if c.Service == nil || len(c.Service.Incidents) == 0 {
				v.Empty = true
				if term := o.String(schema, "emptyTermId"); term != "" {
					v.EmptyText = c.Text.Text(term, nil)
				}
				v.EmptyIcon = c.Icons.Icon(o.String(schema, "emptyIconKey"))
				return v, nil
			}

			sm := c.Config.Domain.StatusModel
			for _, inc := range c.Service.Incidents {
				sev := string(inc.Severity)
				ti := TimelineIncident{
					ID:       inc.ID,
					Severity: sev,
					Tone:     tone(sev),
					Icon:     c.Icons.Icon(sm.IconKey[sev]),
					Note:     c.Text.Text(inc.NoteTermID, nil),
					Open:     inc.Open,
				}

				end := inc.ClosedAt
				if inc.Open {
					end = c.Now
				}
				if term := o.String(schema, "ongoingTermId"); term != "" {
					ti.Duration = c.Text.Text(term, map[string]any{
						"duration": c.Text.Duration(end.Sub(inc.OpenedAt)),
					})
				}

				for i, e := range inc.Events {
					ti.Events = append(ti.Events, TimelineEvent{
						// Stage names resolve as "inc.<type>", so a deployment
						// adds a stage by adding a locale key.
						Label: c.Text.Text("inc."+e.Type, nil),
						When:  c.Text.DateTime(e.At),
						ISO:   e.At.UTC().Format(time.RFC3339),
						Last:  i == len(inc.Events)-1,
					})
				}
				v.Incidents = append(v.Incidents, ti)
			}
			return v, nil
		},
	}
}

// --- data table ------------------------------------------------------------

// DataTableView is the plain-text table behind a chart.
//
// Every chart has one. A line drawn on a screen is not accessible to a screen
// reader, is not copyable, and is not checkable — the table is what makes the
// figure something a reader can actually use.
type DataTableView struct {
	Summary string
	Headers []string
	Rows    [][]string
	Empty   bool
}

func dataTableDef() Definition {
	schema := OptionSchema{
		"summaryTermId": {Kind: KindString, Doc: "Term for the disclosure summary."},
		"every":         {Kind: KindInt, Default: 1, Doc: "Sample every Nth row, to keep long series readable."},
	}
	return Definition{
		Type: "data-table", Template: "data-table", Schema: schema,
		Sources: []string{SourceServiceHistory, SourceServiceErrors},
		Doc:     "The numbers behind a chart, as text.",
		Build: func(c Context, o Options) (any, error) {
			v := DataTableView{}
			if term := o.String(schema, "summaryTermId"); term != "" {
				v.Summary = c.Text.Text(term, nil)
			}
			if c.Service == nil {
				v.Empty = true
				return v, nil
			}

			switch c.Bind.Source {
			case SourceServiceErrors:
				v.Headers = []string{
					c.Text.Text("dr.err.code", nil),
					c.Text.Text("dr.err.meaning", nil),
					c.Text.Text("dr.err.count", nil),
					c.Text.Text("dr.err.share", nil),
				}
				for _, b := range c.Service.Errors {
					v.Rows = append(v.Rows, []string{
						b.Code,
						c.Text.Text(b.TermID, nil),
						c.Text.Number(float64(b.Count), 0),
						c.Text.Percent(b.Share, 1),
					})
				}

			default:
				days := c.Bind.Days
				if days == 0 {
					days = c.View.Days
				}
				history := lastN(c.Service.History, days)

				v.Headers = []string{
					c.Text.Text("dr.day", nil),
					c.Text.Text("metric.volume", nil),
					c.Text.Text("metric.errorRate", nil),
				}
				every := max(1, o.Int(schema, "every"))
				for i, p := range history {
					// Always include the most recent row: sampling that dropped
					// today would be actively misleading.
					if i%every != 0 && i != len(history)-1 {
						continue
					}
					v.Rows = append(v.Rows, []string{
						c.Text.Date(p.Day),
						c.Text.Number(float64(p.Volume), 0),
						c.Text.Percent(p.ErrorRate, 2),
					})
				}
			}

			v.Empty = len(v.Rows) == 0
			return v, nil
		},
	}
}

// encodePoints renders plotted positions compactly for the browser.
//
// "x,y;x,y" rather than JSON: it is a third of the bytes, it appears once per
// chart in every drawer response, and the parsing side is a two-line split.
func encodePoints(points []chart.Point) string {
	var b strings.Builder
	for i, p := range points {
		if i > 0 {
			b.WriteByte(';')
		}
		b.WriteString(strconv.FormatFloat(p.X, 'f', -1, 64))
		b.WriteByte(',')
		b.WriteString(strconv.FormatFloat(p.Y, 'f', -1, 64))
	}
	return b.String()
}

// withDuration copies a parameter set with a formatted duration added, leaving
// the original untouched so a builder never mutates what rules produced.
func withDuration(params map[string]any, formatted string) map[string]any {
	out := make(map[string]any, len(params)+1)
	for k, v := range params {
		out[k] = v
	}
	out["duration"] = formatted
	return out
}

// --- prose -----------------------------------------------------------------

// ProseView is a block of configured body copy.
//
// Spans rather than a string, because the copy needs particular words picked out
// and a locale file must never be able to supply markup. internal/prose parses
// the closed vocabulary into these, and the template renders elements — so
// nothing config supplies ever reaches the browser as HTML.
type ProseView struct {
	Class      string
	Paragraphs [][]prose.Span
}

func proseDef() Definition {
	schema := OptionSchema{
		"termIds": {Kind: KindStringList, Required: true,
			Doc: "Term ids, one per paragraph, rendered in order."},
		"class": {Kind: KindString,
			Doc: "Extra class, e.g. \"lede\"."},
	}
	return Definition{
		Type: "prose", Template: "prose", Schema: schema,
		Doc: "Body copy from the locale files. Each term may use <strong>, <em> and " +
			"<mark tone=\"…\"> to pick out words; nothing else is allowed.",
		Validate: func(o Options, _ Bind, _ ValidationContext) []error {
			if len(o.StringList(schema, "termIds")) == 0 {
				return []error{errors.New("prose has no termIds, so it would render nothing")}
			}
			return nil
		},
		Build: func(c Context, o Options) (any, error) {
			v := ProseView{Class: o.String(schema, "class")}
			for _, id := range o.StringList(schema, "termIds") {
				spans, err := prose.Parse(c.Text.Text(id, nil))
				if err != nil {
					// Named so the reader knows which locale entry to fix, in a
					// file the error cannot point a line at.
					return nil, fmt.Errorf("term %q: %w", id, err)
				}
				v.Paragraphs = append(v.Paragraphs, spans)
			}
			return v, nil
		},
	}
}
