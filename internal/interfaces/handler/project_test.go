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

	projapp "env-vault/internal/application/project"
	projdomain "env-vault/internal/domain/project"
	"env-vault/pkg/userctx"
)

// stubProjectService 内存实现的 projapp.IService，便于 handler 层单测
type stubProjectService struct {
	createFn func(ctx context.Context, in projapp.CreateInput, operator string) (*projdomain.Project, error)
	updateFn func(ctx context.Context, in projapp.UpdateInput, operator string) (*projdomain.Project, error)
	deleteFn func(ctx context.Context, id uuid.UUID, operator string) error
	getByID  func(ctx context.Context, id uuid.UUID) (*projdomain.Project, error)
	listFn   func(ctx context.Context, in projapp.ListInput) ([]*projdomain.Project, int64, error)
}

func (s *stubProjectService) Create(ctx context.Context, in projapp.CreateInput, operator string) (*projdomain.Project, error) {
	if s.createFn != nil {
		return s.createFn(ctx, in, operator)
	}
	return nil, nil
}
func (s *stubProjectService) Update(ctx context.Context, in projapp.UpdateInput, operator string) (*projdomain.Project, error) {
	if s.updateFn != nil {
		return s.updateFn(ctx, in, operator)
	}
	return nil, nil
}
func (s *stubProjectService) Delete(ctx context.Context, id uuid.UUID, operator string) error {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, id, operator)
	}
	return nil
}
func (s *stubProjectService) GetByID(ctx context.Context, id uuid.UUID) (*projdomain.Project, error) {
	if s.getByID != nil {
		return s.getByID(ctx, id)
	}
	return nil, nil
}
func (s *stubProjectService) List(ctx context.Context, in projapp.ListInput) ([]*projdomain.Project, int64, error) {
	if s.listFn != nil {
		return s.listFn(ctx, in)
	}
	return nil, 0, nil
}

func newProjectTestEngine(svc projapp.IService, u *userctx.User) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 模拟认证中间件效果：将 userctx.User 注入请求上下文
	r.Use(func(c *gin.Context) {
		if u != nil {
			userctx.Set(c, u)
		}
		c.Next()
	})
	h := NewProjectHandler(svc)
	g := r.Group("/api/v1/project")
	g.POST("/create", h.Create)
	g.POST("/update", h.Update)
	g.POST("/delete", h.Delete)
	g.POST("/detail", h.Detail)
	g.POST("/list", h.List)
	return r
}

func doJSONP(t *testing.T, r http.Handler, method, path string, body any) *httptest.ResponseRecorder {
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

// ---------- Create ----------

func TestProjectHandler_Create_Success(t *testing.T) {
	orgID := uuid.New()
	want := &projdomain.Project{
		ID: uuid.New(), Code: "p-001", Name: "电商平台", OrgID: orgID,
		ManagerName: "管理员一", FolderCount: 6, MemberCount: 3,
		CreateBy: "u-1", UpdateBy: "u-1",
	}
	svc := &stubProjectService{
		createFn: func(ctx context.Context, in projapp.CreateInput, operator string) (*projdomain.Project, error) {
			if operator != "u-1" {
				t.Fatalf("operator not propagated: %q", operator)
			}
			if in.Code != "p-001" || in.OrgID != orgID {
				t.Fatalf("input not passed: %+v", in)
			}
			if len(in.Environments) != 2 || in.Environments[0].Code != "dev" || in.Environments[1].IsCheckPerm {
				t.Fatalf("environments not passed: %+v", in.Environments)
			}
			return want, nil
		},
	}
	r := newProjectTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/project/create", map[string]any{
		"code": "p-001", "name": "电商平台", "orgId": orgID,
		"environments": []map[string]any{
			{"code": "dev", "name": "开发环境", "remark": "开发环境", "isCheckPerm": false},
			{"code": "test", "name": "测试环境", "remark": "测试环境", "isCheckPerm": false},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected business code 0, got %v", body["code"])
	}
	data := body["data"].(map[string]any)
	if data["code"].(string) != "p-001" || data["orgId"].(string) != orgID.String() {
		t.Fatalf("unexpected response data: %+v", data)
	}
	if data["managerName"] != "管理员一" || data["folderCount"] != float64(6) || data["memberCount"] != float64(3) {
		t.Fatalf("unexpected project summary fields: %+v", data)
	}
}

func TestProjectHandler_Create_InvalidBody(t *testing.T) {
	svc := &stubProjectService{
		createFn: func(ctx context.Context, in projapp.CreateInput, operator string) (*projdomain.Project, error) {
			t.Fatal("svc.Create must not be called on bind failure")
			return nil, nil
		},
	}
	r := newProjectTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/project/create", strings.NewReader("{not json"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1 for bind failure, got %v", body["code"])
	}
}

