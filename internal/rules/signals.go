package rules

import (
	"slices"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
)

// Signal is one finding, ready to render.
//
// Params carries the numbers the finding is about, and RuleTermID the sentence
// describing how it was reached. Both travel together because a card that
// states a finding without stating its basis is an assertion the reader has no
// way to check.
type Signal struct {
	ID          string
	IconKey     string
	Tone        string
	TitleTermID string
	RuleTermID  string
	// ActionTermID overrides the shared action label, for a finding that leads
	// somewhere the shared wording would misdescribe.
	ActionTermID string
	Params       map[string]any

	// Filter is what the card's action applies to the leaderboard.
	Filter *config.SignalFilter

	// Focus is set instead of Filter when the finding is about one service, and
	// names the drawer tab that shows the evidence.
	Focus *Focus

	// Empty marks the "nothing to report" card.
	Empty bool
}

// TermRef is a parameter that names something rather than stating it: the rules
// layer knows a service by its term id and has no resolver, so it passes the id
// and the presentation layer translates it. Typed rather than conventional, so
// a parameter that should be translated cannot be one that merely looks like it.
type TermRef string

// Focus points at a single service and the drawer tab that explains the finding.
type Focus struct {
	ServiceID string
	Tab       string
}

// Signals evaluates every configured rule against the services in view.
//
// There is no blended score and no ranking model: each rule is evaluated
// independently over the same data, and each card prints the rule it applied.
// A dashboard that says "this needs attention" has to be able to say why.
//
// now is passed in rather than read, so that the same input always produces the
// same output.
func Signals(d config.Domain, services []model.Service, now time.Time) []Signal {
	var out []Signal

	for _, def := range d.Signals {
		if s, ok := evaluateSignal(def, d, services, now); ok {
			out = append(out, s)
		}
	}

	if len(out) == 0 {
		return []Signal{{
			ID:          d.SignalsEmpty.TermID,
			IconKey:     d.SignalsEmpty.IconKey,
			Tone:        d.SignalsEmpty.Tone,
			TitleTermID: d.SignalsEmpty.TermID,
			Empty:       true,
		}}
	}
	return out
}

func evaluateSignal(def config.Signal, d config.Domain, services []model.Service, now time.Time) (Signal, bool) {
	switch def.Kind {
	case config.SignalBelowTargetDays:
		return belowTargetDays(def, d, services)
	case config.SignalErrorRisingCategories:
		return errorRisingCategories(def, services)
	case config.SignalLongestOpenIncident:
		return longestOpenIncident(def, services, now)
	case config.SignalMaintenanceActive:
		return maintenanceActive(def, services)
	default:
		return Signal{}, false
	}
}

// card builds the renderable finding from its definition.
func card(def config.Signal, params map[string]any) Signal {
	return Signal{
		ID:           def.ID,
		IconKey:      def.IconKey,
		Tone:         def.Tone,
		TitleTermID:  def.TitleTermID,
		RuleTermID:   def.RuleTermID,
		ActionTermID: def.ActionTermID,
		Params:       params,
		Filter:       def.Filter,
	}
}

// belowTargetDays finds services that missed their availability target on every
// one of the last N days.
//
// Every day, not the average: a service that was perfect for six days and down
// for one has a different problem from one that has been quietly under target
// all week, and only the second is what this rule is looking for.
func belowTargetDays(def config.Signal, d config.Domain, services []model.Service) (Signal, bool) {
	target, ok := availabilityTarget(d)
	if !ok {
		return Signal{}, false
	}
	// Config rejects a non-positive window, but a hot reload races validation,
	// and a window of zero would otherwise mean "every day on record".
	if def.Days <= 0 {
		return Signal{}, false
	}

	count := 0
	for _, sv := range services {
		if !sv.Metrics.Availability.Valid {
			// A service with no readings cannot be shown to have missed
			// anything. Absence is not evidence.
			continue
		}
		window := lastDays(sv.History, def.Days)
		if len(window) < def.Days {
			continue
		}
		missedEveryDay := true
		for _, p := range window {
			if !p.Availability.Valid || p.Availability.Value >= target {
				missedEveryDay = false
				break
			}
		}
		if missedEveryDay {
			count++
		}
	}

	if count == 0 {
		return Signal{}, false
	}
	return card(def, map[string]any{"count": count, "days": def.Days}), true
}

