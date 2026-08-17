package secret

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	envdomain "env-vault/internal/domain/environment"
	folderdomain "env-vault/internal/domain/folder"
	secretdomain "env-vault/internal/domain/secret"
	"env-vault/pkg/crypto"
)

// testCipher 测试用加解密器（与 config 中 security.encryption_key 同值的测试密钥）
func testCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, err := crypto.New("Pk6V+TnUEZO6R8WOklCSrI/iM4QKHc55VQQrrptmVfk=")
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	return c
}

// stubSecretRepo 内存实现的密钥 Repository
type stubSecretRepo struct {
	createBatch     func(ctx context.Context, secrets []*secretdomain.Secret) error
	deleteByGroupID func(ctx context.Context, groupID uuid.UUID, deleteBy string) (int64, error)
	getByID         func(ctx context.Context, id uuid.UUID) (*secretdomain.Secret, error)
	getByFolderKey  func(ctx context.Context, folderIDs []uuid.UUID, key string) (*secretdomain.Secret, error)
	listByFolders   func(ctx context.Context, folderIDs []uuid.UUID) ([]*secretdomain.Secret, error)
	listByGroup     func(ctx context.Context, groupID uuid.UUID) ([]*secretdomain.Secret, error)
}

func (s *stubSecretRepo) CreateBatch(ctx context.Context, secrets []*secretdomain.Secret) error {
	if s.createBatch != nil {
		return s.createBatch(ctx, secrets)
	}
	return nil
}
func (s *stubSecretRepo) DeleteByGroupID(ctx context.Context, groupID uuid.UUID, deleteBy string) (int64, error) {
	if s.deleteByGroupID != nil {
		return s.deleteByGroupID(ctx, groupID, deleteBy)
	}
	return 1, nil
}
func (s *stubSecretRepo) GetByID(ctx context.Context, id uuid.UUID) (*secretdomain.Secret, error) {
	if s.getByID != nil {
		return s.getByID(ctx, id)
	}
	return nil, nil
}
func (s *stubSecretRepo) GetByFolderIDsKey(ctx context.Context, folderIDs []uuid.UUID, key string) (*secretdomain.Secret, error) {
	if s.getByFolderKey != nil {
		return s.getByFolderKey(ctx, folderIDs, key)
	}
	return nil, nil
}
func (s *stubSecretRepo) ListByFolderIDs(ctx context.Context, folderIDs []uuid.UUID) ([]*secretdomain.Secret, error) {
	if s.listByFolders != nil {
		return s.listByFolders(ctx, folderIDs)
	}
	return nil, nil
}
func (s *stubSecretRepo) ListByGroupID(ctx context.Context, groupID uuid.UUID) ([]*secretdomain.Secret, error) {
	if s.listByGroup != nil {
		return s.listByGroup(ctx, groupID)
	}
	return nil, nil
}

// stubFolderRepo 内存实现的文件夹 Repository（secret 应用服务依赖）
type stubFolderRepo struct {
	listByGroupID func(ctx context.Context, groupID uuid.UUID) ([]*folderdomain.Folder, error)
	getByID       func(ctx context.Context, id uuid.UUID) (*folderdomain.Folder, error)
}

