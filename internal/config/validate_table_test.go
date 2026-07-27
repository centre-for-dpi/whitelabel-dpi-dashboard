package config_test

import (
	"strings"
	"testing"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
)

// Each row takes the valid bundle, breaks exactly one rule, and names the
// phrase the reader should see. Together they are the readable specification of
// what the white-label contract will and will not accept.
type rejection struct {
	name string
	file string
	old  string
	new  string
	want string
}

func (r rejection) run(t *testing.T) {
	t.Helper()

	b := bundle()
	src := string(b[r.file])
	if !strings.Contains(src, r.old) {
		t.Fatalf("fixture %s no longer contains %q; update the test", r.file, r.old)
	}
	b[r.file] = []byte(strings.Replace(src, r.old, r.new, 1))

	_, err := config.Parse(b)
	if err == nil {
		t.Fatalf("expected rejection mentioning %q, but the bundle was accepted", r.want)
	}
	msg := err.Error()
	if !strings.Contains(msg, r.want) {
		t.Errorf("error does not mention %q:\n%s", r.want, msg)
	}
	if !strings.Contains(msg, r.file) {
		t.Errorf("error does not name %s:\n%s", r.file, msg)
	}
}

func TestAppRejections(t *testing.T) {
	for _, tc := range []rejection{
		{"empty listen address", "app.yaml", `addr: ":8080"`, `addr: ""`, "address is empty"},
		{"unknown log level", "app.yaml", "level: info", "level: chatty", "chatty"},
		{"unknown log format", "app.yaml", "format: text", "format: xml", "xml"},
		{"negative max open conns", "app.yaml", "maxOpenConns: 10", "maxOpenConns: -1", "maxOpenConns"},
		{"negative max idle conns", "app.yaml", "maxIdleConns: 5", "maxIdleConns: -2", "maxIdleConns"},
		{"zero raw sample retention", "app.yaml", "rawSampleRetentionHours: 48", "rawSampleRetentionHours: 0", "rawSampleRetentionHours"},
		{"zero rollup interval", "app.yaml", "rollupIntervalMinutes: 15", "rollupIntervalMinutes: 0", "rollupIntervalMinutes"},
		// A network database with no DSN starts and then fails on first query.
		{"postgres without dsn", "app.yaml", "driver: memory", "driver: postgres", "needs a dsn"},
		{"mysql without dsn", "app.yaml", "driver: memory", "driver: mysql", "needs a dsn"},
		{"mariadb without dsn", "app.yaml", "driver: memory", "driver: mariadb", "needs a dsn"},
	} {
		t.Run(tc.name, tc.run)
	}
}

func TestBrandRejections(t *testing.T) {
	for _, tc := range []rejection{
		{"no wordmark", "brand.yaml", "wordmarkTermId: brand.wordmark", `wordmarkTermId: ""`, "wordmarkTermId"},
		{"footer link without href", "brand.yaml", "href: https://cdpi.dev/", `href: ""`, "href"},
		{"footer link without term", "brand.yaml", "termId: footer.cdpi", `termId: ""`, "termId"},
	} {
		t.Run(tc.name, tc.run)
	}
}

func TestIconRejections(t *testing.T) {
	for _, tc := range []rejection{
		{
			"no icons at all",
			"icons.yaml",
			validIcons,
			"icons: {}\n",
			"no icons declared",
		},
		{
			"icon with both glyph and svg",
			"icons.yaml",
			`  brand.mark: { glyph: "\u25D0", label: Dashboard }`,
			`  brand.mark: { glyph: "\u25D0", svg: "<svg/>", label: Dashboard }`,
			"both glyph and svg",
		},
	} {
		t.Run(tc.name, tc.run)
	}
}

func TestDomainStructureRejections(t *testing.T) {
	for _, tc := range []rejection{
		{"no scopes", "domain.yaml", "scopes: [national, state]", "scopes: []", "no scopes declared"},
		{"zero denominator", "domain.yaml", "onboardedDenominator: 812", "onboardedDenominator: 0", "onboardedDenominator"},
		{
			"no periods",
			"domain.yaml",
			"periods:\n  - { id: 24h, termId: period.24h, days: 1 }\n  - { id: 30d, termId: period.30d, days: 30 }",
			"periods: []",
			"no periods declared",
		},
		{
			"period without id",
			"domain.yaml",
			"  - { id: 24h, termId: period.24h, days: 1 }",
			"  - { id: \"\", termId: period.24h, days: 1 }",
			"period has no id",
		},
		{
			"period with zero days",
			"domain.yaml",
			"  - { id: 24h, termId: period.24h, days: 1 }",
			"  - { id: 24h, termId: period.24h, days: 0 }",
			"days 0",
		},
		{
			"duplicate period id",
			"domain.yaml",
			"  - { id: 30d, termId: period.30d, days: 30 }",
			"  - { id: 24h, termId: period.30d, days: 30 }\n  - { id: 30d, termId: period.30d, days: 30 }",
			"duplicate period id",
		},
	} {
		t.Run(tc.name, tc.run)
	}
}

