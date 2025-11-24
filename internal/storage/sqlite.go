package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Geogboe/boxy/internal/core/sandbox"
	"github.com/Geogboe/boxy/pkg/provider"
)

// SQLiteStore implements storage using database/sql with modernc.org/sqlite (pure Go).
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates a new SQLite storage instance.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	// Ensure directory exists
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return nil, fmt.Errorf("failed to create database directory: %w", err)
		}
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	store := &SQLiteStore{db: db}
	if err := store.migrate(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) migrate() error {
	stmts := []string{
		`PRAGMA foreign_keys = ON;`,
		`CREATE TABLE IF NOT EXISTS resources (
			id TEXT PRIMARY KEY,
			pool_id TEXT NOT NULL,
			sandbox_id TEXT,
			type TEXT,
			state TEXT,
			provider_type TEXT,
			provider_id TEXT,
			spec TEXT,
			metadata TEXT,
			created_at DATETIME,
			updated_at DATETIME,
			expires_at DATETIME
		);`,
		`CREATE INDEX IF NOT EXISTS idx_resources_pool_state ON resources(pool_id, state);`,
		`CREATE TABLE IF NOT EXISTS sandboxes (
			id TEXT PRIMARY KEY,
			name TEXT,
			state TEXT,
			resource_ids TEXT,
			metadata TEXT,
			created_at DATETIME,
			updated_at DATETIME,
			expires_at DATETIME,
			created_by TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_sandboxes_state ON sandboxes(state);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}
	return nil
}

// Close closes the database connection
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// ResourceRepository methods

func (s *SQLiteStore) CreateResource(ctx context.Context, res *provider.Resource) error {
	now := time.Now().UTC()
	res.CreatedAt = now
	res.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO resources
		(id, pool_id, sandbox_id, type, state, provider_type, provider_id, spec, metadata, created_at, updated_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
		res.ID, res.PoolID, nullableString(res.SandboxID), string(res.Type), string(res.State),
		res.ProviderType, res.ProviderID, mustJSON(res.Spec), mustJSON(res.Metadata),
		res.CreatedAt, res.UpdatedAt, nullableTime(res.ExpiresAt),
	)
	return err
}

func (s *SQLiteStore) UpdateResource(ctx context.Context, res *provider.Resource) error {
	res.UpdatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		UPDATE resources SET
			pool_id=?, sandbox_id=?, type=?, state=?, provider_type=?, provider_id=?,
			spec=?, metadata=?, updated_at=?, expires_at=?
		WHERE id=?;`,
		res.PoolID, nullableString(res.SandboxID), string(res.Type), string(res.State),
		res.ProviderType, res.ProviderID, mustJSON(res.Spec), mustJSON(res.Metadata),
		res.UpdatedAt, nullableTime(res.ExpiresAt), res.ID,
	)
	return err
}

func (s *SQLiteStore) DeleteResource(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM resources WHERE id=?;`, id)
	return err
}

func (s *SQLiteStore) GetResourceByID(ctx context.Context, id string) (*provider.Resource, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, pool_id, sandbox_id, type, state, provider_type, provider_id,
		       spec, metadata, created_at, updated_at, expires_at
		FROM resources WHERE id=?;`, id)
	return scanResource(row)
}

func (s *SQLiteStore) GetResourcesByPoolID(ctx context.Context, poolID string) ([]*provider.Resource, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, pool_id, sandbox_id, type, state, provider_type, provider_id,
		       spec, metadata, created_at, updated_at, expires_at
		FROM resources WHERE pool_id=?;`, poolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanResources(rows)
}

func (s *SQLiteStore) GetResourcesByState(ctx context.Context, poolID string, state provider.ResourceState) ([]*provider.Resource, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, pool_id, sandbox_id, type, state, provider_type, provider_id,
		       spec, metadata, created_at, updated_at, expires_at
		FROM resources WHERE pool_id=? AND state=?;`, poolID, string(state))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanResources(rows)
}

