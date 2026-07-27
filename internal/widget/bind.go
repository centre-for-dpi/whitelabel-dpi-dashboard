package widget

import (
	"fmt"
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
		SourceServiceErrors, SourceServiceIncidents:
		return true
	}
	return false
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

	if b.Field != "" {
		fields := []string{
			config.FieldAvailability, config.FieldErrorRate,
			config.FieldLatencyP50, config.FieldVolume,
		}
		if !slices.Contains(fields, b.Field) {
			errs = append(errs, fmt.Errorf("unknown field %q; expected one of %v", b.Field, fields))
		}
	}
	if b.Overlay != "" {
		fields := []string{
			config.FieldAvailability, config.FieldErrorRate,
			config.FieldLatencyP50, config.FieldVolume,
		}
		if !slices.Contains(fields, b.Overlay) {
			errs = append(errs, fmt.Errorf("unknown overlay field %q; expected one of %v", b.Overlay, fields))
		}
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
	Scoped   []model.Service
	Filtered []model.Service
	Ordered  []model.Service
	Ranks    map[string]int

	// Service is set when a drawer is open.
	Service *model.Service

	// Bind is the widget's own binding, so a builder can read the metric,
	// field or window its layout entry named.
	Bind Bind

	State State
	Text  TextResolver
	Icons IconResolver
	Now   time.Time
}

// State is what the reader has selected. It round-trips through the URL, so it
// is also what a shared link carries.
type State struct {
	Scope      string
	Region     string
	Period     string
	Search     string
	Sort       string
	Dir        string
	Locale     string
	Theme      string
	Statuses   []string
	Categories []string
	DrawerID   string
	DrawerTab  string
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
	return rules.Signals(c.Config.Domain, c.Scoped, c.Now)
}
