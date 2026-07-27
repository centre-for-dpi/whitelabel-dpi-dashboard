-- Initial schema.
--
-- SQLite has no native boolean, timestamp or decimal type. Rather than lean on
-- its permissive affinity rules, the columns below are explicit about what the
-- Go side reads back: integers for booleans and epoch seconds, REAL for rates.
-- A store that guesses is a store that returns a different type on one backend.

CREATE TABLE IF NOT EXISTS services (
    id            TEXT PRIMARY KEY,
    key           TEXT NOT NULL DEFAULT '',
    name_term_id  TEXT NOT NULL DEFAULT '',
    desc_term_id  TEXT NOT NULL DEFAULT '',
    category_id   TEXT NOT NULL DEFAULT '',
    region_id     TEXT NOT NULL DEFAULT '',
    provider_id   TEXT NOT NULL DEFAULT '',
    scope         TEXT NOT NULL DEFAULT '',
    seq           INTEGER NOT NULL DEFAULT 0,
    updated_at    INTEGER NOT NULL DEFAULT 0
);

-- seq preserves first-seen order, so Load returns services in a stable sequence
-- instead of whatever order the query planner finds convenient.
CREATE INDEX IF NOT EXISTS services_seq ON services (seq);

CREATE TABLE IF NOT EXISTS service_state (
    service_id        TEXT PRIMARY KEY,
    -- NULL is load-bearing: it means the service has not reported, which is
    -- rendered as "unknown". Coercing it to 0 would read as a total outage.
    availability      REAL,
    error_rate        REAL    NOT NULL DEFAULT 0,
    latency_p50       INTEGER NOT NULL DEFAULT 0,
    stale_seconds     INTEGER NOT NULL DEFAULT 0,
    volume_total      INTEGER NOT NULL DEFAULT 0,
    volume_success    INTEGER NOT NULL DEFAULT 0,
    status            TEXT    NOT NULL DEFAULT '',
    maint_active      INTEGER NOT NULL DEFAULT 0,
    maint_until       INTEGER NOT NULL DEFAULT 0,
    maint_reason      TEXT    NOT NULL DEFAULT '',
    observed_at       INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (service_id) REFERENCES services (id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS samples (
    service_id    TEXT    NOT NULL,
    ts            INTEGER NOT NULL,
    availability  REAL,
    error_rate    REAL    NOT NULL DEFAULT 0,
    latency_p50   INTEGER NOT NULL DEFAULT 0,
    volume        INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS samples_service_ts ON samples (service_id, ts);

CREATE TABLE IF NOT EXISTS history_daily (
    service_id    TEXT    NOT NULL,
    day           INTEGER NOT NULL,
    availability  REAL,
    error_rate    REAL    NOT NULL DEFAULT 0,
    latency_p50   INTEGER NOT NULL DEFAULT 0,
    volume        INTEGER NOT NULL DEFAULT 0,
    -- samples counts the raw observations folded in, and is 0 for a bucket the
    -- upstream supplied wholesale.
    samples       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (service_id, day)
);

CREATE TABLE IF NOT EXISTS incidents (
    id            TEXT PRIMARY KEY,
    service_id    TEXT    NOT NULL,
    severity      TEXT    NOT NULL DEFAULT '',
    opened_at     INTEGER NOT NULL DEFAULT 0,
    closed_at     INTEGER NOT NULL DEFAULT 0,
    open          INTEGER NOT NULL DEFAULT 0,
    note_term_id  TEXT    NOT NULL DEFAULT '',
    seq           INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS incidents_service ON incidents (service_id, seq);

CREATE TABLE IF NOT EXISTS incident_events (
    incident_id   TEXT    NOT NULL,
    seq           INTEGER NOT NULL,
    type          TEXT    NOT NULL DEFAULT '',
    at            INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (incident_id, seq)
);

CREATE TABLE IF NOT EXISTS error_buckets (
    service_id    TEXT    NOT NULL,
    seq           INTEGER NOT NULL,
    code          TEXT    NOT NULL DEFAULT '',
    term_id       TEXT    NOT NULL DEFAULT '',
    class         TEXT    NOT NULL DEFAULT '',
    count         INTEGER NOT NULL DEFAULT 0,
    share         REAL    NOT NULL DEFAULT 0,
    trend         TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (service_id, seq)
);