func TestTaxonomyRejections(t *testing.T) {
	for _, tc := range []rejection{
		{
			"no categories",
			"domain.yaml",
			"  categories:\n    - { id: cat.identity, termId: cat.identity, iconKey: cat.identity }",
			"  categories: []",
			"no categories declared",
		},
		{
			"category without id",
			"domain.yaml",
			"    - { id: cat.identity, termId: cat.identity, iconKey: cat.identity }",
			`    - { id: "", termId: cat.identity, iconKey: cat.identity }`,
			"category has no id",
		},
		{
			"no regions",
			"domain.yaml",
			"  regions:\n    - { id: reg.national, termId: reg.national, scope: national }\n    - { id: reg.mh, termId: reg.mh, scope: state }",
			"  regions: []",
			"no regions declared",
		},
		{
			"region without id",
			"domain.yaml",
			"    - { id: reg.national, termId: reg.national, scope: national }",
			`    - { id: "", termId: reg.national, scope: national }`,
			"region has no id",
		},
		{
			"duplicate region id",
			"domain.yaml",
			"    - { id: reg.mh, termId: reg.mh, scope: state }",
			"    - { id: reg.national, termId: reg.mh, scope: state }",
			"duplicate region id",
		},
		{
			"provider without id",
			"domain.yaml",
			"    - { id: prov.uidai, termId: prov.uidai }",
			`    - { id: "", termId: prov.uidai }`,
			"provider has no id",
		},
		{
			"duplicate provider id",
			"domain.yaml",
			"    - { id: prov.uidai, termId: prov.uidai }",
			"    - { id: prov.uidai, termId: prov.uidai }\n    - { id: prov.uidai, termId: prov.other }",
			"duplicate provider id",
		},
	} {
		t.Run(tc.name, tc.run)
	}
}

func TestMetricRejections(t *testing.T) {
	for _, tc := range []rejection{
		{"no metrics", "domain.yaml", domainMetrics, "metrics: []\n", "no metrics declared"},
		{"metric without id", "domain.yaml", "  - id: metric.availability", `  - id: ""`, "metric has no id"},
		{
			"duplicate metric id",
			"domain.yaml",
			"  - id: metric.errorRate",
			"  - id: metric.availability\n    termId: metric.dup\n    field: errorRate\n    unit: percent\n    precision: 2\n    target: 1.0\n    direction: lower-is-better\n    showInLeaderboard: false\n  - id: metric.errorRate",
			"duplicate metric id",
		},
		{"negative precision", "domain.yaml", "    precision: 2\n    target: 99.5", "    precision: -1\n    target: 99.5", "precision"},
		{
			// A leaderboard column with no target and no framing renders a bare
			// number with nothing to compare it against.
			"leaderboard metric with no context",
			"domain.yaml",
			"    unit: percent\n    precision: 2\n    target: 99.5\n    direction: higher-is-better\n    showInLeaderboard: true",
			"    unit: percent\n    precision: 2\n    direction: higher-is-better\n    showInLeaderboard: true",
			"neither a target nor a framing",
		},
	} {
		t.Run(tc.name, tc.run)
	}
}

