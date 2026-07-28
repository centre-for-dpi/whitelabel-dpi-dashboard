package config

import (
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/a11y"
)

// validator accumulates located errors while walking a decoded config.
type validator struct {
	nodes map[string]*yaml.Node
	errs  Errors
}

// errf records a problem at a path within a file.
func (v *validator) errf(file string, path []any, format string, args ...any) {
	v.record(file, NodeAt(v.nodes[file], path...), path, format, args...)
}

// errKey records a problem at a *mapping key* rather than at its value, which
// is where the reader's eye goes for "unknown status name" style errors.
func (v *validator) errKey(file string, path []any, key string, format string, args ...any) {
	v.record(file, KeyNode(v.nodes[file], path, key), append(slices.Clone(path), key), format, args...)
}

func (v *validator) record(file string, n *yaml.Node, path []any, format string, args ...any) {
	e := Error{File: file, Path: PathString(path), Msg: fmt.Sprintf(format, args...)}
	if n != nil {
		e.Line, e.Column = n.Line, n.Column
	}
	v.errs = append(v.errs, e)
}

// oneOf reports an error unless got is in allowed.
func (v *validator) oneOf(file string, path []any, got string, allowed []string, what string) {
	if !slices.Contains(allowed, got) {
		v.errf(file, path, "unknown %s %q; expected one of %v", what, got, allowed)
	}
}

// requireAll reports every member of want that is missing from have.
func (v *validator) requireAll(file string, path []any, have []string, want []string, what string) {
	for _, w := range want {
		if !slices.Contains(have, w) {
			v.errf(file, path, "missing %s %q; all of %v must be present", what, w, want)
		}
	}
}

// requireAllKeys is requireAll for a map.
func (v *validator) requireAllKeys(file string, path []any, have map[string]string, want []string, what string) {
	for _, w := range want {
		if _, ok := have[w]; !ok {
			v.errf(file, path, "missing %s %q; all of %v must be present", what, w, want)
		}
	}
}

// dupCheck reports the second and later appearances of an id.
func (v *validator) dupCheck(file string, path []any, seen map[string]bool, id, what string) {
	if seen[id] {
		v.errf(file, path, "duplicate %s %q", what, id)
		return
	}
	seen[id] = true
}

func validate(cfg Config, nodes map[string]*yaml.Node) Errors {
	v := &validator{nodes: nodes}

	iconKeys := make([]string, 0, len(cfg.Icons.Icons))
	for k := range cfg.Icons.Icons {
		iconKeys = append(iconKeys, k)
	}
	slices.Sort(iconKeys) // deterministic error text

	v.validateIcons(cfg.Icons)
	v.validateApp(cfg.App)
	v.validateBrand(cfg.Brand, iconKeys)
	v.validateDomain(cfg.Domain, iconKeys)
	v.validateTheme(cfg.Theme)

	return v.errs
}

