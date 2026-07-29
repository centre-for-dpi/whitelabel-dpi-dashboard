package rules

import (
	"math"
	"slices"
	"sort"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
)

// Opportunities evaluates the demand-side rules.
//
// The mirror of Signals, and deliberately the same shape: each rule is
// evaluated independently over the same data, and each card prints the rule it
// applied. What differs is the question. An attention signal asks who is not
// getting a service; an opportunity asks what is being issued that nobody is
// consuming — a document published at scale with four requestors, a live API
// with none at all, a whole category resting on one caller.
//
// These are findings about supply and demand together, so both populations are
// passed: issuers, and the requestors that name one of them in CallsKey.
func Opportunities(d config.Domain, issuers, requestors []model.Service, now time.Time) []Signal {
	callers := callersByIssuer(issuers, requestors)

	var out []Signal
	for _, def := range d.Opportunities {
		if s, ok := evaluateOpportunity(def, issuers, requestors, callers); ok {
			out = append(out, s)
		}
	}

	if len(out) == 0 {
		empty := d.OpportunitiesNone
		return []Signal{{
			ID:          empty.TermID,
			IconKey:     empty.IconKey,
			Tone:        empty.Tone,
			TitleTermID: empty.TermID,
			Empty:       true,
		}}
	}
	return out
}

func evaluateOpportunity(def config.Signal, issuers, requestors []model.Service, callers map[string][]model.Service) (Signal, bool) {
	switch def.Kind {
	case config.OpportunityIssuedAtScaleRarelyRequested:
		return issuedAtScaleRarelyRequested(def, issuers, callers)
	case config.OpportunitySingleRequestorCategories:
		return singleRequestorCategories(def, requestors)
	case config.OpportunityZeroRequestors:
		return zeroRequestors(def, issuers, callers)
	case config.OpportunityFastestGrowingDemand:
		return fastestGrowingDemand(def, issuers, callers)
	default:
		return Signal{}, false
	}
}

// callersByIssuer indexes requestors by the service they call.
//
// A state-expanded issuer shares its Key across every region, so a requestor
// must match on region too — otherwise all twelve instances of a state register
// report one another's demand and every one of them looks twelve times more
// popular than it is. A national issuer has no region to match against.
func callersByIssuer(issuers, requestors []model.Service) map[string][]model.Service {
	byKey := map[string][]model.Service{}
	for _, r := range requestors {
		if r.CallsKey == "" {
			continue
		}
		byKey[r.CallsKey] = append(byKey[r.CallsKey], r)
	}

	out := make(map[string][]model.Service, len(issuers))
	for _, sv := range issuers {
		for _, r := range byKey[sv.Key] {
			if sv.Scope == "state" && r.RegionID != sv.RegionID {
				continue
			}
			out[sv.ID] = append(out[sv.ID], r)
		}
	}
	return out
}

// issuedAtScaleRarelyRequested finds documents in the top quartile of issuance
// and the bottom quartile of requestor count.
//
// Quartiles rather than fixed thresholds, because "a lot of traffic" and "few
// requestors" only mean anything relative to the rest of the estate — the same
// absolute numbers describe a busy document in one country and a quiet one in
// another.
func issuedAtScaleRarelyRequested(def config.Signal, issuers []model.Service, callers map[string][]model.Service) (Signal, bool) {
	if len(issuers) == 0 {
		return Signal{}, false
	}

	vols := make([]float64, 0, len(issuers))
	reqs := make([]float64, 0, len(issuers))
	for _, sv := range issuers {
		vols = append(vols, float64(sv.Metrics.Volume.Success))
		reqs = append(reqs, float64(len(callers[sv.ID])))
	}
	sort.Float64s(vols)
	sort.Float64s(reqs)

	volQ3, reqQ1 := quantile(vols, 0.75), quantile(reqs, 0.25)

	var ids []string
	for _, sv := range issuers {
		if float64(sv.Metrics.Volume.Success) >= volQ3 && float64(len(callers[sv.ID])) <= reqQ1 {
			ids = append(ids, sv.ID)
		}
	}
	if len(ids) == 0 {
		return Signal{}, false
	}

	s := card(def, map[string]any{"count": len(ids)})
	s.Filter = withIDs(def.Filter, ids)
	return s, true
}

