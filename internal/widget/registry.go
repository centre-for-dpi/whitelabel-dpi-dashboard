package widget

import (
	"fmt"
	"slices"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
)

// Builder turns bound data into a view model, ready for its template.
//
// Builders are pure functions of their Context and Options. That is what makes
// every widget testable with a table of fixtures and no server, and it is why
// the Context carries plain data rather than handles to things that can fail.
type Builder func(Context, Options) (any, error)

// Definition is everything the engine knows about one kind of widget.
type Definition struct {
	// Type is what layout.yaml names.
	Type string
	// Template is the name of the html/template that renders the view model.
	Template string
	// Build produces the view model.
	Build Builder
	// Schema declares the options this widget accepts.
	Schema OptionSchema
	// Sources lists the bind sources this widget can read. An empty list means
	// the widget reads no data of its own.
	Sources []string
	// SourceOptional says the widget can also render from its options alone, so
	// a missing bind is not by itself an error. The widget's own Validate is
	// then responsible for saying which combinations make sense — the engine
	// cannot know that a disclosure with a prose body needs no data.
	SourceOptional bool
	// Validate is an optional extra check for constraints the schema cannot
	// express — a column list naming metrics the deployment declared, say.
	Validate func(Options, Bind, ValidationContext) []error
	// Doc is one line for the layout reference.
	Doc string
}

// Registry is the set of widget types a deployment can compose from.
//
// It is a registry rather than a switch statement so that adding a widget is
// additive: a new type, its template and its schema, with nothing central to
// edit. That matters because the alternative — a central list every widget must
// be threaded through — is exactly the coupling the composition engine exists
// to remove.
type Registry struct {
	defs map[string]Definition
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{defs: map[string]Definition{}}
}

// Register adds a definition, replacing any earlier one of the same type. It
// panics on an incomplete definition, because that is a programming error in
// this repository rather than anything a deployment can cause.
func (r *Registry) Register(d Definition) {
	switch {
	case d.Type == "":
		panic("widget: definition has no type")
	case d.Template == "":
		panic("widget: definition " + d.Type + " has no template")
	case d.Build == nil:
		panic("widget: definition " + d.Type + " has no builder")
	}
	r.defs[d.Type] = d
}

// Lookup finds a definition by type.
func (r *Registry) Lookup(kind string) (Definition, bool) {
	d, ok := r.defs[kind]
	return d, ok
}

// Types lists every registered type, sorted, for validation messages and the
// layout reference.
func (r *Registry) Types() []string {
	out := make([]string, 0, len(r.defs))
	for k := range r.defs {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Build resolves a widget and runs its builder.
func (r *Registry) Build(kind string, c Context, opts Options) (any, error) {
	d, ok := r.Lookup(kind)
	if !ok {
		return nil, fmt.Errorf("unknown widget type %q; this build provides %v", kind, r.Types())
	}
	return d.Build(c, opts)
}

// ValidateWidget checks a widget's type, options and binding together.
//
// Together rather than separately because the three constrain each other: a
// sparkline needs a history binding, and a leaderboard's column list has to
// name metrics the deployment actually declared.
func (r *Registry) ValidateWidget(kind string, opts Options, b Bind, c ValidationContext) []error {
	d, ok := r.Lookup(kind)
	if !ok {
		return []error{fmt.Errorf("unknown widget type %q; this build provides %v", kind, r.Types())}
	}

	errs := d.Schema.Validate(opts)

	if b.Source != "" {
		errs = append(errs, ValidateBind(b, c.Domain, c.InDrawer)...)

		if len(d.Sources) > 0 && !slices.Contains(d.Sources, b.Source) {
			errs = append(errs, fmt.Errorf(
				"widget %q cannot read %q; it accepts %v", kind, b.Source, d.Sources))
		}
	} else if len(d.Sources) > 0 && !d.SourceOptional {
		errs = append(errs, fmt.Errorf(
			"widget %q needs a bind source; it accepts %v", kind, d.Sources))
	}

	if d.Validate != nil {
		errs = append(errs, d.Validate(opts, b, c)...)
	}
	return errs
}

// ValidationContext is what a widget needs to check itself at startup.
type ValidationContext struct {
	Domain   config.Domain
	Icons    config.Icons
	InDrawer bool
}