func (s *SQLiteStore) CountResourcesByPoolAndState(ctx context.Context, poolID string, state provider.ResourceState) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM resources WHERE pool_id=? AND state=?;`, poolID, string(state)).Scan(&count)
	return count, err
}

func (s *SQLiteStore) GetResourcesBySandboxID(ctx context.Context, sandboxID string) ([]*provider.Resource, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, pool_id, sandbox_id, type, state, provider_type, provider_id,
		       spec, metadata, created_at, updated_at, expires_at
		FROM resources WHERE sandbox_id=?;`, sandboxID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanResources(rows)
}

// Sandbox Repository methods

func (s *SQLiteStore) CreateSandbox(ctx context.Context, sb *sandbox.Sandbox) error {
	now := time.Now().UTC()
	sb.CreatedAt = now
	sb.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sandboxes
		(id, name, state, resource_ids, metadata, created_at, updated_at, expires_at, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);`,
		sb.ID, sb.Name, string(sb.State), mustJSON(sb.ResourceIDs), mustJSON(sb.Metadata),
		sb.CreatedAt, sb.UpdatedAt, nullableTime(sb.ExpiresAt), sb.CreatedBy,
	)
	return err
}

func (s *SQLiteStore) UpdateSandbox(ctx context.Context, sb *sandbox.Sandbox) error {
	sb.UpdatedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		UPDATE sandboxes SET
			name=?, state=?, resource_ids=?, metadata=?, updated_at=?, expires_at=?, created_by=?
		WHERE id=?;`,
		sb.Name, string(sb.State), mustJSON(sb.ResourceIDs), mustJSON(sb.Metadata),
		sb.UpdatedAt, nullableTime(sb.ExpiresAt), sb.CreatedBy, sb.ID,
	)
	return err
}

func (s *SQLiteStore) DeleteSandbox(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sandboxes WHERE id=?;`, id)
	return err
}

func (s *SQLiteStore) GetSandboxByID(ctx context.Context, id string) (*sandbox.Sandbox, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, state, resource_ids, metadata, created_at, updated_at, expires_at, created_by
		FROM sandboxes WHERE id=?;`, id)
	return scanSandbox(row)
}

func (s *SQLiteStore) ListSandboxes(ctx context.Context) ([]*sandbox.Sandbox, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, state, resource_ids, metadata, created_at, updated_at, expires_at, created_by
		FROM sandboxes;`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSandboxes(rows)
}

func (s *SQLiteStore) ListActiveSandboxes(ctx context.Context) ([]*sandbox.Sandbox, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, state, resource_ids, metadata, created_at, updated_at, expires_at, created_by
		FROM sandboxes WHERE state != ?;`, sandbox.StateDestroyed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSandboxes(rows)
}