func TestThresholdAndStatusRejections(t *testing.T) {
	for _, tc := range []rejection{
		{
			"prose keyed by unknown status",
			"domain.yaml",
			"    maintenance: rule.maintenance",
			"    maintenance: rule.maintenance\n    degraded: rule.degraded",
			"degraded",
		},
		{
			// If partial is stricter than major, nothing can ever land on it.
			"partial availability stricter than major",
			"domain.yaml",
			"    partialAvailBelow: 99.5",
			"    partialAvailBelow: 98.0",
			"no service can ever be merely partial",
		},
		{
			"partial error threshold above major",
			"domain.yaml",
			"    partialErrAbove: 1.0",
			"    partialErrAbove: 3.0",
			"no service can ever be merely partial",
		},
		{
			"zero staleness threshold",
			"domain.yaml",
			"    staleSecondsAbove: 900",
			"    staleSecondsAbove: 0",
			"every service would read as stale",
		},
		{
			"order names an unknown status",
			"domain.yaml",
			"  order: [operational, partial, major, unknown, maintenance]",
			"  order: [operational, partial, major, unknown, maintenance, degraded]",
			"degraded",
		},
		{
			"severity missing a status",
			"domain.yaml",
			"  severity: { major: 4, partial: 3, unknown: 2, maintenance: 1, operational: 0 }",
			"  severity: { major: 4, partial: 3, unknown: 2, operational: 0 }",
			"maintenance",
		},
		{
			"label missing a status",
			"domain.yaml",
			"  labelTermId:\n    operational: status.operational",
			"  labelTermId:\n    zzz: status.zzz",
			"operational",
		},
		{
			"icon keyed by unknown status",
			"domain.yaml",
			"  iconKey:\n    operational: status.operational",
			"  iconKey:\n    degraded: status.operational\n    operational: status.operational",
			"degraded",
		},
		{
			"status icon that is not declared",
			"domain.yaml",
			"    maintenance: status.maintenance\n  labelTermId:",
			"    maintenance: status.absent\n  labelTermId:",
			"status.absent",
		},
	} {
		t.Run(tc.name, tc.run)
	}
}

func TestSignalRejections(t *testing.T) {
	for _, tc := range []rejection{
		{"signal without id", "domain.yaml", "  - id: sig.belowTarget", `  - id: ""`, "signal has no id"},
		{
			"duplicate signal id",
			"domain.yaml",
			domainSignals,
			domainSignals + "  - id: sig.belowTarget\n    kind: maintenanceActive\n    titleTermId: t\n    ruleTermId: r\n    iconKey: ui.check\n    tone: ok\n",
			"duplicate signal id",
		},
		{
			"below-target signal with no window",
			"domain.yaml",
			"    days: 7",
			"    days: 0",
			"positive days window",
		},
		{
			"error-rising signal with no minimum",
			"domain.yaml",
			"    kind: belowTargetDays\n    titleTermId: sig.belowTarget.title\n    ruleTermId: sig.belowTarget.rule\n    iconKey: status.partial\n    tone: partial\n    days: 7",
			"    kind: errorRisingCategories\n    titleTermId: sig.belowTarget.title\n    ruleTermId: sig.belowTarget.rule\n    iconKey: status.partial\n    tone: partial\n    minCategories: 0",
			"positive minCategories",
		},
		{
			"signal without a title",
			"domain.yaml",
			"    titleTermId: sig.belowTarget.title",
			`    titleTermId: ""`,
			"no title term",
		},
		{
			// Every card prints the rule it applied; without one it would state
			// a finding with no stated basis.
			"signal without a rule",
			"domain.yaml",
			"    ruleTermId: sig.belowTarget.rule",
			`    ruleTermId: ""`,
			"without stating the rule",
		},
		{
			"filter on an unknown category",
			"domain.yaml",
			"      status: [partial, major]",
			"      category: [cat.nonexistent]",
			"cat.nonexistent",
		},
		// The demo hardcoded each signal's glyph and colour by signal id inside
		// the component. Both are configuration here, so both are validated.
		{
			"signal with an undeclared icon",
			"domain.yaml",
			"    iconKey: status.partial",
			"    iconKey: status.absent",
			"status.absent",
		},
		{
			"signal with no icon at all",
			"domain.yaml",
			"    iconKey: status.partial\n",
			"",
			"unknown icon",
		},
		{
			"signal with an unknown tone",
			"domain.yaml",
			"    tone: partial",
			"    tone: chartreuse",
			"chartreuse",
		},
	} {
		t.Run(tc.name, tc.run)
	}
}

func TestEmptySignalCardRejections(t *testing.T) {
	// With no signal firing, this card is the entire section. It cannot be left
	// to a default.
	for _, tc := range []rejection{
		{
			"no term",
			"domain.yaml",
			"  termId: sig.empty",
			`  termId: ""`,
			"would render blank",
		},
		{
			"undeclared icon",
			"domain.yaml",
			"  iconKey: ui.check",
			"  iconKey: ui.absent",
			"ui.absent",
		},
		{
			"unknown tone",
			"domain.yaml",
			"  tone: ok",
			"  tone: beige",
			"beige",
		},
	} {
		t.Run(tc.name, tc.run)
	}
}

