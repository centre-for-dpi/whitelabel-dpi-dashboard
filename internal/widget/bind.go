package widget

import (
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/query"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/rules"
)

// Bind names the data a widget reads.
//
// The set of sources is closed and documented, not an expression language. A
// deployment composes a page from parts that are known to exist; it does not
// write queries. That keeps the engine type-safe, keeps layout.yaml's reference
// documentation finite, and means a bad binding is caught at startup with a
// line number rather than discovered as a blank panel in production.
type Bind struct {
	Source string `yaml:"source"`

	// Field selects which measurement, for sources that carry several.
	Field string `yaml:"field"`
	// Metric names a configured metric, for widgets that render one.
	Metric string `yaml:"metric"`
	// Overlay names a second measurement drawn on top of the first.
	Overlay string `yaml:"overlay"`
	// Days overrides the reader's selected window. Left unset — which is
	// almost always right — the widget follows the period control.
	Days int `yaml:"days"`
}

// The closed set of bind sources.
const (
	// Service lists.
	SourceServicesFiltered = "services.filtered"
	SourceServicesScoped   = "services.scoped"
	SourceServicesAll      = "services.all"

	// Aggregates over the scoped set.
	SourceStatusCounts = "aggregate.statusCounts"
	SourceCoverage     = "aggregate.coverage"
	SourceSignals      = "aggregate.signals"
	SourceUpdatedAt    = "aggregate.updatedAt"

	// The service a drawer is open on.
	SourceService          = "service.current"
	SourceServiceMetric    = "service.metric"
	SourceServiceHistory   = "service.history"
	SourceServiceErrors    = "service.errorBreakdown"
	SourceServiceIncidents = "service.incidents"
	// The demand side of the open service: who pulls it, and how that compares
	// with everything like it.
	SourceServiceDemand = "service.demand"
	SourceServicePeers  = "service.peers"

	// Configuration, for widgets that describe the rules rather than the data.
	SourceConfigThresholdProse = "config.thresholdProse"
	SourceConfigMetrics        = "config.metrics"
	SourceConfigCategories     = "config.categories"
	SourceConfigRegions        = "config.regions"
	SourceConfigPeriods        = "config.periods"
	SourceConfigStatuses       = "config.statuses"
)

// Sources lists every valid bind source, for validation and documentation.
func Sources() []string {
	return []string{
		SourceServicesFiltered,
		SourceServicesScoped,
		SourceServicesAll,
		SourceStatusCounts,
		SourceCoverage,
		SourceSignals,
		SourceUpdatedAt,
		SourceService,
		SourceServiceMetric,
		SourceServiceHistory,
		SourceServiceErrors,
		SourceServiceIncidents,
		SourceServiceDemand,
		SourceServicePeers,
		SourceConfigThresholdProse,
		SourceConfigMetrics,
		SourceConfigCategories,
		SourceConfigRegions,
		SourceConfigPeriods,
		SourceConfigStatuses,
	}
}

// serviceScoped reports whether a source only makes sense inside the drawer.
//
// Binding one of these into a page section is a layout mistake that would
// otherwise render as a permanently empty panel, so it is caught at startup.
func serviceScoped(source string) bool {
	switch source {
	case SourceService, SourceServiceMetric, SourceServiceHistory,
		SourceServiceErrors, SourceServiceIncidents,
		SourceServiceDemand, SourceServicePeers:
		return true
	}
	return false
}

// measuredFields are the fields something actually reports.
func measuredFields() []string {
	return []string{
		config.FieldAvailability, config.FieldErrorRate,
		config.FieldLatencyP50, config.FieldVolume,
	}
}

// ValidateBind checks a binding against the closed source set and the
// deployment's own configuration.
func ValidateBind(b Bind, d config.Domain, inDrawer bool) []error {
	var errs []error

	if b.Source == "" {
		return []error{fmt.Errorf("no bind source; expected one of %v", Sources())}
	}
	if !slices.Contains(Sources(), b.Source) {
		return []error{fmt.Errorf("unknown bind source %q; expected one of %v", b.Source, Sources())}
	}
	if serviceScoped(b.Source) && !inDrawer {
		errs = append(errs, fmt.Errorf(
			"bind source %q reads the service a drawer is open on, so it cannot be used in a page section", b.Source))
	}

	// Downtime is plottable though nothing reports it: it is availability's
	// complement, derived at the point of drawing. An overlay cannot be it,
	// because an overlay is drawn against the series beneath and a complement
	// of that series carries no information the series does not.
	if b.Field != "" && !slices.Contains(append(measuredFields(), config.FieldDowntime), b.Field) {
		errs = append(errs, fmt.Errorf("unknown field %q; expected one of %v",
			b.Field, append(measuredFields(), config.FieldDowntime)))
	}
	if b.Overlay != "" && !slices.Contains(measuredFields(), b.Overlay) {
		errs = append(errs, fmt.Errorf("unknown overlay field %q; expected one of %v",
			b.Overlay, measuredFields()))
	}

	if b.Metric != "" {
		var ids []string
		for _, m := range d.Metrics {
			ids = append(ids, m.ID)
		}
		if !slices.Contains(ids, b.Metric) {
			errs = append(errs, fmt.Errorf("unknown metric %q; this deployment declares %v", b.Metric, ids))
		}
	}

	if b.Days < 0 {
		errs = append(errs, fmt.Errorf("days is %d; a window cannot be negative", b.Days))
	}

	return errs
}

