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
	createFn       func(ctx context.Context, in orgapp.CreateInput, operator string) (*orgdomain.Organization, error)
	updateFn       func(ctx context.Context, in orgapp.UpdateInput, operator string) (*orgdomain.Organization, error)
	deleteFn       func(ctx context.Context, id uuid.UUID, operator string) error
	getByID        func(ctx context.Context, id uuid.UUID) (*orgdomain.Organization, error)
	listFn         func(ctx context.Context, in orgapp.ListInput) ([]*orgdomain.Organization, int64, error)
	withProjectsFn func(ctx context.Context, in orgapp.WithProjectsInput) ([]*orgdomain.OrganizationWithProjects, error)
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
func (s *stubOrgService) ListWithProjects(ctx context.Context, in orgapp.WithProjectsInput) ([]*orgdomain.OrganizationWithProjects, error) {
	if s.withProjectsFn != nil {
		return s.withProjectsFn(ctx, in)
	}
	return nil, nil
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
	g.GET("/withProject", h.WithProject)
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
		ManagerName: "管理员一", ProjectCount: 4, MemberCount: 9,
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
	if data["managerName"] != "管理员一" || data["projectCount"] != float64(4) || data["memberCount"] != float64(9) {
		t.Fatalf("unexpected organization summary fields: %+v", data)
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

// ---------- WithProject ----------

func TestOrgHandler_WithProject_Success(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()
	emptyOrgID := uuid.New()
	svc := &stubOrgService{
		withProjectsFn: func(ctx context.Context, in orgapp.WithProjectsInput) ([]*orgdomain.OrganizationWithProjects, error) {
			if in.UserID != "u-1" {
				t.Fatalf("expected JWT user ID u-1, got %q", in.UserID)
			}
			return []*orgdomain.OrganizationWithProjects{
				{
					ID: orgID, Name: "研发组",
					ProjectList: []orgdomain.ProjectSummary{{ID: projectID, Name: "效能平台"}},
				},
				{ID: emptyOrgID, Name: "空组织", ProjectList: []orgdomain.ProjectSummary{}},
			}, nil
		},
	}

	r := newOrgTestEngine(svc, testUser())
	w := doJSON(t, r, http.MethodGet, "/api/v1/org/withProject", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected business code 0, got %v", body["code"])
	}
	data := body["data"].(map[string]any)
	orgList := data["orgList"].([]any)
	if len(orgList) != 2 {
		t.Fatalf("expected 2 organizations, got %d", len(orgList))
	}
	first := orgList[0].(map[string]any)
	if first["id"] != orgID.String() || first["name"] != "研发组" {
		t.Fatalf("unexpected organization: %+v", first)
	}
	projects := first["projectList"].([]any)
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	project := projects[0].(map[string]any)
	if project["id"] != projectID.String() || project["name"] != "效能平台" {
		t.Fatalf("unexpected project: %+v", project)
	}
	second := orgList[1].(map[string]any)
	if projects, ok := second["projectList"].([]any); !ok || len(projects) != 0 {
		t.Fatalf("expected empty projectList array, got %+v", second["projectList"])
	}
}

func TestOrgHandler_WithProject_InternalError(t *testing.T) {
	svc := &stubOrgService{
		withProjectsFn: func(context.Context, orgapp.WithProjectsInput) ([]*orgdomain.OrganizationWithProjects, error) {
			return nil, errors.New("db unavailable")
		},
	}
	r := newOrgTestEngine(svc, testUser())
	w := doJSON(t, r, http.MethodGet, "/api/v1/org/withProject", nil)
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 || body["msg"] != "internal error" {
		t.Fatalf("unexpected error response: %+v", body)
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

// ---------- handler 字段校验失败（svc 不应被调用） ----------

// expectInvalidParams 断言响应 code=-1 / msg="invalid params"
func expectInvalidParams(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected code -1, got %v", body["code"])
	}
	if body["msg"].(string) != "invalid params" {
		t.Fatalf("expected msg \"invalid params\", got %q", body["msg"])
	}
}

func TestOrgHandler_Create_FieldsRequired(t *testing.T) {
	neverCalled := func(label string) orgapp.IService {
		return &stubOrgService{
			createFn: func(ctx context.Context, in orgapp.CreateInput, operator string) (*orgdomain.Organization, error) {
				t.Fatalf("svc.Create must not be called (%s)", label)
				return nil, nil
			},
		}
	}
	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing code", map[string]any{"name": "n", "tenantId": uuid.New()}},
		{"missing name", map[string]any{"code": "c", "tenantId": uuid.New()}},
		{"missing tenantId", map[string]any{"code": "c", "name": "n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newOrgTestEngine(neverCalled(tc.name), testUser())
			w := doJSON(t, r, http.MethodPost, "/api/v1/org/create", tc.body)
			expectInvalidParams(t, w)
		})
	}
}

func TestOrgHandler_Update_FieldsRequired(t *testing.T) {
	neverCalled := func(label string) orgapp.IService {
		return &stubOrgService{
			updateFn: func(ctx context.Context, in orgapp.UpdateInput, operator string) (*orgdomain.Organization, error) {
				t.Fatalf("svc.Update must not be called (%s)", label)
				return nil, nil
			},
		}
	}
	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing id", map[string]any{"name": "n"}},
		{"missing name", map[string]any{"id": uuid.New()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newOrgTestEngine(neverCalled(tc.name), testUser())
			w := doJSON(t, r, http.MethodPost, "/api/v1/org/update", tc.body)
			expectInvalidParams(t, w)
		})
	}
}

func TestOrgHandler_Delete_FieldsRequired(t *testing.T) {
	svc := &stubOrgService{
		deleteFn: func(ctx context.Context, id uuid.UUID, operator string) error {
			t.Fatal("svc.Delete must not be called")
			return nil
		},
	}
	r := newOrgTestEngine(svc, testUser())
	w := doJSON(t, r, http.MethodPost, "/api/v1/org/delete", map[string]any{})
	expectInvalidParams(t, w)
}

func TestOrgHandler_Detail_FieldsRequired(t *testing.T) {
	svc := &stubOrgService{
		getByID: func(ctx context.Context, id uuid.UUID) (*orgdomain.Organization, error) {
			t.Fatal("svc.GetByID must not be called")
			return nil, nil
		},
	}
	r := newOrgTestEngine(svc, testUser())
	w := doJSON(t, r, http.MethodPost, "/api/v1/org/detail", map[string]any{})
	expectInvalidParams(t, w)
}
