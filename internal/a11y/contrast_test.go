package a11y

import (
	"math"
	"strings"
	"testing"
)

func TestLevelThresholds(t *testing.T) {
	for _, tc := range []struct {
		level Level
		min   float64
		sc    string
		str   string
	}{
		{Text, 4.5, "1.4.3 Contrast (Minimum)", "text"},
		{LargeText, 3, "1.4.3 Contrast (Minimum)", "large text"},
		{NonText, 3, "1.4.11 Non-text Contrast", "non-text"},
	} {
		if got := tc.level.Min(); got != tc.min {
			t.Errorf("%v.Min() = %v, want %v", tc.level, got, tc.min)
		}
		if got := tc.level.SC(); got != tc.sc {
			t.Errorf("%v.SC() = %q, want %q", tc.level, got, tc.sc)
		}
		if got := tc.level.String(); got != tc.str {
			t.Errorf("Level(%d).String() = %q, want %q", tc.level, got, tc.str)
		}
	}
}

// The WCAG ratio for black on white is exactly 21:1, and any colour against
// itself is exactly 1:1. Those two anchors catch a transposed coefficient or a
// missing gamma step, which a spot check of a mid-tone would not.
func TestRatioAnchors(t *testing.T) {
	black := Colour{R: 0, G: 0, B: 0, A: 1}
	white := Colour{R: 255, G: 255, B: 255, A: 1}

	if got := Ratio(black, white); math.Abs(got-21) > 1e-9 {
		t.Errorf("Ratio(black, white) = %v, want 21", got)
	}
	if got := Ratio(white, black); math.Abs(got-21) > 1e-9 {
		t.Errorf("Ratio(white, black) = %v, want 21 (ratio is symmetric)", got)
	}
	if got := Ratio(white, white); math.Abs(got-1) > 1e-9 {
		t.Errorf("Ratio(white, white) = %v, want 1", got)
	}

	// Both sides of the sRGB transfer function's kink at 0.03928.
	if got := Luminance(Colour{R: 8, G: 8, B: 8, A: 1}); got <= 0 {
		t.Errorf("Luminance of a near-black = %v, want > 0 (linear segment)", got)
	}
	if got := Luminance(Colour{R: 200, G: 200, B: 200, A: 1}); got <= 0.5 {
		t.Errorf("Luminance of a light grey = %v, want > 0.5 (power segment)", got)
	}
}

// Dimming is the failure mode this package exists to catch: a pair that clears
// 4.5:1 opaque can fall well under it at 70% opacity.
func TestOpacityIsCompositedNotIgnored(t *testing.T) {
	fg, err := ParseColour("#1F6B4A")
	if err != nil {
		t.Fatal(err)
	}
	bg, err := ParseColour("#E8F2EC")
	if err != nil {
		t.Fatal(err)
	}

	opaque := Ratio(fg, bg)
	if opaque < 4.5 {
		t.Fatalf("fixture is wrong: opaque ratio %v should pass", opaque)
	}

	fg.A = 0.7
	dimmed := Ratio(fg, bg)
	if dimmed >= 4.5 {
		t.Errorf("ratio at 70%% opacity = %v, want below 4.5 — opacity is not being composited", dimmed)
	}
	if dimmed >= opaque {
		t.Errorf("dimming raised the ratio (%v -> %v)", opaque, dimmed)
	}
}

func TestOverAndMix(t *testing.T) {
	black := Colour{R: 0, G: 0, B: 0, A: 1}
	white := Colour{R: 255, G: 255, B: 255, A: 1}

	// An opaque foreground is returned unchanged.
	if got := Over(black, white); got != black {
		t.Errorf("Over(opaque, bg) = %+v, want %+v", got, black)
	}
	// Half-transparent black over white is mid grey.
	half := Colour{R: 0, G: 0, B: 0, A: 0.5}
	if got := Over(half, white); math.Abs(got.R-127.5) > 1e-9 || got.A != 1 {
		t.Errorf("Over(50%% black, white) = %+v, want R=127.5 A=1", got)
	}

	if got := Mix(black, white, 0.5); math.Abs(got.R-127.5) > 1e-9 {
		t.Errorf("Mix(black, white, 0.5).R = %v, want 127.5", got.R)
	}
	// Weights outside 0–1 clamp rather than extrapolating into invalid colours.
	if got := Mix(black, white, 2); got.R != 0 {
		t.Errorf("Mix with weight 2 = %+v, want fully black (clamped)", got)
	}
	if got := Mix(black, white, -1); got.R != 255 {
		t.Errorf("Mix with weight -1 = %+v, want fully white (clamped)", got)
	}
}

