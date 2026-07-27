// Package persist puts the store into the data path without any driver knowing
// about it.
//
// A Recorder wraps whichever source is configured. It watches for new snapshots,
// writes them to the store, folds raw samples into daily buckets on a timer, and
// serves back a snapshot with the stored history spliced in. The pull driver,
// the push endpoint and the seed generator are all unchanged and all benefit:
// none of them has a line about persistence in it, and a fourth driver would
// need none either.
//
// The important consequence is what a restart looks like. Without this, a
// dashboard whose upstream reports only a current reading comes back with empty
// charts and rebuilds ninety days of history from scratch — which takes ninety
// days. With it, the charts are there before the first poll completes.
package persist

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/rules"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/store"
)

// Source is the part of a driver a Recorder needs: the current state.
type Source interface {
	Snapshot() model.Snapshot
}

// Options configure a Recorder.
type Options struct {
	Source  Source
	Store   store.Store
	Storage config.Storage
	Domain  config.Domain
	Log     *slog.Logger
	// Clock is injected so the rollup boundary is testable.
	Clock func() time.Time
	// SyncInterval is how often the source is checked for a new snapshot.
	// Defaults to five seconds.
	SyncInterval time.Duration
}

// Recorder is a Source that persists what it sees.
type Recorder struct {
	src     Source
	store   store.Store
	storage config.Storage
	domain  config.Domain
	log     *slog.Logger
	clock   func() time.Time
	sync    time.Duration

	// merged is what readers get. Recomputed on change rather than on read,
	// because Snapshot is on the render path and splicing history per request
	// would put the whole catalogue's worth of merging in front of every page.
	merged atomic.Pointer[model.Snapshot]
	// stored is the history read back from the database, by service id.
	stored atomic.Pointer[map[string][]model.HistoryPoint]
	// lastSeen is the GeneratedAt of the snapshot last persisted, so an
	// unchanged source is not rewritten every tick.
	lastSeen atomic.Int64
}

// New returns a Recorder. Call Restore before serving and Run to keep it fresh.
func New(o Options) *Recorder {
	if o.Clock == nil {
		o.Clock = time.Now
	}
	if o.SyncInterval <= 0 {
		// Short, because it only compares a timestamp. The store is off the
		// read path, so this is about how quickly an update is made durable,
		// not about how quickly it is visible.
		o.SyncInterval = 5 * time.Second
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}

	r := &Recorder{
		src: o.Source, store: o.Store, storage: o.Storage,
		domain: o.Domain, log: o.Log, clock: o.Clock, sync: o.SyncInterval,
	}
	r.stored.Store(&map[string][]model.HistoryPoint{})
	return r
}

// Snapshot returns the current state with stored history spliced in.
func (r *Recorder) Snapshot() model.Snapshot {
	if merged := r.merged.Load(); merged != nil {
		return *merged
	}
	// Before the first sync. Returning the raw source beats returning nothing:
	// a dashboard with no history is worth more than a blank one.
	return r.src.Snapshot()
}

// Restore reads the stored state and makes it the starting point.
//
// A restore failure is logged, not returned. A dashboard that refuses to start
// because its history database is unreachable is worse than one that starts
// with empty charts and fills them in: the live status — the thing the page
// exists to answer — does not depend on stored history at all.
func (r *Recorder) Restore(ctx context.Context) {
	snap, err := r.store.Load(ctx, r.storage.History.RetentionDays)
	if err != nil {
		r.log.Error("could not restore stored history; charts will start empty and refill",
			"error", err, "driver", r.storage.Driver)
		return
	}

	history := make(map[string][]model.HistoryPoint, len(snap.Services))
	for _, sv := range snap.Services {
		if len(sv.History) > 0 {
			history[sv.ID] = sv.History
		}
	}
	r.stored.Store(&history)

	live := r.src.Snapshot()
	if len(live.Services) == 0 {
		// Nothing live yet — a push deployment before its first payload, or a
		// poller before its first success. What was stored is the best account
		// of the world available, so serve it.
		live = snap
	}
	r.publish(live)

	r.log.Info("restored stored history",
		"services", len(snap.Services), "with_history", len(history), "driver", r.storage.Driver)
}

