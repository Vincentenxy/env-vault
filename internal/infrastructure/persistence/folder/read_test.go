package folder

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestFindByEnvCodeDetectsAmbiguity 验证按环境精确查询且保留两条重名记录供应用层拒绝
func TestFindByEnvCodeDetectsAmbiguity(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	envID := uuid.New()
	mock.ExpectQuery(`SELECT \* FROM "folder_info" WHERE env_id = \$1 AND code = \$2 AND is_deleted = false ORDER BY id ASC LIMIT \$3`).
		WithArgs(envID, "app_%", 2).
		WillReturnRows(sqlmock.NewRows([]string{"id", "env_id", "code"}).
			AddRow(uuid.New(), envID, "app_%").AddRow(uuid.New(), envID, "app_%"))
	folders, err := NewRepository(db).FindByEnvCode(context.Background(), envID, "app_%")
	if err != nil || len(folders) != 2 {
		t.Fatalf("ambiguous code should retain two candidates: count=%d err=%v", len(folders), err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