func (s *SQLiteStore) GetExpiredSandboxes(ctx context.Context) ([]*sandbox.Sandbox, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, state, resource_ids, metadata, created_at, updated_at, expires_at, created_by
		FROM sandboxes WHERE expires_at IS NOT NULL AND expires_at < datetime('now') AND state != ?;`,
		sandbox.StateDestroyed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSandboxes(rows)
}

// Helpers

func mustJSON(v interface{}) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func nullableString(ptr *string) interface{} {
	if ptr == nil {
		return nil
	}
	return *ptr
}

func nullableTime(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func scanResource(row *sql.Row) (*provider.Resource, error) {
	var (
		id, poolID, sandboxID    sql.NullString
		typ, state               string
		providerType, providerID sql.NullString
		specJSON, metaJSON       sql.NullString
		createdAt, updatedAt     time.Time
		expiresAt                sql.NullTime
	)
	err := row.Scan(&id, &poolID, &sandboxID, &typ, &state, &providerType, &providerID, &specJSON, &metaJSON, &createdAt, &updatedAt, &expiresAt)
	if err != nil {
		return nil, err
	}
	return buildResource(id, poolID, sandboxID, typ, state, providerType, providerID, specJSON, metaJSON, createdAt, updatedAt, expiresAt)
}

func scanResources(rows *sql.Rows) ([]*provider.Resource, error) {
	var out []*provider.Resource
	for rows.Next() {
		var (
			id, poolID, sandboxID    sql.NullString
			typ, state               string
			providerType, providerID sql.NullString
			specJSON, metaJSON       sql.NullString
			createdAt, updatedAt     time.Time
			expiresAt                sql.NullTime
		)
		if err := rows.Scan(&id, &poolID, &sandboxID, &typ, &state, &providerType, &providerID, &specJSON, &metaJSON, &createdAt, &updatedAt, &expiresAt); err != nil {
			return nil, err
		}
		res, err := buildResource(id, poolID, sandboxID, typ, state, providerType, providerID, specJSON, metaJSON, createdAt, updatedAt, expiresAt)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, rows.Err()
}

func buildResource(id, poolID, sandboxID sql.NullString, typ, state string, providerType, providerID sql.NullString, specJSON, metaJSON sql.NullString, createdAt, updatedAt time.Time, expiresAt sql.NullTime) (*provider.Resource, error) {
	res := &provider.Resource{
		ID:           id.String,
		PoolID:       poolID.String,
		Type:         provider.ResourceType(typ),
		State:        provider.ResourceState(state),
		ProviderType: providerType.String,
		ProviderID:   providerID.String,
		Spec:         provider.JSONMap{},
		Metadata:     provider.JSONMap{},
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}
	if sandboxID.Valid {
		res.SandboxID = &sandboxID.String
	}
	if expiresAt.Valid {
		t := expiresAt.Time
		res.ExpiresAt = &t
	}
	if specJSON.Valid {
		_ = json.Unmarshal([]byte(specJSON.String), &res.Spec)
	}
	if metaJSON.Valid {
		_ = json.Unmarshal([]byte(metaJSON.String), &res.Metadata)
	}
	return res, nil
}

func scanSandbox(row *sql.Row) (*sandbox.Sandbox, error) {
	var (
		id, name, resourceIDs, metadata, createdBy sql.NullString
		state                                      string
		createdAt, updatedAt                       time.Time
		expiresAt                                  sql.NullTime
	)
	err := row.Scan(&id, &name, &state, &resourceIDs, &metadata, &createdAt, &updatedAt, &expiresAt, &createdBy)
	if err != nil {
		return nil, err
	}
	return buildSandbox(id, name, state, resourceIDs, metadata, createdAt, updatedAt, expiresAt, createdBy)
}

func scanSandboxes(rows *sql.Rows) ([]*sandbox.Sandbox, error) {
	var out []*sandbox.Sandbox
	for rows.Next() {
		var (
			id, name, resourceIDs, metadata, createdBy sql.NullString
			state                                      string
			createdAt, updatedAt                       time.Time
			expiresAt                                  sql.NullTime
		)
		if err := rows.Scan(&id, &name, &state, &resourceIDs, &metadata, &createdAt, &updatedAt, &expiresAt, &createdBy); err != nil {
			return nil, err
		}
		sb, err := buildSandbox(id, name, state, resourceIDs, metadata, createdAt, updatedAt, expiresAt, createdBy)
		if err != nil {
			return nil, err
		}
		out = append(out, sb)
	}
	return out, rows.Err()
}

func buildSandbox(id, name sql.NullString, state string, resourceIDs, metadata sql.NullString, createdAt, updatedAt time.Time, expiresAt sql.NullTime, createdBy sql.NullString) (*sandbox.Sandbox, error) {
	sb := &sandbox.Sandbox{
		ID:        id.String,
		Name:      name.String,
		State:     sandbox.SandboxState(state),
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
	if resourceIDs.Valid {
		_ = json.Unmarshal([]byte(resourceIDs.String), &sb.ResourceIDs)
	}
	if metadata.Valid {
		_ = json.Unmarshal([]byte(metadata.String), &sb.Metadata)
	}
	if expiresAt.Valid {
		t := expiresAt.Time
		sb.ExpiresAt = &t
	}
	if createdBy.Valid {
		sb.CreatedBy = createdBy.String
	}
	return sb, nil
}

// Ensure interfaces are satisfied.
var _ Store = (*SQLiteStore)(nil)
var _ ResourceRepository = (*SQLiteStore)(nil)
var _ SandboxRepository = (*SQLiteStore)(nil)
