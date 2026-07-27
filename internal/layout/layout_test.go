package layout_test

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/layout"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/widget"
)

// cfg is a minimal domain that declares what the shipped layout references.
func cfg() config.Config {
	return config.Config{
		Domain: config.Domain{
			DefaultScope: "national",
			Scopes:       []string{"national", "state"},
			Metrics: []config.Metric{
				{ID: "metric.availability", Field: config.FieldAvailability},
				{ID: "metric.errorRate", Field: config.FieldErrorRate},
				{ID: "metric.latencyP50", Field: config.FieldLatencyP50},
				{ID: "metric.volume", Field: config.FieldVolume},
			},
		},
	}
}

func parse(t *testing.T, src string) (layout.Layout, error) {
	t.Helper()
	return layout.Parse([]byte(src), widget.Default(), cfg())
}

const minimal = `
pages:
  - id: home
    path: /
    sections:
      - id: verdict
        widgets:
          - type: heading
            options: { termId: verdict.h1 }
drawer:
  tabs:
    - id: overview
      titleTermId: dr.tab.overview
      widgets:
        - type: timeline
          bind: { source: service.incidents }
`

func TestParseMinimalLayout(t *testing.T) {
	l, err := parse(t, minimal)
	if err != nil {
		t.Fatalf("a valid layout was rejected: %v", err)
	}
	if len(l.Pages) != 1 || l.Pages[0].ID != "home" {
		t.Errorf("pages = %+v", l.Pages)
	}
	if l.DefaultTab() != "overview" {
		t.Errorf("default tab = %q, want overview", l.DefaultTab())
	}
}

