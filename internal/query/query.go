// Package query narrows, ranks and orders the service list.
//
// Two different senses of time run through the dashboard, and keeping them
// straight is what makes it readable:
//
//	STATUS is now.       "Is this working at the moment?" A planned window or a
//	                     reading from ten minutes ago answers that; an average
//	                     over three months does not.
//	RANK and METRICS are over the reader's selected window. "How has this been
//	                     doing?" A service that is briefly down has not lost the
//	                     standing it earned over ninety days.
//
// So changing the period re-ranks the leaderboard and re-reads every figure in
// it, while leaving the verdict at the top of the page alone. Anything else
// would be incoherent: a leaderboard ordered by ninety-day standing but showing
// this minute's availability invites the reader to conclude the sort is broken.
//
// The package is free of any notion of language. Text search and name ordering
// need resolved, localised strings, and those come from a Labeler the caller
// supplies.
package query

import (
	"math"
	"slices"
	"strings"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
)

// Labeler resolves the display strings this package needs.
//
// Compare is a parameter rather than strings.Compare because byte order is not
// alphabetical order in most of the world: "ä" sorts after "z" by bytes and
// next to "a" in the languages that use it.
type Labeler interface {
	Name(model.Service) string
	Haystack(model.Service) string
	Compare(a, b string) int
}

// View is the frame the reader is looking through: which window, which domain,
// and how to resolve labels. Every operation that depends on any of the three
// hangs off it, so none of them can be forgotten at a call site.
type View struct {
	Domain  config.Domain
	Labeler Labeler

	// Days is the selected window. Zero means "use whatever is current",
	// which is what a deployment reporting no history gets.
	Days int
}

// DaysFor resolves a configured period id to its window in days.
//
// Unknown ids fall back to the default period rather than to zero, so a stale
// bookmark or a hand-edited URL still produces a sensible page.
func DaysFor(d config.Domain, periodID string) int {
	for _, p := range d.Periods {
		if p.ID == periodID {
			return p.Days
		}
	}
	for _, p := range d.Periods {
		if p.ID == d.DefaultPeriod {
			return p.Days
		}
	}
	return 0
}

// Standing is how a service has performed across a window.
//
// It is what the leaderboard shows and what the ranking is built on. Samples
// records how many daily buckets contributed, which is what lets the interface
// distinguish "99.9% over ninety days" from "99.9%, once, yesterday".
type Standing struct {
	Availability model.OptFloat
	ErrorRate    float64
	LatencyP50   int32
	Volume       int64
	Days         int
	Samples      int

	// Current is set when there was no history to average and the figures are
	// the latest reading instead. The interface says so rather than implying a
	// window it does not have.
	Current bool
}

// Standing summarises a service over the view's window.
//
// Rates are averaged and traffic is summed, because that is what each one
// means over a period: "99.4% available across the month" and "2.9 million
// requests during it".
//
// Days with no reading are skipped rather than counted as zero. A gap in
// reporting is not an outage, and averaging it in as one would quietly punish a
// service for its collector having been offline.
func (v View) Standing(sv model.Service) Standing {
	window := lastDays(sv.History, v.Days)

	if len(window) == 0 {
		// Nothing to average — a deployment pushing bare snapshots with no
		// history lands here, and gets the latest reading rather than nothing.
		return Standing{
			Availability: sv.Metrics.Availability,
			ErrorRate:    sv.Metrics.ErrorRate,
			LatencyP50:   sv.Metrics.LatencyP50,
			Volume:       sv.Metrics.Volume.Total,
			Days:         v.Days,
			Current:      true,
		}
	}

	var (
		availSum  float64
		availDays int
		errSum    float64
		latSum    int64
		volSum    int64
	)
	for _, p := range window {
		if p.Availability.Valid {
			availSum += p.Availability.Value
			availDays++
		}
		errSum += p.ErrorRate
		latSum += int64(p.LatencyP50)
		volSum += p.Volume
	}

	s := Standing{
		ErrorRate:  errSum / float64(len(window)),
		LatencyP50: int32(latSum / int64(len(window))),
		Volume:     volSum,
		Days:       v.Days,
		Samples:    len(window),
	}
	if availDays > 0 {
		s.Availability = model.Float(availSum / float64(availDays))
	}
	return s
}

