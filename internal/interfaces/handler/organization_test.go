package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	orgapp "env-vault/internal/application/organization"
	orgdomain "env-vault/internal/domain/organization"
	"env-vault/pkg/userctx"
)

// stubService 内存实现的 orgapp.IService，便于 handler 层单测
type stubOrgService struct {
	createFn func(ctx context.Context, in orgapp.CreateInput, operator string) (*orgdomain.Organization, error)
	updateFn func(ctx context.Context, in orgapp.UpdateInput, operator string) (*orgdomain.Organization, error)
	deleteFn func(ctx context.Context, id uuid.UUID, operator string) error
	getByID  func(ctx context.Context, id uuid.UUID) (*orgdomain.Organization, error)
	listFn   func(ctx context.Context, in orgapp.ListInput) ([]*orgdomain.Organization, int64, error)
}

func (s *stubOrgService) Create(ctx context.Context, in orgapp.CreateInput, operator string) (*orgdomain.Organization, error) {
	if s.createFn != nil {
		return s.createFn(ctx, in, operator)
	}
	return nil, nil
}
func (s *stubOrgService) Update(ctx context.Context, in orgapp.UpdateInput, operator string) (*orgdomain.Organization, error) {
	if s.updateFn != nil {
		return s.updateFn(ctx, in, operator)
	}
	return nil, nil
}
func (s *stubOrgService) Delete(ctx context.Context, id uuid.UUID, operator string) error {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, id, operator)
	}
	return nil
}
func (s *stubOrgService) GetByID(ctx context.Context, id uuid.UUID) (*orgdomain.Organization, error) {
	if s.getByID != nil {
		return s.getByID(ctx, id)
	}
	return nil, nil
}
func (s *stubOrgService) List(ctx context.Context, in orgapp.ListInput) ([]*orgdomain.Organization, int64, error) {
	if s.listFn != nil {
		return s.listFn(ctx, in)
	}
	return nil, 0, nil
}

func newOrgTestEngine(svc orgapp.IService, u *userctx.User) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 模拟认证中间件效果：将 userctx.User 注入请求上下文
	r.Use(func(c *gin.Context) {
		if u != nil {
			userctx.Set(c, u)
		}
		c.Next()
	})
	h := NewOrganizationHandler(svc)
	g := r.Group("/api/v1/org")
	g.POST("/create", h.Create)
	g.POST("/update", h.Update)
	g.POST("/delete", h.Delete)
	g.POST("/detail", h.Detail)
	g.POST("/list", h.List)
	return r
}

// testUser 返回一个固定的测试登录用户，便于验证 operator 字段透传
func testUser() *userctx.User {
	return &userctx.User{UserID: "u-1", Name: "tester"}
}