// A translucent background has to be flattened too, or the ratio is computed
// against a colour that is never painted.
func TestRatioFlattensTranslucentBackground(t *testing.T) {
	fg := Colour{R: 0, G: 0, B: 0, A: 1}
	bg := Colour{R: 0, G: 0, B: 0, A: 0} // fully transparent: paints as white
	if got := Ratio(fg, bg); math.Abs(got-21) > 1e-9 {
		t.Errorf("Ratio over a transparent background = %v, want 21", got)
	}
}

func TestParseColour(t *testing.T) {
	for _, tc := range []struct {
		in             string
		r, g, b, alpha float64
	}{
		{"#fff", 255, 255, 255, 1},
		{"#FFF", 255, 255, 255, 1},
		{"#f00f", 255, 0, 0, 1},
		{"#1A1917", 26, 25, 23, 1},
		{"  #1A1917  ", 26, 25, 23, 1},
		{"#1A191780", 26, 25, 23, 128.0 / 255},
		{"rgb(26, 25, 23)", 26, 25, 23, 1},
		{"rgb(26 25 23)", 26, 25, 23, 1},
		{"rgba(26, 25, 23, 0.5)", 26, 25, 23, 0.5},
		{"rgb(26 25 23 / 50%)", 26, 25, 23, 0.5},
		{"rgb(0 0 0 / 28%)", 0, 0, 0, 0.28},
		{"rgb(100% 0% 0%)", 255, 0, 0, 1},
	} {
		got, err := ParseColour(tc.in)
		if err != nil {
			t.Errorf("ParseColour(%q) returned %v", tc.in, err)
			continue
		}
		if math.Abs(got.R-tc.r) > 1e-6 || math.Abs(got.G-tc.g) > 1e-6 ||
			math.Abs(got.B-tc.b) > 1e-6 || math.Abs(got.A-tc.alpha) > 1e-6 {
			t.Errorf("ParseColour(%q) = %+v, want r=%v g=%v b=%v a=%v",
				tc.in, got, tc.r, tc.g, tc.b, tc.alpha)
		}
	}
}

func TestParseColourRejects(t *testing.T) {
	for _, in := range []string{
		"",                    // not a colour at all
		"2px",                 // a length, which a token map legitimately holds
		"cubic-bezier(0,0,1)", // a timing function that looks like a function call
		"#ff",                 // too short
		"#fffff",              // not a legal length
		"#GGGGGG",             // not hex
		"#GGGG",               // not hex, shorthand length
		"rgb",                 // no arguments
		"rgb(1,2,3",           // unterminated
		"rgb(1,2)",            // too few channels
		"rgb(1,2,3,4,5)",      // too many channels
		"rgb(a,b,c)",          // channels are not numbers
		"rgb(1 2 3 / x)",      // alpha is not a number
		"rgb(1 2 3 / x%)",     // alpha is not a percentage
		"rgba(1,2,3,z)",       // fourth channel is not a number
	} {
		if _, err := ParseColour(in); err == nil {
			t.Errorf("ParseColour(%q) succeeded, want an error", in)
		}
	}
}

// The contract is a claim about the templates, so it must at least be
// self-consistent: every token it names has to be one a theme is required to
// declare, or the obligation can never be evaluated.
func TestContractOnlyNamesRequiredTokens(t *testing.T) {
	required := map[string]bool{}
	for _, name := range []string{
		"--bg", "--card", "--fg", "--muted-fg",
		"--border-subtle", "--border-strong",
		"--primary", "--primary-fg", "--accent",
		"--status-ok", "--status-ok-bg",
		"--status-partial", "--status-partial-bg",
		"--status-major", "--status-major-bg",
		"--status-unknown", "--status-unknown-bg",
		"--status-maintenance", "--status-maintenance-bg",
	} {
		required[name] = true
	}

	pairs := Contract()
	if len(pairs) == 0 {
		t.Fatal("Contract() is empty")
	}
	for _, p := range pairs {
		if !required[p.Fg] {
			t.Errorf("contract names foreground %q, which is not a required token", p.Fg)
		}
		if !required[p.Bg] {
			t.Errorf("contract names background %q, which is not a required token", p.Bg)
		}
		if p.Where == "" {
			t.Errorf("obligation %s on %s has no Where; a failure would not say where to look", p.Fg, p.Bg)
		}
	}

	// --border-subtle is deliberately out of scope. If it ever gains an
	// obligation, that is a decision to make explicitly, not by accident.
	for _, p := range pairs {
		if p.Fg == "--border-subtle" || p.Bg == "--border-subtle" {
			t.Error("--border-subtle has an obligation; it is documented as separating content, not identifying controls")
		}
	}
}