// TestShippedLayoutValidates is the guard that matters most: the layout every
// deployment starts from must compose only widgets that exist, bound only to
// data that exists, with options the widgets accept.
func TestShippedLayoutValidates(t *testing.T) {
	raw, err := os.ReadFile("../../config/layout.yaml")
	if err != nil {
		t.Fatalf("reading the shipped layout: %v", err)
	}

	full, err := os.ReadFile("../../config/domain.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var domain config.Domain
	if err := yamlUnmarshal(full, &domain); err != nil {
		t.Fatalf("reading the shipped domain config: %v", err)
	}

	l, err := layout.Parse(raw, widget.Default(), config.Config{Domain: domain})
	if err != nil {
		t.Fatalf("the shipped layout does not validate:\n%v", err)
	}

	if _, ok := l.PageByPath("/"); !ok {
		t.Error("no page serves the root path")
	}
	for _, id := range []string{"verdict", "signals", "leaderboard"} {
		if _, ok := l.SectionByID(id); !ok {
			t.Errorf("the shipped layout has no %q section", id)
		}
	}
	for _, id := range []string{"overview", "errors", "traffic", "incidents"} {
		if _, ok := l.TabByID(id); !ok {
			t.Errorf("the shipped layout has no %q drawer tab", id)
		}
	}
	// Every section the reader can change must be independently swappable, or
	// changing a filter would reload the whole page.
	if got := l.SwapTargets(); len(got) != 3 {
		t.Errorf("swap targets = %v, want all three sections", got)
	}
}

func requireError(t *testing.T, src, want string) {
	t.Helper()
	_, err := parse(t, src)
	if err == nil {
		t.Fatalf("expected an error mentioning %q, but the layout was accepted", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error does not mention %q:\n%s", want, err)
	}
	if !strings.Contains(err.Error(), layout.FileLayout) {
		t.Errorf("error does not name the file:\n%s", err)
	}
}

func TestRejectsUnknownWidgetType(t *testing.T) {
	// The headline failure this engine exists to prevent: a mistyped type that
	// would otherwise render as a blank panel.
	requireError(t, strings.Replace(minimal, "type: heading", "type: haeding", 1), "haeding")
}

func TestRejectsUnknownOption(t *testing.T) {
	// Same reasoning as the config loader: a silently ignored setting leaves the
	// reader wondering why their edit did nothing.
	requireError(t, strings.Replace(minimal,
		"options: { termId: verdict.h1 }",
		"options: { termId: verdict.h1, colour: red }", 1), "colour")
}

func TestRejectsMissingRequiredOption(t *testing.T) {
	requireError(t, strings.Replace(minimal,
		"options: { termId: verdict.h1 }", "options: {}", 1), "termId")
}

func TestRejectsWrongOptionType(t *testing.T) {
	requireError(t, strings.Replace(minimal,
		"options: { termId: verdict.h1 }",
		"options: { termId: verdict.h1, level: high }", 1), "whole number")
}

func TestRejectsUnknownBindSource(t *testing.T) {
	requireError(t, strings.Replace(minimal,
		"bind: { source: service.incidents }",
		"bind: { source: service.vibes }", 1), "service.vibes")
}

func TestRejectsDrawerOnlySourceInAPageSection(t *testing.T) {
	// Binding the open service into a page section would render as a
	// permanently empty panel, since no drawer is open there.
	requireError(t, strings.Replace(minimal,
		`          - type: heading
            options: { termId: verdict.h1 }`,
		`          - type: timeline
            bind: { source: service.incidents }`, 1), "drawer")
}

func TestRejectsAWidgetReadingASourceItCannotUse(t *testing.T) {
	requireError(t, strings.Replace(minimal,
		"bind: { source: service.incidents }",
		"bind: { source: service.errorBreakdown }", 1), "cannot read")
}

func TestRejectsAWidgetWithNoBinding(t *testing.T) {
	requireError(t, strings.Replace(minimal,
		"          bind: { source: service.incidents }\n", "", 1), "needs a bind source")
}

func TestRejectsUnknownMetricInABinding(t *testing.T) {
	src := strings.Replace(minimal,
		`          - type: heading
            options: { termId: verdict.h1 }`,
		`          - type: leaderboard-table
            bind: { source: services.filtered }
            options:
              columns: [rank, metric.invented]`, 1)
	requireError(t, src, "metric.invented")
}

func TestRejectsUnknownRepeatSource(t *testing.T) {
	requireError(t, strings.Replace(minimal,
		"        - type: timeline",
		"        - type: timeline\n          repeatOver: config.vibes", 1), "config.vibes")
}

func TestRejectsStructuralMistakes(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"no pages", "pages: []\n", "no pages declared"},
		{
			"page without a path",
			strings.Replace(minimal, "    path: /\n", "", 1),
			"has no path",
		},
		{
			"path without a slash",
			strings.Replace(minimal, "path: /", "path: home", 1),
			"does not begin with a slash",
		},
		{
			"section without an id",
			strings.Replace(minimal, "      - id: verdict", "      - id: \"\"", 1),
			"section has no id",
		},
		{
			"section with no widgets",
			strings.Replace(minimal,
				`        widgets:
          - type: heading
            options: { termId: verdict.h1 }`, "        widgets: []", 1),
			"no widgets",
		},
		{
			"drawer tab without a title",
			strings.Replace(minimal, "      titleTermId: dr.tab.overview", `      titleTermId: ""`, 1),
			"blank",
		},
		{
			"heading level out of range",
			strings.Replace(minimal,
				"options: { termId: verdict.h1 }",
				"options: { termId: verdict.h1, level: 9 }", 1),
			"heading level",
		},
	} {
		t.Run(tc.name, func(t *testing.T) { requireError(t, tc.src, tc.want) })
	}
}

func TestRejectsDuplicateIdentifiers(t *testing.T) {
	// Section ids become DOM ids and fragment routes, so a duplicate would make
	// HTMX swap the wrong band.
	dup := strings.Replace(minimal,
		`      - id: verdict
        widgets:
          - type: heading
            options: { termId: verdict.h1 }`,
		`      - id: verdict
        widgets:
          - type: heading
            options: { termId: verdict.h1 }
      - id: verdict
        widgets:
          - type: heading
            options: { termId: verdict.h2 }`, 1)
	requireError(t, dup, "duplicate section id")
}

func TestRejectsUnknownYAMLKeys(t *testing.T) {
	requireError(t, strings.Replace(minimal, "    path: /", "    paht: /", 1), "paht")
}

func TestReportsEveryProblemAtOnce(t *testing.T) {
	// Fixing a layout one error per run is miserable and these are independent.
	src := strings.Replace(minimal, "type: heading", "type: haeding", 1)
	src = strings.Replace(src, "bind: { source: service.incidents }", "bind: { source: service.vibes }", 1)

	_, err := parse(t, src)
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"haeding", "service.vibes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error set omits %q, so validation stopped early:\n%s", want, err)
		}
	}
}

