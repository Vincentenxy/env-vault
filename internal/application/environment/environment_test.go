package environment

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	envdomain "env-vault/internal/domain/environment"
	folderdomain "env-vault/internal/domain/folder"
	secretdomain "env-vault/internal/domain/secret"
	"env-vault/pkg/crypto"
)

// stubRepo 内存实现的 Repository，便于 application 层单测
type stubRepo struct {
	getByProjectCode func(ctx context.Context, projectID uuid.UUID, code string) (*envdomain.Environment, error)
	getByID          func(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error)
	createBatch      func(ctx context.Context, environments []*envdomain.Environment) error
	update           func(ctx context.Context, e *envdomain.Environment) error
	delete           func(ctx context.Context, id uuid.UUID, deleteBy string) error
	list             func(ctx context.Context, projectID uuid.UUID) ([]*envdomain.Environment, error)
	withTx           func(ctx context.Context, fn func(context.Context) error) error
}

func (s *stubRepo) CreateBatch(ctx context.Context, environments []*envdomain.Environment) error {
	if s.createBatch != nil {
		return s.createBatch(ctx, environments)
	}
	return nil
}
func (s *stubRepo) Update(ctx context.Context, e *envdomain.Environment) error {
	if s.update != nil {
		return s.update(ctx, e)
	}
	return nil
}
func (s *stubRepo) Delete(ctx context.Context, id uuid.UUID, deleteBy string) error {
	if s.delete != nil {
		return s.delete(ctx, id, deleteBy)
	}
	return nil
}
func (s *stubRepo) GetByID(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error) {
	if s.getByID != nil {
		return s.getByID(ctx, id)
	}
	return nil, nil
}
func (s *stubRepo) GetByProjectCode(ctx context.Context, projectID uuid.UUID, code string) (*envdomain.Environment, error) {
	if s.getByProjectCode != nil {
		return s.getByProjectCode(ctx, projectID, code)
	}
	return nil, nil
}
func (s *stubRepo) List(ctx context.Context, projectID uuid.UUID) ([]*envdomain.Environment, error) {
	if s.list != nil {
		return s.list(ctx, projectID)
	}
	return nil, nil
}
func (s *stubRepo) WithTx(ctx context.Context, fn func(context.Context) error) error {
	if s.withTx != nil {
		return s.withTx(ctx, fn)
	}
	return fn(ctx)
}

type stubFolderStructureRepo struct {
	createBatch func(ctx context.Context, folders []*folderdomain.Folder) error
	listByEnvID func(ctx context.Context, envID uuid.UUID) ([]*folderdomain.Folder, error)
}

func (s *stubFolderStructureRepo) CreateBatch(ctx context.Context, folders []*folderdomain.Folder) error {
	return s.createBatch(ctx, folders)
}

func (s *stubFolderStructureRepo) ListByEnvID(ctx context.Context, envID uuid.UUID) ([]*folderdomain.Folder, error) {
	return s.listByEnvID(ctx, envID)
}

type stubSecretStructureRepo struct {
	createBatch        func(ctx context.Context, secrets []*secretdomain.Secret) error
	listByFolderIDs    func(ctx context.Context, folderIDs []uuid.UUID) ([]*secretdomain.Secret, error)
	createHistoryBatch func(ctx context.Context, histories []*secretdomain.History) error
}

func (s *stubSecretStructureRepo) CreateBatch(ctx context.Context, secrets []*secretdomain.Secret) error {
	return s.createBatch(ctx, secrets)
}

func (s *stubSecretStructureRepo) ListByFolderIDs(ctx context.Context, folderIDs []uuid.UUID) ([]*secretdomain.Secret, error) {
	return s.listByFolderIDs(ctx, folderIDs)
}

func (s *stubSecretStructureRepo) CreateHistoryBatch(ctx context.Context, histories []*secretdomain.History) error {
	return s.createHistoryBatch(ctx, histories)
}

func newTestEnvironment(projectID uuid.UUID, code string) *envdomain.Environment {
	now := time.Now()
	return &envdomain.Environment{
		ID:        uuid.New(),
		Code:      code,
		Name:      "name-" + code,
		Remark:    "",
		ProjectID: projectID,
		OrderNo:   10,
		CreateAt:  now,
		UpdateAt:  now,
	}
}

