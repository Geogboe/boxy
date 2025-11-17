package storage

import (
	"context"
	"fmt"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Geogboe/boxy/internal/core/resource"
	"github.com/Geogboe/boxy/internal/core/sandbox"
)

// SQLiteStore implements storage using SQLite with GORM
type SQLiteStore struct {
	db *gorm.DB
}

// NewSQLiteStore creates a new SQLite storage instance
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	store := &SQLiteStore{db: db}

	// Auto-migrate schema
	if err := store.Migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate schema: %w", err)
	}

	return store, nil
}

// Migrate runs database migrations
func (s *SQLiteStore) Migrate() error {
	return s.db.AutoMigrate(&resource.Resource{}, &sandbox.Sandbox{})
}

// Close closes the database connection
func (s *SQLiteStore) Close() error {
	db, err := s.db.DB()
	if err != nil {
		return err
	}
	return db.Close()
}

// DB returns the underlying gorm.DB for testing
func (s *SQLiteStore) DB() *gorm.DB {
	return s.db
}

// ResourceRepository methods

// CreateResource creates a new resource
func (s *SQLiteStore) CreateResource(ctx context.Context, res *resource.Resource) error {
	return s.db.WithContext(ctx).Create(res).Error
}

// UpdateResource updates an existing resource
func (s *SQLiteStore) UpdateResource(ctx context.Context, res *resource.Resource) error {
	return s.db.WithContext(ctx).Save(res).Error
}

// DeleteResource deletes a resource by ID
func (s *SQLiteStore) DeleteResource(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&resource.Resource{}, "id = ?", id).Error
}

// GetResourceByID retrieves a resource by ID
func (s *SQLiteStore) GetResourceByID(ctx context.Context, id string) (*resource.Resource, error) {
	var res resource.Resource
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&res).Error; err != nil {
		return nil, err
	}
	return &res, nil
}

// GetResourcesByPoolID retrieves all resources for a pool
func (s *SQLiteStore) GetResourcesByPoolID(ctx context.Context, poolID string) ([]*resource.Resource, error) {
	var resources []*resource.Resource
	if err := s.db.WithContext(ctx).Where("pool_id = ?", poolID).Find(&resources).Error; err != nil {
		return nil, err
	}
	return resources, nil
}

// GetResourcesByState retrieves resources by pool and state
func (s *SQLiteStore) GetResourcesByState(ctx context.Context, poolID string, state resource.ResourceState) ([]*resource.Resource, error) {
	var resources []*resource.Resource
	if err := s.db.WithContext(ctx).Where("pool_id = ? AND state = ?", poolID, state).Find(&resources).Error; err != nil {
		return nil, err
	}
	return resources, nil
}

// CountResourcesByPoolAndState counts resources by pool and state
func (s *SQLiteStore) CountResourcesByPoolAndState(ctx context.Context, poolID string, state resource.ResourceState) (int, error) {
	var count int64
	if err := s.db.WithContext(ctx).Model(&resource.Resource{}).Where("pool_id = ? AND state = ?", poolID, state).Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

// GetResourcesBySandboxID retrieves all resources for a sandbox
func (s *SQLiteStore) GetResourcesBySandboxID(ctx context.Context, sandboxID string) ([]*resource.Resource, error) {
	var resources []*resource.Resource
	if err := s.db.WithContext(ctx).Where("sandbox_id = ?", sandboxID).Find(&resources).Error; err != nil {
		return nil, err
	}
	return resources, nil
}

// Sandbox Repository methods

// CreateSandbox creates a new sandbox
func (s *SQLiteStore) CreateSandbox(ctx context.Context, sb *sandbox.Sandbox) error {
	return s.db.WithContext(ctx).Create(sb).Error
}

// UpdateSandbox updates an existing sandbox
func (s *SQLiteStore) UpdateSandbox(ctx context.Context, sb *sandbox.Sandbox) error {
	return s.db.WithContext(ctx).Save(sb).Error
}

// DeleteSandbox deletes a sandbox by ID
func (s *SQLiteStore) DeleteSandbox(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Delete(&sandbox.Sandbox{}, "id = ?", id).Error
}

// GetSandboxByID retrieves a sandbox by ID
func (s *SQLiteStore) GetSandboxByID(ctx context.Context, id string) (*sandbox.Sandbox, error) {
	var sb sandbox.Sandbox
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&sb).Error; err != nil {
		return nil, err
	}
	return &sb, nil
}

// ListSandboxes retrieves all sandboxes
func (s *SQLiteStore) ListSandboxes(ctx context.Context) ([]*sandbox.Sandbox, error) {
	var sandboxes []*sandbox.Sandbox
	if err := s.db.WithContext(ctx).Find(&sandboxes).Error; err != nil {
		return nil, err
	}
	return sandboxes, nil
}

// ListActiveSandboxes retrieves all non-destroyed sandboxes
func (s *SQLiteStore) ListActiveSandboxes(ctx context.Context) ([]*sandbox.Sandbox, error) {
	var sandboxes []*sandbox.Sandbox
	if err := s.db.WithContext(ctx).Where("state != ?", sandbox.StateDestroyed).Find(&sandboxes).Error; err != nil {
		return nil, err
	}
	return sandboxes, nil
}

// GetExpiredSandboxes retrieves sandboxes past their expiration time
func (s *SQLiteStore) GetExpiredSandboxes(ctx context.Context) ([]*sandbox.Sandbox, error) {
	var sandboxes []*sandbox.Sandbox
	if err := s.db.WithContext(ctx).
		Where("expires_at IS NOT NULL AND expires_at < datetime('now') AND state != ?", sandbox.StateDestroyed).
		Find(&sandboxes).Error; err != nil {
		return nil, err
	}
	return sandboxes, nil
}
