package theme_test

import (
	"strings"
	"testing"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/theme"
)

func sample() config.Theme {
	return config.Theme{
		Light: map[string]string{
			"--bg":     "#FAF9F5",
			"--fg":     "#1A1917",
			"--accent": "#3D5A5B",
		},
		Dark: map[string]string{
			"--bg":     "#16150F",
			"--fg":     "#F3F1E9",
			"--accent": "#8FB6B4",
		},
		Tokens: map[string]string{
			"--radius-lg": "10px",
			"--radius-sm": "2px",
		},
		Fonts: config.Fonts{
			Body: config.FontFamily{
				Family: "Outfit",
				Stack:  "Outfit, system-ui, sans-serif",
				Faces: []config.FontFace{
					{File: "outfit-400.woff2", Weight: "400", Style: "normal", Display: "swap", UnicodeRange: "U+0000-00FF"},
					{File: "outfit-700.woff2", Weight: "700", Style: "normal", Display: "swap"},
				},
			},
			Serif: config.FontFamily{
				Family: "Playfair Display",
				Stack:  `"Playfair Display", Georgia, serif`,
			},
		},
	}
}

func TestCSSIsDeterministic(t *testing.T) {
	// Map iteration order is random in Go. Without sorting, the stylesheet
	// would differ between runs and its ETag would change on every restart,
	// defeating client caching.
	first := theme.CSS(sample())
	for range 20 {
		if got := theme.CSS(sample()); got != first {
			t.Fatal("CSS output varies between calls; token ordering is not deterministic")
		}
	}
}

func TestTokensAreSorted(t *testing.T) {
	css := theme.CSS(sample())
	root := blockAfter(t, css, ":root{")

	for _, pair := range [][2]string{
		{"--accent", "--bg"},
		{"--bg", "--fg"},
	} {
		if strings.Index(root, pair[0]) > strings.Index(root, pair[1]) {
			t.Errorf("%s should sort before %s", pair[0], pair[1])
		}
	}
}

func TestLightTokensAreTheDefault(t *testing.T) {
	css := theme.CSS(sample())
	root := blockAfter(t, css, ":root{")

	for _, want := range []string{"--bg:#FAF9F5", "--fg:#1A1917", "--accent:#3D5A5B"} {
		if !strings.Contains(root, want) {
			t.Errorf(":root block is missing %q:\n%s", want, root)
		}
	}
}

func TestStaticTokensShareTheRootBlock(t *testing.T) {
	// Radii and easing do not change between themes, so they belong with the
	// defaults rather than being repeated in each mode.
	root := blockAfter(t, theme.CSS(sample()), ":root{")

	for _, want := range []string{"--radius-lg:10px", "--radius-sm:2px"} {
		if !strings.Contains(root, want) {
			t.Errorf(":root block is missing static token %q", want)
		}
	}
}

func TestFontStacksBecomeTokens(t *testing.T) {
	// Templates reference var(--font-body), never a family name, so that
	// swapping the typeface is a theme.yaml edit.
	root := blockAfter(t, theme.CSS(sample()), ":root{")

	if !strings.Contains(root, "--font-body:Outfit, system-ui, sans-serif") {
		t.Errorf("missing --font-body token:\n%s", root)
	}
	if !strings.Contains(root, `--font-serif:"Playfair Display", Georgia, serif`) {
		t.Errorf("missing --font-serif token:\n%s", root)
	}
}

func TestDarkModeRespondsToBothSystemAndExplicitChoice(t *testing.T) {
	css := theme.CSS(sample())

	mediaAt := strings.Index(css, "@media (prefers-color-scheme:dark)")
	if mediaAt < 0 {
		t.Fatal("no prefers-color-scheme block; the OS setting would be ignored")
	}
	explicitAt := strings.Index(css, `:root[data-theme="dark"]{`)
	if explicitAt < 0 {
		t.Fatal("no explicit dark rule; the theme toggle would not work")
	}

	// Both selectors have the same specificity, so the explicit choice can only
	// win by coming later in source order.
	if explicitAt < mediaAt {
		t.Error(`:root[data-theme="dark"] precedes the media query, so an explicit dark choice would lose to the OS preference`)
	}

	// And the media query must exempt an explicit light choice, or a user on a
	// dark-mode OS could never select light.
	if !strings.Contains(css, `:root:not([data-theme="light"])`) {
		t.Error("the media query does not exempt an explicit light choice")
	}
}

