package config_test

import (
	"strings"
	"testing"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
)

// The fixtures below are a minimal *valid* bundle. Each test mutates exactly
// one file, so a failure names the rule that broke rather than a soup of
// unrelated errors.

const validApp = `
server:
  addr: ":8080"
  readTimeout: 15s
  writeTimeout: 30s
  idleTimeout: 60s
  shutdownGrace: 10s
client:
  runtime: vanilla
log:
  level: info
  format: text
storage:
  driver: memory
  dsn: ""
  maxOpenConns: 10
  maxIdleConns: 5
  connMaxLifetime: 30m
  history:
    retentionDays: 90
    rawSampleRetentionHours: 48
    rollupIntervalMinutes: 15
`

const validBrand = `
wordmarkTermId: brand.wordmark
taglineTermId: brand.tagline
iconKey: brand.mark
favicon: /assets/favicon.svg
footer:
  - termId: footer.cdpi
    href: https://cdpi.dev/
    external: true
`

// The domain fixture is composed from named sections so that a test can swap a
// whole block — emptying the metric list, say — without matching a page of
// indented YAML.
const domainHead = `
h1TermId: verdict.h1
dataSourceTermId: verdict.source
dataSourceHref: https://directory.apisetu.gov.in/
onboardedDenominator: 812
scopes: [national, state]
defaultScope: national
periods:
  - { id: 24h, termId: period.24h, days: 1 }
  - { id: 30d, termId: period.30d, days: 30 }
defaultPeriod: 30d
`

const domainTaxonomy = `taxonomy:
  categories:
    - { id: cat.identity, termId: cat.identity, iconKey: cat.identity }
  regions:
    - { id: reg.national, termId: reg.national, scope: national }
    - { id: reg.mh, termId: reg.mh, scope: state }
  providers:
    - { id: prov.uidai, termId: prov.uidai }
`

const domainMetrics = `metrics:
  - id: metric.availability
    termId: metric.availability
    field: availability
    unit: percent
    precision: 2
    target: 99.5
    direction: higher-is-better
    showInLeaderboard: true
  - id: metric.errorRate
    termId: metric.errorRate
    field: errorRate
    unit: percent
    precision: 2
    target: 1.0
    direction: lower-is-better
    showInLeaderboard: true
  - id: metric.volume
    termId: metric.volume
    field: volume
    unit: count
    precision: 0
    direction: neutral
    framing: denominator
    denominatorOf: metric.availability
    showInLeaderboard: false
`

const domainThresholds = `thresholds:
  evaluationOrder: [maintenance, unknown, major, partial, operational]
  values:
    majorAvailBelow: 99.0
    majorErrAbove: 2.0
    partialAvailBelow: 99.5
    partialErrAbove: 1.0
    staleSecondsAbove: 900
  prose:
    major: rule.major
    partial: rule.partial
    operational: rule.operational
    unknown: rule.unknown
    maintenance: rule.maintenance
`

const domainStatusModel = `statusModel:
  order: [operational, partial, major, unknown, maintenance]
  severity: { major: 4, partial: 3, unknown: 2, maintenance: 1, operational: 0 }
  iconKey:
    operational: status.operational
    partial: status.partial
    major: status.major
    unknown: status.unknown
    maintenance: status.maintenance
  labelTermId:
    operational: status.operational
    partial: status.partial
    major: status.major
    unknown: status.unknown
    maintenance: status.maintenance
`

const domainSignals = `signals:
  - id: sig.belowTarget
    kind: belowTargetDays
    titleTermId: sig.belowTarget.title
    ruleTermId: sig.belowTarget.rule
    iconKey: status.partial
    tone: partial
    days: 7
    filter:
      status: [partial, major]
`

const domainSignalsEmpty = `signalsEmpty:
  termId: sig.empty
  iconKey: ui.check
  tone: ok
`

var validDomain = domainHead + domainTaxonomy + domainMetrics +
	domainThresholds + domainStatusModel + domainSignals + domainSignalsEmpty