func TestThemeRejections(t *testing.T) {
	for _, tc := range []rejection{
		{
			"body font without a stack",
			"theme.yaml",
			`    stack: "Outfit, system-ui, sans-serif"`,
			`    stack: ""`,
			"no CSS stack",
		},
		{
			"serif font without a stack",
			"theme.yaml",
			`    stack: "\"Playfair Display\", Georgia, serif"`,
			`    stack: ""`,
			"no CSS stack",
		},
		{
			"missing light token",
			"theme.yaml",
			`  --accent: "#3D5A5B"`,
			"",
			"--accent",
		},
		{
			// Theme values are written verbatim into the generated stylesheet,
			// so a closing brace would end the rule and let whatever follows
			// become new CSS.
			"token value that closes the rule",
			"theme.yaml",
			`  --accent: "#3D5A5B"`,
			`  --accent: "red} :root{display:none"`,
			"break out of the generated stylesheet",
		},
		{
			"token value containing a comment",
			"theme.yaml",
			`  --accent: "#3D5A5B"`,
			`  --accent: "red/*"`,
			"break out of the generated stylesheet",
		},
		{
			"static token that is not a custom property",
			"theme.yaml",
			"  --radius-sm: 2px",
			"  radius-sm: 2px",
			"must begin with",
		},
		{
			"theme token that is not a custom property",
			"theme.yaml",
			`  --accent: "#3D5A5B"`,
			`  accent: "#3D5A5B"`,
			"must begin with",
		},
		{
			"font stack that breaks out",
			"theme.yaml",
			`    stack: "Outfit, system-ui, sans-serif"`,
			`    stack: "Outfit; color: red"`,
			"font stack contains",
		},
		{
			// Faces are bound to their parent's family name; without one the
			// generated @font-face rules would apply to nothing.
			"faces with no family name",
			"theme.yaml",
			"    family: Outfit\n",
			"",
			"no family name to bind them to",
		},
		{
			"font file that breaks out",
			"theme.yaml",
			"file: outfit-400.woff2",
			"file: \"a.woff2) format('woff2'); } :root{display:none\"",
			"font file contains",
		},
	} {
		t.Run(tc.name, tc.run)
	}
}

func TestCSSUnsafeSequences(t *testing.T) {
	// Every sequence that could terminate a declaration, a rule, a comment, or
	// an inline <style> element.
	//
	// Note the last two: a raw newline inside a double-quoted YAML scalar is
	// folded to a space and never reaches the value, so the only way to smuggle
	// one in is the YAML escape — which is exactly what an attacker would use,
	// and therefore what the test must exercise.
	for _, tc := range []struct {
		name string
		yaml string
	}{
		{"open brace", `red{x`},
		{"close brace", `red} :root{display:none`},
		{"semicolon", `red; color: blue`},
		{"less than", `red<x`},
		{"greater than", `red>x`},
		{"comment open", `red/*`},
		{"comment close", `red*/x`},
		{"escaped newline", `red\nx`},
		{"escaped carriage return", `red\rx`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := bundle()
			src := string(b["theme.yaml"])
			b["theme.yaml"] = []byte(strings.Replace(src,
				`  --accent: "#3D5A5B"`,
				`  --accent: "`+tc.yaml+`"`, 1))

			if _, err := config.Parse(b); err == nil {
				t.Errorf("a token value of %q was accepted", tc.yaml)
			}
		})
	}

	// And a value with none of them is fine.
	b := bundle()
	src := string(b["theme.yaml"])
	b["theme.yaml"] = []byte(strings.Replace(src,
		`  --accent: "#3D5A5B"`,
		`  --accent: "color-mix(in srgb, #3D5A5B 40%, transparent)"`, 1))
	if _, err := config.Parse(b); err != nil {
		t.Errorf("a legitimate color-mix() value was rejected: %v", err)
	}
}

func TestEmptyThemeMapsAreRejected(t *testing.T) {
	// Emptying either token map means removing a whole indented block, which
	// substring replacement cannot do without disturbing its neighbour — so
	// these splice between the block's own key and the next one.
	for _, tc := range []struct {
		name, from, until, want string
	}{
		{"light", "light:", "dark:", "no light theme tokens"},
		{"dark", "dark:", "tokens:", "no dark theme tokens"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := bundle()
			src := string(b["theme.yaml"])

			head, rest, ok := strings.Cut(src, tc.from)
			if !ok {
				t.Fatalf("fixture has no %q block; update the test", tc.from)
			}
			_, tail, ok := strings.Cut(rest, tc.until)
			if !ok {
				t.Fatalf("fixture has no %q key after %q; update the test", tc.until, tc.from)
			}
			b["theme.yaml"] = []byte(head + tc.from + " {}\n" + tc.until + tail)

			_, err := config.Parse(b)
			if err == nil {
				t.Fatalf("a bundle with no %s theme was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not explain the missing %s theme:\n%s", tc.name, err)
			}
		})
	}
}