// singleRequestorCategories finds categories where one requestor accounts for
// nearly all of the pulls.
//
// Concentration is fragility: a category served by one caller loses the whole
// category when that caller stops, and nothing in an availability figure says
// so.
func singleRequestorCategories(def config.Signal, requestors []model.Service) (Signal, bool) {
	if def.Concentration <= 0 {
		return Signal{}, false
	}

	byCategory := map[string][]model.Service{}
	for _, r := range requestors {
		byCategory[r.CategoryID] = append(byCategory[r.CategoryID], r)
	}

	var hits []string
	for category, list := range byCategory {
		var total, top int64
		for _, r := range list {
			total += r.Metrics.Volume.Success
			top = max(top, r.Metrics.Volume.Success)
		}
		if total == 0 {
			continue
		}
		if float64(top)/float64(total)*100 >= def.Concentration {
			hits = append(hits, category)
		}
	}
	if len(hits) == 0 {
		return Signal{}, false
	}
	// Sorted, because a map decides its own order and a card that reordered
	// itself between two identical loads would look like a change in the data.
	slices.Sort(hits)

	s := card(def, map[string]any{"count": len(hits), "pct": def.Concentration})
	f := config.SignalFilter{Category: hits}
	if def.Filter != nil {
		f = *def.Filter
		f.Category = hits
	}
	s.Filter = &f
	return s, true
}

// zeroRequestors finds APIs that have been live and healthy long enough to have
// been adopted, and have not been.
//
// The days test is the whole point: an API published last week with no
// requestors is not a finding, it is a new API.
func zeroRequestors(def config.Signal, issuers []model.Service, callers map[string][]model.Service) (Signal, bool) {
	if def.Days <= 0 {
		return Signal{}, false
	}

	var ids []string
	for _, sv := range issuers {
		if sv.Status != model.StatusOperational || len(sv.History) < def.Days {
			continue
		}
		if len(callers[sv.ID]) == 0 {
			ids = append(ids, sv.ID)
		}
	}
	if len(ids) == 0 {
		return Signal{}, false
	}

	s := card(def, map[string]any{"count": len(ids), "days": def.Days})
	s.Filter = withIDs(def.Filter, ids)
	return s, true
}

// fastestGrowingDemand names the issuer whose requestors are growing fastest.
//
// The mean rise across its callers rather than the total, so a document with
// two requestors that both doubled beats one with twenty that grew a percent —
// growth in demand, not the size of the demand already there.
func fastestGrowingDemand(def config.Signal, issuers []model.Service, callers map[string][]model.Service) (Signal, bool) {
	if def.Days <= 0 {
		return Signal{}, false
	}

	var best model.Service
	bestRise, found := 0.0, false

	for _, sv := range issuers {
		list := callers[sv.ID]
		if len(list) == 0 || sv.Status != model.StatusOperational {
			continue
		}
		var sum float64
		for _, r := range list {
			sum += relativeRise(r.History, def.Days)
		}
		if rise := sum / float64(len(list)); rise > bestRise {
			bestRise, best, found = rise, sv, true
		}
	}
	if !found {
		return Signal{}, false
	}

	s := card(def, map[string]any{"name": TermRef(best.NameTermID), "days": def.Days})
	s.Filter = nil
	// A finding about one service points at the service, not at a filter that
	// would show it beside services the finding is not about.
	s.Focus = &Focus{ServiceID: best.ID, Tab: def.FocusTab}
	return s, true
}

// relativeRise is how much a series grew over the last n days, as a proportion
// of where it started.
func relativeRise(history []model.HistoryPoint, n int) float64 {
	if len(history) == 0 {
		return 0
	}
	last := history[len(history)-1].Volume
	past := history[max(0, len(history)-1-n)].Volume
	if past <= 0 {
		return 0
	}
	return float64(last-past) / float64(past)
}

// quantile interpolates between the two nearest readings, so a small estate
// does not snap its quartiles to whichever two services it happens to have.
func quantile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := float64(len(sorted)-1) * p
	lo, hi := int(math.Floor(i)), int(math.Ceil(i))
	return sorted[lo] + (sorted[hi]-sorted[lo])*(i-float64(lo))
}

// withIDs attaches an explicit set of services to a card's filter.
//
// Some findings are about a set with nothing else in common — no shared status,
// no shared category — and the only honest filter is the set itself.
func withIDs(base *config.SignalFilter, ids []string) *config.SignalFilter {
	f := config.SignalFilter{}
	if base != nil {
		f = *base
	}
	f.IDs = ids
	return &f
}
