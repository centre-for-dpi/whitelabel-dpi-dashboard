package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/store"
)

// Store persists the dashboard's state in a SQL database.
//
// It is not on the read path. Rendering serves from an in-memory snapshot, so a
// slow or briefly unreachable database delays the next update rather than the
// next page.
type Store struct {
	db         *sql.DB
	dialect    Dialect
	migrations store.MigrationSource
	clock      func() time.Time

	// seq assigns first-seen order to new services, so Load returns them in a
	// stable sequence rather than the planner's preferred one.
	seqMu sync.Mutex
}

// Config opens a Store.
type Config struct {
	// Driver is the configured backend: sqlite, postgres, mysql or mariadb.
	Driver string
	// DSN is the connection string. It comes from the environment, never from a
	// config file, because it usually contains a password.
	DSN string

	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration

	// Migrations defaults to the embedded set.
	Migrations store.MigrationSource
	// Clock defaults to time.Now, and is injected so tests are deterministic.
	Clock func() time.Time
}

// Open connects and returns a Store. It does not migrate; call Migrate.
func Open(cfg Config) (*Store, error) {
	dialect, err := dialectFor(cfg.Driver)
	if err != nil {
		return nil, err
	}

	name, ok := driverName(cfg.Driver)
	if !ok {
		return nil, fmt.Errorf(
			"storage driver %q is not compiled into this build; "+
				"it was excluded by a build tag, so rebuild without it or choose another driver",
			cfg.Driver)
	}
	if cfg.DSN == "" {
		// Failing here beats failing on the first query with whatever the
		// driver makes of an empty string.
		return nil, fmt.Errorf("storage driver %q needs a dsn", cfg.Driver)
	}

	db, err := sql.Open(name, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", cfg.Driver, err)
	}

	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}

	return New(db, dialect, cfg.Migrations, cfg.Clock), nil
}

// New wraps an already-open database. Tests use it to supply their own handle.
func New(db *sql.DB, dialect Dialect, migrations store.MigrationSource, clock func() time.Time) *Store {
	if migrations == nil {
		migrations = Embedded{}
	}
	if clock == nil {
		clock = time.Now
	}
	return &Store{db: db, dialect: dialect, migrations: migrations, clock: clock}
}

// Ping verifies the connection is usable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Close releases the connection pool.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) now() time.Time { return s.clock().UTC() }

// inTx runs fn in a transaction, rolling back on error or panic.
func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			// Roll back before re-panicking, or the connection is returned to
			// the pool mid-transaction and poisons whoever gets it next.
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// --- Save -------------------------------------------------------------------

// Save records the current state of every service and appends one sample each.
//
// The whole snapshot is one transaction: a reader loading part way through an
// update would otherwise see some services at the new reading and some at the
// old, and compute a verdict from a mixture that never actually existed.
func (s *Store) Save(ctx context.Context, snap model.Snapshot) error {
	s.seqMu.Lock()
	defer s.seqMu.Unlock()

	return s.inTx(ctx, func(tx *sql.Tx) error {
		next, err := s.nextSeq(ctx, tx)
		if err != nil {
			return err
		}
		known, err := s.knownServices(ctx, tx)
		if err != nil {
			return err
		}

		updatedAt := snap.GeneratedAt.UTC().Unix()
		for _, sv := range snap.Services {
			seq, seen := known[sv.ID]
			if !seen {
				seq = next
				next++
			}
			if err := s.saveService(ctx, tx, sv, seq, updatedAt); err != nil {
				return fmt.Errorf("service %q: %w", sv.ID, err)
			}
		}

		return s.appendSamples(ctx, tx, snap)
	})
}