const validTheme = `
light:
  --bg: "#FAF9F5"
  --card: "#FFFFFF"
  --fg: "#1A1917"
  --muted-fg: "#6B6862"
  --border-subtle: "#E6E3DC"
  --border-strong: "#C9C5BC"
  --primary: "#1A1917"
  --primary-fg: "#FAF9F5"
  --accent: "#3D5A5B"
  --status-ok: "#1F6B4A"
  --status-ok-bg: "#E8F2EC"
  --status-partial: "#96601A"
  --status-partial-bg: "#FBF0DF"
  --status-major: "#A32A1E"
  --status-major-bg: "#FAE9E7"
  --status-unknown: "#6B6862"
  --status-unknown-bg: "#EFEDE8"
  --status-maintenance: "#2A4E86"
  --status-maintenance-bg: "#E8EFF9"
dark:
  --bg: "#16150F"
  --card: "#201F17"
  --fg: "#F3F1E9"
  --muted-fg: "#A8A395"
  --border-subtle: "#312F26"
  --border-strong: "#464335"
  --primary: "#F3F1E9"
  --primary-fg: "#16150F"
  --accent: "#8FB6B4"
  --status-ok: "#5BC48C"
  --status-ok-bg: "#152A20"
  --status-partial: "#E0A954"
  --status-partial-bg: "#2E2412"
  --status-major: "#EB7468"
  --status-major-bg: "#301715"
  --status-unknown: "#A8A395"
  --status-unknown-bg: "#26251C"
  --status-maintenance: "#7AA6E6"
  --status-maintenance-bg: "#141F30"
tokens:
  --radius-sm: 2px
  --radius-lg: 10px
fonts:
  body:
    family: Outfit
    stack: "Outfit, system-ui, sans-serif"
    faces:
      - { file: outfit-400.woff2, weight: "400", style: normal, display: swap }
  serif:
    family: Playfair Display
    stack: "\"Playfair Display\", Georgia, serif"
`

const validIcons = `
icons:
  brand.mark: { glyph: "\u25D0", label: Dashboard }
  cat.identity: { glyph: "\u25C8", label: Identity }
  status.operational: { glyph: "\u25CF", label: Operational }
  status.partial: { glyph: "\u25D0", label: Partial outage }
  status.major: { glyph: "\u2715", label: Major outage }
  status.unknown: { glyph: "?", label: Unknown }
  status.maintenance: { glyph: "\u2699", label: Under maintenance }
  ui.check: { glyph: "\u2713", label: All clear }
  trend.up: { glyph: "\u25B2", label: Rising }
`

func bundle() config.Bundle {
	return config.Bundle{
		"app.yaml":    []byte(validApp),
		"brand.yaml":  []byte(validBrand),
		"domain.yaml": []byte(validDomain),
		"theme.yaml":  []byte(validTheme),
		"icons.yaml":  []byte(validIcons),
	}
}

// replace swaps one line's value in a fixture, keeping the rest valid.
func replace(t *testing.T, src, old, new string) string {
	t.Helper()
	if !strings.Contains(src, old) {
		t.Fatalf("fixture no longer contains %q; update the test", old)
	}
	return strings.Replace(src, old, new, 1)
}

func TestParseValidBundle(t *testing.T) {
	cfg, err := config.Parse(bundle())
	if err != nil {
		t.Fatalf("valid bundle rejected: %v", err)
	}

	if cfg.App.Server.Addr != ":8080" {
		t.Errorf("addr = %q, want :8080", cfg.App.Server.Addr)
	}
	if got := cfg.App.Storage.ConnMaxLifetime.Std().Minutes(); got != 30 {
		t.Errorf("connMaxLifetime = %v minutes, want 30", got)
	}
	if cfg.Domain.OnboardedDenominator != 812 {
		t.Errorf("onboardedDenominator = %d, want 812", cfg.Domain.OnboardedDenominator)
	}
	if len(cfg.Domain.Metrics) != 3 {
		t.Errorf("got %d metrics, want 3", len(cfg.Domain.Metrics))
	}
	if cfg.Domain.Metrics[0].Target == nil || *cfg.Domain.Metrics[0].Target != 99.5 {
		t.Errorf("availability target = %v, want 99.5", cfg.Domain.Metrics[0].Target)
	}
	// A metric with no target must stay nil rather than defaulting to 0, which
	// would render as "target 0%" beside every traffic figure.
	if cfg.Domain.Metrics[2].Target != nil {
		t.Errorf("volume target = %v, want nil", *cfg.Domain.Metrics[2].Target)
	}
}

