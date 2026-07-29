-- The demand side.
--
-- A requestor is the same record as an issuer — it has availability, an error
-- rate, a history — because it is the same exchange seen from the other end.
-- So it lives in the same table, and these columns say which end it is and, for
-- a requestor, what it consumes.
--
-- Every column has a default rather than being NOT NULL with none, so a store
-- written by an earlier version keeps loading: an existing row is an issuer
-- that consumes nothing, which is exactly what the empty strings say.
--
-- One ALTER for all five, and the index inside it: MySQL has no
-- ADD COLUMN IF NOT EXISTS and no CREATE INDEX IF NOT EXISTS, so each
-- statement here runs exactly once, recorded by schema_migrations.

ALTER TABLE `services`
    ADD COLUMN `role_id`           VARCHAR(64)  NOT NULL DEFAULT '',
    ADD COLUMN `sector_id`         VARCHAR(191) NOT NULL DEFAULT '',
    ADD COLUMN `calls_key`         VARCHAR(191) NOT NULL DEFAULT '',
    ADD COLUMN `subscription_type` VARCHAR(64)  NOT NULL DEFAULT '',
    ADD COLUMN `own_error_share`   DOUBLE       NOT NULL DEFAULT 0,
    -- The board is read one role at a time, so the role is part of every query
    -- that lists services.
    ADD INDEX `services_role` (`role_id`);