func doJSON(t *testing.T, r http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	switch v := body.(type) {
	case nil:
		reader = nil
	case io.Reader:
		reader = v
	default:
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
		reader = &buf
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(w.Body).Decode(&m); err != nil {
		t.Fatalf("decode body: %v (raw=%s)", err, w.Body.String())
	}
	return m
}

// ---------- Create ----------

func TestOrgHandler_Create_Success(t *testing.T) {
	tenantID := uuid.New()
	want := &orgdomain.Organization{
		ID: uuid.New(), Code: "org-001", Name: "研发组", TenantID: tenantID,
		CreateBy: "u-1", UpdateBy: "u-1",
	}
	svc := &stubOrgService{
		createFn: func(ctx context.Context, in orgapp.CreateInput, operator string) (*orgdomain.Organization, error) {
			if operator != "u-1" {
				t.Fatalf("operator not propagated: %q", operator)
			}
			if in.Code != "org-001" || in.TenantID != tenantID {
				t.Fatalf("input not passed: %+v", in)
			}
			return want, nil
		},
	}
	r := newOrgTestEngine(svc, testUser())
	w := doJSON(t, r, http.MethodPost, "/api/v1/org/create", map[string]any{
		"code": "org-001", "name": "研发组", "tenantId": tenantID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected business code 0, got %v", body["code"])
	}
	data := body["data"].(map[string]any)
	if data["code"].(string) != "org-001" || data["tenantId"].(string) != tenantID.String() {
		t.Fatalf("unexpected response data: %+v", data)
	}
}

func TestOrgHandler_Create_InvalidBody(t *testing.T) {
	svc := &stubOrgService{
		createFn: func(ctx context.Context, in orgapp.CreateInput, operator string) (*orgdomain.Organization, error) {
			t.Fatal("svc.Create must not be called on bind failure")
			return nil, nil
		},
	}
	r := newOrgTestEngine(svc, testUser())
	// 非法 JSON 触发 ShouldBindJSON 失败
	w := doJSON(t, r, http.MethodPost, "/api/v1/org/create", strings.NewReader("{not json"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1 for bind failure, got %v", body["code"])
	}
}

func TestOrgHandler_Create_CodeExists(t *testing.T) {
	svc := &stubOrgService{
		createFn: func(ctx context.Context, in orgapp.CreateInput, operator string) (*orgdomain.Organization, error) {
			return nil, orgapp.ErrCodeExists
		},
	}
	r := newOrgTestEngine(svc, testUser())
	w := doJSON(t, r, http.MethodPost, "/api/v1/org/create", map[string]any{
		"code": "org-001", "name": "研发组", "tenantId": uuid.New(),
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1, got %v", body["code"])
	}
}

func TestOrgHandler_Create_InvalidParam(t *testing.T) {
	svc := &stubOrgService{
		createFn: func(ctx context.Context, in orgapp.CreateInput, operator string) (*orgdomain.Organization, error) {
			return nil, orgapp.ErrInvalidParam
		},
	}
	r := newOrgTestEngine(svc, testUser())
	w := doJSON(t, r, http.MethodPost, "/api/v1/org/create", map[string]any{
		"code": "", "name": "研发组", "tenantId": uuid.New(),
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1 for invalid param, got %v", body["code"])
	}
}

// ---------- Update ----------

func TestOrgHandler_Update_Success(t *testing.T) {
	id := uuid.New()
	want := &orgdomain.Organization{ID: id, Code: "org-001", Name: "new-name"}
	svc := &stubOrgService{
		updateFn: func(ctx context.Context, in orgapp.UpdateInput, operator string) (*orgdomain.Organization, error) {
			if in.ID != id || in.Name != "new-name" {
				t.Fatalf("input not passed: %+v", in)
			}
			return want, nil
		},
	}
	r := newOrgTestEngine(svc, testUser())
	w := doJSON(t, r, http.MethodPost, "/api/v1/org/update", map[string]any{
		"id": id, "name": "new-name", "remark": "r",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected 0, got %v", body["code"])
	}
}

func TestOrgHandler_Update_NotFound(t *testing.T) {
	svc := &stubOrgService{
		updateFn: func(ctx context.Context, in orgapp.UpdateInput, operator string) (*orgdomain.Organization, error) {
			return nil, orgapp.ErrNotFound
		},
	}
	r := newOrgTestEngine(svc, testUser())
	w := doJSON(t, r, http.MethodPost, "/api/v1/org/update", map[string]any{
		"id": uuid.New(), "name": "n",
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1, got %v", body["code"])
	}
}

// ---------- Delete ----------

func TestOrgHandler_Delete_Success(t *testing.T) {
	id := uuid.New()
	called := false
	svc := &stubOrgService{
		deleteFn: func(ctx context.Context, gid uuid.UUID, operator string) error {
			called = true
			if gid != id {
				t.Fatalf("unexpected id %s", gid)
			}
			return nil
		},
	}
	r := newOrgTestEngine(svc, testUser())
	w := doJSON(t, r, http.MethodPost, "/api/v1/org/delete", map[string]any{"id": id})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected 0, got %v", body["code"])
	}
	if !called {
		t.Fatal("svc.Delete not called")
	}
}

func TestOrgHandler_Delete_NotFound(t *testing.T) {
	svc := &stubOrgService{
		deleteFn: func(ctx context.Context, id uuid.UUID, operator string) error {
			return orgapp.ErrNotFound
		},
	}
	r := newOrgTestEngine(svc, testUser())
	w := doJSON(t, r, http.MethodPost, "/api/v1/org/delete", map[string]any{"id": uuid.New()})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1, got %v", body["code"])
	}
}

// ---------- Detail ----------

func TestOrgHandler_Detail_Success(t *testing.T) {
	id := uuid.New()
	want := &orgdomain.Organization{ID: id, Code: "org-001", Name: "研发组"}
	svc := &stubOrgService{
		getByID: func(ctx context.Context, gid uuid.UUID) (*orgdomain.Organization, error) {
			return want, nil
		},
	}
	r := newOrgTestEngine(svc, testUser())
	w := doJSON(t, r, http.MethodPost, "/api/v1/org/detail", map[string]any{"id": id})
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected 0, got %v", body["code"])
	}
	data := body["data"].(map[string]any)
	if data["id"].(string) != id.String() {
		t.Fatalf("unexpected id: %v", data["id"])
	}
}

func TestOrgHandler_Detail_NotFound(t *testing.T) {
	svc := &stubOrgService{
		getByID: func(ctx context.Context, id uuid.UUID) (*orgdomain.Organization, error) {
			return nil, orgapp.ErrNotFound
		},
	}
	r := newOrgTestEngine(svc, testUser())
	w := doJSON(t, r, http.MethodPost, "/api/v1/org/detail", map[string]any{"id": uuid.New()})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1, got %v", body["code"])
	}
}

// ---------- List ----------

func TestOrgHandler_List_Success_DefaultPagination(t *testing.T) {
	tenantID := uuid.New()
	var captured orgapp.ListInput
	svc := &stubOrgService{
		listFn: func(ctx context.Context, in orgapp.ListInput) ([]*orgdomain.Organization, int64, error) {
			captured = in
			return []*orgdomain.Organization{
				{ID: uuid.New(), Code: "c1", Name: "n1", TenantID: tenantID},
				{ID: uuid.New(), Code: "c2", Name: "n2", TenantID: tenantID},
			}, 2, nil
		},
	}
	r := newOrgTestEngine(svc, testUser())
	// 不传 pageNum/pageSize，验证 Normalize 兜底
	w := doJSON(t, r, http.MethodPost, "/api/v1/org/list", map[string]any{"tenantId": tenantID})
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected 0, got %v body=%s", body["code"], w.Body.String())
	}
	data := body["data"].(map[string]any)
	if data["total"].(float64) != 2 {
		t.Fatalf("expected total 2, got %v", data["total"])
	}
	if _, ok := data["pageNum"]; ok {
		t.Fatalf("response must NOT contain pageNum, got %+v", data)
	}
	if _, ok := data["pageSize"]; ok {
		t.Fatalf("response must NOT contain pageSize, got %+v", data)
	}
	list := data["list"].([]any)
	if len(list) != 2 {
		t.Fatalf("expected list len 2, got %d", len(list))
	}
	if captured.PageNum != 1 || captured.PageSize != 20 {
		t.Fatalf("expected default pageNum=1 pageSize=20, got %+v", captured)
	}
	if captured.TenantID == nil || *captured.TenantID != tenantID {
		t.Fatalf("expected tenantID %s, got %+v", tenantID, captured)
	}
}

func TestOrgHandler_List_ClampPagination(t *testing.T) {
	var captured orgapp.ListInput
	svc := &stubOrgService{
		listFn: func(ctx context.Context, in orgapp.ListInput) ([]*orgdomain.Organization, int64, error) {
			captured = in
			return nil, 0, nil
		},
	}
	r := newOrgTestEngine(svc, testUser())
	w := doJSON(t, r, http.MethodPost, "/api/v1/org/list", map[string]any{
		"pageNum": -5, "pageSize": 9999,
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected 0, got %v", body["code"])
	}
	if captured.PageNum != 1 || captured.PageSize != 200 {
		t.Fatalf("expected pageNum=1 pageSize=200, got %+v", captured)
	}
}

func TestOrgHandler_List_InvalidBody(t *testing.T) {
	svc := &stubOrgService{
		listFn: func(ctx context.Context, in orgapp.ListInput) ([]*orgdomain.Organization, int64, error) {
			t.Fatal("svc.List must not be called on bind failure")
			return nil, 0, nil
		},
	}
	r := newOrgTestEngine(svc, testUser())
	w := doJSON(t, r, http.MethodPost, "/api/v1/org/list", map[string]any{
		"pageNum": "not-a-number",
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1 for bind failure, got %v", body["code"])
	}
}

// ---------- 通用：未映射错误返回 -1 ----------

func TestOrgHandler_InternalError_FallbackToCodeMinusOne(t *testing.T) {
	svc := &stubOrgService{
		createFn: func(ctx context.Context, in orgapp.CreateInput, operator string) (*orgdomain.Organization, error) {
			return nil, errors.New("db unreachable")
		},
	}
	r := newOrgTestEngine(svc, testUser())
	w := doJSON(t, r, http.MethodPost, "/api/v1/org/create", map[string]any{
		"code": "c", "name": "n", "tenantId": uuid.New(),
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected code -1 for unmapped error, got %v", body["code"])
	}
}