// requireError asserts that parsing fails and that the message names both the
// file and the substring describing the rule.
func requireError(t *testing.T, b config.Bundle, wantFile, wantSubstr string) {
	t.Helper()
	_, err := config.Parse(b)
	if err == nil {
		t.Fatalf("expected an error mentioning %q, got none", wantSubstr)
	}
	msg := err.Error()
	if !strings.Contains(msg, wantFile) {
		t.Errorf("error does not name the file %q:\n%s", wantFile, msg)
	}
	if !strings.Contains(msg, wantSubstr) {
		t.Errorf("error does not mention %q:\n%s", wantSubstr, msg)
	}
}

func TestRejectsUnknownFields(t *testing.T) {
	// A typo must fail loudly. Silently ignoring `runtim:` would leave the
	// deployment wondering why its setting had no effect.
	b := bundle()
	b["app.yaml"] = []byte(replace(t, validApp, "  runtime: vanilla", "  runtim: vanilla"))
	requireError(t, b, "app.yaml", "runtim")
}

func TestRejectsUnknownStorageDriver(t *testing.T) {
	b := bundle()
	b["app.yaml"] = []byte(replace(t, validApp, "driver: memory", "driver: mongodb"))
	requireError(t, b, "app.yaml", "mongodb")
}

func TestRejectsUnknownClientRuntime(t *testing.T) {
	b := bundle()
	b["app.yaml"] = []byte(replace(t, validApp, "runtime: vanilla", "runtime: react"))
	requireError(t, b, "app.yaml", "react")
}

func TestRejectsNonPositiveRetention(t *testing.T) {
	b := bundle()
	b["app.yaml"] = []byte(replace(t, validApp, "retentionDays: 90", "retentionDays: 0"))
	requireError(t, b, "app.yaml", "retentionDays")
}

func TestRejectsBareNumberDuration(t *testing.T) {
	// "30" is ambiguous between seconds and nanoseconds; refusing it is safer
	// than guessing.
	b := bundle()
	b["app.yaml"] = []byte(replace(t, validApp, "connMaxLifetime: 30m", "connMaxLifetime: 30"))
	requireError(t, b, "app.yaml", "duration")
}

func TestRejectsDefaultScopeOutsideScopes(t *testing.T) {
	b := bundle()
	b["domain.yaml"] = []byte(replace(t, validDomain, "defaultScope: national", "defaultScope: galactic"))
	requireError(t, b, "domain.yaml", "galactic")
}

func TestRejectsDefaultPeriodOutsidePeriods(t *testing.T) {
	b := bundle()
	b["domain.yaml"] = []byte(replace(t, validDomain, "defaultPeriod: 30d", "defaultPeriod: 99d"))
	requireError(t, b, "domain.yaml", "99d")
}

func TestRejectsRegionInUnknownScope(t *testing.T) {
	b := bundle()
	b["domain.yaml"] = []byte(replace(t, validDomain,
		"{ id: reg.mh, termId: reg.mh, scope: state }",
		"{ id: reg.mh, termId: reg.mh, scope: province }"))
	requireError(t, b, "domain.yaml", "province")
}

func TestRejectsDuplicateCategoryID(t *testing.T) {
	b := bundle()
	b["domain.yaml"] = []byte(replace(t, validDomain,
		"    - { id: cat.identity, termId: cat.identity, iconKey: cat.identity }",
		"    - { id: cat.identity, termId: cat.identity, iconKey: cat.identity }\n    - { id: cat.identity, termId: cat.other, iconKey: cat.identity }"))
	requireError(t, b, "domain.yaml", "duplicate")
}

func TestRejectsMetricWithUnknownField(t *testing.T) {
	b := bundle()
	b["domain.yaml"] = []byte(replace(t, validDomain, "    field: availability", "    field: uptimeness"))
	requireError(t, b, "domain.yaml", "uptimeness")
}

func TestRejectsMetricWithUnknownDirection(t *testing.T) {
	b := bundle()
	b["domain.yaml"] = []byte(replace(t, validDomain, "direction: higher-is-better", "direction: sideways"))
	requireError(t, b, "domain.yaml", "sideways")
}

func TestRejectsMetricWithUnknownUnit(t *testing.T) {
	b := bundle()
	b["domain.yaml"] = []byte(replace(t, validDomain, "    unit: percent\n    precision: 2\n    target: 99.5", "    unit: furlongs\n    precision: 2\n    target: 99.5"))
	requireError(t, b, "domain.yaml", "furlongs")
}