func TestService_Create_Success(t *testing.T) {
	projectID := uuid.New()
	repo := &stubRepo{
		getByProjectCode: func(ctx context.Context, pid uuid.UUID, code string) (*envdomain.Environment, error) {
			if pid != projectID {
				t.Fatalf("unexpected lookup project=%v code=%s", pid, code)
			}
			return nil, nil
		},
		createBatch: func(ctx context.Context, environments []*envdomain.Environment) error {
			if len(environments) != 3 {
				t.Fatalf("expected 3 environments, got %d", len(environments))
			}
			for _, e := range environments {
				if e.ID == uuid.Nil {
					t.Fatal("ID should be generated")
				}
				if e.ProjectID != projectID {
					t.Fatalf("projectID not propagated: %+v", e)
				}
			}
			return nil
		},
	}
	svc := NewService(repo)
	got, err := svc.Create(context.Background(), CreateInput{
		ProjectID: projectID,
		Environments: []CreateItemInput{
			{Code: "dev", Name: "开发环境", IsCheckPerm: false},
			{Code: "test", Name: "测试环境", IsCheckPerm: false},
			{Code: "prod", Name: "生产环境", IsCheckPerm: true},
		},
	}, "operator-1")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	// orderNo 按列表顺序填充：10/20/30
	for i, wantNo := range []int{10, 20, 30} {
		if got[i].OrderNo != wantNo {
			t.Fatalf("expected orderNo %d at index %d, got %d", wantNo, i, got[i].OrderNo)
		}
		if got[i].CreateBy != "operator-1" || got[i].UpdateBy != "operator-1" {
			t.Fatalf("operator not propagated: %+v", got[i])
		}
	}
	if !got[2].IsCheckPerm {
		t.Fatalf("expected isCheckPerm true for prod, got false")
	}
	if got[0].IsCheckPerm {
		t.Fatalf("expected default isCheckPerm false for dev")
	}
}

func TestService_Create_UsesExplicitOrderNo(t *testing.T) {
	projectID := uuid.New()
	repo := &stubRepo{
		createBatch: func(ctx context.Context, environments []*envdomain.Environment) error {
			if len(environments) != 1 || environments[0].OrderNo != 35 {
				t.Fatalf("expected explicit orderNo 35, got %+v", environments)
			}
			return nil
		},
	}

	svc := NewService(repo)
	got, err := svc.Create(context.Background(), CreateInput{
		ProjectID: projectID,
		Environments: []CreateItemInput{
			{Code: "stage", Name: "预发布环境", OrderNo: 35},
		},
	}, "operator-1")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if got[0].OrderNo != 35 {
		t.Fatalf("expected explicit orderNo 35, got %d", got[0].OrderNo)
	}
}

