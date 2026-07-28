// Package theme turns configured design tokens into a stylesheet.
//
// The demo this dashboard replaces built its styling as inline style attributes
// assembled in JavaScript, which meant every element carried its own copy of
// the palette and nothing could be cached. Here the tokens become one small
// generated stylesheet of CSS custom properties, served once with an ETag; the
// templates only ever reference var(--token).
//
// CSS is a pure, total function. Token values cannot break out of the rules
// they are written into because config rejects such values at parse time, which
// is why nothing here needs to escape or fail.
package theme

import (
	"slices"
	"strings"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
)

// FontBasePath is the URL prefix under which self-hosted font files are served.
// Faces naming a file are resolved against it; faces naming a url are used as
// given.
const FontBasePath = "/assets/fonts/"

// CSS renders the theme as a stylesheet.
//
// The output is deterministic — tokens are emitted in sorted order — so the
// stylesheet's ETag is stable across restarts and clients keep their cache.
func CSS(t config.Theme) string {
	var b strings.Builder

	b.WriteString("/* Generated from theme.yaml. Do not edit; change the config instead. */\n")

	writeFontFaces(&b, t.Fonts.Body)
	writeFontFaces(&b, t.Fonts.Serif)

	// Light is the default: it applies unless something more specific wins.
	//
	// color-scheme is declared alongside the tokens because the browser paints
	// several things this stylesheet cannot reach — the select dropdown, the
	// scrollbar, the caret, form control chrome. Without it those stay in light
	// colours over a dark page, which is how a themed dashboard ends up with an
	// unreadable language menu.
	b.WriteString(":root{color-scheme:light;")
	writeTokens(&b, t.Light)
	writeTokens(&b, t.Tokens)
	writeFontToken(&b, "--font-body", t.Fonts.Body.Stack)
	writeFontToken(&b, "--font-serif", t.Fonts.Serif.Stack)
	b.WriteString("}\n")

	if len(t.Dark) > 0 {
		// Follow the operating system, but stand aside when the reader has
		// explicitly chosen light — otherwise the toggle would be a no-op for
		// anyone whose OS is set to dark.
		b.WriteString(`@media (prefers-color-scheme:dark){:root:not([data-theme="light"]){color-scheme:dark;`)
		writeTokens(&b, t.Dark)
		b.WriteString("}}\n")

		// This selector and the one above have equal specificity, so an
		// explicit choice can only win by coming later. Do not reorder.
		b.WriteString(`:root[data-theme="dark"]{color-scheme:dark;`)
		writeTokens(&b, t.Dark)
		b.WriteString("}\n")
	}

	return b.String()
}

// writeTokens emits declarations in sorted key order.
func writeTokens(b *strings.Builder, tokens map[string]string) {
	if len(tokens) == 0 {
		return
	}
	names := make([]string, 0, len(tokens))
	for k := range tokens {
		names = append(names, k)
	}
	slices.Sort(names)

	for _, name := range names {
		b.WriteString(name)
		b.WriteString(":")
		b.WriteString(tokens[name])
		b.WriteString(";")
	}
}

// writeFontToken emits a stack token, skipping it when unset so that an empty
// theme does not produce `--font-body:;`.
func writeFontToken(b *strings.Builder, name, stack string) {
	if stack == "" {
		return
	}
	b.WriteString(name)
	b.WriteString(":")
	b.WriteString(stack)
	b.WriteString(";")
}

// writeFontFaces emits one @font-face rule per declared face.
func writeFontFaces(b *strings.Builder, fam config.FontFamily) {
	if fam.Family == "" {
		return
	}
	for _, f := range fam.Faces {
		src := f.URL
		if src == "" {
			src = FontBasePath + f.File
		}

		b.WriteString(`@font-face{font-family:"`)
		b.WriteString(fam.Family)
		b.WriteString(`";src:url("`)
		b.WriteString(src)
		b.WriteString(`") format("woff2")`)

		// Each optional descriptor is omitted rather than emitted empty: an
		// invalid descriptor makes some browsers discard the whole rule.
		writeDescriptor(b, "font-weight", f.Weight)
		writeDescriptor(b, "font-style", f.Style)
		writeDescriptor(b, "font-display", f.Display)
		writeDescriptor(b, "unicode-range", f.UnicodeRange)

		b.WriteString("}\n")
	}
}

func writeDescriptor(b *strings.Builder, name, value string) {
	if value == "" {
		return
	}
	b.WriteString(";")
	b.WriteString(name)
	b.WriteString(":")
	b.WriteString(value)
}