func (s *stubFolderRepo) CreateBatch(ctx context.Context, folders []*folderdomain.Folder) error {
	return nil
}
func (s *stubFolderRepo) UpdateByGroupID(ctx context.Context, groupID uuid.UUID, name, remark, updateBy string, updateAt time.Time) (int64, error) {
	return 0, nil
}
func (s *stubFolderRepo) DeleteByGroupID(ctx context.Context, groupID uuid.UUID, deleteBy string) (int64, error) {
	return 0, nil
}
func (s *stubFolderRepo) GetByID(ctx context.Context, id uuid.UUID) (*folderdomain.Folder, error) {
	if s.getByID != nil {
		return s.getByID(ctx, id)
	}
	return nil, nil
}
func (s *stubFolderRepo) GetByGroupID(ctx context.Context, groupID uuid.UUID) (*folderdomain.Folder, error) {
	return nil, nil
}
func (s *stubFolderRepo) ListByGroupID(ctx context.Context, groupID uuid.UUID) ([]*folderdomain.Folder, error) {
	if s.listByGroupID != nil {
		return s.listByGroupID(ctx, groupID)
	}
	return nil, nil
}
func (s *stubFolderRepo) GetByEnvIDsCode(ctx context.Context, envIDs []uuid.UUID, code string) (*folderdomain.Folder, error) {
	return nil, nil
}
func (s *stubFolderRepo) GetByEnvCode(ctx context.Context, envID uuid.UUID, code string) (*folderdomain.Folder, error) {
	return nil, nil
}
func (s *stubFolderRepo) GetByParentCode(ctx context.Context, parentID uuid.UUID, code string) (*folderdomain.Folder, error) {
	return nil, nil
}
func (s *stubFolderRepo) ListTopGroupIDsByEnvIDs(ctx context.Context, envIDs []uuid.UUID) ([]uuid.UUID, error) {
	return nil, nil
}
func (s *stubFolderRepo) ListSubGroupIDsByParentFolderID(ctx context.Context, parentFolderID uuid.UUID) ([]uuid.UUID, error) {
	return nil, nil
}
func (s *stubFolderRepo) ListByGroupIDs(ctx context.Context, groupIDs []uuid.UUID, filter folderdomain.ListFilter) ([]*folderdomain.Folder, int64, error) {
	return nil, 0, nil
}

// stubEnvRepo 内存实现的环境 Repository（secret 应用服务依赖）
type stubEnvRepo struct {
	getByID func(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error)
}

func (s *stubEnvRepo) CreateBatch(ctx context.Context, environments []*envdomain.Environment) error {
	return nil
}
func (s *stubEnvRepo) Update(ctx context.Context, e *envdomain.Environment) error {
	return nil
}
func (s *stubEnvRepo) Delete(ctx context.Context, id uuid.UUID, deleteBy string) error {
	return nil
}
func (s *stubEnvRepo) GetByID(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error) {
	if s.getByID != nil {
		return s.getByID(ctx, id)
	}
	return nil, nil
}
func (s *stubEnvRepo) GetByProjectCode(ctx context.Context, projectID uuid.UUID, code string) (*envdomain.Environment, error) {
	return nil, nil
}
func (s *stubEnvRepo) List(ctx context.Context, projectID uuid.UUID) ([]*envdomain.Environment, error) {
	return nil, nil
}

// newTestFolderGroup 构造一个业务 folder 组：groupID + 两个环境实例
func newTestFolderGroup(groupID uuid.UUID, envs []*envdomain.Environment) []*folderdomain.Folder {
	folders := make([]*folderdomain.Folder, 0, len(envs))
	for _, e := range envs {
		folders = append(folders, &folderdomain.Folder{
			ID:      uuid.New(),
			GroupID: groupID,
			EnvID:   e.ID,
		})
	}
	return folders
}

// newTestEnvs 构造环境列表（dev/test）
func newTestEnvs() []*envdomain.Environment {
	return []*envdomain.Environment{
		{ID: uuid.New(), Code: "dev", ProjectID: uuid.New()},
		{ID: uuid.New(), Code: "test", ProjectID: uuid.New()},
	}
}

