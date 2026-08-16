package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	envapp "env-vault/internal/application/environment"
	envdomain "env-vault/internal/domain/environment"
	"env-vault/pkg/userctx"
)

// stubEnvironmentService 内存实现的 envapp.IService，便于 handler 层单测
type stubEnvironmentService struct {
	createFn func(ctx context.Context, in envapp.CreateInput, operator string) ([]*envdomain.Environment, error)
	updateFn func(ctx context.Context, in envapp.UpdateInput, operator string) (*envdomain.Environment, error)
	deleteFn func(ctx context.Context, id uuid.UUID, operator string) error
	getByID  func(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error)
	listFn   func(ctx context.Context, in envapp.ListInput) ([]*envdomain.Environment, error)
}

func (s *stubEnvironmentService) Create(ctx context.Context, in envapp.CreateInput, operator string) ([]*envdomain.Environment, error) {
	if s.createFn != nil {
		return s.createFn(ctx, in, operator)
	}
	return nil, nil
}
func (s *stubEnvironmentService) Update(ctx context.Context, in envapp.UpdateInput, operator string) (*envdomain.Environment, error) {
	if s.updateFn != nil {
		return s.updateFn(ctx, in, operator)
	}
	return nil, nil
}
func (s *stubEnvironmentService) Delete(ctx context.Context, id uuid.UUID, operator string) error {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, id, operator)
	}
	return nil
}
func (s *stubEnvironmentService) GetByID(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error) {
	if s.getByID != nil {
		return s.getByID(ctx, id)
	}
	return nil, nil
}
func (s *stubEnvironmentService) List(ctx context.Context, in envapp.ListInput) ([]*envdomain.Environment, error) {
	if s.listFn != nil {
		return s.listFn(ctx, in)
	}
	return nil, nil
}

func newEnvironmentTestEngine(svc envapp.IService, u *userctx.User) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 模拟认证中间件效果：将 userctx.User 注入请求上下文
	r.Use(func(c *gin.Context) {
		if u != nil {
			userctx.Set(c, u)
		}
		c.Next()
	})
	h := NewEnvironmentHandler(svc)
	g := r.Group("/api/v1/environment")
	g.POST("/create", h.Create)
	g.POST("/update", h.Update)
	g.POST("/delete", h.Delete)
	g.POST("/detail", h.Detail)
	g.POST("/list", h.List)
	return r
}

// ---------- Create ----------

func TestEnvironmentHandler_Create_Success(t *testing.T) {
	projectID := uuid.New()
	svc := &stubEnvironmentService{
		createFn: func(ctx context.Context, in envapp.CreateInput, operator string) ([]*envdomain.Environment, error) {
			if operator != "u-1" {
				t.Fatalf("operator not propagated: %q", operator)
			}
			if in.ProjectID != projectID || len(in.Environments) != 3 {
				t.Fatalf("input not passed: %+v", in)
			}
			if in.Environments[0].Code != "dev" || in.Environments[0].IsCheckPerm {
				t.Fatalf("first item wrong: %+v", in.Environments[0])
			}
			if !in.Environments[2].IsCheckPerm {
				t.Fatalf("prod isCheckPerm not passed: %+v", in.Environments[2])
			}
			return []*envdomain.Environment{
				{ID: uuid.New(), Code: "dev", Name: "开发环境", ProjectID: projectID, OrderNo: 10},
				{ID: uuid.New(), Code: "test", Name: "测试环境", ProjectID: projectID, OrderNo: 20},
				{ID: uuid.New(), Code: "prod", Name: "生产环境", ProjectID: projectID, OrderNo: 30, IsCheckPerm: true},
			}, nil
		},
	}
	r := newEnvironmentTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/environment/create", map[string]any{
		"projectId": projectID,
		"environments": []map[string]any{
			{"code": "dev", "name": "开发环境", "isCheckPerm": false},
			{"code": "test", "name": "测试环境", "isCheckPerm": false},
			{"code": "prod", "name": "生产环境", "isCheckPerm": true},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected business code 0, got %v", body["code"])
	}
	// 批量创建响应 data 为数组
	list := body["data"].([]any)
	if len(list) != 3 {
		t.Fatalf("expected 3 items in response, got %d", len(list))
	}
	first := list[0].(map[string]any)
	if first["code"].(string) != "dev" || first["orderNo"].(float64) != 10 {
		t.Fatalf("unexpected first item: %+v", first)
	}
	third := list[2].(map[string]any)
	if third["code"].(string) != "prod" || third["isCheckPerm"].(bool) != true {
		t.Fatalf("unexpected third item: %+v", third)
	}
}

