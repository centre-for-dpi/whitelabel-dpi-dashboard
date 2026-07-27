package chart_test

import (
	"strings"
	"testing"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/chart"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
)

func avail(vals ...float64) []model.HistoryPoint {
	day := time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC)
	out := make([]model.HistoryPoint, len(vals))
	for i, v := range vals {
		out[i] = model.HistoryPoint{Day: day.AddDate(0, 0, i), Availability: model.Float(v)}
	}
	return out
}

func traffic(vols ...int64) []model.HistoryPoint {
	day := time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC)
	out := make([]model.HistoryPoint, len(vols))
	for i, v := range vols {
		out[i] = model.HistoryPoint{Day: day.AddDate(0, 0, i), Volume: v}
	}
	return out
}

// --- sparkline -------------------------------------------------------------

func TestSparkSpansTheFullWidth(t *testing.T) {
	got := chart.Spark(avail(99.5, 99.6, 99.7), chart.DefaultSparkOptions(), chart.SparkViewport)

	if got.Points[0].X != 0 {
		t.Errorf("first point x = %v, want 0", got.Points[0].X)
	}
	if last := got.Points[len(got.Points)-1].X; last != chart.SparkViewport.Width {
		t.Errorf("last point x = %v, want %v", last, chart.SparkViewport.Width)
	}
	if !strings.HasPrefix(got.Path, "M0,") {
		t.Errorf("path does not start with a move: %q", got.Path)
	}
	if strings.Count(got.Path, "L") != 2 {
		t.Errorf("path should have two line segments for three points: %q", got.Path)
	}
}

func TestSparkKeepsFlatSeriesFlat(t *testing.T) {
	// Availability series are nearly flat. Scaling one to its own extremes would
	// turn hundredths of a percent into cliffs and make every service look
	// alarming, so the axis always spans at least 99% to 100%.
	flat := chart.Spark(avail(99.97, 99.98, 99.99), chart.DefaultSparkOptions(), chart.SparkViewport)

	var lo, hi float64 = 1e9, -1e9
	for _, p := range flat.Points {
		lo = min(lo, p.Y)
		hi = max(hi, p.Y)
	}
	if spread := hi - lo; spread > 3 {
		t.Errorf("a 0.02%% range rendered %v units tall; the axis is scaling to the data instead of the target band", spread)
	}
}

func TestSparkShowsRealVariationWhenThereIsSome(t *testing.T) {
	got := chart.Spark(avail(97.0, 99.9), chart.DefaultSparkOptions(), chart.SparkViewport)

	if spread := got.Points[0].Y - got.Points[1].Y; spread < 20 {
		t.Errorf("a 2.9%% range rendered only %v units tall", spread)
	}
}

func TestSparkReportsTheObservedExtremes(t *testing.T) {
	// The labels quote what was actually seen, not the padded axis bounds.
	got := chart.Spark(avail(99.2, 99.9, 99.5), chart.DefaultSparkOptions(), chart.SparkViewport)

	if got.Min != 99.2 || got.Max != 99.9 {
		t.Errorf("min/max = %v/%v, want 99.2/99.9", got.Min, got.Max)
	}
	if got.Last != 99.5 {
		t.Errorf("last = %v, want 99.5", got.Last)
	}
}

func TestSparkAreaClosesToTheBaseline(t *testing.T) {
	got := chart.Spark(avail(99.5, 99.6), chart.DefaultSparkOptions(), chart.SparkViewport)

	if !strings.HasSuffix(got.Area, " L300,64 L0,64 Z") {
		t.Errorf("area does not close to the baseline: %q", got.Area)
	}
	if !strings.HasPrefix(got.Area, "M0,") {
		t.Errorf("area does not start at the first point: %q", got.Area)
	}
}

func TestSparkSkipsUnreportedDays(t *testing.T) {
	// A gap in reporting is not an outage. Plotting it as zero would be a lie
	// the chart tells more loudly than any label could correct.
	h := avail(99.5, 99.6, 99.7)
	h[1].Availability = model.NoFloat()

	got := chart.Spark(h, chart.DefaultSparkOptions(), chart.SparkViewport)

	if len(got.Points) != 2 {
		t.Fatalf("got %d points, want 2 — the unreported day should be skipped", len(got.Points))
	}
	if got.Min != 99.5 || got.Max != 99.7 {
		t.Errorf("min/max = %v/%v; the skipped day leaked into the extremes", got.Min, got.Max)
	}
	if strings.Contains(got.Path, ",62") {
		t.Errorf("a skipped day appears to have been plotted at the floor: %q", got.Path)
	}
}

