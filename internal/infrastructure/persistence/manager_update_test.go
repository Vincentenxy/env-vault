package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	orgdomain "env-vault/internal/domain/organization"
	projdomain "env-vault/internal/domain/project"
	tenantdomain "env-vault/internal/domain/tenant"
	folderrepo "env-vault/internal/infrastructure/persistence/folder"
	orgrepo "env-vault/internal/infrastructure/persistence/organization"
	projrepo "env-vault/internal/infrastructure/persistence/project"
	tenantrepo "env-vault/internal/infrastructure/persistence/tenant"
)

func TestManagerIsIncludedInResourceUpdates(t *testing.T) {
	const manager = "manager-new"
	now := time.Now()

	tests := []struct {
		name   string
		update func(*gorm.DB) error
	}{
		{
			name: "tenant",
			update: func(db *gorm.DB) error {
				return tenantrepo.NewRepository(db).Update(context.Background(), &tenantdomain.Tenant{
					ID: uuid.New(), Name: "tenant", Manager: manager, UpdateAt: now,
				})
			},
		},
		{
			name: "organization",
			update: func(db *gorm.DB) error {
				return orgrepo.NewRepository(db).Update(context.Background(), &orgdomain.Organization{
					ID: uuid.New(), Name: "organization", Manager: manager, UpdateAt: now,
				})
			},
		},
		{
			name: "folder",
			update: func(db *gorm.DB) error {
				_, err := folderrepo.NewRepository(db).UpdateByGroupID(
					context.Background(), uuid.New(), "folder", "", manager, nil, "operator", now,
				)
				return err
			},
		},
		{
			name: "project",
			update: func(db *gorm.DB) error {
				return projrepo.NewRepository(db).Update(context.Background(), &projdomain.Project{
					ID: uuid.New(), Name: "project", Manager: manager, UpdateAt: now,
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := gorm.Open(postgres.New(postgres.Config{
				DSN:                  "host=localhost user=test dbname=test sslmode=disable",
				PreferSimpleProtocol: true,
			}), &gorm.Config{
				DryRun:                 true,
				DisableAutomaticPing:   true,
				SkipDefaultTransaction: true,
			})
			if err != nil {
				t.Fatalf("open dry-run database: %v", err)
			}

			var updateFields map[string]any
			if err := db.Callback().Update().Before("gorm:update").Register(
				"test:capture_update_fields",
				func(tx *gorm.DB) {
					if fields, ok := tx.Statement.Dest.(map[string]any); ok {
						updateFields = fields
					}
				},
			); err != nil {
				t.Fatalf("register update callback: %v", err)
			}

			if err := tt.update(db); err != nil {
				t.Fatalf("update resource: %v", err)
			}
			if got, ok := updateFields["manager"]; !ok || got != manager {
				t.Fatalf("manager update field = %v, present = %v", got, ok)
			}
		})
	}
}