func (s *Store) saveService(ctx context.Context, tx *sql.Tx, sv model.Service, seq int64, updatedAt int64) error {
	d := s.dialect

	cols := []string{"id", "key", "name_term_id", "desc_term_id", "category_id",
		"region_id", "provider_id", "scope", "seq", "updated_at"}
	// seq is deliberately not in the update list: a service's first-seen
	// position should not move because it reported again.
	update := []string{"key", "name_term_id", "desc_term_id", "category_id",
		"region_id", "provider_id", "scope", "updated_at"}

	if _, err := tx.ExecContext(ctx,
		insert(d, "services", cols, []string{"id"}, update),
		sv.ID, sv.Key, sv.NameTermID, sv.DescTermID, sv.CategoryID,
		sv.RegionID, sv.ProviderID, sv.Scope, seq, updatedAt,
	); err != nil {
		return err
	}

	stateCols := []string{"service_id", "availability", "error_rate", "latency_p50",
		"stale_seconds", "volume_total", "volume_success", "status",
		"maint_active", "maint_until", "maint_reason", "observed_at"}

	if _, err := tx.ExecContext(ctx,
		insert(d, "service_state", stateCols, []string{"service_id"}, stateCols[1:]),
		sv.ID, optFloat(sv.Metrics.Availability), sv.Metrics.ErrorRate, sv.Metrics.LatencyP50,
		sv.Metrics.StaleSeconds, sv.Metrics.Volume.Total, sv.Metrics.Volume.Success,
		string(sv.Status), sv.Maintenance.Active, unix(sv.Maintenance.Until),
		sv.Maintenance.ReasonTermID, unix(sv.ObservedAt),
	); err != nil {
		return err
	}

	// History travels separately from the service record: an upstream that
	// stops sending history must not erase what is already stored, so an empty
	// slice writes nothing rather than deleting.
	if err := s.saveHistory(ctx, tx, sv.ID, sv.History); err != nil {
		return err
	}
	if err := s.saveIncidents(ctx, tx, sv.ID, sv.Incidents); err != nil {
		return err
	}
	return s.saveErrors(ctx, tx, sv.ID, sv.Errors)
}

func (s *Store) saveHistory(ctx context.Context, tx *sql.Tx, serviceID string, points []model.HistoryPoint) error {
	cols := []string{"service_id", "day", "availability", "error_rate", "latency_p50", "volume", "samples"}
	stmt := insert(s.dialect, "history_daily", cols, []string{"service_id", "day"}, cols[2:])

	for _, p := range points {
		if _, err := tx.ExecContext(ctx, stmt,
			serviceID, store.Day(p.Day).Unix(), optFloat(p.Availability),
			p.ErrorRate, p.LatencyP50, p.Volume, p.Samples,
		); err != nil {
			return fmt.Errorf("history %s: %w", p.Day.Format(time.DateOnly), err)
		}
	}
	return nil
}

// saveIncidents replaces a service's incidents wholesale.
//
// Replace rather than upsert because an incident that has disappeared upstream
// should disappear here, and because the events are an ordered list rather than
// a set — merging them would need identity the wire contract does not give them.
// Incidents are few per service, so the delete costs nothing.
func (s *Store) saveIncidents(ctx context.Context, tx *sql.Tx, serviceID string, incidents []model.Incident) error {
	if len(incidents) == 0 {
		return nil
	}
	d := s.dialect

	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE %s IN (SELECT %s FROM %s WHERE %s)",
			d.Quote("incident_events"), d.Quote("incident_id"), d.Quote("id"),
			d.Quote("incidents"), cmp(d, "service_id", "=", 1)),
		serviceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE %s", d.Quote("incidents"), cmp(d, "service_id", "=", 1)),
		serviceID); err != nil {
		return err
	}

	incCols := []string{"id", "service_id", "severity", "opened_at", "closed_at", "open", "note_term_id", "seq"}
	incStmt := insert(d, "incidents", incCols, nil, nil)
	evtCols := []string{"incident_id", "seq", "type", "at"}
	evtStmt := insert(d, "incident_events", evtCols, nil, nil)

	for i, inc := range incidents {
		if _, err := tx.ExecContext(ctx, incStmt,
			inc.ID, serviceID, string(inc.Severity), unix(inc.OpenedAt),
			unix(inc.ClosedAt), inc.Open, inc.NoteTermID, i,
		); err != nil {
			return fmt.Errorf("incident %q: %w", inc.ID, err)
		}
		for j, e := range inc.Events {
			if _, err := tx.ExecContext(ctx, evtStmt, inc.ID, j, e.Type, unix(e.At)); err != nil {
				return fmt.Errorf("incident %q event %d: %w", inc.ID, j, err)
			}
		}
	}
	return nil
}