func TestProjectHandler_Create_CodeExists(t *testing.T) {
	svc := &stubProjectService{
		createFn: func(ctx context.Context, in projapp.CreateInput, operator string) (*projdomain.Project, error) {
			return nil, projapp.ErrCodeExists
		},
	}
	r := newProjectTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/project/create", map[string]any{
		"code": "p-001", "name": "电商平台", "orgId": uuid.New(),
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1, got %v", body["code"])
	}
}

func TestProjectHandler_Create_InvalidParam(t *testing.T) {
	svc := &stubProjectService{
		createFn: func(ctx context.Context, in projapp.CreateInput, operator string) (*projdomain.Project, error) {
			return nil, projapp.ErrInvalidParam
		},
	}
	r := newProjectTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/project/create", map[string]any{
		"code": "", "name": "电商平台", "orgId": uuid.New(),
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1 for invalid param, got %v", body["code"])
	}
}

// ---------- Update ----------

func TestProjectHandler_Update_Success(t *testing.T) {
	id := uuid.New()
	want := &projdomain.Project{ID: id, Code: "p-001", Name: "new-name"}
	svc := &stubProjectService{
		updateFn: func(ctx context.Context, in projapp.UpdateInput, operator string) (*projdomain.Project, error) {
			if in.ID != id || in.Name != "new-name" {
				t.Fatalf("input not passed: %+v", in)
			}
			return want, nil
		},
	}
	r := newProjectTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/project/update", map[string]any{
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

func TestProjectHandler_Update_NotFound(t *testing.T) {
	svc := &stubProjectService{
		updateFn: func(ctx context.Context, in projapp.UpdateInput, operator string) (*projdomain.Project, error) {
			return nil, projapp.ErrNotFound
		},
	}
	r := newProjectTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/project/update", map[string]any{
		"id": uuid.New(), "name": "n",
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1, got %v", body["code"])
	}
}

// ---------- Delete ----------

func TestProjectHandler_Delete_Success(t *testing.T) {
	id := uuid.New()
	called := false
	svc := &stubProjectService{
		deleteFn: func(ctx context.Context, gid uuid.UUID, operator string) error {
			called = true
			if gid != id {
				t.Fatalf("unexpected id %s", gid)
			}
			return nil
		},
	}
	r := newProjectTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/project/delete", map[string]any{"id": id})
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

func TestProjectHandler_Delete_NotFound(t *testing.T) {
	svc := &stubProjectService{
		deleteFn: func(ctx context.Context, id uuid.UUID, operator string) error {
			return projapp.ErrNotFound
		},
	}
	r := newProjectTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/project/delete", map[string]any{"id": uuid.New()})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1, got %v", body["code"])
	}
}

// ---------- Detail ----------

func TestProjectHandler_Detail_Success(t *testing.T) {
	id := uuid.New()
	want := &projdomain.Project{ID: id, Code: "p-001", Name: "电商平台"}
	svc := &stubProjectService{
		getByID: func(ctx context.Context, gid uuid.UUID) (*projdomain.Project, error) {
			return want, nil
		},
	}
	r := newProjectTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/project/detail", map[string]any{"id": id})
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected 0, got %v", body["code"])
	}
	data := body["data"].(map[string]any)
	if data["id"].(string) != id.String() {
		t.Fatalf("unexpected id: %v", data["id"])
	}
}

func TestProjectHandler_Detail_NotFound(t *testing.T) {
	svc := &stubProjectService{
		getByID: func(ctx context.Context, id uuid.UUID) (*projdomain.Project, error) {
			return nil, projapp.ErrNotFound
		},
	}
	r := newProjectTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/project/detail", map[string]any{"id": uuid.New()})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1, got %v", body["code"])
	}
}

// ---------- List ----------