func TestEmptyFileIsRejected(t *testing.T) {
	b := bundle()
	b["icons.yaml"] = []byte("")

	_, err := config.Parse(b)
	if err == nil {
		t.Fatal("an empty file was accepted")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error does not say the file is empty:\n%s", err)
	}
}

func TestDurationRejectsNonStringNode(t *testing.T) {
	b := bundle()
	b["app.yaml"] = []byte(strings.Replace(validApp,
		"connMaxLifetime: 30m",
		"connMaxLifetime: [1, 2]", 1))

	_, err := config.Parse(b)
	if err == nil {
		t.Fatal("a sequence was accepted as a duration")
	}
	if !strings.Contains(err.Error(), "duration") {
		t.Errorf("error does not mention durations:\n%s", err)
	}
}

func TestExpandEnvRejectsEmptyName(t *testing.T) {
	lookup := func(string) (string, bool) { return "", false }

	if _, err := config.ExpandEnv([]byte("x: ${}\n"), lookup); err == nil {
		t.Error("${} was accepted")
	}
	if _, err := config.ExpandEnv([]byte("x: ${   }\n"), lookup); err == nil {
		t.Error("${   } was accepted")
	}
}

func TestExpandEnvRespectsYAMLStructure(t *testing.T) {
	set := func(k string) (string, bool) {
		switch k {
		case "SET":
			return "resolved", true
		case "QUOTED":
			return `pa"ss\word`, true
		}
		return "", false
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			// Documenting a setting as "supply this with ${DATABASE_URL}" is
			// the natural thing to write and must not have to resolve.
			"comment at line start",
			"# use ${UNSET} here\nkey: value\n",
			"# use ${UNSET} here\nkey: value\n",
		},
		{
			"trailing comment",
			"key: value # or ${UNSET}\n",
			"key: value # or ${UNSET}\n",
		},
		{
			// A # that does not follow whitespace is part of the value — a
			// colour, say — not the start of a comment.
			"hash inside a value is not a comment",
			"colour: ${SET}#FAF9F5\n",
			"colour: resolved#FAF9F5\n",
		},
		{
			"single quotes are literal",
			"key: '${UNSET}'\n",
			"key: '${UNSET}'\n",
		},
		{
			"escaped quote inside single quotes",
			"key: 'it''s ${UNSET}'\nnext: ${SET}\n",
			"key: 'it''s ${UNSET}'\nnext: resolved\n",
		},
		{
			"double quotes expand",
			`key: "${SET}"` + "\n",
			`key: "resolved"` + "\n",
		},
		{
			// A password containing a quote must not terminate the scalar.
			"substitution into double quotes is escaped",
			`key: "${QUOTED}"` + "\n",
			`key: "pa\"ss\\word"` + "\n",
		},
		{
			"escape sequence inside double quotes is preserved",
			`key: "a\"b ${SET}"` + "\n",
			`key: "a\"b resolved"` + "\n",
		},
		{
			"quote state resets each line",
			"key: \"unterminated\nnext: ${SET}\n",
			"key: \"unterminated\nnext: resolved\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := config.ExpandEnv([]byte(tc.in), set)
			if err != nil {
				t.Fatalf("ExpandEnv: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestExpandEnvRejectsNewlineInValue(t *testing.T) {
	// A newline is the one character that could turn a value into new keys.
	lookup := func(string) (string, bool) { return "line1\nkey: injected", true }

	if _, err := config.ExpandEnv([]byte("key: ${EVIL}\n"), lookup); err == nil {
		t.Error("a substituted value containing a newline was accepted")
	}
}

func TestExpandEnvDoesNotReachAcrossLines(t *testing.T) {
	// A stray "${" should fail on its own line rather than swallowing an
	// unrelated closing brace further down the file.
	lookup := func(string) (string, bool) { return "x", true }

	_, err := config.ExpandEnv([]byte("a: ${OPEN\nb: what}\n"), lookup)
	if err == nil {
		t.Error("an unterminated reference reached across a line boundary")
	}
}

func TestExpandEnvHandlesTrailingDollar(t *testing.T) {
	lookup := func(string) (string, bool) { return "", false }

	got, err := config.ExpandEnv([]byte("x: y$"), lookup)
	if err != nil {
		t.Fatalf("ExpandEnv: %v", err)
	}
	if string(got) != "x: y$" {
		t.Errorf("got %q, want %q", got, "x: y$")
	}
}