func TestService_Create_Success(t *testing.T) {
	envs := newTestEnvs()
	folderGroupID := uuid.New()
	folders := newTestFolderGroup(folderGroupID, envs)
	// folderID 集合（校验落库的 folder_id 属于该 folder 组）
	folderSet := map[uuid.UUID]bool{}
	for _, f := range folders {
		folderSet[f.ID] = true
	}

	secretRepo := &stubSecretRepo{
		getByFolderKey: func(ctx context.Context, folderIDs []uuid.UUID, key string) (*secretdomain.Secret, error) {
			if len(folderIDs) != 2 || key != "DB_PASSWORD" {
				t.Fatalf("unexpected lookup folderIDs=%v key=%s", folderIDs, key)
			}
			return nil, nil
		},
		createBatch: func(ctx context.Context, secrets []*secretdomain.Secret) error {
			if len(secrets) != 2 {
				t.Fatalf("expected 2 secrets (one per env), got %d", len(secrets))
			}
			// 全组共享同一 group_id
			if secrets[0].GroupID == uuid.Nil || secrets[0].GroupID != secrets[1].GroupID {
				t.Fatalf("group_id must be shared: %+v %+v", secrets[0], secrets[1])
			}
			for _, s := range secrets {
				// 密文不包含明文
				if strings.Contains(s.ValueCiphertext, "plain-password") {
					t.Fatalf("ciphertext must not contain plaintext: %s", s.ValueCiphertext)
				}
				if !folderSet[s.FolderID] {
					t.Fatalf("folder_id %s not in folder group", s.FolderID)
				}
				if s.EnvCode == "" {
					t.Fatal("env_code should be written")
				}
				if s.Version != 1 {
					t.Fatalf("expected version 1, got %d", s.Version)
				}
				if s.CreateBy != "operator-1" {
					t.Fatalf("operator not propagated: %+v", s)
				}
			}
			return nil
		},
	}
	folderRepo := &stubFolderRepo{
		listByGroupID: func(ctx context.Context, gid uuid.UUID) ([]*folderdomain.Folder, error) {
			if gid != folderGroupID {
				t.Fatalf("unexpected folder group %v", gid)
			}
			return folders, nil
		},
	}
	envRepo := &stubEnvRepo{
		getByID: func(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error) {
			for _, e := range envs {
				if e.ID == id {
					return e, nil
				}
			}
			return nil, nil
		},
	}
	svc := NewService(secretRepo, folderRepo, envRepo, testCipher(t))

	created, err := svc.Create(context.Background(), CreateInput{SecretList: []CreateItemInput{
		{
			FolderGroupID: folderGroupID,
			Key:           "DB_PASSWORD",
			Remark:        "数据库密码",
			Values: []ValueItemInput{
				{EnvID: envs[0].ID, Value: "plain-password"},
				{EnvID: envs[1].ID, Value: "another-password"},
			},
		},
	}}, "operator-1")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("expected 2 created, got %d", len(created))
	}
}

func TestService_Create_KeyExists(t *testing.T) {
	envs := newTestEnvs()
	folderGroupID := uuid.New()
	folders := newTestFolderGroup(folderGroupID, envs)

	secretRepo := &stubSecretRepo{
		getByFolderKey: func(ctx context.Context, folderIDs []uuid.UUID, key string) (*secretdomain.Secret, error) {
			return &secretdomain.Secret{Key: key}, nil
		},
	}
	folderRepo := &stubFolderRepo{
		listByGroupID: func(ctx context.Context, gid uuid.UUID) ([]*folderdomain.Folder, error) {
			return folders, nil
		},
	}
	envRepo := &stubEnvRepo{
		getByID: func(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error) {
			for _, e := range envs {
				if e.ID == id {
					return e, nil
				}
			}
			return nil, nil
		},
	}
	svc := NewService(secretRepo, folderRepo, envRepo, testCipher(t))
	_, err := svc.Create(context.Background(), CreateInput{SecretList: []CreateItemInput{
		{
			FolderGroupID: folderGroupID, Key: "DB_PASSWORD",
			Values: []ValueItemInput{{EnvID: envs[0].ID, Value: "v"}},
		},
	}}, "u")
	if !errors.Is(err, ErrKeyExists) {
		t.Fatalf("expected ErrKeyExists, got %v", err)
	}
}

func TestService_Create_FolderNotFound(t *testing.T) {
	svc := NewService(&stubSecretRepo{}, &stubFolderRepo{}, &stubEnvRepo{}, testCipher(t))
	_, err := svc.Create(context.Background(), CreateInput{SecretList: []CreateItemInput{
		{
			FolderGroupID: uuid.New(), Key: "K",
			Values: []ValueItemInput{{EnvID: uuid.New(), Value: "v"}},
		},
	}}, "u")
	if !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("expected ErrFolderNotFound, got %v", err)
	}
}

