// Package config defines the white-label contract: everything domain-specific
// lives here as data, so retargeting the dashboard to a new country, a new
// brand, or a non-government domain is a config change rather than a code
// change. No package downstream of this one may hardcode a domain noun.
//
// Parsing and validation are pure functions over bytes. Reading files from disk
// happens in Load, which is the only part of this package that touches I/O.
package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that reads from YAML as a Go duration string,
// e.g. "30m". Bare numbers are rejected, because "30" is ambiguous between
// seconds and nanoseconds and silently picking one has burned people before.
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	// The yaml error is deliberately not wrapped. yaml.v3 unwraps a *TypeError
	// returned from an unmarshaler and reports only its inner message, which
	// here would be the unhelpful "cannot unmarshal !!seq into string". A fresh
	// error survives intact and can name the field's actual expectation.
	if err := n.Decode(&s); err != nil {
		return fmt.Errorf("expected a duration string such as \"30m\", got %s", n.Tag)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// Std returns the underlying time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// --- app.yaml --------------------------------------------------------------

// App is process-level configuration: how to serve, where to store, how to log.
type App struct {
	Server  Server  `yaml:"server"`
	Client  Client  `yaml:"client"`
	Storage Storage `yaml:"storage"`
	Log     Log     `yaml:"log"`
}

// Server configures the HTTP listener.
type Server struct {
	Addr          string   `yaml:"addr"`
	ReadTimeout   Duration `yaml:"readTimeout"`
	WriteTimeout  Duration `yaml:"writeTimeout"`
	IdleTimeout   Duration `yaml:"idleTimeout"`
	ShutdownGrace Duration `yaml:"shutdownGrace"`
	// BaseURL is used to build absolute links. Empty means derive per request.
	BaseURL string `yaml:"baseURL"`
}

// Client selects the browser-side runtime. Only "vanilla" is implemented; the
// key exists so that adopting Alpine is a config change rather than a rewrite.
type Client struct {
	Runtime string `yaml:"runtime"`
}

// Log configures structured logging.
type Log struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // text | json
}

// Storage selects and tunes the persistence backend.
type Storage struct {
	Driver          string   `yaml:"driver"` // sqlite | postgres | mysql | mariadb | memory
	DSN             string   `yaml:"dsn"`
	MaxOpenConns    int      `yaml:"maxOpenConns"`
	MaxIdleConns    int      `yaml:"maxIdleConns"`
	ConnMaxLifetime Duration `yaml:"connMaxLifetime"`
	History         History  `yaml:"history"`
}

// History bounds how much time-series data is kept and how often raw samples
// are folded into daily buckets.
type History struct {
	RetentionDays           int `yaml:"retentionDays"`
	RawSampleRetentionHours int `yaml:"rawSampleRetentionHours"`
	RollupIntervalMinutes   int `yaml:"rollupIntervalMinutes"`
}

// Storage driver names.
const (
	DriverSQLite   = "sqlite"
	DriverPostgres = "postgres"
	DriverMySQL    = "mysql"
	DriverMariaDB  = "mariadb"
	DriverMemory   = "memory"
)

// Client runtime names.
const (
	RuntimeVanilla = "vanilla"
	RuntimeAlpine  = "alpine"
)

// --- brand.yaml ------------------------------------------------------------

// Brand is the visible identity: what the dashboard calls itself and who it
// credits.
type Brand struct {
	WordmarkTermID string       `yaml:"wordmarkTermId"`
	TaglineTermID  string       `yaml:"taglineTermId"`
	IconKey        string       `yaml:"iconKey"`
	Favicon        string       `yaml:"favicon"`
	Footer         []FooterLink `yaml:"footer"`
}

// FooterLink is one credit or source link in the page footer.
type FooterLink struct {
	TermID   string `yaml:"termId"`
	Href     string `yaml:"href"`
	External bool   `yaml:"external"`
}

