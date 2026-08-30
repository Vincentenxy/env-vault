package audit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	auditdomain "env-vault/internal/domain/audit"
)

type stubRepository struct {
	createBatch func(context.Context, []*auditdomain.Event) error
	list        func(context.Context, auditdomain.ListFilter) ([]*auditdomain.Event, int64, error)
}

func (s *stubRepository) CreateBatch(ctx context.Context, events []*auditdomain.Event) error {
	if s.createBatch != nil {
		return s.createBatch(ctx, events)
	}
	return nil
}

func (s *stubRepository) List(ctx context.Context, filter auditdomain.ListFilter) ([]*auditdomain.Event, int64, error) {
	if s.list != nil {
		return s.list(ctx, filter)
	}
	return nil, 0, nil
}

func TestService_Record_EnrichesTrustedEntryContext(t *testing.T) {
	now := time.Date(2026, 8, 28, 10, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	var captured *auditdomain.Event
	repo := &stubRepository{createBatch: func(_ context.Context, events []*auditdomain.Event) error {
		if len(events) != 1 {
			t.Fatalf("expected one event, got %d", len(events))
		}
		captured = events[0]
		return nil
	}}
	svc := NewService(repo, nil)
	svc.now = func() time.Time { return now }
	ctx := WithEntryContext(context.Background(), EntryContext{
		EventSource: auditdomain.EventSourceServer, EntryType: auditdomain.EntryTypeHTTP,
		CallerType: auditdomain.CallerTypeSDK, CallerName: "env-vault-go", CallerVersion: "1.2.0",
		OperationName: "POST /api/v1/secret/update", CorrelationID: "request-1",
		ProtocolStatus: "200", ProtocolDetail: map[string]any{"method": "POST"},
		ActorType: auditdomain.ActorTypeUser, ActorID: "user-1", ActorName: "测试用户",
	})
	err := svc.Record(ctx, &auditdomain.Event{
		ActionCode: "secret.update", ResultCode: auditdomain.ResultSuccess,
		ResourceType: "secret", ResourceID: uuid.NewString(),
		ChangeDetail: []auditdomain.Change{{Field: "values.dev", Changed: true, Redacted: true}},
	})
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	if captured.ID == uuid.Nil || captured.CreateAt != now || captured.ExpireAt != now.AddDate(1, 0, 0) {
		t.Fatalf("timestamps/id not enriched: %+v", captured)
	}
	if captured.EntryType != auditdomain.EntryTypeHTTP || captured.CallerType != auditdomain.CallerTypeSDK ||
		captured.CallerName != "env-vault-go" || captured.CorrelationID != "request-1" {
		t.Fatalf("entry context not enriched: %+v", captured)
	}
	if captured.CreateBy != "user-1" || captured.CreateByName != "测试用户" {
		t.Fatalf("actor not enriched: %+v", captured)
	}
}

func TestService_Record_RejectsSensitivePayload(t *testing.T) {
	called := false
	svc := NewService(&stubRepository{createBatch: func(context.Context, []*auditdomain.Event) error {
		called = true
		return nil
	}}, nil)

	tests := []struct {
		name  string
		event *auditdomain.Event
	}{
		{
			name: "secret value in event detail",
			event: &auditdomain.Event{
				ActionCode: "secret.update", ResultCode: auditdomain.ResultSuccess,
				ResourceType: "secret", ResourceID: uuid.NewString(),
				EventDetail: map[string]any{"value": "must-not-be-recorded"},
			},
		},
		{
			name: "redacted value still carries before",
			event: &auditdomain.Event{
				ActionCode: "secret.update", ResultCode: auditdomain.ResultSuccess,
				ResourceType: "secret", ResourceID: uuid.NewString(),
				ChangeDetail: []auditdomain.Change{{
					Field: "values.prod", Before: "must-not-be-recorded", Changed: true, Redacted: true,
				}},
			},
		},
		{
			name: "ciphertext in nested detail",
			event: &auditdomain.Event{
				ActionCode: "secret.create", ResultCode: auditdomain.ResultSuccess,
				ResourceType: "secret", ResourceID: uuid.NewString(),
				EventDetail: map[string]any{"payload": map[string]any{"valueCiphertext": "cipher"}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := svc.Record(context.Background(), test.event); !errors.Is(err, ErrSensitivePayload) {
				t.Fatalf("expected ErrSensitivePayload, got %v", err)
			}
		})
	}
	if called {
		t.Fatal("repository must not be called for sensitive events")
	}
}

func TestService_List_PassesNormalizedPaginationAndResource(t *testing.T) {
	repo := &stubRepository{list: func(_ context.Context, filter auditdomain.ListFilter) ([]*auditdomain.Event, int64, error) {
		if filter.ResourceType != "secret" || filter.ResourceID != "group-1" || filter.UserID != "user-1" {
			t.Fatalf("unexpected filter: %+v", filter)
		}
		if filter.Offset != 20 || filter.Limit != 10 {
			t.Fatalf("unexpected pagination: %+v", filter)
		}
		return []*auditdomain.Event{{ID: uuid.New()}}, 31, nil
	}}
	items, total, err := NewService(repo, nil).List(context.Background(), ListInput{
		ResourceType: " secret ", ResourceID: " group-1 ", UserID: " user-1 ",
		PageNum: 3, PageSize: 10,
	})
	if err != nil || total != 31 || len(items) != 1 {
		t.Fatalf("unexpected result: items=%d total=%d err=%v", len(items), total, err)
	}
}