func TestService_Create_EnvNotUnderFolder(t *testing.T) {
	envs := newTestEnvs()
	folderGroupID := uuid.New()
	folders := newTestFolderGroup(folderGroupID, envs[:1]) // 组内只有 dev 一个环境

	secretRepo := &stubSecretRepo{}
	folderRepo := &stubFolderRepo{
		listByGroupID: func(ctx context.Context, gid uuid.UUID) ([]*folderdomain.Folder, error) {
			return folders, nil
		},
	}
	envRepo := &stubEnvRepo{
		getByID: func(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error) {
			for _, e := range envs {
				if e.ID == id {
					return e, nil
				}
			}
			return nil, nil
		},
	}
	svc := NewService(secretRepo, folderRepo, envRepo, testCipher(t))
	_, err := svc.Create(context.Background(), CreateInput{SecretList: []CreateItemInput{
		{
			FolderGroupID: folderGroupID, Key: "K",
			// test 环境不在该 folder 组内
			Values: []ValueItemInput{{EnvID: envs[1].ID, Value: "v"}},
		},
	}}, "u")
	if !errors.Is(err, ErrEnvNotFound) {
		t.Fatalf("expected ErrEnvNotFound, got %v", err)
	}
}

func TestService_Create_InvalidParam(t *testing.T) {
	svc := NewService(&stubSecretRepo{}, &stubFolderRepo{}, &stubEnvRepo{}, testCipher(t))
	cases := []CreateInput{
		{SecretList: nil},
		{SecretList: []CreateItemInput{{FolderGroupID: uuid.Nil, Key: "K", Values: []ValueItemInput{{EnvID: uuid.New(), Value: "v"}}}}},
		{SecretList: []CreateItemInput{{FolderGroupID: uuid.New(), Key: "", Values: []ValueItemInput{{EnvID: uuid.New(), Value: "v"}}}}},
		{SecretList: []CreateItemInput{{FolderGroupID: uuid.New(), Key: "K", Values: nil}}},
	}
	for _, in := range cases {
		if _, err := svc.Create(context.Background(), in, "u"); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("expected ErrInvalidParam for %+v, got %v", in, err)
		}
	}
}

func TestService_ListByFolder_Success_Decrypted(t *testing.T) {
	envs := newTestEnvs()
	folderGroupID := uuid.New()
	folders := newTestFolderGroup(folderGroupID, envs)

	groupID := uuid.New()
	// 两个环境实例，密文由真 Cipher 生成
	c := testCipher(t)
	ct0, _ := c.Encrypt("dev-password")
	ct1, _ := c.Encrypt("test-password")

	secretRepo := &stubSecretRepo{
		listByFolders: func(ctx context.Context, folderIDs []uuid.UUID) ([]*secretdomain.Secret, error) {
			if len(folderIDs) != 2 {
				t.Fatalf("expected 2 folder ids, got %v", folderIDs)
			}
			// 记录自带 env_code 冗余列，聚合无需跳查 folder/env 表
			return []*secretdomain.Secret{
				{ID: uuid.New(), GroupID: groupID, FolderID: folders[0].ID, EnvCode: "dev", Key: "DB_PASSWORD", Remark: "r", ValueCiphertext: ct0},
				{ID: uuid.New(), GroupID: groupID, FolderID: folders[1].ID, EnvCode: "test", Key: "DB_PASSWORD", Remark: "r", ValueCiphertext: ct1},
			}, nil
		},
	}
	folderRepo := &stubFolderRepo{
		listByGroupID: func(ctx context.Context, gid uuid.UUID) ([]*folderdomain.Folder, error) {
			return folders, nil
		},
	}
	svc := NewService(secretRepo, folderRepo, &stubEnvRepo{}, c)

	views, err := svc.ListByFolder(context.Background(), folderGroupID)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	v := views[0]
	if v.GroupID != groupID || v.Key != "DB_PASSWORD" {
		t.Fatalf("unexpected view meta: %+v", v)
	}
	// values 的 key 是 env code，值为解密后的明文
	if v.Values["dev"].Value != "dev-password" {
		t.Fatalf("dev value not decrypted: %+v", v.Values["dev"])
	}
	if v.Values["test"].Value != "test-password" {
		t.Fatalf("test value not decrypted: %+v", v.Values["test"])
	}
	if v.Values["dev"].FolderID != folders[0].ID {
		t.Fatalf("dev folderID not from record: %+v", v.Values["dev"])
	}
}

