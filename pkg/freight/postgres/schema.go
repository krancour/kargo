package postgres

// schemaLockID is an arbitrary application-specific key used with
// pg_advisory_xact_lock to serialize schema application across concurrent
// component startups.
const schemaLockID = 7201

// schema creates the freight table, its indexes, and the trigger that
// notifies watchers of changes. It is idempotent and safe to run every time
// a Store is constructed.
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

CREATE OR REPLACE FUNCTION freight_notify() RETURNS trigger AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    PERFORM pg_notify('freight', json_build_object('op', TG_OP, 'project', OLD.project, 'name', OLD.name)::text);
    RETURN OLD;
  END IF;
  PERFORM pg_notify('freight', json_build_object('op', TG_OP, 'project', NEW.project, 'name', NEW.name)::text);
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE OR REPLACE TRIGGER freight_notify AFTER INSERT OR UPDATE OR DELETE ON freight
  FOR EACH ROW EXECUTE FUNCTION freight_notify();
`
