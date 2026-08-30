package handler

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	auditapp "env-vault/internal/application/audit"
	auditdomain "env-vault/internal/domain/audit"
	"env-vault/pkg/page"
	"env-vault/pkg/userctx"
)

type stubAuditService struct {
	list func(context.Context, auditapp.ListInput) ([]*auditdomain.Event, int64, error)
}

func (s *stubAuditService) List(ctx context.Context, in auditapp.ListInput) ([]*auditdomain.Event, int64, error) {
	if s.list != nil {
		return s.list(ctx, in)
	}
	return nil, 0, nil
}

func newAuditTestEngine(svc auditapp.IService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		userctx.Set(c, &userctx.User{UserID: "operator-1", Name: "操作人"})
		c.Next()
	})
	r.POST("/api/v1/audit/list", NewAuditHandler(svc).List)
	return r
}

func TestAuditHandler_ListReturnsSafePagedEvents(t *testing.T) {
	resourceID := uuid.NewString()
	eventID := uuid.New()
	now := time.Now().UTC().Truncate(time.Second)
	svc := &stubAuditService{list: func(_ context.Context, in auditapp.ListInput) ([]*auditdomain.Event, int64, error) {
		if in.ResourceType != "secret" || in.ResourceID != resourceID || in.UserID != "operator-1" {
			t.Fatalf("unexpected input: %+v", in)
		}
		return []*auditdomain.Event{{
			ID: eventID, ActionCode: "secret.update", ResultCode: auditdomain.ResultSuccess,
			ResourceType: "secret", ResourceID: resourceID, ResourceName: "DB_HOST",
			ChangeDetail: []auditdomain.Change{{Field: "values.prod", Changed: true, Redacted: true}},
			EventDetail:  map[string]any{"versions": map[string]any{"prod": 2}},
			CreateBy:     "operator-1", CreateByName: "操作人", CreateAt: now,
		}}, 1, nil
	}}

	w := doJSON(t, newAuditTestEngine(svc), http.MethodPost, "/api/v1/audit/list", map[string]any{
		"resourceType": "secret", "resourceId": resourceID,
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
	data := body["data"].(map[string]any)
	if data["total"].(float64) != 1 {
		t.Fatalf("unexpected total: %+v", data)
	}
	item := data["list"].([]any)[0].(map[string]any)
	if item["id"] != eventID.String() || item["resourceId"] != resourceID {
		t.Fatalf("unexpected item: %+v", item)
	}
	change := item["changeDetail"].([]any)[0].(map[string]any)
	if change["redacted"] != true {
		t.Fatalf("secret change must stay redacted: %+v", change)
	}
	if _, exists := change["before"]; exists {
		t.Fatalf("redacted change leaked before value: %+v", change)
	}
}

func TestAuditHandler_ListNormalizesPagination(t *testing.T) {
	tests := []struct {
		name             string
		request          map[string]any
		expectedPageNum  int
		expectedPageSize int
	}{
		{name: "missing", request: map[string]any{}, expectedPageNum: page.DefaultPageNum, expectedPageSize: page.DefaultPageSize},
		{name: "negative", request: map[string]any{"pageNum": -3, "pageSize": -5}, expectedPageNum: page.DefaultPageNum, expectedPageSize: page.DefaultPageSize},
		{name: "zero page size", request: map[string]any{"pageNum": 2, "pageSize": 0}, expectedPageNum: 2, expectedPageSize: page.DefaultPageSize},
		{name: "over max", request: map[string]any{"pageNum": 2, "pageSize": page.MaxPageSize + 1}, expectedPageNum: 2, expectedPageSize: page.MaxPageSize},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			svc := &stubAuditService{list: func(_ context.Context, in auditapp.ListInput) ([]*auditdomain.Event, int64, error) {
				called = true
				if in.PageNum != test.expectedPageNum || in.PageSize != test.expectedPageSize {
					t.Fatalf("pagination not normalized: %+v", in)
				}
				return nil, 0, nil
			}}
			body := map[string]any{"resourceType": "secret", "resourceId": uuid.NewString()}
			for key, value := range test.request {
				body[key] = value
			}
			w := doJSON(t, newAuditTestEngine(svc), http.MethodPost, "/api/v1/audit/list", body)
			if w.Code != http.StatusOK || !called {
				t.Fatalf("unexpected response: status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