// errorRisingCategories finds categories whose average error rate is higher
// than it was a month ago.
//
// It reports categories rather than services because a single service getting
// worse is that service's problem, while several across a category moving
// together usually points at something shared.
func errorRisingCategories(def config.Signal, services []model.Service) (Signal, bool) {
	type totals struct {
		current, past float64
		n             int
	}
	byCategory := map[string]*totals{}

	for _, sv := range services {
		if len(sv.History) == 0 {
			continue
		}
		t := byCategory[sv.CategoryID]
		if t == nil {
			t = &totals{}
			byCategory[sv.CategoryID] = t
		}
		last := len(sv.History) - 1
		t.current += sv.History[last].ErrorRate
		t.past += sv.History[max(0, last-30)].ErrorRate
		t.n++
	}

	var rising []string
	for id, t := range byCategory {
		// n is always at least one: an entry is only created when a service is
		// added to it. A margin, so that ordinary drift does not read as a trend.
		if t.current/float64(t.n) > t.past/float64(t.n)+0.05 {
			rising = append(rising, id)
		}
	}
	if len(rising) < def.MinCategory {
		return Signal{}, false
	}
	slices.Sort(rising) // deterministic: map iteration order is not

	s := card(def, map[string]any{"count": len(rising), "min": def.MinCategory})
	// The action narrows the leaderboard to exactly the categories the rule
	// found, rather than to a fixed list written in config.
	s.Filter = &config.SignalFilter{Category: rising}
	return s, true
}

// longestOpenIncident finds the incident that has been open the longest.
//
// The oldest open incident is the one most likely to have stopped being
// actively worked on, which is why it is worth surfacing over the newest.
func longestOpenIncident(def config.Signal, services []model.Service, now time.Time) (Signal, bool) {
	var (
		oldest    model.Incident
		serviceID string
		found     bool
	)

	for _, sv := range services {
		for _, inc := range sv.Incidents {
			if !inc.Open {
				continue
			}
			if !found || inc.OpenedAt.Before(oldest.OpenedAt) {
				oldest, serviceID, found = inc, sv.ID, true
			}
		}
	}
	if !found {
		return Signal{}, false
	}

	s := card(def, map[string]any{
		"seconds":   int64(now.Sub(oldest.OpenedAt).Seconds()),
		"serviceId": serviceID,
	})
	s.Filter = nil
	s.Focus = &Focus{ServiceID: serviceID, Tab: "incidents"}
	return s, true
}

// maintenanceActive counts services in a planned window right now.
func maintenanceActive(def config.Signal, services []model.Service) (Signal, bool) {
	count := 0
	for _, sv := range services {
		if sv.Status == model.StatusMaintenance {
			count++
		}
	}
	if count == 0 {
		return Signal{}, false
	}
	return card(def, map[string]any{"count": count}), true
}

// availabilityTarget finds the configured target the below-target rule measures
// against. A deployment that removes the metric or its target removes the rule
// along with it, rather than getting a rule silently measuring against zero.
func availabilityTarget(d config.Domain) (float64, bool) {
	for _, m := range d.Metrics {
		if m.Field == config.FieldAvailability && m.Target != nil {
			return *m.Target, true
		}
	}
	return 0, false
}

// lastDays returns the final n points of a history, or all of them when there
// are fewer. Callers guarantee n is positive.
func lastDays(h []model.HistoryPoint, n int) []model.HistoryPoint {
	if len(h) <= n {
		return h
	}
	return h[len(h)-n:]
}