// saveErrors replaces a service's error breakdown, for the same reason as
// incidents: it is a ranked list, and a code that stopped occurring should stop
// being shown.
func (s *Store) saveErrors(ctx context.Context, tx *sql.Tx, serviceID string, buckets []model.ErrorBucket) error {
	if len(buckets) == 0 {
		return nil
	}
	d := s.dialect

	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE %s", d.Quote("error_buckets"), cmp(d, "service_id", "=", 1)),
		serviceID); err != nil {
		return err
	}

	cols := []string{"service_id", "seq", "code", "term_id", "class", "count", "share", "trend"}
	stmt := insert(d, "error_buckets", cols, nil, nil)
	for i, b := range buckets {
		if _, err := tx.ExecContext(ctx, stmt,
			serviceID, i, b.Code, b.TermID, string(b.Class), b.Count, b.Share, string(b.Trend),
		); err != nil {
			return fmt.Errorf("error bucket %q: %w", b.Code, err)
		}
	}
	return nil
}

func (s *Store) appendSamples(ctx context.Context, tx *sql.Tx, snap model.Snapshot) error {
	cols := []string{"service_id", "ts", "availability", "error_rate", "latency_p50", "volume"}
	stmt := insert(s.dialect, "samples", cols, nil, nil)

	for _, sv := range snap.Services {
		at := sv.ObservedAt
		if at.IsZero() {
			at = snap.GeneratedAt
		}
		if _, err := tx.ExecContext(ctx, stmt,
			sv.ID, at.UTC().Unix(), optFloat(sv.Metrics.Availability),
			sv.Metrics.ErrorRate, sv.Metrics.LatencyP50, sv.Metrics.Volume.Total,
		); err != nil {
			return fmt.Errorf("sample for %q: %w", sv.ID, err)
		}
	}
	return nil
}

func (s *Store) nextSeq(ctx context.Context, tx *sql.Tx) (int64, error) {
	var max sql.NullInt64
	err := tx.QueryRowContext(ctx,
		fmt.Sprintf("SELECT MAX(%s) FROM %s", s.dialect.Quote("seq"), s.dialect.Quote("services")),
	).Scan(&max)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("reading service order: %w", err)
	}
	return max.Int64 + 1, nil
}

func (s *Store) knownServices(ctx context.Context, tx *sql.Tx) (map[string]int64, error) {
	rows, err := tx.QueryContext(ctx,
		selectAll(s.dialect, "services", []string{"id", "seq"}, "", ""))
	if err != nil {
		return nil, fmt.Errorf("reading services: %w", err)
	}
	defer rows.Close()

	out := map[string]int64{}
	for rows.Next() {
		var (
			id  string
			seq int64
		)
		if err := rows.Scan(&id, &seq); err != nil {
			return nil, fmt.Errorf("reading services: %w", err)
		}
		out[id] = seq
	}
	return out, rows.Err()
}

// --- Load -------------------------------------------------------------------

