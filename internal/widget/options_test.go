package widget_test

import (
	"strings"
	"testing"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/widget"
)

var schema = widget.OptionSchema{
	"name":    {Kind: widget.KindString, Required: true, Doc: "d"},
	"mode":    {Kind: widget.KindString, Enum: []string{"a", "b"}, Default: "a", Doc: "d"},
	"count":   {Kind: widget.KindInt, Default: 7, Doc: "d"},
	"enabled": {Kind: widget.KindBool, Default: true, Doc: "d"},
	"list":    {Kind: widget.KindStringList, Default: []string{"x"}, Doc: "d"},
	"limited": {Kind: widget.KindStringList, Enum: []string{"p", "q"}, Doc: "d"},
}

func messages(errs []error) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "\n")
}

func TestSchemaAcceptsWhatItDeclares(t *testing.T) {
	errs := schema.Validate(widget.Options{
		"name": "x", "mode": "b", "count": 3, "enabled": false,
		"list": []any{"one", "two"}, "limited": []any{"p"},
	})
	if len(errs) != 0 {
		t.Errorf("a valid option set was rejected:\n%s", messages(errs))
	}
}

func TestSchemaRejectsWhatItDoesNot(t *testing.T) {
	tests := []struct {
		name string
		opts widget.Options
		want string
	}{
		{
			// Same reasoning as the config loader: a silently ignored setting
			// leaves the reader wondering why their edit did nothing.
			"unknown option", widget.Options{"name": "x", "colour": "red"}, "unknown option",
		},
		{"missing required", widget.Options{}, `option "name" is required`},
		{"string given a number", widget.Options{"name": 3}, "must be text"},
		{"int given text", widget.Options{"name": "x", "count": "many"}, "whole number"},
		{"bool given text", widget.Options{"name": "x", "enabled": "yes"}, "true or false"},
		{"list given text", widget.Options{"name": "x", "list": "one"}, "list of text"},
		{"list of non-text", widget.Options{"name": "x", "list": []any{1, 2}}, "list of text"},
		{"value outside an enum", widget.Options{"name": "x", "mode": "c"}, `is "c"`},
		{"list value outside an enum", widget.Options{"name": "x", "limited": []any{"p", "z"}}, `contains "z"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errs := schema.Validate(tc.opts)
			if len(errs) == 0 {
				t.Fatalf("accepted %v", tc.opts)
			}
			if !strings.Contains(messages(errs), tc.want) {
				t.Errorf("errors do not mention %q:\n%s", tc.want, messages(errs))
			}
		})
	}
}

func TestSchemaErrorsAreOrderedStably(t *testing.T) {
	// Map iteration would otherwise shuffle them, making a startup failure read
	// differently on each run and a diff of two failures meaningless.
	opts := widget.Options{"zebra": 1, "alpha": 2, "count": "many"}

	first := messages(schema.Validate(opts))
	for range 20 {
		if got := messages(schema.Validate(opts)); got != first {
			t.Fatal("error order varies between runs")
		}
	}
}

func TestAccessorsFallBackToDefaults(t *testing.T) {
	empty := widget.Options{}

	if got := empty.String(schema, "mode"); got != "a" {
		t.Errorf("String default = %q", got)
	}
	if got := empty.Int(schema, "count"); got != 7 {
		t.Errorf("Int default = %d", got)
	}
	if got := empty.Bool(schema, "enabled"); !got {
		t.Error("Bool default was not applied")
	}
	if got := empty.StringList(schema, "list"); len(got) != 1 || got[0] != "x" {
		t.Errorf("StringList default = %v", got)
	}
}

func TestAccessorsFallBackWhenTheValueIsTheWrongType(t *testing.T) {
	// Validation rejects these at startup, but a hot reload races validation and
	// a builder must not panic on a value it did not expect.
	wrong := widget.Options{"mode": 1, "count": "many", "enabled": "yes", "list": "one"}

	if got := wrong.String(schema, "mode"); got != "a" {
		t.Errorf("String = %q, want the default", got)
	}
	if got := wrong.Int(schema, "count"); got != 7 {
		t.Errorf("Int = %d, want the default", got)
	}
	if got := wrong.Bool(schema, "enabled"); !got {
		t.Error("Bool did not fall back to its default")
	}
	if got := wrong.StringList(schema, "list"); len(got) != 1 || got[0] != "x" {
		t.Errorf("StringList = %v, want the default", got)
	}
}

func TestAccessorsOnUndeclaredOptionsReturnZero(t *testing.T) {
	empty := widget.Options{}

	if got := empty.String(schema, "nope"); got != "" {
		t.Errorf("String = %q", got)
	}
	if got := empty.Int(schema, "nope"); got != 0 {
		t.Errorf("Int = %d", got)
	}
	if got := empty.Bool(schema, "nope"); got {
		t.Error("Bool = true")
	}
	if got := empty.StringList(schema, "nope"); got != nil {
		t.Errorf("StringList = %v", got)
	}
}

func TestIntAcceptsEveryShapeANumberArrivesIn(t *testing.T) {
	// YAML gives an int for a bare number and a float for one with a decimal
	// point; JSON gives a float for both. The same written number must work
	// whichever decoder produced it.
	for _, raw := range []any{3, int64(3), 3.0, "3"} {
		if got := (widget.Options{"count": raw}).Int(schema, "count"); got != 3 {
			t.Errorf("Int(%T %v) = %d, want 3", raw, raw, got)
		}
	}
	// A fractional value is a mistake worth falling back on rather than
	// silently truncating.
	if got := (widget.Options{"count": 3.5}).Int(schema, "count"); got != 7 {
		t.Errorf("Int(3.5) = %d, want the default", got)
	}
	if got := (widget.Options{"count": "three"}).Int(schema, "count"); got != 7 {
		t.Errorf("Int(\"three\") = %d, want the default", got)
	}
}

func TestStringListAcceptsBothSliceShapes(t *testing.T) {
	// YAML decodes into []any; a Go caller supplies []string.
	if got := (widget.Options{"list": []string{"a", "b"}}).StringList(schema, "list"); len(got) != 2 {
		t.Errorf("got %v", got)
	}
	if got := (widget.Options{"list": []any{"a", "b"}}).StringList(schema, "list"); len(got) != 2 {
		t.Errorf("got %v", got)
	}
}

// --- bind validation -------------------------------------------------------

func vctx(inDrawer bool) widget.ValidationContext {
	return widget.ValidationContext{Domain: domain(), InDrawer: inDrawer}
}

func TestSourcesAreAClosedSet(t *testing.T) {
	// The list is what validation checks against and what the layout reference
	// documents, so it must not be empty and must not carry duplicates.
	seen := map[string]bool{}
	for _, s := range widget.Sources() {
		if seen[s] {
			t.Errorf("duplicate source %q", s)
		}
		seen[s] = true
	}
	if len(seen) < 10 {
		t.Errorf("only %d sources declared", len(seen))
	}
}

func TestValidateBind(t *testing.T) {
	tests := []struct {
		name     string
		bind     widget.Bind
		inDrawer bool
		want     string
	}{
		{"no source", widget.Bind{}, false, "no bind source"},
		{"unknown source", widget.Bind{Source: "service.vibes"}, true, "unknown bind source"},
		{
			// Would render as a permanently empty panel, since no drawer is
			// open in a page section.
			"drawer source in a page section",
			widget.Bind{Source: widget.SourceServiceIncidents}, false, "drawer",
		},
		{"unknown field", widget.Bind{Source: widget.SourceServiceHistory, Field: "vibes"}, true, "unknown field"},
		{"unknown overlay", widget.Bind{Source: widget.SourceServiceHistory, Overlay: "vibes"}, true, "unknown overlay"},
		{"unknown metric", widget.Bind{Source: widget.SourceServiceMetric, Metric: "metric.invented"}, true, "metric.invented"},
		{"negative window", widget.Bind{Source: widget.SourceServiceHistory, Days: -3}, true, "cannot be negative"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errs := widget.ValidateBind(tc.bind, domain(), tc.inDrawer)
			if len(errs) == 0 {
				t.Fatalf("accepted %+v", tc.bind)
			}
			if !strings.Contains(messages(errs), tc.want) {
				t.Errorf("errors do not mention %q:\n%s", tc.want, messages(errs))
			}
		})
	}
}

func TestValidateBindAcceptsAWellFormedBinding(t *testing.T) {
	errs := widget.ValidateBind(widget.Bind{
		Source: widget.SourceServiceHistory,
		Field:  config.FieldVolume, Overlay: config.FieldErrorRate,
		Metric: "metric.volume", Days: 30,
	}, domain(), true)

	if len(errs) != 0 {
		t.Errorf("a valid binding was rejected:\n%s", messages(errs))
	}
}

// --- widget validation -----------------------------------------------------

func TestValidateWidget(t *testing.T) {
	r := widget.Default()

	tests := []struct {
		name   string
		kind   string
		opts   widget.Options
		bind   widget.Bind
		drawer bool
		want   string
	}{
		{
			"unknown type", "haeding",
			widget.Options{}, widget.Bind{}, false, "unknown widget type",
		},
		{
			"a widget that needs data with none bound", "legend",
			widget.Options{}, widget.Bind{}, false, "needs a bind source",
		},
		{
			"a source the widget cannot read", "timeline",
			widget.Options{}, widget.Bind{Source: widget.SourceServiceErrors}, true, "cannot read",
		},
		{
			"a column that is not a declared metric", "leaderboard-table",
			widget.Options{"columns": []any{"rank", "metric.invented"}},
			widget.Bind{Source: widget.SourceServicesFiltered}, false, "metric.invented",
		},
		{
			"an impossible heading level", "heading",
			widget.Options{"termId": "x", "level": 9}, widget.Bind{}, false, "heading level",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errs := r.ValidateWidget(tc.kind, tc.opts, tc.bind, vctx(tc.drawer))
			if len(errs) == 0 {
				t.Fatalf("accepted %s %v", tc.kind, tc.opts)
			}
			if !strings.Contains(messages(errs), tc.want) {
				t.Errorf("errors do not mention %q:\n%s", tc.want, messages(errs))
			}
		})
	}
}

func TestValidateWidgetAcceptsWellFormedWidgets(t *testing.T) {
	r := widget.Default()

	for _, tc := range []struct {
		kind   string
		opts   widget.Options
		bind   widget.Bind
		drawer bool
	}{
		{"heading", widget.Options{"termId": "x", "level": 1}, widget.Bind{}, false},
		{"legend", widget.Options{}, widget.Bind{Source: widget.SourceStatusCounts}, false},
		{"timeline", widget.Options{}, widget.Bind{Source: widget.SourceServiceIncidents}, true},
		{
			"leaderboard-table",
			widget.Options{"columns": []any{"rank", "name", "status", "metric.availability"}},
			widget.Bind{Source: widget.SourceServicesFiltered}, false,
		},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			if errs := r.ValidateWidget(tc.kind, tc.opts, tc.bind, vctx(tc.drawer)); len(errs) != 0 {
				t.Errorf("a valid %s was rejected:\n%s", tc.kind, messages(errs))
			}
		})
	}
}

func TestAWidgetThatReadsNoDataAcceptsNoBinding(t *testing.T) {
	// A heading has nothing to bind to, so requiring one would be noise.
	errs := widget.Default().ValidateWidget("heading",
		widget.Options{"termId": "x"}, widget.Bind{}, vctx(false))

	if len(errs) != 0 {
		t.Errorf("a heading with no binding was rejected:\n%s", messages(errs))
	}
}

// --- presentation helpers --------------------------------------------------

func TestNeutralMetricsHaveNoGoodDirection(t *testing.T) {
	// Traffic rising is neither good nor bad, so it must not be coloured as
	// either.
	sv := svc("busy")
	for i := range sv.History {
		sv.History[i].Volume = int64(1000 + i*100)
	}

	v := leaderboard(t, ctx([]model.Service{sv}), "rank", "metric.volume")

	if got := cellOf(v.Rows[0], 0).TrendTone; got != "neutral" {
		t.Errorf("volume trend toned %q, want neutral", got)
	}
}

func TestEveryMetricUnitFormats(t *testing.T) {
	v := leaderboard(t, ctx([]model.Service{svc("a")}),
		"rank", "metric.availability", "metric.latencyP50", "metric.volume")

	cells := metricCells(v.Rows[0])
	if !strings.HasSuffix(cells[0].Value, "%") {
		t.Errorf("percent = %q", cells[0].Value)
	}
	if !strings.Contains(cells[1].Value, "millisecond") {
		t.Errorf("duration = %q", cells[1].Value)
	}
	if strings.ContainsAny(cells[2].Value, "%m") {
		t.Errorf("count = %q, want a bare number", cells[2].Value)
	}
}

func TestARowWithAnUnrecognisedCategoryHasNoIcon(t *testing.T) {
	// Config validation rejects this; a hot reload races it, and a missing icon
	// is better than a panic.
	v := leaderboard(t, ctx([]model.Service{svc("a", category("cat.invented"))}), "rank", "name")

	if got := cellKind(v.Rows[0], widget.CellName).CategoryIcon.Glyph; got != "" {
		t.Errorf("icon = %q, want none", got)
	}
}

func TestDecliningMetricsAreTonedByTheirOwnDirection(t *testing.T) {
	// The mirror of the rising case: falling availability is bad news, falling
	// error rate is good, and the arrow is the same either way.
	falling := svc("falling")
	for i := range falling.History {
		falling.History[i].Availability = model.Float(99.9 - float64(i)*0.02)
		falling.History[i].ErrorRate = 2.0 - float64(i)*0.05
	}
	falling.Metrics.ErrorRate = falling.History[len(falling.History)-1].ErrorRate

	v := leaderboard(t, ctx([]model.Service{falling}),
		"rank", "metric.availability", "metric.errorRate")

	if got := cellOf(v.Rows[0], 0).TrendTone; got != "major" {
		t.Errorf("falling availability toned %q, want major", got)
	}
	if got := cellOf(v.Rows[0], 1).TrendTone; got != "ok" {
		t.Errorf("falling error rate toned %q, want ok", got)
	}
}

func TestAMetricBoundToAnUnrecognisedFieldRendersNothing(t *testing.T) {
	// Config validation rejects an unknown field, but a hot reload races it. An
	// empty cell is a far better failure than a wrong number.
	c := ctx([]model.Service{svc("a")})
	c.Config.Domain.Metrics = append(c.Config.Domain.Metrics, config.Metric{
		ID: "metric.invented", TermID: "metric.invented",
		Field: "somethingElse", Unit: config.UnitCount,
	})

	v := leaderboard(t, c, "rank", "metric.invented")

	if got := cellOf(v.Rows[0], 0).Value; got != "" {
		t.Errorf("value = %q, want nothing", got)
	}
}
