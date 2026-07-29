package i18n_test

import (
	"strings"
	"testing"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/i18n"
)

func en(terms map[string]string) i18n.Bundle {
	return i18n.Bundle{Locale: "en", Name: "English", Direction: "ltr", Terms: terms}
}

func cat(t *testing.T, bundles ...i18n.Bundle) *i18n.Catalogue {
	t.Helper()
	c, err := i18n.NewCatalogue(bundles[0].Locale, bundles...)
	if err != nil {
		t.Fatalf("NewCatalogue: %v", err)
	}
	return c
}

func resolver(t *testing.T, terms map[string]string) *i18n.Resolver {
	t.Helper()
	return cat(t, en(terms)).For("en")
}

// --- resolution ------------------------------------------------------------

func TestTermsResolve(t *testing.T) {
	r := resolver(t, map[string]string{"brand.wordmark": "Service Status"})

	if got := r.Text("brand.wordmark", nil); got != "Service Status" {
		t.Errorf("got %q", got)
	}
}

func TestAnUnknownTermRendersAsItself(t *testing.T) {
	// So a deployment that does not translate can put literal text in its
	// config files and have it simply work.
	r := resolver(t, map[string]string{})

	if got := r.Text("Aadhaar Authentication", nil); got != "Aadhaar Authentication" {
		t.Errorf("got %q, want the id back", got)
	}
}

func TestTranslationsFallBackKeyByKey(t *testing.T) {
	// A half-finished translation should be genuinely useful: what is
	// translated appears translated, and the rest appears in the base language
	// rather than as raw ids or a wholesale revert.
	c := cat(t,
		en(map[string]string{"a": "Alpha", "b": "Bravo"}),
		i18n.Bundle{Locale: "fr", Name: "Français", Terms: map[string]string{"a": "Alfa"}},
	)
	fr := c.For("fr")

	if got := fr.Text("a", nil); got != "Alfa" {
		t.Errorf("translated term = %q", got)
	}
	if got := fr.Text("b", nil); got != "Bravo" {
		t.Errorf("untranslated term = %q, want the base language", got)
	}
}

func TestAnUnknownLocaleFallsBackToTheBase(t *testing.T) {
	c := cat(t, en(map[string]string{"a": "Alpha"}))

	if got := c.For("kl").Text("a", nil); got != "Alpha" {
		t.Errorf("got %q", got)
	}
}

func TestHasReportsWhatIsDefined(t *testing.T) {
	c := cat(t,
		en(map[string]string{"a": "Alpha"}),
		i18n.Bundle{Locale: "fr", Terms: map[string]string{"b": "Bravo"}},
	)
	fr := c.For("fr")

	for _, id := range []string{"a", "b"} {
		if !fr.Has(id) {
			t.Errorf("Has(%q) = false", id)
		}
	}
	if fr.Has("c") {
		t.Error("Has reported an undefined term")
	}
}

// --- interpolation ---------------------------------------------------------

func TestInterpolation(t *testing.T) {
	r := resolver(t, map[string]string{"greet": "Showing {shown} of {total}"})

	if got := r.Text("greet", map[string]any{"shown": 12, "total": 178}); got != "Showing 12 of 178" {
		t.Errorf("got %q", got)
	}
}

func TestAMissingParameterRendersEmptyRatherThanBreaking(t *testing.T) {
	// A cosmetic gap is survivable; a panic while rendering a status page is not.
	r := resolver(t, map[string]string{"greet": "Hello {name}!"})

	if got := r.Text("greet", nil); got != "Hello !" {
		t.Errorf("got %q", got)
	}
}

func TestQuotesEscapeBraces(t *testing.T) {
	r := resolver(t, map[string]string{
		"literal": "Write '{'count'}' to interpolate",
		"apos":    "It''s fine",
	})

	if got := r.Text("literal", nil); got != "Write {count} to interpolate" {
		t.Errorf("got %q", got)
	}
	if got := r.Text("apos", nil); got != "It's fine" {
		t.Errorf("got %q", got)
	}
}

