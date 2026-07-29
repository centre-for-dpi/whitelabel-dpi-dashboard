-- The demand side.
--
-- A requestor is the same record as an issuer — it has availability, an error
-- rate, a history — because it is the same exchange seen from the other end.
-- So it lives in the same table, and these columns say which end it is and, for
-- a requestor, what it consumes.
--
-- Every column is nullable-with-default rather than NOT NULL, so a store
-- written by an earlier version keeps loading: an existing row is an issuer
-- that consumes nothing, which is exactly what the empty strings say.

ALTER TABLE services ADD COLUMN role_id           TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN sector_id         TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN calls_key         TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN subscription_type TEXT NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN own_error_share   REAL NOT NULL DEFAULT 0;

-- The board is read one role at a time, so the role is part of every query
-- that lists services.
CREATE INDEX IF NOT EXISTS services_role ON services (role_id);