func TestEnvironmentHandler_Create_InvalidBody(t *testing.T) {
	svc := &stubEnvironmentService{
		createFn: func(ctx context.Context, in envapp.CreateInput, operator string) ([]*envdomain.Environment, error) {
			t.Fatal("svc.Create must not be called on bind failure")
			return nil, nil
		},
	}
	r := newEnvironmentTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/environment/create", strings.NewReader("{not json"))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1 for bind failure, got %v", body["code"])
	}
}

func TestEnvironmentHandler_Create_CodeExists(t *testing.T) {
	svc := &stubEnvironmentService{
		createFn: func(ctx context.Context, in envapp.CreateInput, operator string) ([]*envdomain.Environment, error) {
			return nil, envapp.ErrCodeExists
		},
	}
	r := newEnvironmentTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/environment/create", map[string]any{
		"projectId": uuid.New(),
		"environments": []map[string]any{
			{"code": "dev", "name": "开发环境"},
		},
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1, got %v", body["code"])
	}
}

func TestEnvironmentHandler_Create_CodeDuplicated(t *testing.T) {
	svc := &stubEnvironmentService{
		createFn: func(ctx context.Context, in envapp.CreateInput, operator string) ([]*envdomain.Environment, error) {
			return nil, envapp.ErrCodeDuplicated
		},
	}
	r := newEnvironmentTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/environment/create", map[string]any{
		"projectId": uuid.New(),
		"environments": []map[string]any{
			{"code": "dev", "name": "开发环境"},
			{"code": "dev", "name": "开发环境2"},
		},
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1 for duplicated code, got %v", body["code"])
	}
	if body["msg"].(string) != envapp.ErrCodeDuplicated.Error() {
		t.Fatalf("expected msg %q, got %v", envapp.ErrCodeDuplicated.Error(), body["msg"])
	}
}

func TestEnvironmentHandler_Create_InvalidParam(t *testing.T) {
	svc := &stubEnvironmentService{
		createFn: func(ctx context.Context, in envapp.CreateInput, operator string) ([]*envdomain.Environment, error) {
			return nil, envapp.ErrInvalidParam
		},
	}
	r := newEnvironmentTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/environment/create", map[string]any{
		"projectId": uuid.New(),
		"environments": []map[string]any{
			{"code": "", "name": "开发环境"},
		},
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1 for invalid param, got %v", body["code"])
	}
}

// ---------- Update ----------

func TestEnvironmentHandler_Update_Success(t *testing.T) {
	id := uuid.New()
	want := &envdomain.Environment{ID: id, Code: "dev", Name: "new-name", OrderNo: 30, IsCheckPerm: true}
	svc := &stubEnvironmentService{
		updateFn: func(ctx context.Context, in envapp.UpdateInput, operator string) (*envdomain.Environment, error) {
			if in.ID != id || in.Name != "new-name" || in.OrderNo != 30 || !in.IsCheckPerm {
				t.Fatalf("input not passed: %+v", in)
			}
			return want, nil
		},
	}
	r := newEnvironmentTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/environment/update", map[string]any{
		"id": id, "name": "new-name", "remark": "r", "orderNo": 30, "isCheckPerm": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected 0, got %v", body["code"])
	}
}

func TestEnvironmentHandler_Update_NotFound(t *testing.T) {
	svc := &stubEnvironmentService{
		updateFn: func(ctx context.Context, in envapp.UpdateInput, operator string) (*envdomain.Environment, error) {
			return nil, envapp.ErrNotFound
		},
	}
	r := newEnvironmentTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/environment/update", map[string]any{
		"id": uuid.New(), "name": "n",
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1, got %v", body["code"])
	}
}

// ---------- Delete ----------