func TestService_Create_ClonesFoldersAndEmptySecrets(t *testing.T) {
	projectID := uuid.New()
	sourceEnvironment := newTestEnvironment(projectID, "dev")
	sourceEnvironment.OrderNo = 20
	topFolderID := uuid.New()
	childFolderID := uuid.New()
	topGroupID := uuid.New()
	childGroupID := uuid.New()
	secretGroupID := uuid.New()
	steps := make([]string, 0, 4)

	cipher, err := crypto.New(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatalf("create cipher: %v", err)
	}

	var createdEnvironments []*envdomain.Environment
	envRepo := &stubRepo{
		list: func(ctx context.Context, pid uuid.UUID) ([]*envdomain.Environment, error) {
			return []*envdomain.Environment{sourceEnvironment}, nil
		},
		createBatch: func(ctx context.Context, environments []*envdomain.Environment) error {
			steps = append(steps, "environment")
			createdEnvironments = environments
			return nil
		},
		withTx: func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		},
	}

	var createdFolders []*folderdomain.Folder
	folderRepo := &stubFolderStructureRepo{
		listByEnvID: func(ctx context.Context, envID uuid.UUID) ([]*folderdomain.Folder, error) {
			if envID != sourceEnvironment.ID {
				t.Fatalf("unexpected source environment %s", envID)
			}
			return []*folderdomain.Folder{
				{ID: topFolderID, GroupID: topGroupID, Code: "groups", Name: "分组", EnvID: envID, Type: folderdomain.TypeCommon},
				{ID: childFolderID, GroupID: childGroupID, Code: "app", Name: "应用", EnvID: envID, ParentFolderID: &topFolderID, Type: folderdomain.TypeCommon, KeyPattern: `^[A-Z][A-Z0-9_]*$`},
			}, nil
		},
		createBatch: func(ctx context.Context, folders []*folderdomain.Folder) error {
			steps = append(steps, "folder")
			createdFolders = folders
			return nil
		},
	}

	var createdSecrets []*secretdomain.Secret
	var createdHistories []*secretdomain.History
	secretRepo := &stubSecretStructureRepo{
		listByFolderIDs: func(ctx context.Context, folderIDs []uuid.UUID) ([]*secretdomain.Secret, error) {
			if len(folderIDs) != 2 || folderIDs[0] != topFolderID || folderIDs[1] != childFolderID {
				t.Fatalf("unexpected source folders: %v", folderIDs)
			}
			return []*secretdomain.Secret{{
				ID: uuid.New(), GroupID: secretGroupID, FolderID: childFolderID,
				EnvCode: "dev", Key: "TOKEN", ValueType: "string", Remark: "令牌", Version: 3,
			}}, nil
		},
		createBatch: func(ctx context.Context, secrets []*secretdomain.Secret) error {
			steps = append(steps, "secret")
			createdSecrets = secrets
			return nil
		},
		createHistoryBatch: func(ctx context.Context, histories []*secretdomain.History) error {
			steps = append(steps, "history")
			createdHistories = histories
			return nil
		},
	}

	svc := NewService(envRepo, WithResourceClone(folderRepo, secretRepo, cipher))
	got, err := svc.Create(context.Background(), CreateInput{
		ProjectID: projectID,
		Environments: []CreateItemInput{
			{Code: "test", Name: "测试环境"},
			{Code: "prod", Name: "生产环境"},
		},
	}, "operator-1")
	if err != nil {
		t.Fatalf("create environments: %v", err)
	}
	if len(got) != 2 || got[0].OrderNo != 30 || got[1].OrderNo != 40 {
		t.Fatalf("unexpected environments: %+v", got)
	}
	if len(createdEnvironments) != 2 || len(createdFolders) != 4 || len(createdSecrets) != 2 || len(createdHistories) != 2 {
		t.Fatalf("unexpected clone counts: environments=%d folders=%d secrets=%d histories=%d",
			len(createdEnvironments), len(createdFolders), len(createdSecrets), len(createdHistories))
	}
	if createdFolders[1].KeyPattern != `^[A-Z][A-Z0-9_]*$` || createdFolders[3].KeyPattern != `^[A-Z][A-Z0-9_]*$` {
		t.Fatalf("folder key pattern was not preserved: %+v", createdFolders)
	}
	for _, secret := range createdSecrets {
		if secret.GroupID != secretGroupID || secret.Version != 1 || secret.ValueType != "string" {
			t.Fatalf("secret metadata was not preserved: %+v", secret)
		}
		value, decryptErr := cipher.Decrypt(secret.ValueCiphertext)
		if decryptErr != nil || value != "" {
			t.Fatalf("expected encrypted empty value, value=%q err=%v", value, decryptErr)
		}
	}
	batchID := createdHistories[0].BatchID
	if batchID == uuid.Nil || createdHistories[1].BatchID != batchID {
		t.Fatalf("histories should share one batch ID: %+v", createdHistories)
	}
	for i, history := range createdHistories {
		if history.SecretID != createdSecrets[i].ID || history.Version != 1 || history.CommitMsg != initialHistoryCommitMsg {
			t.Fatalf("unexpected history: %+v", history)
		}
	}
	wantSteps := []string{"environment", "folder", "secret", "history"}
	for i := range wantSteps {
		if steps[i] != wantSteps[i] {
			t.Fatalf("unexpected create order: %v", steps)
		}
	}
}

