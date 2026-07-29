// Package layout composes a page out of registered widgets.
//
// The whole page is data. Which sections exist, in what order, with which
// widgets bound to which data — all of it comes from layout.yaml, so a
// deployment rearranges its dashboard by editing a text file rather than by
// editing Go.
//
// The engine's job is to make that safe. A layout is validated in full at
// startup, against the widget registry and against the deployment's own domain
// configuration, and every problem is reported with the file and line that
// caused it. The alternative — discovering a mistyped widget type as a blank
// panel in production — is exactly what this trades away.
package layout

import (
	"bytes"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/widget"
)

// FileLayout is the bundle name this package reads.
const FileLayout = "layout.yaml"

// Layout is a whole dashboard.
type Layout struct {
	Pages  []Page `yaml:"pages"`
	Drawer Drawer `yaml:"drawer"`
}

// Page is one addressable view.
type Page struct {
	ID   string `yaml:"id"`
	Path string `yaml:"path"`
	// TitleTermID is the document title — what a browser tab, a bookmark and a
	// search result show. Optional: without it the title falls back to the
	// wordmark, which is what it used to be unconditionally. They are different
	// strings for a reason. A wordmark is a name and wants to be short; a title
	// competes for attention in a list of twenty tabs and wants to say what the
	// page answers.
	TitleTermID string `yaml:"titleTermId"`
	// DescriptionTermID becomes the meta description, and falls back to the
	// brand tagline.
	DescriptionTermID string    `yaml:"descriptionTermId"`
	Sections          []Section `yaml:"sections"`
}

// Section is a band of the page, and the unit HTMX swaps.
//
// Sections are the swap unit rather than individual widgets because they are
// what the reader perceives as a thing that changed: narrowing a filter
// replaces the leaderboard, not eleven separate cells.
type Section struct {
	ID string `yaml:"id"`
	// Heading is optional; a section can be a bare band of widgets.
	Heading *Heading `yaml:"heading"`
	// Grid describes how the widgets are laid out within the section.
	Grid Grid `yaml:"grid"`
	// Swap names the fragment route that re-renders this section. Empty means
	// the section is static and never swaps on its own.
	Swap string `yaml:"swap"`
	// Aside is rendered in the heading row, opposite the title: the things that
	// qualify a section rather than belong to its body — how many rows are
	// showing, which ranking rule produced them, which set of cards is on view.
	// Putting them in the grid instead pushed them below the controls they
	// describe, and reading "Showing 34 of 34" underneath the filter that
	// produced the 34 states the answer before the question.
	//
	// A section with an aside and no heading has nowhere to put it, which
	// validation rejects rather than silently dropping.
	Aside   []Widget `yaml:"aside"`
	Widgets []Widget `yaml:"widgets"`
}

// Heading is a section's title block.
type Heading struct {
	EyebrowTermID string `yaml:"eyebrowTermId"`
	TitleTermID   string `yaml:"titleTermId"`
	// Scoped appends the current scope to the title term, so one heading can
	// read differently in the national and sub-national views.
	Scoped bool `yaml:"scoped"`
	// Variants replace the title when the reader has switched which findings
	// the band shows, keyed by that selection. It is the one reader choice that
	// changes what a band is *about* rather than what is in it — "what needs
	// attention" and "where the opportunities are" are two different questions,
	// and a heading that answered only the first would mislabel the second.
	Variants map[string]string `yaml:"variants"`
	Level    int               `yaml:"level"`
	Class    string            `yaml:"class"`
}

// Grid is the section's layout. It is expressed in the same terms CSS grid
// uses, because that is what it becomes.
type Grid struct {
	// MinColWidth is the narrowest a column may be before the grid reflows.
	// Empty means a single full-width column.
	MinColWidth string `yaml:"minColWidth"`
	Gap         string `yaml:"gap"`
	// Columns names tracks, for a section whose parts are not interchangeable:
	// the verdict's figures want most of the measure and the sentences
	// explaining them want a column beside it, which a grid of equal
	// auto-fitted tracks cannot express. A widget joins one by naming it.
	//
	// Empty keeps the auto-fitting behaviour, which is right wherever the parts
	// really are interchangeable — a row of signal cards, a row of stat tiles.
	Columns []Column `yaml:"columns"`
}