// lastDays returns the final n points, or all of them when there are fewer.
// A non-positive window means "everything on record".
func lastDays(h []model.HistoryPoint, n int) []model.HistoryPoint {
	if n <= 0 || len(h) <= n {
		return h
	}
	return h[len(h)-n:]
}

// Filter is the set of narrowing choices a reader has made.
type Filter struct {
	Statuses   []string
	Categories []string
	Search     string
}

// Active reports whether the filter narrows anything, which decides whether the
// interface offers to clear it.
func (f Filter) Active() bool {
	return len(f.Statuses) > 0 || len(f.Categories) > 0 || strings.TrimSpace(f.Search) != ""
}

// Count reports how many distinct kinds of narrowing are applied. It is the
// number shown beside "Filters" when they are collapsed on a small screen.
func (f Filter) Count() int {
	n := 0
	if len(f.Statuses) > 0 {
		n++
	}
	if len(f.Categories) > 0 {
		n++
	}
	if strings.TrimSpace(f.Search) != "" {
		n++
	}
	return n
}

// Sort keys. Anything else falls back to rank.
const (
	SortRank   = "rank"
	SortName   = "name"
	SortStatus = "status"
)

// Sort directions.
const (
	Asc  = "asc"
	Desc = "desc"
)

// Scope narrows to one scope, and within a sub-national scope optionally to one
// region.
//
// The national and sub-national views answer different questions, so a service
// belongs to one or the other rather than appearing in both.
func (v View) Scope(services []model.Service, scope, region string) []model.Service {
	out := make([]model.Service, 0, len(services))
	for _, sv := range services {
		if sv.Scope != scope {
			continue
		}
		if region != "" && !v.isAllRegions(region) && sv.RegionID != region {
			continue
		}
		out = append(out, sv)
	}
	return out
}

// isAllRegions reports whether a region selection means "no region filter".
// The region belonging to the default scope doubles as the "all" choice, since
// a sub-national view has no use for the national bucket.
func (v View) isAllRegions(region string) bool {
	for _, r := range v.Domain.Taxonomy.Regions {
		if r.ID == region {
			return r.Scope == v.Domain.DefaultScope
		}
	}
	return false
}

// Rank orders services by standing over the window: best first.
//
// Status deliberately does not contribute. Rank answers "how has this been
// doing", and a service having a bad afternoon has not lost the standing it
// earned over the preceding weeks. Services with no availability reading in the
// window cannot claim good standing, so they rank last — but they are not
// scored as failures either.
//
// Because the standing is windowed, changing the period genuinely re-orders the
// board: a service that was poor last month and excellent this week leads on
// 7 days and trails on 90.
func (v View) Rank(services []model.Service) []model.Service {
	standings := make(map[string]Standing, len(services))
	for _, sv := range services {
		standings[sv.ID] = v.Standing(sv)
	}

	out := slices.Clone(services)
	slices.SortStableFunc(out, func(a, b model.Service) int {
		as, bs := standings[a.ID], standings[b.ID]

		if sa, sb := as.Attention(), bs.Attention(); sa != sb {
			if sa > sb {
				return -1 // needs attention more, so ranks first
			}
			return 1
		}
		return v.Labeler.Compare(v.Labeler.Name(a), v.Labeler.Name(b))
	})
	return out
}

// Ranks maps service id to its 1-based position in a ranked list.
//
// Rank is computed over everything in scope, not over what survives a filter,
// so that "rank 4" means the same thing however the reader has narrowed the
// view. A rank that shuffled as you typed in the search box would be useless.
func Ranks(ranked []model.Service) map[string]int {
	out := make(map[string]int, len(ranked))
	for i, sv := range ranked {
		out[sv.ID] = i + 1
	}
	return out
}

// Apply narrows a list by the reader's choices.
func (v View) Apply(services []model.Service, f Filter) []model.Service {
	needle := strings.ToLower(strings.TrimSpace(f.Search))

	out := make([]model.Service, 0, len(services))
	for _, sv := range services {
		if len(f.Statuses) > 0 && !slices.Contains(f.Statuses, string(sv.Status)) {
			continue
		}
		if len(f.Categories) > 0 && !slices.Contains(f.Categories, sv.CategoryID) {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(v.Labeler.Haystack(sv)), needle) {
			continue
		}
		out = append(out, sv)
	}
	return out
}

