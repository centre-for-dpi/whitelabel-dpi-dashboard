//go:build a11y

// The browser layer of the accessibility suite.
//
// Behind a build tag because it needs a Chrome and a running server, so it must
// not slow down `go test ./...`. Run it with `make a11y-browser`.
//
// Two kinds of assertion live here, and only these two. Everything decidable
// from HTML alone belongs in the pure-Go layer, which runs on every commit:
//
//  1. axe-core over a matrix of the states a reader can actually be in. A page
//     is not one artefact — it is a locale times a theme times a viewport times
//     whatever the reader has filtered — and a violation can exist in one cell
//     and not another. Arabic is where an unresolved term empties an accessible
//     name; 320px is where the table becomes cards; forced-colors is where
//     everything said with a tone stops being said.
//
//  2. Behaviour axe cannot see. Whether focus is contained in a modal, whether
//     Escape returns it to where it came from, whether a live region survives a
//     swap, whether a scripted scroll honours prefers-reduced-motion, whether
//     adjacent bar segments remain distinguishable when colour is stripped.
//     These are the failures that read as correct in markup and are broken in
//     use, which is exactly why they need a browser to catch.
package a11y

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	baseURL = flag.String("base", envOr("A11Y_BASE", "http://localhost:8080"),
		"the running dashboard to audit")
	shotDir = flag.String("shots", envOr("A11Y_SHOTS", ""),
		"write a screenshot per matrix cell to this directory")

	// requireChrome turns a missing browser from a skip into a failure.
	//
	// Skipping is right on a developer's machine: not everyone has a Chrome, and
	// the pure-Go layers already gate the commit. It is wrong in CI, where a
	// silent skip is a green tick reporting that accessibility was verified when
	// nothing ran at all — which is worse than having no gate, because it is
	// indistinguishable from a pass. Defaults on from the CI variable every
	// provider sets.
	requireChrome = flag.Bool("require-chrome", envOr("CI", "") != "",
		"fail rather than skip when no browser is available")
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// browser is shared: starting Chrome costs about a second, and every case opens
// its own tab so emulation cannot leak between them.
var browser *Browser