// Column is one named track of a section's layout.
type Column struct {
	Name string `yaml:"name"`
	// Basis is a CSS flex shorthand: "1 1 540px". The third value is the width
	// the column wants, and columns stack when they no longer fit side by side
	// — so a section declares the width its content needs rather than a
	// breakpoint, and there is no viewport number to keep in step with the
	// stylesheet.
	Basis string `yaml:"basis"`
	// Direction is "column" (the default) or "row". A row is for the few groups
	// whose parts belong on one line — a button and the two lines qualifying it
	// — which stacked would read as separate claims.
	Direction string `yaml:"direction"`
}

// Column directions.
const (
	DirectionColumn = "column"
	DirectionRow    = "row"
)

// Widget is one composed part.
type Widget struct {
	Type    string         `yaml:"type"`
	Bind    widget.Bind    `yaml:"bind"`
	Options widget.Options `yaml:"options"`
	// RepeatOver renders the widget once per item of a configured collection —
	// one stat tile per declared metric, say — so that adding a metric to
	// domain.yaml adds a tile without touching the layout.
	RepeatOver string `yaml:"repeatOver"`
	// Column names the grid track this widget belongs to. Empty means the
	// section's default flow; a name the grid does not declare is a startup
	// error rather than a widget that silently lands in the wrong place.
	Column string `yaml:"column"`
	// Span stretches the widget across every track of an auto-fitted grid, for
	// the parts that label or summarise the rest rather than sit beside them —
	// a heading over a row of tiles, a chart under it. Ignored by a grid with
	// named columns, where a widget says which track it is in instead.
	Span bool `yaml:"span"`
}

// Drawer is the per-service panel.
type Drawer struct {
	Tabs []Tab `yaml:"tabs"`
}

// Tab is one pane of the drawer.
type Tab struct {
	ID          string   `yaml:"id"`
	TitleTermID string   `yaml:"titleTermId"`
	Grid        Grid     `yaml:"grid"`
	Widgets     []Widget `yaml:"widgets"`
}

// RepeatSources are the collections a widget may be repeated over.
var RepeatSources = []string{
	"config.metrics.leaderboard",
	"config.metrics.drawer",
	"config.statuses",
	"config.categories",
	"config.periods",
	"config.regions",
}

// Parse decodes and validates a layout.
//
// Decoding is strict — an unknown key is an error — and validation continues
// past the first failure so that a reader fixing their layout sees everything
// at once rather than one problem per run.
func Parse(raw []byte, reg *widget.Registry, cfg config.Config) (Layout, error) {
	var l Layout

	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&l); err != nil {
		return Layout{}, config.Errors{*config.DecodeError(FileLayout, err)}
	}

	var node yaml.Node
	_ = yaml.Unmarshal(raw, &node)

	if errs := validate(l, &node, reg, cfg); len(errs) > 0 {
		return Layout{}, errs
	}
	return l, nil
}

// PageByPath finds the page serving a request path.
func (l Layout) PageByPath(path string) (Page, bool) {
	for _, p := range l.Pages {
		if p.Path == path {
			return p, true
		}
	}
	return Page{}, false
}

// SectionByID finds a section anywhere in the layout, which is how a fragment
// route re-renders exactly the band that changed.
func (l Layout) SectionByID(id string) (Section, bool) {
	for _, p := range l.Pages {
		for _, s := range p.Sections {
			if s.ID == id {
				return s, true
			}
		}
	}
	return Section{}, false
}

// TabByID finds a drawer tab.
func (l Layout) TabByID(id string) (Tab, bool) {
	for _, t := range l.Drawer.Tabs {
		if t.ID == id {
			return t, true
		}
	}
	return Tab{}, false
}

// DefaultTab is the drawer pane opened when none is named.
func (l Layout) DefaultTab() string {
	if len(l.Drawer.Tabs) == 0 {
		return ""
	}
	return l.Drawer.Tabs[0].ID
}

// SwapTargets lists the sections that can be re-rendered on their own, which is
// what the server registers fragment routes for.
func (l Layout) SwapTargets() []string {
	var out []string
	for _, p := range l.Pages {
		for _, s := range p.Sections {
			if s.Swap != "" {
				out = append(out, s.ID)
			}
		}
	}
	slices.Sort(out)
	return out
}