func TestErrorsAreLocatedByLine(t *testing.T) {
	_, err := parse(t, strings.Replace(minimal, "type: heading", "type: haeding", 1))
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "layout.yaml:0") {
		t.Errorf("error reported line 0, so the node lookup failed:\n%s", err)
	}
	if !strings.Contains(err.Error(), "layout.yaml:") {
		t.Errorf("error is not located:\n%s", err)
	}
}

func TestLookupsMissWithoutPanicking(t *testing.T) {
	l, err := parse(t, minimal)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := l.PageByPath("/nowhere"); ok {
		t.Error("found a page that does not exist")
	}
	if _, ok := l.SectionByID("nowhere"); ok {
		t.Error("found a section that does not exist")
	}
	if _, ok := l.TabByID("nowhere"); ok {
		t.Error("found a tab that does not exist")
	}
	if got := (layout.Layout{}).DefaultTab(); got != "" {
		t.Errorf("default tab of an empty layout = %q", got)
	}
}

// yamlUnmarshal keeps the yaml dependency out of the test's import list at the
// top, where it would read as though layout parsing needed it.
func yamlUnmarshal(raw []byte, into any) error { return yaml.Unmarshal(raw, into) }

func TestRejectsRemainingStructuralMistakes(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"page without an id",
			strings.Replace(minimal, "  - id: home", `  - id: ""`, 1),
			"page has no id",
		},
		{
			"duplicate page id",
			strings.Replace(minimal, "drawer:", `  - id: home
    path: /other
    sections:
      - id: other
        widgets:
          - type: heading
            options: { termId: x }
drawer:`, 1),
			"duplicate page id",
		},
		{
			"two pages on one path",
			strings.Replace(minimal, "drawer:", `  - id: second
    path: /
    sections:
      - id: other
        widgets:
          - type: heading
            options: { termId: x }
drawer:`, 1),
			"duplicate path",
		},
		{
			"page with no sections",
			strings.Replace(minimal, `    sections:
      - id: verdict
        widgets:
          - type: heading
            options: { termId: verdict.h1 }`, "    sections: []", 1),
			"has no sections",
		},
		{
			"drawer tab without an id",
			strings.Replace(minimal, "    - id: overview", `    - id: ""`, 1),
			"drawer tab has no id",
		},
		{
			"duplicate drawer tab id",
			minimal + `    - id: overview
      titleTermId: dr.tab.other
      widgets:
        - type: timeline
          bind: { source: service.incidents }
`,
			"duplicate drawer tab id",
		},
		{
			"drawer tab with no widgets",
			strings.Replace(minimal, `      widgets:
        - type: timeline
          bind: { source: service.incidents }`, "      widgets: []", 1),
			"no widgets",
		},
		{
			"widget with no type",
			strings.Replace(minimal,
				"          - type: heading\n            options: { termId: verdict.h1 }",
				"          - options: { termId: verdict.h1 }", 1),
			"widget has no type",
		},
	} {
		t.Run(tc.name, func(t *testing.T) { requireError(t, tc.src, tc.want) })
	}
}

func TestSectionHeadingIsValidated(t *testing.T) {
	withHeading := func(h string) string {
		return strings.Replace(minimal, "      - id: verdict\n",
			"      - id: verdict\n        heading:\n"+h, 1)
	}

	requireError(t, withHeading("          titleTermId: \"\"\n"), "heading has no title")
	requireError(t, withHeading("          titleTermId: lb.title\n          level: 9\n"), "heading level")

	// A well-formed heading is accepted, and level 0 means "unset".
	if _, err := parse(t, withHeading("          titleTermId: lb.title\n          level: 2\n")); err != nil {
		t.Errorf("a valid heading was rejected: %v", err)
	}
	if _, err := parse(t, withHeading("          titleTermId: lb.title\n")); err != nil {
		t.Errorf("a heading with no explicit level was rejected: %v", err)
	}
}

func TestSyntaxErrorsAreReported(t *testing.T) {
	requireError(t, "pages:\n  - id: home\n   path: /\n", "")
}
