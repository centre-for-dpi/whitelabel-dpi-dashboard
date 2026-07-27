package fieldpath_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/fieldpath"
)

// doc is shaped like a real upstream response: nested objects, an array of
// records, and an array of daily points inside each one.
const doc = `{
  "generatedAt": "2026-07-27T12:00:00Z",
  "data": {
    "services": [
      {
        "serviceId": "aadhaar",
        "sla": {"uptimePct": 0.9992, "errorPct": 0.0062},
        "requests": {"total": 2480492, "succeeded": 2465112},
        "daily": [
          {"ts": "2026-07-25", "uptime": 0.9989},
          {"ts": "2026-07-26", "uptime": 0.9991},
          {"ts": "2026-07-27", "uptime": 0.9992}
        ],
        "tags": ["identity", "national"],
        "maintenance": null
      },
      {
        "serviceId": "pan",
        "sla": {"uptimePct": null, "errorPct": 0.01},
        "daily": []
      }
    ]
  }
}`

func parsed(t *testing.T) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func get(t *testing.T, expr string) (any, bool) {
	t.Helper()
	p, err := fieldpath.Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q): %v", expr, err)
	}
	return p.Get(parsed(t))
}

func TestReadsNestedValues(t *testing.T) {
	tests := []struct {
		expr string
		want any
	}{
		{"$.generatedAt", "2026-07-27T12:00:00Z"},
		{"$.data.services[0].serviceId", "aadhaar"},
		{"$.data.services[0].sla.uptimePct", 0.9992},
		{"$.data.services[0].requests.total", 2480492.0},
		{"$.data.services[1].serviceId", "pan"},
		{"$.data.services[0].daily[1].ts", "2026-07-26"},
		{"$.data.services[0].tags[0]", "identity"},
		// The leading $. is optional; rejecting the shorter form would be
		// pedantry rather than safety.
		{"data.services[0].serviceId", "aadhaar"},
		{"generatedAt", "2026-07-27T12:00:00Z"},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			got, ok := get(t, tc.expr)
			if !ok {
				t.Fatalf("did not resolve")
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestWildcardSelectsEveryElement(t *testing.T) {
	// This is what the history block uses: one path, many daily points.
	p, err := fieldpath.Parse("$.data.services[0].daily[*].ts")
	if err != nil {
		t.Fatal(err)
	}

	got := p.All(parsed(t))

	want := []any{"2026-07-25", "2026-07-26", "2026-07-27"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestWildcardOverRecords(t *testing.T) {
	p, err := fieldpath.Parse("$.data.services[*].serviceId")
	if err != nil {
		t.Fatal(err)
	}

	if got := p.All(parsed(t)); len(got) != 2 || got[0] != "aadhaar" || got[1] != "pan" {
		t.Errorf("got %#v", got)
	}
}

func TestGetOnAWildcardReturnsTheFirst(t *testing.T) {
	got, ok := get(t, "$.data.services[*].serviceId")

	if !ok || got != "aadhaar" {
		t.Errorf("got %#v, %v", got, ok)
	}
}

func TestUnresolvedPathsAreNotErrors(t *testing.T) {
	// A field an API omits for some services is ordinary. Callers read this as
	// "not reported", which is exactly what it means.
	for _, expr := range []string{
		"$.nonexistent",
		"$.data.services[0].nonexistent",
		"$.data.services[9].serviceId",
		"$.data.services[0].daily[99].ts",
		"$.data.services[1].daily[*].uptime", // an empty array
		"$.generatedAt.deeper",               // stepping into a string
		"$.data.services.notAnIndex",         // treating an array as an object
		"$.data[0]",                          // treating an object as an array
	} {
		t.Run(expr, func(t *testing.T) {
			if _, ok := get(t, expr); ok {
				t.Error("resolved, want not found")
			}
		})
	}
}

func TestNullResolvesAsAPresentNull(t *testing.T) {
	// "Reported as null" and "not reported" are different claims, and the
	// mapping layer needs to tell them apart: a null uptime means unknown,
	// while an absent one may mean the field is simply not in this API.
	got, ok := get(t, "$.data.services[1].sla.uptimePct")

	if !ok {
		t.Fatal("an explicit null did not resolve")
	}
	if got != nil {
		t.Errorf("got %#v, want nil", got)
	}
}

func TestEmptyPathSelectsNothing(t *testing.T) {
	// How a mapping omits a field it does not have.
	p, err := fieldpath.Parse("")
	if err != nil {
		t.Fatalf("an empty path was rejected: %v", err)
	}
	if !p.Empty() {
		t.Error("not reported as empty")
	}
	if _, ok := p.Get(parsed(t)); ok {
		t.Error("an empty path resolved to something")
	}
	if got := p.All(parsed(t)); got != nil {
		t.Errorf("All = %#v, want nothing", got)
	}
}

func TestMalformedPathsAreRejected(t *testing.T) {
	// Rejected at startup with the expression quoted, rather than silently
	// resolving to nothing on every request.
	for _, expr := range []string{
		"$.items[a]",
		"$.items[-1]",
		"$.items[]",
		"$..items",
		"$.a b.c",
		"$.",
	} {
		t.Run(expr, func(t *testing.T) {
			if _, err := fieldpath.Parse(expr); err == nil {
				t.Error("accepted")
			}
		})
	}
}

func TestErrorsQuoteTheExpression(t *testing.T) {
	// The reader is looking at a YAML file full of paths and needs to know which
	// one is wrong.
	_, err := fieldpath.Parse("$.items[a]")
	if err == nil {
		t.Fatal("accepted")
	}
	if !contains(err.Error(), "$.items[a]") {
		t.Errorf("error does not quote the path: %v", err)
	}
}

func TestStringReportsWhatWasWritten(t *testing.T) {
	p, err := fieldpath.Parse("  $.a.b  ")
	if err != nil {
		t.Fatal(err)
	}
	if got := p.String(); got != "$.a.b" {
		t.Errorf("got %q", got)
	}
}

func TestMustParsePanicsOnlyOnBadPaths(t *testing.T) {
	// Used for paths written in this repository, where a bad one is a
	// programming error rather than a deployment's mistake.
	fieldpath.MustParse("$.a.b")

	defer func() {
		if recover() == nil {
			t.Error("a malformed path did not panic")
		}
	}()
	fieldpath.MustParse("$.a[")
}

func TestMultipleIndicesInOneSegment(t *testing.T) {
	var v any
	if err := json.Unmarshal([]byte(`{"grid":[[1,2],[3,4]]}`), &v); err != nil {
		t.Fatal(err)
	}

	p, err := fieldpath.Parse("$.grid[1][0]")
	if err != nil {
		t.Fatalf("a nested index was rejected: %v", err)
	}
	got, ok := p.Get(v)
	if !ok || got != 3.0 {
		t.Errorf("got %#v, %v; want 3", got, ok)
	}
}

func TestNilDocument(t *testing.T) {
	p := fieldpath.MustParse("$.a")

	if _, ok := p.Get(nil); ok {
		t.Error("resolved against nothing")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

func TestUnterminatedBracketsAreRejected(t *testing.T) {
	// A path that quietly selects nothing on every request is far harder to
	// diagnose than one that refuses to start.
	for _, expr := range []string{
		"$.a[",
		"$.a[1",
		"$.a[*",
		"$.items[0][1",
	} {
		t.Run(expr, func(t *testing.T) {
			if _, err := fieldpath.Parse(expr); err == nil {
				t.Error("accepted")
			}
		})
	}
}

func TestBracketWithoutAKeyIsRejected(t *testing.T) {
	// "]0]" reaches cutBracket without a leading bracket.
	if _, err := fieldpath.Parse("$.a.]0]"); err == nil {
		t.Error("accepted a stray closing bracket")
	}
}