func TestProjectHandler_List_Success_DefaultPagination(t *testing.T) {
	orgID := uuid.New()
	var captured projapp.ListInput
	svc := &stubProjectService{
		listFn: func(ctx context.Context, in projapp.ListInput) ([]*projdomain.Project, int64, error) {
			captured = in
			return []*projdomain.Project{
				{ID: uuid.New(), Code: "c1", Name: "n1", OrgID: orgID},
				{ID: uuid.New(), Code: "c2", Name: "n2", OrgID: orgID},
			}, 2, nil
		},
	}
	r := newProjectTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/project/list", map[string]any{"orgId": orgID})
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
	if captured.OrgID == nil || *captured.OrgID != orgID {
		t.Fatalf("expected orgID %s, got %+v", orgID, captured)
	}
}

func TestProjectHandler_List_ClampPagination(t *testing.T) {
	var captured projapp.ListInput
	svc := &stubProjectService{
		listFn: func(ctx context.Context, in projapp.ListInput) ([]*projdomain.Project, int64, error) {
			captured = in
			return nil, 0, nil
		},
	}
	r := newProjectTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/project/list", map[string]any{
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

func TestProjectHandler_List_InvalidBody(t *testing.T) {
	svc := &stubProjectService{
		listFn: func(ctx context.Context, in projapp.ListInput) ([]*projdomain.Project, int64, error) {
			t.Fatal("svc.List must not be called on bind failure")
			return nil, 0, nil
		},
	}
	r := newProjectTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/project/list", map[string]any{
		"pageNum": "not-a-number",
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1 for bind failure, got %v", body["code"])
	}
}

// ---------- 通用：未映射错误返回 -1 ----------

func TestProjectHandler_InternalError_FallbackToCodeMinusOne(t *testing.T) {
	svc := &stubProjectService{
		createFn: func(ctx context.Context, in projapp.CreateInput, operator string) (*projdomain.Project, error) {
			return nil, errors.New("db unreachable")
		},
	}
	r := newProjectTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/project/create", map[string]any{
		"code": "c", "name": "n", "orgId": uuid.New(),
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected code -1 for unmapped error, got %v", body["code"])
	}
}

// ---------- handler 字段校验失败（svc 不应被调用） ----------

func TestProjectHandler_Create_FieldsRequired(t *testing.T) {
	neverCalled := func(label string) projapp.IService {
		return &stubProjectService{
			createFn: func(ctx context.Context, in projapp.CreateInput, operator string) (*projdomain.Project, error) {
				t.Fatalf("svc.Create must not be called (%s)", label)
				return nil, nil
			},
		}
	}
	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing code", map[string]any{"name": "n", "orgId": uuid.New()}},
		{"missing name", map[string]any{"code": "c", "orgId": uuid.New()}},
		{"missing orgId", map[string]any{"code": "c", "name": "n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newProjectTestEngine(neverCalled(tc.name), testUser())
			w := doJSONP(t, r, http.MethodPost, "/api/v1/project/create", tc.body)
			expectInvalidParams(t, w)
		})
	}
}

func TestProjectHandler_Update_FieldsRequired(t *testing.T) {
	neverCalled := func(label string) projapp.IService {
		return &stubProjectService{
			updateFn: func(ctx context.Context, in projapp.UpdateInput, operator string) (*projdomain.Project, error) {
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
			r := newProjectTestEngine(neverCalled(tc.name), testUser())
			w := doJSONP(t, r, http.MethodPost, "/api/v1/project/update", tc.body)
			expectInvalidParams(t, w)
		})
	}
}

func TestProjectHandler_Delete_FieldsRequired(t *testing.T) {
	svc := &stubProjectService{
		deleteFn: func(ctx context.Context, id uuid.UUID, operator string) error {
			t.Fatal("svc.Delete must not be called")
			return nil
		},
	}
	r := newProjectTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/project/delete", map[string]any{})
	expectInvalidParams(t, w)
}

func TestProjectHandler_Detail_FieldsRequired(t *testing.T) {
	svc := &stubProjectService{
		getByID: func(ctx context.Context, id uuid.UUID) (*projdomain.Project, error) {
			t.Fatal("svc.GetByID must not be called")
			return nil, nil
		},
	}
	r := newProjectTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/project/detail", map[string]any{})
	expectInvalidParams(t, w)
}
