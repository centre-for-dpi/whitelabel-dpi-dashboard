// Package fieldpath reads values out of decoded JSON by path.
//
// It exists so that integrating an upstream service is a mapping written in
// YAML rather than a Go type someone has to define, compile and deploy. A
// deployment says `uptimePct: $.sla.uptime` and that is the whole integration.
//
// The syntax is a deliberately small subset of JSONPath — enough to reach into
// the shapes real APIs actually return, and small enough that its behaviour
// fits in a paragraph:
//
//	$.a.b        a nested object
//	$.items[0]   one element
//	$.items[*]   every element, for the history block
//	$.a.b[2].c   the three combined
//
// There are no filters, no wildcards over keys, no recursive descent and no
// expressions. Each of those would turn a config file into a program, and a
// program in a config file is something nobody can validate at startup.
package fieldpath

import (
	"fmt"
	"strconv"
	"strings"
)

// Path is a compiled expression.
type Path struct {
	raw   string
	steps []step
}

type step struct {
	key      string
	index    int
	wildcard bool
	isIndex  bool
}

// String returns the expression as written, for error messages.
func (p Path) String() string { return p.raw }

// Empty reports whether the path selects nothing, which is how a mapping omits
// a field it does not have.
func (p Path) Empty() bool { return p.raw == "" }

// Parse compiles an expression.
//
// The leading "$." is optional: a deployment writing `sla.uptime` means the
// same thing as `$.sla.uptime`, and rejecting the shorter form would be
// pedantry rather than safety.
func Parse(expr string) (Path, error) {
	trimmed := strings.TrimSpace(expr)
	if trimmed == "" {
		return Path{}, nil
	}

	p := Path{raw: trimmed}
	rest := strings.TrimPrefix(strings.TrimPrefix(trimmed, "$"), ".")

	for rest != "" {
		var segment string
		segment, rest = cutSegment(rest)
		if segment == "" {
			return Path{}, fmt.Errorf("empty segment in %q", expr)
		}

		name, brackets, hasBracket := strings.Cut(segment, "[")
		if name != "" {
			if err := validKey(name, expr); err != nil {
				return Path{}, err
			}
			p.steps = append(p.steps, step{key: name})
		}
		if hasBracket {
			// Put the bracket back so the loop below sees a uniform "[...]..."
			// and an unterminated one cannot be mistaken for the end.
			brackets = "[" + brackets
		}

		for brackets != "" {
			inner, rest, ok := cutBracket(brackets)
			if !ok {
				return Path{}, fmt.Errorf("unterminated [ in %q", expr)
			}
			brackets = rest

			switch inner {
			case "*":
				p.steps = append(p.steps, step{wildcard: true, isIndex: true})
			case "":
				return Path{}, fmt.Errorf("empty [] in %q", expr)
			default:
				n, err := strconv.Atoi(inner)
				if err != nil {
					return Path{}, fmt.Errorf("index %q in %q is not a number or *", inner, expr)
				}
				if n < 0 {
					return Path{}, fmt.Errorf("negative index %d in %q", n, expr)
				}
				p.steps = append(p.steps, step{index: n, isIndex: true})
			}
		}
	}

	if len(p.steps) == 0 {
		return Path{}, fmt.Errorf("%q selects nothing", expr)
	}
	return p, nil
}

// MustParse is Parse for paths written in this repository rather than by a
// deployment. It panics, because a bad path here is a programming error.
func MustParse(expr string) Path {
	p, err := Parse(expr)
	if err != nil {
		panic("fieldpath: " + err.Error())
	}
	return p
}

func cutSegment(s string) (segment, rest string) {
	// A dot inside brackets is not a separator.
	depth := 0
	for i := range len(s) {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
		case '.':
			if depth == 0 {
				return s[:i], s[i+1:]
			}
		}
	}
	return s, ""
}

// cutBracket reads one "[...]" group. It reports false for an unterminated
// bracket, which must be an error rather than being silently dropped: a path
// that quietly selects nothing on every request is far harder to diagnose than
// one that refuses to start.
// The caller always hands it a string beginning with "[", so there is no
// leading-bracket check here to go stale.
func cutBracket(s string) (inner, rest string, ok bool) {
	body, rest, found := strings.Cut(s[1:], "]")
	if !found {
		return "", "", false
	}
	return body, rest, true
}

func validKey(key, expr string) error {
	if strings.ContainsAny(key, " ]") {
		return fmt.Errorf("key %q in %q contains a space or bracket", key, expr)
	}
	return nil
}

// Get returns the single value a path selects.
//
// The second result is false when the path does not resolve, which callers
// treat as "the upstream did not report this" rather than as an error. A field
// an API omits for some services is ordinary, not exceptional.
func (p Path) Get(doc any) (any, bool) {
	values := p.All(doc)
	if len(values) == 0 {
		return nil, false
	}
	return values[0], true
}

// All returns every value a path selects, which is one for a plain path and
// many when it contains a wildcard.
func (p Path) All(doc any) []any {
	if len(p.steps) == 0 {
		return nil
	}
	current := []any{doc}

	for _, s := range p.steps {
		next := make([]any, 0, len(current))
		for _, node := range current {
			next = append(next, s.apply(node)...)
		}
		if len(next) == 0 {
			return nil
		}
		current = next
	}
	return current
}

func (s step) apply(node any) []any {
	if !s.isIndex {
		obj, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		v, ok := obj[s.key]
		if !ok {
			return nil
		}
		return []any{v}
	}

	arr, ok := node.([]any)
	if !ok {
		return nil
	}
	if s.wildcard {
		return arr
	}
	if s.index >= len(arr) {
		return nil
	}
	return []any{arr[s.index]}
}
