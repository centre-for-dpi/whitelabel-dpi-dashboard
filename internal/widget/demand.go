package widget

import (
	"math"
	"slices"
	"sort"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
)

// The demand side of one service, in the drawer.
//
// Two widgets, because they answer two questions that survive independently: who
// is pulling this document, and how that compares with everything like it. Most
// APIs have one requestor or none, so a mix alone would leave the interesting
// cases blank — a service with no demand at all still has a category median and
// a category best to be read against, and that comparison is the whole finding.

// DemandBar is one share of a service's demand.
type DemandBar struct {
	Label string
	Count string
	Share string
	// Percent is the share as a number, for the bar's width.
	Percent float64
	// Highlight marks the bar the reader came here about.
	Highlight bool
}

// DemandChart is one reading of the same demand: by sector, or by named caller.
type DemandChart struct {
	Title string
	// NameHeader is what the first column of the table behind it is called.
	NameHeader string
	// Bars is what is drawn; TableBars is the complete list behind it. The
	// chart stays compact and the table stays whole, because the table is the
	// accessible route to the rest of it.
	Bars      []DemandBar
	TableBars []DemandBar
	Note      string
}

// DemandStat is one headline figure above the charts.
type DemandStat struct {
	Label string
	Value string
	Tone  string
}

// DemandView is the whole "who wants this" block.
type DemandView struct {
	Summary     string
	Stats       []DemandStat
	Charts      []DemandChart
	TableToggle string
	CountHeader string
	ShareHeader string
}

// PeerBar is one member of a peer comparison.
type PeerBar struct {
	Label     string
	Value     string
	Percent   float64
	Highlight bool
}

// PeerGroup is one measure compared across peers.
type PeerGroup struct {
	Title string
	Bars  []PeerBar
}

// PeerView is the "against its peers" block.
type PeerView struct {
	Title  string
	Groups []PeerGroup
	// Rule names the set "peer" means, so best and median can be read. Without
	// it the two bars are numbers with no stated population.
	Rule string
}

// callersOf are the requestors that name this service.
//
// A state-expanded issuer shares its Key across every region, so a requestor
// must match on region too — otherwise all twelve instances of a state register
// report one another's demand.
func callersOf(all []model.Service, sv model.Service) []model.Service {
	var out []model.Service
	for _, r := range all {
		if r.CallsKey != sv.Key {
			continue
		}
		if sv.Scope == scopeState && r.RegionID != sv.RegionID {
			continue
		}
		out = append(out, r)
	}
	return out
}

// siblingsOf are the requestors calling the same service as this one, including
// it: the set its share is a share of.
func siblingsOf(all []model.Service, sv model.Service, publisher *model.Service) []model.Service {
	var out []model.Service
	for _, r := range all {
		if r.CallsKey != sv.CallsKey {
			continue
		}
		if publisher != nil && publisher.Scope == scopeState && r.RegionID != sv.RegionID {
			continue
		}
		out = append(out, r)
	}
	return out
}

// publisherOf is the service a requestor calls.
func publisherOf(all []model.Service, sv model.Service) *model.Service {
	for i := range all {
		p := &all[i]
		if p.Key != sv.CallsKey || p.CallsKey != "" {
			continue
		}
		if p.Scope == scopeState && p.RegionID != sv.RegionID {
			continue
		}
		return p
	}
	return nil
}

// peersOf are the records this one is measured against: the same category (or,
// for a requestor, the same sector) at the same scope, excluding itself.
//
// Excluding itself matters. Including the open record would make "peer best"
// tautological for any category leader, and would contradict the count the rule
// line states.
func peersOf(all []model.Service, sv model.Service) []model.Service {
	requestor := sv.CallsKey != ""

	var out []model.Service
	for _, p := range all {
		if p.ID == sv.ID || p.Scope != sv.Scope || (p.CallsKey != "") != requestor {
			continue
		}
		if requestor {
			if p.SectorID != sv.SectorID {
				continue
			}
		} else if p.CategoryID != sv.CategoryID {
			continue
		}
		out = append(out, p)
	}
	return out
}

const scopeState = "state"

// demandDelta is how much a set's traffic grew over the window, in percent.
func demandDelta(list []model.Service, days int) (float64, bool) {
	var current, past int64
	for _, c := range list {
		h := c.History
		if len(h) == 0 {
			continue
		}
		current += h[len(h)-1].Volume
		past += h[max(0, len(h)-1-days)].Volume
	}
	if past == 0 {
		return 0, false
	}
	return math.Round(float64(current-past)/float64(past)*1000) / 10, true
}

// medianOf is the middle reading, or the mean of the middle two.
func medianOf(nums []int64) int64 {
	if len(nums) == 0 {
		return 0
	}
	s := slices.Clone(nums)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	m := len(s) / 2
	if len(s)%2 == 1 {
		return s[m]
	}
	return (s[m-1] + s[m] + 1) / 2
}

func maxOf(nums []int64) int64 {
	var out int64
	for _, n := range nums {
		out = max(out, n)
	}
	return out
}

// byVolumeDesc orders a list by successful requests, busiest first, breaking
// ties by id so the same data always draws the same chart.
func byVolumeDesc(list []model.Service) []model.Service {
	out := slices.Clone(list)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Metrics.Volume.Success, out[j].Metrics.Volume.Success
		if a != b {
			return a > b
		}
		return out[i].ID < out[j].ID
	})
	return out
}
