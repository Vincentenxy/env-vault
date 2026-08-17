package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	secretapp "env-vault/internal/application/secret"
	secretdomain "env-vault/internal/domain/secret"
	"env-vault/pkg/userctx"
)

// stubSecretService 内存实现的 secretapp.IService，便于 handler 层单测
type stubSecretService struct {
	createFn     func(ctx context.Context, in secretapp.CreateInput, operator string) ([]*secretdomain.Secret, error)
	listByFolder func(ctx context.Context, folderGroupID uuid.UUID) ([]secretapp.SecretView, error)
	getByGroup   func(ctx context.Context, groupID uuid.UUID) (*secretapp.SecretView, error)
	deleteFn     func(ctx context.Context, groupID uuid.UUID, operator string) error
}

func (s *stubSecretService) Create(ctx context.Context, in secretapp.CreateInput, operator string) ([]*secretdomain.Secret, error) {
	if s.createFn != nil {
		return s.createFn(ctx, in, operator)
	}
	return nil, nil
}
func (s *stubSecretService) ListByFolder(ctx context.Context, folderGroupID uuid.UUID) ([]secretapp.SecretView, error) {
	if s.listByFolder != nil {
		return s.listByFolder(ctx, folderGroupID)
	}
	return nil, nil
}
func (s *stubSecretService) GetByGroup(ctx context.Context, groupID uuid.UUID) (*secretapp.SecretView, error) {
	if s.getByGroup != nil {
		return s.getByGroup(ctx, groupID)
	}
	return nil, nil
}
func (s *stubSecretService) Delete(ctx context.Context, groupID uuid.UUID, operator string) error {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, groupID, operator)
	}
	return nil
}

func newSecretTestEngine(svc secretapp.IService, u *userctx.User) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 模拟认证中间件效果：将 userctx.User 注入请求上下文
	r.Use(func(c *gin.Context) {
		if u != nil {
			userctx.Set(c, u)
		}
		c.Next()
	})
	h := NewSecretHandler(svc)
	g := r.Group("/api/v1/secret")
	g.POST("/create", h.Create)
	g.POST("/list", h.List)
	g.POST("/detail", h.Detail)
	g.POST("/delete", h.Delete)
	return r
}

// ---------- Create ----------

func TestSecretHandler_Create_Success(t *testing.T) {
	folderGroupID := uuid.New()
	envID := uuid.New()
	groupID := uuid.New()
	svc := &stubSecretService{
		createFn: func(ctx context.Context, in secretapp.CreateInput, operator string) ([]*secretdomain.Secret, error) {
			if operator != "u-1" {
				t.Fatalf("operator not propagated: %q", operator)
			}
			if len(in.SecretList) != 1 {
				t.Fatalf("expected 1 item, got %d", len(in.SecretList))
			}
			item := in.SecretList[0]
			if item.FolderGroupID != folderGroupID || item.Key != "DB_PASSWORD" || item.Remark != "r" {
				t.Fatalf("input not passed: %+v", item)
			}
			if len(item.Values) != 1 || item.Values[0].EnvID != envID || item.Values[0].Value != "secret-value" {
				t.Fatalf("values not passed: %+v", item.Values)
			}
			return []*secretdomain.Secret{
				{ID: uuid.New(), GroupID: groupID, Key: "DB_PASSWORD", Remark: "r"},
			}, nil
		},
	}
	r := newSecretTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/create", map[string]any{
		"secretList": []map[string]any{
			{
				"folderGroupId": folderGroupID,
				"key":           "DB_PASSWORD",
				"remark":        "r",
				"values": []map[string]any{
					{"envId": envID, "value": "secret-value"},
				},
			},
		},
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
		t.Fatalf("expected 1 created, got %d", len(list))
	}
	first := list[0].(map[string]any)
	if first["groupId"].(string) != groupID.String() || first["key"].(string) != "DB_PASSWORD" {
		t.Fatalf("unexpected response data: %+v", first)
	}
}

