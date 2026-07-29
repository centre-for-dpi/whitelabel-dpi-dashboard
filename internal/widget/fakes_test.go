package widget_test

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/query"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/widget"
)

// The fakes below render their inputs rather than their outputs: a term comes
// back as its own id plus the parameters it was given. That makes assertions
// say what the widget *asked for*, which is the thing under test — a builder's
// job is to pick the right term and pass the right numbers, not to translate.

type text struct{}

func (text) Text(id string, params map[string]any) string {
	if len(params) == 0 {
		return id
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%v", k, params[k])
	}
	return id + "{" + strings.Join(parts, ",") + "}"
}

func (text) Number(v float64, precision int) string {
	return strconv.FormatFloat(v, 'f', precision, 64)
}
func (t text) Percent(v float64, precision int) string { return t.Number(v, precision) + "%" }
func (t text) Unit(v float64, unit string, precision int) string {
	return t.Number(v, precision) + " " + unit
}
func (text) Date(at time.Time) string     { return at.Format("2006-01-02") }
func (text) DateTime(at time.Time) string { return at.Format("2006-01-02 15:04") }
func (text) RelativeTime(at, now time.Time) string {
	return now.Sub(at).Round(time.Minute).String() + " ago"
}
func (text) Duration(d time.Duration) string { return d.Round(time.Minute).String() }
func (text) Direction() string               { return "ltr" }

type icons struct{}

func (icons) Icon(key string) widget.Icon {
	if key == "" {
		return widget.Icon{}
	}
	return widget.Icon{Glyph: "[" + key + "]", Label: key}
}

type labels struct{}

func (labels) Name(s model.Service) string { return s.NameTermID }
func (labels) Haystack(s model.Service) string {
	return s.NameTermID + " " + s.CategoryID + " " + s.RegionID
}
func (labels) Compare(a, b string) int { return strings.Compare(a, b) }

var now = time.Date(2026, time.July, 27, 15, 0, 0, 0, time.UTC)

func target(v float64) *float64 { return &v }

func domain() config.Domain {
	return config.Domain{
		DefaultScope:         "national",
		Scopes:               []string{"national", "state"},
		DefaultPeriod:        "30d",
		OnboardedDenominator: 812,
		Periods: []config.Period{
			{ID: "7d", TermID: "period.7d", Days: 7},
			{ID: "30d", TermID: "period.30d", Days: 30},
		},
		Taxonomy: config.Taxonomy{
			Categories: []config.Category{
				{ID: "cat.identity", TermID: "cat.identity", IconKey: "cat.identity"},
				{ID: "cat.money", TermID: "cat.money", IconKey: "cat.money"},
			},
			Regions: []config.Region{
				{ID: "reg.national", TermID: "reg.national", Scope: "national"},
				{ID: "reg.mh", TermID: "reg.mh", Scope: "state"},
			},
		},
		Metrics: []config.Metric{
			{ID: "metric.availability", TermID: "metric.availability", Field: config.FieldAvailability,
				Unit: config.UnitPercent, Precision: 2, Target: target(99.5),
				Direction: config.DirectionHigherIsBetter, ShowInLeaderboard: true},
			{ID: "metric.errorRate", TermID: "metric.errorRate", Field: config.FieldErrorRate,
				Unit: config.UnitPercent, Precision: 2, Target: target(1.0),
				Direction: config.DirectionLowerIsBetter, ShowInLeaderboard: true},
			{ID: "metric.latencyP50", TermID: "metric.latencyP50", Field: config.FieldLatencyP50,
				Unit: config.UnitMillisecond, Precision: 0, Target: target(300),
				Direction: config.DirectionLowerIsBetter},
			{ID: "metric.volume", TermID: "metric.volume", Field: config.FieldVolume,
				Unit: config.UnitCount, Precision: 0, Direction: config.DirectionNeutral,
				Framing: "denominator"},
		},
		Thresholds: config.Thresholds{
			EvaluationOrder: []string{"maintenance", "unknown", "major", "partial", "operational"},
			Values: config.ThresholdValues{
				MajorAvailBelow: 99.0, MajorErrAbove: 2.0,
				PartialAvailBelow: 99.5, PartialErrAbove: 1.0,
				StaleSecondsAbove: 900,
			},
			Prose: map[string]string{
				"maintenance": "rule.maintenance", "unknown": "rule.unknown",
				"major": "rule.major", "partial": "rule.partial", "operational": "rule.operational",
			},
		},
		StatusModel: config.StatusModel{
			Order:    config.Statuses,
			Severity: map[string]int{"major": 4, "partial": 3, "unknown": 2, "maintenance": 1, "operational": 0},
			IconKey: map[string]string{
				"operational": "status.operational", "partial": "status.partial",
				"major": "status.major", "unknown": "status.unknown", "maintenance": "status.maintenance",
			},
			LabelTermID: map[string]string{
				"operational": "status.operational", "partial": "status.partial",
				"major": "status.major", "unknown": "status.unknown", "maintenance": "status.maintenance",
			},
		},
		SignalsEmpty: config.SignalEmpty{TermID: "sig.empty", IconKey: "ui.check", Tone: "ok"},
	}
}

