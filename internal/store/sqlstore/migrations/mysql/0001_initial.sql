-- Initial schema. Also used for MariaDB, which is wire- and SQL-compatible here.
--
-- Three MySQL-specific adaptations, none of them cosmetic:
--
--   * Identifiers are backticked throughout. `key` is a reserved word, and
--     picking through the reserved list column by column is the kind of check
--     that passes review and then fails on a version bump.
--   * Keys are VARCHAR(191) rather than TEXT. MySQL cannot index a TEXT column
--     without a prefix length, and 191 is the longest utf8mb4 key that fits the
--     767-byte index limit on older InnoDB row formats.
--   * Indexes are declared inside CREATE TABLE. MySQL has no
--     CREATE INDEX IF NOT EXISTS, so a separate statement would fail the second
--     time the migration ran.

CREATE TABLE IF NOT EXISTS `services` (
    `id`            VARCHAR(191) NOT NULL,
    `key`           VARCHAR(191) NOT NULL DEFAULT '',
    `name_term_id`  VARCHAR(191) NOT NULL DEFAULT '',
    `desc_term_id`  VARCHAR(191) NOT NULL DEFAULT '',
    `category_id`   VARCHAR(191) NOT NULL DEFAULT '',
    `region_id`     VARCHAR(191) NOT NULL DEFAULT '',
    `provider_id`   VARCHAR(191) NOT NULL DEFAULT '',
    `scope`         VARCHAR(64)  NOT NULL DEFAULT '',
    -- seq preserves first-seen order, so Load returns services in a stable
    -- sequence instead of whatever order the query planner finds convenient.
    `seq`           BIGINT       NOT NULL DEFAULT 0,
    `updated_at`    BIGINT       NOT NULL DEFAULT 0,
    PRIMARY KEY (`id`),
    KEY `services_seq` (`seq`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `service_state` (
    `service_id`      VARCHAR(191) NOT NULL,
    -- NULL is load-bearing: it means the service has not reported, which is
    -- rendered as "unknown". Coercing it to 0 would read as a total outage.
    `availability`    DOUBLE       NULL,
    `error_rate`      DOUBLE       NOT NULL DEFAULT 0,
    `latency_p50`     INT          NOT NULL DEFAULT 0,
    `stale_seconds`   BIGINT       NOT NULL DEFAULT 0,
    `volume_total`    BIGINT       NOT NULL DEFAULT 0,
    `volume_success`  BIGINT       NOT NULL DEFAULT 0,
    `status`          VARCHAR(64)  NOT NULL DEFAULT '',
    `maint_active`    TINYINT(1)   NOT NULL DEFAULT 0,
    `maint_until`     BIGINT       NOT NULL DEFAULT 0,
    `maint_reason`    VARCHAR(191) NOT NULL DEFAULT '',
    `observed_at`     BIGINT       NOT NULL DEFAULT 0,
    PRIMARY KEY (`service_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `samples` (
    `service_id`    VARCHAR(191) NOT NULL,
    `ts`            BIGINT       NOT NULL,
    `availability`  DOUBLE       NULL,
    `error_rate`    DOUBLE       NOT NULL DEFAULT 0,
    `latency_p50`   INT          NOT NULL DEFAULT 0,
    `volume`        BIGINT       NOT NULL DEFAULT 0,
    KEY `samples_service_ts` (`service_id`, `ts`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `history_daily` (
    `service_id`    VARCHAR(191) NOT NULL,
    `day`           BIGINT       NOT NULL,
    `availability`  DOUBLE       NULL,
    `error_rate`    DOUBLE       NOT NULL DEFAULT 0,
    `latency_p50`   INT          NOT NULL DEFAULT 0,
    `volume`        BIGINT       NOT NULL DEFAULT 0,
    -- samples counts the raw observations folded in, and is 0 for a bucket the
    -- upstream supplied wholesale.
    `samples`       INT          NOT NULL DEFAULT 0,
    PRIMARY KEY (`service_id`, `day`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `incidents` (
    `id`            VARCHAR(191) NOT NULL,
    `service_id`    VARCHAR(191) NOT NULL,
    `severity`      VARCHAR(64)  NOT NULL DEFAULT '',
    `opened_at`     BIGINT       NOT NULL DEFAULT 0,
    `closed_at`     BIGINT       NOT NULL DEFAULT 0,
    `open`          TINYINT(1)   NOT NULL DEFAULT 0,
    `note_term_id`  VARCHAR(191) NOT NULL DEFAULT '',
    `seq`           BIGINT       NOT NULL DEFAULT 0,
    PRIMARY KEY (`id`),
    KEY `incidents_service` (`service_id`, `seq`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `incident_events` (
    `incident_id`   VARCHAR(191) NOT NULL,
    `seq`           BIGINT       NOT NULL,
    `type`          VARCHAR(64)  NOT NULL DEFAULT '',
    `at`            BIGINT       NOT NULL DEFAULT 0,
    PRIMARY KEY (`incident_id`, `seq`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `error_buckets` (
    `service_id`    VARCHAR(191) NOT NULL,
    `seq`           BIGINT       NOT NULL,
    `code`          VARCHAR(64)  NOT NULL DEFAULT '',
    `term_id`       VARCHAR(191) NOT NULL DEFAULT '',
    `class`         VARCHAR(64)  NOT NULL DEFAULT '',
    `count`         BIGINT       NOT NULL DEFAULT 0,
    `share`         DOUBLE       NOT NULL DEFAULT 0,
    `trend`         VARCHAR(64)  NOT NULL DEFAULT '',
    PRIMARY KEY (`service_id`, `seq`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