// --- domain.yaml -----------------------------------------------------------

// Domain is the subject matter: what is being measured, how it is grouped, and
// what counts as working.
type Domain struct {
	H1TermID             string   `yaml:"h1TermId"`
	DataSourceTermID     string   `yaml:"dataSourceTermId"`
	DataSourceHref       string   `yaml:"dataSourceHref"`
	OnboardedDenominator int      `yaml:"onboardedDenominator"`
	Scopes               []string `yaml:"scopes"`
	DefaultScope         string   `yaml:"defaultScope"`
	Periods              []Period `yaml:"periods"`
	DefaultPeriod        string   `yaml:"defaultPeriod"`

	// Roles are the sides of the exchange the deployment reports on: who issues
	// a document, and who requests one. A deployment with only one side
	// declares one role and never renders a switch.
	//
	// The role narrows the population the board is computed over, exactly as
	// scope does — the two are the same kind of thing, and a reader looking at
	// requestors should see requestors ranked against requestors.
	Roles       []Role `yaml:"roles"`
	DefaultRole string `yaml:"defaultRole"`

	// Platforms are the exchanges this dashboard reports on, named and linked
	// so a reader can tell whose numbers these are. A deployment covering one
	// platform lists one; a deployment covering none lists none and the strip
	// does not render.
	Platforms []Platform `yaml:"platforms"`

	Taxonomy    Taxonomy    `yaml:"taxonomy"`
	Metrics     []Metric    `yaml:"metrics"`
	Thresholds  Thresholds  `yaml:"thresholds"`
	StatusModel StatusModel `yaml:"statusModel"`
	Signals     []Signal    `yaml:"signals"`
	// Opportunities are the demand-side signals: the same contract, looking for
	// issuance nobody is consuming rather than service nobody is getting.
	Opportunities     []Signal    `yaml:"opportunities"`
	SignalsEmpty      SignalEmpty `yaml:"signalsEmpty"`
	OpportunitiesNone SignalEmpty `yaml:"opportunitiesEmpty"`
}

// Period is a selectable comparison window. Days is the lookback used for trend
// deltas — it is data rather than a parsed suffix so that a deployment can add
// "quarter" or "sinceLaunch" without a code change.
type Period struct {
	ID     string `yaml:"id"`
	TermID string `yaml:"termId"`
	Days   int    `yaml:"days"`
}

// Role is one side of the exchange, and how the switch between sides reads.
type Role struct {
	ID     string `yaml:"id"`
	TermID string `yaml:"termId"`
	// CaptionTermID is what the board's caption says while this role is on
	// view: the ranking rule is the same, but what is being ranked is not.
	CaptionTermID string `yaml:"captionTermId"`
}

// Platform is one exchange the dashboard covers.
type Platform struct {
	ID     string `yaml:"id"`
	TermID string `yaml:"termId"`
	Href   string `yaml:"href"`
	// Logo is a path under the asset root. A wordmark travels better as a file
	// the theme pipeline can serve and a browser can cache than as a data URI
	// baked into the config, which is what the prototype had to do to stay a
	// single file.
	Logo string `yaml:"logo"`
	// LogoWidth and LogoHeight are the mark's intrinsic size, so the row does
	// not reflow while the image loads.
	LogoWidth  int `yaml:"logoWidth"`
	LogoHeight int `yaml:"logoHeight"`
}

// Taxonomy groups services four ways: by what the citizen wants (category), by
// where they are (region), by who runs it (provider), and — for the demand side
// — by what line of business is asking (sector).
type Taxonomy struct {
	Categories []Category `yaml:"categories"`
	Regions    []Region   `yaml:"regions"`
	Providers  []Provider `yaml:"providers"`
	// Sectors group requestors by what they do rather than by who they are: a
	// bank and a lender pulling the same document are two sectors making the
	// same request, and that is the shape of the demand a publisher needs.
	Sectors []Sector `yaml:"sectors"`
}