func TestService_ListByFolder_FolderNotFound(t *testing.T) {
	svc := NewService(&stubSecretRepo{}, &stubFolderRepo{}, &stubEnvRepo{}, testCipher(t))
	_, err := svc.ListByFolder(context.Background(), uuid.New())
	if !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("expected ErrFolderNotFound, got %v", err)
	}
}

func TestService_GetByGroup_Success(t *testing.T) {
	envs := newTestEnvs()
	folderGroupID := uuid.New()
	folders := newTestFolderGroup(folderGroupID, envs)

	groupID := uuid.New()
	c := testCipher(t)
	ct0, _ := c.Encrypt("prod-token")
	ct1, _ := c.Encrypt("test-token")

	secretRepo := &stubSecretRepo{
		listByGroup: func(ctx context.Context, gid uuid.UUID) ([]*secretdomain.Secret, error) {
			if gid != groupID {
				t.Fatalf("unexpected group %v", gid)
			}
			return []*secretdomain.Secret{
				{ID: uuid.New(), GroupID: groupID, FolderID: folders[0].ID, EnvCode: "dev", Key: "TOKEN", ValueCiphertext: ct0},
				{ID: uuid.New(), GroupID: groupID, FolderID: folders[1].ID, EnvCode: "test", Key: "TOKEN", ValueCiphertext: ct1},
			}, nil
		},
	}
	svc := NewService(secretRepo, &stubFolderRepo{}, &stubEnvRepo{}, c)

	view, err := svc.GetByGroup(context.Background(), groupID)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if view.Values["dev"].Value != "prod-token" || view.Values["test"].Value != "test-token" {
		t.Fatalf("values not decrypted: %+v", view.Values)
	}
}

func TestService_GetByGroup_NotFound(t *testing.T) {
	svc := NewService(&stubSecretRepo{}, &stubFolderRepo{}, &stubEnvRepo{}, testCipher(t))
	if _, err := svc.GetByGroup(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_Delete_Success(t *testing.T) {
	groupID := uuid.New()
	called := false
	secretRepo := &stubSecretRepo{
		listByGroup: func(ctx context.Context, gid uuid.UUID) ([]*secretdomain.Secret, error) {
			return []*secretdomain.Secret{{ID: uuid.New(), GroupID: gid}}, nil
		},
		deleteByGroupID: func(ctx context.Context, gid uuid.UUID, by string) (int64, error) {
			called = true
			if gid != groupID || by != "operator" {
				t.Fatalf("delete args wrong: gid=%v by=%s", gid, by)
			}
			return 2, nil
		},
	}
	svc := NewService(secretRepo, &stubFolderRepo{}, &stubEnvRepo{}, testCipher(t))
	if err := svc.Delete(context.Background(), groupID, "operator"); err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if !called {
		t.Fatal("repo.DeleteByGroupID not called")
	}
}

func TestService_Delete_NotFound(t *testing.T) {
	svc := NewService(&stubSecretRepo{}, &stubFolderRepo{}, &stubEnvRepo{}, testCipher(t))
	if err := svc.Delete(context.Background(), uuid.New(), "u"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_Delete_InvalidParam(t *testing.T) {
	svc := NewService(&stubSecretRepo{}, &stubFolderRepo{}, &stubEnvRepo{}, testCipher(t))
	if err := svc.Delete(context.Background(), uuid.Nil, "u"); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam, got %v", err)
	}
}