func TestMain(m *testing.M) {
	flag.Parse()

	if _, err := FindChrome(); err != nil {
		if *requireChrome {
			fmt.Fprintf(os.Stderr, "the browser suite cannot run: %v\n"+
				"Refusing to skip, because -require-chrome is set (it defaults on when CI is set):\n"+
				"a skipped accessibility suite reports success without checking anything.\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "skipping the browser suite: %v\n", err)
		os.Exit(0)
	}
	b, err := Launch()
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not start Chrome: %v\n", err)
		os.Exit(1)
	}
	browser = b
	code := m.Run()
	b.Close()
	os.Exit(code)
}

// cell is one combination of reader circumstances.
type cell struct {
	name   string
	path   string
	width  int
	height int
	media  map[string]string
}

// The light default is explicit in every cell. Without it the host machine's own
// colour scheme decides which half of the stylesheet gets tested, and a CI box
// set to dark would silently never exercise the light palette.
func light() map[string]string {
	return map[string]string{"prefers-color-scheme": "light"}
}

func with(base map[string]string, extra ...string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for i := 0; i+1 < len(extra); i += 2 {
		out[extra[i]] = extra[i+1]
	}
	return out
}

func matrix() []cell {
	var cells []cell

	// Locales: English, an RTL script, and the two that stress glyph width.
	for _, locale := range []string{"en", "ar", "hi", "zh"} {
		cells = append(cells, cell{
			name: "locale=" + locale, path: "/?lang=" + locale,
			width: 1440, height: 900, media: light(),
		})
	}

	// Themes, including the explicit choice overriding the system preference —
	// the case where a mis-ordered stylesheet makes the toggle a no-op.
	cells = append(cells,
		cell{"theme=dark", "/?theme=dark", 1440, 900, light()},
		cell{"theme=system-dark", "/", 1440, 900, map[string]string{"prefers-color-scheme": "dark"}},
		cell{"theme=light-over-system-dark", "/?theme=light", 1440, 900,
			map[string]string{"prefers-color-scheme": "dark"}},
	)

	// Viewports. 320 is the reflow floor; 640x480 is 1280x960 at 200% zoom.
	for _, v := range []struct {
		name string
		w, h int
	}{{"1440", 1440, 900}, {"760", 760, 900}, {"390", 390, 844}, {"320", 320, 640}, {"zoom200", 640, 480}} {
		cells = append(cells, cell{"viewport=" + v.name, "/", v.w, v.h, light()})
	}

	// Reader preferences.
	cells = append(cells,
		cell{"contrast=more", "/", 1440, 900, with(light(), "prefers-contrast", "more")},
		cell{"forced-colors", "/", 1440, 900, with(light(), "forced-colors", "active")},
		cell{"reduced-motion", "/", 1440, 900, with(light(), "prefers-reduced-motion", "reduce")},
	)

	// States a reader reaches by using the thing.
	for _, s := range []struct{ name, path string }{
		{"state=filtered", "/?q=pension"},
		{"state=empty", "/?q=zzzznothingmatches"},
		{"state=status-filter", "/?status=major,partial"},
		{"state=sorted", "/?sort=name&dir=desc"},
		{"state=subnational", "/?scope=state"},
		{"state=opportunities", "/?signals=opportunity"},
		{"drawer=overview", "/service/pan"},
		{"drawer=opportunity", "/service/pan?tab=opportunity"},
		{"drawer=errors", "/service/pan?tab=errors"},
		{"drawer=traffic", "/service/pan?tab=traffic"},
		{"drawer=incidents", "/service/pan?tab=incidents"},
		{"drawer=rtl", "/service/pan?lang=ar"},
	} {
		cells = append(cells, cell{s.name, s.path, 1440, 900, light()})
	}
	return cells
}

func (c cell) open(t *testing.T) *Page {
	t.Helper()
	p, err := browser.NewPage()
	if err != nil {
		t.Fatalf("opening a tab: %v", err)
	}
	t.Cleanup(p.Close)
	if err := p.Viewport(c.width, c.height); err != nil {
		t.Fatalf("setting the viewport: %v", err)
	}
	if err := p.Media(c.media); err != nil {
		t.Fatalf("emulating media: %v", err)
	}
	if err := p.Navigate(*baseURL + c.path); err != nil {
		t.Fatalf("loading %s: %v", c.path, err)
	}
	if err := p.Settle(); err != nil {
		t.Fatalf("waiting for animations: %v", err)
	}
	return p
}

func TestAxeFindsNothingAcrossTheMatrix(t *testing.T) {
	for _, c := range matrix() {
		t.Run(c.name, func(t *testing.T) {
			p := c.open(t)

			result, err := p.Axe()
			if err != nil {
				t.Fatalf("running axe: %v", err)
			}
			if *shotDir != "" {
				name := strings.NewReplacer("/", "_", "=", "-", "?", "_", "&", "_").Replace(c.name)
				if err := p.Screenshot(filepath.Join(*shotDir, name+".png")); err != nil {
					t.Logf("screenshot: %v", err)
				}
			}
			if len(result.Violations) > 0 {
				t.Errorf("axe-core %s found %d violation(s) at %s:%s",
					result.Engine, len(result.Violations), c.path, result.Report())
			}
			if report := result.IncompleteReport(); report != "" {
				t.Logf("axe could not decide the following (not failures):\n%s", report)
			}
		})
	}
}

// --- what axe cannot see ----------------------------------------------------

// A modal must contain focus. The prototype's hand-rolled trap enumerated
// focusable elements with a selector list that omitted <summary>, so any panel
// with a disclosure in it leaked focus to the document — while still claiming
// aria-modal="true". Only a real tab cycle finds that.
func TestFocusStaysInsideTheDrawer(t *testing.T) {
	// The errors tab is the one whose panel contains a <details><summary>.
	c := cell{"drawer", "/service/pan?tab=errors", 1440, 900, light()}
	p := c.open(t)

	var hasSummary bool
	if err := p.EvalInto(`!!document.querySelector("dialog summary")`, &hasSummary); err != nil {
		t.Fatal(err)
	}
	if !hasSummary {
		t.Fatal("this case is meant to cover a panel containing a <summary>; there is none, so it proves nothing")
	}

	var modal bool
	if err := p.EvalInto(`(() => {
		const d = document.getElementById("drawer");
		return !!d && d.matches(":modal");
	})()`, &modal); err != nil {
		t.Fatal(err)
	}
	if !modal {
		t.Fatal("the drawer is not a modal dialog, so the browser is providing no focus containment at all")
	}

	var escaped []string
	for i := 0; i < 24; i++ {
		if err := p.Press("Tab"); err != nil {
			t.Fatal(err)
		}
		var stop struct {
			Tag      string
			Text     string
			InDialog bool
		}
		if err := p.EvalInto(`(() => {
			const a = document.activeElement;
			return {
				Tag: a.tagName,
				Text: (a.getAttribute("aria-label") || a.textContent || "").trim().replace(/\s+/g, " ").slice(0, 40),
				InDialog: !!a.closest("dialog")
			};
		})()`, &stop); err != nil {
			t.Fatal(err)
		}
		// A stop on <body> is the wrap point: in a real browser focus goes to
		// the browser's own UI there, and headless has none. Containment is
		// about focusable elements of the page escaping, which is what counts.
		if !stop.InDialog && stop.Tag != "BODY" {
			escaped = append(escaped, fmt.Sprintf("%s %q", stop.Tag, stop.Text))
		}
	}
	if len(escaped) > 0 {
		t.Errorf("focus left the dialog onto %d focusable element(s): %s",
			len(escaped), strings.Join(escaped, ", "))
	}
}

// Escape must close the drawer and put focus back where the reader left it.
// Dumping focus to the top of the document is how a keyboard reader loses their
// place in a list of 178 services.
func TestEscapeClosesTheDrawerAndRestoresFocus(t *testing.T) {
	c := cell{"home", "/", 1440, 900, light()}
	p := c.open(t)

	if err := p.Click(".lb-name"); err != nil {
		t.Fatal(err)
	}
	if err := p.Settle(); err != nil {
		t.Fatal(err)
	}

	var opened bool
	if err := p.EvalInto(`(() => {
		const d = document.getElementById("drawer");
		window.__opener = document.querySelector(".lb-name");
		return !!d && d.matches(":modal");
	})()`, &opened); err != nil {
		t.Fatal(err)
	}
	if !opened {
		t.Fatal("clicking a service name did not open the drawer as a modal")
	}

	if err := p.Press("Escape"); err != nil {
		t.Fatal(err)
	}
	if err := p.Settle(); err != nil {
		t.Fatal(err)
	}

	var after struct {
		Closed        bool
		HostEmpty     bool
		FocusOnOpener bool
		FocusNow      string
		URL           string
	}
	if err := p.EvalInto(`(() => {
		const a = document.activeElement;
		return {
			Closed: !document.getElementById("drawer"),
			HostEmpty: document.getElementById("drawer-host").innerHTML.trim() === "",
			FocusOnOpener: a === window.__opener || (a.classList && a.classList.contains("lb-name")),
			FocusNow: a.tagName + " " + (a.textContent || "").trim().replace(/\s+/g, " ").slice(0, 40),
			URL: location.pathname + location.search
		};
	})()`, &after); err != nil {
		t.Fatal(err)
	}

	if !after.Closed || !after.HostEmpty {
		t.Error("Escape did not remove the drawer")
	}
	if !after.FocusOnOpener {
		t.Errorf("focus did not return to the service link; it is on %s", after.FocusNow)
	}
	// A URL naming a panel that is no longer open is a URL that lies.
	if strings.Contains(after.URL, "tab=") || strings.Contains(after.URL, "/service/") {
		t.Errorf("closing the drawer left %q in the address bar", after.URL)
	}
}

// A live region only announces a change to a node the screen reader was already
// watching, so it has to survive the swap that changes it. This is the failure
// that looks perfectly correct in markup and is silent in use.
func TestTheResultCountIsAnnouncedAfterFiltering(t *testing.T) {
	c := cell{"home", "/", 1440, 900, light()}
	p := c.open(t)

	var before struct {
		Present bool
		Outside bool
		Role    string
	}
	if err := p.EvalInto(`(() => {
		const r = document.getElementById("a11y-status");
		window.__region = r;
		return {
			Present: !!r,
			Outside: !!r && !r.closest("#leaderboard, #drawer-host, main"),
			Role: r ? (r.getAttribute("role") || "") : ""
		};
	})()`, &before); err != nil {
		t.Fatal(err)
	}
	if !before.Present {
		t.Fatal("there is no #a11y-status live region")
	}
	if !before.Outside {
		t.Error("the live region is inside a swap target, so a swap replaces it rather than updating it")
	}

	if err := p.Click(".search"); err != nil {
		t.Fatal(err)
	}
	if err := p.Type("pension"); err != nil {
		t.Fatal(err)
	}
	// The filter is debounced, so give the swap time to land.
	if err := p.Settle(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Eval(`new Promise(r => setTimeout(r, 1200))`); err != nil {
		t.Fatal(err)
	}

	var after struct {
		SameNode bool
		Text     string
		Rows     int
		Focused  string
		Caret    int
	}
	if err := p.EvalInto(`(() => {
		const r = document.getElementById("a11y-status");
		const a = document.activeElement;
		return {
			SameNode: r === window.__region,
			Text: (r ? r.textContent : "").trim(),
			Rows: document.querySelectorAll(".lb tbody tr").length,
			Focused: a.className || a.tagName,
			Caret: a.selectionStart === undefined || a.selectionStart === null ? -1 : a.selectionStart
		};
	})()`, &after); err != nil {
		t.Fatal(err)
	}

	if !after.SameNode {
		t.Error("the live region was replaced by the swap, so nothing is announced")
	}
	if after.Text == "" {
		t.Error("the live region is empty after filtering, so the new result count is never announced")
	}
	// Losing the caret mid-word makes a search box unusable for anyone typing
	// more slowly than the debounce.
	if !strings.Contains(after.Focused, "search") {
		t.Errorf("the search box lost focus during filtering; focus is on %q", after.Focused)
	}
	if after.Caret != len("pension") {
		t.Errorf("the caret moved to %d of %d during filtering", after.Caret, len("pension"))
	}
}

// The stylesheet's prefers-reduced-motion rule cannot reach a scripted scroll:
// behavior:"smooth" is an argument, not a declaration. So the script has to
// consult the query itself, and this is the only way to prove it does.
func TestScriptedScrollingHonoursReducedMotion(t *testing.T) {
	c := cell{"reduced", "/", 1440, 900, with(light(), "prefers-reduced-motion", "reduce")}
	p := c.open(t)

	if _, err := p.Eval(`(() => {
		window.__behaviours = [];
		const original = Element.prototype.scrollIntoView;
		Element.prototype.scrollIntoView = function (opts) {
			window.__behaviours.push(opts && opts.behavior || "legacy");
			return original.apply(this, arguments);
		};
		return true;
	})()`); err != nil {
		t.Fatal(err)
	}

	var present bool
	if err := p.EvalInto(`!!document.querySelector("[data-dpi-scroll-to]")`, &present); err != nil {
		t.Fatal(err)
	}
	if !present {
		t.Skip("no scroll-to control on this page")
	}
	if err := p.Click("[data-dpi-scroll-to]"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Eval(`new Promise(r => setTimeout(r, 500))`); err != nil {
		t.Fatal(err)
	}

	var behaviours []string
	if err := p.EvalInto(`window.__behaviours`, &behaviours); err != nil {
		t.Fatal(err)
	}
	for _, b := range behaviours {
		if b == "smooth" {
			t.Errorf("a scripted scroll asked for smooth behaviour under prefers-reduced-motion: reduce (calls: %v)", behaviours)
		}
	}
}

// Under forced colours every background composites to Canvas, so anything the
// design says with a tone stops being said. The segmented bar's proportions are
// its content, and adjacent fills are pastels that cannot contrast with each
// other, so without a divider it becomes one undivided block.
func TestStatusSurvivesForcedColours(t *testing.T) {
	c := cell{"forced", "/", 1440, 900, with(light(), "forced-colors", "active")}
	p := c.open(t)

	var bar struct {
		Active     bool
		Segments   int
		Fills      []string
		Dividers   []float64
		AllTheSame bool
		HasText    bool
	}
	if err := p.EvalInto(`(() => {
		const segs = [...document.querySelectorAll(".bar-seg")];
		const fills = segs.map(s => getComputedStyle(s).backgroundColor);
		return {
			Active: matchMedia("(forced-colors: active)").matches,
			Segments: segs.length,
			Fills: [...new Set(fills)],
			Dividers: segs.slice(1).map(s => parseFloat(getComputedStyle(s).borderInlineStartWidth) || 0),
			AllTheSame: new Set(fills).size <= 1,
			HasText: segs.every(s => s.textContent.trim().length > 0)
		};
	})()`, &bar); err != nil {
		t.Fatal(err)
	}

	if !bar.Active {
		t.Fatal("forced-colors emulation did not take effect, so this proves nothing")
	}
	if bar.Segments == 0 {
		t.Skip("no segmented bar on this page")
	}
	// The fills collapsing is expected and is the reader's own setting winning.
	// What must not happen is the bar losing its structure with them.
	if bar.AllTheSame {
		for i, w := range bar.Dividers {
			if w <= 0 {
				t.Errorf("segment %d has no divider, and all fills composite to the same colour, so adjacent segments are indistinguishable", i+2)
			}
		}
	}
	if !bar.HasText {
		t.Error("a bar segment carries no text, so with colour stripped it says nothing at all")
	}
}

// 1.4.10 Reflow: no horizontal scrolling at 320 CSS pixels.
func TestNoHorizontalScrollingAtTheReflowFloor(t *testing.T) {
	for _, width := range []int{320, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			c := cell{"reflow", "/", width, 640, light()}
			p := c.open(t)

			var probe struct {
				ScrollWidth int
				Viewport    int
				Offenders   []string
			}
			if err := p.EvalInto(`(() => {
				const vw = innerWidth;
				const offenders = [...document.querySelectorAll("*")].filter(el => {
					const r = el.getBoundingClientRect();
					if (!r.width) return false;
					if (getComputedStyle(el).position === "fixed") return false;
					// The skip link is parked off-screen by design.
					if (el.classList.contains("skip")) return false;
					return r.right > vw + 1;
				}).slice(0, 6).map(el =>
					el.tagName.toLowerCase() + "." + String(el.className).split(" ")[0] +
					" right=" + Math.round(el.getBoundingClientRect().right));
				return {ScrollWidth: document.documentElement.scrollWidth, Viewport: vw, Offenders: offenders};
			})()`, &probe); err != nil {
				t.Fatal(err)
			}
			if probe.ScrollWidth > probe.Viewport+1 {
				t.Errorf("the document scrolls horizontally at %dpx (%d > %d): %v",
					width, probe.ScrollWidth, probe.Viewport, probe.Offenders)
			}
		})
	}
}

// 1.4.12 Text Spacing: the page must survive the reader's own spacing overrides
// without clipping. Fixed heights are what fail this, and they are invisible
// until someone applies the overrides.
func TestTextSpacingOverridesDoNotClipContent(t *testing.T) {
	c := cell{"home", "/", 1440, 900, light()}
	p := c.open(t)

	var result struct {
		Clipped    []string
		Horizontal bool
	}
	if err := p.EvalInto(`(async () => {
		const style = document.createElement("style");
		style.textContent = "*{line-height:1.5 !important;letter-spacing:0.12em !important;" +
			"word-spacing:0.16em !important} p{margin-bottom:2em !important}";
		document.head.appendChild(style);
		await new Promise(r => setTimeout(r, 400));
		const clipped = [...document.querySelectorAll("*")].filter(el => {
			const cs = getComputedStyle(el);
			if (!el.children.length) return false;
			if (cs.overflow !== "hidden" && cs.overflowY !== "hidden") return false;
			return el.scrollHeight > el.clientHeight + 2;
		}).slice(0, 8).map(el => el.tagName.toLowerCase() + "." + String(el.className).split(" ")[0] +
			" " + el.scrollHeight + ">" + el.clientHeight);
		return {Clipped: clipped, Horizontal: document.documentElement.scrollWidth > innerWidth + 1};
	})()`, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Clipped) > 0 {
		t.Errorf("content is clipped under the required text-spacing overrides: %v", result.Clipped)
	}
	if result.Horizontal {
		t.Error("the text-spacing overrides introduce horizontal scrolling")
	}
}

// 2.5.8 Target Size (Minimum), new in WCAG 2.2. axe checks this, but only for
// the controls it recognises as targets, so measuring every focusable element
// directly is a stronger claim.
func TestEveryTargetIsLargeEnough(t *testing.T) {
	for _, c := range []cell{
		{"desktop", "/", 1440, 900, light()},
		{"mobile", "/", 390, 844, light()},
		{"drawer", "/service/pan?tab=errors", 1440, 900, light()},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := c.open(t)

			var small []string
			if err := p.EvalInto(`(() => {
				const targets = [...document.querySelectorAll(
					'a[href], button, input:not([type=hidden]), select, summary, [tabindex="0"]')]
					.filter(el => el.offsetParent !== null || el.getClientRects().length);
				return targets.filter(el => {
					const r = el.getBoundingClientRect();
					if (!r.width || !r.height) return false;
					if (el.classList.contains("skip")) return false;   // off-screen until focused
					// An inline link in a sentence is exempt from 2.5.8.
					const cs = getComputedStyle(el);
					if (el.tagName === "A" && cs.display === "inline") return false;
					return r.width < 24 || r.height < 24;
				}).map(el => {
					const r = el.getBoundingClientRect();
					return el.tagName.toLowerCase() + ' "' +
						(el.getAttribute("aria-label") || el.textContent || "").trim().replace(/\s+/g, " ").slice(0, 30) +
						'" ' + r.width.toFixed(1) + "x" + r.height.toFixed(1);
				});
			})()`, &small); err != nil {
				t.Fatal(err)
			}
			if len(small) > 0 {
				t.Errorf("%d target(s) smaller than 24x24 CSS px: %s", len(small), strings.Join(small, "; "))
			}
		})
	}
}

