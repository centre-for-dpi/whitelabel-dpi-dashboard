// Package render turns view models into HTML.
//
// It owns the template set, the icon resolver and the small set of functions
// templates are allowed to call. Everything it renders comes from a widget
// builder, so this package contains no domain logic — only the mechanics of
// getting already-decided values onto a page safely.
package render

import (
	"html/template"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/widget"
)

// Icons resolves semantic icon keys.
//
// Templates ask for a role — "status.major" — never a character. That is what
// lets a deployment swap the entire icon set from icons.yaml without a single
// template edit, and it is why no template in this repository contains a glyph.
type Icons struct {
	set map[string]widget.Icon
}

// NewIcons compiles the configured set, resolving each accessible name.
//
// The name is resolved once here rather than per render because it is the same
// string on every request for a given locale — but note that this makes an Icons
// per-locale, which is why the server builds one per request rather than holding
// a single set. An icon's name is announced to a reader, so it has to be in
// their language; a set built once at startup could only ever be in one.
func NewIcons(c config.Icons, text widget.TextResolver) *Icons {
	out := &Icons{set: make(map[string]widget.Icon, len(c.Icons))}
	for key, i := range c.Icons {
		label := i.Label // the deprecated literal, still honoured
		if i.LabelTermID != "" {
			label = text.Text(i.LabelTermID, nil)
		}
		out.set[key] = widget.Icon{Glyph: i.Glyph, SVG: i.SVG, Label: label}
	}
	return out
}

// Icon returns the icon for a role. An unknown key yields an empty icon rather
// than a placeholder: config validation rejects unknown keys at startup, so
// reaching here means a hot reload raced, and a missing glyph is a far better
// failure than a box of question marks across the page.
func (i *Icons) Icon(key string) widget.Icon { return i.set[key] }

// SafeSVG marks inline SVG markup as trusted for the template.
//
// It is trusted because it comes from the deployment's own icons.yaml, which is
// the same trust level as the templates themselves — a deployment that can edit
// its config can already edit its config. It is emphatically NOT for anything
// arriving over the wire.
func SafeSVG(markup string) template.HTML { return template.HTML(markup) }
