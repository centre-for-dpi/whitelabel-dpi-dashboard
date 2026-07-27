// Package mapping turns an upstream document into services.
//
// This is where "integrating your API" actually happens, and the whole design
// goal is that it happens in YAML:
//
//	itemsPath: $.data.services
//	map:
//	  id:                   $.serviceId
//	  metrics.availability: $.sla.uptimePct
//	transform:
//	  metrics.availability: [{ fn: ratioToPercent }]
//
// Two principles run through it. Absent is not zero — a field the upstream did
// not report leaves the model's zero value rather than being filled in, because
// the difference between "we cannot tell" and "it is down" is the whole point
// of the status rules. And a bad record does not sink the batch: it is reported
// and skipped, so one malformed service does not empty the dashboard.
package mapping

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/fieldpath"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/transform"
)

// Spec is a mapping as written in config.
type Spec struct {
	// ItemsPath selects the array of records. Empty means the document is
	// itself the array.
	ItemsPath string `yaml:"itemsPath"`

	// Map is field name to path into each record.
	Map map[string]string `yaml:"map"`

	// Transform is field name to the chain applied after reading it.
	Transform map[string][]transform.Spec `yaml:"transform"`

	// History maps the nested daily series, if the upstream offers one.
	History *HistorySpec `yaml:"history"`
}

// HistorySpec maps a nested series of daily points.
type HistorySpec struct {
	Path         string `yaml:"path"`
	Date         string `yaml:"date"`
	Availability string `yaml:"availability"`
	ErrorRate    string `yaml:"errorRate"`
	LatencyP50   string `yaml:"latencyP50"`
	Volume       string `yaml:"volume"`
}

// Fields lists every mappable field, for validation and documentation.
//
// A closed list, so a typo in a mapping is caught at startup naming the field,
// rather than silently mapping nothing.
func Fields() []string {
	return []string{
		"id", "key", "name", "description",
		"categoryId", "regionId", "providerId", "scope",
		"metrics.availability", "metrics.errorRate", "metrics.latencyP50",
		"metrics.staleSeconds", "metrics.volume.total", "metrics.volume.success",
		"maintenance.active", "maintenance.until", "maintenance.reason",
		"observedAt",

		// The daily series needs the same conversions as the current reading.
		// An upstream reporting ratios reports them in both places, and
		// transforming only one would make the windowed figures disagree with
		// the instantaneous ones — which is exactly the incoherence the
		// windowed leaderboard exists to avoid.
		"history.availability", "history.errorRate",
		"history.latencyP50", "history.volume",
	}
}

// Mapper is a compiled spec, ready to apply.
type Mapper struct {
	items      fieldpath.Path
	fields     map[string]fieldpath.Path
	transforms map[string][]transform.Spec
	history    *compiledHistory
}

type compiledHistory struct {
	path         fieldpath.Path
	date         fieldpath.Path
	availability fieldpath.Path
	errorRate    fieldpath.Path
	latency      fieldpath.Path
	volume       fieldpath.Path
}