func TestSecretHandler_Create_BusinessErrors(t *testing.T) {
	cases := []error{
		secretapp.ErrInvalidParam,
		secretapp.ErrFolderNotFound,
		secretapp.ErrEnvNotFound,
		secretapp.ErrKeyExists,
		secretapp.ErrDecrypt,
	}
	for _, wantErr := range cases {
		svc := &stubSecretService{
			createFn: func(ctx context.Context, in secretapp.CreateInput, operator string) ([]*secretdomain.Secret, error) {
				return nil, wantErr
			},
		}
		r := newSecretTestEngine(svc, testUser())
		w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/create", map[string]any{
			"secretList": []map[string]any{
				{
					"folderGroupId": uuid.New(),
					"key":           "K",
					"values":        []map[string]any{{"envId": uuid.New(), "value": "v"}},
				},
			},
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

func TestSecretHandler_Create_InvalidBody(t *testing.T) {
	svc := &stubSecretService{
		createFn: func(ctx context.Context, in secretapp.CreateInput, operator string) ([]*secretdomain.Secret, error) {
			t.Fatal("svc must not be called on bind failure")
			return nil, nil
		},
	}
	r := newSecretTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/create", strings.NewReader("{not json"))
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1 for bind failure, got %v", body["code"])
	}
}

// ---------- List（查询1） ----------

func TestSecretHandler_List_Success(t *testing.T) {
	folderGroupID := uuid.New()
	devFolderID := uuid.New()
	testFolderID := uuid.New()
	groupID := uuid.New()
	svc := &stubSecretService{
		listByFolder: func(ctx context.Context, gid uuid.UUID) ([]secretapp.SecretView, error) {
			if gid != folderGroupID {
				t.Fatalf("unexpected folder group %v", gid)
			}
			return []secretapp.SecretView{
				{
					GroupID: groupID,
					Key:     "DB_PASSWORD",
					Remark:  "数据库密码",
					Values: map[string]secretapp.SecretValueView{
						"dev":  {FolderID: devFolderID, Value: "dev-pass"},
						"test": {FolderID: testFolderID, Value: "test-pass"},
					},
				},
			}, nil
		},
	}
	r := newSecretTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/list", map[string]any{"folderGroupId": folderGroupID})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected 0, got %v", body["code"])
	}
	data := body["data"].(map[string]any)
	list := data["secretList"].([]any)
	if len(list) != 1 {
		t.Fatalf("expected 1 secret, got %d", len(list))
	}
	first := list[0].(map[string]any)
	if first["groupId"].(string) != groupID.String() || first["key"].(string) != "DB_PASSWORD" {
		t.Fatalf("unexpected item: %+v", first)
	}
	values := first["values"].(map[string]any)
	dev := values["dev"].(map[string]any)
	if dev["value"].(string) != "dev-pass" || dev["folderId"].(string) != devFolderID.String() {
		t.Fatalf("unexpected dev value: %+v", dev)
	}
	// 方案1：values 项不再输出 envId（key 即 env code）
	if _, ok := dev["envId"]; ok {
		t.Fatalf("values item must NOT contain envId: %+v", dev)
	}
	test := values["test"].(map[string]any)
	if test["folderId"].(string) != testFolderID.String() {
		t.Fatalf("unexpected test value: %+v", test)
	}
}

// ---------- Detail（查询2） ----------

func TestSecretHandler_Detail_Success(t *testing.T) {
	groupID := uuid.New()
	svc := &stubSecretService{
		getByGroup: func(ctx context.Context, gid uuid.UUID) (*secretapp.SecretView, error) {
			if gid != groupID {
				t.Fatalf("unexpected group %v", gid)
			}
			return &secretapp.SecretView{
				GroupID: groupID,
				Key:     "TOKEN",
				Values: map[string]secretapp.SecretValueView{
					"prod": {FolderID: uuid.New(), Value: "prod-token"},
				},
			}, nil
		},
	}
	r := newSecretTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/detail", map[string]any{"groupId": groupID})
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected 0, got %v", body["code"])
	}
	data := body["data"].(map[string]any)
	if data["groupId"].(string) != groupID.String() || data["key"].(string) != "TOKEN" {
		t.Fatalf("unexpected data: %+v", data)
	}
}

func TestSecretHandler_Detail_NotFound(t *testing.T) {
	svc := &stubSecretService{
		getByGroup: func(ctx context.Context, groupID uuid.UUID) (*secretapp.SecretView, error) {
			return nil, secretapp.ErrNotFound
		},
	}
	r := newSecretTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/detail", map[string]any{"groupId": uuid.New()})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1, got %v", body["code"])
	}
}

// ---------- Delete ----------

func TestSecretHandler_Delete_Success(t *testing.T) {
	groupID := uuid.New()
	called := false
	svc := &stubSecretService{
		deleteFn: func(ctx context.Context, gid uuid.UUID, operator string) error {
			called = true
			if gid != groupID {
				t.Fatalf("unexpected group %v", gid)
			}
			return nil
		},
	}
	r := newSecretTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/delete", map[string]any{"groupId": groupID})
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected 0, got %v", body["code"])
	}
	if !called {
		t.Fatal("svc.Delete not called")
	}
}

func TestSecretHandler_Delete_NotFound(t *testing.T) {
	svc := &stubSecretService{
		deleteFn: func(ctx context.Context, groupID uuid.UUID, operator string) error {
			return secretapp.ErrNotFound
		},
	}
	r := newSecretTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/delete", map[string]any{"groupId": uuid.New()})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1, got %v", body["code"])
	}
}

// ---------- 通用：未映射错误返回 -1 ----------

func TestSecretHandler_InternalError_FallbackToCodeMinusOne(t *testing.T) {
	svc := &stubSecretService{
		deleteFn: func(ctx context.Context, groupID uuid.UUID, operator string) error {
			return errors.New("db unreachable")
		},
	}
	r := newSecretTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/delete", map[string]any{"groupId": uuid.New()})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected code -1 for unmapped error, got %v", body["code"])
	}
}
