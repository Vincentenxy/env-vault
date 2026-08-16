package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	folderapp "env-vault/internal/application/folder"
	folderdomain "env-vault/internal/domain/folder"
	"env-vault/pkg/userctx"
)

// stubFolderService 内存实现的 folderapp.IService，便于 handler 层单测
type stubFolderService struct {
	createTopFn func(ctx context.Context, in folderapp.CreateTopInput, operator string) ([]*folderdomain.Folder, error)
	createSubFn func(ctx context.Context, in folderapp.CreateSubInput, operator string) ([]*folderdomain.Folder, error)
	updateFn    func(ctx context.Context, in folderapp.UpdateInput, operator string) error
	deleteFn    func(ctx context.Context, in folderapp.DeleteInput, operator string) error
	getByID     func(ctx context.Context, id uuid.UUID) (*folderdomain.Folder, error)
	listFn      func(ctx context.Context, in folderapp.ListInput) ([]*folderdomain.Folder, int64, error)
}

func (s *stubFolderService) CreateTop(ctx context.Context, in folderapp.CreateTopInput, operator string) ([]*folderdomain.Folder, error) {
	if s.createTopFn != nil {
		return s.createTopFn(ctx, in, operator)
	}
	return nil, nil
}
func (s *stubFolderService) CreateSub(ctx context.Context, in folderapp.CreateSubInput, operator string) ([]*folderdomain.Folder, error) {
	if s.createSubFn != nil {
		return s.createSubFn(ctx, in, operator)
	}
	return nil, nil
}
func (s *stubFolderService) Update(ctx context.Context, in folderapp.UpdateInput, operator string) error {
	if s.updateFn != nil {
		return s.updateFn(ctx, in, operator)
	}
	return nil
}
func (s *stubFolderService) Delete(ctx context.Context, in folderapp.DeleteInput, operator string) error {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, in, operator)
	}
	return nil
}
func (s *stubFolderService) GetByID(ctx context.Context, id uuid.UUID) (*folderdomain.Folder, error) {
	if s.getByID != nil {
		return s.getByID(ctx, id)
	}
	return nil, nil
}
func (s *stubFolderService) List(ctx context.Context, in folderapp.ListInput) ([]*folderdomain.Folder, int64, error) {
	if s.listFn != nil {
		return s.listFn(ctx, in)
	}
	return nil, 0, nil
}

func newFolderTestEngine(svc folderapp.IService, u *userctx.User) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 模拟认证中间件效果：将 userctx.User 注入请求上下文
	r.Use(func(c *gin.Context) {
		if u != nil {
			userctx.Set(c, u)
		}
		c.Next()
	})
	h := NewFolderHandler(svc)
	g := r.Group("/api/v1/folder")
	g.POST("/create", h.Create)
	g.POST("/update", h.Update)
	g.POST("/delete", h.Delete)
	g.POST("/info", h.Detail)
	g.POST("/list", h.List)
	return r
}

// ---------- Create（顶级 / 二级分发） ----------

func TestFolderHandler_Create_TopLevel(t *testing.T) {
	projectID := uuid.New()
	envID := uuid.New()
	createTopCalled := false
	svc := &stubFolderService{
		createTopFn: func(ctx context.Context, in folderapp.CreateTopInput, operator string) ([]*folderdomain.Folder, error) {
			createTopCalled = true
			if operator != "u-1" {
				t.Fatalf("operator not propagated: %q", operator)
			}
			if in.ProjectID != projectID || in.Code != "global" || in.Type != "common" {
				t.Fatalf("input not passed: %+v", in)
			}
			return []*folderdomain.Folder{
				{ID: uuid.New(), Code: "global", Name: "全局目录", EnvID: envID, Type: "common"},
			}, nil
		},
		createSubFn: func(ctx context.Context, in folderapp.CreateSubInput, operator string) ([]*folderdomain.Folder, error) {
			t.Fatal("CreateSub must not be called for top-level create")
			return nil, nil
		},
	}
	r := newFolderTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/folder/create", map[string]any{
		"projectId": projectID, "code": "global", "name": "全局目录", "type": "common",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected business code 0, got %v", body["code"])
	}
	// 响应为各环境下的记录数组
	list := body["data"].([]any)
	if len(list) != 1 {
		t.Fatalf("expected 1 item, got %d", len(list))
	}
	first := list[0].(map[string]any)
	if first["code"].(string) != "global" || first["envId"].(string) != envID.String() {
		t.Fatalf("unexpected response data: %+v", first)
	}
	if !createTopCalled {
		t.Fatal("svc.CreateTop not called")
	}
}