func TestSparkWithNothingToDrawIsMarkedEmpty(t *testing.T) {
	// Callers render a note rather than an empty box, which would read as
	// "zero" instead of "no data".
	for _, tc := range []struct {
		name string
		h    []model.HistoryPoint
	}{
		{"no history", nil},
		{"no readings", []model.HistoryPoint{{Availability: model.NoFloat()}, {Availability: model.NoFloat()}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := chart.Spark(tc.h, chart.DefaultSparkOptions(), chart.SparkViewport)
			if !got.Empty {
				t.Error("not marked empty")
			}
			if got.Path != "" || len(got.Points) != 0 {
				t.Errorf("empty chart produced geometry: %+v", got)
			}
		})
	}
}

func TestSparkWithOneReadingDoesNotDivideByZero(t *testing.T) {
	got := chart.Spark(avail(99.5), chart.DefaultSparkOptions(), chart.SparkViewport)

	if len(got.Points) != 1 {
		t.Fatalf("got %d points, want 1", len(got.Points))
	}
	if got.Points[0].X != 0 {
		t.Errorf("single point x = %v, want 0", got.Points[0].X)
	}
	if strings.Contains(got.Path, "NaN") || strings.Contains(got.Area, "NaN") {
		t.Errorf("NaN in the path: %q / %q", got.Path, got.Area)
	}
}

func TestSparkSurvivesAPerfectlyFlatSeries(t *testing.T) {
	// Identical readings give a zero range; without a minimum span the scale
	// would divide by zero and every coordinate would be NaN.
	got := chart.Spark(avail(100, 100, 100), chart.DefaultSparkOptions(), chart.SparkViewport)

	for _, p := range got.Points {
		if p.Y != p.Y { // NaN
			t.Fatalf("NaN coordinate in %v", got.Points)
		}
	}
	if strings.Contains(got.Path, "NaN") {
		t.Errorf("NaN in the path: %q", got.Path)
	}
}

func TestSparkIsDeterministic(t *testing.T) {
	h := avail(99.1, 99.9, 99.4, 99.7)
	first := chart.Spark(h, chart.DefaultSparkOptions(), chart.SparkViewport)

	for range 20 {
		again := chart.Spark(h, chart.DefaultSparkOptions(), chart.SparkViewport)
		if again.Path != first.Path || again.Area != first.Area {
			t.Fatal("geometry varies between identical calls")
		}
	}
}

func TestSparkCoordinatesAreCompact(t *testing.T) {
	// Paths ship in every drawer response, so trailing zeroes are not free.
	got := chart.Spark(avail(99.5, 99.6, 99.7), chart.DefaultSparkOptions(), chart.SparkViewport)

	for _, seg := range strings.Split(got.Path, " ") {
		if strings.HasSuffix(seg, ".0") {
			t.Errorf("path segment %q carries a redundant decimal", seg)
		}
	}
}

// --- bar chart -------------------------------------------------------------

func TestBarsLimitToTheRecentWindow(t *testing.T) {
	h := traffic(1, 2, 3, 4, 5, 6, 7, 8, 9, 10)

	got := chart.Bars(h, chart.BarOptions{Days: 3, Gap: 1, MinErrorCeiling: 0.5}, chart.TrafficViewport)

	if len(got.Bars) != 3 {
		t.Fatalf("got %d bars, want 3", len(got.Bars))
	}
	if got.Max != 10 {
		t.Errorf("max = %d, want 10 — the window should be the last three days", got.Max)
	}
}

func TestBarsUseTheWholeHistoryWhenShorter(t *testing.T) {
	got := chart.Bars(traffic(1, 2), chart.DefaultBarOptions(), chart.TrafficViewport)

	if len(got.Bars) != 2 {
		t.Errorf("got %d bars, want 2", len(got.Bars))
	}
}

func TestBarsFillTheWidth(t *testing.T) {
	got := chart.Bars(traffic(1, 1, 1, 1), chart.BarOptions{Gap: 1, MinErrorCeiling: 0.5}, chart.TrafficViewport)

	first, last := got.Bars[0], got.Bars[len(got.Bars)-1]
	if first.X < 0 {
		t.Errorf("first bar starts off-canvas at %v", first.X)
	}
	if right := last.X + last.W; right > chart.TrafficViewport.Width {
		t.Errorf("last bar ends at %v, past the %v edge", right, chart.TrafficViewport.Width)
	}
}

func TestTallestBarIsTheBusiestDay(t *testing.T) {
	got := chart.Bars(traffic(10, 100, 50), chart.BarOptions{Gap: 1, MinErrorCeiling: 0.5}, chart.TrafficViewport)

	if got.Bars[1].H <= got.Bars[0].H || got.Bars[1].H <= got.Bars[2].H {
		t.Errorf("the busiest day is not the tallest bar: %+v", got.Bars)
	}
	if got.Peak.Volume != 100 {
		t.Errorf("peak = %d, want 100", got.Peak.Volume)
	}
	if got.Low.Volume != 10 {
		t.Errorf("low = %d, want 10", got.Low.Volume)
	}
}