// Order sorts a list by a column.
//
// Metric columns sort on the windowed standing, not the latest reading, so the
// order agrees with the figures displayed beside it.
func (v View) Order(services []model.Service, key, dir string, ranks map[string]int) []model.Service {
	sign := 1
	if dir == Desc {
		sign = -1
	}

	cmp := v.comparator(key, ranks)
	out := slices.Clone(services)
	// Stable, so that services which tie on the chosen column keep the order
	// they already had rather than shuffling between requests.
	slices.SortStableFunc(out, func(a, b model.Service) int { return cmp(a, b) * sign })
	return out
}

func (v View) comparator(key string, ranks map[string]int) func(a, b model.Service) int {
	switch key {
	case SortName:
		return func(a, b model.Service) int {
			return v.Labeler.Compare(v.Labeler.Name(a), v.Labeler.Name(b))
		}

	case SortStatus:
		return func(a, b model.Service) int {
			return v.Domain.StatusModel.Severity[string(a.Status)] -
				v.Domain.StatusModel.Severity[string(b.Status)]
		}

	case SortRank:
		return func(a, b model.Service) int { return ranks[a.ID] - ranks[b.ID] }
	}

	// A metric column, if the deployment declared one under this id.
	for _, m := range v.Domain.Metrics {
		if m.ID != key {
			continue
		}
		field := m.Field
		return func(a, b model.Service) int {
			av := v.Standing(a).value(field)
			bv := v.Standing(b).value(field)
			switch {
			case av < bv:
				return -1
			case av > bv:
				return 1
			default:
				return 0
			}
		}
	}

	return func(a, b model.Service) int { return ranks[a.ID] - ranks[b.ID] }
}

// Attention is the published ranking rule: how much of this service is failing
// the people using it.
//
// Downtime and error rate are both percentages of the same population of
// requests, so adding them compares like with like — a service down for 2% of
// the window and erroring on 1% of what got through is failing 3% of the people
// who came to it. That is the number, and it is why rank 1 is the service that
// needs attention most rather than the one performing best. A leaderboard whose
// top entry is the thing already working asks the reader to scroll to find the
// problem.
//
// A service with no availability reading at all ranks first, ahead of every
// measured failure. Not because it is necessarily worse, but because it cannot be
// verified: an unmonitored service is the one case where nobody can say whether
// citizens are being served, and that is the most urgent thing on the page.
// Scoring it zero would sort it among the healthy, which is the specific mistake
// model.OptFloat exists to prevent.
func (s Standing) Attention() float64 {
	if !s.Availability.Valid {
		return math.Inf(1)
	}
	return config.Complement(s.Availability.Value) + s.ErrorRate
}

// value reads a sortable number out of a standing.
//
// An absent availability sorts below every real reading rather than as zero, so
// ascending order puts "not reported" at the bottom instead of implying a total
// outage at the top.
func (s Standing) value(field string) float64 {
	switch field {
	case config.FieldAvailability:
		if !s.Availability.Valid {
			return -1
		}
		return s.Availability.Value
	case config.FieldDowntime:
		// Sorted the same way round as availability — an absent reading below
		// every real one — so "not reported" does not masquerade as a total
		// outage at the top of a descending sort.
		if !s.Availability.Valid {
			return -1
		}
		return config.Complement(s.Availability.Value)
	case config.FieldErrorRate:
		return s.ErrorRate
	case config.FieldLatencyP50:
		return float64(s.LatencyP50)
	case config.FieldVolume:
		return float64(s.Volume)
	default:
		return 0
	}
}

// DefaultDirection is the direction a column sorts on first click.
//
// Rank and name read naturally forwards; a measurement is nearly always being
// examined for its extreme, so those open at the worst end.
func DefaultDirection(key string) string {
	if key == SortRank || key == SortName {
		return Asc
	}
	return Desc
}

// Counts tallies services by status.
//
// Deliberately not windowed: status is a statement about now, and averaging it
// would produce a verdict bar that answers a question nobody asked. Statuses
// with no services are included so the legend shows a complete picture rather
// than only what happens to be present today.
func (v View) Counts(services []model.Service) map[string]int {
	out := make(map[string]int, len(v.Domain.StatusModel.Order))
	for _, s := range v.Domain.StatusModel.Order {
		out[s] = 0
	}
	for _, sv := range services {
		out[string(sv.Status)]++
	}
	return out
}