func TestEnvironmentHandler_Delete_Success(t *testing.T) {
	id := uuid.New()
	called := false
	svc := &stubEnvironmentService{
		deleteFn: func(ctx context.Context, gid uuid.UUID, operator string) error {
			called = true
			if gid != id {
				t.Fatalf("unexpected id %s", gid)
			}
			return nil
		},
	}
	r := newEnvironmentTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/environment/delete", map[string]any{"id": id})
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

func TestEnvironmentHandler_Delete_NotFound(t *testing.T) {
	svc := &stubEnvironmentService{
		deleteFn: func(ctx context.Context, id uuid.UUID, operator string) error {
			return envapp.ErrNotFound
		},
	}
	r := newEnvironmentTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/environment/delete", map[string]any{"id": uuid.New()})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1, got %v", body["code"])
	}
}

// ---------- Detail ----------

func TestEnvironmentHandler_Detail_Success(t *testing.T) {
	id := uuid.New()
	want := &envdomain.Environment{ID: id, Code: "dev", Name: "开发环境", OrderNo: 10, IsCheckPerm: false}
	svc := &stubEnvironmentService{
		getByID: func(ctx context.Context, gid uuid.UUID) (*envdomain.Environment, error) {
			return want, nil
		},
	}
	r := newEnvironmentTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/environment/detail", map[string]any{"id": id})
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected 0, got %v", body["code"])
	}
	data := body["data"].(map[string]any)
	if data["id"].(string) != id.String() {
		t.Fatalf("unexpected id: %v", data["id"])
	}
	if data["orderNo"].(float64) != 10 {
		t.Fatalf("unexpected orderNo: %v", data["orderNo"])
	}
}

func TestEnvironmentHandler_Detail_NotFound(t *testing.T) {
	svc := &stubEnvironmentService{
		getByID: func(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error) {
			return nil, envapp.ErrNotFound
		},
	}
	r := newEnvironmentTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/environment/detail", map[string]any{"id": uuid.New()})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1, got %v", body["code"])
	}
}

// ---------- List ----------

func TestEnvironmentHandler_List_Success(t *testing.T) {
	projectID := uuid.New()
	svc := &stubEnvironmentService{
		listFn: func(ctx context.Context, in envapp.ListInput) ([]*envdomain.Environment, error) {
			if in.ProjectID != projectID {
				t.Fatalf("expected projectID %s, got %s", projectID, in.ProjectID)
			}
			return []*envdomain.Environment{
				{ID: uuid.New(), Code: "dev", Name: "开发环境", ProjectID: projectID, OrderNo: 10},
				{ID: uuid.New(), Code: "test", Name: "测试环境", ProjectID: projectID, OrderNo: 20},
			}, nil
		},
	}
	r := newEnvironmentTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/environment/list", map[string]any{"projectId": projectID})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected 0, got %v body=%s", body["code"], w.Body.String())
	}
	// 响应 data 为环境数组，无分页结构
	list := body["data"].([]any)
	if len(list) != 2 {
		t.Fatalf("expected list len 2, got %d", len(list))
	}
	first := list[0].(map[string]any)
	if first["code"].(string) != "dev" || first["orderNo"].(float64) != 10 {
		t.Fatalf("unexpected first item: %+v", first)
	}
}

func TestEnvironmentHandler_List_InvalidParam(t *testing.T) {
	svc := &stubEnvironmentService{
		listFn: func(ctx context.Context, in envapp.ListInput) ([]*envdomain.Environment, error) {
			return nil, envapp.ErrInvalidParam
		},
	}
	r := newEnvironmentTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/environment/list", map[string]any{})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1 for missing projectId, got %v", body["code"])
	}
}

func TestEnvironmentHandler_List_InvalidBody(t *testing.T) {
	svc := &stubEnvironmentService{
		listFn: func(ctx context.Context, in envapp.ListInput) ([]*envdomain.Environment, error) {
			t.Fatal("svc.List must not be called on bind failure")
			return nil, nil
		},
	}
	r := newEnvironmentTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/environment/list", strings.NewReader("{not json"))
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1 for bind failure, got %v", body["code"])
	}
}

// ---------- 通用：未映射错误返回 -1 ----------

func TestEnvironmentHandler_InternalError_FallbackToCodeMinusOne(t *testing.T) {
	svc := &stubEnvironmentService{
		createFn: func(ctx context.Context, in envapp.CreateInput, operator string) ([]*envdomain.Environment, error) {
			return nil, errors.New("db unreachable")
		},
	}
	r := newEnvironmentTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/environment/create", map[string]any{
		"projectId": uuid.New(),
		"environments": []map[string]any{
			{"code": "dev", "name": "n"},
		},
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected code -1 for unmapped error, got %v", body["code"])
	}
}
