// Package i18n resolves term ids into text a reader can understand.
//
// Two jobs, deliberately kept apart. Choosing the right *words* is a
// translator's, and lives in the locale files. Choosing the right *forms* —
// which plural, which digit grouping, which decimal mark — is a property of the
// language itself, and comes from CLDR through golang.org/x/text. A translator
// should never have to encode that Russian has four plural forms; they should
// only have to write the ones their language uses.
//
// Three principles hold throughout:
//
//   - A missing translation falls back key by key to the base locale, so a
//     half-finished translation is genuinely useful rather than a half-broken
//     page.
//   - A term id with no entry anywhere renders as itself, so a deployment that
//     does not translate can put literal text in its config and have it work.
//   - Nothing here can fail. A cosmetic problem must never become a blank page.
package i18n

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/number"
)

// Bundle is one locale as it is written in a locale file.
type Bundle struct {
	Locale    string `yaml:"locale"`
	Name      string `yaml:"name"`
	Direction string `yaml:"direction"`
	// PluralRule names the language whose CLDR plural categories this locale
	// uses, when that is not the locale itself. Almost always empty: the plural
	// rule for "fr" is French's, and saying so twice only creates a way for the
	// two to disagree.
	//
	// It earns its place for a locale whose tag CLDR does not know. A
	// deployment adding a regional variant, a private-use tag, or a language
	// x/text has no rules for gets "other" for every count — which reads as
	// "1 services" — and this is the one line that fixes it.
	PluralRule string            `yaml:"pluralRule"`
	Date       DateFormat        `yaml:"date"`
	Terms      map[string]string `yaml:"terms"`
}

// There is deliberately no number format here.
//
// Digit grouping, the decimal mark and the digits themselves come from CLDR via
// the locale tag, and CLDR is right about more cases than a config block would
// be: Hindi groups as 29,00,000 rather than 2,900,000; Arabic renders
// ٢٬٩٠٠٬٠٠٠ in Arabic-Indic digits with the percent sign on the correct side;
// French and Russian group with a narrow no-break space; Spanish swaps the
// comma and the full stop. All of that is already correct with nothing
// configured.
//
// An earlier version of this file did carry a `number:` block. It was parsed
// and then ignored, which is worse than not having it — a deployment editing it
// would have seen no effect and no error. A deployment that genuinely wants
// Latin digits in Arabic asks for the locale "ar-u-nu-latn", which is a
// language tag rather than a setting this package has to invent.

// DateFormat carries Go layout strings, so a locale can order the parts the way
// its readers expect.
type DateFormat struct {
	Short string `yaml:"short"`
	Full  string `yaml:"full"`
}

// Catalogue is every loaded locale, with one nominated as the base.
type Catalogue struct {
	base    string
	bundles map[string]*compiled
	order   []string
}

type compiled struct {
	bundle Bundle
	tag    language.Tag
	// pluralTag is the tag CLDR plural rules are looked up under. Normally the
	// same as tag; see the pluralRule field on Bundle for when it is not.
	pluralTag language.Tag
	printer   *message.Printer
	terms     map[string]Message
}

// NewCatalogue compiles a set of bundles. base names the locale others fall
// back to.
func NewCatalogue(base string, bundles ...Bundle) (*Catalogue, error) {
	if len(bundles) == 0 {
		return nil, fmt.Errorf("no locales supplied")
	}

	c := &Catalogue{base: base, bundles: map[string]*compiled{}}
	for _, b := range bundles {
		if b.Locale == "" {
			return nil, fmt.Errorf("a locale bundle has no locale code")
		}
		tag, err := language.Parse(b.Locale)
		if err != nil {
			return nil, fmt.Errorf("locale %q is not a language tag: %w", b.Locale, err)
		}

		pluralTag := tag
		if b.PluralRule != "" {
			pluralTag, err = language.Parse(b.PluralRule)
			if err != nil {
				return nil, fmt.Errorf("locale %q: pluralRule %q is not a language tag: %w",
					b.Locale, b.PluralRule, err)
			}
		}

		cm := &compiled{
			bundle:    b,
			tag:       tag,
			pluralTag: pluralTag,
			printer:   message.NewPrinter(tag),
			terms:     make(map[string]Message, len(b.Terms)),
		}
		// Messages are parsed once at load, not per request. A dashboard
		// rendering a hundred rows would otherwise re-parse the same message a
		// hundred times.
		for id, src := range b.Terms {
			cm.terms[id] = Parse(src)
		}
		c.bundles[b.Locale] = cm
		c.order = append(c.order, b.Locale)
	}

	if _, ok := c.bundles[base]; !ok {
		return nil, fmt.Errorf("base locale %q was not supplied", base)
	}
	return c, nil
}

// Locales lists the loaded locale codes in the order they were supplied, which
// is the order the language selector offers them.
func (c *Catalogue) Locales() []string { return c.order }

// Names maps locale code to the language's name in its own language, which is
// the only form of it a speaker reliably recognises.
func (c *Catalogue) Names() map[string]string {
	out := make(map[string]string, len(c.bundles))
	for code, b := range c.bundles {
		name := b.bundle.Name
		if name == "" {
			name = code
		}
		out[code] = name
	}
	return out
}