func TestAnApostropheIsPunctuationUnlessItProtectsSyntax(t *testing.T) {
	// ICU 4.8 semantics. Under the older "any apostrophe quotes" rule a pair of
	// contractions in one sentence swallowed itself: both apostrophes vanished
	// and the words between them were taken as a quoted literal run. English
	// mostly hid it; French, which puts "l'", "d'" and "n'" in nearly every
	// line, corrupted almost every string it had.
	r := resolver(t, map[string]string{
		"two":    "a record that didn't arrive, and someone who didn't get it",
		"french": "L'indisponibilité, c'est un service inaccessible : en attente d'un permis",
		"three":  "'''",
		"guard":  "Write '{'count'}' but don't lose this",
	})

	for _, tc := range []struct{ id, want string }{
		{"two", "a record that didn't arrive, and someone who didn't get it"},
		{"french", "L'indisponibilité, c'est un service inaccessible : en attente d'un permis"},
		{"three", "''"},
		{"guard", "Write {count} but don't lose this"},
	} {
		if got := r.Text(tc.id, nil); got != tc.want {
			t.Errorf("%s gave %q, want %q", tc.id, got, tc.want)
		}
	}
}

// --- plurals ---------------------------------------------------------------

func TestPluralFormsComeFromCLDR(t *testing.T) {
	// The translator writes only the arms their language uses; which arm applies
	// to which number is the language's business, not the sentence's.
	r := resolver(t, map[string]string{
		"n": "{count, plural, one{# service} other{# services}}",
	})

	for _, tc := range []struct {
		n    int
		want string
	}{
		{1, "1 service"},
		{0, "0 services"},
		{2, "2 services"},
		{178, "178 services"},
	} {
		if got := r.Text("n", map[string]any{"count": tc.n}); got != tc.want {
			t.Errorf("count=%d gave %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestExactArmsBeatCategories(t *testing.T) {
	// So a translator can write "no services" rather than "0 services" without
	// needing to know whether their language has a zero category.
	r := resolver(t, map[string]string{
		"n": "{count, plural, =0{no services} one{# service} other{# services}}",
	})

	if got := r.Text("n", map[string]any{"count": 0}); got != "no services" {
		t.Errorf("got %q", got)
	}
	if got := r.Text("n", map[string]any{"count": 1}); got != "1 service" {
		t.Errorf("got %q", got)
	}
}

func TestRussianUsesItsOwnPluralForms(t *testing.T) {
	// Russian has four categories where English has two. The proof that CLDR is
	// doing the work rather than an English-shaped assumption.
	c := cat(t,
		en(map[string]string{"n": "{count, plural, one{# service} other{# services}}"}),
		i18n.Bundle{Locale: "ru", Terms: map[string]string{
			"n": "{count, plural, one{# сервис} few{# сервиса} many{# сервисов} other{# сервиса}}",
		}},
	)
	ru := c.For("ru")

	for _, tc := range []struct {
		n    int
		want string
	}{
		{1, "1 сервис"},     // one
		{3, "3 сервиса"},    // few
		{11, "11 сервисов"}, // many
		{25, "25 сервисов"}, // many
	} {
		if got := ru.Text("n", map[string]any{"count": tc.n}); got != tc.want {
			t.Errorf("count=%d gave %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestPluralWithANonNumberFallsBackToOther(t *testing.T) {
	r := resolver(t, map[string]string{
		"n": "{count, plural, one{# service} other{# services}}",
	})

	// The sentence still renders rather than disappearing.
	if got := r.Text("n", map[string]any{"count": "many"}); !strings.Contains(got, "service") {
		t.Errorf("got %q", got)
	}
}

func TestPluralWithNoMatchingArmUsesOther(t *testing.T) {
	r := resolver(t, map[string]string{"n": "{count, plural, other{# services}}"})

	if got := r.Text("n", map[string]any{"count": 1}); got != "1 services" {
		t.Errorf("got %q", got)
	}
}

func TestNestedPluralsAndInterpolation(t *testing.T) {
	r := resolver(t, map[string]string{
		"n": "{count, plural, one{# service in {region}} other{# services in {region}}}",
	})

	got := r.Text("n", map[string]any{"count": 3, "region": "Maharashtra"})
	if got != "3 services in Maharashtra" {
		t.Errorf("got %q", got)
	}
}

// --- select ----------------------------------------------------------------

func TestSelect(t *testing.T) {
	r := resolver(t, map[string]string{
		"trend": "{dir, select, up{rose} down{fell} other{held steady}}",
	})

	for _, tc := range []struct{ dir, want string }{
		{"up", "rose"},
		{"down", "fell"},
		{"flat", "held steady"},
		{"anything", "held steady"},
	} {
		if got := r.Text("trend", map[string]any{"dir": tc.dir}); got != tc.want {
			t.Errorf("dir=%q gave %q, want %q", tc.dir, got, tc.want)
		}
	}
}

// --- malformed messages ----------------------------------------------------

func TestMalformedMessagesRenderRatherThanVanish(t *testing.T) {
	// A translator's mistake should be visible and survivable, not a blank page.
	for _, tc := range []struct{ name, src string }{
		{"unterminated brace", "Showing {shown of {total}"},
		{"stray closing brace", "Showing } services"},
		{"unknown argument type", "{count, ordinal, one{#st}}"},
		{"empty message", ""},
		{"no arms", "{count, plural, }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := resolver(t, map[string]string{"x": tc.src})
			// The assertion is simply that it returns without panicking.
			_ = r.Text("x", map[string]any{"count": 1, "shown": 2, "total": 3})
		})
	}
}

// --- numbers ---------------------------------------------------------------

func TestNumbersUseTheLocalesOwnGrouping(t *testing.T) {
	// Indian locales group as 29,00,000 rather than 2,900,000. Getting this
	// wrong is the sort of thing that quietly tells a reader the dashboard was
	// not built for them.
	c := cat(t,
		en(map[string]string{}),
		i18n.Bundle{Locale: "hi", Terms: map[string]string{}},
	)

	if got := c.For("en").Number(2900000, 0); got != "2,900,000" {
		t.Errorf("English grouping = %q", got)
	}
	if got := c.For("hi").Number(2900000, 0); !strings.Contains(got, "29,00,000") {
		t.Errorf("Hindi grouping = %q, want the Indian grouping", got)
	}
}

func TestPrecisionIsHonoured(t *testing.T) {
	r := resolver(t, nil)

	if got := r.Number(99.9, 2); got != "99.90" {
		t.Errorf("got %q", got)
	}
	if got := r.Number(99.912, 0); got != "100" {
		t.Errorf("got %q", got)
	}
}

func TestPercentIsNotRescaled(t *testing.T) {
	// Metrics are stored as 99.91 meaning 99.91%. Re-deriving that would invite
	// exactly the ratio-versus-percentage confusion the ingest contract avoids.
	r := resolver(t, nil)

	if got := r.Percent(99.91, 2); !strings.HasPrefix(got, "99.91") {
		t.Errorf("got %q, want 99.91 with a percent sign", got)
	}
	if !strings.Contains(r.Percent(99.91, 2), "%") {
		t.Errorf("got %q, want a percent sign", r.Percent(99.91, 2))
	}
}

func TestUnits(t *testing.T) {
	r := resolver(t, nil)

	if got := r.Unit(231, "millisecond", 0); got != "231 ms" {
		t.Errorf("got %q", got)
	}
	if got := r.Unit(5, "second", 0); got != "5 s" {
		t.Errorf("got %q", got)
	}
	// An unrecognised unit renders the bare number rather than inventing a
	// suffix.
	if got := r.Unit(5, "parsec", 0); got != "5" {
		t.Errorf("got %q", got)
	}
}

// --- time ------------------------------------------------------------------

var now = time.Date(2026, time.July, 27, 15, 0, 0, 0, time.UTC)

func timeTerms() map[string]string {
	return map[string]string{
		"time.ago.second": "{n, plural, =0{just now} one{# second ago} other{# seconds ago}}",
		"time.ago.minute": "{n, plural, one{# minute ago} other{# minutes ago}}",
		"time.ago.hour":   "{n, plural, one{# hour ago} other{# hours ago}}",
		"time.ago.day":    "{n, plural, one{# day ago} other{# days ago}}",
		"time.for.second": "{n, plural, one{# sec} other{# sec}}",
		"time.for.hour":   "{n, plural, one{# hour} other{# hours}}",
		"time.for.minute": "{n, plural, one{# minute} other{# minutes}}",
		"time.for.day":    "{n, plural, one{# day} other{# days}}",
		"time.for.join":   "{major} {minor}",
	}
}

func TestRelativeTimePicksAReadableUnit(t *testing.T) {
	// Nobody wants to be told the data is 10,800 seconds old.
	r := resolver(t, timeTerms())

	for _, tc := range []struct {
		ago  time.Duration
		want string
	}{
		{4 * time.Minute, "4 minutes ago"},
		{1 * time.Minute, "1 minute ago"},
		{3 * time.Hour, "3 hours ago"},
		{50 * time.Hour, "2 days ago"},
		{0, "just now"},
	} {
		if got := r.RelativeTime(now.Add(-tc.ago), now); got != tc.want {
			t.Errorf("%v ago gave %q, want %q", tc.ago, got, tc.want)
		}
	}
}

func TestATimestampInTheFutureReadsAsNow(t *testing.T) {
	// Clock skew between a collector and the dashboard is ordinary; "in -3
	// minutes" is not something a reader should ever see.
	r := resolver(t, timeTerms())

	if got := r.RelativeTime(now.Add(time.Hour), now); got != "just now" {
		t.Errorf("got %q", got)
	}
}

func TestDuration(t *testing.T) {
	// Two units, because how long an incident has been open is a number people
	// act on and rounding 13h31m to "13 hours" discards half an hour of it. The
	// second unit only appears when it says something.
	r := resolver(t, timeTerms())

	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{3 * time.Hour, "3 hours"},
		{13*time.Hour + 31*time.Minute, "13 hours 31 minutes"},
		{time.Hour + time.Minute, "1 hour 1 minute"},
		{25 * time.Hour, "1 day 1 hour"},
		{48 * time.Hour, "2 days"},
		{90 * time.Second, "1 minute 30 sec"},
		{45 * time.Second, "45 sec"},
	} {
		if got := r.Duration(tc.d); got != tc.want {
			t.Errorf("%s gave %q, want %q", tc.d, got, tc.want)
		}
	}

	if got := r.Duration(-time.Hour); got == "" {
		t.Error("a negative duration produced nothing")
	}
}

func TestDates(t *testing.T) {
	c := cat(t, i18n.Bundle{
		Locale: "en",
		Date:   i18n.DateFormat{Short: "2 Jan", Full: "2 Jan 2006, 15:04"},
	})
	r := c.For("en")

	if got := r.Date(now); got != "27 Jul" {
		t.Errorf("short date = %q", got)
	}
	if got := r.DateTime(now); got != "27 Jul 2026, 15:00" {
		t.Errorf("full date = %q", got)
	}
}

func TestDatesFallBackWithNoLayoutConfigured(t *testing.T) {
	r := resolver(t, nil)

	if got := r.Date(now); got == "" {
		t.Error("no date rendered")
	}
	if got := r.DateTime(now); got == "" {
		t.Error("no datetime rendered")
	}
}

// --- catalogue -------------------------------------------------------------

func TestDirection(t *testing.T) {
	c := cat(t,
		en(nil),
		i18n.Bundle{Locale: "ar", Direction: "rtl"},
	)

	if got := c.For("en").Direction(); got != "ltr" {
		t.Errorf("English direction = %q", got)
	}
	if got := c.For("ar").Direction(); got != "rtl" {
		t.Errorf("Arabic direction = %q", got)
	}
}

func TestLocalesAndNames(t *testing.T) {
	c := cat(t,
		en(nil),
		i18n.Bundle{Locale: "hi", Name: "हिन्दी"},
		i18n.Bundle{Locale: "fr"},
	)

	if got := c.Locales(); len(got) != 3 || got[0] != "en" {
		t.Errorf("locales = %v, want them in the order supplied", got)
	}
	names := c.Names()
	// A language's name in its own language is the only form a speaker reliably
	// recognises.
	if names["hi"] != "हिन्दी" {
		t.Errorf("Hindi name = %q", names["hi"])
	}
	// A bundle with no name at least identifies itself.
	if names["fr"] != "fr" {
		t.Errorf("unnamed locale = %q", names["fr"])
	}
}

func TestMatchPrefersAnExplicitChoice(t *testing.T) {
	// A reader who has picked a language has said something the browser header
	// has not: that this page should be in it.
	c := cat(t, en(nil), i18n.Bundle{Locale: "fr"}, i18n.Bundle{Locale: "hi"})

	if got := c.Match("fr", "hi,en;q=0.8"); got != "fr" {
		t.Errorf("got %q, want the explicit choice", got)
	}
}

func TestMatchFallsBackToTheBrowserPreference(t *testing.T) {
	c := cat(t, en(nil), i18n.Bundle{Locale: "fr"}, i18n.Bundle{Locale: "hi"})

	if got := c.Match("", "fr-CA,fr;q=0.9"); got != "fr" {
		t.Errorf("got %q, want fr", got)
	}
	if got := c.Match("", ""); got != "en" {
		t.Errorf("got %q, want the base", got)
	}
	if got := c.Match("kl", "kl"); got != "en" {
		t.Errorf("an unavailable language gave %q, want the base", got)
	}
	if got := c.Match("", "not a header"); got != "en" {
		t.Errorf("a malformed header gave %q, want the base", got)
	}
}

func TestCatalogueRejectsBadInput(t *testing.T) {
	if _, err := i18n.NewCatalogue("en"); err == nil {
		t.Error("an empty catalogue was accepted")
	}
	if _, err := i18n.NewCatalogue("en", i18n.Bundle{}); err == nil {
		t.Error("a bundle with no locale code was accepted")
	}
	if _, err := i18n.NewCatalogue("en", i18n.Bundle{Locale: "not a tag!"}); err == nil {
		t.Error("an invalid language tag was accepted")
	}
	if _, err := i18n.NewCatalogue("de", en(nil)); err == nil {
		t.Error("a base locale that was not supplied was accepted")
	}
}

func TestResolverReportsItsLocale(t *testing.T) {
	c := cat(t, en(nil), i18n.Bundle{Locale: "fr"})

	if got := c.For("fr").Locale(); got != "fr" {
		t.Errorf("got %q", got)
	}
	// An unknown locale reports the base it actually fell back to, rather than
	// the code it was asked for — the page's lang attribute has to be true.
	if got := c.For("kl").Locale(); got != "en" {
		t.Errorf("got %q, want the locale actually in use", got)
	}
}

func TestEveryParameterTypeInterpolates(t *testing.T) {
	// Builders pass whatever the model holds: counts as int, deltas as float,
	// ids as string, flags as bool.
	r := resolver(t, map[string]string{"x": "{v}"})

	for _, tc := range []struct {
		v    any
		want string
	}{
		{"text", "text"},
		{42, "42"},
		{int32(42), "42"},
		{int64(2900000), "2,900,000"},
		// The digits the number has, not a fixed two. A threshold of 99 in a
		// published rule reads "99%", not "99.00%".
		{99.5, "99.5"},
		{99.0, "99"},
		{0.125, "0.125"},
		{true, "true"},
		{nil, ""},
		{[]string{"a"}, "[a]"},
	} {
		if got := r.Text("x", map[string]any{"v": tc.v}); got != tc.want {
			t.Errorf("%T %v gave %q, want %q", tc.v, tc.v, got, tc.want)
		}
	}
}

func TestPluralAcceptsEveryNumericType(t *testing.T) {
	r := resolver(t, map[string]string{
		"n": "{count, plural, one{# service} other{# services}}",
	})

	for _, v := range []any{1, int32(1), int64(1), 1.0, float32(1)} {
		if got := r.Text("n", map[string]any{"count": v}); got != "1 service" {
			t.Errorf("%T gave %q", v, got)
		}
	}
}

func TestFractionalPluralsUseTheirOwnCategory(t *testing.T) {
	// English treats 1.5 as plural even though its integer part is one.
	r := resolver(t, map[string]string{
		"n": "{count, plural, one{# day} other{# days}}",
	})

	if got := r.Text("n", map[string]any{"count": 1.5}); !strings.Contains(got, "days") {
		t.Errorf("got %q, want the plural form", got)
	}
}

func TestArabicUsesItsSixForms(t *testing.T) {
	// The strongest evidence that plural selection is CLDR's and not ours.
	c := cat(t,
		en(map[string]string{"n": "{count, plural, one{# service} other{# services}}"}),
		i18n.Bundle{Locale: "ar", Direction: "rtl", Terms: map[string]string{
			"n": "{count, plural, zero{صفر} one{واحد} two{اثنان} few{قليل} many{كثير} other{آخر}}",
		}},
	)
	ar := c.For("ar")

	for _, tc := range []struct {
		n    int
		want string
	}{
		{0, "صفر"},
		{1, "واحد"},
		{2, "اثنان"},
		{3, "قليل"},
		{11, "كثير"},
		{100, "آخر"},
	} {
		if got := ar.Text("n", map[string]any{"count": tc.n}); got != tc.want {
			t.Errorf("count=%d gave %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestPluralOffset(t *testing.T) {
	// ICU's "and N others" phrasing: the offset is subtracted before the form
	// is chosen and before # is substituted.
	r := resolver(t, map[string]string{
		"n": "{count, plural, offset:1 =0{nobody else} one{and # other} other{and # others}}",
	})

	if got := r.Text("n", map[string]any{"count": 1}); got != "nobody else" {
		t.Errorf("count=1 gave %q", got)
	}
	if got := r.Text("n", map[string]any{"count": 2}); got != "and 1 other" {
		t.Errorf("count=2 gave %q", got)
	}
	if got := r.Text("n", map[string]any{"count": 5}); got != "and 4 others" {
		t.Errorf("count=5 gave %q", got)
	}
}

func TestMalformedOffsetIsIgnored(t *testing.T) {
	r := resolver(t, map[string]string{
		"n": "{count, plural, offset:many one{# service} other{# services}}",
	})

	// The sentence still renders rather than being dropped.
	if got := r.Text("n", map[string]any{"count": 2}); !strings.Contains(got, "service") {
		t.Errorf("got %q", got)
	}
}

func TestPluralWithNoArmsAtAllRendersNothingRatherThanPanicking(t *testing.T) {
	r := resolver(t, map[string]string{"n": "{count, plural, }"})

	if got := r.Text("n", map[string]any{"count": 2}); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestUnterminatedQuoteIsLiteral(t *testing.T) {
	// Losing the rest of a sentence to one stray apostrophe is worse than
	// rendering it with the quoting not applied.
	r := resolver(t, map[string]string{
		"plain": "it's fine",
		"open":  "Write '{count to interpolate",
	})

	if got := r.Text("plain", nil); got != "it's fine" {
		t.Errorf("plain gave %q", got)
	}
	if got := r.Text("open", nil); got != "Write {count to interpolate" {
		t.Errorf("open gave %q", got)
	}
}

func TestPluralOperandsDistinguishTrailingZeros(t *testing.T) {
	// CLDR treats 1.0 and 1 differently in several languages, and the
	// difference lives entirely in the fraction operands. Czech is one such:
	// 1 is "one" while 1.0 is "many".
	c := cat(t,
		en(map[string]string{"n": "{count, plural, one{# day} other{# days}}"}),
		i18n.Bundle{Locale: "cs", Terms: map[string]string{
			"n": "{count, plural, one{# den} few{# dny} many{# dne} other{# dní}}",
		}},
	)
	cs := c.For("cs")

	if got := cs.Text("n", map[string]any{"count": 1}); got != "1 den" {
		t.Errorf("1 gave %q, want the one form", got)
	}
	// "1,5" with a comma: Czech's decimal mark, applied by the same formatter
	// that chose the plural form.
	if got := cs.Text("n", map[string]any{"count": 1.5}); got != "1,5 dne" {
		t.Errorf("1.5 gave %q, want the many form with a Czech decimal mark", got)
	}
}

func TestHashRendersAtTheNumbersOwnPrecision(t *testing.T) {
	// Rounding 1.5 to "2" would contradict the very form the arm was chosen for.
	r := resolver(t, map[string]string{"n": "{count, plural, one{# day} other{# days}}"})

	if got := r.Text("n", map[string]any{"count": 1.5}); got != "1.5 days" {
		t.Errorf("got %q", got)
	}
	if got := r.Text("n", map[string]any{"count": 3}); got != "3 days" {
		t.Errorf("got %q", got)
	}
}

func TestNegativeCountsStillChooseAForm(t *testing.T) {
	r := resolver(t, map[string]string{"n": "{count, plural, one{# day} other{# days}}"})

	if got := r.Text("n", map[string]any{"count": -1}); got == "" {
		t.Error("a negative count produced nothing")
	}
}

func TestSelectFallsThroughWhenNoOtherArmExists(t *testing.T) {
	r := resolver(t, map[string]string{"x": "{dir, select, up{rose}}"})

	// Nothing matches and there is no "other": render nothing rather than panic.
	if got := r.Text("x", map[string]any{"dir": "down"}); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestOptionsWithNoClosingBraceStopCleanly(t *testing.T) {
	r := resolver(t, map[string]string{"n": "{count, plural, one{# day"})

	_ = r.Text("n", map[string]any{"count": 1}) // must not panic
}

func TestSelectWithNoArmsAtAll(t *testing.T) {
	r := resolver(t, map[string]string{"x": "{dir, select, }"})

	if got := r.Text("x", map[string]any{"dir": "up"}); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestATrailingKeyWithNoBodyIsIgnored(t *testing.T) {
	r := resolver(t, map[string]string{"n": "{count, plural, one{# day} other}"})

	// "other" has no braced body, so only the "one" arm exists.
	if got := r.Text("n", map[string]any{"count": 1}); got != "1 day" {
		t.Errorf("got %q", got)
	}
	if got := r.Text("n", map[string]any{"count": 5}); got != "" {
		t.Errorf("got %q, want nothing rather than a panic", got)
	}
}

func TestAStrayClosingBraceInsideAnArmIsLiteral(t *testing.T) {
	r := resolver(t, map[string]string{"x": "a } b"})

	if got := r.Text("x", nil); got != "a } b" {
		t.Errorf("got %q", got)
	}
}