// Sector is a requestor's line of business.
type Sector struct {
	ID     string `yaml:"id"`
	TermID string `yaml:"termId"`
}

// Category is a demand-side grouping — what task the citizen is trying to do.
type Category struct {
	ID      string `yaml:"id"`
	TermID  string `yaml:"termId"`
	IconKey string `yaml:"iconKey"`
}

// Region is a geographic grouping, bound to one scope.
type Region struct {
	ID     string `yaml:"id"`
	TermID string `yaml:"termId"`
	Scope  string `yaml:"scope"`
}

// Provider is the organisation operating a service.
type Provider struct {
	ID     string `yaml:"id"`
	TermID string `yaml:"termId"`
}

// Metric describes one measurable quantity and how to present it.
//
// Field names which model field supplies the value. It is explicit rather than
// inferred from ID so that a deployment can rename a metric for its audience
// without breaking the binding.
type Metric struct {
	ID                string   `yaml:"id"`
	TermID            string   `yaml:"termId"`
	Field             string   `yaml:"field"`
	Unit              string   `yaml:"unit"`
	Precision         int      `yaml:"precision"`
	Target            *float64 `yaml:"target"`
	Direction         string   `yaml:"direction"`
	ShowInLeaderboard bool     `yaml:"showInLeaderboard"`
	// ShowInDrawer puts the metric on the open service's tile grid. It is a
	// separate switch from the board's because the two surfaces answer
	// different questions, and a deployment may reasonably rank on one figure
	// and explain with another.
	ShowInDrawer  bool   `yaml:"showInDrawer"`
	Framing       string `yaml:"framing"`
	DenominatorOf string `yaml:"denominatorOf"`
	// ComplementOf names a metric this one is the arithmetic complement of:
	// downtime is what is left of a hundred per cent after availability.
	//
	// The point is the target. A complement's target is DERIVED from the other
	// metric's, so declaring "availability should reach 99.5%" and "downtime
	// should stay under 0.5%" cannot become two numbers that disagree — which is
	// what happens the first time somebody raises one and forgets the other. Set
	// target on the metric being complemented, never on the complement.
	ComplementOf string `yaml:"complementOf"`
}

// Complement returns the value of a metric that is the complement of v.
//
// Percentages only, because a complement is only meaningful against a known
// whole and a hundred per cent is the only whole this vocabulary has.
func Complement(v float64) float64 { return 100 - v }

// ResolvedTarget is the metric's target, derived if it is a complement.
//
// Callers must use this rather than reading Target directly, or a complement will
// appear to have no target at all.
func (d Domain) ResolvedTarget(m Metric) *float64 {
	if m.ComplementOf == "" {
		return m.Target
	}
	for _, other := range d.Metrics {
		if other.ID == m.ComplementOf && other.Target != nil {
			t := Complement(*other.Target)
			return &t
		}
	}
	return nil
}

// Metric framings — what gives a figure with no target its context.
const (
	// FramingDenominator renders the metric as the share of a pair of counts,
	// framed by the counts themselves.
	FramingDenominator = "denominator"
)

// Metric field names, matching model.Service.Metrics.
const (
	FieldAvailability = "availability"
	// FieldDowntime is derived, not stored: no source reports it, and a source
	// that did could disagree with the availability beside it.
	FieldDowntime   = "downtime"
	FieldErrorRate  = "errorRate"
	FieldLatencyP50 = "latencyP50"
	FieldVolume     = "volume"
)

// Metric units.
const (
	UnitPercent     = "percent"
	UnitMillisecond = "millisecond"
	UnitCount       = "count"
)

// Metric directions: which way is good.
const (
	DirectionHigherIsBetter = "higher-is-better"
	DirectionLowerIsBetter  = "lower-is-better"
	DirectionNeutral        = "neutral"
)