func (v *validator) validateIcons(ic Icons) {
	const f = FileIcons

	if len(ic.Icons) == 0 {
		v.errf(f, []any{"icons"}, "no icons declared; templates reference icons by role and cannot fall back")
		return
	}
	keys := make([]string, 0, len(ic.Icons))
	for k := range ic.Icons {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	for _, k := range keys {
		i := ic.Icons[k]
		if i.Glyph == "" && i.SVG == "" {
			v.errKey(f, []any{"icons"}, k, "icon %q sets neither glyph nor svg", k)
		}
		if i.Glyph != "" && i.SVG != "" {
			v.errKey(f, []any{"icons"}, k, "icon %q sets both glyph and svg; set exactly one", k)
		}
	}
}

func (v *validator) validateApp(a App) {
	const f = FileApp

	if a.Server.Addr == "" {
		v.errf(f, []any{"server", "addr"}, "server address is empty; set something like \":8080\"")
	}
	v.oneOf(f, []any{"client", "runtime"}, a.Client.Runtime, []string{RuntimeVanilla, RuntimeAlpine}, "client runtime")
	v.oneOf(f, []any{"log", "level"}, a.Log.Level, []string{"debug", "info", "warn", "error"}, "log level")
	v.oneOf(f, []any{"log", "format"}, a.Log.Format, []string{"text", "json"}, "log format")

	drivers := []string{DriverSQLite, DriverPostgres, DriverMySQL, DriverMariaDB, DriverMemory}
	v.oneOf(f, []any{"storage", "driver"}, a.Storage.Driver, drivers, "storage driver")

	// A network database with no DSN cannot be reached; sqlite and memory both
	// have workable defaults.
	switch a.Storage.Driver {
	case DriverPostgres, DriverMySQL, DriverMariaDB:
		if a.Storage.DSN == "" {
			v.errf(f, []any{"storage", "dsn"}, "driver %q needs a dsn", a.Storage.Driver)
		}
	}

	if a.Storage.MaxOpenConns < 0 {
		v.errf(f, []any{"storage", "maxOpenConns"}, "must not be negative, got %d", a.Storage.MaxOpenConns)
	}
	if a.Storage.MaxIdleConns < 0 {
		v.errf(f, []any{"storage", "maxIdleConns"}, "must not be negative, got %d", a.Storage.MaxIdleConns)
	}

	h := a.Storage.History
	if h.RetentionDays <= 0 {
		v.errf(f, []any{"storage", "history", "retentionDays"}, "must be positive, got %d; charts would have nothing to draw", h.RetentionDays)
	}
	if h.RawSampleRetentionHours <= 0 {
		v.errf(f, []any{"storage", "history", "rawSampleRetentionHours"}, "must be positive, got %d", h.RawSampleRetentionHours)
	}
	if h.RollupIntervalMinutes <= 0 {
		v.errf(f, []any{"storage", "history", "rollupIntervalMinutes"}, "must be positive, got %d; samples would never be folded into daily buckets", h.RollupIntervalMinutes)
	}
}

func (v *validator) validateBrand(b Brand, iconKeys []string) {
	const f = FileBrand

	if b.WordmarkTermID == "" {
		v.errf(f, []any{"wordmarkTermId"}, "is empty; the dashboard would render with no name")
	}
	if b.IconKey != "" && !slices.Contains(iconKeys, b.IconKey) {
		v.errf(f, []any{"iconKey"}, "unknown icon %q; declare it in %s", b.IconKey, FileIcons)
	}
	for i, l := range b.Footer {
		if l.Href == "" {
			v.errf(f, []any{"footer", i, "href"}, "footer link has no href")
		}
		if l.TermID == "" {
			v.errf(f, []any{"footer", i, "termId"}, "footer link has no termId, so it would render as an empty link")
		}
	}
}

func (v *validator) validateDomain(d Domain, iconKeys []string) {
	const f = FileDomain

	if len(d.Scopes) == 0 {
		v.errf(f, []any{"scopes"}, "no scopes declared")
	}
	if !slices.Contains(d.Scopes, d.DefaultScope) {
		v.errf(f, []any{"defaultScope"}, "unknown scope %q; expected one of %v", d.DefaultScope, d.Scopes)
	}
	if d.OnboardedDenominator <= 0 {
		v.errf(f, []any{"onboardedDenominator"}, "must be positive, got %d; it is the denominator of the coverage line", d.OnboardedDenominator)
	}

	periodIDs := make([]string, 0, len(d.Periods))
	seenPeriod := map[string]bool{}
	for i, p := range d.Periods {
		if p.ID == "" {
			v.errf(f, []any{"periods", i, "id"}, "period has no id")
			continue
		}
		v.dupCheck(f, []any{"periods", i, "id"}, seenPeriod, p.ID, "period id")
		if p.Days <= 0 {
			v.errf(f, []any{"periods", i, "days"}, "period %q has days %d; it must be positive to compute a trend", p.ID, p.Days)
		}
		periodIDs = append(periodIDs, p.ID)
	}
	if len(d.Periods) == 0 {
		v.errf(f, []any{"periods"}, "no periods declared")
	} else if !slices.Contains(periodIDs, d.DefaultPeriod) {
		v.errf(f, []any{"defaultPeriod"}, "unknown period %q; expected one of %v", d.DefaultPeriod, periodIDs)
	}

	categoryIDs := v.validateTaxonomy(d, iconKeys)
	metricIDs := v.validateMetrics(d)
	v.validateThresholdsAndStatus(d, iconKeys)
	v.validateSignals(d, categoryIDs, metricIDs, iconKeys)
}

func (v *validator) validateTaxonomy(d Domain, iconKeys []string) []string {
	const f = FileDomain

	var categoryIDs []string
	seenCat := map[string]bool{}
	for i, c := range d.Taxonomy.Categories {
		p := []any{"taxonomy", "categories", i}
		if c.ID == "" {
			v.errf(f, append(p, "id"), "category has no id")
			continue
		}
		v.dupCheck(f, append(p, "id"), seenCat, c.ID, "category id")
		if c.IconKey != "" && !slices.Contains(iconKeys, c.IconKey) {
			v.errf(f, append(p, "iconKey"), "unknown icon %q; declare it in %s", c.IconKey, FileIcons)
		}
		categoryIDs = append(categoryIDs, c.ID)
	}
	if len(d.Taxonomy.Categories) == 0 {
		v.errf(f, []any{"taxonomy", "categories"}, "no categories declared")
	}

	seenReg := map[string]bool{}
	for i, r := range d.Taxonomy.Regions {
		p := []any{"taxonomy", "regions", i}
		if r.ID == "" {
			v.errf(f, append(p, "id"), "region has no id")
			continue
		}
		v.dupCheck(f, append(p, "id"), seenReg, r.ID, "region id")
		if !slices.Contains(d.Scopes, r.Scope) {
			v.errf(f, append(p, "scope"), "region %q is in unknown scope %q; expected one of %v", r.ID, r.Scope, d.Scopes)
		}
	}
	if len(d.Taxonomy.Regions) == 0 {
		v.errf(f, []any{"taxonomy", "regions"}, "no regions declared")
	}

	seenProv := map[string]bool{}
	for i, p := range d.Taxonomy.Providers {
		path := []any{"taxonomy", "providers", i}
		if p.ID == "" {
			v.errf(f, append(path, "id"), "provider has no id")
			continue
		}
		v.dupCheck(f, append(path, "id"), seenProv, p.ID, "provider id")
	}

	return categoryIDs
}

func (v *validator) validateMetrics(d Domain) []string {
	const f = FileDomain

	fields := []string{FieldAvailability, FieldErrorRate, FieldLatencyP50, FieldVolume}
	units := []string{UnitPercent, UnitMillisecond, UnitCount}
	directions := []string{DirectionHigherIsBetter, DirectionLowerIsBetter, DirectionNeutral}

	var metricIDs []string
	seen := map[string]bool{}
	for i, m := range d.Metrics {
		p := []any{"metrics", i}
		if m.ID == "" {
			v.errf(f, append(p, "id"), "metric has no id")
			continue
		}
		v.dupCheck(f, append(p, "id"), seen, m.ID, "metric id")
		metricIDs = append(metricIDs, m.ID)

		v.oneOf(f, append(p, "field"), m.Field, fields, "metric field")
		v.oneOf(f, append(p, "unit"), m.Unit, units, "metric unit")
		v.oneOf(f, append(p, "direction"), m.Direction, directions, "metric direction")

		if m.Precision < 0 {
			v.errf(f, append(p, "precision"), "must not be negative, got %d", m.Precision)
		}
		// A metric with neither a target nor a framing has nothing to say
		// beyond its raw number, and the demo deliberately declines to render
		// one. Catching it here beats a silently blank tile.
		if m.Target == nil && m.Framing == "" && m.ShowInLeaderboard {
			v.errf(f, p, "metric %q is shown in the leaderboard but has neither a target nor a framing, so it has no context to render", m.ID)
		}
	}
	if len(d.Metrics) == 0 {
		v.errf(f, []any{"metrics"}, "no metrics declared")
	}

	// Resolved in a second pass so that forward references are legal.
	for i, m := range d.Metrics {
		if m.DenominatorOf != "" && !slices.Contains(metricIDs, m.DenominatorOf) {
			v.errf(f, []any{"metrics", i, "denominatorOf"}, "unknown metric %q", m.DenominatorOf)
		}
	}

	return metricIDs
}

func (v *validator) validateThresholdsAndStatus(d Domain, iconKeys []string) {
	const f = FileDomain

	for i, s := range d.Thresholds.EvaluationOrder {
		v.oneOf(f, []any{"thresholds", "evaluationOrder", i}, s, Statuses, "status")
	}
	v.requireAll(f, []any{"thresholds", "evaluationOrder"}, d.Thresholds.EvaluationOrder, Statuses, "status")
	v.requireAllKeys(f, []any{"thresholds", "prose"}, d.Thresholds.Prose, Statuses, "status")
	for k := range d.Thresholds.Prose {
		if !slices.Contains(Statuses, k) {
			v.errKey(f, []any{"thresholds", "prose"}, k, "unknown status %q; expected one of %v", k, Statuses)
		}
	}

	tv := d.Thresholds.Values
	if tv.PartialAvailBelow < tv.MajorAvailBelow {
		v.errf(f, []any{"thresholds", "values", "partialAvailBelow"},
			"partial threshold %.2f is stricter than the major threshold %.2f, so no service can ever be merely partial",
			tv.PartialAvailBelow, tv.MajorAvailBelow)
	}
	if tv.PartialErrAbove > tv.MajorErrAbove {
		v.errf(f, []any{"thresholds", "values", "partialErrAbove"},
			"partial error threshold %.2f is above the major threshold %.2f, so no service can ever be merely partial",
			tv.PartialErrAbove, tv.MajorErrAbove)
	}
	if tv.StaleSecondsAbove <= 0 {
		v.errf(f, []any{"thresholds", "values", "staleSecondsAbove"}, "must be positive, got %d; every service would read as stale", tv.StaleSecondsAbove)
	}

	sm := d.StatusModel
	for i, s := range sm.Order {
		v.oneOf(f, []any{"statusModel", "order", i}, s, Statuses, "status")
	}
	v.requireAll(f, []any{"statusModel", "order"}, sm.Order, Statuses, "status")

	for _, s := range Statuses {
		if _, ok := sm.Severity[s]; !ok {
			v.errf(f, []any{"statusModel", "severity"}, "missing status %q; all of %v must be present", s, Statuses)
		}
		if _, ok := sm.LabelTermID[s]; !ok {
			v.errf(f, []any{"statusModel", "labelTermId"}, "missing status %q; all of %v must be present", s, Statuses)
		}
	}
	v.requireAllKeys(f, []any{"statusModel", "iconKey"}, sm.IconKey, Statuses, "status")
	for s, key := range sm.IconKey {
		if !slices.Contains(Statuses, s) {
			v.errKey(f, []any{"statusModel", "iconKey"}, s, "unknown status %q; expected one of %v", s, Statuses)
			continue
		}
		if !slices.Contains(iconKeys, key) {
			v.errKey(f, []any{"statusModel", "iconKey"}, s, "unknown icon %q; declare it in %s", key, FileIcons)
		}
	}
}

func (v *validator) validateSignals(d Domain, categoryIDs, metricIDs, iconKeys []string) {
	const f = FileDomain
	_ = metricIDs

	kinds := []string{
		SignalBelowTargetDays,
		SignalErrorRisingCategories,
		SignalLongestOpenIncident,
		SignalMaintenanceActive,
	}

	// The card shown when nothing fired is configured too: "nothing to report"
	// is a statement the deployment makes, in its own words.
	e := d.SignalsEmpty
	if e.TermID == "" {
		v.errf(f, []any{"signalsEmpty", "termId"}, "no term for the empty-signals card; with no signal firing the section would render blank")
	}
	if e.IconKey == "" || !slices.Contains(iconKeys, e.IconKey) {
		v.errf(f, []any{"signalsEmpty", "iconKey"}, "unknown icon %q; declare it in %s", e.IconKey, FileIcons)
	}
	v.oneOf(f, []any{"signalsEmpty", "tone"}, e.Tone, Tones, "tone")

	seen := map[string]bool{}
	for i, s := range d.Signals {
		p := []any{"signals", i}
		if s.ID == "" {
			v.errf(f, append(p, "id"), "signal has no id")
			continue
		}
		v.dupCheck(f, append(p, "id"), seen, s.ID, "signal id")
		v.oneOf(f, append(p, "kind"), s.Kind, kinds, "signal kind")

		// The demo hardcoded each signal's glyph and colour by signal id inside
		// the component, which meant adding a signal meant editing code. Both
		// are configuration here.
		if s.IconKey == "" || !slices.Contains(iconKeys, s.IconKey) {
			v.errf(f, append(p, "iconKey"), "unknown icon %q; declare it in %s", s.IconKey, FileIcons)
		}
		v.oneOf(f, append(p, "tone"), s.Tone, Tones, "tone")

		switch s.Kind {
		case SignalBelowTargetDays:
			if s.Days <= 0 {
				v.errf(f, append(p, "days"), "signal %q needs a positive days window, got %d", s.ID, s.Days)
			}
		case SignalErrorRisingCategories:
			if s.MinCategory <= 0 {
				v.errf(f, append(p, "minCategories"), "signal %q needs a positive minCategories, got %d", s.ID, s.MinCategory)
			}
		}

		if s.TitleTermID == "" {
			v.errf(f, append(p, "titleTermId"), "signal %q has no title term", s.ID)
		}
		// Every signal card prints the literal rule it applied; without a rule
		// term the card would assert a finding with no stated basis.
		if s.RuleTermID == "" {
			v.errf(f, append(p, "ruleTermId"), "signal %q has no rule term, so its card would state a finding without stating the rule", s.ID)
		}

		if s.Filter == nil {
			continue
		}
		for j, st := range s.Filter.Status {
			v.oneOf(f, append(p, "filter", "status", j), st, Statuses, "status")
		}
		for j, c := range s.Filter.Category {
			if !slices.Contains(categoryIDs, c) {
				v.errf(f, append(p, "filter", "category", j), "unknown category %q", c)
			}
		}
	}
}

// cssUnsafe reports the first sequence in s that could break out of the CSS
// declaration it will be written into.
//
// Theme values are interpolated verbatim into a generated stylesheet, so this
// is the boundary where a stray brace or comment must be caught. Validating
// here rather than escaping later keeps the generator a total function and puts
// the error where the reader can act on it: in their own theme file.
func cssUnsafe(s string) string {
	for _, bad := range []string{"{", "}", ";", "<", ">", "/*", "*/", "\n", "\r"} {
		if strings.Contains(s, bad) {
			return bad
		}
	}
	return ""
}

func (v *validator) checkCSSValue(path []any, what, s string) {
	if bad := cssUnsafe(s); bad != "" {
		v.errf(FileTheme, path, "%s contains %q, which would break out of the generated stylesheet", what, bad)
	}
}

func (v *validator) validateTheme(t Theme) {
	const f = FileTheme

	for _, mode := range []struct {
		name   string
		tokens map[string]string
	}{
		{"light", t.Light},
		{"dark", t.Dark},
	} {
		if len(mode.tokens) == 0 {
			v.errf(f, []any{mode.name}, "no %s theme tokens declared", mode.name)
			continue
		}
		for _, want := range RequiredThemeTokens {
			if _, ok := mode.tokens[want]; !ok {
				v.errf(f, []any{mode.name}, "missing required token %q in the %s theme", want, mode.name)
			}
		}
		// Iterated in sorted order so the error list is stable across runs.
		for _, name := range sortedKeys(mode.tokens) {
			if !strings.HasPrefix(name, "--") {
				v.errKey(f, []any{mode.name}, name, "token %q is not a CSS custom property; names must begin with \"--\"", name)
			}
			v.checkCSSValue([]any{mode.name, name}, "token value", mode.tokens[name])
		}

		// WCAG contrast. A white-label dashboard hands its palette to every
		// deployment, so this is the only moment anyone can be sure the
		// combinations the templates actually produce are legible. Reported
		// against the offending token so the reader is taken to the line they
		// have to change, not to the pair.
		for _, finding := range a11y.Check(mode.name, mode.tokens) {
			v.errKey(f, []any{mode.name}, finding.Fg, "%s", finding)
		}
	}

	for _, name := range sortedKeys(t.Tokens) {
		if !strings.HasPrefix(name, "--") {
			v.errKey(f, []any{"tokens"}, name, "token %q is not a CSS custom property; names must begin with \"--\"", name)
		}
		v.checkCSSValue([]any{"tokens", name}, "token value", t.Tokens[name])
	}

	for _, fam := range []struct {
		name string
		fam  FontFamily
	}{
		{"body", t.Fonts.Body},
		{"serif", t.Fonts.Serif},
	} {
		if fam.fam.Stack == "" {
			v.errf(f, []any{"fonts", fam.name, "stack"}, "font family %q has no CSS stack", fam.name)
		}
		v.checkCSSValue([]any{"fonts", fam.name, "stack"}, "font stack", fam.fam.Stack)
		v.checkCSSValue([]any{"fonts", fam.name, "family"}, "font family name", fam.fam.Family)

		for i, face := range fam.fam.Faces {
			p := []any{"fonts", fam.name, "faces", i}
			switch {
			case face.File == "" && face.URL == "":
				v.errf(f, p, "font face sets neither file nor url; set exactly one")
			case face.File != "" && face.URL != "":
				v.errf(f, p, "font face sets both file and url; set exactly one")
			}
			// A face declares a family only implicitly, so an empty family on
			// the parent would emit @font-face rules bound to nothing.
			if fam.fam.Family == "" {
				v.errf(f, []any{"fonts", fam.name, "family"}, "font family %q declares faces but has no family name to bind them to", fam.name)
				break
			}
			v.checkCSSValue(append(p, "file"), "font file", face.File)
			v.checkCSSValue(append(p, "url"), "font url", face.URL)
			v.checkCSSValue(append(p, "weight"), "font weight", face.Weight)
			v.checkCSSValue(append(p, "style"), "font style", face.Style)
			v.checkCSSValue(append(p, "display"), "font display", face.Display)
			v.checkCSSValue(append(p, "unicodeRange"), "unicode range", face.UnicodeRange)
		}
	}
}

// sortedKeys returns a map's keys in a deterministic order, so that error lists
// and generated stylesheets do not shuffle between runs.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
