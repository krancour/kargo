package postgres

// schemaLockID is an arbitrary application-specific key used with
// pg_advisory_xact_lock to serialize schema application across concurrent
// component startups.
const schemaLockID = 7201

// schema creates the freight table and its indexes, and removes the
// freight_notify trigger and function if a database still has them. It is
// idempotent and safe to run every time a Store is constructed.
const schema = `
CREATE TABLE IF NOT EXISTS freight (
  project    text        NOT NULL,
  name       text        NOT NULL,
  alias      text        NOT NULL,
  warehouse  text        NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  version    bigint      NOT NULL DEFAULT 1,
  object     jsonb       NOT NULL,
  status     jsonb       NOT NULL DEFAULT '{}'::jsonb,
  PRIMARY KEY (project, name),
  CONSTRAINT freight_project_alias_key UNIQUE (project, alias)
);
CREATE INDEX IF NOT EXISTS freight_warehouse_idx ON freight (project, warehouse);
CREATE INDEX IF NOT EXISTS freight_status_idx ON freight USING gin (status);

-- Change notifications are published by the store itself, inside each write
-- transaction. Remove the trigger that once published them from existing
-- databases.
DROP TRIGGER IF EXISTS freight_notify ON freight;
DROP FUNCTION IF EXISTS freight_notify();
`