// Match picks the best available locale for a request.
//
// An explicit choice wins over the browser's preference, because a reader who
// has picked a language has told you something the Accept-Language header has
// not: that this particular page should be in it.
func (c *Catalogue) Match(explicit, acceptLanguage string) string {
	if _, ok := c.bundles[explicit]; ok && explicit != "" {
		return explicit
	}
	if acceptLanguage == "" {
		return c.base
	}

	tags := make([]language.Tag, 0, len(c.order))
	for _, code := range c.order {
		tags = append(tags, c.bundles[code].tag)
	}
	matcher := language.NewMatcher(tags)

	desired, _, err := language.ParseAcceptLanguage(acceptLanguage)
	if err != nil {
		return c.base
	}
	_, index, conf := matcher.Match(desired...)
	if conf == language.No || index >= len(c.order) {
		return c.base
	}
	return c.order[index]
}

// Resolver renders text in one locale. It satisfies widget.TextResolver.
type Resolver struct {
	cat  *Catalogue
	self *compiled
	base *compiled
}

// For returns a resolver for a locale, falling back to the base when the code
// is not loaded.
func (c *Catalogue) For(locale string) *Resolver {
	self, ok := c.bundles[locale]
	if !ok {
		self = c.bundles[c.base]
	}
	return &Resolver{cat: c, self: self, base: c.bundles[c.base]}
}

// Locale is the code this resolver renders in.
func (r *Resolver) Locale() string { return r.self.bundle.Locale }

// Direction is "rtl" or "ltr", for the document's dir attribute.
func (r *Resolver) Direction() string {
	if r.self.bundle.Direction == "rtl" {
		return "rtl"
	}
	return "ltr"
}

// Text renders a term.
//
// Resolution falls back key by key rather than bundle by bundle: a locale that
// has translated half its terms shows those in the reader's language and the
// rest in the base language, instead of showing raw term ids or reverting
// wholesale.
func (r *Resolver) Text(id string, params map[string]any) string {
	msg, ok := r.self.terms[id]
	if !ok {
		msg, ok = r.base.terms[id]
	}
	if !ok {
		// Renders as itself, so literal text in a config file simply works.
		return id
	}
	return r.render(msg, params)
}

// Has reports whether a term is defined anywhere, which lets a caller choose
// between a translated label and a fallback of its own.
func (r *Resolver) Has(id string) bool {
	if _, ok := r.self.terms[id]; ok {
		return true
	}
	_, ok := r.base.terms[id]
	return ok
}

func (r *Resolver) render(m Message, params map[string]any) string {
	var b strings.Builder
	r.write(&b, m, params, math.NaN())
	return b.String()
}

// write emits a message. hash is the number that # stands for inside a plural
// arm, and is NaN outside one.
func (r *Resolver) write(b *strings.Builder, m Message, params map[string]any, hash float64) {
	for _, p := range m.parts {
		switch p.kind {
		case partLiteral:
			// Inside a plural arm, # is the number the arm was chosen for,
			// rendered at its own precision — rounding 1.5 to "2" would
			// contradict the very form the arm was selected for.
			if !math.IsNaN(hash) && strings.Contains(p.literal, "#") {
				b.WriteString(strings.ReplaceAll(p.literal, "#", r.Number(hash, naturalPrecision(hash))))
				continue
			}
			b.WriteString(p.literal)

		case partArg:
			b.WriteString(r.format(params[p.arg]))

		case partPlural:
			n, ok := toFloat(params[p.arg])
			if !ok {
				// Not a number: fall back to the "other" arm rather than
				// dropping the sentence.
				r.writeOption(b, p, []string{"other"}, params, math.NaN())
				continue
			}
			r.writeOption(b, p, r.pluralKeys(n-p.offset), params, n-p.offset)

		case partSelect:
			r.writeOption(b, p, []string{r.format(params[p.arg]), "other"}, params, hash)
		}
	}
}

// writeOption emits the first arm the message actually defines.
//
// Candidates are tried in order of specificity, so a translator's "=0" arm
// beats the "zero" category, which beats "other". That is what lets them write
// "no services" instead of "0 services" without having to know whether their
// language has a zero category at all.
func (r *Resolver) writeOption(b *strings.Builder, p part, candidates []string, params map[string]any, hash float64) {
	// Callers always end their candidate list with "other", so there is no
	// separate fallback here; a message that defines no matching arm and no
	// "other" renders nothing, which is the only honest option left.
	for _, key := range candidates {
		if opt, ok := p.options[key]; ok {
			r.write(b, opt, params, hash)
			return
		}
	}
}