// Thresholds is the single source of truth for what counts as working.
//
// Both the status evaluator and the on-screen prose read these same numbers, so
// the published rule and the applied rule cannot drift apart.
type Thresholds struct {
	EvaluationOrder []string          `yaml:"evaluationOrder"`
	Values          ThresholdValues   `yaml:"values"`
	Prose           map[string]string `yaml:"prose"`
}

// ThresholdValues are the boundaries themselves.
type ThresholdValues struct {
	MajorAvailBelow   float64 `yaml:"majorAvailBelow"`
	MajorErrAbove     float64 `yaml:"majorErrAbove"`
	PartialAvailBelow float64 `yaml:"partialAvailBelow"`
	PartialErrAbove   float64 `yaml:"partialErrAbove"`
	StaleSecondsAbove int64   `yaml:"staleSecondsAbove"`
}

// StatusModel styles and orders the fixed status vocabulary. The set of
// statuses is not extensible — the evaluation rules are tied to them — but
// every label, icon, ordering and severity here is a deployment's to choose.
type StatusModel struct {
	Order       []string          `yaml:"order"`
	Severity    map[string]int    `yaml:"severity"`
	IconKey     map[string]string `yaml:"iconKey"`
	LabelTermID map[string]string `yaml:"labelTermId"`
}

// Signal is one rule that, when it fires, surfaces a card telling the reader
// what changed and why. Each prints the literal rule it applied.
type Signal struct {
	ID          string        `yaml:"id"`
	Kind        string        `yaml:"kind"`
	TitleTermID string        `yaml:"titleTermId"`
	RuleTermID  string        `yaml:"ruleTermId"`
	IconKey     string        `yaml:"iconKey"`
	Tone        string        `yaml:"tone"`
	Days        int           `yaml:"days"`
	MinCategory int           `yaml:"minCategories"`
	Filter      *SignalFilter `yaml:"filter"`
	// Concentration is the share of a group one member must account for before
	// the group counts as concentrated, in percent.
	Concentration float64 `yaml:"concentration"`
	// ActionTermID overrides the shared "view affected" label. An opportunity
	// leads somewhere different from a fault — to a list of documents, or to a
	// trend — and saying so is the difference between a link and a guess.
	ActionTermID string `yaml:"actionTermId"`
	// FocusTab names the drawer pane a single-service finding opens on.
	FocusTab string `yaml:"focusTab"`
}

// SignalEmpty is the card shown when no signal fired. It is configured rather
// than hardcoded for the same reason every other card is: "nothing to report"
// is a statement the deployment makes, in its own words.
type SignalEmpty struct {
	TermID  string `yaml:"termId"`
	IconKey string `yaml:"iconKey"`
	Tone    string `yaml:"tone"`
}

// Tones name a semantic colour pair rather than a colour. A tone resolves to
// the --status-<tone> and --status-<tone>-bg custom properties, or to --accent,
// so a deployment restyles every signal card by editing theme.yaml alone.
var Tones = []string{"ok", "partial", "major", "unknown", "maintenance", "accent"}

// Signal kinds. Each corresponds to one evaluator in package rules.
const (
	SignalBelowTargetDays       = "belowTargetDays"
	SignalErrorRisingCategories = "errorRisingCategories"
	SignalLongestOpenIncident   = "longestOpenIncident"
	SignalMaintenanceActive     = "maintenanceActive"
)

// Opportunity kinds: the demand-side mirror. Same contract as the attention
// rules — each card prints the rule it applied — but what they look for is
// issuance nobody is consuming rather than service nobody is getting.
const (
	// OpportunityIssuedAtScaleRarelyRequested finds documents in the top
	// quartile of issuance and the bottom quartile of requestor count.
	OpportunityIssuedAtScaleRarelyRequested = "issuedAtScaleRarelyRequested"
	// OpportunitySingleRequestorCategories finds categories where one
	// requestor accounts for almost all of the pulls.
	OpportunitySingleRequestorCategories = "singleRequestorCategories"
	// OpportunityZeroRequestors finds live APIs nobody has onboarded against.
	OpportunityZeroRequestors = "zeroRequestors"
	// OpportunityFastestGrowingDemand finds the issuer whose requestors are
	// growing fastest.
	OpportunityFastestGrowingDemand = "fastestGrowingDemand"
)

