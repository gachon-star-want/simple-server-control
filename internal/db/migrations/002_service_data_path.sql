-- App persistent folder (e.g. /data): a named volume mounted there survives redeploys.
ALTER TABLE services ADD COLUMN data_path TEXT NOT NULL DEFAULT '';