func TestFolderHandler_Create_SubLevel(t *testing.T) {
	parentFolderID := uuid.New()
	envID := uuid.New()
	svc := &stubFolderService{
		createTopFn: func(ctx context.Context, in folderapp.CreateTopInput, operator string) ([]*folderdomain.Folder, error) {
			t.Fatal("CreateTop must not be called for sub create")
			return nil, nil
		},
		createSubFn: func(ctx context.Context, in folderapp.CreateSubInput, operator string) ([]*folderdomain.Folder, error) {
			if in.ParentFolderID != parentFolderID || in.Code != "ob_efficient_cfg" {
				t.Fatalf("input not passed: %+v", in)
			}
			return []*folderdomain.Folder{
				{ID: uuid.New(), Code: "ob_efficient_cfg", Name: "OB高效配置", EnvID: envID, Type: "common"},
			}, nil
		},
	}
	r := newFolderTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/folder/create", map[string]any{
		"parentFolderId": parentFolderID, "code": "ob_efficient_cfg", "name": "OB高效配置", "type": "common",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected business code 0, got %v", body["code"])
	}
	list := body["data"].([]any)
	if len(list) != 1 {
		t.Fatalf("expected 1 item, got %d", len(list))
	}
}

func TestFolderHandler_Create_InvalidBody(t *testing.T) {
	svc := &stubFolderService{
		createTopFn: func(ctx context.Context, in folderapp.CreateTopInput, operator string) ([]*folderdomain.Folder, error) {
			t.Fatal("svc must not be called on bind failure")
			return nil, nil
		},
	}
	r := newFolderTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/folder/create", strings.NewReader("{not json"))
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1 for bind failure, got %v", body["code"])
	}
}

func TestFolderHandler_Create_BusinessErrors(t *testing.T) {
	cases := []error{
		folderapp.ErrCodeExists,
		folderapp.ErrInvalidType,
		folderapp.ErrCommonCodeInvalid,
		folderapp.ErrParentNotAllowed,
		folderapp.ErrNoEnvironment,
		folderapp.ErrGroupsNotFound,
	}
	for _, wantErr := range cases {
		svc := &stubFolderService{
			createTopFn: func(ctx context.Context, in folderapp.CreateTopInput, operator string) ([]*folderdomain.Folder, error) {
				return nil, wantErr
			},
			createSubFn: func(ctx context.Context, in folderapp.CreateSubInput, operator string) ([]*folderdomain.Folder, error) {
				return nil, wantErr
			},
		}
		r := newFolderTestEngine(svc, testUser())
		w := doJSONP(t, r, http.MethodPost, "/api/v1/folder/create", map[string]any{
			"projectId": uuid.New(), "code": "global", "name": "n", "type": "common",
		})
		body := decodeBody(t, w)
		if body["code"].(float64) != -1 {
			t.Fatalf("expected generic code -1 for %v, got %v", wantErr, body["code"])
		}
		if body["msg"].(string) != wantErr.Error() {
			t.Fatalf("expected msg %q, got %v", wantErr.Error(), body["msg"])
		}
	}
}

// ---------- Update ----------

func TestFolderHandler_Update_Success(t *testing.T) {
	idList := []uuid.UUID{uuid.New(), uuid.New()}
	called := false
	svc := &stubFolderService{
		updateFn: func(ctx context.Context, in folderapp.UpdateInput, operator string) error {
			called = true
			if len(in.IDList) != 2 || in.Name != "new-name" || in.Remark != "r" {
				t.Fatalf("input not passed: %+v", in)
			}
			return nil
		},
	}
	r := newFolderTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/folder/update", map[string]any{
		"idList": idList, "name": "new-name", "remark": "r",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected 0, got %v", body["code"])
	}
	if !called {
		t.Fatal("svc.Update not called")
	}
}

func TestFolderHandler_Update_NotFound(t *testing.T) {
	svc := &stubFolderService{
		updateFn: func(ctx context.Context, in folderapp.UpdateInput, operator string) error {
			return folderapp.ErrNotFound
		},
	}
	r := newFolderTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/folder/update", map[string]any{
		"idList": []uuid.UUID{uuid.New()}, "name": "n",
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1, got %v", body["code"])
	}
}

// ---------- Delete ----------