// SignalKinds and OpportunityKinds are every kind, for validation and for the
// generated schema.
var SignalKinds = []string{
	SignalBelowTargetDays, SignalErrorRisingCategories,
	SignalLongestOpenIncident, SignalMaintenanceActive,
}

// OpportunityKinds is the demand-side set.
var OpportunityKinds = []string{
	OpportunityIssuedAtScaleRarelyRequested, OpportunitySingleRequestorCategories,
	OpportunityZeroRequestors, OpportunityFastestGrowingDemand,
}

// SignalFilter is what the signal card's action applies to the leaderboard.
type SignalFilter struct {
	Status   []string `yaml:"status"`
	Category []string `yaml:"category"`
	// Role switches the board to the side the finding is about, because half
	// the demand-side findings are about services the current board does not
	// contain.
	Role string `yaml:"role"`
	// IDs are set by the evaluator rather than configured: some findings are
	// about a set of services with nothing else in common, and the only honest
	// filter is the set itself.
	IDs []string `yaml:"-"`
}

// --- theme.yaml ------------------------------------------------------------

// Theme is presentation tokens only. It declares values; it adds no selectors,
// so a deployment cannot accidentally restructure the page from here.
type Theme struct {
	Light  map[string]string `yaml:"light"`
	Dark   map[string]string `yaml:"dark"`
	Tokens map[string]string `yaml:"tokens"`
	Fonts  Fonts             `yaml:"fonts"`
}

// Fonts binds the two type roles the templates use.
type Fonts struct {
	Body  FontFamily `yaml:"body"`
	Serif FontFamily `yaml:"serif"`
}

// FontFamily is a CSS stack plus any self-hosted faces to declare for it.
type FontFamily struct {
	Family string     `yaml:"family"`
	Stack  string     `yaml:"stack"`
	Faces  []FontFace `yaml:"faces"`
}

// FontFace is one @font-face declaration. Exactly one of File or URL is set:
// File is served from the static asset tree, URL is fetched by the browser.
type FontFace struct {
	File         string `yaml:"file"`
	URL          string `yaml:"url"`
	Weight       string `yaml:"weight"`
	Style        string `yaml:"style"`
	Display      string `yaml:"display"`
	UnicodeRange string `yaml:"unicodeRange"`
}

// RequiredThemeTokens must be present in both the light and dark maps. Extra
// tokens are allowed and are emitted verbatim, so a deployment can introduce
// its own custom properties for use in its own widget templates.
var RequiredThemeTokens = []string{
	"--bg", "--card", "--fg", "--muted-fg",
	"--border-subtle", "--border-strong",
	"--primary", "--primary-fg", "--accent",
	"--status-ok", "--status-ok-bg",
	"--status-partial", "--status-partial-bg",
	"--status-major", "--status-major-bg",
	"--status-unknown", "--status-unknown-bg",
	"--status-maintenance", "--status-maintenance-bg",
}

// --- icons.yaml ------------------------------------------------------------

// Icons is the semantic icon set. Templates reference icons by role — never by
// glyph — so a deployment can swap text glyphs for SVG without touching markup.
type Icons struct {
	Icons map[string]Icon `yaml:"icons"`
}

// Icon is either a text glyph or inline SVG markup.
//
// LabelTermID is the accessible name, used where the icon carries meaning on its
// own rather than sitting beside text that already says it. It is a term id, not
// a string, because that name is what a screen reader announces — and a literal
// here announced English to every reader in every language, including the ones
// whose page is otherwise entirely translated.
//
// Label remains for the literal form. It is deprecated and validation says so,
// but it still works: an integrator upgrading should not have their dashboard
// stop starting over an accessible name.
type Icon struct {
	Glyph       string `yaml:"glyph"`
	SVG         string `yaml:"svg"`
	LabelTermID string `yaml:"labelTermId"`
	Label       string `yaml:"label"`
}

