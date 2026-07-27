-- Initial schema.
--
-- Times are stored as BIGINT epoch seconds rather than TIMESTAMPTZ, matching
-- SQLite and MySQL. It costs some readability at the psql prompt and buys the
-- thing that matters more: every backend hands Go the same value for the same
-- row, so a dashboard cannot render one history on Postgres and another on
-- SQLite because of how a driver chose to parse a timestamp.

CREATE TABLE IF NOT EXISTS services (
    id            TEXT PRIMARY KEY,
    key           TEXT   NOT NULL DEFAULT '',
    name_term_id  TEXT   NOT NULL DEFAULT '',
    desc_term_id  TEXT   NOT NULL DEFAULT '',
    category_id   TEXT   NOT NULL DEFAULT '',
    region_id     TEXT   NOT NULL DEFAULT '',
    provider_id   TEXT   NOT NULL DEFAULT '',
    scope         TEXT   NOT NULL DEFAULT '',
    seq           BIGINT NOT NULL DEFAULT 0,
    updated_at    BIGINT NOT NULL DEFAULT 0
);

-- seq preserves first-seen order, so Load returns services in a stable sequence
-- instead of whatever order the query planner finds convenient.
CREATE INDEX IF NOT EXISTS services_seq ON services (seq);

CREATE TABLE IF NOT EXISTS service_state (
    service_id        TEXT PRIMARY KEY REFERENCES services (id) ON DELETE CASCADE,
    -- NULL is load-bearing: it means the service has not reported, which is
    -- rendered as "unknown". Coercing it to 0 would read as a total outage.
    availability      DOUBLE PRECISION,
    error_rate        DOUBLE PRECISION NOT NULL DEFAULT 0,
    latency_p50       INTEGER          NOT NULL DEFAULT 0,
    stale_seconds     BIGINT           NOT NULL DEFAULT 0,
    volume_total      BIGINT           NOT NULL DEFAULT 0,
    volume_success    BIGINT           NOT NULL DEFAULT 0,
    status            TEXT             NOT NULL DEFAULT '',
    maint_active      BOOLEAN          NOT NULL DEFAULT FALSE,
    maint_until       BIGINT           NOT NULL DEFAULT 0,
    maint_reason      TEXT             NOT NULL DEFAULT '',
    observed_at       BIGINT           NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS samples (
    service_id    TEXT   NOT NULL,
    ts            BIGINT NOT NULL,
    availability  DOUBLE PRECISION,
    error_rate    DOUBLE PRECISION NOT NULL DEFAULT 0,
    latency_p50   INTEGER          NOT NULL DEFAULT 0,
    volume        BIGINT           NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS samples_service_ts ON samples (service_id, ts);

CREATE TABLE IF NOT EXISTS history_daily (
    service_id    TEXT   NOT NULL,
    day           BIGINT NOT NULL,
    availability  DOUBLE PRECISION,
    error_rate    DOUBLE PRECISION NOT NULL DEFAULT 0,
    latency_p50   INTEGER          NOT NULL DEFAULT 0,
    volume        BIGINT           NOT NULL DEFAULT 0,
    -- samples counts the raw observations folded in, and is 0 for a bucket the
    -- upstream supplied wholesale.
    samples       INTEGER          NOT NULL DEFAULT 0,
    PRIMARY KEY (service_id, day)
);

CREATE TABLE IF NOT EXISTS incidents (
    id            TEXT PRIMARY KEY,
    service_id    TEXT    NOT NULL,
    severity      TEXT    NOT NULL DEFAULT '',
    opened_at     BIGINT  NOT NULL DEFAULT 0,
    closed_at     BIGINT  NOT NULL DEFAULT 0,
    open          BOOLEAN NOT NULL DEFAULT FALSE,
    note_term_id  TEXT    NOT NULL DEFAULT '',
    seq           BIGINT  NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS incidents_service ON incidents (service_id, seq);

CREATE TABLE IF NOT EXISTS incident_events (
    incident_id   TEXT   NOT NULL,
    seq           BIGINT NOT NULL,
    type          TEXT   NOT NULL DEFAULT '',
    at            BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (incident_id, seq)
);

CREATE TABLE IF NOT EXISTS error_buckets (
    service_id    TEXT   NOT NULL,
    seq           BIGINT NOT NULL,
    code          TEXT   NOT NULL DEFAULT '',
    term_id       TEXT   NOT NULL DEFAULT '',
    class         TEXT   NOT NULL DEFAULT '',
    count         BIGINT NOT NULL DEFAULT 0,
    share         DOUBLE PRECISION NOT NULL DEFAULT 0,
    trend         TEXT   NOT NULL DEFAULT '',
    PRIMARY KEY (service_id, seq)
);