func TestRejectsDenominatorOfUnknownMetric(t *testing.T) {
	b := bundle()
	b["domain.yaml"] = []byte(replace(t, validDomain, "denominatorOf: metric.availability", "denominatorOf: metric.nonexistent"))
	requireError(t, b, "domain.yaml", "metric.nonexistent")
}

func TestRejectsSignalWithUnknownKind(t *testing.T) {
	b := bundle()
	b["domain.yaml"] = []byte(replace(t, validDomain, "kind: belowTargetDays", "kind: vibesCheck"))
	requireError(t, b, "domain.yaml", "vibesCheck")
}

func TestRejectsSignalFilterOnUnknownStatus(t *testing.T) {
	b := bundle()
	b["domain.yaml"] = []byte(replace(t, validDomain, "status: [partial, major]", "status: [partial, catastrophic]"))
	requireError(t, b, "domain.yaml", "catastrophic")
}

func TestRejectsIncompleteStatusOrder(t *testing.T) {
	// The status vocabulary is fixed; omitting one would leave services that
	// evaluate to it with no label, icon or position.
	b := bundle()
	b["domain.yaml"] = []byte(replace(t, validDomain,
		"order: [operational, partial, major, unknown, maintenance]",
		"order: [operational, partial, major, unknown]"))
	requireError(t, b, "domain.yaml", "maintenance")
}

func TestRejectsEvaluationOrderWithUnknownStatus(t *testing.T) {
	b := bundle()
	b["domain.yaml"] = []byte(replace(t, validDomain,
		"evaluationOrder: [maintenance, unknown, major, partial, operational]",
		"evaluationOrder: [maintenance, unknown, major, partial, degraded]"))
	requireError(t, b, "domain.yaml", "degraded")
}

func TestRejectsUnknownIconReference(t *testing.T) {
	// Cross-file validation: domain.yaml names an icon that icons.yaml lacks.
	b := bundle()
	b["domain.yaml"] = []byte(replace(t, validDomain, "iconKey: cat.identity }", "iconKey: cat.missing }"))
	requireError(t, b, "domain.yaml", "cat.missing")
}

func TestRejectsUnknownBrandIcon(t *testing.T) {
	b := bundle()
	b["brand.yaml"] = []byte(replace(t, validBrand, "iconKey: brand.mark", "iconKey: brand.absent"))
	requireError(t, b, "brand.yaml", "brand.absent")
}

func TestRejectsMissingThemeToken(t *testing.T) {
	b := bundle()
	b["theme.yaml"] = []byte(replace(t, validTheme, `  --accent: "#8FB6B4"`, ""))
	requireError(t, b, "theme.yaml", "--accent")
}

func TestRejectsFontFaceWithNeitherFileNorURL(t *testing.T) {
	b := bundle()
	b["theme.yaml"] = []byte(replace(t, validTheme,
		`      - { file: outfit-400.woff2, weight: "400", style: normal, display: swap }`,
		`      - { weight: "400", style: normal, display: swap }`))
	requireError(t, b, "theme.yaml", "file")
}

func TestRejectsFontFaceWithBothFileAndURL(t *testing.T) {
	b := bundle()
	b["theme.yaml"] = []byte(replace(t, validTheme,
		`      - { file: outfit-400.woff2, weight: "400", style: normal, display: swap }`,
		`      - { file: a.woff2, url: "https://example.test/a.woff2", weight: "400", style: normal }`))
	requireError(t, b, "theme.yaml", "both")
}

func TestRejectsIconWithNeitherGlyphNorSVG(t *testing.T) {
	b := bundle()
	b["icons.yaml"] = []byte(replace(t, validIcons, `  brand.mark: { glyph: "\u25D0", label: Dashboard }`, `  brand.mark: { label: Dashboard }`))
	requireError(t, b, "icons.yaml", "brand.mark")
}