// Load reconstructs the last known state.
//
// Five queries rather than one join: a service joined against its history, its
// incidents, its incident events and its error buckets multiplies rows by the
// product of all four, and reassembling that in Go is both slower and easier to
// get wrong than reading four indexed lists and keying them by service.
func (s *Store) Load(ctx context.Context, retentionDays int) (model.Snapshot, error) {
	services, generatedAt, err := s.loadServices(ctx)
	if err != nil {
		return model.Snapshot{}, err
	}
	if len(services) == 0 {
		return model.Snapshot{}, nil
	}

	history, err := s.loadHistory(ctx, retentionCutoff(generatedAt, retentionDays))
	if err != nil {
		return model.Snapshot{}, err
	}
	incidents, err := s.loadIncidents(ctx)
	if err != nil {
		return model.Snapshot{}, err
	}
	errorBuckets, err := s.loadErrors(ctx)
	if err != nil {
		return model.Snapshot{}, err
	}

	for i := range services {
		id := services[i].ID
		services[i].History = history[id]
		services[i].Incidents = incidents[id]
		services[i].Errors = errorBuckets[id]
	}
	return model.Snapshot{Services: services, GeneratedAt: generatedAt}, nil
}

func (s *Store) loadServices(ctx context.Context) ([]model.Service, time.Time, error) {
	d := s.dialect
	stmt := fmt.Sprintf(
		`SELECT sv.%s, sv.%s, sv.%s, sv.%s, sv.%s, sv.%s, sv.%s, sv.%s, sv.%s,
		        st.%s, st.%s, st.%s, st.%s, st.%s, st.%s, st.%s, st.%s, st.%s, st.%s, st.%s
		 FROM %s sv LEFT JOIN %s st ON st.%s = sv.%s ORDER BY sv.%s`,
		d.Quote("id"), d.Quote("key"), d.Quote("name_term_id"), d.Quote("desc_term_id"),
		d.Quote("category_id"), d.Quote("region_id"), d.Quote("provider_id"),
		d.Quote("scope"), d.Quote("updated_at"),
		d.Quote("availability"), d.Quote("error_rate"), d.Quote("latency_p50"),
		d.Quote("stale_seconds"), d.Quote("volume_total"), d.Quote("volume_success"),
		d.Quote("status"), d.Quote("maint_active"), d.Quote("maint_until"),
		d.Quote("maint_reason"), d.Quote("observed_at"),
		d.Quote("services"), d.Quote("service_state"), d.Quote("service_id"), d.Quote("id"),
		d.Quote("seq"))

	rows, err := s.db.QueryContext(ctx, stmt)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("loading services: %w", err)
	}
	defer rows.Close()

	var (
		out         []model.Service
		generatedAt int64
	)
	for rows.Next() {
		var (
			sv          model.Service
			updatedAt   int64
			avail       sql.NullFloat64
			errRate     sql.NullFloat64
			latency     sql.NullInt64
			stale       sql.NullInt64
			volTotal    sql.NullInt64
			volSuccess  sql.NullInt64
			status      sql.NullString
			maintActive sql.NullBool
			maintUntil  sql.NullInt64
			maintReason sql.NullString
			observedAt  sql.NullInt64
		)
		// service_state is LEFT JOINed, so every one of its columns can be NULL
		// for a service recorded before its first reading arrived.
		if err := rows.Scan(
			&sv.ID, &sv.Key, &sv.NameTermID, &sv.DescTermID, &sv.CategoryID,
			&sv.RegionID, &sv.ProviderID, &sv.Scope, &updatedAt,
			&avail, &errRate, &latency, &stale, &volTotal, &volSuccess,
			&status, &maintActive, &maintUntil, &maintReason, &observedAt,
		); err != nil {
			return nil, time.Time{}, fmt.Errorf("loading services: %w", err)
		}

		if avail.Valid {
			sv.Metrics.Availability = model.Float(avail.Float64)
		}
		sv.Metrics.ErrorRate = errRate.Float64
		sv.Metrics.LatencyP50 = int32(latency.Int64)
		sv.Metrics.StaleSeconds = stale.Int64
		sv.Metrics.Volume = model.Volume{Total: volTotal.Int64, Success: volSuccess.Int64}
		sv.Status = model.Status(status.String)
		sv.Maintenance = model.Maintenance{
			Active:       maintActive.Bool,
			Until:        fromUnix(maintUntil.Int64),
			ReasonTermID: maintReason.String,
		}
		sv.ObservedAt = fromUnix(observedAt.Int64)

		generatedAt = max(generatedAt, updatedAt)
		out = append(out, sv)
	}
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, fmt.Errorf("loading services: %w", err)
	}
	return out, fromUnix(generatedAt), nil
}

