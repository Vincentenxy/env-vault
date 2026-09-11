package tag

import (
	"context"
	domain "env-vault/internal/domain/tag"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"regexp"
	"testing"
	"time"
)

func TestCreateFalseAndDuplicateCode(t *testing.T) {
	for _, affected := range []int64{0, 1} {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		item := &domain.Tag{ID: uuid.New(), TenantID: uuid.New(), Code: "password", Name: "密码", AllowValueSearch: false, CreateBy: "user", UpdateBy: "user", CreateAt: time.Now(), UpdateAt: time.Now()}
		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO "tag_info" ("id","tenant_id","code","name","remark","allow_value_search","create_by","update_by","create_at","update_at") VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT ("tenant_id","code") WHERE is_deleted = false DO NOTHING`)).WithArgs(item.ID, item.TenantID, item.Code, item.Name, "", false, "user", "user", item.CreateAt, item.UpdateAt).WillReturnResult(sqlmock.NewResult(0, affected))
		mock.ExpectCommit()
		err = NewRepository(orm).Create(context.Background(), item)
		if affected == 0 && !errors.Is(err, domain.ErrCodeExists) {
			t.Fatalf("expected duplicate: %v", err)
		}
		if affected == 1 && err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}
}

func TestUpdatePersistsFalseAndScopesTenant(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	item := &domain.Tag{ID: uuid.New(), TenantID: uuid.New(), Name: "password", AllowValueSearch: false, UpdateBy: "user", UpdateAt: time.Now()}
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE "tag_info" SET "allow_value_search"=$1,"name"=$2,"remark"=$3,"update_at"=$4,"update_by"=$5 WHERE is_deleted = false AND (tenant_id = $6 AND id = $7)`)).WithArgs(false, item.Name, "", item.UpdateAt, "user", item.TenantID, item.ID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := NewRepository(orm).Update(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
