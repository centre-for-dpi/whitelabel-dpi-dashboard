package transform_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/transform"
)

func num(t *testing.T, v transform.Value) float64 {
	t.Helper()
	n, ok := v.Number()
	if !ok {
		t.Fatalf("not a number: %#v", v.Raw)
	}
	return n
}

func text(t *testing.T, v transform.Value) string {
	t.Helper()
	s, ok := v.Text()
	if !ok {
		t.Fatalf("not text: %#v", v.Raw)
	}
	return s
}

// --- reading values ---------------------------------------------------------

func TestNumberAcceptsWhatAPIsActuallySend(t *testing.T) {
	var decoded any
	if err := json.Unmarshal([]byte(`{"a": 1.5, "b": "2900000", "c": true}`), &decoded); err != nil {
		t.Fatal(err)
	}
	obj := decoded.(map[string]any)

	for _, tc := range []struct {
		name string
		raw  any
		want float64
	}{
		{"a JSON number", obj["a"], 1.5},
		// Large integers sent as strings are common enough that refusing them
		// would be pedantry.
		{"a numeric string", obj["b"], 2900000},
		{"a boolean true", obj["c"], 1},
		{"a boolean false", false, 0},
		{"a Go int", 42, 42},
		{"a Go int64", int64(42), 42},
		{"a Go float32", float32(1.5), 1.5},
		{"a padded string", "  7  ", 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := num(t, transform.Of(tc.raw)); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNumberRejectsWhatIsNotOne(t *testing.T) {
	for _, raw := range []any{"not a number", []any{1}, map[string]any{}} {
		if _, ok := transform.Of(raw).Number(); ok {
			t.Errorf("%#v was read as a number", raw)
		}
	}
	if _, ok := transform.Absent().Number(); ok {
		t.Error("an absent value was read as a number")
	}
}

func TestAbsenceIsNotZero(t *testing.T) {
	// The whole availability rule depends on this: absence means unknown, zero
	// means a total outage.
	if transform.Absent().Present {
		t.Error("Absent() reports present")
	}
	if transform.Of(nil).Present {
		t.Error("a reported null reports present")
	}
	if !transform.Of(0.0).Present {
		t.Error("a reported zero reports absent")
	}
}

func TestTextAndBool(t *testing.T) {
	if got := text(t, transform.Of("IDENTITY")); got != "IDENTITY" {
		t.Errorf("got %q", got)
	}
	if got := text(t, transform.Of(1.5)); got != "1.5" {
		t.Errorf("got %q", got)
	}
	if got := text(t, transform.Of(true)); got != "true" {
		t.Errorf("got %q", got)
	}
	if _, ok := transform.Of([]any{}).Text(); ok {
		t.Error("a list was read as text")
	}
	if _, ok := transform.Absent().Text(); ok {
		t.Error("an absent value was read as text")
	}

	for _, tc := range []struct {
		raw  any
		want bool
	}{
		{true, true}, {"true", true}, {"false", false}, {1.0, true}, {0.0, false},
	} {
		got, ok := transform.Of(tc.raw).Bool()
		if !ok || got != tc.want {
			t.Errorf("Bool(%#v) = %v, %v; want %v", tc.raw, got, ok, tc.want)
		}
	}
	if _, ok := transform.Of([]any{}).Bool(); ok {
		t.Error("a list was read as a boolean")
	}
	if _, ok := transform.Absent().Bool(); ok {
		t.Error("an absent value was read as a boolean")
	}
}

func TestTimeAcceptsTheFormatsAPIsSend(t *testing.T) {
	want := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name string
		raw  any
		want time.Time
	}{
		{"RFC3339", "2026-07-27T12:00:00Z", want},
		{"RFC3339 with nanoseconds", "2026-07-27T12:00:00.500Z", want.Add(500 * time.Millisecond)},
		{"no zone", "2026-07-27T12:00:00", want},
		{"a space instead of T", "2026-07-27 12:00:00", want},
		{"a bare date", "2026-07-27", want.Truncate(24 * time.Hour)},
		{"epoch seconds", float64(want.Unix()), want},
		{"epoch milliseconds", float64(want.UnixMilli()), want},
		{"padded", "  2026-07-27T12:00:00Z  ", want},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := transform.Of(tc.raw).Time()
			if !ok {
				t.Fatalf("%#v was not read as a time", tc.raw)
			}
			if !got.Equal(tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}

	for _, raw := range []any{"yesterday", true, []any{}} {
		if _, ok := transform.Of(raw).Time(); ok {
			t.Errorf("%#v was read as a time", raw)
		}
	}
	if _, ok := transform.Absent().Time(); ok {
		t.Error("an absent value was read as a time")
	}
}

// --- transforms -------------------------------------------------------------

func apply(t *testing.T, raw any, specs ...transform.Spec) transform.Value {
	t.Helper()
	return transform.Apply(transform.Of(raw), specs)
}

func TestNumericTransforms(t *testing.T) {
	// The single most common integration mistake: a collector reporting 0.9991
	// and a dashboard rendering 99.91%.
	for _, tc := range []struct {
		name string
		spec transform.Spec
		in   float64
		want float64
	}{
		{"ratioToPercent", transform.Spec{Fn: "ratioToPercent"}, 0.9991, 99.91},
		{"percentToRatio", transform.Spec{Fn: "percentToRatio"}, 99.91, 0.9991},
		{"multiply", transform.Spec{Fn: "multiply", By: 3}, 4, 12},
		{"divide", transform.Spec{Fn: "divide", By: 4}, 12, 3},
		{"negate", transform.Spec{Fn: "negate"}, 5, -5},
		{"round", transform.Spec{Fn: "round", By: 2}, 99.9149, 99.91},
		{"secondsToMillis", transform.Spec{Fn: "secondsToMillis"}, 1.5, 1500},
		{"millisToSeconds", transform.Spec{Fn: "millisToSeconds"}, 1500, 1.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := num(t, apply(t, tc.in, tc.spec))
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClampPercentKeepsRoundingArtefactsOffTheScreen(t *testing.T) {
	// An upstream reporting 100.4% is not evidence of better than perfect
	// availability, and rendering it looks like a bug in the dashboard rather
	// than in the source.
	spec := transform.Spec{Fn: "clampPercent"}

	if got := num(t, apply(t, 100.4, spec)); got != 100 {
		t.Errorf("got %v, want 100", got)
	}
	if got := num(t, apply(t, -0.2, spec)); got != 0 {
		t.Errorf("got %v, want 0", got)
	}
	if got := num(t, apply(t, 99.5, spec)); got != 99.5 {
		t.Errorf("got %v, want it untouched", got)
	}
}

func TestEnumMap(t *testing.T) {
	spec := transform.Spec{Fn: "enumMap", Table: map[string]string{
		"IDENTITY": "cat.identity",
		"MONEY":    "cat.money",
	}}

	if got := text(t, apply(t, "IDENTITY", spec)); got != "cat.identity" {
		t.Errorf("got %q", got)
	}
	// An unmapped value passes through as itself, so a new category appearing
	// upstream surfaces as a config validation error naming it rather than
	// silently vanishing from the dashboard.
	if got := text(t, apply(t, "AGRICULTURE", spec)); got != "AGRICULTURE" {
		t.Errorf("got %q, want the value passed through", got)
	}
}

func TestStringTransforms(t *testing.T) {
	for _, tc := range []struct {
		spec transform.Spec
		in   string
		want string
	}{
		{transform.Spec{Fn: "lowercase"}, "IDENTITY", "identity"},
		{transform.Spec{Fn: "uppercase"}, "identity", "IDENTITY"},
		{transform.Spec{Fn: "trim"}, "  spaced  ", "spaced"},
		{transform.Spec{Fn: "trimPrefix", Value: "svc-"}, "svc-aadhaar", "aadhaar"},
		{transform.Spec{Fn: "trimPrefix", Value: "svc-"}, "aadhaar", "aadhaar"},
	} {
		t.Run(tc.spec.Fn+"/"+tc.in, func(t *testing.T) {
			if got := text(t, apply(t, tc.in, tc.spec)); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestChainsApplyInOrder(t *testing.T) {
	// Order matters and is the deployment's to choose: rounding before scaling
	// gives a different answer from scaling before rounding.
	got := num(t, apply(t, 0.99914,
		transform.Spec{Fn: "ratioToPercent"},
		transform.Spec{Fn: "round", By: 2},
	))

	if got != 99.91 {
		t.Errorf("got %v, want 99.91", got)
	}
}

func TestAbsentValuesPassThroughUntouched(t *testing.T) {
	// Transforming a value that was never reported would manufacture data,
	// which is the one thing a status dashboard must not do.
	got := transform.Apply(transform.Absent(), []transform.Spec{
		{Fn: "ratioToPercent"},
		{Fn: "multiply", By: 100},
	})

	if got.Present {
		t.Errorf("an absent value acquired a value: %#v", got.Raw)
	}
}

func TestDefaultIsTheOneTransformThatFillsAnAbsence(t *testing.T) {
	got := transform.Apply(transform.Absent(), []transform.Spec{
		{Fn: "default", Value: "reg.national"},
	})

	if !got.Present {
		t.Fatal("default did not fill the absence")
	}
	if text(t, got) != "reg.national" {
		t.Errorf("got %q", text(t, got))
	}

	// And it leaves a value that was reported alone, including a zero.
	if v := apply(t, 0.0, transform.Spec{Fn: "default", Value: "99"}); num(t, v) != 0 {
		t.Errorf("default overwrote a reported zero")
	}
}

func TestTransformsThatCannotApplyLeaveTheValueAlone(t *testing.T) {
	// A numeric transform on text, or a text transform on a list. Better to
	// carry the value through and let mapping report a type error than to
	// silently produce a zero.
	if got := text(t, apply(t, "not a number", transform.Spec{Fn: "ratioToPercent"})); got != "not a number" {
		t.Errorf("got %q", got)
	}
	if got := apply(t, []any{1}, transform.Spec{Fn: "uppercase"}); got.Raw == nil {
		t.Error("a list was consumed by a text transform")
	}
	if got := apply(t, []any{1}, transform.Spec{Fn: "enumMap", Table: map[string]string{"a": "b"}}); got.Raw == nil {
		t.Error("a list was consumed by enumMap")
	}
}

func TestUnknownTransformIsAPassThroughAtRuntime(t *testing.T) {
	// Validation rejects it at startup; a hot reload races validation, and
	// passing the value through is better than dropping it.
	if got := num(t, apply(t, 5.0, transform.Spec{Fn: "vibes"})); got != 5 {
		t.Errorf("got %v", got)
	}
}

// --- validation --------------------------------------------------------------

func TestValidateAcceptsEveryDeclaredTransform(t *testing.T) {
	for _, name := range transform.Names() {
		spec := transform.Spec{Fn: name}
		// Supply the operands the ones that need them require.
		switch name {
		case "enumMap":
			spec.Table = map[string]string{"A": "b"}
		case "multiply", "divide":
			spec.By = 2
		}
		if errs := transform.Validate([]transform.Spec{spec}); len(errs) != 0 {
			t.Errorf("%s was rejected: %v", name, errs)
		}
	}
}

func TestValidateRejectsMistakes(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec transform.Spec
		want string
	}{
		{"unknown transform", transform.Spec{Fn: "vibes"}, "unknown transform"},
		{"enumMap with no table", transform.Spec{Fn: "enumMap"}, "needs a table"},
		// Dividing by zero would produce an infinity that renders as "+Inf".
		{"divide by zero", transform.Spec{Fn: "divide"}, "non-zero"},
		{"multiply by zero", transform.Spec{Fn: "multiply"}, "non-zero"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := transform.Validate([]transform.Spec{tc.spec})
			if len(errs) == 0 {
				t.Fatal("accepted")
			}
			if !strings.Contains(errs[0].Error(), tc.want) {
				t.Errorf("error does not mention %q: %v", tc.want, errs[0])
			}
		})
	}
}

func TestValidateNamesTheStepThatIsWrong(t *testing.T) {
	// A chain of five transforms needs to say which one.
	errs := transform.Validate([]transform.Spec{
		{Fn: "ratioToPercent"},
		{Fn: "vibes"},
	})

	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
	if !strings.Contains(errs[0].Error(), "step 1") {
		t.Errorf("error does not name the step: %v", errs[0])
	}
}

func TestValidateOfAnEmptyChain(t *testing.T) {
	if errs := transform.Validate(nil); len(errs) != 0 {
		t.Errorf("an empty chain was rejected: %v", errs)
	}
}

func TestNumberAcceptsADecoderUsingJSONNumber(t *testing.T) {
	// A caller decoding with UseNumber preserves integer precision above 2^53,
	// which a request count could conceivably reach. Values arrive as
	// json.Number rather than float64, and must still read as numbers.
	dec := json.NewDecoder(strings.NewReader(`{"total": 9007199254740993}`))
	dec.UseNumber()

	var decoded map[string]any
	if err := dec.Decode(&decoded); err != nil {
		t.Fatal(err)
	}

	got, ok := transform.Of(decoded["total"]).Number()
	if !ok {
		t.Fatalf("a json.Number was not read as a number: %#v", decoded["total"])
	}
	if got < 9e15 {
		t.Errorf("got %v, want the large value preserved", got)
	}

	// And one that is not a number at all still reports so.
	if _, ok := transform.Of(json.Number("not a number")).Number(); ok {
		t.Error("a malformed json.Number was read as a number")
	}
}
