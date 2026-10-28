package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	auditapp "env-vault/internal/application/audit"
	auditdomain "env-vault/internal/domain/audit"
	"env-vault/pkg/page"
	"env-vault/pkg/permission"
	"env-vault/pkg/userctx"
)

func TestAuditGlobalList_NormalizesPaginationAndPreservesIdentity(t *testing.T) {
	for _, tt := range []struct {
		name              string
		request           map[string]any
		wantNum, wantSize int
	}{
		{"default", map[string]any{}, page.DefaultPageNum, page.DefaultPageSize},
		{"negative", map[string]any{"pageNum": -5, "pageSize": -10}, page.DefaultPageNum, page.DefaultPageSize},
		{"zero size", map[string]any{"pageNum": 3, "pageSize": 0}, 3, page.DefaultPageSize},
		{"maximum", map[string]any{"pageNum": 2, "pageSize": 201}, 2, page.MaxPageSize},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			svc := &stubAuditService{globalList: func(ctx context.Context, in auditapp.GlobalListInput) ([]*auditdomain.Event, int64, error) {
				called = true
				if in.PageNum != tt.wantNum || in.PageSize != tt.wantSize || in.ResourceType != "" || in.ResourceID != "" {
					t.Fatalf("unexpected global input: %+v", in)
				}
				if user := userctx.FromContext(ctx); user == nil || user.UserID != "operator-1" {
					t.Fatal("authenticated identity was lost at adapter boundary")
				}
				return nil, 0, nil
			}}
			w := doJSON(t, newAuditTestEngine(svc), http.MethodPost, "/api/v1/audit/global/list", tt.request)
			if w.Code != http.StatusOK || !called || !strings.Contains(w.Body.String(), `"list":[]`) {
				t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAuditGlobalList_BindsFiltersAndSafeDetails(t *testing.T) {
	batch := uuid.New()
	svc := &stubAuditService{globalList: func(_ context.Context, in auditapp.GlobalListInput) ([]*auditdomain.Event, int64, error) {
		if in.BatchID == nil || *in.BatchID != batch || in.CreateBy != "other-user" || in.StartAt == nil || in.EndAt == nil ||
			in.CorrelationID != "request-1" || in.TraceID != "trace-1" || in.ResourceID != "deleted-id" || in.ActionCode != "folder.delete" || in.ResultCode != "success" {
			t.Fatalf("unexpected filters: %+v", in)
		}
		return []*auditdomain.Event{{
			ID: uuid.New(), ResourceType: "folder", ResourceID: "deleted-id", ResourceName: "删除前的名称",
			TraceID: "trace-1", ClientIP: "private-client-ip", UserAgent: "private-agent",
			ChangeDetail: []auditdomain.Change{{Field: "values.prod", Changed: true, Redacted: true}},
		}}, 1, nil
	}}
	w := doJSON(t, newAuditTestEngine(svc), http.MethodPost, "/api/v1/audit/global/list", map[string]any{
		"resourceId": "deleted-id", "actionCode": "folder.delete", "resultCode": "success",
		"createBy": "other-user", "batchId": batch.String(), "correlationId": "request-1", "traceId": "trace-1",
		"startAt": "2026-10-01T00:00:00+08:00", "endAt": "2026-10-28T23:59:59+08:00",
	})
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `"redacted":true`) || !strings.Contains(body, `"traceId":"trace-1"`) ||
		strings.Contains(body, "before") || strings.Contains(body, "private-client-ip") || strings.Contains(body, "private-agent") {
		t.Fatalf("unexpected DTO: %d %s", w.Code, body)
	}
}

func TestAuditGlobalList_RejectsMalformedFiltersBeforeService(t *testing.T) {
	for _, body := range []map[string]any{{"batchId": "bad-uuid"}, {"startAt": "2026/10/28"}, {"pageSize": "all"}} {
		svc := &stubAuditService{globalList: func(context.Context, auditapp.GlobalListInput) ([]*auditdomain.Event, int64, error) {
			t.Fatal("malformed body reached service")
			return nil, 0, nil
		}}
		w := doJSON(t, newAuditTestEngine(svc), http.MethodPost, "/api/v1/audit/global/list", body)
		if w.Code != 400 || decodeBody(t, w)["code"] != float64(400) {
			t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestAuditGlobalList_StandardErrorStatuses(t *testing.T) {
	for _, tt := range []struct {
		err    error
		status int
	}{
		{auditapp.ErrInvalidParam, 400},
		{permission.Unauthenticated("AUTH", "private reason"), 401},
		{permission.Denied("DENIED", "private reason"), 403},
		{permission.EvaluationError("OFFLINE", "private reason", nil), 500},
		{errors.New("private database details"), 500},
	} {
		svc := &stubAuditService{globalList: func(context.Context, auditapp.GlobalListInput) ([]*auditdomain.Event, int64, error) {
			return nil, 0, tt.err
		}}
		w := doJSON(t, newAuditTestEngine(svc), http.MethodPost, "/api/v1/audit/global/list", map[string]any{})
		body := decodeBody(t, w)
		if w.Code != tt.status || body["code"] != float64(tt.status) || body["msg"] != http.StatusText(tt.status) || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
		}
	}
}