// svc builds a service with thirty days of flat history, so that a windowed
// figure and a current one agree unless a test deliberately varies them.
func svc(id string, opts ...func(*model.Service)) model.Service {
	s := model.Service{
		ID: id, Key: id, NameTermID: "svc." + id + ".name", DescTermID: "svc." + id + ".desc",
		CategoryID: "cat.identity", RegionID: "reg.national", Scope: "national",
		Status: model.StatusOperational,
		Metrics: model.Metrics{
			Availability: model.Float(99.9),
			ErrorRate:    0.1,
			LatencyP50:   200,
			Volume:       model.Volume{Total: 1000, Success: 999},
		},
		ObservedAt: now,
	}
	day := now.AddDate(0, 0, -30).Truncate(24 * time.Hour)
	for i := range 30 {
		s.History = append(s.History, model.HistoryPoint{
			Day:          day.AddDate(0, 0, i),
			Availability: model.Float(99.9),
			ErrorRate:    0.1,
			LatencyP50:   200,
			Volume:       1000,
		})
	}
	for _, o := range opts {
		o(&s)
	}
	return s
}

func status(v model.Status) func(*model.Service) {
	return func(s *model.Service) { s.Status = v }
}
func avail(v float64) func(*model.Service) {
	return func(s *model.Service) {
		s.Metrics.Availability = model.Float(v)
		for i := range s.History {
			s.History[i].Availability = model.Float(v)
		}
	}
}
func noAvail() func(*model.Service) {
	return func(s *model.Service) {
		s.Metrics.Availability = model.NoFloat()
		for i := range s.History {
			s.History[i].Availability = model.NoFloat()
		}
	}
}
func errRate(v float64) func(*model.Service) {
	return func(s *model.Service) {
		s.Metrics.ErrorRate = v
		for i := range s.History {
			s.History[i].ErrorRate = v
		}
	}
}
func category(id string) func(*model.Service) {
	return func(s *model.Service) { s.CategoryID = id }
}
func noHistory() func(*model.Service) {
	return func(s *model.Service) { s.History = nil }
}

// ctx assembles a Context the way the server will, so builders are exercised
// against the same shape they see in production.
func ctx(services []model.Service, mutate ...func(*widget.Context)) widget.Context {
	d := domain()
	v := query.View{Domain: d, Labeler: labels{}, Days: 30}

	scoped := v.Scope(services, "national", "")
	ranked := v.Rank(scoped)
	ranks := query.Ranks(ranked)
	filtered := v.Apply(scoped, query.Filter{})
	ordered := v.Order(filtered, query.SortRank, query.Asc, ranks)

	c := widget.Context{
		Config:   config.Config{Domain: d},
		View:     v,
		Snapshot: model.Snapshot{Services: services, GeneratedAt: now.Add(-4 * time.Minute)},
		Scoped:   scoped,
		// The board's own universe. The fixture declares one role, so it is
		// the same list as Scoped — which is exactly what a deployment
		// reporting one side of the exchange sees.
		RoleScoped: scoped,
		Filtered:   filtered,
		Ordered:    ordered,
		Ranks:      ranks,
		State: widget.State{
			Scope: "national", Period: "30d",
			Sort: query.SortRank, Dir: query.Asc,
		},
		Text:  text{},
		Icons: icons{},
		Now:   now,
	}
	for _, m := range mutate {
		m(&c)
	}
	return c
}

// build runs one widget and returns its view model.
func build[T any](t testingT, kind string, c widget.Context, opts widget.Options) T {
	t.Helper()
	got, err := widget.Default().Build(kind, c, opts)
	if err != nil {
		t.Fatalf("building %s: %v", kind, err)
	}
	v, ok := got.(T)
	if !ok {
		t.Fatalf("%s produced %T, want %T", kind, got, *new(T))
	}
	return v
}

// testingT is the slice of *testing.T the helpers above need, so build can be
// generic without dragging the whole interface in.
type testingT interface {
	Helper()
	Fatalf(format string, args ...any)
}
