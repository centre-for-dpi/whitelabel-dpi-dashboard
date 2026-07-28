// Package a11y holds the accessibility obligations this dashboard enforces on
// itself, expressed as code rather than as a review checklist.
//
// Contrast is the part of WCAG that a white-label product cannot verify once and
// forget. Every deployment supplies its own palette, so every deployment can
// break WCAG 1.4.3 and 1.4.11 without touching a line of the dashboard. The only
// place that can be caught reliably is the moment the palette is loaded — which
// is why this package is pure, takes nothing but a token map, and is called from
// config validation. A brand whose colours fail does not start.
//
// It works only because of a property the rest of the codebase maintains: the
// stylesheet consumes var(--token) exclusively and no template contains a
// colour, so the token map really is the whole palette. If a colour were ever
// hardcoded in CSS, this package would still pass and the page would still fail.
// The structural layer asserts that separately.
package a11y

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Level is the contrast floor a pair of colours must clear, and the success
// criterion that demands it.
type Level int

const (
	// Text is body-sized text: WCAG 1.4.3 Contrast (Minimum), 4.5:1.
	Text Level = iota
	// LargeText is text at 24px, or 18.66px bold, and above: 1.4.3 at 3:1.
	LargeText
	// NonText is the visual boundary of a user interface component, or a
	// graphical object carrying meaning: WCAG 1.4.11 Non-text Contrast, 3:1.
	NonText
)

// Min is the required ratio.
func (l Level) Min() float64 {
	if l == Text {
		return 4.5
	}
	return 3
}

// SC names the success criterion, so a failure tells the reader which rule they
// have broken rather than only that a number is too small.
func (l Level) SC() string {
	if l == NonText {
		return "1.4.11 Non-text Contrast"
	}
	return "1.4.3 Contrast (Minimum)"
}

func (l Level) String() string {
	switch l {
	case Text:
		return "text"
	case LargeText:
		return "large text"
	default:
		return "non-text"
	}
}

// Pair is one contrast obligation between two named tokens.
//
// Where names it in terms of what the reader sees, not what the CSS does. A
// failure that says "the boundary of the period selector" is actionable; one
// that says "--border-strong on --bg" sends the reader looking for the selector.
type Pair struct {
	Fg, Bg string
	Level  Level
	Where  string

	// Alpha is the opacity the foreground is drawn at, when the design dims it.
	// Zero means fully opaque. Dimmed text is the single most common way a
	// palette that looks compliant on paper fails in the browser, so the
	// obligation records the dimming rather than pretending it away.
	Alpha float64
}

// Statuses are the five outcome tones. Kept in one place because every one of
// them appears in four different contexts and the obligations must not drift
// apart.
var Statuses = []string{"ok", "partial", "major", "unknown", "maintenance"}

// Contract is every contrast obligation the shipped markup creates.
//
// This is a claim about the templates: each entry exists because some element
// really does put that foreground on that background. Adding a colour
// combination to a template without adding it here means it is not checked, so
// the two must be changed together.
func Contract() []Pair {
	pairs := []Pair{
		{Fg: "--fg", Bg: "--bg", Level: Text, Where: "body text on the page"},
		{Fg: "--fg", Bg: "--card", Level: Text, Where: "body text inside a card"},
		{Fg: "--muted-fg", Bg: "--bg", Level: Text, Where: "secondary text on the page"},
		{Fg: "--muted-fg", Bg: "--card", Level: Text, Where: "secondary text inside a card"},
		{Fg: "--accent", Bg: "--bg", Level: Text, Where: "links and disclosure summaries"},
		{Fg: "--accent", Bg: "--card", Level: Text, Where: "links inside a card"},
		{Fg: "--primary-fg", Bg: "--primary", Level: Text, Where: "the primary button label"},

		// The focus ring is the one graphical object every keyboard reader
		// depends on, and it is drawn against both surfaces.
		{Fg: "--accent", Bg: "--bg", Level: NonText, Where: "the focus ring on the page"},
		{Fg: "--accent", Bg: "--card", Level: NonText, Where: "the focus ring inside a card"},

		// Control boundaries. --border-subtle is deliberately absent: it
		// separates content rather than identifying a control, which 1.4.11
		// does not cover. The structural layer asserts that no interactive
		// element uses it as its own boundary, which is what would move it
		// into scope.
		{Fg: "--border-strong", Bg: "--bg", Level: NonText, Where: "the boundary of selects, inputs and toggles on the page"},
		{Fg: "--border-strong", Bg: "--card", Level: NonText, Where: "the boundary of controls inside a card"},
	}

	for _, s := range Statuses {
		fg, bg := "--status-"+s, "--status-"+s+"-bg"
		pairs = append(pairs,
			Pair{Fg: fg, Bg: bg, Level: Text, Where: "the " + s + " status chip"},
			Pair{Fg: fg, Bg: "--bg", Level: Text, Where: "the " + s + " legend entry"},
			Pair{Fg: fg, Bg: "--card", Level: Text, Where: "the " + s + " figure inside a card"},
			Pair{Fg: "--fg", Bg: bg, Level: Text, Where: "text highlighted in the " + s + " tone"},
			// The segmented bar's proportions are its content, and adjacent
			// fills are pastels that cannot contrast with each other, so the
			// hairline between them is the only thing that makes two touching
			// segments read as two. That makes it a graphical object under
			// 1.4.11 rather than decoration.
			Pair{Fg: "--border-strong", Bg: bg, Level: NonText, Where: "the divider between the " + s + " bar segment and its neighbour"},
		)
	}
	return pairs
}