func (s *Store) loadHistory(ctx context.Context, cutoff time.Time) (map[string][]model.HistoryPoint, error) {
	d := s.dialect
	cols := []string{"service_id", "day", "availability", "error_rate", "latency_p50", "volume", "samples"}

	var (
		where string
		args  []any
	)
	if !cutoff.IsZero() {
		where = cmp(d, "day", ">=", 1)
		args = append(args, cutoff.Unix())
	}

	rows, err := s.db.QueryContext(ctx,
		selectAll(d, "history_daily", cols, where, d.Quote("service_id")+", "+d.Quote("day")),
		args...)
	if err != nil {
		return nil, fmt.Errorf("loading history: %w", err)
	}
	defer rows.Close()

	out := map[string][]model.HistoryPoint{}
	for rows.Next() {
		var (
			id    string
			day   int64
			p     model.HistoryPoint
			avail sql.NullFloat64
		)
		if err := rows.Scan(&id, &day, &avail, &p.ErrorRate, &p.LatencyP50, &p.Volume, &p.Samples); err != nil {
			return nil, fmt.Errorf("loading history: %w", err)
		}
		p.Day = fromUnix(day)
		if avail.Valid {
			p.Availability = model.Float(avail.Float64)
		}
		out[id] = append(out[id], p)
	}
	return out, rows.Err()
}

func (s *Store) loadIncidents(ctx context.Context) (map[string][]model.Incident, error) {
	d := s.dialect
	cols := []string{"id", "service_id", "severity", "opened_at", "closed_at", "open", "note_term_id"}

	rows, err := s.db.QueryContext(ctx,
		selectAll(d, "incidents", cols, "", d.Quote("service_id")+", "+d.Quote("seq")))
	if err != nil {
		return nil, fmt.Errorf("loading incidents: %w", err)
	}
	defer rows.Close()

	var (
		out   = map[string][]model.Incident{}
		index = map[string]*model.Incident{}
	)
	for rows.Next() {
		var (
			inc                model.Incident
			severity           string
			openedAt, closedAt int64
		)
		if err := rows.Scan(&inc.ID, &inc.ServiceID, &severity, &openedAt,
			&closedAt, &inc.Open, &inc.NoteTermID); err != nil {
			return nil, fmt.Errorf("loading incidents: %w", err)
		}
		inc.Severity = model.Status(severity)
		inc.OpenedAt = fromUnix(openedAt)
		inc.ClosedAt = fromUnix(closedAt)

		out[inc.ServiceID] = append(out[inc.ServiceID], inc)
		index[inc.ID] = &out[inc.ServiceID][len(out[inc.ServiceID])-1]
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("loading incidents: %w", err)
	}
	if len(index) == 0 {
		return out, nil
	}

	return out, s.loadIncidentEvents(ctx, index)
}

func (s *Store) loadIncidentEvents(ctx context.Context, index map[string]*model.Incident) error {
	d := s.dialect
	rows, err := s.db.QueryContext(ctx,
		selectAll(d, "incident_events", []string{"incident_id", "type", "at"}, "",
			d.Quote("incident_id")+", "+d.Quote("seq")))
	if err != nil {
		return fmt.Errorf("loading incident events: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			incidentID string
			e          model.IncidentEvent
			at         int64
		)
		if err := rows.Scan(&incidentID, &e.Type, &at); err != nil {
			return fmt.Errorf("loading incident events: %w", err)
		}
		e.At = fromUnix(at)
		if inc, ok := index[incidentID]; ok {
			inc.Events = append(inc.Events, e)
		}
	}
	return rows.Err()
}

