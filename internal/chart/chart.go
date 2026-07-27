// Package chart computes SVG geometry.
//
// The demo this replaces drew its charts as hand-built inline SVG rather than
// with a charting library, and that turns out to be the right call for a
// server-rendered page: there is no library to load, no client-side data
// transformation, and the whole chart arrives in the first response. This
// package does the same arithmetic in Go.
//
// Everything is computed against a fixed viewBox and drawn with
// preserveAspectRatio="none", so a chart fills whatever width the layout gives
// it without the server knowing anything about the reader's screen.
//
// Coordinates are emitted to one decimal place. That is enough for a path that
// will be scaled anyway, and it keeps the markup — and the golden files that
// pin it — small and readable.
package chart

import (
	"math"
	"strconv"
	"strings"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
)

// Viewport is the coordinate space a chart is drawn in.
type Viewport struct {
	Width  float64
	Height float64
	// Baseline is where the drawing area ends, leaving room beneath for the
	// stroke of the lowest point to not be clipped.
	Baseline float64
}

// Sparkline is the availability trace in the service drawer.
var SparkViewport = Viewport{Width: 300, Height: 64, Baseline: 62}

// TrafficViewport is the daily-traffic bar chart.
var TrafficViewport = Viewport{Width: 300, Height: 80, Baseline: 78}

// Point is one plotted position in viewBox coordinates.
//
// The server sends these alongside the path so the browser can place a
// crosshair without re-deriving the scale. It is the one piece of chart state
// the client needs, and it is far smaller than the data it was computed from.
type Point struct {
	X float64
	Y float64
}

// Sparkline is a line chart with a filled area beneath it.
type Sparkline struct {
	// Path is the trace; Area is the same trace closed to the baseline.
	Path string
	Area string

	Points []Point

	// Min and Max are the extremes actually observed, for the axis labels.
	Min float64
	Max float64

	// Last is the most recent reading, which the summary quotes.
	Last float64

	// Empty is set when there was nothing to draw. Callers render a note rather
	// than an empty box, which would read as "zero" instead of "no data".
	Empty bool
}

// SparkOptions tunes the vertical scale.
type SparkOptions struct {
	// FloorAtMost and CeilingAtLeast widen the scale so that a service sitting
	// flat at 99.99% does not render as a dramatic mountain range. Availability
	// series are nearly flat, and scaling them to their own extremes turns
	// hundredths of a percent into cliffs.
	FloorAtMost    float64
	CeilingAtLeast float64

	// Padding keeps the trace off the very edge of the viewport.
	FloorPadding   float64
	CeilingPadding float64

	// MinSpan stops a perfectly flat series from dividing by zero.
	MinSpan float64
}

// DefaultSparkOptions matches the demo's availability scaling: the axis always
// spans at least 99% to 100%, so the eye reads a genuinely flat service as flat.
func DefaultSparkOptions() SparkOptions {
	return SparkOptions{
		FloorAtMost:    99,
		CeilingAtLeast: 100,
		FloorPadding:   0.2,
		CeilingPadding: 0.05,
		MinSpan:        0.2,
	}
}

// Spark plots a series of readings.
//
// Points with no reading are skipped rather than plotted as zero: a gap in
// reporting is not an outage, and drawing it as one would be a lie the chart
// tells more loudly than any label could correct.
func Spark(h []model.HistoryPoint, opts SparkOptions, vp Viewport) Sparkline {
	vals := make([]float64, 0, len(h))
	for _, p := range h {
		if p.Availability.Valid {
			vals = append(vals, p.Availability.Value)
		}
	}
	if len(vals) == 0 {
		return Sparkline{Empty: true}
	}

	minV, maxV := vals[0], vals[0]
	for _, v := range vals {
		minV = math.Min(minV, v)
		maxV = math.Max(maxV, v)
	}

	lo := math.Min(minV, opts.FloorAtMost) - opts.FloorPadding
	hi := math.Max(maxV, opts.CeilingAtLeast) + opts.CeilingPadding
	span := math.Max(opts.MinSpan, hi-lo)

	n := len(vals)
	x := func(i int) float64 {
		if n == 1 {
			// A single reading has no line to draw, so it sits at the left edge
			// where the crosshair can still find it.
			return 0
		}
		return float64(i) / float64(n-1) * vp.Width
	}
	y := func(v float64) float64 {
		return vp.Baseline - ((v-lo)/span)*(vp.Baseline-2)
	}

	points := make([]Point, n)
	var path, area strings.Builder
	for i, v := range vals {
		px, py := round1(x(i)), round1(y(v))
		points[i] = Point{X: px, Y: py}

		if i > 0 {
			path.WriteString(" ")
			area.WriteString(" ")
		}
		cmd := "L"
		if i == 0 {
			cmd = "M"
		}
		seg := cmd + fmtF(px) + "," + fmtF(py)
		path.WriteString(seg)
		area.WriteString(seg)
	}
	// Close the area down to the baseline and back, so the fill sits under the
	// trace rather than inside it.
	area.WriteString(" L" + fmtF(vp.Width) + "," + fmtF(vp.Height))
	area.WriteString(" L0," + fmtF(vp.Height) + " Z")

	return Sparkline{
		Path:   path.String(),
		Area:   area.String(),
		Points: points,
		Min:    minV,
		Max:    maxV,
		Last:   vals[n-1],
	}
}