func TestErrorsAreLocatedByLine(t *testing.T) {
	b := bundle()
	b["domain.yaml"] = []byte(replace(t, validDomain, "defaultScope: national", "defaultScope: galactic"))

	_, err := config.Parse(b)
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()

	// The whole point of carrying yaml.Node positions: the integrator gets a
	// line to jump to, not just a file.
	if !strings.Contains(msg, "domain.yaml:") {
		t.Fatalf("error is not located in a file:\n%s", msg)
	}
	loc := msg[strings.Index(msg, "domain.yaml:"):]
	if !strings.HasPrefix(loc, "domain.yaml:1") && !strings.Contains(loc[:20], ":") {
		t.Errorf("error has no line number:\n%s", msg)
	}
	if strings.Contains(msg, "domain.yaml:0") {
		t.Errorf("error reported line 0, meaning the node lookup failed:\n%s", msg)
	}
}

func TestReportsAllErrorsAtOnce(t *testing.T) {
	// Fixing config one error per run is miserable and these are independent.
	b := bundle()
	dom := replace(t, validDomain, "defaultScope: national", "defaultScope: galactic")
	dom = replace(t, dom, "kind: belowTargetDays", "kind: vibesCheck")
	dom = replace(t, dom, "direction: higher-is-better", "direction: sideways")
	b["domain.yaml"] = []byte(dom)

	_, err := config.Parse(b)
	if err == nil {
		t.Fatal("expected errors")
	}
	msg := err.Error()

	for _, want := range []string{"galactic", "vibesCheck", "sideways"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error set omits %q, so validation stopped early:\n%s", want, msg)
		}
	}
}

func TestRejectsMissingFile(t *testing.T) {
	b := bundle()
	delete(b, "domain.yaml")
	requireError(t, b, "domain.yaml", "missing")
}

func TestRejectsMalformedYAML(t *testing.T) {
	b := bundle()
	b["icons.yaml"] = []byte("icons:\n  - this is a sequence\n    not a mapping: true\n")
	requireError(t, b, "icons.yaml", "")
}

// --- environment expansion -------------------------------------------------

func TestExpandEnvSubstitutesVariables(t *testing.T) {
	env := map[string]string{"DATABASE_URL": "postgres://localhost/dpi"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	got, err := config.ExpandEnv([]byte("dsn: ${DATABASE_URL}\n"), lookup)
	if err != nil {
		t.Fatalf("ExpandEnv: %v", err)
	}
	if want := "dsn: postgres://localhost/dpi\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExpandEnvAppliesDefaults(t *testing.T) {
	lookup := func(string) (string, bool) { return "", false }

	got, err := config.ExpandEnv([]byte("addr: ${PORT:-:8080}\n"), lookup)
	if err != nil {
		t.Fatalf("ExpandEnv: %v", err)
	}
	if want := "addr: :8080\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExpandEnvPrefersSetValueOverDefault(t *testing.T) {
	lookup := func(string) (string, bool) { return ":9000", true }

	got, err := config.ExpandEnv([]byte("addr: ${PORT:-:8080}\n"), lookup)
	if err != nil {
		t.Fatalf("ExpandEnv: %v", err)
	}
	if want := "addr: :9000\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExpandEnvFailsOnUnsetWithoutDefault(t *testing.T) {
	// Expanding to empty would produce a config that parses but misbehaves at
	// runtime, which is worse than refusing to start.
	lookup := func(string) (string, bool) { return "", false }

	if _, err := config.ExpandEnv([]byte("dsn: ${DATABASE_URL}\n"), lookup); err == nil {
		t.Error("an unset variable with no default was accepted")
	}
}

func TestExpandEnvEscapesDoubledDollar(t *testing.T) {
	lookup := func(string) (string, bool) { return "", false }

	got, err := config.ExpandEnv([]byte("literal: $${NOT_A_VAR}\n"), lookup)
	if err != nil {
		t.Fatalf("ExpandEnv: %v", err)
	}
	if want := "literal: ${NOT_A_VAR}\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExpandEnvLeavesLoneDollarAlone(t *testing.T) {
	lookup := func(string) (string, bool) { return "", false }

	got, err := config.ExpandEnv([]byte("price: $5 and 100$\n"), lookup)
	if err != nil {
		t.Fatalf("ExpandEnv: %v", err)
	}
	if want := "price: $5 and 100$\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExpandEnvRejectsUnterminatedReference(t *testing.T) {
	lookup := func(string) (string, bool) { return "x", true }

	if _, err := config.ExpandEnv([]byte("dsn: ${UNTERMINATED\n"), lookup); err == nil {
		t.Error("an unterminated ${ reference was accepted")
	}
}