func (s *Store) loadErrors(ctx context.Context) (map[string][]model.ErrorBucket, error) {
	d := s.dialect
	cols := []string{"service_id", "code", "term_id", "class", "count", "share", "trend"}

	rows, err := s.db.QueryContext(ctx,
		selectAll(d, "error_buckets", cols, "", d.Quote("service_id")+", "+d.Quote("seq")))
	if err != nil {
		return nil, fmt.Errorf("loading error buckets: %w", err)
	}
	defer rows.Close()

	out := map[string][]model.ErrorBucket{}
	for rows.Next() {
		var (
			id           string
			b            model.ErrorBucket
			class, trend string
		)
		if err := rows.Scan(&id, &b.Code, &b.TermID, &class, &b.Count, &b.Share, &trend); err != nil {
			return nil, fmt.Errorf("loading error buckets: %w", err)
		}
		b.Class = model.ErrorClass(class)
		b.Trend = model.Direction(trend)
		out[id] = append(out[id], b)
	}
	return out, rows.Err()
}

// --- Rollup and prune -------------------------------------------------------

// Rollup folds raw samples into daily buckets.
//
// The arithmetic happens in Go, using the same function the in-memory store
// uses, rather than as a GROUP BY. That is a deliberate trade: aggregating in
// SQL would avoid loading the samples, but AVG over an integer column returns a
// different type and rounds differently on each of the three databases, and
// integer division is spelled `/` on two of them and `DIV` on the third. The
// dashboard would then show slightly different history depending on which
// backend a deployment chose — the exact failure the contract suite exists to
// prevent. The volume loaded is bounded by raw-sample retention, which is hours,
// not the ninety days of daily buckets.
func (s *Store) Rollup(ctx context.Context, through time.Time) error {
	limit := store.Day(through)

	samples, err := s.loadSamples(ctx, limit.AddDate(0, 0, 1))
	if err != nil {
		return err
	}
	if len(samples) == 0 {
		return nil
	}

	existing, err := s.existingDays(ctx)
	if err != nil {
		return err
	}

	return s.inTx(ctx, func(tx *sql.Tx) error {
		for _, serviceID := range sortedKeys(samples) {
			byDay := samples[serviceID]
			// Ordered, so a failure part way through leaves a predictable
			// prefix rather than an arbitrary subset of the days.
			for _, day := range sortedDays(byDay) {
				group := byDay[day]
				if day.After(limit) {
					continue
				}
				// A day already recorded is left alone. An upstream that
				// reported its own figure for the day knows more about it than
				// the dashboard's sparse sampling does.
				if existing[dayKey{serviceID, day.Unix()}] {
					continue
				}
				if err := s.saveHistory(ctx, tx, serviceID,
					[]model.HistoryPoint{store.RollupSamples(group)}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

type dayKey struct {
	serviceID string
	day       int64
}

func (s *Store) loadSamples(ctx context.Context, before time.Time) (map[string]map[time.Time][]model.Sample, error) {
	d := s.dialect
	cols := []string{"service_id", "ts", "availability", "error_rate", "latency_p50", "volume"}

	rows, err := s.db.QueryContext(ctx,
		selectAll(d, "samples", cols, cmp(d, "ts", "<", 1),
			d.Quote("service_id")+", "+d.Quote("ts")),
		before.Unix())
	if err != nil {
		return nil, fmt.Errorf("loading samples: %w", err)
	}
	defer rows.Close()

	out := map[string]map[time.Time][]model.Sample{}
	for rows.Next() {
		var (
			sample model.Sample
			ts     int64
			avail  sql.NullFloat64
		)
		if err := rows.Scan(&sample.ServiceID, &ts, &avail,
			&sample.ErrorRate, &sample.LatencyP50, &sample.Volume); err != nil {
			return nil, fmt.Errorf("loading samples: %w", err)
		}
		sample.At = fromUnix(ts)
		if avail.Valid {
			sample.Availability = model.Float(avail.Float64)
		}

		day := store.Day(sample.At)
		if out[sample.ServiceID] == nil {
			out[sample.ServiceID] = map[time.Time][]model.Sample{}
		}
		out[sample.ServiceID][day] = append(out[sample.ServiceID][day], sample)
	}
	return out, rows.Err()
}

func (s *Store) existingDays(ctx context.Context) (map[dayKey]bool, error) {
	rows, err := s.db.QueryContext(ctx,
		selectAll(s.dialect, "history_daily", []string{"service_id", "day"}, "", ""))
	if err != nil {
		return nil, fmt.Errorf("reading history days: %w", err)
	}
	defer rows.Close()

	out := map[dayKey]bool{}
	for rows.Next() {
		var k dayKey
		if err := rows.Scan(&k.serviceID, &k.day); err != nil {
			return nil, fmt.Errorf("reading history days: %w", err)
		}
		out[k] = true
	}
	return out, rows.Err()
}

// Prune discards rolled-up samples and expired daily buckets.
func (s *Store) Prune(ctx context.Context, sampleCutoff, dayCutoff time.Time) error {
	d := s.dialect
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf("DELETE FROM %s WHERE %s", d.Quote("samples"), cmp(d, "ts", "<", 1)),
			sampleCutoff.UTC().Unix()); err != nil {
			return fmt.Errorf("pruning samples: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf("DELETE FROM %s WHERE %s", d.Quote("history_daily"), cmp(d, "day", "<", 1)),
			store.Day(dayCutoff).Unix()); err != nil {
			return fmt.Errorf("pruning history: %w", err)
		}
		return nil
	})
}

// tables lists every table this store owns, children before parents so a
// delete respects the foreign keys.
var tables = []string{
	"incident_events", "incidents", "error_buckets",
	"samples", "history_daily", "service_state", "services",
}

// Truncate empties every table, leaving the schema in place.
//
// It exists for the contract suite, which runs each case against a clean
// database while keeping the migration path under test. DELETE rather than
// TRUNCATE: SQLite has no TRUNCATE, MySQL's does not respect foreign keys, and
// the row counts here are small enough that the difference does not matter.
func (s *Store) Truncate(ctx context.Context) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for _, t := range tables {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+s.dialect.Quote(t)); err != nil {
				return fmt.Errorf("truncating %s: %w", t, err)
			}
		}
		return nil
	})
}

