package widget

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Options are a widget's settings, as written in layout.yaml.
//
// They arrive as whatever YAML decoded them to, so every read goes through a
// typed accessor that reports what it found rather than panicking or silently
// substituting a zero. A deployment editing layout.yaml is editing a text file
// with no compiler behind it; the errors have to be worth reading.
type Options map[string]any

// OptionKind is the type an option is declared to hold.
type OptionKind string

const (
	KindString     OptionKind = "string"
	KindInt        OptionKind = "int"
	KindBool       OptionKind = "bool"
	KindStringList OptionKind = "stringList"
)

// OptionField declares one setting a widget accepts.
type OptionField struct {
	Kind     OptionKind
	Required bool
	// Enum constrains a string option to a fixed set.
	Enum []string
	// Default is used when the option is absent. It is also what the generated
	// documentation shows, so it should be the value that makes the widget
	// behave the way the shipped layout expects.
	Default any
	// Doc is one line explaining the setting, for the layout reference.
	Doc string
}

// OptionSchema is everything a widget accepts. Anything not declared here is
// rejected, for the same reason the config loader rejects unknown fields: a
// silently ignored setting leaves the reader wondering why their edit did
// nothing.
type OptionSchema map[string]OptionField

// Validate checks a widget's options against its schema, returning every
// problem rather than only the first.
func (s OptionSchema) Validate(opts Options) []error {
	var errs []error

	for name := range opts {
		if _, ok := s[name]; !ok {
			errs = append(errs, fmt.Errorf("unknown option %q; this widget accepts %s", name, s.names()))
		}
	}

	for _, name := range s.names() {
		f := s[name]
		raw, present := opts[name]

		if !present {
			if f.Required {
				errs = append(errs, fmt.Errorf("option %q is required", name))
			}
			continue
		}
		if err := f.check(name, raw); err != nil {
			errs = append(errs, err)
		}
	}

	slices.SortFunc(errs, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
	return errs
}

func (s OptionSchema) names() []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func (f OptionField) check(name string, raw any) error {
	switch f.Kind {
	case KindString:
		v, ok := asString(raw)
		if !ok {
			return fmt.Errorf("option %q must be text, got %T", name, raw)
		}
		if len(f.Enum) > 0 && !slices.Contains(f.Enum, v) {
			return fmt.Errorf("option %q is %q; expected one of %v", name, v, f.Enum)
		}

	case KindInt:
		if _, ok := asInt(raw); !ok {
			return fmt.Errorf("option %q must be a whole number, got %T", name, raw)
		}

	case KindBool:
		if _, ok := raw.(bool); !ok {
			return fmt.Errorf("option %q must be true or false, got %T", name, raw)
		}

	case KindStringList:
		list, ok := asStringList(raw)
		if !ok {
			return fmt.Errorf("option %q must be a list of text values, got %T", name, raw)
		}
		if len(f.Enum) > 0 {
			for _, v := range list {
				if !slices.Contains(f.Enum, v) {
					return fmt.Errorf("option %q contains %q; expected values from %v", name, v, f.Enum)
				}
			}
		}
	}
	return nil
}

// String reads a text option, falling back to the schema default.
func (o Options) String(s OptionSchema, name string) string {
	if raw, ok := o[name]; ok {
		if v, ok := asString(raw); ok {
			return v
		}
	}
	if v, ok := asString(s[name].Default); ok {
		return v
	}
	return ""
}

// Int reads a whole-number option, falling back to the schema default.
func (o Options) Int(s OptionSchema, name string) int {
	if raw, ok := o[name]; ok {
		if v, ok := asInt(raw); ok {
			return v
		}
	}
	if v, ok := asInt(s[name].Default); ok {
		return v
	}
	return 0
}

// Bool reads a true/false option, falling back to the schema default.
func (o Options) Bool(s OptionSchema, name string) bool {
	if raw, ok := o[name]; ok {
		if v, ok := raw.(bool); ok {
			return v
		}
	}
	if v, ok := s[name].Default.(bool); ok {
		return v
	}
	return false
}

// StringList reads a list option, falling back to the schema default.
func (o Options) StringList(s OptionSchema, name string) []string {
	if raw, ok := o[name]; ok {
		if v, ok := asStringList(raw); ok {
			return v
		}
	}
	if v, ok := asStringList(s[name].Default); ok {
		return v
	}
	return nil
}

func asString(raw any) (string, bool) {
	v, ok := raw.(string)
	return v, ok
}

// asInt accepts the several numeric shapes YAML and JSON produce for the same
// written number.
func asInt(raw any) (int, bool) {
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		// YAML gives a float for a number written with a decimal point, and
		// JSON gives one for every number. A whole value is still a whole
		// number; a fractional one is a mistake worth reporting.
		if v == float64(int(v)) {
			return int(v), true
		}
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n, true
		}
	}
	return 0, false
}

func asStringList(raw any) ([]string, bool) {
	switch v := raw.(type) {
	case []string:
		return v, true
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := asString(item)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}
