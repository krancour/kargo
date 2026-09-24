// Package postgres provides a Postgres-backed implementation of
// freight.Store.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	"github.com/akuity/kargo/pkg/freight"
)

// maxAliasRetries bounds the number of times Create will regenerate an
// alias after a collision. Generated aliases are drawn from a large,
// effectively-random namespace, so collisions -- let alone repeated ones --
// are rare; this bound only guards against pathological cases.
const maxAliasRetries = 10

// freightTypeMeta is the TypeMeta stamped onto every Freight this Store
// returns.
var freightTypeMeta = metav1.TypeMeta{
	APIVersion: kargoapi.GroupVersion.String(),
	Kind:       "Freight",
}

const (
	insertSQL = `
INSERT INTO freight (project, name, alias, warehouse, created_at, object, status)
VALUES ($1, $2, $3, $4, $5, $6, $7)`

	getSQL = `SELECT object, status, version FROM freight WHERE project = $1 AND name = $2`

	getForUpdateSQL = `
SELECT object, status, version FROM freight
WHERE project = $1 AND name = $2
FOR UPDATE`

	getByAliasSQL = `SELECT object, status, version FROM freight WHERE project = $1 AND alias = $2`

	listSQL = `
SELECT object, status, version FROM freight
WHERE project = $1
  AND ($2 = '' OR warehouse = $2)
  AND (cardinality($3::text[]) = 0 OR warehouse = ANY($3::text[]))
  AND ($4 = '' OR status->'currentlyIn' ? $4)
  AND (cardinality($5::text[]) = 0 OR status->'verifiedIn' ?& $5::text[])
  AND ($6 = '' OR status->'approvedFor' ? $6)
ORDER BY name`

	updateSQL = `
UPDATE freight SET object = $1, status = $2, alias = $3, version = version + 1
WHERE project = $4 AND name = $5`

	deleteSQL = `DELETE FROM freight WHERE project = $1 AND name = $2`

	deleteProjectSQL = `DELETE FROM freight WHERE project = $1`
)

// store is a Postgres-backed implementation of freight.Store.
type store struct {
	pool     *pgxpool.Pool
	newAlias func() string
}

var _ freight.Store = (*store)(nil)

// NewStore returns a new Store backed by the Postgres database described by
// cfg. It opens a connection pool, verifies connectivity, and ensures the
// schema is present before returning.
func NewStore(ctx context.Context, cfg Config) (freight.Store, error) {
	pool, err := pgxpool.New(ctx, cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("error creating connection pool: %w", err)
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("error pinging database: %w", err)
	}
	if err = applySchema(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &store{
		pool:     pool,
		newAlias: freight.NewAliasGenerator(),
	}, nil
}

// applySchema applies schema inside a transaction guarded by
// pg_advisory_xact_lock, so that concurrent component startups serialize
// rather than racing on schema creation.
func applySchema(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("error starting schema transaction: %w", err)
	}
	defer tx.Rollback(ctx) // nolint:errcheck // no-op if the transaction was committed

	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", schemaLockID); err != nil {
		return fmt.Errorf("error acquiring schema lock: %w", err)
	}
	if _, err = tx.Exec(ctx, schema); err != nil {
		return fmt.Errorf("error applying schema: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("error committing schema transaction: %w", err)
	}
	return nil
}

// Create implements freight.Store.
func (s *store) Create(ctx context.Context, f *kargoapi.Freight) error {
	callerAlias := f.Alias
	freight.Prepare(f, s.newAlias)

	for attempt := 0; attempt < maxAliasRetries; attempt++ {
		objectJSON, statusJSON, err := marshalRow(f)
		if err != nil {
			return err
		}
		_, err = s.pool.Exec(
			ctx,
			insertSQL,
			f.Namespace,
			f.Name,
			f.Alias,
			f.Origin.Name,
			f.CreationTimestamp.Time,
			objectJSON,
			statusJSON,
		)
		if err == nil {
			return nil
		}

		mapped := mapPgError(err)
		if errors.Is(mapped, freight.ErrAliasConflict) && callerAlias == "" {
			// The alias was generated on our behalf, so a collision is ours to
			// resolve transparently.
			f.Alias = s.newAlias()
			f.Labels[kargoapi.LabelKeyAlias] = f.Alias
			continue
		}
		return mapped
	}
	return fmt.Errorf(
		"error creating Freight %q in project %q: too many alias conflicts",
		f.Name, f.Namespace,
	)
}

// Get implements freight.Store.
func (s *store) Get(ctx context.Context, project, name string) (*kargoapi.Freight, error) {
	f, _, err := s.get(ctx, project, name)
	return f, err
}

// get returns the Freight with the given project and name, along with its
// current version, for use by callers that need to perform an optimistic
// update.
func (s *store) get(ctx context.Context, project, name string) (*kargoapi.Freight, int64, error) {
	return scanRow(s.pool.QueryRow(ctx, getSQL, project, name))
}

// GetByAlias implements freight.Store.
func (s *store) GetByAlias(ctx context.Context, project, alias string) (*kargoapi.Freight, error) {
	f, _, err := scanRow(s.pool.QueryRow(ctx, getByAliasSQL, project, alias))
	return f, err
}

// List implements freight.Store.
func (s *store) List(
	ctx context.Context,
	project string,
	opts freight.ListOptions,
) ([]kargoapi.Freight, error) {
	rows, err := s.pool.Query(
		ctx,
		listSQL,
		project,
		opts.Warehouse,
		nonNilStrings(opts.Origins),
		opts.CurrentlyIn,
		nonNilStrings(opts.VerifiedIn),
		opts.ApprovedFor,
	)
	if err != nil {
		return nil, fmt.Errorf("error listing Freight in project %q: %w", project, err)
	}
	defer rows.Close()

	var result []kargoapi.Freight
	for rows.Next() {
		var objectJSON, statusJSON []byte
		var version int64
		if err = rows.Scan(&objectJSON, &statusJSON, &version); err != nil {
			return nil, fmt.Errorf("error scanning Freight row: %w", err)
		}
		var f *kargoapi.Freight
		if f, err = unmarshalRow(objectJSON, statusJSON, version); err != nil {
			return nil, err
		}
		result = append(result, *f)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating Freight rows: %w", err)
	}
	return result, nil
}