func TestFolderHandler_Delete_Success(t *testing.T) {
	projectID := uuid.New()
	called := false
	svc := &stubFolderService{
		deleteFn: func(ctx context.Context, in folderapp.DeleteInput, operator string) error {
			called = true
			if in.ProjectID != projectID || in.FolderCode != "global" {
				t.Fatalf("input not passed: %+v", in)
			}
			return nil
		},
	}
	r := newFolderTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/folder/delete", map[string]any{
		"projectId": projectID, "folderCode": "global",
	})
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

// ---------- Detail ----------

func TestFolderHandler_Detail_Success(t *testing.T) {
	id := uuid.New()
	parentID := uuid.New()
	want := &folderdomain.Folder{
		ID: id, Code: "ob_efficient_cfg", Name: "OB高效配置", ParentFolderID: &parentID, Type: "common",
	}
	svc := &stubFolderService{
		getByID: func(ctx context.Context, gid uuid.UUID) (*folderdomain.Folder, error) {
			if gid != id {
				t.Fatalf("unexpected id %s", gid)
			}
			return want, nil
		},
	}
	r := newFolderTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/folder/info", map[string]any{"id": id})
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected 0, got %v", body["code"])
	}
	data := body["data"].(map[string]any)
	if data["id"].(string) != id.String() || data["parentFolderId"].(string) != parentID.String() {
		t.Fatalf("unexpected response data: %+v", data)
	}
}

func TestFolderHandler_Detail_NotFound(t *testing.T) {
	svc := &stubFolderService{
		getByID: func(ctx context.Context, id uuid.UUID) (*folderdomain.Folder, error) {
			return nil, folderapp.ErrNotFound
		},
	}
	r := newFolderTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/folder/info", map[string]any{"id": uuid.New()})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1, got %v", body["code"])
	}
}

// ---------- List ----------

func TestFolderHandler_List_Success(t *testing.T) {
	projectID := uuid.New()
	var captured folderapp.ListInput
	svc := &stubFolderService{
		listFn: func(ctx context.Context, in folderapp.ListInput) ([]*folderdomain.Folder, int64, error) {
			captured = in
			return []*folderdomain.Folder{
				{ID: uuid.New(), Code: "global", Name: "全局目录"},
				{ID: uuid.New(), Code: "groups", Name: "分组目录"},
			}, 2, nil
		},
	}
	r := newFolderTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/folder/list", map[string]any{
		"projectId": projectID, "code": "g", "name": "目录",
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected 0, got %v body=%s", body["code"], w.Body.String())
	}
	data := body["data"].(map[string]any)
	if data["total"].(float64) != 2 {
		t.Fatalf("expected total 2, got %v", data["total"])
	}
	list := data["list"].([]any)
	if len(list) != 2 {
		t.Fatalf("expected list len 2, got %d", len(list))
	}
	if captured.ProjectID != projectID || captured.Code != "g" || captured.Name != "目录" {
		t.Fatalf("filters lost: %+v", captured)
	}
	if captured.PageNum != 1 || captured.PageSize != 20 {
		t.Fatalf("expected default pageNum=1 pageSize=20, got %+v", captured)
	}
}

func TestFolderHandler_List_InvalidParam(t *testing.T) {
	svc := &stubFolderService{
		listFn: func(ctx context.Context, in folderapp.ListInput) ([]*folderdomain.Folder, int64, error) {
			return nil, 0, folderapp.ErrInvalidParam
		},
	}
	r := newFolderTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/folder/list", map[string]any{})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1 for missing projectId, got %v", body["code"])
	}
}

func TestFolderHandler_List_InvalidBody(t *testing.T) {
	svc := &stubFolderService{
		listFn: func(ctx context.Context, in folderapp.ListInput) ([]*folderdomain.Folder, int64, error) {
			t.Fatal("svc.List must not be called on bind failure")
			return nil, 0, nil
		},
	}
	r := newFolderTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/folder/list", strings.NewReader("{not json"))
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1 for bind failure, got %v", body["code"])
	}
}

// ---------- 通用：未映射错误返回 -1 ----------

func TestFolderHandler_InternalError_FallbackToCodeMinusOne(t *testing.T) {
	svc := &stubFolderService{
		createTopFn: func(ctx context.Context, in folderapp.CreateTopInput, operator string) ([]*folderdomain.Folder, error) {
			return nil, errors.New("db unreachable")
		},
	}
	r := newFolderTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/folder/create", map[string]any{
		"projectId": uuid.New(), "code": "global", "name": "n", "type": "common",
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected code -1 for unmapped error, got %v", body["code"])
	}
}
