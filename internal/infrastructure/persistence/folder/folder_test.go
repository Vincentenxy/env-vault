package folder

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	folderdomain "env-vault/internal/domain/folder"
)

func TestListByGroupIDsTypeFilterIncludesCountAndPagination(t *testing.T) {
	for _, category := range []string{"", "common", "customer"} {
		t.Run("type="+category, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			groupID := uuid.New()
			count := mock.ExpectQuery(`SELECT COUNT.*FROM "folder_info"`)
			codes := mock.ExpectQuery(`SELECT DISTINCT code.*ORDER BY code ASC LIMIT .* OFFSET`)
			groups := mock.ExpectQuery(`SELECT DISTINCT group_id.*FROM "folder_info"`)
			if category == "" {
				count.WithArgs(groupID)
				codes.WithArgs(groupID, 5, 5)
				groups.WithArgs(groupID, "application")
			} else {
				count.WithArgs(groupID, category)
				codes.WithArgs(groupID, category, 5, 5)
				groups.WithArgs(groupID, "application", category)
			}
			count.WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(11))
			codes.WillReturnRows(sqlmock.NewRows([]string{"code"}).AddRow("application"))
			groups.WillReturnRows(sqlmock.NewRows([]string{"group_id"}).AddRow(groupID))
			mock.ExpectQuery(`(?s)WITH representative AS.*type = .*FROM representative`).
				WithArgs(groupID, category, category).
				WillReturnRows(sqlmock.NewRows([]string{"id", "group_id", "code", "type"}).
					AddRow(uuid.New(), groupID, "application", category))

			items, total, err := NewRepository(db).ListByGroupIDs(context.Background(), []uuid.UUID{groupID}, folderdomain.ListFilter{
				Type: category, PageNum: 2, PageSize: 5,
			})
			if err != nil {
				t.Fatal(err)
			}
			if total != 11 || len(items) != 1 || items[0].Type != category {
				t.Fatalf("unexpected filtered result: total=%d items=%+v", total, items)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
