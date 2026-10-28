package secret

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	secretdomain "env-vault/internal/domain/secret"
)

// TestClientReadConstrainsFolderEnvironmentAndExactKeys 防止目录 ID 查询回退到其他目录、环境或模糊匹配
func TestClientReadConstrainsFolderEnvironmentAndExactKeys(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	projectID, folderID := uuid.New(), uuid.New()
	mock.ExpectQuery(`(?s)SELECT secret_info\.\*.*JOIN folder_info.*folder_info.is_deleted = false.*JOIN environment_info.*environment_info.is_deleted = false.*secret_info.is_deleted = false.*environment_info.project_id = .*folder_info.code = .*environment_info.code IN .*secret_info.folder_id IN .*secret_info.key IN .*ORDER BY`).
		WithArgs(projectID, "global", "dev", folderID, "DB_%密码").
		WillReturnRows(sqlmock.NewRows([]string{"id", "folder_id", "env_code", "key"}).
			AddRow(uuid.New(), folderID, "dev", "DB_%密码"))
	items, err := NewRepository(db).ListByProjectFolder(context.Background(), secretdomain.ProjectFolderListFilter{
		ProjectID: projectID, FolderCode: "global", EnvCodes: []string{"dev"}, FolderIDs: []uuid.UUID{folderID}, Keys: []string{"DB_%密码"},
	})
	if err != nil || len(items) != 1 || items[0].FolderID != folderID {
		t.Fatalf("scoped query failed: %+v %v", items, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
