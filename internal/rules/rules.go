// Package rules decides what the numbers mean.
//
// It holds the two judgements the dashboard makes — what status a service is
// in, and which signals are worth surfacing — and both read the same thresholds
// the interface publishes. That is the point: the rule shown on screen is
// provably the rule that was applied, because there is only one copy of it.
//
// Everything here is pure. Status is a function of an observation and a
// threshold set; a signal is a function of the scoped service list and a clock
// reading passed in by the caller.
package rules

import (
	"math"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
)

// Evaluate returns the status of one observation.
//
// Rules are applied in the order the configuration publishes, first match wins.
// Walking the configured order rather than a hardcoded one is what keeps the
// dashboard's own explanation of itself honest: if a deployment reorders the
// rules, the reported status changes to match.
//
// Anything that matches no rule is unknown, not healthy. Silence is not
// evidence of good standing.
func Evaluate(m model.Metrics, maint model.Maintenance, th config.Thresholds) model.Status {
	for _, name := range th.EvaluationOrder {
		s := model.Status(name)
		if matches(s, m, maint, th.Values) {
			return s
		}
	}
	return model.StatusUnknown
}

func matches(s model.Status, m model.Metrics, maint model.Maintenance, v config.ThresholdValues) bool {
	switch s {
	case model.StatusMaintenance:
		return maint.Active

	case model.StatusUnknown:
		// Either nothing was reported, or what was reported is too old to stand
		// behind.
		return !m.Availability.Valid || m.StaleSeconds > v.StaleSecondsAbove

	case model.StatusMajor:
		return belowTarget(m.Availability, v.MajorAvailBelow) || m.ErrorRate > v.MajorErrAbove

	case model.StatusPartial:
		return belowTarget(m.Availability, v.PartialAvailBelow) || m.ErrorRate > v.PartialErrAbove

	case model.StatusOperational:
		return true

	default:
		return false
	}
}

// belowTarget guards every availability comparison against an absent reading.
//
// Without this, a deployment that ordered major before unknown would see a
// missing reading treated as 0% and reported as a total outage. Absence is not
// zero, and the difference is the difference between "we cannot tell" and "it
// is down".
func belowTarget(v model.OptFloat, limit float64) bool {
	return v.Valid && v.Value < limit
}

// ProseParams supplies the threshold numbers to the published rule text.
//
// The interface prints sentences like "under 99.5% availability on each of the
// last 7 days". Those numbers come from here rather than from the translation,
// so a deployment that changes a threshold does not have to remember to change
// eight locale files to match.
func ProseParams(v config.ThresholdValues) map[string]any {
	return map[string]any{
		"majorAvail":   v.MajorAvailBelow,
		"majorErr":     v.MajorErrAbove,
		"partialAvail": v.PartialAvailBelow,
		"partialErr":   v.PartialErrAbove,

		// Both forms of the staleness limit, because "15 minutes" reads better
		// in a sentence than "900 seconds" while some languages will want the
		// other. Which to use is the translator's call, not this package's.
		"staleSeconds": v.StaleSecondsAbove,
		"staleMinutes": v.StaleSecondsAbove / 60,
	}
}

// Trend measures how a field has moved over the last days of history.
func Trend(h []model.HistoryPoint, field string, days int) model.Trend {
	if len(h) < 2 {
		return model.Trend{Direction: model.DirectionFlat, PeriodDays: int32(days)}
	}

	last := len(h) - 1
	past := max(0, last-days)

	cur, curOK := fieldValue(h[last], field)
	prev, prevOK := fieldValue(h[past], field)
	if !curOK || !prevOK {
		return model.Trend{Direction: model.DirectionFlat, PeriodDays: int32(days)}
	}

	delta := round2(cur - prev)

	// Rates wobble, so a fixed band keeps hundredths of a percent out of the
	// trend column. Traffic needs a proportional band instead: 0.02 requests is
	// nothing at any scale.
	band := 0.02
	if field == config.FieldVolume {
		band = math.Abs(cur) * 0.005
	}

	dir := model.DirectionFlat
	switch {
	case math.Abs(delta) < band:
	case delta > 0:
		dir = model.DirectionUp
	default:
		dir = model.DirectionDown
	}

	return model.Trend{Delta: delta, Direction: dir, PeriodDays: int32(days)}
}

// fieldValue reads one metric out of a history point. The second result is
// false when the point carries no reading for that field, which is different
// from a reading of zero.
func fieldValue(p model.HistoryPoint, field string) (float64, bool) {
	switch field {
	case config.FieldAvailability:
		return p.Availability.Value, p.Availability.Valid
	case config.FieldDowntime:
		// Derived, so the trend is availability's with the sign flipped: a fall
		// in availability is a rise in downtime, and the arrow has to agree with
		// the number it sits beside.
		return config.Complement(p.Availability.Value), p.Availability.Valid
	case config.FieldErrorRate:
		return p.ErrorRate, true
	case config.FieldLatencyP50:
		return float64(p.LatencyP50), true
	case config.FieldVolume:
		return float64(p.Volume), true
	default:
		return 0, false
	}
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// Finalise fills in everything the dashboard derives rather than accepts.
//
// Status, trends and rank movement are never reported by an upstream: they are
// computed here, from the observations, against the thresholds the interface
// publishes. Every driver runs incoming data through this, so a new source
// cannot accidentally skip it and produce services whose status contradicts
// their own numbers.
//
// now is a parameter so the result is a function of its inputs alone.
func Finalise(services []model.Service, d config.Domain, now time.Time) []model.Service {
	trendDays := defaultTrendDays(d)

	out := make([]model.Service, len(services))
	for i, sv := range services {
		sv.Status = Evaluate(sv.Metrics, sv.Maintenance, d.Thresholds)

		sv.Trends = make(map[string]model.Trend, 4)
		for _, field := range []string{
			config.FieldAvailability, config.FieldErrorRate,
			config.FieldLatencyP50, config.FieldVolume,
		} {
			sv.Trends[field] = Trend(sv.History, field, trendDays)
		}

		if sv.ObservedAt.IsZero() {
			sv.ObservedAt = now
		}
		out[i] = sv
	}
	return out
}

// defaultTrendDays is the window trends are measured over when nothing else
// says otherwise: the deployment's own default period.
func defaultTrendDays(d config.Domain) int {
	for _, p := range d.Periods {
		if p.ID == d.DefaultPeriod {
			return p.Days
		}
	}
	return 30
}