// Finding is one obligation the palette fails.
type Finding struct {
	Pair
	Mode  string
	Ratio float64
}

func (f Finding) String() string {
	dimmed := ""
	if f.Alpha > 0 && f.Alpha < 1 {
		dimmed = fmt.Sprintf(" at %g%% opacity", f.Alpha*100)
	}
	return fmt.Sprintf("%s: %s on %s%s is %.2f:1, below the %.1f:1 required by WCAG %s for %s (%s)",
		f.Mode, f.Fg, f.Bg, dimmed, f.Ratio, f.Level.Min(), f.Level.SC(), f.Level, f.Where)
}

// Check evaluates the contract against one mode's tokens.
//
// A token the contract names but the palette omits is not reported here: the
// required-token check in config validation already says so, and repeating it
// would bury the real failure under a list of consequences.
func Check(mode string, tokens map[string]string) []Finding {
	return checkPairs(mode, tokens, Contract())
}

func checkPairs(mode string, tokens map[string]string, pairs []Pair) []Finding {
	var out []Finding
	for _, p := range pairs {
		fgRaw, ok := tokens[p.Fg]
		if !ok {
			continue
		}
		bgRaw, ok := tokens[p.Bg]
		if !ok {
			continue
		}
		fg, err := ParseColour(fgRaw)
		if err != nil {
			continue
		}
		bg, err := ParseColour(bgRaw)
		if err != nil {
			continue
		}
		if p.Alpha > 0 {
			fg.A *= p.Alpha
		}
		r := Ratio(fg, bg)
		if r+1e-9 < p.Level.Min() {
			out = append(out, Finding{Pair: p, Mode: mode, Ratio: r})
		}
	}
	return out
}

// --- colour ----------------------------------------------------------------

// Colour is sRGB with straight alpha. Channels are 0–255 and kept as floats so
// compositing does not accumulate rounding error.
type Colour struct {
	R, G, B float64
	A       float64
}

// ParseColour reads the CSS colour notations a theme file may legitimately use.
//
// It deliberately understands a small set. A theme value it cannot parse is not
// an error here — an unparseable value is either not a colour (a radius, a
// duration) or one whose contrast cannot be known statically, and in both cases
// silence is the honest answer. What must never happen is treating an
// unparseable colour as passing, so callers skip the pair rather than scoring it.
func ParseColour(s string) (Colour, error) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "#"):
		return parseHex(s)
	case strings.HasPrefix(s, "rgb"):
		return parseRGB(s)
	}
	return Colour{}, fmt.Errorf("a11y: %q is not a colour this package can read", s)
}