// nonNilStrings returns ss, or a non-nil empty slice if ss is nil. pgx
// encodes a nil []string as SQL NULL rather than an empty array, which would
// make the cardinality(...) = 0 checks in listSQL evaluate to NULL (and thus
// exclude every row) whenever a filter is left unset.
func nonNilStrings(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}

// UpdateStatus implements freight.Store.
func (s *store) UpdateStatus(
	ctx context.Context,
	project, name string,
	mutate func(*kargoapi.FreightStatus) error,
) (*kargoapi.Freight, error) {
	return s.update(ctx, project, name, func(f *kargoapi.Freight) error {
		return mutate(&f.Status)
	})
}

// UpdateAlias implements freight.Store.
func (s *store) UpdateAlias(ctx context.Context, project, name, alias string) (*kargoapi.Freight, error) {
	return s.update(ctx, project, name, func(f *kargoapi.Freight) error {
		f.Alias = alias
		if f.Labels == nil {
			f.Labels = make(map[string]string, 1)
		}
		f.Labels[kargoapi.LabelKeyAlias] = alias
		return nil
	})
}

// update reads the Freight with the given project and name inside a
// transaction that holds a row lock on it (SELECT ... FOR UPDATE), applies
// mutate to it, and writes the result back within that same transaction.
// The row lock means concurrent callers serialize on this Freight rather
// than racing: each transaction waits for the previous one to commit (or
// roll back) before it can read and update, so no retry loop is needed.
func (s *store) update(
	ctx context.Context,
	project, name string,
	mutate func(*kargoapi.Freight) error,
) (*kargoapi.Freight, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("error starting update transaction: %w", err)
	}
	defer tx.Rollback(ctx) // nolint:errcheck // no-op if the transaction was committed

	f, version, err := scanRow(tx.QueryRow(ctx, getForUpdateSQL, project, name))
	if err != nil {
		return nil, err
	}
	if err = mutate(f); err != nil {
		return nil, err
	}
	objectJSON, statusJSON, err := marshalRow(f)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, updateSQL, objectJSON, statusJSON, f.Alias, project, name); err != nil {
		return nil, mapPgError(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("error committing update transaction: %w", err)
	}

	f.ResourceVersion = strconv.FormatInt(version+1, 10)
	return f, nil
}

// Delete implements freight.Store.
func (s *store) Delete(ctx context.Context, project, name string) error {
	tag, err := s.pool.Exec(ctx, deleteSQL, project, name)
	if err != nil {
		return fmt.Errorf("error deleting Freight %q in project %q: %w", name, project, err)
	}
	if tag.RowsAffected() == 0 {
		return freight.ErrNotFound
	}
	return nil
}

// DeleteProject implements freight.Store.
func (s *store) DeleteProject(ctx context.Context, project string) error {
	if _, err := s.pool.Exec(ctx, deleteProjectSQL, project); err != nil {
		return fmt.Errorf("error deleting Freight in project %q: %w", project, err)
	}
	return nil
}

// marshalRow marshals f into the JSON representations stored in the object
// and status columns. The object column never carries status -- it is
// stored separately so that it can be indexed and queried independently.
func marshalRow(f *kargoapi.Freight) (objectJSON, statusJSON []byte, err error) {
	obj := *f
	obj.Status = kargoapi.FreightStatus{}
	if objectJSON, err = json.Marshal(obj); err != nil {
		return nil, nil, fmt.Errorf("error marshaling Freight object: %w", err)
	}
	if statusJSON, err = json.Marshal(f.Status); err != nil {
		return nil, nil, fmt.Errorf("error marshaling Freight status: %w", err)
	}
	return objectJSON, statusJSON, nil
}

// scanRow scans a single row produced by getSQL or getByAliasSQL into a
// Freight and its version.
func scanRow(row pgx.Row) (*kargoapi.Freight, int64, error) {
	var objectJSON, statusJSON []byte
	var version int64
	if err := row.Scan(&objectJSON, &statusJSON, &version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, freight.ErrNotFound
		}
		return nil, 0, fmt.Errorf("error scanning Freight row: %w", err)
	}
	f, err := unmarshalRow(objectJSON, statusJSON, version)
	return f, version, err
}

// unmarshalRow unmarshals object and status column values, along with the
// row's version, into a Freight.
func unmarshalRow(objectJSON, statusJSON []byte, version int64) (*kargoapi.Freight, error) {
	f := &kargoapi.Freight{}
	if err := json.Unmarshal(objectJSON, f); err != nil {
		return nil, fmt.Errorf("error unmarshaling Freight object: %w", err)
	}
	if err := json.Unmarshal(statusJSON, &f.Status); err != nil {
		return nil, fmt.Errorf("error unmarshaling Freight status: %w", err)
	}
	f.TypeMeta = freightTypeMeta
	f.ResourceVersion = strconv.FormatInt(version, 10)
	return f, nil
}

// mapPgError maps a unique-constraint violation on the freight table to the
// appropriate sentinel error. Any other error is wrapped and returned as-is.
func mapPgError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case "freight_pkey":
			return freight.ErrAlreadyExists
		case "freight_project_alias_key":
			return freight.ErrAliasConflict
		}
	}
	return fmt.Errorf("error executing query: %w", err)
}