func TestBarsSurviveADayWithNoTraffic(t *testing.T) {
	// A service with no requests at all must not divide by zero.
	got := chart.Bars(traffic(0, 0, 0), chart.DefaultBarOptions(), chart.TrafficViewport)

	for _, b := range got.Bars {
		if b.H != 0 {
			t.Errorf("bar height = %v, want 0", b.H)
		}
		if b.Y != b.Y {
			t.Fatal("NaN coordinate")
		}
	}
}

func TestErrorOverlayIsScaledToItsOwnCeiling(t *testing.T) {
	h := traffic(100, 100, 100)
	h[0].ErrorRate = 0.1
	h[1].ErrorRate = 4.0
	h[2].ErrorRate = 0.1

	got := chart.Bars(h, chart.DefaultBarOptions(), chart.TrafficViewport)

	// The worst day is highest on the chart, so lowest in y.
	if got.Points[1].Y >= got.Points[0].Y {
		t.Errorf("the worst error day is not the peak of the overlay: %v", got.Points)
	}
	if !strings.HasPrefix(got.ErrPath, "M") || strings.Count(got.ErrPath, "L") != 2 {
		t.Errorf("overlay path is malformed: %q", got.ErrPath)
	}
}

func TestQuietErrorsDoNotRenderAsDrama(t *testing.T) {
	// Without a floor on the error scale, a service whose worst day was 0.02%
	// would draw its noise floor as a mountain.
	h := traffic(100, 100, 100)
	h[0].ErrorRate = 0.01
	h[1].ErrorRate = 0.02
	h[2].ErrorRate = 0.01

	got := chart.Bars(h, chart.DefaultBarOptions(), chart.TrafficViewport)

	// With a ceiling of 0.5, a 0.02 reading should sit very near the baseline.
	if drop := chart.TrafficViewport.Baseline - got.Points[1].Y; drop > 10 {
		t.Errorf("a 0.02%% error rate rose %v units off the baseline", drop)
	}
}

func TestBarsWithNothingToDrawAreMarkedEmpty(t *testing.T) {
	got := chart.Bars(nil, chart.DefaultBarOptions(), chart.TrafficViewport)

	if !got.Empty {
		t.Error("not marked empty")
	}
	if len(got.Bars) != 0 || got.ErrPath != "" {
		t.Errorf("empty chart produced geometry: %+v", got)
	}
}

func TestBarsAreDeterministic(t *testing.T) {
	h := traffic(5, 9, 2, 7)
	first := chart.Bars(h, chart.DefaultBarOptions(), chart.TrafficViewport)

	for range 20 {
		again := chart.Bars(h, chart.DefaultBarOptions(), chart.TrafficViewport)
		if again.ErrPath != first.ErrPath || len(again.Bars) != len(first.Bars) {
			t.Fatal("geometry varies between identical calls")
		}
		for i := range first.Bars {
			if again.Bars[i] != first.Bars[i] {
				t.Fatal("bar geometry varies between identical calls")
			}
		}
	}
}

func TestNarrowBarsStayVisible(t *testing.T) {
	// Ninety days in a 300-unit viewport gives bars barely three units wide; the
	// gap must not consume them entirely.
	vols := make([]int64, 90)
	for i := range vols {
		vols[i] = int64(i + 1)
	}

	got := chart.Bars(traffic(vols...), chart.BarOptions{Days: 90, Gap: 1, MinErrorCeiling: 0.5}, chart.TrafficViewport)

	for i, b := range got.Bars {
		if b.W <= 0 {
			t.Fatalf("bar %d has width %v", i, b.W)
		}
	}
}

func TestGapWiderThanTheBarStillDraws(t *testing.T) {
	vols := make([]int64, 200)
	for i := range vols {
		vols[i] = 10
	}

	got := chart.Bars(traffic(vols...), chart.BarOptions{Days: 200, Gap: 5, MinErrorCeiling: 0.5}, chart.TrafficViewport)

	for i, b := range got.Bars {
		if b.W <= 0 {
			t.Fatalf("bar %d collapsed to width %v", i, b.W)
		}
	}
}

// --- proportional shares ---------------------------------------------------

func TestSharesAreRelativeToTheLargest(t *testing.T) {
	// Relative to the largest rather than the total, so a breakdown dominated by
	// one code still shows the others as visible bars rather than slivers.
	got := chart.Shares([]int64{100, 50, 10})

	if got[0].Percent != 100 {
		t.Errorf("largest = %v%%, want 100", got[0].Percent)
	}
	if got[1].Percent != 50 {
		t.Errorf("half = %v%%, want 50", got[1].Percent)
	}
	if got[2].Percent != 10 {
		t.Errorf("tenth = %v%%, want 10", got[2].Percent)
	}
}

func TestSharesOfNothingAreZero(t *testing.T) {
	got := chart.Shares([]int64{0, 0})

	for i, s := range got {
		if s.Percent != 0 {
			t.Errorf("share %d = %v, want 0", i, s.Percent)
		}
	}

	if got := chart.Shares(nil); len(got) != 0 {
		t.Errorf("got %v, want nothing", got)
	}
}
