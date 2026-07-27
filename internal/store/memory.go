package store

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
)

// Memory is a store that keeps everything in the process.
//
// It is the default, and the right choice more often than it sounds: a
// deployment whose upstream serves its own history has nothing to persist, and
// a stateless platform deploy has nowhere to persist it to. What it costs is
// that locally rolled-up history does not survive a restart.
//
// It is also the reference implementation. The contract suite in storetest runs
// against it and against every SQL backend, so "what the store does" is defined
// by something small enough to read rather than by whichever database happened
// to be tested first.
type Memory struct {
	mu       sync.RWMutex
	services map[string]model.Service
	order    []string
	history  map[string][]model.HistoryPoint
	samples  map[string][]model.Sample
	updated  time.Time
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{
		services: map[string]model.Service{},
		history:  map[string][]model.HistoryPoint{},
		samples:  map[string][]model.Sample{},
	}
}

// Migrate is a no-op: there is no schema.
func (m *Memory) Migrate(context.Context) error { return nil }

// Close is a no-op: there is no connection.
func (m *Memory) Close() error { return nil }

// Save records the current state and appends a sample per service.
func (m *Memory) Save(_ context.Context, snap model.Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, sv := range snap.Services {
		if _, seen := m.services[sv.ID]; !seen {
			// Insertion order is preserved so Load returns services in a
			// stable sequence rather than a map's arbitrary one.
			m.order = append(m.order, sv.ID)
		}

		// History travels separately from the service record: an upstream that
		// stops sending history should not erase what has already been stored.
		if len(sv.History) > 0 {
			m.history[sv.ID] = MergeHistory(m.history[sv.ID], sv.History)
		}
		sv.History = nil
		m.services[sv.ID] = sv
	}

	for _, s := range samplesFrom(snap) {
		m.samples[s.ServiceID] = append(m.samples[s.ServiceID], s)
	}

	m.updated = snap.GeneratedAt
	return nil
}

// Load reconstructs the last known state.
func (m *Memory) Load(_ context.Context, retentionDays int) (model.Snapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cutoff := retentionCutoff(m.updated, retentionDays)

	out := model.Snapshot{
		Services:    make([]model.Service, 0, len(m.order)),
		GeneratedAt: m.updated,
	}
	for _, id := range m.order {
		sv := m.services[id]
		sv.History = trimHistory(slices.Clone(m.history[id]), cutoff)
		out.Services = append(out.Services, sv)
	}
	return out, nil
}

// Rollup folds each service's raw samples into daily buckets.
func (m *Memory) Rollup(_ context.Context, through time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	limit := Day(through)
	for id, samples := range m.samples {
		byDay := map[time.Time][]model.Sample{}
		for _, s := range samples {
			day := Day(s.At)
			if day.After(limit) {
				continue
			}
			byDay[day] = append(byDay[day], s)
		}

		var rolled []model.HistoryPoint
		for _, group := range byDay {
			rolled = append(rolled, RollupSamples(group))
		}
		if len(rolled) > 0 {
			// Rolled-up buckets lose to a bucket the upstream supplied: an
			// upstream reporting its own history knows more about its own day
			// than the dashboard's sparse sampling of it does.
			m.history[id] = mergeExisting(m.history[id], rolled)
		}
	}
	return nil
}

// Prune discards rolled-up samples and expired buckets.
func (m *Memory) Prune(_ context.Context, sampleCutoff, dayCutoff time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, samples := range m.samples {
		kept := samples[:0]
		for _, s := range samples {
			if !s.At.Before(sampleCutoff) {
				kept = append(kept, s)
			}
		}
		if len(kept) == 0 {
			delete(m.samples, id)
			continue
		}
		m.samples[id] = kept
	}

	for id, points := range m.history {
		trimmed := trimHistory(points, Day(dayCutoff))
		if len(trimmed) == 0 {
			delete(m.history, id)
			continue
		}
		m.history[id] = trimmed
	}
	return nil
}

// mergeExisting adds points only where a day is not already recorded.
//
// The opposite precedence to MergeHistory, and deliberately so: MergeHistory is
// for what an upstream reports, which is authoritative, while this is for what
// the dashboard inferred from its own sampling, which is not.
func mergeExisting(existing, rolled []model.HistoryPoint) []model.HistoryPoint {
	present := make(map[time.Time]bool, len(existing))
	for _, p := range existing {
		present[Day(p.Day)] = true
	}

	out := slices.Clone(existing)
	for _, p := range rolled {
		if present[Day(p.Day)] {
			continue
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b model.HistoryPoint) int { return a.Day.Compare(b.Day) })
	return out
}

// retentionCutoff is the oldest day still worth returning.
func retentionCutoff(from time.Time, retentionDays int) time.Time {
	if retentionDays <= 0 {
		// No retention configured means no trimming, rather than trimming
		// everything — the latter would silently empty every chart.
		return time.Time{}
	}
	if from.IsZero() {
		from = time.Now()
	}
	return Day(from).AddDate(0, 0, -retentionDays+1)
}