// Context is everything a builder may read.
//
// It is plain data with no methods that reach outside it, which is what keeps
// every builder a pure function and every widget testable without a server, a
// clock or a database.
type Context struct {
	Config   config.Config
	View     query.View
	Snapshot model.Snapshot

	// The service list at each stage of narrowing. Widgets bind to whichever
	// stage answers their question: a count of statuses is over the scoped set,
	// while the table shows what survived the filter.
	//
	// Scoped is deliberately role-agnostic — it is the deployment's default
	// role, narrowed by scope. The verdict asks whether the country's services
	// are working, and that question does not change because the reader has
	// switched the board to look at who is calling them. RoleScoped is the
	// board's own universe, and everything that qualifies the board — its
	// ranking, its filter counts, its "showing 12 of 34" — reads that.
	Scoped     []model.Service
	RoleScoped []model.Service
	// Demand is every service in scope that calls another, whichever role it
	// carries. The opportunity rules are findings about supply and demand
	// together, so they need both sides regardless of which one is on the
	// board.
	Demand   []model.Service
	Filtered []model.Service
	Ordered  []model.Service
	Ranks    map[string]int

	// Service is set when a drawer is open.
	Service *model.Service

	// Bind is the widget's own binding, so a builder can read the metric,
	// field or window its layout entry named.
	Bind Bind

	State State
	// Params is State as it arrived: the known query parameters this request
	// carried, and nothing else. State is the validated reading of them, which is
	// what a builder decides with; Params is what a builder must hand on, because
	// a link built from defaults silently discards whatever the reader chose that
	// the link's own control does not name.
	Params url.Values
	Text   TextResolver
	Icons  IconResolver
	Now    time.Time
}

// State is what the reader has selected. It round-trips through the URL, so it
// is also what a shared link carries.
type State struct {
	Scope string
	// Role is which side of the exchange is on view: who issues, or who
	// requests. It narrows the population the same way Scope does.
	Role       string
	Region     string
	Period     string
	Search     string
	Sort       string
	Dir        string
	Locale     string
	Theme      string
	Statuses   []string
	Categories []string
	// IDs narrows the board to an explicit set of services. Set by a signal
	// card whose finding is about a set with nothing else in common — no shared
	// status, no shared category — where the only honest filter is the set.
	IDs []string
	// SignalTab is which set of findings is on view: what needs attention, or
	// where the opportunities are.
	SignalTab string
	DrawerID  string
	DrawerTab string
	// FiltersOpen carries whether the narrow-screen filter panel is showing. It
	// is reader state like any other, so it rides in the URL: the panel used to
	// re-collapse on every filter change, because the fragment that replaced it
	// carried a hardcoded aria-expanded="false".
	FiltersOpen bool
}

// TextResolver turns a term id into displayable text.
//
// A term id with no translation falls through as itself, so a deployment that
// does not translate can put literal text in its config and have it work.
type TextResolver interface {
	Text(termID string, params map[string]any) string
	Number(v float64, precision int) string
	Percent(v float64, precision int) string
	Unit(v float64, unit string, precision int) string
	Date(t time.Time) string
	DateTime(t time.Time) string
	RelativeTime(t time.Time, now time.Time) string
	Duration(d time.Duration) string
	Direction() string
}

// IconResolver turns a semantic icon key into what the template renders.
type IconResolver interface {
	Icon(key string) Icon
}

// Icon is a resolved icon, ready to render.
type Icon struct {
	Glyph string
	SVG   string
	Label string
}

// Signals evaluates the configured signal rules over the scoped set. It is here
// rather than in the builder so that both the signal widget and any future
// consumer see the same findings.
func (c Context) Signals() []rules.Signal {
	// One source, two sets: which findings are on view is a reader selection
	// like any other, so the binding says "the signals" and the tab decides
	// which ones those are — the same way services.filtered means "the ones
	// the filters left".
	if c.State.SignalTab == query.SignalsOpportunity {
		return rules.Opportunities(c.Config.Domain, c.Scoped, c.Demand, c.Now)
	}
	return rules.Signals(c.Config.Domain, c.Scoped, c.Now)
}