func TestDarkTokensAppearInBothDarkRules(t *testing.T) {
	css := theme.CSS(sample())

	media := blockAfter(t, css, `:root:not([data-theme="light"]){`)
	explicit := blockAfter(t, css, `:root[data-theme="dark"]{`)

	for _, want := range []string{"--bg:#16150F", "--fg:#F3F1E9", "--accent:#8FB6B4"} {
		if !strings.Contains(media, want) {
			t.Errorf("system-preference dark rule is missing %q", want)
		}
		if !strings.Contains(explicit, want) {
			t.Errorf("explicit dark rule is missing %q", want)
		}
	}
}

func TestFontFacesAreEmitted(t *testing.T) {
	css := theme.CSS(sample())

	if n := strings.Count(css, "@font-face{"); n != 2 {
		t.Errorf("got %d @font-face rules, want 2", n)
	}
	for _, want := range []string{
		`font-family:"Outfit"`,
		`url("/assets/fonts/outfit-400.woff2") format("woff2")`,
		"font-weight:400",
		"font-style:normal",
		"font-display:swap",
		"unicode-range:U+0000-00FF",
		`url("/assets/fonts/outfit-700.woff2") format("woff2")`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("stylesheet is missing %q", want)
		}
	}

	// The second face declares no unicode-range; emitting an empty one would be
	// invalid CSS and would silently drop the whole rule in some browsers.
	if strings.Contains(css, "unicode-range:;") {
		t.Error("an empty unicode-range was emitted")
	}
}

func TestExternalFontURLIsUsedVerbatim(t *testing.T) {
	th := sample()
	th.Fonts.Body.Faces = []config.FontFace{
		{URL: "https://fonts.example.test/outfit.woff2", Weight: "400", Style: "normal"},
	}

	css := theme.CSS(th)

	if !strings.Contains(css, `url("https://fonts.example.test/outfit.woff2") format("woff2")`) {
		t.Errorf("external font url was not used verbatim:\n%s", css)
	}
	if strings.Contains(css, "/assets/fonts/https") {
		t.Error("an external url was mistakenly prefixed with the local asset path")
	}
}

func TestNoFacesMeansNoFontFaceRules(t *testing.T) {
	th := sample()
	th.Fonts.Body.Faces = nil

	css := theme.CSS(th)

	if strings.Contains(css, "@font-face") {
		t.Errorf("@font-face emitted for a family with no faces:\n%s", css)
	}
	// The stack token must survive: a deployment using only system fonts still
	// needs var(--font-body) to resolve.
	if !strings.Contains(css, "--font-body:Outfit, system-ui, sans-serif") {
		t.Error("--font-body was dropped along with the faces")
	}
}

func TestEmptyThemeProducesValidEmptyRules(t *testing.T) {
	// A theme this bare cannot pass validation, but the generator must stay
	// total: it is a pure function and has no way to report a problem.
	css := theme.CSS(config.Theme{})

	if strings.Contains(css, "{}{") || strings.Contains(css, ";;") {
		t.Errorf("degenerate output:\n%s", css)
	}
	if !strings.Contains(css, ":root{") {
		t.Errorf("no :root block at all:\n%s", css)
	}
}

// blockAfter returns the text between marker and its matching closing brace.
func blockAfter(t *testing.T, css, marker string) string {
	t.Helper()
	i := strings.Index(css, marker)
	if i < 0 {
		t.Fatalf("stylesheet has no %q:\n%s", marker, css)
	}
	rest := css[i+len(marker):]
	j := strings.Index(rest, "}")
	if j < 0 {
		t.Fatalf("block %q is never closed", marker)
	}
	return rest[:j]
}
