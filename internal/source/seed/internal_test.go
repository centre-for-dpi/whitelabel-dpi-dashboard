package seed

import (
	"math"
	"testing"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
)

// TestRNGMatchesTheJavaScriptReference pins the port.
//
// These values were produced by running the prototype's own mulberry32 under
// Node. If the Go transcription of JavaScript's 32-bit Math.imul and >>> ever
// drifts, this fails immediately rather than silently producing a different but
// plausible-looking demo.
func TestRNGMatchesTheJavaScriptReference(t *testing.T) {
	tests := []struct {
		seed uint32
		want []float64
	}{
		{1000, []float64{0.795194906881, 0.827687913552, 0.691516105784, 0.88057521428, 0.017807392636, 0.425102789653}},
		{1485, []float64{0.269677722128, 0.110191508196, 0.288297154941, 0.444224402541}},
	}

	for _, tc := range tests {
		r := newRNG(tc.seed)
		for i, want := range tc.want {
			got := math.Round(r.float()*1e12) / 1e12
			if got != want {
				t.Errorf("seed %d, draw %d: got %.12f, want %.12f", tc.seed, i, got, want)
			}
		}
	}
}

func TestRNGStaysInRange(t *testing.T) {
	r := newRNG(7)
	for range 10000 {
		v := r.float()
		if v < 0 || v >= 1 {
			t.Fatalf("draw out of range: %v", v)
		}
	}
}

func TestBetweenSpansTheRequestedRange(t *testing.T) {
	r := newRNG(42)
	lo, hi := math.Inf(1), math.Inf(-1)
	for range 5000 {
		v := r.between(10, 20)
		if v < 10 || v >= 20 {
			t.Fatalf("value %v outside [10,20)", v)
		}
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	// A generator that returned a constant would pass the bounds check above.
	if hi-lo < 9 {
		t.Errorf("values only spanned %v of the 10-unit range", hi-lo)
	}
}

func TestPickOnAnEmptySliceYieldsTheZeroValue(t *testing.T) {
	// Callers index a map of note templates by tier; a tier with no templates
	// must not panic.
	r := newRNG(1)
	if got := pick(r, []string(nil)); got != "" {
		t.Errorf("got %q, want the zero value", got)
	}
}

func TestPickReachesEveryElement(t *testing.T) {
	r := newRNG(3)
	seen := map[string]bool{}
	for range 200 {
		seen[pick(r, []string{"a", "b", "c"})] = true
	}
	if len(seen) != 3 {
		t.Errorf("only reached %v", seen)
	}
}

func TestPlaceSpreadsEvenly(t *testing.T) {
	out := make([]tier, 100)
	for i := range out {
		out[i] = tierOperational
	}

	place(out, tierMajor, 4)

	var at []int
	for i, v := range out {
		if v == tierMajor {
			at = append(at, i)
		}
	}
	if len(at) != 4 {
		t.Fatalf("placed %d, want 4", len(at))
	}
	// Roughly a quarter apart, rather than clustered at one end.
	for i := 1; i < len(at); i++ {
		if gap := at[i] - at[i-1]; gap < 20 || gap > 30 {
			t.Errorf("gap between placements %d and %d is %d, want about 25", i-1, i, gap)
		}
	}
}

func TestPlaceWalksPastOccupiedSlots(t *testing.T) {
	// Two tiers wanting the same slot must not overwrite each other, or the
	// requested mix would silently come out short.
	out := make([]tier, 10)
	for i := range out {
		out[i] = tierOperational
	}

	place(out, tierMajor, 5)
	place(out, tierPartial, 5)

	counts := map[tier]int{}
	for _, v := range out {
		counts[v]++
	}
	if counts[tierMajor] != 5 || counts[tierPartial] != 5 {
		t.Errorf("counts = %v, want five of each", counts)
	}
	if counts[tierOperational] != 0 {
		t.Errorf("counts = %v, want the list fully consumed", counts)
	}
}

func TestPlaceIgnoresNonsenseCounts(t *testing.T) {
	out := make([]tier, 5)
	for i := range out {
		out[i] = tierOperational
	}

	place(out, tierMajor, 0)
	place(out, tierMajor, -3)
	for _, v := range out {
		if v != tierOperational {
			t.Fatalf("a non-positive count placed something: %v", out)
		}
	}

	// More than fits is clamped rather than looping forever.
	place(out, tierMajor, 99)
	for _, v := range out {
		if v != tierMajor {
			t.Fatalf("clamped placement left a gap: %v", out)
		}
	}

	place(nil, tierMajor, 3) // must not panic
}

func TestHistoryHandlesAServiceWithNoErrors(t *testing.T) {
	// A zero error rate would otherwise make every generated day identical,
	// producing a suspiciously flat error chart.
	r := newRNG(11)
	m := model.Metrics{
		Availability: model.Float(99.9),
		ErrorRate:    0,
		LatencyP50:   200,
		Volume:       model.Volume{Total: 1000, Success: 1000},
	}

	h := generateHistory(r, m, tierOperational, Options{HistoryDays: 10, Now: time.Now()})

	if len(h) != 10 {
		t.Fatalf("got %d days, want 10", len(h))
	}
	varied := false
	for i := 1; i < len(h); i++ {
		if h[i].ErrorRate != h[0].ErrorRate {
			varied = true
			break
		}
	}
	if !varied {
		t.Error("every day has an identical error rate")
	}
}

func TestNoTrafficMeansNoErrorBreakdown(t *testing.T) {
	// Zero requests cannot have produced any errors, and a breakdown of nothing
	// would render as an empty table rather than being omitted.
	r := newRNG(13)
	m := model.Metrics{ErrorRate: 2.0, Volume: model.Volume{Total: 0}}

	if got := generateErrors(r, tierMajor, m); got != nil {
		t.Errorf("got %v, want nothing", got)
	}
}

func TestTinyErrorCountsDropEmptyBuckets(t *testing.T) {
	// With only a handful of errors most codes round to zero, and a bucket
	// reading "0 requests · 0%" is noise.
	r := newRNG(17)
	m := model.Metrics{ErrorRate: 0.1, Volume: model.Volume{Total: 2000}}

	for _, b := range generateErrors(r, tierPartial, m) {
		if b.Count <= 0 {
			t.Errorf("bucket %q survived with count %d", b.Code, b.Count)
		}
	}
}

func TestSeverityTierMapsToItsNoteTemplates(t *testing.T) {
	if got := severityTier(model.StatusMajor); got != tierMajor {
		t.Errorf("major mapped to %q", got)
	}
	// Anything else is treated as partial, so an unexpected severity still gets
	// a usable note rather than an empty one.
	for _, s := range []model.Status{model.StatusPartial, model.StatusUnknown, ""} {
		if got := severityTier(s); got != tierPartial {
			t.Errorf("%q mapped to %q, want partial", s, got)
		}
	}
}

func TestRandomDirectionCoversEveryOutcome(t *testing.T) {
	r := newRNG(23)
	seen := map[model.Direction]bool{}
	for range 200 {
		seen[randomDirection(r)] = true
	}
	for _, d := range []model.Direction{model.DirectionUp, model.DirectionDown, model.DirectionFlat} {
		if !seen[d] {
			t.Errorf("never produced %q", d)
		}
	}
}
