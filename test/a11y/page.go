package a11y

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Page is one attached tab.
type Page struct {
	b         *Browser
	sessionID string
	targetID  string
}

// NewPage opens a tab and enables the domains this suite uses.
func (b *Browser) NewPage() (*Page, error) {
	var created struct{ TargetID string }
	if err := b.call("", "Target.createTarget", map[string]any{"url": "about:blank"}, &created); err != nil {
		return nil, err
	}
	var attached struct{ SessionID string }
	if err := b.call("", "Target.attachToTarget",
		map[string]any{"targetId": created.TargetID, "flatten": true}, &attached); err != nil {
		return nil, err
	}
	p := &Page{b: b, sessionID: attached.SessionID, targetID: created.TargetID}
	for _, domain := range []string{"Page.enable", "Runtime.enable", "DOM.enable", "CSS.enable"} {
		if err := p.b.call(p.sessionID, domain, map[string]any{}, nil); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// Close discards the tab. Tabs are per-case so that emulation set by one case
// cannot leak into the next.
func (p *Page) Close() {
	_ = p.b.call("", "Target.closeTarget", map[string]any{"targetId": p.targetID}, nil)
}

// Viewport sets the layout size in CSS pixels.
func (p *Page) Viewport(width, height int) error {
	return p.b.call(p.sessionID, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": width, "height": height, "deviceScaleFactor": 1, "mobile": false,
		"screenWidth": width, "screenHeight": height,
	}, nil)
}

// Media emulates user preferences: prefers-color-scheme, prefers-reduced-motion,
// prefers-contrast, forced-colors.
//
// Emulating these rather than trusting the host matters — a CI machine's own
// theme would otherwise decide which branch of the stylesheet the suite tested.
func (p *Page) Media(features map[string]string) error {
	list := make([]map[string]string, 0, len(features))
	for name, value := range features {
		list = append(list, map[string]string{"name": name, "value": value})
	}
	return p.b.call(p.sessionID, "Emulation.setEmulatedMedia", map[string]any{
		"media": "screen", "features": list,
	}, nil)
}

// Navigate loads a URL and waits for the document to finish.
func (p *Page) Navigate(url string) error {
	if err := p.b.call(p.sessionID, "Page.navigate", map[string]any{"url": url}, nil); err != nil {
		return err
	}
	_, err := p.Eval(`new Promise(resolve => {
		if (document.readyState === "complete") resolve(true);
		else addEventListener("load", () => resolve(true));
	})`)
	return err
}

// Eval runs an expression and unmarshals its value.
func (p *Page) Eval(expression string) (json.RawMessage, error) {
	var out struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	err := p.b.call(p.sessionID, "Runtime.evaluate", map[string]any{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  true,
	}, &out)
	if err != nil {
		return nil, err
	}
	if out.ExceptionDetails != nil {
		msg := out.ExceptionDetails.Text
		if out.ExceptionDetails.Exception != nil && out.ExceptionDetails.Exception.Description != "" {
			msg = out.ExceptionDetails.Exception.Description
		}
		return nil, fmt.Errorf("evaluating script: %s", msg)
	}
	return out.Result.Value, nil
}

// EvalInto runs an expression and decodes its value into v.
func (p *Page) EvalInto(expression string, v any) error {
	raw, err := p.Eval(expression)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return fmt.Errorf("expression returned nothing: %s", firstLine(expression))
	}
	return json.Unmarshal(raw, v)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + "…"
	}
	return s
}

// Settle waits until no CSS animation is still running, so a measurement or a
// screenshot describes the resting state rather than an arbitrary frame of a
// fade. Without this, assertions about position and opacity are a coin toss.
func (p *Page) Settle() error {
	_, err := p.Eval(`(async () => {
		for (let i = 0; i < 40; i++) {
			const running = document.getAnimations().filter(a => a.playState === "running");
			if (!running.length) break;
			try {
				await Promise.race([
					Promise.all(running.map(a => a.finished)),
					new Promise(r => setTimeout(r, 700)),
				]);
			} catch (e) { /* a cancelled animation is settled */ }
		}
		await new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)));
		return true;
	})()`)
	if err != nil {
		return err
	}
	time.Sleep(80 * time.Millisecond)
	return nil
}

