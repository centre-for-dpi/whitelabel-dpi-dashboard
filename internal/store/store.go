// Package store persists what the dashboard has observed.
//
// Without it the dashboard is only as old as its process: a restart loses every
// daily bucket it rolled up itself, and the charts start empty again. That is
// tolerable when the upstream serves ninety days of history on every request,
// and not tolerable at all when it serves only a current reading — which is the
// common case, and the case a push collector almost always falls into.
//
// The interface is deliberately small. Four backends implement it, and every
// method one of them has to emulate awkwardly is a method that will behave
// differently on one of them.
package store

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
)

// Store is the persistence contract.
//
// It is not on the read path. Rendering serves from an in-memory snapshot, so
// page latency does not depend on the database being fast, or up. The store's
// job is to survive a restart and to accumulate history the upstream does not
// provide.
type Store interface {
	// Migrate brings the schema up to date. Safe to call on every start.
	Migrate(ctx context.Context) error

	// Save records the current state of every service, and appends one raw
	// sample each. Any history the services carry is upserted into the daily
	// buckets, so an upstream that serves its own history backfills the charts
	// immediately.
	Save(ctx context.Context, snap model.Snapshot) error

	// Load reconstructs the last known state, including history. It is what a
	// restart reads so the charts do not start empty.
	Load(ctx context.Context, retentionDays int) (model.Snapshot, error)

	// Rollup folds raw samples into daily buckets, for services whose upstream
	// reports no history of its own.
	Rollup(ctx context.Context, through time.Time) error

	// Prune discards raw samples already rolled up, and daily buckets past
	// retention.
	Prune(ctx context.Context, sampleCutoff, dayCutoff time.Time) error

	// Close releases the connection.
	Close() error
}

// Options configure a store.
type Options struct {
	Driver          string
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	// Migrations supplies the per-dialect SQL, embedded by the caller.
	Migrations MigrationSource
}

// MigrationSource yields the ordered migrations for a dialect.
type MigrationSource interface {
	Migrations(dialect string) ([]Migration, error)
}

// Migration is one versioned schema change.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Drivers that can be configured.
const (
	DriverMemory   = "memory"
	DriverSQLite   = "sqlite"
	DriverPostgres = "postgres"
	DriverMySQL    = "mysql"
	DriverMariaDB  = "mariadb"
)

// Drivers lists every supported backend.
func Drivers() []string {
	return []string{DriverMemory, DriverSQLite, DriverPostgres, DriverMySQL, DriverMariaDB}
}

// Dialect resolves a driver name to the SQL flavour it speaks.
//
// MariaDB is wire-compatible with MySQL and shares its SQL, so one dialect and
// one driver cover both.
func Dialect(driver string) string {
	if driver == DriverMariaDB {
		return DriverMySQL
	}
	return driver
}

// Day truncates a time to the UTC day a reading belongs to.
//
// Every bucket boundary in this package goes through here, so a sample taken at
// 23:59 in one timezone and 00:01 in another cannot land in different buckets
// depending on where the collector happens to run.
func Day(t time.Time) time.Time { return t.UTC().Truncate(24 * time.Hour) }

// MergeHistory folds new daily points into an existing series, newest value
// winning for a day already present, and returns it in ascending order.
//
// Last write wins per day on purpose: a later poll of the same day is a better
// reading than an earlier one, and an upstream correcting yesterday's figure
// should be able to.
//
// Exported because the persistence layer needs the same rule when it splices
// stored history into a live snapshot: an upstream serving ninety days should
// override what was stored, an upstream serving only today should be topped up
// from it, and one serving none should get the stored series whole.
func MergeHistory(existing, incoming []model.HistoryPoint) []model.HistoryPoint {
	byDay := make(map[time.Time]model.HistoryPoint, len(existing)+len(incoming))
	for _, p := range existing {
		byDay[Day(p.Day)] = p
	}
	for _, p := range incoming {
		p.Day = Day(p.Day)
		byDay[p.Day] = p
	}

	out := make([]model.HistoryPoint, 0, len(byDay))
	for _, p := range byDay {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b model.HistoryPoint) int { return a.Day.Compare(b.Day) })
	return out
}

// trimHistory drops points older than the retention window.
func trimHistory(points []model.HistoryPoint, cutoff time.Time) []model.HistoryPoint {
	out := points[:0]
	for _, p := range points {
		if !p.Day.Before(cutoff) {
			out = append(out, p)
		}
	}
	return out
}

// RollupSamples folds raw samples into one daily point.
//
// Rates are averaged and traffic summed, matching what a windowed figure means
// over a period. Samples with no availability reading are skipped for that
// average rather than counted as zero — a gap in reporting is not an outage,
// and averaging it in as one would quietly punish a service for its collector
// having been offline.
//
// It is exported because every backend calls it. Doing this arithmetic as a SQL
// GROUP BY would round differently on each database, and the dashboard would
// then show slightly different history depending on which one a deployment
// chose. One function, four backends, identical numbers.
func RollupSamples(samples []model.Sample) model.HistoryPoint {
	if len(samples) == 0 {
		return model.HistoryPoint{}
	}

	var (
		availSum  float64
		availDays int
		errSum    float64
		latSum    int64
		volSum    int64
	)
	for _, s := range samples {
		if s.Availability.Valid {
			availSum += s.Availability.Value
			availDays++
		}
		errSum += s.ErrorRate
		latSum += int64(s.LatencyP50)
		volSum += s.Volume
	}

	n := float64(len(samples))
	p := model.HistoryPoint{
		Day:        Day(samples[0].At),
		ErrorRate:  errSum / n,
		LatencyP50: int32(latSum / int64(len(samples))),
		// Summed rather than averaged: a day's traffic is the total of its
		// samples, not their mean.
		Volume:  volSum,
		Samples: int32(len(samples)),
	}
	if availDays > 0 {
		p.Availability = model.Float(availSum / float64(availDays))
	}
	return p
}

// samplesFrom turns a snapshot into one raw sample per service.
func samplesFrom(snap model.Snapshot) []model.Sample {
	out := make([]model.Sample, 0, len(snap.Services))
	for _, sv := range snap.Services {
		at := sv.ObservedAt
		if at.IsZero() {
			at = snap.GeneratedAt
		}
		out = append(out, model.Sample{
			ServiceID:    sv.ID,
			At:           at.UTC(),
			Availability: sv.Metrics.Availability,
			ErrorRate:    sv.Metrics.ErrorRate,
			LatencyP50:   sv.Metrics.LatencyP50,
			Volume:       sv.Metrics.Volume.Total,
		})
	}
	return out
}

// ErrUnsupportedDriver is returned for a driver this build does not include.
type ErrUnsupportedDriver struct{ Driver string }

func (e ErrUnsupportedDriver) Error() string {
	return fmt.Sprintf("unsupported storage driver %q; this build provides %v", e.Driver, Drivers())
}