// --- conversions ------------------------------------------------------------

// optFloat renders an optional float for binding: nil, not zero.
//
// The whole status model rests on this. A service that has not reported must
// come back with no availability, so it reads as unknown rather than as a total
// outage.
func optFloat(v model.OptFloat) any {
	if !v.Valid {
		return nil
	}
	return v.Value
}

// unix renders a time for binding, mapping the zero time to 0 rather than to
// the year 1 so an unset timestamp is recognisable on the way back.
func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().Unix()
}

// fromUnix is the inverse.
func fromUnix(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

// retentionCutoff is the oldest day still worth loading.
func retentionCutoff(from time.Time, retentionDays int) time.Time {
	if retentionDays <= 0 {
		// No retention configured means no limit, rather than no history.
		return time.Time{}
	}
	if from.IsZero() {
		from = time.Now()
	}
	return store.Day(from).AddDate(0, 0, -retentionDays+1)
}

// sortedDays orders a sample map's days, so the rollup writes deterministically.
func sortedDays(byDay map[time.Time][]model.Sample) []time.Time {
	out := make([]time.Time, 0, len(byDay))
	for day := range byDay {
		out = append(out, day)
	}
	slices.SortFunc(out, func(a, b time.Time) int { return a.Compare(b) })
	return out
}

// sortedKeys orders a map's keys, for the same reason.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