// Screenshot writes a PNG, for the report rather than for assertions.
func (p *Page) Screenshot(path string) error {
	var out struct{ Data string }
	if err := p.b.call(p.sessionID, "Page.captureScreenshot",
		map[string]any{"format": "png"}, &out); err != nil {
		return err
	}
	data, err := base64.StdEncoding.DecodeString(out.Data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// --- input ------------------------------------------------------------------

// Click dispatches a real pointer sequence at the centre of a selector, rather
// than calling el.click(). The difference matters: a synthetic click skips
// hit-testing, so an element covered by an overlay still "works" — which is
// exactly the bug a click test should catch.
func (p *Page) Click(selector string) error {
	var box struct {
		X, Y float64
		OK   bool
	}
	if err := p.EvalInto(fmt.Sprintf(`(() => {
		const el = document.querySelector(%s);
		if (!el) return {OK: false};
		el.scrollIntoView({block: "center", behavior: "instant"});
		const r = el.getBoundingClientRect();
		return {X: r.x + r.width / 2, Y: r.y + r.height / 2, OK: r.width > 0 && r.height > 0};
	})()`, jsString(selector)), &box); err != nil {
		return err
	}
	if !box.OK {
		return fmt.Errorf("click: %s is missing or has no size", selector)
	}
	for _, event := range []struct {
		typ     string
		clicks  int
		buttons int
	}{{"mouseMoved", 0, 0}, {"mousePressed", 1, 1}, {"mouseReleased", 1, 0}} {
		if err := p.b.call(p.sessionID, "Input.dispatchMouseEvent", map[string]any{
			"type": event.typ, "x": box.X, "y": box.Y,
			"button": "left", "clickCount": event.clicks, "buttons": event.buttons,
		}, nil); err != nil {
			return err
		}
	}
	return nil
}

var keyCodes = map[string]struct {
	Key  string
	Code string
	VK   int
	Text string
}{
	"Tab":        {"Tab", "Tab", 9, "\t"},
	"Enter":      {"Enter", "Enter", 13, "\r"},
	"Space":      {" ", "Space", 32, " "},
	"Escape":     {"Escape", "Escape", 27, ""},
	"ArrowRight": {"ArrowRight", "ArrowRight", 39, ""},
	"ArrowLeft":  {"ArrowLeft", "ArrowLeft", 37, ""},
	"ArrowDown":  {"ArrowDown", "ArrowDown", 40, ""},
	"ArrowUp":    {"ArrowUp", "ArrowUp", 38, ""},
	"Home":       {"Home", "Home", 36, ""},
	"End":        {"End", "End", 35, ""},
}

// Press sends one key.
func (p *Page) Press(name string) error {
	k, ok := keyCodes[name]
	if !ok {
		return fmt.Errorf("press: unknown key %q", name)
	}
	base := map[string]any{
		"key": k.Key, "code": k.Code,
		"windowsVirtualKeyCode": k.VK, "nativeVirtualKeyCode": k.VK,
	}
	send := func(typ string, extra map[string]any) error {
		params := map[string]any{"type": typ}
		for key, v := range base {
			params[key] = v
		}
		for key, v := range extra {
			params[key] = v
		}
		return p.b.call(p.sessionID, "Input.dispatchKeyEvent", params, nil)
	}
	if err := send("keyDown", map[string]any{"text": k.Text}); err != nil {
		return err
	}
	return send("keyUp", nil)
}

// Type enters text one character at a time, so input and change handlers and any
// debounce see what a person typing would produce.
func (p *Page) Type(text string) error {
	for _, r := range text {
		s := string(r)
		if err := p.b.call(p.sessionID, "Input.dispatchKeyEvent", map[string]any{
			"type": "keyDown", "text": s, "key": s, "unmodifiedText": s,
		}, nil); err != nil {
			return err
		}
		if err := p.b.call(p.sessionID, "Input.dispatchKeyEvent", map[string]any{
			"type": "keyUp", "key": s,
		}, nil); err != nil {
			return err
		}
	}
	return nil
}

// jsString quotes a Go string for embedding in JavaScript.
func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