func TestCheckFindsAndReports(t *testing.T) {
	// Deliberately illegible: mid grey on white fails text, and a near-white
	// border fails non-text.
	tokens := map[string]string{
		"--bg": "#FFFFFF", "--card": "#FFFFFF",
		"--fg": "#999999", "--muted-fg": "#AAAAAA",
		"--border-subtle": "#EEEEEE", "--border-strong": "#FAFAFA",
		"--primary": "#FFFFFF", "--primary-fg": "#FFFFFF", "--accent": "#CCCCCC",
	}
	for _, s := range Statuses {
		tokens["--status-"+s] = "#BBBBBB"
		tokens["--status-"+s+"-bg"] = "#FFFFFF"
	}

	found := Check("light", tokens)
	if len(found) == 0 {
		t.Fatal("Check found nothing in a palette that is entirely illegible")
	}
	for _, f := range found {
		if f.Mode != "light" {
			t.Errorf("finding has mode %q, want %q", f.Mode, "light")
		}
		if f.Ratio >= f.Level.Min() {
			t.Errorf("finding reports ratio %v, which meets its own minimum %v", f.Ratio, f.Level.Min())
		}
		msg := f.String()
		for _, want := range []string{f.Fg, f.Bg, "WCAG", f.Where} {
			if !strings.Contains(msg, want) {
				t.Errorf("message %q does not mention %q", msg, want)
			}
		}
	}
}

func TestCheckPassesALegiblePalette(t *testing.T) {
	// Black on white everywhere: nothing can fail.
	tokens := map[string]string{
		"--bg": "#FFFFFF", "--card": "#FFFFFF",
		"--fg": "#000000", "--muted-fg": "#000000",
		"--border-subtle": "#000000", "--border-strong": "#000000",
		"--primary": "#FFFFFF", "--primary-fg": "#000000", "--accent": "#000000",
	}
	for _, s := range Statuses {
		tokens["--status-"+s] = "#000000"
		tokens["--status-"+s+"-bg"] = "#FFFFFF"
	}
	if found := Check("light", tokens); len(found) != 0 {
		t.Errorf("Check reported %d findings for black on white: %v", len(found), found[0])
	}
}

// A missing token is the required-token check's business. Reporting it here too
// would bury the one real error under a list of its consequences.
func TestCheckSkipsUnevaluableObligations(t *testing.T) {
	for _, tokens := range []map[string]string{
		{},                                       // nothing at all
		{"--fg": "#000000"},                      // foreground only
		{"--bg": "#FFFFFF"},                      // background only
		{"--fg": "2px", "--bg": "#FFFFFF"},       // foreground is not a colour
		{"--fg": "#000000", "--bg": "260ms"},     // background is not a colour
		{"--fg": "not-a-colour", "--bg": "#FFF"}, // unparseable foreground
	} {
		if found := Check("light", tokens); len(found) != 0 {
			t.Errorf("Check(%v) reported %v, want no findings", tokens, found[0])
		}
	}
}

func TestFindingMentionsOpacityWhenDimmed(t *testing.T) {
	dimmed := Finding{
		Pair:  Pair{Fg: "--status-ok", Bg: "--status-ok-bg", Level: Text, Where: "a chip count", Alpha: 0.7},
		Mode:  "light",
		Ratio: 3.07,
	}
	if msg := dimmed.String(); !strings.Contains(msg, "70% opacity") {
		t.Errorf("message %q does not mention the opacity that caused the failure", msg)
	}

	opaque := Finding{
		Pair:  Pair{Fg: "--fg", Bg: "--bg", Level: Text, Where: "body text"},
		Mode:  "dark",
		Ratio: 2,
	}
	if msg := opaque.String(); strings.Contains(msg, "opacity") {
		t.Errorf("message %q mentions opacity for an opaque pair", msg)
	}
}

// An obligation recording an Alpha must actually apply it, or the dimming would
// be documented and unchecked.
func TestCheckAppliesPairAlpha(t *testing.T) {
	tokens := map[string]string{"--fg": "#767676", "--bg": "#FFFFFF"}

	opaque := []Pair{{Fg: "--fg", Bg: "--bg", Level: Text, Where: "x"}}
	dim := []Pair{{Fg: "--fg", Bg: "--bg", Level: Text, Where: "x", Alpha: 0.5}}

	if got := checkPairs("light", tokens, opaque); len(got) != 0 {
		t.Fatalf("#767676 on white should pass at full opacity, got %v", got[0])
	}
	if got := checkPairs("light", tokens, dim); len(got) != 1 {
		t.Error("#767676 on white at 50% opacity should fail")
	}
}