// Bar is one column of a bar chart, in viewBox coordinates.
type Bar struct {
	X float64
	Y float64
	W float64
	H float64
}

// BarChart is daily traffic with an error-rate trace laid over it.
//
// The two are drawn together on purpose: a spike in errors during a traffic
// peak is a capacity story, and the same spike during a trough is not. Neither
// chart alone can tell them apart.
type BarChart struct {
	Bars    []Bar
	Points  []Point
	ErrPath string

	Peak  model.HistoryPoint
	Low   model.HistoryPoint
	Max   int64
	Min   int64
	Empty bool
}

// BarOptions tunes the bar chart.
type BarOptions struct {
	// Days limits the chart to the most recent window. Zero means all of it.
	Days int

	// Gap is the space between bars, in viewBox units.
	Gap float64

	// MinErrorCeiling stops a service with almost no errors from rendering its
	// noise floor as a dramatic overlay.
	MinErrorCeiling float64
}

// DefaultBarOptions matches the demo: the last thirty days, with the error
// overlay scaled to at least half a percent.
func DefaultBarOptions() BarOptions {
	return BarOptions{Days: 30, Gap: 1, MinErrorCeiling: 0.5}
}

// Bars plots daily volume with an error-rate overlay.
func Bars(h []model.HistoryPoint, opts BarOptions, vp Viewport) BarChart {
	if opts.Days > 0 && len(h) > opts.Days {
		h = h[len(h)-opts.Days:]
	}
	if len(h) == 0 {
		return BarChart{Empty: true}
	}

	n := len(h)
	maxVol, minVol := h[0].Volume, h[0].Volume
	peak, low := h[0], h[0]
	maxErr := opts.MinErrorCeiling

	for _, p := range h {
		if p.Volume > maxVol {
			maxVol, peak = p.Volume, p
		}
		if p.Volume < minVol {
			minVol, low = p.Volume, p
		}
		maxErr = math.Max(maxErr, p.ErrorRate)
	}

	width := vp.Width / float64(n)
	// The drawing area stops short of the baseline so the tallest bar has a
	// little headroom rather than touching the top edge.
	plot := vp.Baseline - 4

	bars := make([]Bar, n)
	points := make([]Point, n)
	var errPath strings.Builder

	for i, p := range h {
		height := 0.0
		if maxVol > 0 {
			height = float64(p.Volume) / float64(maxVol) * plot
		}
		bars[i] = Bar{
			X: round1(float64(i)*width + opts.Gap/2),
			Y: round1(vp.Baseline - height),
			W: round1(math.Max(0.1, width-opts.Gap)),
			H: round1(height),
		}

		ex := round1(float64(i)*width + width/2)
		ey := round1(vp.Baseline - (p.ErrorRate/maxErr)*plot)
		points[i] = Point{X: ex, Y: ey}

		if i > 0 {
			errPath.WriteString(" ")
		}
		cmd := "L"
		if i == 0 {
			cmd = "M"
		}
		errPath.WriteString(cmd + fmtF(ex) + "," + fmtF(ey))
	}

	return BarChart{
		Bars:    bars,
		Points:  points,
		ErrPath: errPath.String(),
		Peak:    peak,
		Low:     low,
		Max:     maxVol,
		Min:     minVol,
	}
}

// Share is one row of a proportional bar list, such as the error breakdown.
type Share struct {
	// Percent is the width as a percentage of the row, already clamped to a
	// sane range so a bad upstream cannot produce a bar wider than its track.
	Percent float64
}

// Shares converts counts into row widths.
//
// Widths are relative to the largest row rather than to the total, so that a
// breakdown where one code dominates still shows the smaller ones as visible
// rather than as slivers.
func Shares(counts []int64) []Share {
	out := make([]Share, len(counts))
	var largest int64
	for _, c := range counts {
		largest = max(largest, c)
	}
	if largest == 0 {
		return out
	}
	for i, c := range counts {
		out[i] = Share{Percent: round1(float64(c) / float64(largest) * 100)}
	}
	return out
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// fmtF renders a coordinate without a trailing ".0", which halves the size of
// most path strings.
func fmtF(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