// --- chrome.yaml -----------------------------------------------------------

// Chrome is the page furniture: the header bar and what sits in it.
//
// It exists because the header used to be a fixed struct in Go with a fixed
// template beside it, so a deployment could restyle the whole dashboard and
// rearrange every section of the page — and still not add a link to its own
// service desk, or remove a language switcher it did not need. The bar a reader
// sees on every page was the one part of the product they could not touch.
type Chrome struct {
	Header ChromeBar `yaml:"header"`
	// Strip is a second bar below the header, for what qualifies the whole
	// dashboard rather than what controls it: whose exchanges these are, and
	// which side of them is on view. Leave it out and nothing renders.
	Strip ChromeBar `yaml:"strip"`
}

// ChromeBar is an ordered list of items. Order is the order they appear.
type ChromeBar struct {
	Items []ChromeItem `yaml:"items"`
}

// ChromeKind names what an item is. A closed set, because each kind is a piece of
// behaviour — a control that submits, a toggle that writes a cookie — and not
// something that can be described generically in YAML.
type ChromeKind string

const (
	// ChromeWordmark is the brand mark and name, linking home.
	ChromeWordmark ChromeKind = "wordmark"
	// ChromeScopeSwitch is the scope control, one segment per configured scope.
	ChromeScopeSwitch ChromeKind = "scope-switch"
	// ChromeSelect is a labelled dropdown bound to a named piece of reader state.
	ChromeSelect ChromeKind = "select"
	// ChromeThemeToggle switches between light and dark.
	ChromeThemeToggle ChromeKind = "theme-toggle"
	// ChromeLink is an arbitrary link: a service desk, an about page, a status
	// history. The kind that makes this file worth having.
	ChromeLink ChromeKind = "link"
	// ChromeSpacer pushes everything after it to the far end of the bar.
	ChromeSpacer ChromeKind = "spacer"
	// ChromeRoleSwitch is the demand/supply control, one option per configured
	// role. Like the scope switch, it narrows what the board is about rather
	// than filtering what is on it.
	ChromeRoleSwitch ChromeKind = "role-switch"
	// ChromePlatforms names the exchanges the dashboard covers, and links out
	// to them.
	ChromePlatforms ChromeKind = "platforms"
)

// ChromeKinds is every kind, for validation and for the generated schema.
var ChromeKinds = []string{
	string(ChromeWordmark), string(ChromeScopeSwitch), string(ChromeSelect),
	string(ChromeThemeToggle), string(ChromeLink), string(ChromeSpacer),
	string(ChromeRoleSwitch), string(ChromePlatforms),
}

// ChromeStates are the pieces of reader state a select may be bound to.
var ChromeStates = []string{"period", "locale"}

// ChromeItem is one item in the bar.
type ChromeItem struct {
	Kind ChromeKind `yaml:"kind"`
	// State names what a select changes. Required for select, meaningless
	// otherwise.
	State string `yaml:"state"`
	// TermID is the label. Required for link; a select falls back to the
	// conventional term for its state.
	TermID string `yaml:"termId"`
	// Href is where a link goes.
	Href string `yaml:"href"`
	// External marks a link as leaving the site, which adds rel="noopener" and
	// the new-tab indicator.
	External bool `yaml:"external"`
	// IconKey is an icon from icons.yaml, shown before the label.
	IconKey string `yaml:"iconKey"`
}

// --- assembled -------------------------------------------------------------

// Config is the fully loaded and validated configuration.
type Config struct {
	App    App
	Brand  Brand
	Domain Domain
	Theme  Theme
	Icons  Icons
	Chrome Chrome
}
