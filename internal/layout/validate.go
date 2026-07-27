package layout

import (
	"fmt"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/widget"
)

// validator walks a layout accumulating located problems.
type validator struct {
	node *yaml.Node
	reg  *widget.Registry
	cfg  config.Config
	errs config.Errors
}

func validate(l Layout, node *yaml.Node, reg *widget.Registry, cfg config.Config) config.Errors {
	v := &validator{node: node, reg: reg, cfg: cfg}

	if len(l.Pages) == 0 {
		v.at([]any{"pages"}, "no pages declared; the dashboard would have nothing to serve")
	}

	seenPage := map[string]bool{}
	seenPath := map[string]bool{}
	seenSection := map[string]bool{}

	for i, p := range l.Pages {
		path := []any{"pages", i}

		if p.ID == "" {
			v.at(append(path, "id"), "page has no id")
		} else if seenPage[p.ID] {
			v.at(append(path, "id"), "duplicate page id %q", p.ID)
		}
		seenPage[p.ID] = true

		switch {
		case p.Path == "":
			v.at(append(path, "path"), "page %q has no path", p.ID)
		case p.Path[0] != '/':
			v.at(append(path, "path"), "path %q does not begin with a slash", p.Path)
		case seenPath[p.Path]:
			v.at(append(path, "path"), "duplicate path %q; two pages cannot serve the same URL", p.Path)
		}
		seenPath[p.Path] = true

		if len(p.Sections) == 0 {
			v.at(append(path, "sections"), "page %q has no sections", p.ID)
		}

		for j, s := range p.Sections {
			sp := append(slices.Clone(path), "sections", j)

			if s.ID == "" {
				v.at(append(slices.Clone(sp), "id"), "section has no id; a section needs one to be swapped or linked to")
			} else if seenSection[s.ID] {
				// Ids become DOM ids and fragment routes, so a duplicate would
				// make HTMX swap the wrong band.
				v.at(append(slices.Clone(sp), "id"), "duplicate section id %q", s.ID)
			}
			seenSection[s.ID] = true

			v.heading(sp, s.Heading)

			if len(s.Widgets) == 0 {
				v.at(append(slices.Clone(sp), "widgets"), "section %q has no widgets", s.ID)
			}
			for k, w := range s.Widgets {
				v.widget(append(slices.Clone(sp), "widgets", k), w, false)
			}
		}
	}

	seenTab := map[string]bool{}
	for i, t := range l.Drawer.Tabs {
		tp := []any{"drawer", "tabs", i}

		if t.ID == "" {
			v.at(append(slices.Clone(tp), "id"), "drawer tab has no id")
		} else if seenTab[t.ID] {
			v.at(append(slices.Clone(tp), "id"), "duplicate drawer tab id %q", t.ID)
		}
		seenTab[t.ID] = true

		if t.TitleTermID == "" {
			v.at(append(slices.Clone(tp), "titleTermId"), "drawer tab %q has no title, so its button would be blank", t.ID)
		}
		if len(t.Widgets) == 0 {
			v.at(append(slices.Clone(tp), "widgets"), "drawer tab %q has no widgets", t.ID)
		}
		for k, w := range t.Widgets {
			v.widget(append(slices.Clone(tp), "widgets", k), w, true)
		}
	}

	return v.errs
}

func (v *validator) heading(path []any, h *Heading) {
	if h == nil {
		return
	}
	if h.TitleTermID == "" {
		v.at(append(slices.Clone(path), "heading", "titleTermId"), "heading has no title term")
	}
	if h.Level != 0 && (h.Level < 1 || h.Level > 6) {
		v.at(append(slices.Clone(path), "heading", "level"),
			"heading level %d is not an HTML heading level; expected 1 to 6", h.Level)
	}
}

func (v *validator) widget(path []any, w Widget, inDrawer bool) {
	if w.Type == "" {
		v.at(append(slices.Clone(path), "type"), "widget has no type; this build provides %v", v.reg.Types())
		return
	}

	vc := widget.ValidationContext{
		Domain:   v.cfg.Domain,
		Icons:    v.cfg.Icons,
		InDrawer: inDrawer,
	}
	for _, err := range v.reg.ValidateWidget(w.Type, w.Options, w.Bind, vc) {
		v.at(path, "%s", err)
	}

	if w.RepeatOver != "" && !slices.Contains(RepeatSources, w.RepeatOver) {
		v.at(append(slices.Clone(path), "repeatOver"),
			"unknown repeat source %q; expected one of %v", w.RepeatOver, RepeatSources)
	}
}

// at records a problem, located at the deepest node the path resolves to.
func (v *validator) at(path []any, format string, args ...any) {
	e := config.Error{
		File: FileLayout,
		Path: config.PathString(path),
		Msg:  fmt.Sprintf(format, args...),
	}
	if n := config.NodeAt(v.node, path...); n != nil {
		e.Line, e.Column = n.Line, n.Column
	}
	v.errs = append(v.errs, e)
}
