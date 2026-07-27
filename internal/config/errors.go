package config

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Error is a single configuration problem, located in the file that caused it.
//
// Locating errors matters more here than in most packages: the people editing
// these files are integrators rebranding the dashboard, not the people who wrote
// it, and "unknown metric id" without a line number is a scavenger hunt.
type Error struct {
	File   string
	Line   int
	Column int
	Path   string
	Msg    string
}

func (e Error) Error() string {
	var b strings.Builder
	b.WriteString(e.File)
	if e.Line > 0 {
		b.WriteString(":")
		b.WriteString(strconv.Itoa(e.Line))
		if e.Column > 0 {
			b.WriteString(":")
			b.WriteString(strconv.Itoa(e.Column))
		}
	}
	b.WriteString(": ")
	if e.Path != "" {
		b.WriteString(e.Path)
		b.WriteString(": ")
	}
	b.WriteString(e.Msg)
	return b.String()
}

// Errors is a set of configuration problems reported together.
//
// Validation deliberately does not stop at the first failure. Fixing a config
// file one error per run is miserable, and the errors are usually independent.
type Errors []Error

func (es Errors) Error() string {
	switch len(es) {
	case 0:
		return "no configuration errors"
	case 1:
		return es[0].Error()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d configuration errors:", len(es))
	for _, e := range es {
		b.WriteString("\n  ")
		b.WriteString(e.Error())
	}
	return b.String()
}

// Err returns the set as an error, or nil when it is empty. It exists so
// callers can write `return errs.Err()` without a length check, which is the
// usual source of the "non-nil error interface holding a nil value" bug.
func (es Errors) Err() error {
	if len(es) == 0 {
		return nil
	}
	return es
}

// pathString renders a path as the reader would write it in the file:
// []any{"metrics", 2, "id"} becomes "metrics[2].id".
func PathString(path []any) string {
	var b strings.Builder
	for _, p := range path {
		switch v := p.(type) {
		case int:
			b.WriteString("[")
			b.WriteString(strconv.Itoa(v))
			b.WriteString("]")
		default:
			if b.Len() > 0 {
				b.WriteString(".")
			}
			fmt.Fprintf(&b, "%v", v)
		}
	}
	return b.String()
}

// nodeAt walks a parsed YAML tree to the node named by path, returning nil when
// the path does not resolve. Path elements are strings for mapping keys and
// ints for sequence indices.
//
// The walk resolves as far as it can and reports the deepest node it reached,
// so an error about a missing key is still located at its parent rather than
// losing its position entirely.
// NodeAt walks a parsed YAML tree to the node named by path, returning nil when
// the path does not resolve. Path elements are strings for mapping keys and
// ints for sequence indices.
//
// It is exported because every package that validates a configuration file
// needs it: an error without a line number sends the reader on a scavenger
// hunt through a file they did not write.
func NodeAt(root *yaml.Node, path ...any) *yaml.Node {
	n := root
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}

	for _, p := range path {
		next := childNode(n, p)
		if next == nil {
			// Deepest resolvable ancestor: a located parent beats no location.
			return n
		}
		n = next
	}
	return n
}

func childNode(n *yaml.Node, p any) *yaml.Node {
	switch key := p.(type) {
	case string:
		if n.Kind != yaml.MappingNode {
			return nil
		}
		// Mapping content alternates key, value, key, value.
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				return n.Content[i+1]
			}
		}
	case int:
		if n.Kind != yaml.SequenceNode || key < 0 || key >= len(n.Content) {
			return nil
		}
		return n.Content[key]
	}
	return nil
}

// keyNode returns the node for a mapping *key* rather than its value. Errors
// about a key itself — an unknown status name, say — should point at the key.
func KeyNode(root *yaml.Node, path []any, key string) *yaml.Node {
	parent := NodeAt(root, path...)
	if parent == nil || parent.Kind != yaml.MappingNode {
		return parent
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return parent.Content[i]
		}
	}
	return parent
}