func TestService_Create_InvalidParam(t *testing.T) {
	svc := NewService(&stubRepo{})

	cases := []CreateInput{
		{ProjectID: uuid.Nil, Environments: []CreateItemInput{{Code: "c", Name: "n"}}},
		{ProjectID: uuid.New(), Environments: nil},
		{ProjectID: uuid.New(), Environments: []CreateItemInput{{Code: "", Name: "n"}}},
		{ProjectID: uuid.New(), Environments: []CreateItemInput{{Code: "c", Name: ""}}},
	}
	for _, in := range cases {
		if _, err := svc.Create(context.Background(), in, "u"); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("expected ErrInvalidParam for %+v, got %v", in, err)
		}
	}
}

func TestService_Create_CodeDuplicatedInRequest(t *testing.T) {
	svc := NewService(&stubRepo{}) // 数组内重复应在查询库前拦截
	_, err := svc.Create(context.Background(), CreateInput{
		ProjectID: uuid.New(),
		Environments: []CreateItemInput{
			{Code: "dev", Name: "开发环境"},
			{Code: "dev", Name: "开发环境2"},
		},
	}, "u")
	if !errors.Is(err, ErrCodeDuplicated) {
		t.Fatalf("expected ErrCodeDuplicated, got %v", err)
	}
}

func TestService_Create_CodeExists(t *testing.T) {
	projectID := uuid.New()
	repo := &stubRepo{
		getByProjectCode: func(ctx context.Context, pid uuid.UUID, code string) (*envdomain.Environment, error) {
			if code == "dev" {
				return newTestEnvironment(pid, code), nil
			}
			return nil, nil
		},
	}
	svc := NewService(repo)
	_, err := svc.Create(context.Background(), CreateInput{
		ProjectID: projectID,
		Environments: []CreateItemInput{
			{Code: "test", Name: "测试环境"},
			{Code: "dev", Name: "开发环境"},
		},
	}, "u")
	if !errors.Is(err, ErrCodeExists) {
		t.Fatalf("expected ErrCodeExists, got %v", err)
	}
}

func TestService_Update_Success(t *testing.T) {
	id := uuid.New()
	projectID := uuid.New()
	existing := newTestEnvironment(projectID, "dev")
	existing.ID = id
	existing.OrderNo = 20

	repo := &stubRepo{
		getByID: func(ctx context.Context, gid uuid.UUID) (*envdomain.Environment, error) {
			if gid != id {
				t.Fatalf("unexpected id %s", gid)
			}
			return existing, nil
		},
		update: func(ctx context.Context, e *envdomain.Environment) error {
			if e.Name != "测试环境" || e.Remark != "new-remark" {
				t.Fatalf("fields not updated: %+v", e)
			}
			if e.OrderNo != 30 {
				t.Fatalf("expected orderNo updated to 30, got %d", e.OrderNo)
			}
			if !e.IsCheckPerm {
				t.Fatalf("expected isCheckPerm true")
			}
			if e.UpdateBy != "operator-2" {
				t.Fatalf("updateBy not set")
			}
			return nil
		},
	}
	svc := NewService(repo)
	got, err := svc.Update(context.Background(), UpdateInput{
		ID: id, Name: "测试环境", Remark: "new-remark", OrderNo: 30, IsCheckPerm: true,
	}, "operator-2")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if got.OrderNo != 30 || !got.IsCheckPerm {
		t.Fatalf("returned environment not updated: %+v", got)
	}
}

func TestService_Update_OrderNoKeptWhenZero(t *testing.T) {
	id := uuid.New()
	projectID := uuid.New()
	existing := newTestEnvironment(projectID, "dev")
	existing.ID = id
	existing.OrderNo = 25

	repo := &stubRepo{
		getByID: func(ctx context.Context, gid uuid.UUID) (*envdomain.Environment, error) {
			return existing, nil
		},
		update: func(ctx context.Context, e *envdomain.Environment) error {
			if e.OrderNo != 25 {
				t.Fatalf("expected orderNo kept 25, got %d", e.OrderNo)
			}
			return nil
		},
	}
	svc := NewService(repo)
	_, err := svc.Update(context.Background(), UpdateInput{
		ID: id, Name: "开发环境", OrderNo: 0,
	}, "u")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
}

