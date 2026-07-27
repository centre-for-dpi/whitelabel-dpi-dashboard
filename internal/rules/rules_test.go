package rules_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/rules"
)

func thresholds() config.Thresholds {
	return config.Thresholds{
		EvaluationOrder: []string{"maintenance", "unknown", "major", "partial", "operational"},
		Values: config.ThresholdValues{
			MajorAvailBelow:   99.0,
			MajorErrAbove:     2.0,
			PartialAvailBelow: 99.5,
			PartialErrAbove:   1.0,
			StaleSecondsAbove: 900,
		},
	}
}

func metrics(avail model.OptFloat, errRate float64, stale int64) model.Metrics {
	return model.Metrics{Availability: avail, ErrorRate: errRate, StaleSeconds: stale}
}

func TestEvaluateCoversEveryStatus(t *testing.T) {
	th := thresholds()

	tests := []struct {
		name  string
		m     model.Metrics
		maint model.Maintenance
		want  model.Status
	}{
		{"healthy", metrics(model.Float(99.91), 0.31, 45), model.Maintenance{}, model.StatusOperational},
		{"availability just below the partial line", metrics(model.Float(99.49), 0.31, 45), model.Maintenance{}, model.StatusPartial},
		{"error rate just above the partial line", metrics(model.Float(99.91), 1.01, 45), model.Maintenance{}, model.StatusPartial},
		{"availability below the major line", metrics(model.Float(98.99), 0.31, 45), model.Maintenance{}, model.StatusMajor},
		{"error rate above the major line", metrics(model.Float(99.91), 2.01, 45), model.Maintenance{}, model.StatusMajor},
		{"no availability reading", metrics(model.NoFloat(), 0.31, 45), model.Maintenance{}, model.StatusUnknown},
		{"reading too old to trust", metrics(model.Float(99.91), 0.31, 901), model.Maintenance{}, model.StatusUnknown},
		{"planned window", metrics(model.Float(99.91), 0.31, 45), model.Maintenance{Active: true}, model.StatusMaintenance},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := rules.Evaluate(tc.m, tc.maint, th); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEvaluateBoundariesAreInclusiveAsPublished(t *testing.T) {
	th := thresholds()

	// The published prose says "below 99.5%" and "above 1.0%". A service sitting
	// exactly on its target is meeting it, so the comparisons must be strict.
	tests := []struct {
		name string
		m    model.Metrics
		want model.Status
	}{
		{"exactly at the availability target", metrics(model.Float(99.5), 0.0, 0), model.StatusOperational},
		{"exactly at the error target", metrics(model.Float(100), 1.0, 0), model.StatusOperational},
		{"exactly at the major availability line", metrics(model.Float(99.0), 0.0, 0), model.StatusPartial},
		{"exactly at the major error line", metrics(model.Float(100), 2.0, 0), model.StatusPartial},
		{"exactly at the staleness limit", metrics(model.Float(100), 0.0, 900), model.StatusOperational},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := rules.Evaluate(tc.m, model.Maintenance{}, th); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEvaluateFollowsTheConfiguredOrder(t *testing.T) {
	// The order is published as configuration, so it has to be the order that is
	// actually applied — otherwise the dashboard's own explanation would be
	// wrong. A service that is both in maintenance and erroring badly reports
	// whichever the deployment put first.
	m := metrics(model.Float(90.0), 9.0, 45)
	maint := model.Maintenance{Active: true}

	th := thresholds()
	if got := rules.Evaluate(m, maint, th); got != model.StatusMaintenance {
		t.Errorf("with maintenance first, got %q, want maintenance", got)
	}

	th.EvaluationOrder = []string{"unknown", "major", "partial", "maintenance", "operational"}
	if got := rules.Evaluate(m, maint, th); got != model.StatusMajor {
		t.Errorf("with major first, got %q, want major", got)
	}
}

func TestEvaluateWithNoMatchingRuleIsUnknown(t *testing.T) {
	// A deployment can shorten the order. Anything that falls off the end is
	// genuinely undetermined rather than quietly healthy.
	th := thresholds()
	th.EvaluationOrder = []string{"major"}

	if got := rules.Evaluate(metrics(model.Float(100), 0, 0), model.Maintenance{}, th); got != model.StatusUnknown {
		t.Errorf("got %q, want unknown", got)
	}
}

func TestEvaluateIgnoresUnrecognisedStatusesInTheOrder(t *testing.T) {
	th := thresholds()
	th.EvaluationOrder = []string{"invented", "operational"}

	if got := rules.Evaluate(metrics(model.Float(100), 0, 0), model.Maintenance{}, th); got != model.StatusOperational {
		t.Errorf("got %q, want operational", got)
	}
}

func TestMissingAvailabilityNeverCountsAsAnOutage(t *testing.T) {
	// If a deployment puts major before unknown, an absent reading must not be
	// read as 0% and reported as a total outage. Absence is not zero.
	th := thresholds()
	th.EvaluationOrder = []string{"major", "partial", "unknown", "operational"}

	got := rules.Evaluate(metrics(model.NoFloat(), 0.1, 45), model.Maintenance{}, th)
	if got == model.StatusMajor {
		t.Fatal("an absent availability reading was treated as 0% and reported as a major outage")
	}
	if got != model.StatusUnknown {
		t.Errorf("got %q, want unknown", got)
	}
}

// --- trends ----------------------------------------------------------------

func history(vals ...float64) []model.HistoryPoint {
	out := make([]model.HistoryPoint, len(vals))
	day := time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC)
	for i, v := range vals {
		out[i] = model.HistoryPoint{
			Day:          day.AddDate(0, 0, i),
			Availability: model.Float(v),
			ErrorRate:    v / 100,
			Volume:       int64(v * 1000),
			LatencyP50:   int32(v),
		}
	}
	return out
}

func TestTrendComparesAgainstTheLookback(t *testing.T) {
	h := history(99.0, 99.2, 99.4, 99.6, 99.8)

	got := rules.Trend(h, config.FieldAvailability, 2)

	if got.Delta != 0.4 {
		t.Errorf("delta = %v, want 0.4 (99.8 today against 99.4 two days back)", got.Delta)
	}
	if got.Direction != model.DirectionUp {
		t.Errorf("direction = %q, want up", got.Direction)
	}
	if got.PeriodDays != 2 {
		t.Errorf("periodDays = %d, want 2", got.PeriodDays)
	}
}

func TestTrendReportsDeclines(t *testing.T) {
	// Direction is only a direction: whether a fall is good news depends on the
	// metric, and that judgement belongs to the metric's configured `direction`
	// rather than here.
	h := history(99.8, 99.6, 99.4)

	got := rules.Trend(h, config.FieldAvailability, 2)

	if got.Direction != model.DirectionDown {
		t.Errorf("direction = %q, want down", got.Direction)
	}
	if got.Delta != -0.4 {
		t.Errorf("delta = %v, want -0.4", got.Delta)
	}
}

func TestTrendClampsToAvailableHistory(t *testing.T) {
	// Asking for 90 days when only 3 exist compares against the oldest point
	// rather than reading off the end of the slice.
	h := history(99.0, 99.5, 99.9)

	got := rules.Trend(h, config.FieldAvailability, 90)

	if got.Delta != 0.9 {
		t.Errorf("delta = %v, want 0.9 against the oldest available point", got.Delta)
	}
}

func TestTrendTreatsSmallMovementAsFlat(t *testing.T) {
	// Rates wobble. Reporting every hundredth of a percent as a rise would make
	// the trend column noise.
	h := history(99.50, 99.51)

	if got := rules.Trend(h, config.FieldAvailability, 1); got.Direction != model.DirectionFlat {
		t.Errorf("a 0.01 move reported as %q, want flat", got.Direction)
	}

	h = history(99.50, 99.60)
	if got := rules.Trend(h, config.FieldAvailability, 1); got.Direction != model.DirectionUp {
		t.Errorf("a 0.10 move reported as %q, want up", got.Direction)
	}
}

func TestTrendScalesTheFlatBandWithVolume(t *testing.T) {
	// A fixed epsilon is meaningless for traffic: 0.02 requests is nothing at
	// any scale, so the band is proportional instead.
	h := []model.HistoryPoint{{Volume: 1_000_000}, {Volume: 1_004_000}}
	if got := rules.Trend(h, config.FieldVolume, 1); got.Direction != model.DirectionFlat {
		t.Errorf("a 0.4%% traffic move reported as %q, want flat", got.Direction)
	}

	h = []model.HistoryPoint{{Volume: 1_000_000}, {Volume: 1_060_000}}
	if got := rules.Trend(h, config.FieldVolume, 1); got.Direction != model.DirectionUp {
		t.Errorf("a 6%% traffic move reported as %q, want up", got.Direction)
	}
}

func TestTrendHandlesEveryField(t *testing.T) {
	h := history(10, 20)

	for _, field := range []string{
		config.FieldAvailability,
		config.FieldErrorRate,
		config.FieldLatencyP50,
		config.FieldVolume,
	} {
		got := rules.Trend(h, field, 1)
		if got.Direction != model.DirectionUp {
			t.Errorf("%s: direction = %q, want up", field, got.Direction)
		}
	}
}

func TestTrendOnAbsentOrEmptyHistoryIsFlat(t *testing.T) {
	// Direction is explicitly flat rather than left empty: an absent direction
	// would render as a missing glyph rather than as "unchanged".
	want := model.Trend{Direction: model.DirectionFlat, PeriodDays: 30}
	if got := rules.Trend(nil, config.FieldAvailability, 30); got != want {
		t.Errorf("empty history produced %+v, want %+v", got, want)
	}

	// A single point has nothing to compare against.
	if got := rules.Trend(history(99.5), config.FieldAvailability, 30); got.Direction != model.DirectionFlat {
		t.Errorf("single-point history produced %q, want flat", got.Direction)
	}

	// An unreported availability cannot move.
	h := []model.HistoryPoint{{Availability: model.NoFloat()}, {Availability: model.NoFloat()}}
	if got := rules.Trend(h, config.FieldAvailability, 1); got.Direction != model.DirectionFlat {
		t.Errorf("absent readings produced %q, want flat", got.Direction)
	}
}

func TestTrendOnAnUnknownFieldIsFlat(t *testing.T) {
	if got := rules.Trend(history(1, 2), "invented", 1); got.Direction != model.DirectionFlat {
		t.Errorf("unknown field produced %q, want flat", got.Direction)
	}
}

func TestTrendRoundsToTwoPlaces(t *testing.T) {
	// The delta is rendered directly, so it is rounded here rather than left to
	// each caller to format consistently.
	h := history(99.111111, 99.999999)

	if got := rules.Trend(h, config.FieldAvailability, 1); got.Delta != 0.89 {
		t.Errorf("delta = %v, want 0.89", got.Delta)
	}
}

// --- threshold prose -------------------------------------------------------

func TestProseParamsExposesEveryPublishedNumber(t *testing.T) {
	// The rule shown on screen quotes these, so a missing key would render as an
	// empty gap in the middle of a sentence.
	got := rules.ProseParams(thresholds().Values)

	// The names are the published contract with translators: they are what the
	// shipped locale files interpolate, so renaming one here silently empties a
	// sentence on screen.
	want := map[string]any{
		"majorAvail":   99.0,
		"majorErr":     2.0,
		"partialAvail": 99.5,
		"partialErr":   1.0,
		"staleSeconds": int64(900),
		"staleMinutes": int64(15),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}
