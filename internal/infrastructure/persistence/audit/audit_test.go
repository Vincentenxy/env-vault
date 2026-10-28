package audit

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	auditdomain "env-vault/internal/domain/audit"
)

func auditDB(t *testing.T) (*Repository, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatal(err)
	}
	return NewRepository(db), mock
}

func TestList_GlobalQueryDoesNotDependOnLiveResources(t *testing.T) {
	repo, mock := auditDB(t)
	id := uuid.New()
	mock.ExpectQuery(`SELECT count(*) FROM "audit_event_log"`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(21))
	mock.ExpectQuery(`SELECT * FROM "audit_event_log" ORDER BY create_at DESC, id DESC LIMIT $1 OFFSET $2`).WithArgs(20, 20).
		WillReturnRows(sqlmock.NewRows([]string{"id", "resource_type", "resource_id", "resource_name", "change_detail", "event_detail", "protocol_detail"}).
			AddRow(id.String(), "folder", "deleted-id", "deleted resource", `[]`, `{}`, `{}`))
	items, total, err := repo.List(context.Background(), auditdomain.ListFilter{UserID: "caller", Offset: 20, Limit: 20})
	if err != nil || total != 21 || len(items) != 1 || items[0].ResourceID != "deleted-id" {
		t.Fatalf("unexpected global result: %+v %d %v", items, total, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestList_AllFiltersApplyToCountAndPage(t *testing.T) {
	repo, mock := auditDB(t)
	batch := uuid.New()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	where := ` WHERE resource_type = $1 AND resource_id = $2 AND action_code = $3 AND result_code = $4 AND create_by = $5 AND batch_id = $6 AND correlation_id = $7 AND trace_id = $8 AND create_at >= $9 AND create_at <= $10`
	mock.ExpectQuery(`SELECT count(*) FROM "audit_event_log"`+where).
		WithArgs("folder", "deleted-id", "folder.delete", "success", "operator", batch, "req-1", "trace-1", start, end).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT * FROM "audit_event_log"`+where+` ORDER BY create_at DESC, id DESC LIMIT $11`).
		WithArgs("folder", "deleted-id", "folder.delete", "success", "operator", batch, "req-1", "trace-1", start, end, 20).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	_, _, err := repo.List(context.Background(), auditdomain.ListFilter{
		ResourceType: "folder", ResourceID: "deleted-id", ActionCode: "folder.delete", ResultCode: "success",
		CreateBy: "operator", BatchID: &batch, CorrelationID: "req-1", TraceID: "trace-1", StartAt: &start, EndAt: &end,
		UserID: "caller", Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