// pluralKeys returns the arms to try, most specific first.
//
// The category comes from CLDR, which is the part a translator must never have
// to think about: English has two forms, Russian four, Arabic six, Chinese one,
// and which applies to which number is a property of the language rather than
// of the sentence. A translator writes only the arms their language uses.
func (r *Resolver) pluralKeys(n float64) []string {
	i, v, w, f, t := pluralOperands(n)

	category := "other"
	switch plural.Cardinal.MatchPlural(r.self.pluralTag, i, v, w, f, t) {
	case plural.Zero:
		category = "zero"
	case plural.One:
		category = "one"
	case plural.Two:
		category = "two"
	case plural.Few:
		category = "few"
	case plural.Many:
		category = "many"
	}

	return []string{
		"=" + strconv.FormatFloat(n, 'f', -1, 64),
		category,
		"other",
	}
}

// format renders an interpolated value.
func (r *Resolver) format(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case int:
		return r.Number(float64(t), 0)
	case int32:
		return r.Number(float64(t), 0)
	case int64:
		return r.Number(float64(t), 0)
	case float64:
		return r.Number(t, 2)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprint(v)
	}
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case int:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	case float64:
		return t, true
	case float32:
		return float64(t), true
	}
	return 0, false
}

// --- number and time formatting --------------------------------------------

// Number renders a value with the locale's own grouping and decimal mark.
func (r *Resolver) Number(v float64, precision int) string {
	return r.self.printer.Sprint(number.Decimal(v,
		number.MinFractionDigits(precision),
		number.MaxFractionDigits(precision)))
}

// Percent renders a value already expressed as a percentage.
//
// The value is divided rather than passed to number.Percent's own scaling,
// because the dashboard's metrics are stored as 99.91 meaning 99.91%, and
// re-deriving that would invite the ratio-versus-percentage confusion the
// ingest contract works hard to avoid.
func (r *Resolver) Percent(v float64, precision int) string {
	return r.self.printer.Sprint(number.Percent(v/100,
		number.MinFractionDigits(precision),
		number.MaxFractionDigits(precision)))
}

// Unit renders a value with its unit.
func (r *Resolver) Unit(v float64, unit string, precision int) string {
	n := r.Number(v, precision)
	// A short suffix reads better in a dense table than a spelled-out unit.
	switch unit {
	case "millisecond":
		return n + " ms"
	case "second":
		return n + " s"
	default:
		return n
	}
}

// Date renders a day.
func (r *Resolver) Date(t time.Time) string {
	return t.Format(layoutOr(r.self.bundle.Date.Short, "2 Jan"))
}

// DateTime renders a moment.
func (r *Resolver) DateTime(t time.Time) string {
	return t.Format(layoutOr(r.self.bundle.Date.Full, "2 Jan 2006, 15:04"))
}

func layoutOr(layout, fallback string) string {
	if layout == "" {
		return fallback
	}
	return layout
}

// RelativeTime renders how long ago something happened.
//
// "4 minutes ago" is what a reader actually wants from a status page; the exact
// timestamp is offered alongside for when they want to be precise.
func (r *Resolver) RelativeTime(t, now time.Time) string {
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}

	unit, n := breakDown(d)
	return r.Text("time.ago."+unit, map[string]any{"n": n})
}

// Duration renders a span of time, for "open for 3 hours".
func (r *Resolver) Duration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	unit, n := breakDown(d)
	return r.Text("time.for."+unit, map[string]any{"n": n})
}

// breakDown picks the largest unit that gives a number worth reading. Nobody
// wants to be told an incident has been open for 10,800 seconds.
func breakDown(d time.Duration) (string, int) {
	switch {
	case d < time.Minute:
		return "second", int(d.Seconds())
	case d < time.Hour:
		return "minute", int(d.Minutes())
	case d < 24*time.Hour:
		return "hour", int(d.Hours())
	default:
		return "day", int(d.Hours() / 24)
	}
}

// pluralOperands decomposes a number into the five values CLDR's plural rules
// are written against.
//
// They are not obvious and getting them wrong is silent: English calls 1.5
// "other" and 1 "one", and the difference lives entirely in the fraction
// operands. The names are CLDR's own:
//
//	i  the integer part
//	v  how many fraction digits are visible, trailing zeros included
//	w  how many are visible with trailing zeros removed
//	f  those digits as a number, trailing zeros included
//	t  those digits as a number, trailing zeros removed
func pluralOperands(n float64) (i, v, w, f, t int) {
	abs := math.Abs(n)
	i = int(math.Trunc(abs))

	digits := fractionDigits(abs)
	if digits == "" {
		return i, 0, 0, 0, 0
	}

	v = len(digits)
	f, _ = strconv.Atoi(digits)

	trimmed := strings.TrimRight(digits, "0")
	w = len(trimmed)
	if trimmed != "" {
		t, _ = strconv.Atoi(trimmed)
	}
	return i, v, w, f, t
}

// fractionDigits returns the digits after the decimal point, or "" when there
// are none.
func fractionDigits(abs float64) string {
	s := strconv.FormatFloat(abs, 'f', -1, 64)
	_, frac, ok := strings.Cut(s, ".")
	if !ok {
		return ""
	}
	return frac
}

// naturalPrecision is how many decimal places a number needs to render without
// gaining or losing information.
func naturalPrecision(n float64) int {
	return len(fractionDigits(math.Abs(n)))
}
