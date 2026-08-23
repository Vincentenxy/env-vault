package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	tenantapp "env-vault/internal/application/tenant"
	orgdomain "env-vault/internal/domain/organization"
	tenantdomain "env-vault/internal/domain/tenant"
	"env-vault/pkg/userctx"
)

type stubTenantService struct {
	listWithOrgProjects func(context.Context, tenantapp.WithOrgProjectsInput) ([]*tenantdomain.TenantWithOrgProjects, error)
	list                func(context.Context, tenantapp.ListInput) ([]*tenantdomain.Tenant, int64, error)
}

func (s *stubTenantService) Create(context.Context, tenantapp.CreateInput, string) (*tenantdomain.Tenant, error) {
	return nil, nil
}
func (s *stubTenantService) Update(context.Context, tenantapp.UpdateInput, string) (*tenantdomain.Tenant, error) {
	return nil, nil
}
func (s *stubTenantService) Delete(context.Context, uuid.UUID, string) error { return nil }
func (s *stubTenantService) GetByID(context.Context, uuid.UUID) (*tenantdomain.Tenant, error) {
	return nil, nil
}

func (s *stubTenantService) List(ctx context.Context, in tenantapp.ListInput) ([]*tenantdomain.Tenant, int64, error) {
	if s.list != nil {
		return s.list(ctx, in)
	}
	return nil, 0, nil
}
func (s *stubTenantService) ListWithOrgProjects(ctx context.Context, in tenantapp.WithOrgProjectsInput) ([]*tenantdomain.TenantWithOrgProjects, error) {
	if s.listWithOrgProjects != nil {
		return s.listWithOrgProjects(ctx, in)
	}
	return nil, nil
}

func newTenantTestEngine(svc tenantapp.IService, user *userctx.User) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if user != nil {
			userctx.Set(c, user)
		}
		c.Next()
	})
	h := NewTenantHandler(svc)
	r.POST("/api/v1/tenant/list", h.List)
	r.GET("/api/v1/tenant/withOrgProject", h.WithOrgProject)
	return r
}

func TestTenantHandler_List_SummaryFields(t *testing.T) {
	tenantID := uuid.New()
	svc := &stubTenantService{
		list: func(context.Context, tenantapp.ListInput) ([]*tenantdomain.Tenant, int64, error) {
			return []*tenantdomain.Tenant{{
				ID: tenantID, Code: "tenant-1", Name: "租户一", Manager: "manager-1",
				ManagerName: "管理员一", OrgCount: 3, MemberCount: 12,
			}}, 1, nil
		},
	}
	r := newTenantTestEngine(svc, &userctx.User{UserID: "u-1"})
	w := doJSONP(t, r, http.MethodPost, "/api/v1/tenant/list", map[string]any{})
	body := decodeBody(t, w)
	data := body["data"].(map[string]any)
	item := data["list"].([]any)[0].(map[string]any)
	if item["orgCount"] != float64(3) || item["memberCount"] != float64(12) || item["managerName"] != "管理员一" {
		t.Fatalf("unexpected tenant summary fields: %+v", item)
	}
}

func TestTenantHandler_List_NormalizesPagination(t *testing.T) {
	tests := []struct {
		name         string
		body         map[string]any
		wantPageNum  int
		wantPageSize int
	}{
		{name: "defaults", body: map[string]any{}, wantPageNum: 1, wantPageSize: 20},
		{name: "negative page and zero size", body: map[string]any{"pageNum": -3, "pageSize": 0}, wantPageNum: 1, wantPageSize: 20},
		{name: "clamp max size", body: map[string]any{"pageNum": 2, "pageSize": 9999}, wantPageNum: 2, wantPageSize: 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &stubTenantService{list: func(_ context.Context, in tenantapp.ListInput) ([]*tenantdomain.Tenant, int64, error) {
				if in.PageNum != tt.wantPageNum || in.PageSize != tt.wantPageSize {
					t.Fatalf("unexpected normalized pagination: %+v", in)
				}
				return []*tenantdomain.Tenant{}, 0, nil
			}}
			r := newTenantTestEngine(svc, &userctx.User{UserID: "u-1"})
			w := doJSONP(t, r, http.MethodPost, "/api/v1/tenant/list", tt.body)
			body := decodeBody(t, w)
			if body["code"].(float64) != 0 {
				t.Fatalf("expected success, got %+v", body)
			}
		})
	}
}

func TestTenantHandler_WithOrgProject_Success(t *testing.T) {
	tenantID := uuid.New()
	orgID := uuid.New()
	projectID := uuid.New()
	svc := &stubTenantService{
		listWithOrgProjects: func(ctx context.Context, in tenantapp.WithOrgProjectsInput) ([]*tenantdomain.TenantWithOrgProjects, error) {
			if in.UserID != "u-1" {
				t.Fatalf("expected JWT user ID u-1, got %q", in.UserID)
			}
			return []*tenantdomain.TenantWithOrgProjects{{
				ID: tenantID, Name: "效能租户",
				OrgList: []*orgdomain.OrganizationWithProjects{{
					ID: orgID, Name: "研发组", TenantID: tenantID,
					ProjectList: []orgdomain.ProjectSummary{{ID: projectID, Name: "效能平台"}},
				}},
			}}, nil
		},
	}
	r := newTenantTestEngine(svc, &userctx.User{UserID: "u-1"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tenant/withOrgProject", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected business code 0, got %+v", body)
	}
	tenantList := body["data"].(map[string]any)["tenantList"].([]any)
	if len(tenantList) != 1 {
		t.Fatalf("expected one tenant, got %d", len(tenantList))
	}
	tenant := tenantList[0].(map[string]any)
	if tenant["id"] != tenantID.String() || tenant["name"] != "效能租户" {
		t.Fatalf("unexpected tenant: %+v", tenant)
	}
	orgList := tenant["orgList"].([]any)
	projects := orgList[0].(map[string]any)["projectList"].([]any)
	if len(projects) != 1 || projects[0].(map[string]any)["id"] != projectID.String() {
		t.Fatalf("unexpected projects: %+v", projects)
	}
}

func TestTenantHandler_WithOrgProject_EmptyList(t *testing.T) {
	svc := &stubTenantService{
		listWithOrgProjects: func(context.Context, tenantapp.WithOrgProjectsInput) ([]*tenantdomain.TenantWithOrgProjects, error) {
			return []*tenantdomain.TenantWithOrgProjects{}, nil
		},
	}
	r := newTenantTestEngine(svc, &userctx.User{UserID: "u-1"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/tenant/withOrgProject", nil))
	body := decodeBody(t, w)
	tenantList, ok := body["data"].(map[string]any)["tenantList"].([]any)
	if !ok || len(tenantList) != 0 {
		t.Fatalf("expected empty tenantList array, got %+v", body)
	}
}

func TestTenantHandler_WithOrgProject_InternalError(t *testing.T) {
	svc := &stubTenantService{
		listWithOrgProjects: func(context.Context, tenantapp.WithOrgProjectsInput) ([]*tenantdomain.TenantWithOrgProjects, error) {
			return nil, errors.New("db unavailable")
		},
	}
	r := newTenantTestEngine(svc, &userctx.User{UserID: "u-1"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/tenant/withOrgProject", nil))
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 || body["msg"] != "internal error" {
		t.Fatalf("unexpected error response: %+v", body)
	}
}