func TestService_Update_InvalidParam(t *testing.T) {
	svc := NewService(&stubRepo{})
	if _, err := svc.Update(context.Background(), UpdateInput{ID: uuid.Nil, Name: "n"}, "u"); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam for nil id")
	}
	if _, err := svc.Update(context.Background(), UpdateInput{ID: uuid.New(), Name: ""}, "u"); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam for empty name")
	}
}

func TestService_Update_NotFound(t *testing.T) {
	repo := &stubRepo{
		getByID: func(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error) {
			return nil, nil
		},
	}
	svc := NewService(repo)
	_, err := svc.Update(context.Background(), UpdateInput{ID: uuid.New(), Name: "n"}, "u")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_Delete_Success(t *testing.T) {
	id := uuid.New()
	projectID := uuid.New()
	called := false
	repo := &stubRepo{
		getByID: func(ctx context.Context, gid uuid.UUID) (*envdomain.Environment, error) {
			if gid != id {
				t.Fatalf("unexpected id %s", gid)
			}
			return newTestEnvironment(projectID, "dev"), nil
		},
		delete: func(ctx context.Context, gid uuid.UUID, by string) error {
			called = true
			if gid != id || by != "operator" {
				t.Fatalf("delete args wrong: id=%s by=%s", gid, by)
			}
			return nil
		},
	}
	svc := NewService(repo)
	if err := svc.Delete(context.Background(), id, "operator"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if !called {
		t.Fatal("repo.Delete not called")
	}
}

func TestService_Delete_NotFound(t *testing.T) {
	repo := &stubRepo{
		getByID: func(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error) {
			return nil, nil
		},
	}
	svc := NewService(repo)
	if err := svc.Delete(context.Background(), uuid.New(), "u"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_Delete_InvalidParam(t *testing.T) {
	svc := NewService(&stubRepo{})
	if err := svc.Delete(context.Background(), uuid.Nil, "u"); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam, got %v", err)
	}
}

func TestService_GetByID_Success(t *testing.T) {
	id := uuid.New()
	want := newTestEnvironment(uuid.New(), "dev")
	want.ID = id
	repo := &stubRepo{
		getByID: func(ctx context.Context, gid uuid.UUID) (*envdomain.Environment, error) {
			if gid != id {
				t.Fatalf("unexpected id %s", gid)
			}
			return want, nil
		},
	}
	svc := NewService(repo)
	got, err := svc.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if got.ID != id {
		t.Fatalf("expected id %s, got %s", id, got.ID)
	}
}

func TestService_GetByID_NotFound(t *testing.T) {
	repo := &stubRepo{
		getByID: func(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error) {
			return nil, nil
		},
	}
	svc := NewService(repo)
	if _, err := svc.GetByID(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_GetByID_InvalidParam(t *testing.T) {
	svc := NewService(&stubRepo{})
	if _, err := svc.GetByID(context.Background(), uuid.Nil); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam, got %v", err)
	}
}

func TestService_List_Success(t *testing.T) {
	projectID := uuid.New()
	repo := &stubRepo{
		list: func(ctx context.Context, pid uuid.UUID) ([]*envdomain.Environment, error) {
			if pid != projectID {
				t.Fatalf("expected projectID %s, got %s", projectID, pid)
			}
			return []*envdomain.Environment{newTestEnvironment(projectID, "dev")}, nil
		},
	}
	svc := NewService(repo)
	got, err := svc.List(context.Background(), ListInput{ProjectID: projectID})
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 environment, got %d", len(got))
	}
}

func TestService_List_InvalidParam(t *testing.T) {
	repo := &stubRepo{
		list: func(ctx context.Context, projectID uuid.UUID) ([]*envdomain.Environment, error) {
			t.Fatal("repo.List must not be called with nil projectID")
			return nil, nil
		},
	}
	svc := NewService(repo)
	if _, err := svc.List(context.Background(), ListInput{ProjectID: uuid.Nil}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam, got %v", err)
	}
}