// Run keeps the store in step with the source until ctx is cancelled.
func (r *Recorder) Run(ctx context.Context) {
	rollupEvery := time.Duration(r.storage.History.RollupIntervalMinutes) * time.Minute
	if rollupEvery <= 0 {
		rollupEvery = 15 * time.Minute
	}

	syncTick := time.NewTicker(r.sync)
	defer syncTick.Stop()
	rollupTick := time.NewTicker(rollupEvery)
	defer rollupTick.Stop()

	for {
		select {
		case <-ctx.Done():
			// One last write, so a graceful shutdown does not discard the
			// reading taken between the final tick and the signal.
			r.SyncOnce(context.WithoutCancel(ctx))
			return
		case <-syncTick.C:
			r.SyncOnce(ctx)
		case <-rollupTick.C:
			r.RollupOnce(ctx)
		}
	}
}

// SyncOnce persists the source's snapshot if it has changed since the last one.
func (r *Recorder) SyncOnce(ctx context.Context) {
	live := r.src.Snapshot()
	if len(live.Services) == 0 {
		return
	}

	gen := live.GeneratedAt.UnixNano()
	if gen != 0 && gen == r.lastSeen.Load() {
		// Unchanged. Rewriting it would append a duplicate sample for every
		// service on every tick, which inflates the rollup's denominator and
		// makes a stalled upstream look like a busy one.
		return
	}
	r.lastSeen.Store(gen)

	if err := r.store.Save(ctx, live); err != nil {
		r.log.Error("could not persist snapshot; the dashboard keeps serving from memory",
			"error", err, "driver", r.storage.Driver)
		// Fall through: the merge below is still worth doing, and dropping it
		// would freeze the charts because one write failed.
	}
	r.publish(live)
}

// RollupOnce folds raw samples into daily buckets and prunes what has expired.
func (r *Recorder) RollupOnce(ctx context.Context) {
	now := r.clock().UTC()

	// Yesterday, not today: today's bucket is still filling, and sealing it now
	// would freeze a partial day as though it were complete.
	through := store.Day(now).AddDate(0, 0, -1)
	if err := r.store.Rollup(ctx, through); err != nil {
		r.log.Error("rollup failed", "error", err, "driver", r.storage.Driver)
		return
	}

	rawHours := r.storage.History.RawSampleRetentionHours
	if rawHours <= 0 {
		rawHours = 48
	}
	retention := r.storage.History.RetentionDays
	if retention <= 0 {
		retention = 90
	}

	if err := r.store.Prune(ctx,
		now.Add(-time.Duration(rawHours)*time.Hour),
		store.Day(now).AddDate(0, 0, -retention),
	); err != nil {
		r.log.Error("prune failed", "error", err, "driver", r.storage.Driver)
		return
	}

	// Re-read, so the newly sealed day appears in the charts without waiting
	// for a restart.
	r.Restore(ctx)
}

// publish recomputes the merged snapshot readers see.
func (r *Recorder) publish(live model.Snapshot) {
	stored := *r.stored.Load()

	services := make([]model.Service, len(live.Services))
	copy(services, live.Services)

	for i := range services {
		past := stored[services[i].ID]
		if len(past) == 0 {
			continue
		}
		// Live history wins per day. An upstream serving its own ninety days
		// overrides the stored copy; one serving only today is topped up from
		// it; one serving none gets the stored series whole.
		services[i].History = store.MergeHistory(past, services[i].History)
	}

	// Everything derived is recomputed, because splicing history in changes the
	// trends and the ranking that depend on it. Running it here rather than
	// trusting what the driver produced is the same rule the rest of the
	// pipeline follows: the dashboard decides verdicts, it does not accept them.
	generatedAt := live.GeneratedAt
	if generatedAt.IsZero() {
		generatedAt = r.clock().UTC()
	}
	services = rules.Finalise(services, r.domain, generatedAt)

	merged := model.Snapshot{Services: services, GeneratedAt: live.GeneratedAt}
	r.merged.Store(&merged)
}

// SinkRecorder is a Recorder over a source that also accepts pushes.
//
// Two types rather than one because Go cannot conditionally implement an
// interface: the ingest route is registered only when the source is writable,
// and that test has to stay meaningful once a Recorder is in front of it.
type SinkRecorder struct {
	*Recorder
	sink interface{ Store(model.Snapshot) }
}

// NewSink wraps a writable source.
func NewSink(o Options, sink interface{ Store(model.Snapshot) }) *SinkRecorder {
	return &SinkRecorder{Recorder: New(o), sink: sink}
}

// Store forwards a pushed snapshot to the underlying source and persists it
// immediately.
//
// Immediately, rather than on the next tick, because a collector that gets a
// 200 back has been told the data was accepted. Waiting up to five seconds to
// make that true would be a lie small enough to go unnoticed and large enough
// to lose a payload to a restart.
func (s *SinkRecorder) Store(snap model.Snapshot) {
	s.sink.Store(snap)
	s.SyncOnce(context.Background())
}