func parseHex(s string) (Colour, error) {
	h := s[1:]
	// #RGB and #RGBA are shorthand for doubled nibbles.
	if len(h) == 3 || len(h) == 4 {
		var b strings.Builder
		for _, c := range h {
			b.WriteRune(c)
			b.WriteRune(c)
		}
		h = b.String()
	}
	if len(h) != 6 && len(h) != 8 {
		return Colour{}, fmt.Errorf("a11y: %q is not a 3, 4, 6 or 8 digit hex colour", s)
	}
	n, err := strconv.ParseUint(h, 16, 64)
	if err != nil {
		return Colour{}, fmt.Errorf("a11y: %q is not a hex colour: %w", s, err)
	}
	c := Colour{A: 1}
	if len(h) == 8 {
		c.A = float64(n&0xFF) / 255
		n >>= 8
	}
	c.B = float64(n & 0xFF)
	c.G = float64((n >> 8) & 0xFF)
	c.R = float64((n >> 16) & 0xFF)
	return c, nil
}

// parseRGB reads both the legacy comma form and the modern space form,
// including the slash-separated alpha and percentage channels.
func parseRGB(s string) (Colour, error) {
	open := strings.IndexByte(s, '(')
	if open < 0 || !strings.HasSuffix(s, ")") {
		return Colour{}, fmt.Errorf("a11y: %q is not an rgb() colour", s)
	}
	body := s[open+1 : len(s)-1]
	body = strings.ReplaceAll(body, ",", " ")

	alpha := 1.0
	if i := strings.IndexByte(body, '/'); i >= 0 {
		a, err := parseChannel(strings.TrimSpace(body[i+1:]), 1)
		if err != nil {
			return Colour{}, err
		}
		alpha = a
		body = body[:i]
	}

	parts := strings.Fields(body)
	if len(parts) != 3 && len(parts) != 4 {
		return Colour{}, fmt.Errorf("a11y: %q does not have three colour channels", s)
	}
	if len(parts) == 4 {
		a, err := parseChannel(parts[3], 1)
		if err != nil {
			return Colour{}, err
		}
		alpha = a
		parts = parts[:3]
	}

	c := Colour{A: alpha}
	dst := []*float64{&c.R, &c.G, &c.B}
	for i, p := range parts {
		v, err := parseChannel(p, 255)
		if err != nil {
			return Colour{}, err
		}
		*dst[i] = v
	}
	return c, nil
}

// parseChannel reads a number or a percentage, scaling percentages to full.
func parseChannel(s string, full float64) (float64, error) {
	if pct, ok := strings.CutSuffix(s, "%"); ok {
		v, err := strconv.ParseFloat(strings.TrimSpace(pct), 64)
		if err != nil {
			return 0, fmt.Errorf("a11y: %q is not a percentage: %w", s, err)
		}
		return v / 100 * full, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("a11y: %q is not a number: %w", s, err)
	}
	return v, nil
}

// Over composites a colour onto an opaque backdrop.
//
// WCAG's ratio is defined between two opaque colours, so a translucent
// foreground has to be flattened first. This is where dimmed text stops looking
// compliant: opacity multiplies towards the background, and a 4.7:1 pair at 70%
// opacity is nowhere near 4.5:1.
func Over(fg, bg Colour) Colour {
	if fg.A >= 1 {
		return Colour{R: fg.R, G: fg.G, B: fg.B, A: 1}
	}
	a := fg.A
	return Colour{
		R: fg.R*a + bg.R*(1-a),
		G: fg.G*a + bg.G*(1-a),
		B: fg.B*a + bg.B*(1-a),
		A: 1,
	}
}

// Mix blends two colours by weight, the srgb form of CSS color-mix().
func Mix(a, b Colour, weightA float64) Colour {
	w := math.Max(0, math.Min(1, weightA))
	return Colour{
		R: a.R*w + b.R*(1-w),
		G: a.G*w + b.G*(1-w),
		B: a.B*w + b.B*(1-w),
		A: a.A*w + b.A*(1-w),
	}
}

// Luminance is WCAG relative luminance.
func Luminance(c Colour) float64 {
	f := func(v float64) float64 {
		v /= 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*f(c.R) + 0.7152*f(c.G) + 0.0722*f(c.B)
}

// Ratio is the WCAG contrast ratio, flattening a translucent foreground onto
// the background first.
func Ratio(fg, bg Colour) float64 {
	bg = Over(bg, Colour{R: 255, G: 255, B: 255, A: 1})
	lf := Luminance(Over(fg, bg))
	lb := Luminance(bg)
	hi, lo := math.Max(lf, lb), math.Min(lf, lb)
	return (hi + 0.05) / (lo + 0.05)
}
