package main

import (
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/i18n"
)

// These test the shipped locale files rather than the i18n machinery, which has
// its own suite against fixtures. The distinction matters: every check here is
// one a translator can fail, and most of them fail silently — a dropped
// placeholder renders a sentence with a hole in it, and a missing plural arm
// renders "1 services". Neither is a crash, so neither shows up without this.

// Domain nouns are deliberately not translated. They name Indian services,
// providers and states, which belong to the example domain config a deployment
// replaces wholesale.
var domainPrefixes = []string{"svc.", "prov.", "reg."}

// The demand side shares its prefix with the interface terms that describe it —
// req.callsLabel is a label and must be translated, req.bankKyc.name is the
// name of an organisation's use case and must not be. So the exemption is on
// the shape of the id rather than on the prefix alone.
var domainNounSuffixes = []string{".name", ".desc"}

func isDomainNoun(id string) bool {
	for _, p := range domainPrefixes {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	if strings.HasPrefix(id, "req.") {
		for _, s := range domainNounSuffixes {
			if strings.HasSuffix(id, s) {
				return true
			}
		}
	}
	return false
}

func catalogue(t *testing.T) *i18n.Catalogue {
	t.Helper()
	c, err := loadLocales("")
	if err != nil {
		t.Fatalf("loading the shipped locales: %v", err)
	}
	return c
}

// shippedBundles reads the embedded locale files as raw bundles.
//
// The catalogue compiles messages and hides the source, which is exactly what
// the placeholder check needs to see.
func shippedBundles(t *testing.T) map[string]i18n.Bundle {
	t.Helper()

	entries, err := fs.ReadDir(configFS, "config/locales")
	if err != nil {
		t.Fatalf("reading embedded locales: %v", err)
	}

	out := map[string]i18n.Bundle{}
	for _, e := range entries {
		raw, err := configFS.ReadFile("config/locales/" + e.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		var b i18n.Bundle
		if err := yaml.Unmarshal(raw, &b); err != nil {
			t.Fatalf("parsing %s: %v", e.Name(), err)
		}
		out[b.Locale] = b
	}
	if len(out) == 0 {
		t.Fatal("no locale bundles are embedded")
	}
	return out
}

// shippedTermIDs is every term English defines, which is the full set the
// interface can ask for.
func shippedTermIDs(t *testing.T) []string {
	t.Helper()

	en, ok := shippedBundles(t)["en"]
	if !ok {
		t.Fatal("en.yaml is not embedded")
	}
	out := make([]string, 0, len(en.Terms))
	for id := range en.Terms {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func TestEveryShippedLocaleLoads(t *testing.T) {
	got := catalogue(t).Locales()
	sort.Strings(got)

	want := []string{"ar", "en", "es", "fr", "hi", "ru", "sw", "zh"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
			break
		}
	}
}

func TestEveryLocaleTranslatesTheWholeInterface(t *testing.T) {
	// The promise the locale file headers make. A gap here is a term that
	// renders in English on an otherwise-translated page.
	c := catalogue(t)
	en := c.For("en")

	var ui []string
	for _, id := range shippedTermIDs(t) {
		if !isDomainNoun(id) {
			ui = append(ui, id)
		}
	}
	if len(ui) == 0 {
		t.Fatal("found no interface terms to check")
	}

	for _, locale := range c.Locales() {
		if locale == "en" {
			continue
		}
		t.Run(locale, func(t *testing.T) {
			r := c.For(locale)
			var missing []string
			for _, id := range ui {
				if !r.Has(id) {
					missing = append(missing, id)
				}
			}
			if len(missing) > 0 {
				t.Errorf("%d interface terms fall back to English: %v", len(missing), missing)
			}
			_ = en
		})
	}
}

// placeholders finds {name} and the {n, plural, …} / {v, select, …} argument
// names, ignoring the arm bodies.
var placeholder = regexp.MustCompile(`\{\s*([a-zA-Z][a-zA-Z0-9_]*)\s*[,}]`)

func placeholdersIn(s string) map[string]bool {
	out := map[string]bool{}
	for _, m := range placeholder.FindAllStringSubmatch(s, -1) {
		out[m[1]] = true
	}
	return out
}

func TestTranslationsKeepEveryPlaceholder(t *testing.T) {
	// The highest-value check in this file. A translator who drops {days} gets
	// a grammatical sentence with the number missing — no error, no warning,
	// and nobody notices until a reader asks what the chart is measuring.
	bundles := shippedBundles(t)
	base := bundles["en"]

	for code, b := range bundles {
		if code == "en" {
			continue
		}
		t.Run(code, func(t *testing.T) {
			for id, translated := range b.Terms {
				english, ok := base.Terms[id]
				if !ok {
					t.Errorf("%s translates %q, which en.yaml does not define", code, id)
					continue
				}

				want, got := placeholdersIn(english), placeholdersIn(translated)
				for name := range want {
					if !got[name] {
						t.Errorf("%s: %q drops the {%s} placeholder\n  en: %s\n  %s: %s",
							code, id, name, english, code, translated)
					}
				}
				for name := range got {
					if !want[name] {
						t.Errorf("%s: %q adds a {%s} placeholder nothing supplies\n  en: %s\n  %s: %s",
							code, id, name, english, code, translated)
					}
				}
			}
		})
	}
}

// cldrCategory is the plural category x/text selects for a whole number.
func cldrCategory(tag language.Tag, n int) string {
	switch plural.Cardinal.MatchPlural(tag, n, 0, 0, 0, 0) {
	case plural.Zero:
		return "zero"
	case plural.One:
		return "one"
	case plural.Two:
		return "two"
	case plural.Few:
		return "few"
	case plural.Many:
		return "many"
	default:
		return "other"
	}
}

// armsIn returns the arm names a plural message defines.
var armName = regexp.MustCompile(`(?:^|\s)(=\d+|zero|one|two|few|many|other)\s*\{`)

func armsIn(msg string) map[string]bool {
	out := map[string]bool{}
	for _, m := range armName.FindAllStringSubmatch(msg, -1) {
		out[m[1]] = true
	}
	return out
}

// TestPluralTermsDefineAnArmForEveryCategoryTheyCanReach is the CLDR check.
//
// Falling through to "other" is legal and sometimes correct — Chinese has only
// that one, and Arabic's zero, many and other genuinely share a form. What is
// not correct is a language that *distinguishes* a category writing no arm for
// it: Russian without "few" renders "2 минут назад" where it should say "2
// минуты назад", and Arabic without "two" renders a plural where the language
// has a dual.
//
// The assertion is on the message source rather than on rendered output,
// because two categories legitimately sharing a form makes rendered output an
// unreliable signal — and because it is the file a translator edits.
func TestPluralTermsDefineAnArmForEveryCategoryTheyCanReach(t *testing.T) {
	// The counts this dashboard actually shows: seconds, minutes, hours and
	// days since a reading, and incident durations.
	counts := []int{0, 1, 2, 3, 5, 11, 21, 100}

	for code, b := range shippedBundles(t) {
		tag, err := language.Parse(code)
		if err != nil {
			t.Fatalf("locale %q: %v", code, err)
		}

		t.Run(code, func(t *testing.T) {
			for id, msg := range b.Terms {
				if !strings.Contains(msg, ", plural,") {
					continue
				}
				arms := armsIn(msg)

				for _, n := range counts {
					// An exact arm satisfies the count on its own.
					if arms["="+strconv.Itoa(n)] {
						continue
					}
					cat := cldrCategory(tag, n)
					if !arms[cat] && !arms["other"] {
						t.Errorf("%s: %q has no %q arm and no fallback for n=%d\n  %s",
							code, id, cat, n, msg)
						continue
					}
					if !arms[cat] && cat != "other" {
						t.Errorf("%s: %q falls through to \"other\" for n=%d, "+
							"but this language has a distinct %q form\n  %s",
							code, id, n, cat, msg)
					}
				}
			}
		})
	}
}

func TestArabicRendersItsDualAndItsCategories(t *testing.T) {
	// Arabic is the reason the ICU parser delegates category selection to CLDR
	// instead of guessing, so it gets an explicit assertion rather than only a
	// count. A dual that reads as a plural is the error a non-speaker cannot
	// see in a screenshot.
	r := catalogue(t).For("ar")

	for _, tc := range []struct {
		n    float64
		want string // the distinguishing fragment
	}{
		{0, "الآن"},        // the exact =0 arm: "now"
		{1, "دقيقة واحدة"}, // one
		{2, "دقيقتين"},     // two — a dual, not a plural
		{3, "دقائق"},       // few
		{11, "دقيقة"},      // many
	} {
		got := r.Text("time.ago.minute", map[string]any{"n": tc.n})
		if tc.n == 0 {
			// time.ago.minute has no =0 arm; check the seconds term for that.
			got = r.Text("time.ago.second", map[string]any{"n": tc.n})
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("n=%v rendered %q, want it to contain %q", tc.n, got, tc.want)
		}
	}
}

func TestRussianRendersItsThreeCountForms(t *testing.T) {
	r := catalogue(t).For("ru")

	for _, tc := range []struct {
		n    float64
		want string
	}{
		{1, "1 минуту назад"},   // one
		{2, "2 минуты назад"},   // few
		{5, "5 минут назад"},    // many
		{21, "21 минуту назад"}, // one again — 21, not "21 минут"
	} {
		if got := r.Text("time.ago.minute", map[string]any{"n": tc.n}); got != tc.want {
			t.Errorf("n=%v rendered %q, want %q", tc.n, got, tc.want)
		}
	}
}

// TestNoPlaceholderSurvivesRendering catches the mismatch between what a widget
// passes and what a term declares.
//
// This is the failure mode with no symptom. A message given a parameter it does
// not name substitutes nothing, so "Peak {v} · {d}" rendered "Peak  · " — a
// label that looks like a styling quirk rather than missing data. A plural given
// the wrong argument name is louder but no more obvious: it leaves the literal
// "#" on the page, which reads as a bullet.
//
// Both shipped for a while. Scanning the rendered output in every locale is the
// only check that would have found them, because both are correct YAML, correct
// Go, and correct ICU.
func TestNoPlaceholderSurvivesRendering(t *testing.T) {
	h := dashboard(t)

	unresolved := regexp.MustCompile(`\{[a-zA-Z][a-zA-Z0-9_]*\}`)
	// A "#" bounded by whitespace in text content. Hex colours, fragment links
	// and HTML entities all fail this on purpose.
	loneHash := regexp.MustCompile(`(^|\s)#(\s|$)`)
	tags := regexp.MustCompile(`(?s)<(script|style)[^>]*>.*?</(script|style)>`)
	markup := regexp.MustCompile(`<[^>]+>`)

	for _, locale := range catalogue(t).Locales() {
		// The fragments are here as well as the pages. Every one of them is
		// something a reader actually sees, and the drawer's opportunity tab
		// reached production untested by any of these because nothing requested
		// it — no page load renders a tab the reader has not chosen.
		for _, path := range []string{
			"/", "/?q=pan", "/?status=major", "/?scope=state", "/?signals=opportunity",
			"/service/aadhaar", "/service/aadhaar?tab=opportunity",
			"/service/aadhaar?tab=errors",
			"/service/aadhaar?tab=traffic", "/service/aadhaar?tab=incidents",
			"/fragments/leaderboard", "/fragments/signals?signals=opportunity",
			"/fragments/service/aadhaar?tab=opportunity",
			"/fragments/service/aadhaar/pane?tab=errors",
		} {
			t.Run(locale+path, func(t *testing.T) {
				sep := "?"
				if strings.Contains(path, "?") {
					sep = "&"
				}
				body := get(t, h, path+sep+"lang="+locale)
				text := markup.ReplaceAllString(tags.ReplaceAllString(body, ""), " ")

				if m := unresolved.FindString(text); m != "" {
					t.Errorf("unsubstituted placeholder %s reached the page", m)
				}
				if loneHash.MatchString(text) {
					t.Errorf("a bare \"#\" reached the page: a plural was given an "+
						"argument name its message does not declare\n  near: %s",
						around(text, loneHash))
				}
			})
		}
	}
}

// around returns a little of the text either side of a match, for the failure message.
func around(text string, re *regexp.Regexp) string {
	loc := re.FindStringIndex(text)
	if loc == nil {
		return ""
	}
	start := max(loc[0]-60, 0)
	end := min(loc[1]+60, len(text))
	return strings.Join(strings.Fields(text[start:end]), " ")
}

// TestNoTemplateContainsItsOwnCopy catches text hardcoded into a template.
//
// A literal in an HTML file never reaches the locale files, so it renders in
// English on every page in every language and no amount of translating fixes
// it. Two did exactly that: the no-JavaScript "apply" button and the "clear"
// link, which hid for as long as they did because neither appears in a normal
// browser session.
//
// The check reads the templates rather than the rendered page. Rendered output
// cannot tell hardcoded copy from the 178 deliberately-English Indian service
// names in the example config — most of the page is domain vocabulary — but a
// template has no business containing any prose at all.
func TestNoTemplateContainsItsOwnCopy(t *testing.T) {
	// Template actions produce the text; what is left between tags should be
	// punctuation and whitespace at most.
	actions := regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	comments := regexp.MustCompile(`(?s)<!--.*?-->`)
	inline := regexp.MustCompile(`(?s)<(script|style|svg)[^>]*>.*?</(script|style|svg)>`)
	tags := regexp.MustCompile(`(?s)<[^>]*>`)
	word := regexp.MustCompile(`[A-Za-z]{2,}`)

	err := fs.WalkDir(webFS, "web/templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		raw, err := webFS.ReadFile(path)
		if err != nil {
			return err
		}

		text := string(raw)
		for _, re := range []*regexp.Regexp{actions, comments, inline, tags} {
			text = re.ReplaceAllString(text, " ")
		}

		for _, w := range word.FindAllString(text, -1) {
			// &nbsp; and friends survive tag-stripping as bare entity names.
			if strings.EqualFold(w, "nbsp") || strings.EqualFold(w, "amp") {
				continue
			}
			t.Errorf("%s contains the literal word %q between tags; "+
				"copy belongs in a locale file, or it renders in English "+
				"whichever language a reader picks", path, w)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking templates: %v", err)
	}
}

func TestOnlyArabicIsRightToLeft(t *testing.T) {
	c := catalogue(t)
	for _, locale := range c.Locales() {
		want := "ltr"
		if locale == "ar" {
			want = "rtl"
		}
		if got := c.For(locale).Direction(); got != want {
			t.Errorf("%s direction = %q, want %q", locale, got, want)
		}
	}
}

func TestEveryLocaleNamesItselfInItsOwnScript(t *testing.T) {
	// The language picker is the one control a reader who cannot read the
	// current language still has to be able to use, so "Arabic" is the wrong
	// label and "العربية" is the right one.
	want := map[string]string{
		"en": "English", "hi": "हिन्दी", "ar": "العربية", "zh": "中文",
		"es": "Español", "fr": "Français", "ru": "Русский", "sw": "Kiswahili",
	}

	got := catalogue(t).Names()
	for code, name := range want {
		if got[code] != name {
			t.Errorf("%s is named %q, want %q", code, got[code], name)
		}
	}
}

// TestNoTermIDLeaksInAnyLocale extends the English-only check to all eight.
//
// A term id reaching the page looks like a bug to a reader and is one. Running
// it per locale catches the case where a translation exists but its id was
// mistyped, which English alone cannot see.
func TestNoTermIDLeaksInAnyLocale(t *testing.T) {
	h := dashboard(t)

	for _, locale := range catalogue(t).Locales() {
		for _, path := range []string{
			"/", "/?signals=opportunity",
			"/service/aadhaar", "/service/aadhaar?tab=opportunity",
			"/service/aadhaar?tab=errors",
			"/service/aadhaar?tab=traffic", "/service/aadhaar?tab=incidents",
			"/fragments/service/aadhaar?tab=opportunity",
			"/fragments/service/aadhaar/pane?tab=errors",
		} {
			t.Run(locale+path, func(t *testing.T) {
				sep := "?"
				if strings.Contains(path, "?") {
					sep = "&"
				}
				body := get(t, h, path+sep+"lang="+locale)
				for _, m := range termIDs.FindAllStringSubmatch(body, -1) {
					id := m[1]
					if strings.Contains(id, "/") || strings.HasSuffix(id, ".js") {
						continue
					}
					t.Errorf("unresolved term id in %s: %q", locale, id)
				}
			})
		}
	}
}

// TestEveryLocaleRendersTheWholeDashboard is the blunt end-to-end check: each
// locale must produce a full page, not a 500 from a malformed message.
func TestEveryLocaleRendersTheWholeDashboard(t *testing.T) {
	h := dashboard(t)

	for _, locale := range catalogue(t).Locales() {
		t.Run(locale, func(t *testing.T) {
			body := get(t, h, "/?lang="+locale)
			if n := rows(body); n == 0 {
				t.Errorf("%s renders no leaderboard rows", locale)
			}
			if !strings.Contains(body, `lang="`+locale+`"`) {
				t.Errorf("%s does not set lang on the document", locale)
			}
		})
	}
}