// Compile prepares a spec, reporting every problem at once.
//
// Paths and transforms are compiled here rather than per request, so a mapping
// is validated when the dashboard starts and costs nothing on each poll.
func Compile(s Spec) (*Mapper, error) {
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	m := &Mapper{
		fields:     map[string]fieldpath.Path{},
		transforms: s.Transform,
	}

	if s.ItemsPath != "" {
		p, err := fieldpath.Parse(s.ItemsPath)
		if err != nil {
			add("itemsPath: %v", err)
		}
		m.items = p
	}

	if len(s.Map) == 0 {
		add("the mapping is empty; at least `id` must be mapped")
	}
	for _, field := range sortedKeys(s.Map) {
		if !slices.Contains(Fields(), field) {
			add("unknown field %q; expected one of %v", field, Fields())
			continue
		}
		p, err := fieldpath.Parse(s.Map[field])
		if err != nil {
			add("%s: %v", field, err)
			continue
		}
		m.fields[field] = p
	}
	if _, ok := m.fields["id"]; !ok && len(s.Map) > 0 {
		// Without an id there is nothing to upsert against and nothing to link
		// to; every record would collide with every other.
		add("`id` is not mapped, so services cannot be told apart")
	}

	for _, field := range sortedTransformKeys(s.Transform) {
		if !slices.Contains(Fields(), field) {
			add("transform for unknown field %q", field)
			continue
		}
		for _, e := range transform.Validate(s.Transform[field]) {
			add("transform %s: %v", field, e)
		}
	}

	if s.History != nil {
		h, hErrs := compileHistory(*s.History)
		for _, e := range hErrs {
			add("history: %v", e)
		}
		m.history = h
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return m, nil
}

func compileHistory(s HistorySpec) (*compiledHistory, []error) {
	var errs []error
	parse := func(name, expr string) fieldpath.Path {
		p, err := fieldpath.Parse(expr)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
		return p
	}

	h := &compiledHistory{
		path:         parse("path", s.Path),
		date:         parse("date", s.Date),
		availability: parse("availability", s.Availability),
		errorRate:    parse("errorRate", s.ErrorRate),
		latency:      parse("latencyP50", s.LatencyP50),
		volume:       parse("volume", s.Volume),
	}
	if h.path.Empty() {
		errs = append(errs, fmt.Errorf("no path to the daily series"))
	}
	if h.date.Empty() {
		// Without a date the points cannot be ordered or bucketed, and an
		// unordered chart is worse than no chart.
		errs = append(errs, fmt.Errorf("no date path; daily points cannot be placed in time"))
	}
	return h, errs
}

// Result is what one document produced.
type Result struct {
	Services []model.Service
	// Skipped names the records that could not be read, so a partial upstream
	// failure is visible rather than silently shrinking the dashboard.
	Skipped []Skip
}

// Skip is one record that could not be mapped.
type Skip struct {
	Index  int
	Reason string
}

// Apply maps a decoded document.
func (m *Mapper) Apply(doc any) Result {
	var out Result

	for i, record := range m.records(doc) {
		sv, err := m.service(record)
		if err != nil {
			// One malformed record must not empty the dashboard.
			out.Skipped = append(out.Skipped, Skip{Index: i, Reason: err.Error()})
			continue
		}
		out.Services = append(out.Services, sv)
	}
	return out
}

func (m *Mapper) records(doc any) []any {
	if m.items.Empty() {
		// No itemsPath means the document is itself the array.
		if arr, ok := doc.([]any); ok {
			return arr
		}
		return nil
	}

	values := m.items.All(doc)
	// A path selecting the array itself yields one value which is that array.
	if len(values) == 1 {
		if arr, ok := values[0].([]any); ok {
			return arr
		}
	}
	return values
}

// read pulls one field out of a record and runs its transforms.
func (m *Mapper) read(record any, field string) transform.Value {
	p, ok := m.fields[field]
	if !ok {
		return transform.Absent()
	}
	raw, found := p.Get(record)
	if !found {
		// Still run the chain: `default` exists precisely to fill an absence.
		return transform.Apply(transform.Absent(), m.transforms[field])
	}
	return transform.Apply(transform.Of(raw), m.transforms[field])
}

func (m *Mapper) service(record any) (model.Service, error) {
	id, ok := m.read(record, "id").Text()
	if !ok || strings.TrimSpace(id) == "" {
		return model.Service{}, fmt.Errorf("no id")
	}

	sv := model.Service{ID: strings.TrimSpace(id)}

	// Every assignment below is guarded, so a field the upstream did not report
	// leaves the model's zero value rather than being filled with one.
	setText(&sv.Key, m.read(record, "key"))
	setText(&sv.NameTermID, m.read(record, "name"))
	setText(&sv.DescTermID, m.read(record, "description"))
	setText(&sv.CategoryID, m.read(record, "categoryId"))
	setText(&sv.RegionID, m.read(record, "regionId"))
	setText(&sv.ProviderID, m.read(record, "providerId"))
	setText(&sv.Scope, m.read(record, "scope"))

	if v, ok := m.read(record, "metrics.availability").Number(); ok {
		sv.Metrics.Availability = model.Float(v)
	}
	setNumber(&sv.Metrics.ErrorRate, m.read(record, "metrics.errorRate"))
	setInt32(&sv.Metrics.LatencyP50, m.read(record, "metrics.latencyP50"))
	setInt64(&sv.Metrics.StaleSeconds, m.read(record, "metrics.staleSeconds"))
	setInt64(&sv.Metrics.Volume.Total, m.read(record, "metrics.volume.total"))
	setInt64(&sv.Metrics.Volume.Success, m.read(record, "metrics.volume.success"))

	if v, ok := m.read(record, "maintenance.active").Bool(); ok {
		sv.Maintenance.Active = v
	}
	if v, ok := m.read(record, "maintenance.until").Time(); ok {
		sv.Maintenance.Until = v
	}
	setText(&sv.Maintenance.ReasonTermID, m.read(record, "maintenance.reason"))

	if v, ok := m.read(record, "observedAt").Time(); ok {
		sv.ObservedAt = v
	}

	// A key defaults to the id, since it is only ever a shorter handle for the
	// same thing and an upstream is unlikely to send both.
	if sv.Key == "" {
		sv.Key = sv.ID
	}

	sv.History = m.readHistory(record)
	return sv, nil
}

func (m *Mapper) readHistory(record any) []model.HistoryPoint {
	if m.history == nil {
		return nil
	}

	points := m.history.path.All(record)
	out := make([]model.HistoryPoint, 0, len(points))

	for _, node := range points {
		day, ok := transform.Of(first(m.history.date, node)).Time()
		if !ok {
			// A point with no readable date cannot be placed in time, and an
			// unordered chart is worse than a shorter one.
			continue
		}

		p := model.HistoryPoint{Day: day.UTC().Truncate(24 * time.Hour)}
		if v, ok := m.historyValue(node, m.history.availability, "history.availability").Number(); ok {
			p.Availability = model.Float(v)
		}
		setNumber(&p.ErrorRate, m.historyValue(node, m.history.errorRate, "history.errorRate"))
		setInt32(&p.LatencyP50, m.historyValue(node, m.history.latency, "history.latencyP50"))
		setInt64(&p.Volume, m.historyValue(node, m.history.volume, "history.volume"))
		out = append(out, p)
	}

	// Oldest first, which is the order the charts plot regardless of how the
	// upstream happened to sort them.
	slices.SortStableFunc(out, func(a, b model.HistoryPoint) int { return a.Day.Compare(b.Day) })
	return out
}

// historyValue reads one field of a daily point and runs its transforms.
func (m *Mapper) historyValue(node any, p fieldpath.Path, field string) transform.Value {
	return transform.Apply(transform.Of(first(p, node)), m.transforms[field])
}

func first(p fieldpath.Path, node any) any {
	if p.Empty() {
		return nil
	}
	v, ok := p.Get(node)
	if !ok {
		return nil
	}
	return v
}

func setText(dst *string, v transform.Value) {
	if s, ok := v.Text(); ok {
		*dst = s
	}
}

func setNumber(dst *float64, v transform.Value) {
	if n, ok := v.Number(); ok {
		*dst = n
	}
}

func setInt32(dst *int32, v transform.Value) {
	if n, ok := v.Number(); ok {
		*dst = int32(n)
	}
}

func setInt64(dst *int64, v transform.Value) {
	if n, ok := v.Number(); ok {
		*dst = int64(n)
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func sortedTransformKeys(m map[string][]transform.Spec) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
