package handler

import (
	"context"
	tagapp "env-vault/internal/application/tag"
	domain "env-vault/internal/domain/tag"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http"
	"testing"
)

type tagServiceStub struct {
	tagapp.IService
	captured domain.Filter
}

func (s *tagServiceStub) List(ctx context.Context, filter domain.Filter) ([]*domain.Tag, int64, error) {
	s.captured = filter
	return make([]*domain.Tag, 0), 0, nil
}
func TestTagListPagination(t *testing.T) {
	for _, tc := range []struct{ pageNum, pageSize, wantNum, wantSize int }{{0, 0, 1, 20}, {-2, 10, 1, 10}, {2, 0, 2, 20}, {1, 500, 1, 200}} {
		t.Run(fmt.Sprintf("%d_%d", tc.pageNum, tc.pageSize), func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			router := gin.New()
			svc := &tagServiceStub{}
			router.POST("/tag/list", NewTagHandler(svc).List)
			tenantID := uuid.New()
			result := doJSONP(t, router, http.MethodPost, "/tag/list", map[string]any{"tenantId": tenantID, "pageNum": tc.pageNum, "pageSize": tc.pageSize})
			if svc.captured.TenantID != tenantID || svc.captured.PageNum != tc.wantNum || svc.captured.PageSize != tc.wantSize {
				t.Fatalf("filter: %+v", svc.captured)
			}
			body := decodeBody(t, result)
			if body["code"].(float64) != 0 {
				t.Fatalf("response: %s", result.Body.String())
			}
			data := body["data"].(map[string]any)
			if _, ok := data["list"].([]any); !ok {
				t.Fatalf("empty list must be array: %s", result.Body.String())
			}
		})
	}
}