// 2.4.7 Focus Visible, measured rather than assumed: every focusable element
// must show an outline the reader can actually see.
func TestEveryFocusableShowsAVisibleRing(t *testing.T) {
	c := cell{"home", "/", 1440, 900, light()}
	p := c.open(t)

	var missing []string
	if err := p.EvalInto(`(() => {
		const focusables = [...document.querySelectorAll(
			'a[href], button, input:not([type=hidden]), select, summary, [tabindex="0"]')]
			.filter(el => el.offsetParent !== null);
		const bad = [];
		for (const el of focusables) {
			// A disabled control cannot take focus, so focus() is a no-op and
			// what we would measure is its resting style. Whether a disabled
			// control should exist at all is a different question, asked
			// elsewhere.
			if (el.disabled) continue;
			el.focus();
			const cs = getComputedStyle(el);
			const width = parseFloat(cs.outlineWidth) || 0;
			const solid = cs.outlineStyle !== "none" && cs.outlineStyle !== "hidden";
			// A ring can also come from a box-shadow or a border change; accept
			// any of them, but require that something changed.
			const shadowed = cs.boxShadow && cs.boxShadow !== "none";
			if (!((width > 0 && solid) || shadowed)) {
				bad.push(el.tagName.toLowerCase() + ' "' +
					(el.getAttribute("aria-label") || el.textContent || "").trim().replace(/\s+/g, " ").slice(0, 30) + '"');
			}
		}
		return bad.slice(0, 10);
	})()`, &missing); err != nil {
		t.Fatal(err)
	}
	if len(missing) > 0 {
		t.Errorf("%d focusable element(s) show no focus indicator: %s", len(missing), strings.Join(missing, "; "))
	}
}
