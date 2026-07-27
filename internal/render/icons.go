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

// NewIcons compiles the configured set.
func NewIcons(c config.Icons) *Icons {
	out := &Icons{set: make(map[string]widget.Icon, len(c.Icons))}
	for key, i := range c.Icons {
		out.set[key] = widget.Icon{Glyph: i.Glyph, SVG: i.SVG, Label: i.Label}
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
