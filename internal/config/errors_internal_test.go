package config

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestErrorFormatting(t *testing.T) {
	tests := []struct {
		name string
		err  Error
		want string
	}{
		{
			"located with path",
			Error{File: "domain.yaml", Line: 12, Column: 7, Path: "metrics[2].id", Msg: "unknown metric"},
			"domain.yaml:12:7: metrics[2].id: unknown metric",
		},
		{
			// A column of 0 means the node carried no column; printing ":0"
			// would send the reader to the wrong place.
			"line without column",
			Error{File: "app.yaml", Line: 3, Msg: "bad"},
			"app.yaml:3: bad",
		},
		{
			// Whole-file problems have no position at all.
			"no position at all",
			Error{File: "icons.yaml", Msg: "missing from the configuration bundle"},
			"icons.yaml: missing from the configuration bundle",
		},
		{
			"position without path",
			Error{File: "theme.yaml", Line: 9, Column: 2, Msg: "bad token"},
			"theme.yaml:9:2: bad token",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestErrorsAggregation(t *testing.T) {
	if got := (Errors{}).Error(); got != "no configuration errors" {
		t.Errorf("empty set = %q", got)
	}
	if err := (Errors{}).Err(); err != nil {
		t.Errorf("empty set returned non-nil error %v", err)
	}

	one := Errors{{File: "a.yaml", Msg: "boom"}}
	if got := one.Error(); got != "a.yaml: boom" {
		t.Errorf("single error = %q, want it rendered bare", got)
	}
	if one.Err() == nil {
		t.Error("non-empty set returned a nil error")
	}

	two := Errors{{File: "a.yaml", Msg: "boom"}, {File: "b.yaml", Msg: "bang"}}
	got := two.Error()
	if !strings.HasPrefix(got, "2 configuration errors:") {
		t.Errorf("multi-error set does not lead with a count: %q", got)
	}
	for _, want := range []string{"a.yaml: boom", "b.yaml: bang"} {
		if !strings.Contains(got, want) {
			t.Errorf("aggregate omits %q:\n%s", want, got)
		}
	}
}

func TestPathString(t *testing.T) {
	tests := []struct {
		path []any
		want string
	}{
		{nil, ""},
		{[]any{"metrics"}, "metrics"},
		{[]any{"metrics", 2}, "metrics[2]"},
		{[]any{"metrics", 2, "id"}, "metrics[2].id"},
		{[]any{"a", "b", "c"}, "a.b.c"},
		{[]any{0}, "[0]"},
		{[]any{"signals", 0, "filter", "status", 1}, "signals[0].filter.status[1]"},
	}

	for _, tc := range tests {
		if got := PathString(tc.path); got != tc.want {
			t.Errorf("PathString(%v) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

const navFixture = `
top:
  nested:
    leaf: value
  list:
    - first
    - second
scalar: plain
`

func parseNav(t *testing.T) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(navFixture), &n); err != nil {
		t.Fatalf("fixture does not parse: %v", err)
	}
	return &n
}

func TestNodeAtResolvesPaths(t *testing.T) {
	root := parseNav(t)

	if got := NodeAt(root, "top", "nested", "leaf"); got == nil || got.Value != "value" {
		t.Errorf("leaf lookup got %v, want the node holding \"value\"", got)
	}
	if got := NodeAt(root, "top", "list", 1); got == nil || got.Value != "second" {
		t.Errorf("sequence index lookup got %v, want \"second\"", got)
	}
	if got := NodeAt(root, "scalar"); got == nil || got.Value != "plain" {
		t.Errorf("scalar lookup got %v, want \"plain\"", got)
	}
	if got := NodeAt(root); got == nil || got.Kind != yaml.MappingNode {
		t.Errorf("empty path should yield the document's root mapping, got %v", got)
	}
}

func TestNodeAtFallsBackToDeepestAncestor(t *testing.T) {
	// A located parent beats no location: an error about a key that is missing
	// should still send the reader to the block that should have contained it.
	root := parseNav(t)

	tests := []struct {
		name string
		path []any
	}{
		{"missing map key", []any{"top", "absent"}},
		{"index past the end", []any{"top", "list", 9}},
		{"negative index", []any{"top", "list", -1}},
		{"index into a mapping", []any{"top", 0}},
		{"key into a sequence", []any{"top", "list", "notAnIndex"}},
		{"key into a scalar", []any{"scalar", "deeper"}},
		{"unsupported path element", []any{"top", 3.5}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NodeAt(root, tc.path...)
			if got == nil {
				t.Fatal("lookup returned nil instead of the deepest resolvable ancestor")
			}
			if got.Line == 0 {
				t.Error("fallback node carries no line, so the error would be unlocated")
			}
		})
	}
}

func TestNodeAtHandlesNilAndBareNodes(t *testing.T) {
	if got := NodeAt(nil, "anything"); got != nil {
		t.Errorf("NodeAt(nil) = %v, want nil", got)
	}

	// A document node with no content must not panic on the Content[0] unwrap.
	empty := &yaml.Node{Kind: yaml.DocumentNode}
	if got := NodeAt(empty, "x"); got != empty {
		t.Errorf("empty document lookup = %v, want the document itself", got)
	}
}

func TestKeyNodePointsAtTheKey(t *testing.T) {
	root := parseNav(t)

	keyN := KeyNode(root, []any{"top"}, "nested")
	if keyN == nil || keyN.Value != "nested" {
		t.Fatalf("got %v, want the node for the key \"nested\"", keyN)
	}

	valueN := NodeAt(root, "top", "nested")
	if keyN.Line == valueN.Line && keyN.Column == valueN.Column {
		t.Error("key and value resolved to the same position; the key lookup is not distinguishing them")
	}
}

func TestKeyNodeFallsBackWhenNotAMapping(t *testing.T) {
	root := parseNav(t)

	// Asking for a key inside a sequence: fall back to the sequence itself
	// rather than losing the location.
	if got := KeyNode(root, []any{"top", "list"}, "nested"); got == nil {
		t.Error("key lookup in a sequence returned nil instead of falling back")
	}
	// Asking for a key the mapping does not have.
	if got := KeyNode(root, []any{"top"}, "absent"); got == nil || got.Kind != yaml.MappingNode {
		t.Errorf("missing key lookup = %v, want the parent mapping", got)
	}
	if got := KeyNode(nil, []any{"top"}, "nested"); got != nil {
		t.Errorf("KeyNode(nil) = %v, want nil", got)
	}
}

func TestSplitYAMLLinePrefix(t *testing.T) {
	tests := []struct {
		in       string
		wantLine int
		wantMsg  string
	}{
		{"line 12: field foo not found", 12, "field foo not found"},
		{"yaml: line 7: did not find expected key", 7, "did not find expected key"},
		{"no prefix at all", 0, "no prefix at all"},
		{"line notanumber: something", 0, "line notanumber: something"},
		{"line 12 without a colon", 0, "line 12 without a colon"},
		{"", 0, ""},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			line, msg := splitYAMLLinePrefix(tc.in)
			if line != tc.wantLine || msg != tc.wantMsg {
				t.Errorf("got (%d, %q), want (%d, %q)", line, msg, tc.wantLine, tc.wantMsg)
			}
		})
	}
}

func TestDecodeErrorOnPlainError(t *testing.T) {
	// Not every yaml failure is a *TypeError; a syntax error arrives as a plain
	// one and must still be located and attributed to its file.
	got := DecodeError("app.yaml", errors.New("line 4: some syntax problem"))
	if got.File != "app.yaml" {
		t.Errorf("File = %q, want app.yaml", got.File)
	}
	if got.Line != 4 {
		t.Errorf("Line = %d, want 4", got.Line)
	}
	if got.Msg != "some syntax problem" {
		t.Errorf("Msg = %q", got.Msg)
	}
}

func TestDecodeErrorJoinsMultipleTypeErrors(t *testing.T) {
	// A strict decode can surface several unknown fields at once; reporting
	// only the first would send the reader round the loop again.
	got := DecodeError("domain.yaml", &yaml.TypeError{Errors: []string{
		"line 3: field alpha not found in type config.Domain",
		"line 9: field beta not found in type config.Domain",
	}})

	if got.Line != 3 {
		t.Errorf("Line = %d, want the first error's line 3", got.Line)
	}
	for _, want := range []string{"alpha", "beta"} {
		if !strings.Contains(got.Msg, want) {
			t.Errorf("joined message omits %q: %q", want, got.Msg)
		}
	}
}

func TestRequireAllKeysAcceptsCompleteMap(t *testing.T) {
	v := &validator{}
	v.requireAllKeys("f.yaml", []any{"m"}, map[string]string{"a": "1", "b": "2"}, []string{"a", "b"}, "thing")
	if len(v.errs) != 0 {
		t.Errorf("complete map reported %d errors: %v", len(v.errs), v.errs)
	}

	v.requireAllKeys("f.yaml", []any{"m"}, map[string]string{"a": "1"}, []string{"a", "b"}, "thing")
	if len(v.errs) != 1 {
		t.Fatalf("incomplete map reported %d errors, want 1", len(v.errs))
	}
	if !strings.Contains(v.errs[0].Msg, `"b"`) {
		t.Errorf("error does not name the missing key: %q", v.errs[0].Msg)
	}
}
