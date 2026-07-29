package render

import (
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/url"
	"strconv"
	"strings"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/layout"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/widget"
)

// Renderer holds the parsed template set.
//
// html/template is used rather than text/template throughout, so every value a
// widget produces is contextually escaped on the way out. The only exceptions
// are the two types deliberately marked safe — inline SVG from icons.yaml and
// generated CSS from theme.yaml — both of which come from the deployment's own
// config rather than from any upstream.
type Renderer struct {
	tmpl *template.Template
	reg  *widget.Registry
}

// New parses every template in the tree.
func New(files fs.FS, reg *widget.Registry) (*Renderer, error) {
	t := template.New("").Funcs(funcs())

	t, err := t.ParseFS(files,
		"web/templates/*.html",
		"web/templates/widgets/*.html",
	)
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}

	r := &Renderer{tmpl: t, reg: reg}
	if err := r.checkWidgetTemplates(); err != nil {
		return nil, err
	}
	return r, nil
}

// checkWidgetTemplates verifies that every registered widget has somewhere to
// render into.
//
// Caught at startup rather than at request time: a widget with no template
// would otherwise fail on whichever page happened to use it, quite possibly in
// production and quite possibly not the page anyone was testing.
func (r *Renderer) checkWidgetTemplates() error {
	var missing []string
	for _, name := range r.reg.Types() {
		d, _ := r.reg.Lookup(name)
		if r.tmpl.Lookup("widget/"+d.Template) == nil {
			missing = append(missing, fmt.Sprintf("%s (wants %q)", name, "widget/"+d.Template))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("widgets with no template: %s", strings.Join(missing, ", "))
	}
	return nil
}

// Page renders a whole document.
func (r *Renderer) Page(w io.Writer, data any) error {
	return r.tmpl.ExecuteTemplate(w, "page", data)
}

// Fragment renders one named template, which is what an HTMX swap receives.
func (r *Renderer) Fragment(w io.Writer, name string, data any) error {
	if r.tmpl.Lookup(name) == nil {
		return fmt.Errorf("no template named %q", name)
	}
	return r.tmpl.ExecuteTemplate(w, name, data)
}

// Widget renders one widget's view model into its own template.
func (r *Renderer) Widget(w io.Writer, kind string, view any) error {
	d, ok := r.reg.Lookup(kind)
	if !ok {
		return fmt.Errorf("unknown widget type %q", kind)
	}
	return r.tmpl.ExecuteTemplate(w, "widget/"+d.Template, view)
}

// Rendered is one widget after its builder has run, carrying enough for the
// section template to place it.
type Rendered struct {
	Type string
	// Column is the named grid track this widget joins, if the section declares
	// any. Empty means the default flow.
	Column string
	// Span stretches the widget across every track of an auto-fitted grid.
	Span bool
	HTML template.HTML
}

// Section is a band of the page, ready to render.
type Section struct {
	ID   string
	Swap string
	Grid layout.Grid
	// Aside renders in the heading row, opposite the title.
	Aside   []Rendered
	Heading *HeadingBlock
	Widgets []Rendered
	// Columns is set only when the section declares named tracks. Grouping
	// happens here rather than in the template because html/template cannot
	// partition a list, and because the grouping is the same question the
	// validator already answered.
	Columns []RenderedColumn
}

// RenderedColumn is one named track with the widgets that joined it.
type RenderedColumn struct {
	Name  string
	Basis string
	// Row lays the column's widgets along a line rather than down one.
	Row     bool
	Widgets []Rendered
}

// HeadingBlock is a section's resolved title.
type HeadingBlock struct {
	Eyebrow string
	Title   string
	Level   int
	Class   string
	ID      string
}

// funcs is the complete set of functions a template may call.
//
// Deliberately small. A template that can compute is a template that can hold
// domain logic, and the whole point of the widget layer is that decisions
// happen in Go where they can be tested.
func funcs() template.FuncMap {
	return template.FuncMap{
		// Presentation-only helpers.
		"pct":   func(v float64) template.CSS { return template.CSS(strconv.FormatFloat(v, 'f', 4, 64) + "%") },
		"css":   func(s string) template.CSS { return template.CSS(s) },
		"svg":   SafeSVG,
		"attr":  func(s string) template.HTMLAttr { return template.HTMLAttr(s) },
		"num":   func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) },
		"join":  strings.Join,
		"query": buildQuery,

		// Sequence helpers, for the few places a template genuinely iterates.
		"add": func(a, b int) int { return a + b },
		"seq": func(n int) []int {
			out := make([]int, n)
			for i := range out {
				out[i] = i
			}
			return out
		},
	}
}

// buildQuery assembles a URL query from alternating key/value pairs, dropping
// empties so a link carries only what the reader actually chose.
//
// Keeping default values out of the URL is what makes a shared link readable:
// "?scope=state&status=major" says what it means, where a link carrying every
// default says nothing at all.
func buildQuery(pairs ...string) template.URL {
	if len(pairs)%2 != 0 {
		return ""
	}
	v := url.Values{}
	for i := 0; i < len(pairs); i += 2 {
		key, val := pairs[i], pairs[i+1]
		if key == "" || val == "" {
			continue
		}
		v.Set(key, val)
	}
	if len(v) == 0 {
		return ""
	}
	return template.URL("?" + v.Encode())
}
