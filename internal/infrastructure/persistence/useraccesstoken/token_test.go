package useraccesstoken

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLockOwnerScansPostgresUUID(t *testing.T) {
	ownerID := uuid.MustParse("c61d4ffd-5468-4914-9637-91b36ea43896")
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("create sql mock: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		DisableAutomaticPing: true,
		Logger:               logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open gorm database: %v", err)
	}

	mock.ExpectQuery("SELECT id FROM user_info WHERE id = $1 AND is_deleted = false FOR UPDATE").
		WithArgs(ownerID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(ownerID.String()))

	locked, err := NewRepository(db).LockOwner(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("lock owner: %v", err)
	}
	if !locked {
		t.Fatal("expected owner row to be locked")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}
