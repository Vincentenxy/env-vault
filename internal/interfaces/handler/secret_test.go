package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	secretapp "env-vault/internal/application/secret"
	secretdomain "env-vault/internal/domain/secret"
	"env-vault/pkg/userctx"
)

// stubSecretService 内存实现的 secretapp.IService，便于 handler 层单测
type stubSecretService struct {
	createFn     func(ctx context.Context, in secretapp.CreateInput, operator string) ([]*secretdomain.Secret, error)
	updateFn     func(ctx context.Context, in secretapp.UpdateInput, operator string) error
	listByFolder func(ctx context.Context, folderGroupID uuid.UUID) ([]secretapp.SecretView, error)
	listFn       func(ctx context.Context, in secretapp.ListInput) ([]secretapp.SecretView, error)
	getByGroup   func(ctx context.Context, groupID uuid.UUID) (*secretapp.SecretView, error)
	historyFn    func(ctx context.Context, in secretapp.HistoryInput) (*secretapp.HistoryResult, error)
	deleteFn     func(ctx context.Context, groupID uuid.UUID, operator string) error
}

func (s *stubSecretService) Create(ctx context.Context, in secretapp.CreateInput, operator string) ([]*secretdomain.Secret, error) {
	if s.createFn != nil {
		return s.createFn(ctx, in, operator)
	}
	return nil, nil
}
func (s *stubSecretService) Update(ctx context.Context, in secretapp.UpdateInput, operator string) error {
	if s.updateFn != nil {
		return s.updateFn(ctx, in, operator)
	}
	return nil
}
func (s *stubSecretService) ListByFolder(ctx context.Context, folderGroupID uuid.UUID) ([]secretapp.SecretView, error) {
	if s.listByFolder != nil {
		return s.listByFolder(ctx, folderGroupID)
	}
	return nil, nil
}
func (s *stubSecretService) List(ctx context.Context, in secretapp.ListInput) ([]secretapp.SecretView, error) {
	if s.listFn != nil {
		return s.listFn(ctx, in)
	}
	if s.listByFolder != nil {
		return s.listByFolder(ctx, in.FolderGroupID)
	}
	return nil, nil
}
func (s *stubSecretService) GetByGroup(ctx context.Context, groupID uuid.UUID) (*secretapp.SecretView, error) {
	if s.getByGroup != nil {
		return s.getByGroup(ctx, groupID)
	}
	return nil, nil
}
func (s *stubSecretService) History(ctx context.Context, in secretapp.HistoryInput) (*secretapp.HistoryResult, error) {
	if s.historyFn != nil {
		return s.historyFn(ctx, in)
	}
	return &secretapp.HistoryResult{}, nil
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
	g.POST("/update", h.Update)
	g.POST("/list", h.List)
	g.POST("/detail", h.Detail)
	g.POST("/history", h.History)
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

// 多个 secret 一次性创建时：handler 应把所有 items 都转换并传给 svc，且响应按返回的物理记录逐条映射。
func TestSecretHandler_Create_BatchMultiple(t *testing.T) {
	fgA, fgB := uuid.New(), uuid.New()
	envA1, envA2 := uuid.New(), uuid.New()
	envB1, envB2 := uuid.New(), uuid.New()
	gA, gB := uuid.New(), uuid.New()

	svc := &stubSecretService{
		createFn: func(ctx context.Context, in secretapp.CreateInput, operator string) ([]*secretdomain.Secret, error) {
			if operator != "u-1" {
				t.Fatalf("operator not propagated: %q", operator)
			}
			if len(in.SecretList) != 2 {
				t.Fatalf("expected 2 items, got %d", len(in.SecretList))
			}
			// 校验每个 item 的 fields 都正确转换
			if in.SecretList[0].FolderGroupID != fgA || in.SecretList[0].Key != "DB_PASSWORD" ||
				len(in.SecretList[0].Values) != 2 ||
				in.SecretList[0].Values[0].EnvID != envA1 || in.SecretList[0].Values[0].Value != "v-a1" ||
				in.SecretList[0].Values[1].EnvID != envA2 || in.SecretList[0].Values[1].Value != "v-a2" {
				t.Fatalf("item[0] not passed correctly: %+v", in.SecretList[0])
			}
			if in.SecretList[1].FolderGroupID != fgB || in.SecretList[1].Key != "API_TOKEN" ||
				len(in.SecretList[1].Values) != 2 {
				t.Fatalf("item[1] not passed correctly: %+v", in.SecretList[1])
			}
			return []*secretdomain.Secret{
				{ID: uuid.New(), GroupID: gA, Key: "DB_PASSWORD", Remark: "r1"},
				{ID: uuid.New(), GroupID: gB, Key: "API_TOKEN", Remark: "r2"},
			}, nil
		},
	}
	r := newSecretTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/create", map[string]any{
		"secretList": []map[string]any{
			{
				"folderGroupId": fgA,
				"key":           "DB_PASSWORD",
				"remark":        "r1",
				"values": []map[string]any{
					{"envId": envA1, "value": "v-a1"},
					{"envId": envA2, "value": "v-a2"},
				},
			},
			{
				"folderGroupId": fgB,
				"key":           "API_TOKEN",
				"remark":        "r2",
				"values": []map[string]any{
					{"envId": envB1, "value": "v-b1"},
					{"envId": envB2, "value": "v-b2"},
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
	if len(list) != 2 {
		t.Fatalf("expected 2 created, got %d", len(list))
	}
	// Secret 展示列表统一按 key 升序。
	got0 := list[0].(map[string]any)
	got1 := list[1].(map[string]any)
	if got0["groupId"].(string) != gB.String() || got0["key"].(string) != "API_TOKEN" || got0["remark"].(string) != "r2" {
		t.Fatalf("response[0] wrong: %+v", got0)
	}
	if got1["groupId"].(string) != gA.String() || got1["key"].(string) != "DB_PASSWORD" || got1["remark"].(string) != "r1" {
		t.Fatalf("response[1] wrong: %+v", got1)
	}
}

// ---------- List（查询1） ----------

func TestSecretHandler_List_Success(t *testing.T) {
	folderGroupID := uuid.New()
	devFolderID := uuid.New()
	testFolderID := uuid.New()
	devSecretID := uuid.New()
	testSecretID := uuid.New()
	groupID := uuid.New()
	devUpdateAt := time.Date(2026, time.August, 24, 10, 11, 12, 0, time.UTC)
	testUpdateAt := time.Date(2026, time.August, 24, 11, 12, 13, 0, time.UTC)
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
						"dev":  {SecretID: devSecretID, FolderID: devFolderID, Value: "dev-pass", Version: 2, ValueType: "string", UpdateAt: devUpdateAt},
						"test": {SecretID: testSecretID, FolderID: testFolderID, Value: "test-pass", Version: 4, ValueType: "number", UpdateAt: testUpdateAt},
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
	if dev["value"].(string) != "dev-pass" || dev["folderId"].(string) != devFolderID.String() || dev["secretId"].(string) != devSecretID.String() {
		t.Fatalf("unexpected dev value: %+v", dev)
	}
	// 方案1：values 项不再输出 envId（key 即 env code）
	if _, ok := dev["envId"]; ok {
		t.Fatalf("values item must NOT contain envId: %+v", dev)
	}
	if dev["version"].(float64) != 2 || dev["valueType"].(string) != "string" {
		t.Fatalf("list detail fields missing from dev value: %+v", dev)
	}
	if dev["updateAt"].(string) != devUpdateAt.Format(time.RFC3339) {
		t.Fatalf("unexpected dev updateAt: %+v", dev)
	}
	test := values["test"].(map[string]any)
	if test["folderId"].(string) != testFolderID.String() {
		t.Fatalf("unexpected test value: %+v", test)
	}
	if test["version"].(float64) != 4 || test["valueType"].(string) != "number" {
		t.Fatalf("list detail fields missing from test value: %+v", test)
	}
	if test["updateAt"].(string) != testUpdateAt.Format(time.RFC3339) {
		t.Fatalf("unexpected test updateAt: %+v", test)
	}
}

func TestSecretHandler_List_ProjectFolderMode(t *testing.T) {
	projectID := uuid.New()
	svc := &stubSecretService{
		listFn: func(ctx context.Context, in secretapp.ListInput) ([]secretapp.SecretView, error) {
			if in.ProjectID != projectID || in.FolderCode != "groups" {
				t.Fatalf("unexpected project/folder input: %+v", in)
			}
			if len(in.EnvList) != 2 || in.EnvList[0] != "dev" || in.EnvList[1] != "test" {
				t.Fatalf("unexpected env list: %+v", in.EnvList)
			}
			if len(in.KeyList) != 1 || in.KeyList[0] != "DB_PASSWORD" {
				t.Fatalf("unexpected key list: %+v", in.KeyList)
			}
			return []secretapp.SecretView{{GroupID: uuid.New(), Key: "DB_PASSWORD", Values: map[string]secretapp.SecretValueView{}}}, nil
		},
	}
	r := newSecretTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/list", map[string]any{
		"projectId":  projectID,
		"folderCode": "groups",
		"envList":    []string{"dev", "test"},
		"keyList":    []string{"DB_PASSWORD"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected code 0, got %v", body["code"])
	}
}

// ---------- Detail（查询2） ----------

func TestSecretHandler_Detail_Success(t *testing.T) {
	groupID := uuid.New()
	prodSecretID := uuid.New()
	svc := &stubSecretService{
		getByGroup: func(ctx context.Context, gid uuid.UUID) (*secretapp.SecretView, error) {
			if gid != groupID {
				t.Fatalf("unexpected group %v", gid)
			}
			return &secretapp.SecretView{
				GroupID: groupID,
				Key:     "TOKEN",
				Values: map[string]secretapp.SecretValueView{
					"prod": {SecretID: prodSecretID, FolderID: uuid.New(), Value: "prod-token", Version: 7, ValueType: "string"},
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
	prod := data["values"].(map[string]any)["prod"].(map[string]any)
	if prod["secretId"].(string) != prodSecretID.String() {
		t.Fatalf("detail secretId missing from prod value: %+v", prod)
	}
	if prod["version"].(float64) != 7 || prod["valueType"].(string) != "string" {
		t.Fatalf("detail fields missing from prod value: %+v", prod)
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

func TestSecretHandler_History_Success(t *testing.T) {
	secretID := uuid.New()
	batchID := uuid.New()
	groupID := uuid.New()
	createdAt := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	svc := &stubSecretService{
		historyFn: func(ctx context.Context, in secretapp.HistoryInput) (*secretapp.HistoryResult, error) {
			if in.SecretID != secretID || in.BatchID != batchID || in.GroupID != uuid.Nil || in.PageNum != 2 || in.PageSize != 5 {
				t.Fatalf("history input not passed: %+v", in)
			}
			return &secretapp.HistoryResult{
				Total: 6,
				HistoryList: []secretapp.HistoryView{{
					ID: uuid.New(), SecretID: secretID, BatchID: batchID, GroupID: groupID,
					FolderID: uuid.New(), EnvCode: "prod", Value: "secret-v2", ValueType: "string",
					Version: 2, CommitMsg: "rotate", CreateBy: "u-1", CreateByName: "创建人一", CreateAt: createdAt,
				}},
			}, nil
		},
	}
	r := newSecretTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/history", map[string]any{
		"secretId": secretID,
		"batchId":  batchID,
		"pageNum":  2,
		"pageSize": 5,
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected 0, got body=%+v", body)
	}
	data := body["data"].(map[string]any)
	if data["total"].(float64) != 6 {
		t.Fatalf("unexpected total: %+v", data)
	}
	if _, exists := data["historyList"]; exists {
		t.Fatalf("paginated response must use list: %+v", data)
	}
	history := data["list"].([]any)[0].(map[string]any)
	if history["secretId"].(string) != secretID.String() || history["batchId"].(string) != batchID.String() {
		t.Fatalf("history identity fields missing: %+v", history)
	}
	if history["value"].(string) != "secret-v2" || history["version"].(float64) != 2 || history["commitMsg"].(string) != "rotate" {
		t.Fatalf("unexpected history data: %+v", history)
	}
	if history["createBy"].(string) != "u-1" || history["createByName"].(string) != "创建人一" {
		t.Fatalf("creator fields missing: %+v", history)
	}
}

func TestSecretHandler_History_BatchGroupedResponse(t *testing.T) {
	batchID := uuid.New()
	groupID := uuid.New()
	envID := uuid.New()
	secretID := uuid.New()
	historyID := uuid.New()
	folderID := uuid.New()
	createdAt := time.Date(2026, 8, 23, 11, 40, 49, 0, time.FixedZone("CST", 8*60*60))
	svc := &stubSecretService{
		historyFn: func(_ context.Context, in secretapp.HistoryInput) (*secretapp.HistoryResult, error) {
			if in.BatchID != batchID || in.GroupID != uuid.Nil || in.SecretID != uuid.Nil {
				t.Fatalf("history input not passed: %+v", in)
			}
			return &secretapp.HistoryResult{BatchHistories: []secretapp.BatchHistoryView{
				{
					GroupID: groupID,
					Key:     "OB_PROXY_HOST",
					Remark:  "ob proxy address",
					Versions: map[uuid.UUID]secretapp.HistoryView{
						envID: {
							ID: historyID, SecretID: secretID, BatchID: batchID, GroupID: groupID,
							FolderID: folderID, EnvCode: "prod", Value: "192.168.5.5333", Version: 5,
							CommitMsg: "rotate", CreateBy: "10121993", CreateByName: "creator", CreateAt: createdAt,
						},
					},
				},
			}}, nil
		},
	}

	r := newSecretTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/history", map[string]any{"batchId": batchID})
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected success, got %+v", body)
	}
	data, ok := body["data"].([]any)
	if !ok || len(data) != 1 {
		t.Fatalf("batch response data must be an array: %+v", body["data"])
	}
	item := data[0].(map[string]any)
	if item["groupId"].(string) != groupID.String() || item["key"].(string) != "OB_PROXY_HOST" || item["remark"].(string) != "ob proxy address" {
		t.Fatalf("unexpected batch item: %+v", item)
	}
	version := item["versions"].(map[string]any)[envID.String()].(map[string]any)
	if version["id"].(string) != historyID.String() || version["secretId"].(string) != secretID.String() || version["folderId"].(string) != folderID.String() {
		t.Fatalf("history identity fields missing: %+v", version)
	}
	if version["value"].(string) != "192.168.5.5333" || version["version"].(float64) != 5 || version["createByName"].(string) != "creator" {
		t.Fatalf("unexpected environment version: %+v", version)
	}
}

func TestSecretHandler_History_GroupedByEnvironmentID(t *testing.T) {
	groupID := uuid.New()
	ignoredSecretID, ignoredBatchID := uuid.New(), uuid.New()
	devEnvID, prodEnvID := uuid.New(), uuid.New()
	devSecretID, prodSecretID := uuid.New(), uuid.New()
	svc := &stubSecretService{
		historyFn: func(_ context.Context, in secretapp.HistoryInput) (*secretapp.HistoryResult, error) {
			if in.GroupID != groupID || in.SecretID != ignoredSecretID || in.BatchID != ignoredBatchID || in.PageNum != 2 || in.PageSize != 5 {
				t.Fatalf("history input not passed: %+v", in)
			}
			return &secretapp.HistoryResult{EnvironmentHistories: map[uuid.UUID]secretapp.HistoryPage{
				devEnvID: {
					Total:       3,
					HistoryList: []secretapp.HistoryView{{SecretID: devSecretID, GroupID: groupID, EnvCode: "dev", Value: "dev-v2", Version: 2, CreateBy: "u-1", CreateByName: "创建人一"}},
				},
				prodEnvID: {
					Total:       2,
					HistoryList: []secretapp.HistoryView{{SecretID: prodSecretID, GroupID: groupID, EnvCode: "prod", Value: "prod-v1", Version: 1}},
				},
			}}, nil
		},
	}
	r := newSecretTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/history", map[string]any{
		"groupId": groupID, "secretId": ignoredSecretID, "batchId": ignoredBatchID, "pageNum": 2, "pageSize": 5,
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected success, got %+v", body)
	}
	data := body["data"].(map[string]any)
	dev := data[devEnvID.String()].(map[string]any)
	prod := data[prodEnvID.String()].(map[string]any)
	if dev["total"].(float64) != 3 || prod["total"].(float64) != 2 {
		t.Fatalf("unexpected environment totals: %+v", data)
	}
	devHistory := dev["list"].([]any)[0].(map[string]any)
	prodHistory := prod["list"].([]any)[0].(map[string]any)
	if devHistory["secretId"].(string) != devSecretID.String() || prodHistory["secretId"].(string) != prodSecretID.String() {
		t.Fatalf("histories not grouped by environment: %+v", data)
	}
	if devHistory["createByName"].(string) != "创建人一" {
		t.Fatalf("creator name missing from grouped history: %+v", devHistory)
	}
}

func TestSecretHandler_History_NormalizesPagination(t *testing.T) {
	secretID := uuid.New()
	tests := []struct {
		name         string
		body         map[string]any
		wantPageNum  int
		wantPageSize int
	}{
		{name: "defaults", body: map[string]any{"secretId": secretID}, wantPageNum: 1, wantPageSize: 20},
		{name: "negative page and zero size", body: map[string]any{"secretId": secretID, "pageNum": -3, "pageSize": 0}, wantPageNum: 1, wantPageSize: 20},
		{name: "clamp max size", body: map[string]any{"secretId": secretID, "pageNum": 2, "pageSize": 9999}, wantPageNum: 2, wantPageSize: 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &stubSecretService{historyFn: func(_ context.Context, in secretapp.HistoryInput) (*secretapp.HistoryResult, error) {
				if in.PageNum != tt.wantPageNum || in.PageSize != tt.wantPageSize {
					t.Fatalf("unexpected normalized pagination: %+v", in)
				}
				return &secretapp.HistoryResult{HistoryList: []secretapp.HistoryView{}}, nil
			}}
			r := newSecretTestEngine(svc, testUser())
			w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/history", tt.body)
			body := decodeBody(t, w)
			if body["code"].(float64) != 0 {
				t.Fatalf("expected success, got %+v", body)
			}
			data := body["data"].(map[string]any)
			if _, ok := data["list"].([]any); !ok {
				t.Fatalf("response must contain list array: %+v", data)
			}
		})
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

// ---------- Update ----------

func TestSecretHandler_Update_Success_OnlyRemark(t *testing.T) {
	groupID := uuid.New()
	called := false
	svc := &stubSecretService{
		updateFn: func(ctx context.Context, in secretapp.UpdateInput, operator string) error {
			called = true
			if operator != "u-1" {
				t.Fatalf("operator not propagated: %q", operator)
			}
			if len(in.Secrets) != 1 {
				t.Fatalf("expected 1 item, got %d", len(in.Secrets))
			}
			item := in.Secrets[0]
			if item.GroupID != groupID || item.Remark != "new-remark" || item.Key != "DB_PASSWORD" {
				t.Fatalf("item not passed: %+v", item)
			}
			if len(item.Values) != 0 {
				t.Fatalf("values must be empty, got %d", len(item.Values))
			}
			return nil
		},
	}
	r := newSecretTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/update", map[string]any{
		"commitMsg": "remark update",
		"secrets": []map[string]any{
			{"groupId": groupID, "key": "DB_PASSWORD", "remark": "new-remark"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected business code 0, got %v", body["code"])
	}
	if !called {
		t.Fatal("svc.Update not called")
	}
}

func TestSecretHandler_Update_Success_WithValues(t *testing.T) {
	groupID := uuid.New()
	devFolder := uuid.New()
	testFolder := uuid.New()
	devSecret := uuid.New()
	testSecret := uuid.New()
	var capturedBatchID uuid.UUID
	svc := &stubSecretService{
		updateFn: func(ctx context.Context, in secretapp.UpdateInput, operator string) error {
			capturedBatchID = in.BatchID
			if in.BatchID == uuid.Nil || in.CommitMsg != "batch update" {
				t.Fatalf("batch fields not passed: %+v", in)
			}
			if len(in.Secrets) != 1 {
				t.Fatalf("expected 1 item, got %d", len(in.Secrets))
			}
			item := in.Secrets[0]
			if item.GroupID != groupID || item.Remark != "r1" || item.CommitMsg != "secret update" {
				t.Fatalf("item fields wrong: %+v", item)
			}
			if len(item.Values) != 2 {
				t.Fatalf("expected 2 values, got %d", len(item.Values))
			}
			if item.Values[0].SecretID != devSecret || item.Values[0].FolderID != devFolder || item.Values[0].Value != "v1" || item.Values[0].EnvCode != "dev" {
				t.Fatalf("values[0] not passed: %+v", item.Values[0])
			}
			if item.Values[1].SecretID != testSecret || item.Values[1].FolderID != testFolder || item.Values[1].Value != "v2" || item.Values[1].EnvCode != "test" {
				t.Fatalf("values[1] not passed: %+v", item.Values[1])
			}
			return nil
		},
	}
	r := newSecretTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/update", map[string]any{
		"commitMsg": "batch update",
		"secrets": []map[string]any{
			{
				"groupId":   groupID,
				"key":       "DB_PASSWORD",
				"remark":    "r1",
				"commitMsg": "secret update",
				"values": []map[string]any{
					{"secretId": devSecret, "envCode": "dev", "folderId": devFolder, "value": "v1"},
					{"secretId": testSecret, "envCode": "test", "folderId": testFolder, "value": "v2"},
				},
			},
		},
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected 0, got %v", body["code"])
	}
	data := body["data"].(map[string]any)
	if data["batchId"].(string) != capturedBatchID.String() {
		t.Fatalf("response batchId does not match service input: %+v", data)
	}
}

func TestSecretHandler_Update_Success_BatchMultiple(t *testing.T) {
	g1, g2 := uuid.New(), uuid.New()
	svc := &stubSecretService{
		updateFn: func(ctx context.Context, in secretapp.UpdateInput, operator string) error {
			if len(in.Secrets) != 2 {
				t.Fatalf("expected 2 items, got %d", len(in.Secrets))
			}
			if in.Secrets[0].GroupID != g1 || in.Secrets[1].GroupID != g2 {
				t.Fatalf("group order wrong: %+v", in.Secrets)
			}
			return nil
		},
	}
	r := newSecretTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/update", map[string]any{
		"commitMsg": "batch update",
		"secrets": []map[string]any{
			{"groupId": g1, "key": "K1", "remark": "r1"},
			{"groupId": g2, "key": "K2", "remark": "r2"},
		},
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected 0, got %v", body["code"])
	}
}

func TestSecretHandler_Update_FieldsRequired(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
	}{
		{
			name: "empty_secrets",
			body: map[string]any{"secrets": []map[string]any{}},
		},
		{
			name: "nil_groupId",
			body: map[string]any{
				"commitMsg": "msg",
				"secrets": []map[string]any{
					{"groupId": "00000000-0000-0000-0000-000000000000", "key": "K"},
				},
			},
		},
		{
			name: "nil_secretId",
			body: map[string]any{
				"commitMsg": "msg",
				"secrets": []map[string]any{
					{
						"groupId": uuid.New(),
						"key":     "K",
						"values": []map[string]any{
							{"secretId": "00000000-0000-0000-0000-000000000000", "envCode": "dev", "value": "v"},
						},
					},
				},
			},
		},
		{
			name: "missing_commitMsg",
			body: map[string]any{
				"secrets": []map[string]any{{"groupId": uuid.New()}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &stubSecretService{
				updateFn: func(ctx context.Context, in secretapp.UpdateInput, operator string) error {
					t.Fatal("svc.Update must NOT be called on invalid params")
					return nil
				},
			}
			r := newSecretTestEngine(svc, testUser())
			w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/update", tc.body)
			expectInvalidParams(t, w)
		})
	}
}

func TestSecretHandler_Update_InvalidBody(t *testing.T) {
	svc := &stubSecretService{
		updateFn: func(ctx context.Context, in secretapp.UpdateInput, operator string) error {
			t.Fatal("svc.Update must NOT be called on bind failure")
			return nil
		},
	}
	r := newSecretTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/update", strings.NewReader("{not json"))
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("expected generic code -1 for bind failure, got %v", body["code"])
	}
}

func TestSecretHandler_Update_BusinessErrors(t *testing.T) {
	cases := []error{
		secretapp.ErrInvalidParam,
		secretapp.ErrNotFound,
		secretapp.ErrSecretNotUnderFolder,
	}
	for _, wantErr := range cases {
		svc := &stubSecretService{
			updateFn: func(ctx context.Context, in secretapp.UpdateInput, operator string) error {
				return wantErr
			},
		}
		r := newSecretTestEngine(svc, testUser())
		w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/update", map[string]any{
			"commitMsg": "msg",
			"secrets": []map[string]any{
				{"groupId": uuid.New(), "key": "K"},
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

// item.key 任意值都不参与业务校验（groupId 已唯一定位）：通过 updateFn 入参确认 key 透传
func TestSecretHandler_Update_KeyNotValidated(t *testing.T) {
	groupID := uuid.New()
	svc := &stubSecretService{
		updateFn: func(ctx context.Context, in secretapp.UpdateInput, operator string) error {
			// key 是 "TOTALLY_WRONG" 也应该照常进入更新路径
			if in.Secrets[0].Key != "TOTALLY_WRONG" {
				t.Fatalf("expected key to be passed through, got %q", in.Secrets[0].Key)
			}
			return nil
		},
	}
	r := newSecretTestEngine(svc, testUser())
	w := doJSONP(t, r, http.MethodPost, "/api/v1/secret/update", map[string]any{
		"commitMsg": "msg",
		"secrets": []map[string]any{
			{"groupId": groupID, "key": "TOTALLY_WRONG", "remark": "r"},
		},
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("expected 0 (key not validated), got %v", body["code"])
	}
}
