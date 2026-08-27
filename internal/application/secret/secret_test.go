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

type nicknameResolverFunc func(context.Context, string) (string, error)

func (f nicknameResolverFunc) GetNickname(ctx context.Context, userID string) (string, error) {
	return f(ctx, userID)
}

// stubSecretRepo 内存实现的密钥 Repository
type stubSecretRepo struct {
	createBatch         func(ctx context.Context, secrets []*secretdomain.Secret) error
	deleteByGroupID     func(ctx context.Context, groupID uuid.UUID, deleteBy string) (int64, error)
	getByID             func(ctx context.Context, id uuid.UUID) (*secretdomain.Secret, error)
	getByFolderKey      func(ctx context.Context, folderIDs []uuid.UUID, key string) (*secretdomain.Secret, error)
	listByFolders       func(ctx context.Context, folderIDs []uuid.UUID) ([]*secretdomain.Secret, error)
	listByGroup         func(ctx context.Context, groupID uuid.UUID) ([]*secretdomain.Secret, error)
	listByProjectFolder func(ctx context.Context, filter secretdomain.ProjectFolderListFilter) ([]*secretdomain.Secret, error)
	updateValueByIDs    func(ctx context.Context, items []secretdomain.ValueUpdateItem, updateBy string, updateAt time.Time) error
	updateRemarkByGroup func(ctx context.Context, groupID uuid.UUID, remark, updateBy string, updateAt time.Time) (int64, error)
	createHistoryBatch  func(ctx context.Context, histories []*secretdomain.History) error
	listHistoryBySecret func(ctx context.Context, filter secretdomain.HistoryPageFilter) ([]*secretdomain.History, int64, error)
	listHistoryTargets  func(ctx context.Context, filter secretdomain.HistoryTargetFilter) ([]secretdomain.HistoryTarget, error)
	listHistoryByBatch  func(ctx context.Context, filter secretdomain.HistoryBatchFilter) ([]*secretdomain.History, error)
	withTx              func(ctx context.Context, fn func(ctx context.Context) error) error
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
func (s *stubSecretRepo) ListByProjectFolder(ctx context.Context, filter secretdomain.ProjectFolderListFilter) ([]*secretdomain.Secret, error) {
	if s.listByProjectFolder != nil {
		return s.listByProjectFolder(ctx, filter)
	}
	return nil, nil
}
func (s *stubSecretRepo) UpdateValueByIDs(ctx context.Context, items []secretdomain.ValueUpdateItem, updateBy string, updateAt time.Time) error {
	if s.updateValueByIDs != nil {
		return s.updateValueByIDs(ctx, items, updateBy, updateAt)
	}
	return nil
}
func (s *stubSecretRepo) UpdateRemarkByGroupID(ctx context.Context, groupID uuid.UUID, remark, updateBy string, updateAt time.Time) (int64, error) {
	if s.updateRemarkByGroup != nil {
		return s.updateRemarkByGroup(ctx, groupID, remark, updateBy, updateAt)
	}
	return 1, nil
}
func (s *stubSecretRepo) CreateHistoryBatch(ctx context.Context, histories []*secretdomain.History) error {
	if s.createHistoryBatch != nil {
		return s.createHistoryBatch(ctx, histories)
	}
	return nil
}
func (s *stubSecretRepo) ListHistoryBySecretID(ctx context.Context, filter secretdomain.HistoryPageFilter) ([]*secretdomain.History, int64, error) {
	if s.listHistoryBySecret != nil {
		return s.listHistoryBySecret(ctx, filter)
	}
	return nil, 0, nil
}
func (s *stubSecretRepo) ListHistoryTargetsByGroupID(ctx context.Context, filter secretdomain.HistoryTargetFilter) ([]secretdomain.HistoryTarget, error) {
	if s.listHistoryTargets != nil {
		return s.listHistoryTargets(ctx, filter)
	}
	return nil, nil
}
func (s *stubSecretRepo) ListHistoryByBatchID(ctx context.Context, filter secretdomain.HistoryBatchFilter) ([]*secretdomain.History, error) {
	if s.listHistoryByBatch != nil {
		return s.listHistoryByBatch(ctx, filter)
	}
	return nil, nil
}
func (s *stubSecretRepo) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if s.withTx != nil {
		return s.withTx(ctx, fn)
	}
	return fn(ctx)
}

// stubFolderRepo 内存实现的文件夹 Repository（secret 应用服务依赖）
type stubFolderRepo struct {
	listByGroupID func(ctx context.Context, groupID uuid.UUID) ([]*folderdomain.Folder, error)
	getByID       func(ctx context.Context, id uuid.UUID) (*folderdomain.Folder, error)
}

func (s *stubFolderRepo) CreateBatch(ctx context.Context, folders []*folderdomain.Folder) error {
	return nil
}
func (s *stubFolderRepo) UpdateByGroupID(ctx context.Context, groupID uuid.UUID, name, remark, manager string, keyPattern *string, updateBy string, updateAt time.Time) (int64, error) {
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
	batchID := uuid.New()
	var capturedHistories []*secretdomain.History
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
		createHistoryBatch: func(ctx context.Context, histories []*secretdomain.History) error {
			capturedHistories = histories
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

	created, err := svc.Create(context.Background(), CreateInput{BatchID: batchID, SecretList: []CreateItemInput{
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
	if len(capturedHistories) != 2 {
		t.Fatalf("expected 2 initial histories, got %d", len(capturedHistories))
	}
	for i, history := range capturedHistories {
		if history.SecretID != created[i].ID || history.BatchID != batchID || history.Version != 1 {
			t.Fatalf("unexpected initial history: %+v", history)
		}
		if history.CommitMsg != initialCommitMsg || history.CreateBy != "operator-1" {
			t.Fatalf("initial history audit fields wrong: %+v", history)
		}
		if history.ValueCiphertext != created[i].ValueCiphertext {
			t.Fatalf("initial history must snapshot created ciphertext")
		}
	}
}

func TestService_Create_PartialEnvironmentValues(t *testing.T) {
	envs := newTestEnvs()
	folderGroupID := uuid.New()
	folders := newTestFolderGroup(folderGroupID, envs)
	var createdBatch []*secretdomain.Secret
	var histories []*secretdomain.History

	secretRepo := &stubSecretRepo{
		getByFolderKey: func(context.Context, []uuid.UUID, string) (*secretdomain.Secret, error) {
			return nil, nil
		},
		createBatch: func(_ context.Context, secrets []*secretdomain.Secret) error {
			createdBatch = secrets
			return nil
		},
		createHistoryBatch: func(_ context.Context, items []*secretdomain.History) error {
			histories = items
			return nil
		},
	}
	folderRepo := &stubFolderRepo{listByGroupID: func(context.Context, uuid.UUID) ([]*folderdomain.Folder, error) {
		return folders, nil
	}}
	envRepo := &stubEnvRepo{getByID: func(_ context.Context, id uuid.UUID) (*envdomain.Environment, error) {
		for _, env := range envs {
			if env.ID == id {
				return env, nil
			}
		}
		return nil, nil
	}}

	svc := NewService(secretRepo, folderRepo, envRepo, testCipher(t))
	created, err := svc.Create(context.Background(), CreateInput{SecretList: []CreateItemInput{{
		FolderGroupID: folderGroupID,
		Key:           "PARTIAL_SECRET",
		Values: []ValueItemInput{
			{EnvID: envs[0].ID, Value: "dev-value"},
			{EnvID: envs[1].ID, Value: ""},
		},
	}}}, "operator")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(created) != 2 || len(createdBatch) != 2 || len(histories) != 2 {
		t.Fatalf("expected both submitted environments, got created=%d batch=%d histories=%d", len(created), len(createdBatch), len(histories))
	}
	if created[0].EnvCode != "dev" || created[0].FolderID != folders[0].ID {
		t.Fatalf("unexpected created environment: %+v", created[0])
	}
	plaintext, err := testCipher(t).Decrypt(created[0].ValueCiphertext)
	if err != nil || plaintext != "dev-value" {
		t.Fatalf("unexpected encrypted value plaintext=%q err=%v", plaintext, err)
	}
	emptyPlaintext, err := testCipher(t).Decrypt(created[1].ValueCiphertext)
	if err != nil || emptyPlaintext != "" {
		t.Fatalf("empty environment value must remain updateable, plaintext=%q err=%v", emptyPlaintext, err)
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

func TestService_Create_KeyPatternMismatch(t *testing.T) {
	envs := newTestEnvs()
	folderGroupID := uuid.New()
	folders := newTestFolderGroup(folderGroupID, envs)
	for _, folder := range folders {
		folder.KeyPattern = `^[A-Z][A-Z0-9_]*$`
	}

	svc := NewService(
		&stubSecretRepo{},
		&stubFolderRepo{listByGroupID: func(context.Context, uuid.UUID) ([]*folderdomain.Folder, error) {
			return folders, nil
		}},
		&stubEnvRepo{},
		testCipher(t),
	)
	_, err := svc.Create(context.Background(), CreateInput{SecretList: []CreateItemInput{{
		FolderGroupID: folderGroupID,
		Key:           "db-password",
		Values:        []ValueItemInput{{EnvID: envs[0].ID, Value: "value"}},
	}}}, "operator")
	if !errors.Is(err, ErrKeyPatternMismatch) {
		t.Fatalf("Create() error = %v, want ErrKeyPatternMismatch", err)
	}
}

func TestService_Create_InconsistentFolderKeyPattern(t *testing.T) {
	envs := newTestEnvs()
	folderGroupID := uuid.New()
	folders := newTestFolderGroup(folderGroupID, envs)
	folders[0].KeyPattern = `^[A-Z]+$`
	folders[1].KeyPattern = `^[a-z]+$`

	svc := NewService(
		&stubSecretRepo{},
		&stubFolderRepo{listByGroupID: func(context.Context, uuid.UUID) ([]*folderdomain.Folder, error) {
			return folders, nil
		}},
		&stubEnvRepo{},
		testCipher(t),
	)
	_, err := svc.Create(context.Background(), CreateInput{SecretList: []CreateItemInput{{
		FolderGroupID: folderGroupID,
		Key:           "VALID",
		Values:        []ValueItemInput{{EnvID: envs[0].ID, Value: "value"}},
	}}}, "operator")
	if !errors.Is(err, ErrFolderPatternInvalid) {
		t.Fatalf("Create() error = %v, want ErrFolderPatternInvalid", err)
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
		{SecretList: []CreateItemInput{{FolderGroupID: uuid.New(), Key: "K", Values: []ValueItemInput{{EnvID: uuid.New(), Value: ""}}}}},
		{SecretList: []CreateItemInput{{FolderGroupID: uuid.New(), Key: "K", Values: []ValueItemInput{{EnvID: uuid.New(), Value: "   "}, {EnvID: uuid.New(), Value: "\t"}}}}},
	}
	for _, in := range cases {
		if _, err := svc.Create(context.Background(), in, "u"); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("expected ErrInvalidParam for %+v, got %v", in, err)
		}
	}
}

func TestService_Create_Batch_MultipleSecrets(t *testing.T) {
	envs := newTestEnvs()
	folderGroupID := uuid.New()
	folders := newTestFolderGroup(folderGroupID, envs)

	groups := make(map[string]uuid.UUID) // key -> group_id，方便断言不同 secret 拥有不同 group_id
	secretRepo := &stubSecretRepo{
		getByFolderKey: func(ctx context.Context, folderIDs []uuid.UUID, key string) (*secretdomain.Secret, error) {
			return nil, nil // 全部不冲突
		},
		createBatch: func(ctx context.Context, secrets []*secretdomain.Secret) error {
			// 第一次调用: K1 (2 条), 第二次调用: K2 (2 条)
			k := secrets[0].Key
			if _, ok := groups[k]; !ok {
				groups[k] = secrets[0].GroupID
			}
			return nil
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

	created, err := svc.Create(context.Background(), CreateInput{SecretList: []CreateItemInput{
		{FolderGroupID: folderGroupID, Key: "K1", Values: []ValueItemInput{{EnvID: envs[0].ID, Value: "v1a"}, {EnvID: envs[1].ID, Value: "v1b"}}},
		{FolderGroupID: folderGroupID, Key: "K2", Values: []ValueItemInput{{EnvID: envs[0].ID, Value: "v2a"}, {EnvID: envs[1].ID, Value: "v2b"}}},
	}}, "u")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if len(created) != 4 {
		t.Fatalf("expected 4 created (2 secrets x 2 envs), got %d", len(created))
	}
	// 两个 secret 共享 group_id 不一致（各自独立）
	g1, g2 := groups["K1"], groups["K2"]
	if g1 == uuid.Nil || g2 == uuid.Nil {
		t.Fatalf("group_id missing: %+v", groups)
	}
	if g1 == g2 {
		t.Fatalf("K1/K2 must have distinct group_id, both = %s", g1)
	}
	// 每个 secret 内部各环境共享同一 group_id
	countByGroup := make(map[uuid.UUID]int)
	for _, s := range created {
		countByGroup[s.GroupID]++
	}
	if countByGroup[g1] != 2 || countByGroup[g2] != 2 {
		t.Fatalf("per-group counts wrong: %+v", countByGroup)
	}
}

func TestService_Create_FolderRepoError(t *testing.T) {
	dbErr := errors.New("db down")
	folderRepo := &stubFolderRepo{
		listByGroupID: func(ctx context.Context, gid uuid.UUID) ([]*folderdomain.Folder, error) {
			return nil, dbErr
		},
	}
	svc := NewService(&stubSecretRepo{}, folderRepo, &stubEnvRepo{}, testCipher(t))
	_, err := svc.Create(context.Background(), CreateInput{SecretList: []CreateItemInput{
		{FolderGroupID: uuid.New(), Key: "K", Values: []ValueItemInput{{EnvID: uuid.New(), Value: "v"}}},
	}}, "u")
	if !errors.Is(err, dbErr) {
		t.Fatalf("expected dbErr, got %v", err)
	}
}

func TestService_Create_EnvRepoError(t *testing.T) {
	envs := newTestEnvs()
	folderGroupID := uuid.New()
	folders := newTestFolderGroup(folderGroupID, envs)
	dbErr := errors.New("env db down")
	folderRepo := &stubFolderRepo{
		listByGroupID: func(ctx context.Context, gid uuid.UUID) ([]*folderdomain.Folder, error) {
			return folders, nil
		},
	}
	envRepo := &stubEnvRepo{
		getByID: func(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error) {
			return nil, dbErr
		},
	}
	svc := NewService(&stubSecretRepo{}, folderRepo, envRepo, testCipher(t))
	_, err := svc.Create(context.Background(), CreateInput{SecretList: []CreateItemInput{
		{FolderGroupID: folderGroupID, Key: "K", Values: []ValueItemInput{{EnvID: envs[0].ID, Value: "v"}}},
	}}, "u")
	if !errors.Is(err, dbErr) {
		t.Fatalf("expected dbErr, got %v", err)
	}
}

// 覆盖"folder 引用了一个 env_id，但 env 表已无该记录"场景
// 与 TestService_Create_EnvNotUnderFolder 不同：folder 业务组内有 env，value 也填了该 env，
// 但 envRepo 查不到 → 应返回 ErrEnvNotFound。
func TestService_Create_EnvRecordMissing(t *testing.T) {
	envs := newTestEnvs()
	folderGroupID := uuid.New()
	folders := newTestFolderGroup(folderGroupID, envs)

	folderRepo := &stubFolderRepo{
		listByGroupID: func(ctx context.Context, gid uuid.UUID) ([]*folderdomain.Folder, error) {
			return folders, nil
		},
	}
	envRepo := &stubEnvRepo{
		getByID: func(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error) {
			return nil, nil // env 表查不到
		},
	}
	svc := NewService(&stubSecretRepo{}, folderRepo, envRepo, testCipher(t))
	_, err := svc.Create(context.Background(), CreateInput{SecretList: []CreateItemInput{
		{FolderGroupID: folderGroupID, Key: "K", Values: []ValueItemInput{{EnvID: envs[0].ID, Value: "v"}}},
	}}, "u")
	if !errors.Is(err, ErrEnvNotFound) {
		t.Fatalf("expected ErrEnvNotFound, got %v", err)
	}
}

func TestService_Create_GetByFolderKeyError(t *testing.T) {
	envs := newTestEnvs()
	folderGroupID := uuid.New()
	folders := newTestFolderGroup(folderGroupID, envs)
	dbErr := errors.New("query failed")
	secretRepo := &stubSecretRepo{
		getByFolderKey: func(ctx context.Context, folderIDs []uuid.UUID, key string) (*secretdomain.Secret, error) {
			return nil, dbErr
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
		{FolderGroupID: folderGroupID, Key: "K", Values: []ValueItemInput{{EnvID: envs[0].ID, Value: "v"}}},
	}}, "u")
	if !errors.Is(err, dbErr) {
		t.Fatalf("expected dbErr, got %v", err)
	}
}

func TestService_Create_CreateBatchError(t *testing.T) {
	envs := newTestEnvs()
	folderGroupID := uuid.New()
	folders := newTestFolderGroup(folderGroupID, envs)
	dbErr := errors.New("insert failed")
	secretRepo := &stubSecretRepo{
		createBatch: func(ctx context.Context, secrets []*secretdomain.Secret) error {
			return dbErr
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
		{FolderGroupID: folderGroupID, Key: "K", Values: []ValueItemInput{{EnvID: envs[0].ID, Value: "v"}}},
	}}, "u")
	if !errors.Is(err, dbErr) {
		t.Fatalf("expected dbErr, got %v", err)
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

func TestService_List_ProjectFolderMode(t *testing.T) {
	projectID := uuid.New()
	c := testCipher(t)
	devCiphertext, _ := c.Encrypt("dev-value")
	testCiphertext, _ := c.Encrypt("test-value")
	groupID := uuid.New()

	repo := &stubSecretRepo{
		listByProjectFolder: func(ctx context.Context, filter secretdomain.ProjectFolderListFilter) ([]*secretdomain.Secret, error) {
			if filter.ProjectID != projectID || filter.FolderCode != "groups" {
				t.Fatalf("unexpected project/folder filter: %+v", filter)
			}
			if len(filter.EnvCodes) != 2 || filter.EnvCodes[0] != "dev" || filter.EnvCodes[1] != "test" {
				t.Fatalf("unexpected env filter: %+v", filter.EnvCodes)
			}
			if len(filter.Keys) != 1 || filter.Keys[0] != "DB_PASSWORD" {
				t.Fatalf("unexpected key filter: %+v", filter.Keys)
			}
			return []*secretdomain.Secret{
				{ID: uuid.New(), GroupID: groupID, FolderID: uuid.New(), EnvCode: "dev", Key: "DB_PASSWORD", ValueCiphertext: devCiphertext},
				{ID: uuid.New(), GroupID: groupID, FolderID: uuid.New(), EnvCode: "test", Key: "DB_PASSWORD", ValueCiphertext: testCiphertext},
			}, nil
		},
	}
	svc := NewService(repo, &stubFolderRepo{}, &stubEnvRepo{}, c)

	views, err := svc.List(context.Background(), ListInput{
		ProjectID: projectID, FolderCode: " groups ", EnvList: []string{" dev ", "test", "dev"}, KeyList: []string{" DB_PASSWORD ", "DB_PASSWORD"},
	})
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if len(views) != 1 || views[0].Key != "DB_PASSWORD" || len(views[0].Values) != 2 {
		t.Fatalf("unexpected project folder views: %+v", views)
	}
}

func TestService_List_ProjectFolderMode_AllKeysWhenKeyListEmpty(t *testing.T) {
	projectID := uuid.New()
	c := testCipher(t)
	valueCiphertext, _ := c.Encrypt("value")
	called := false
	repo := &stubSecretRepo{
		listByProjectFolder: func(ctx context.Context, filter secretdomain.ProjectFolderListFilter) ([]*secretdomain.Secret, error) {
			called = true
			if len(filter.Keys) != 0 {
				t.Fatalf("expected empty key filter, got %+v", filter.Keys)
			}
			return []*secretdomain.Secret{{
				ID: uuid.New(), GroupID: uuid.New(), FolderID: uuid.New(), EnvCode: "prod", Key: "ANY_KEY", ValueCiphertext: valueCiphertext,
			}}, nil
		},
	}
	svc := NewService(repo, &stubFolderRepo{}, &stubEnvRepo{}, c)
	_, err := svc.List(context.Background(), ListInput{ProjectID: projectID, FolderCode: "global", EnvList: []string{"prod"}})
	if err != nil || !called {
		t.Fatalf("unexpected result err=%v called=%v", err, called)
	}
}

func TestService_List_ProjectFolderMode_InvalidParams(t *testing.T) {
	svc := NewService(&stubSecretRepo{}, &stubFolderRepo{}, &stubEnvRepo{}, testCipher(t))
	_, err := svc.List(context.Background(), ListInput{ProjectID: uuid.New(), FolderCode: "global"})
	if !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam, got %v", err)
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
	devUpdateAt := time.Date(2026, time.August, 24, 10, 11, 12, 0, time.UTC)
	testUpdateAt := time.Date(2026, time.August, 24, 11, 12, 13, 0, time.UTC)

	secretRepo := &stubSecretRepo{
		listByGroup: func(ctx context.Context, gid uuid.UUID) ([]*secretdomain.Secret, error) {
			if gid != groupID {
				t.Fatalf("unexpected group %v", gid)
			}
			return []*secretdomain.Secret{
				{ID: uuid.New(), GroupID: groupID, FolderID: folders[0].ID, EnvCode: "dev", Key: "TOKEN", ValueCiphertext: ct0, Version: 3, ValueType: "string", UpdateAt: devUpdateAt},
				{ID: uuid.New(), GroupID: groupID, FolderID: folders[1].ID, EnvCode: "test", Key: "TOKEN", ValueCiphertext: ct1, Version: 5, ValueType: "number", UpdateAt: testUpdateAt},
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
	if view.Values["dev"].Version != 3 || view.Values["dev"].ValueType != "string" {
		t.Fatalf("dev detail fields not propagated: %+v", view.Values["dev"])
	}
	if view.Values["test"].Version != 5 || view.Values["test"].ValueType != "number" {
		t.Fatalf("test detail fields not propagated: %+v", view.Values["test"])
	}
	if !view.Values["dev"].UpdateAt.Equal(devUpdateAt) || !view.Values["test"].UpdateAt.Equal(testUpdateAt) {
		t.Fatalf("environment update times not propagated: %+v", view.Values)
	}
}

func TestService_GetByGroup_NotFound(t *testing.T) {
	svc := NewService(&stubSecretRepo{}, &stubFolderRepo{}, &stubEnvRepo{}, testCipher(t))
	if _, err := svc.GetByGroup(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_History_SecretIDPriorityOverBatchAndPagination(t *testing.T) {
	secretID := uuid.New()
	batchID := uuid.New()
	groupID := uuid.New()
	cipher := testCipher(t)
	ciphertext, _ := cipher.Encrypt("version-value")
	repo := &stubSecretRepo{
		listHistoryBySecret: func(ctx context.Context, filter secretdomain.HistoryPageFilter) ([]*secretdomain.History, int64, error) {
			if filter.SecretID != secretID || filter.Offset != 20 || filter.Limit != 10 || filter.UserID != "reader-1" {
				t.Fatalf("unexpected secret history query: %+v", filter)
			}
			if len(filter.EnvCodes) != 2 || filter.EnvCodes[0] != "prod" || filter.EnvCodes[1] != "test" {
				t.Fatalf("environment filter not normalized: %+v", filter.EnvCodes)
			}
			return []*secretdomain.History{{
				ID: uuid.New(), SecretID: secretID, BatchID: batchID, GroupID: groupID,
				FolderID: uuid.New(), EnvCode: "prod", ValueCiphertext: ciphertext,
				ValueType: "string", Version: 3, CommitMsg: "rotate", CreateBy: "u",
			}}, 21, nil
		},
		listHistoryByBatch: func(ctx context.Context, filter secretdomain.HistoryBatchFilter) ([]*secretdomain.History, error) {
			t.Fatal("batch query must not run when secretId is present")
			return nil, nil
		},
	}
	resolver := nicknameResolverFunc(func(_ context.Context, userID string) (string, error) {
		if userID != "u" {
			t.Fatalf("unexpected creator id %q", userID)
		}
		return "创建人一", nil
	})
	svc := NewService(repo, &stubFolderRepo{}, &stubEnvRepo{}, cipher, resolver)
	result, err := svc.History(context.Background(), HistoryInput{
		SecretID: secretID, BatchID: batchID, EnvList: []string{" prod ", "test", "prod", ""}, UserID: " reader-1 ", PageNum: 3, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if result.Total != 21 || len(result.HistoryList) != 1 {
		t.Fatalf("unexpected history result: %+v", result)
	}
	if result.HistoryList[0].Value != "version-value" || result.HistoryList[0].Version != 3 || result.HistoryList[0].CommitMsg != "rotate" || result.HistoryList[0].CreateByName != "创建人一" {
		t.Fatalf("history not decrypted/mapped: %+v", result.HistoryList[0])
	}
}

func TestService_History_BatchWithoutPagination(t *testing.T) {
	batchID := uuid.New()
	firstGroupID, secondGroupID := uuid.New(), uuid.New()
	devEnvID, prodEnvID, testEnvID := uuid.New(), uuid.New(), uuid.New()
	devSecretID, prodSecretID, testSecretID := uuid.New(), uuid.New(), uuid.New()
	cipher := testCipher(t)
	devCiphertext, _ := cipher.Encrypt("dev-value")
	prodCiphertext, _ := cipher.Encrypt("prod-value")
	testCiphertext, _ := cipher.Encrypt("test-value")
	repo := &stubSecretRepo{
		listHistoryByBatch: func(ctx context.Context, filter secretdomain.HistoryBatchFilter) ([]*secretdomain.History, error) {
			if filter.BatchID != batchID || len(filter.EnvCodes) != 0 {
				t.Fatalf("unexpected batch history filter: %+v", filter)
			}
			return []*secretdomain.History{
				{
					SecretID: testSecretID, BatchID: batchID, GroupID: secondGroupID, EnvID: testEnvID,
					EnvCode: "test", Key: "DB_USER", Remark: "database user", ValueCiphertext: testCiphertext,
					Version: 1, CreateBy: "missing",
				},
				{
					SecretID: devSecretID, BatchID: batchID, GroupID: firstGroupID, EnvID: devEnvID,
					EnvCode: "dev", Key: "DB_HOST", Remark: "database host", ValueCiphertext: devCiphertext,
					Version: 2, CreateBy: "missing",
				},
				{
					SecretID: prodSecretID, BatchID: batchID, GroupID: firstGroupID, EnvID: prodEnvID,
					EnvCode: "prod", Key: "DB_HOST", Remark: "database host", ValueCiphertext: prodCiphertext,
					Version: 3, CreateBy: "missing",
				},
			}, nil
		},
	}
	resolver := nicknameResolverFunc(func(context.Context, string) (string, error) {
		return "", errors.New("user not found")
	})
	svc := NewService(repo, &stubFolderRepo{}, &stubEnvRepo{}, cipher, resolver)
	result, err := svc.History(context.Background(), HistoryInput{BatchID: batchID, PageNum: 99, PageSize: 1})
	if err != nil {
		t.Fatalf("unexpected batch history result result=%+v err=%v", result, err)
	}
	if result.Total != 0 || result.HistoryList != nil || len(result.BatchHistories) != 2 {
		t.Fatalf("batch history must use grouped result: %+v", result)
	}
	first := result.BatchHistories[0]
	if first.GroupID != firstGroupID || first.Key != "DB_HOST" || first.Remark != "database host" || len(first.Versions) != 2 {
		t.Fatalf("unexpected first group: %+v", first)
	}
	if version := first.Versions[devEnvID]; version.SecretID != devSecretID || version.Value != "dev-value" || version.Version != 2 || version.CreateByName != "" {
		t.Fatalf("unexpected dev version: %+v", version)
	}
	if version := first.Versions[prodEnvID]; version.SecretID != prodSecretID || version.Value != "prod-value" || version.Version != 3 {
		t.Fatalf("unexpected prod version: %+v", version)
	}
	second := result.BatchHistories[1]
	if second.GroupID != secondGroupID || second.Key != "DB_USER" || second.Remark != "database user" || len(second.Versions) != 1 {
		t.Fatalf("unexpected second group: %+v", second)
	}
	if version := second.Versions[testEnvID]; version.SecretID != testSecretID || version.Value != "test-value" {
		t.Fatalf("unexpected test version: %+v", version)
	}
}

func TestService_ListByFolder_SortsByKey(t *testing.T) {
	folderGroupID := uuid.New()
	folderID := uuid.New()
	cipher := testCipher(t)
	ciphertext, _ := cipher.Encrypt("value")
	repo := &stubSecretRepo{
		listByFolders: func(context.Context, []uuid.UUID) ([]*secretdomain.Secret, error) {
			return []*secretdomain.Secret{
				{ID: uuid.New(), GroupID: uuid.New(), FolderID: folderID, EnvCode: "dev", Key: "REDIS_HOST", ValueCiphertext: ciphertext},
				{ID: uuid.New(), GroupID: uuid.New(), FolderID: folderID, EnvCode: "dev", Key: "DB_USER", ValueCiphertext: ciphertext},
				{ID: uuid.New(), GroupID: uuid.New(), FolderID: folderID, EnvCode: "dev", Key: "DB_HOST", ValueCiphertext: ciphertext},
			}, nil
		},
	}
	folderRepo := &stubFolderRepo{
		listByGroupID: func(context.Context, uuid.UUID) ([]*folderdomain.Folder, error) {
			return []*folderdomain.Folder{{ID: folderID}}, nil
		},
	}

	views, err := NewService(repo, folderRepo, &stubEnvRepo{}, cipher).ListByFolder(context.Background(), folderGroupID)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if len(views) != 3 || views[0].Key != "DB_HOST" || views[1].Key != "DB_USER" || views[2].Key != "REDIS_HOST" {
		t.Fatalf("secret views not sorted by key: %+v", views)
	}
}

func TestService_History_GroupIDPriorityAndPaginatesEachEnvironment(t *testing.T) {
	groupID := uuid.New()
	ignoredSecretID, ignoredBatchID := uuid.New(), uuid.New()
	devEnvID, prodEnvID := uuid.New(), uuid.New()
	devSecretID, prodSecretID := uuid.New(), uuid.New()
	cipher := testCipher(t)
	devCiphertext, _ := cipher.Encrypt("dev-v3")
	prodCiphertext, _ := cipher.Encrypt("prod-v2")

	repo := &stubSecretRepo{
		listHistoryTargets: func(_ context.Context, filter secretdomain.HistoryTargetFilter) ([]secretdomain.HistoryTarget, error) {
			if filter.GroupID != groupID || filter.UserID != "reader-1" || len(filter.EnvCodes) != 2 || filter.EnvCodes[0] != "dev" || filter.EnvCodes[1] != "prod" {
				t.Fatalf("unexpected group history filter: %+v", filter)
			}
			return []secretdomain.HistoryTarget{
				{EnvID: devEnvID, SecretID: devSecretID},
				{EnvID: prodEnvID, SecretID: prodSecretID},
			}, nil
		},
		listHistoryBySecret: func(_ context.Context, filter secretdomain.HistoryPageFilter) ([]*secretdomain.History, int64, error) {
			if filter.Offset != 5 || filter.Limit != 5 || filter.UserID != "reader-1" || len(filter.EnvCodes) != 2 {
				t.Fatalf("unexpected environment page filter: %+v", filter)
			}
			switch filter.SecretID {
			case devSecretID:
				return []*secretdomain.History{{SecretID: filter.SecretID, GroupID: groupID, EnvCode: "dev", ValueCiphertext: devCiphertext, Version: 3}}, 8, nil
			case prodSecretID:
				return []*secretdomain.History{{SecretID: filter.SecretID, GroupID: groupID, EnvCode: "prod", ValueCiphertext: prodCiphertext, Version: 2}}, 6, nil
			default:
				t.Fatalf("unexpected secret id %s", filter.SecretID)
				return nil, 0, nil
			}
		},
		listHistoryByBatch: func(context.Context, secretdomain.HistoryBatchFilter) ([]*secretdomain.History, error) {
			t.Fatal("batch query must not run when groupId is present")
			return nil, nil
		},
	}

	result, err := NewService(repo, &stubFolderRepo{}, &stubEnvRepo{}, cipher).History(
		context.Background(), HistoryInput{
			GroupID: groupID, SecretID: ignoredSecretID, BatchID: ignoredBatchID,
			EnvList: []string{"dev", "prod"}, UserID: "reader-1", PageNum: 2, PageSize: 5,
		},
	)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if result.Total != 0 || result.HistoryList != nil || len(result.EnvironmentHistories) != 2 {
		t.Fatalf("unexpected grouped result: %+v", result)
	}
	if page := result.EnvironmentHistories[devEnvID]; page.Total != 8 || len(page.HistoryList) != 1 || page.HistoryList[0].Value != "dev-v3" {
		t.Fatalf("unexpected dev history: %+v", page)
	}
	if page := result.EnvironmentHistories[prodEnvID]; page.Total != 6 || len(page.HistoryList) != 1 || page.HistoryList[0].Value != "prod-v2" {
		t.Fatalf("unexpected prod history: %+v", page)
	}
}

func TestService_History_GroupQueriesEnvironmentsConcurrently(t *testing.T) {
	groupID := uuid.New()
	targets := []secretdomain.HistoryTarget{
		{EnvID: uuid.New(), SecretID: uuid.New()},
		{EnvID: uuid.New(), SecretID: uuid.New()},
	}
	started := make(chan struct{}, len(targets))
	release := make(chan struct{})
	repo := &stubSecretRepo{
		listHistoryTargets: func(context.Context, secretdomain.HistoryTargetFilter) ([]secretdomain.HistoryTarget, error) {
			return targets, nil
		},
		listHistoryBySecret: func(context.Context, secretdomain.HistoryPageFilter) ([]*secretdomain.History, int64, error) {
			started <- struct{}{}
			<-release
			return []*secretdomain.History{}, 0, nil
		},
	}

	type historyCallResult struct {
		result *HistoryResult
		err    error
	}
	cipher := testCipher(t)
	done := make(chan historyCallResult, 1)
	go func() {
		result, err := NewService(repo, &stubFolderRepo{}, &stubEnvRepo{}, cipher).History(
			context.Background(), HistoryInput{GroupID: groupID, PageNum: 1, PageSize: 20},
		)
		done <- historyCallResult{result: result, err: err}
	}()

	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for range targets {
		select {
		case <-started:
		case <-timer.C:
			close(release)
			<-done
			t.Fatal("environment history queries did not run concurrently")
		}
	}
	close(release)
	call := <-done
	if call.err != nil || len(call.result.EnvironmentHistories) != len(targets) {
		t.Fatalf("unexpected concurrent history result=%+v err=%v", call.result, call.err)
	}
}

func TestService_History_RequiresOneQueryID(t *testing.T) {
	result, err := NewService(&stubSecretRepo{}, &stubFolderRepo{}, &stubEnvRepo{}, testCipher(t)).History(
		context.Background(), HistoryInput{},
	)
	if result != nil || !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam, got result=%+v err=%v", result, err)
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

// ---------- Update ----------

// newUpdateEnvGroup 构造一对跨环境 folder 业务组：dev + test
func newUpdateEnvGroup(t *testing.T) (*folderdomain.Folder, *folderdomain.Folder, uuid.UUID) {
	t.Helper()
	groupID := uuid.New()
	dev := &folderdomain.Folder{ID: uuid.New(), GroupID: groupID, EnvID: uuid.New(), Code: "DB", Name: "db", Type: "common"}
	test := &folderdomain.Folder{ID: uuid.New(), GroupID: groupID, EnvID: uuid.New(), Code: "DB", Name: "db", Type: "common"}
	return dev, test, groupID
}

// newUpdateSecretGroup 构造 secret 业务组：两个环境实例（dev/test），每个 secret 关联对应 folder
func newUpdateSecretGroup(t *testing.T, groupID uuid.UUID, devFolder, testFolder *folderdomain.Folder) ([]*secretdomain.Secret, *secretdomain.Secret, *secretdomain.Secret) {
	t.Helper()
	cipher := testCipher(t)
	devCiphertext, _ := cipher.Encrypt("old-dev-value")
	testCiphertext, _ := cipher.Encrypt("old-test-value")
	devSecret := &secretdomain.Secret{
		ID: uuid.New(), GroupID: groupID, FolderID: devFolder.ID,
		EnvCode: "dev", Key: "DB_PASSWORD", ValueCiphertext: devCiphertext, Version: 1,
	}
	testSecret := &secretdomain.Secret{
		ID: uuid.New(), GroupID: groupID, FolderID: testFolder.ID,
		EnvCode: "test", Key: "DB_PASSWORD", ValueCiphertext: testCiphertext, Version: 1,
	}
	return []*secretdomain.Secret{devSecret, testSecret}, devSecret, testSecret
}

func TestService_Update_Success_OnlyRemark(t *testing.T) {
	dev, test, groupID := newUpdateEnvGroup(t)
	secrets, _, _ := newUpdateSecretGroup(t, groupID, dev, test)

	remarkCalled := false
	valueCalled := false
	secretRepo := &stubSecretRepo{
		withTx: func(ctx context.Context, fn func(ctx context.Context) error) error { return fn(ctx) },
		listByGroup: func(ctx context.Context, gid uuid.UUID) ([]*secretdomain.Secret, error) {
			return secrets, nil
		},
		updateRemarkByGroup: func(ctx context.Context, gid uuid.UUID, remark, by string, at time.Time) (int64, error) {
			remarkCalled = true
			if gid != groupID || remark != "new remark" || by != "operator-1" {
				t.Fatalf("updateRemark args wrong: gid=%v remark=%q by=%q", gid, remark, by)
			}
			return 2, nil
		},
		updateValueByIDs: func(ctx context.Context, items []secretdomain.ValueUpdateItem, by string, at time.Time) error {
			valueCalled = true
			return nil
		},
	}
	svc := NewService(secretRepo, &stubFolderRepo{}, &stubEnvRepo{}, testCipher(t))

	err := svc.Update(context.Background(), UpdateInput{CommitMsg: "remark update", Secrets: []UpdateItemInput{
		{GroupID: groupID, Key: "DB_PASSWORD", Remark: "new remark"},
	}}, "operator-1")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if !remarkCalled {
		t.Fatal("UpdateRemarkByGroupID not called")
	}
	if valueCalled {
		t.Fatal("UpdateValueByIDs must NOT be called when values empty")
	}
}

func TestService_Update_Success_OnlyValues(t *testing.T) {
	dev, test, groupID := newUpdateEnvGroup(t)
	secrets, _, _ := newUpdateSecretGroup(t, groupID, dev, test)

	cipher := testCipher(t)
	plaintext := "new-password"

	remarkCalled := false
	var capturedItems []secretdomain.ValueUpdateItem
	var capturedHistories []*secretdomain.History
	batchID := uuid.New()
	secretRepo := &stubSecretRepo{
		withTx: func(ctx context.Context, fn func(ctx context.Context) error) error { return fn(ctx) },
		listByGroup: func(ctx context.Context, gid uuid.UUID) ([]*secretdomain.Secret, error) {
			return secrets, nil
		},
		updateRemarkByGroup: func(ctx context.Context, gid uuid.UUID, remark, by string, at time.Time) (int64, error) {
			remarkCalled = true
			return 0, nil
		},
		updateValueByIDs: func(ctx context.Context, items []secretdomain.ValueUpdateItem, by string, at time.Time) error {
			capturedItems = items
			return nil
		},
		createHistoryBatch: func(ctx context.Context, histories []*secretdomain.History) error {
			capturedHistories = histories
			return nil
		},
	}
	svc := NewService(secretRepo, &stubFolderRepo{}, &stubEnvRepo{}, cipher)

	err := svc.Update(context.Background(), UpdateInput{BatchID: batchID, CommitMsg: "outer message", Secrets: []UpdateItemInput{
		{
			GroupID: groupID, Key: "DB_PASSWORD", Remark: "", CommitMsg: "rotate password",
			Values: []UpdateValueInput{{SecretID: secrets[0].ID, EnvCode: "dev", FolderID: dev.ID, Value: plaintext}},
		},
	}}, "operator-1")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if remarkCalled {
		t.Fatal("UpdateRemarkByGroupID must NOT be called when remark empty")
	}
	if len(capturedItems) != 1 {
		t.Fatalf("expected 1 update item, got %d", len(capturedItems))
	}
	if capturedItems[0].ID != secrets[0].ID {
		t.Fatalf("value update mapped to wrong id: got %s want %s", capturedItems[0].ID, secrets[0].ID)
	}
	if strings.Contains(capturedItems[0].ValueCiphertext, plaintext) {
		t.Fatalf("ciphertext must not contain plaintext: %s", capturedItems[0].ValueCiphertext)
	}
	if capturedItems[0].ExpectedVersion != 1 {
		t.Fatalf("expected optimistic version 1, got %d", capturedItems[0].ExpectedVersion)
	}
	if len(capturedHistories) != 1 {
		t.Fatalf("expected 1 history, got %d", len(capturedHistories))
	}
	history := capturedHistories[0]
	if history.SecretID != secrets[0].ID || history.BatchID != batchID || history.Version != 2 {
		t.Fatalf("unexpected update history: %+v", history)
	}
	if history.CommitMsg != "rotate password" {
		t.Fatalf("item commitMsg must override outer commitMsg: %+v", history)
	}
	gotValue, err := cipher.Decrypt(history.ValueCiphertext)
	if err != nil || gotValue != plaintext {
		t.Fatalf("history ciphertext does not contain updated value: value=%q err=%v", gotValue, err)
	}
}

func TestService_Update_UnchangedValueDoesNotIncreaseVersionOrWriteHistory(t *testing.T) {
	dev, test, groupID := newUpdateEnvGroup(t)
	secrets, _, _ := newUpdateSecretGroup(t, groupID, dev, test)
	valueCalls := 0
	historyCalls := 0
	repo := &stubSecretRepo{
		withTx: func(ctx context.Context, fn func(ctx context.Context) error) error { return fn(ctx) },
		listByGroup: func(ctx context.Context, gid uuid.UUID) ([]*secretdomain.Secret, error) {
			return secrets, nil
		},
		updateValueByIDs: func(ctx context.Context, items []secretdomain.ValueUpdateItem, by string, at time.Time) error {
			valueCalls++
			if len(items) != 0 {
				t.Fatalf("unchanged value must not produce updates: %+v", items)
			}
			return nil
		},
		createHistoryBatch: func(ctx context.Context, histories []*secretdomain.History) error {
			historyCalls++
			if len(histories) != 0 {
				t.Fatalf("unchanged value must not produce histories: %+v", histories)
			}
			return nil
		},
	}
	svc := NewService(repo, &stubFolderRepo{}, &stubEnvRepo{}, testCipher(t))
	err := svc.Update(context.Background(), UpdateInput{CommitMsg: "submitted unchanged value", Secrets: []UpdateItemInput{
		{GroupID: groupID, Values: []UpdateValueInput{{SecretID: secrets[0].ID, Value: "old-dev-value"}}},
	}}, "u")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if valueCalls != 0 || historyCalls != 0 {
		t.Fatalf("unchanged value must not call update/history repositories, value=%d history=%d", valueCalls, historyCalls)
	}
}

func TestService_Update_Success_RemarkAndValues(t *testing.T) {
	dev, test, groupID := newUpdateEnvGroup(t)
	secrets, _, _ := newUpdateSecretGroup(t, groupID, dev, test)

	remarkCalled := false
	valueCalled := false
	historyCalled := false
	secretRepo := &stubSecretRepo{
		withTx: func(ctx context.Context, fn func(ctx context.Context) error) error { return fn(ctx) },
		listByGroup: func(ctx context.Context, gid uuid.UUID) ([]*secretdomain.Secret, error) {
			return secrets, nil
		},
		updateRemarkByGroup: func(ctx context.Context, gid uuid.UUID, remark, by string, at time.Time) (int64, error) {
			remarkCalled = true
			return 2, nil
		},
		updateValueByIDs: func(ctx context.Context, items []secretdomain.ValueUpdateItem, by string, at time.Time) error {
			valueCalled = true
			if len(items) != 2 {
				t.Fatalf("expected 2 value updates, got %d", len(items))
			}
			return nil
		},
		createHistoryBatch: func(ctx context.Context, histories []*secretdomain.History) error {
			historyCalled = true
			if len(histories) != 2 {
				t.Fatalf("expected 2 histories, got %d", len(histories))
			}
			for _, history := range histories {
				if history.CommitMsg != "batch update" {
					t.Fatalf("outer commitMsg must be used as fallback: %+v", history)
				}
			}
			return nil
		},
	}
	svc := NewService(secretRepo, &stubFolderRepo{}, &stubEnvRepo{}, testCipher(t))

	err := svc.Update(context.Background(), UpdateInput{CommitMsg: "batch update", Secrets: []UpdateItemInput{
		{
			GroupID: groupID, Key: "DB_PASSWORD", Remark: "remark-update",
			Values: []UpdateValueInput{
				{SecretID: secrets[0].ID, EnvCode: "dev", FolderID: dev.ID, Value: "v1"},
				{SecretID: secrets[1].ID, EnvCode: "test", FolderID: test.ID, Value: "v2"},
			},
		},
	}}, "u")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if !remarkCalled || !valueCalled || !historyCalled {
		t.Fatalf("all repo calls expected: remark=%v value=%v history=%v", remarkCalled, valueCalled, historyCalled)
	}
}

func TestService_Update_Success_BatchMultipleItems(t *testing.T) {
	dev1, test1, groupID1 := newUpdateEnvGroup(t)
	dev2, test2, groupID2 := newUpdateEnvGroup(t)
	secrets1, _, _ := newUpdateSecretGroup(t, groupID1, dev1, test1)
	secrets2, _, _ := newUpdateSecretGroup(t, groupID2, dev2, test2)

	remarkGroups := make(map[uuid.UUID]bool)
	// secret IDs（用于判断两个 group 的 value 都更新到）
	ids1 := map[uuid.UUID]bool{secrets1[0].ID: true, secrets1[1].ID: true}
	ids2 := map[uuid.UUID]bool{secrets2[0].ID: true, secrets2[1].ID: true}
	var seenIDs []uuid.UUID

	secretRepo := &stubSecretRepo{
		withTx: func(ctx context.Context, fn func(ctx context.Context) error) error { return fn(ctx) },
		listByGroup: func(ctx context.Context, gid uuid.UUID) ([]*secretdomain.Secret, error) {
			switch gid {
			case groupID1:
				return secrets1, nil
			case groupID2:
				return secrets2, nil
			}
			return nil, nil
		},
		updateRemarkByGroup: func(ctx context.Context, gid uuid.UUID, remark, by string, at time.Time) (int64, error) {
			remarkGroups[gid] = true
			return 2, nil
		},
		updateValueByIDs: func(ctx context.Context, items []secretdomain.ValueUpdateItem, by string, at time.Time) error {
			for _, it := range items {
				seenIDs = append(seenIDs, it.ID)
			}
			return nil
		},
	}
	svc := NewService(secretRepo, &stubFolderRepo{}, &stubEnvRepo{}, testCipher(t))

	err := svc.Update(context.Background(), UpdateInput{CommitMsg: "batch update", Secrets: []UpdateItemInput{
		{GroupID: groupID1, Key: "K1", Remark: "r1", Values: []UpdateValueInput{{SecretID: secrets1[0].ID, FolderID: dev1.ID, Value: "v1"}}},
		{GroupID: groupID2, Key: "K2", Remark: "r2", Values: []UpdateValueInput{{SecretID: secrets2[0].ID, FolderID: dev2.ID, Value: "v2"}}},
	}}, "u")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if !remarkGroups[groupID1] || !remarkGroups[groupID2] {
		t.Fatalf("both groups should have remark updated: %+v", remarkGroups)
	}
	if len(seenIDs) != 2 {
		t.Fatalf("expected 2 value updates, got %d", len(seenIDs))
	}
	got1, got2 := false, false
	for _, id := range seenIDs {
		if ids1[id] {
			got1 = true
		}
		if ids2[id] {
			got2 = true
		}
	}
	if !got1 || !got2 {
		t.Fatalf("value updates should cover both groups: seen=%v", seenIDs)
	}
}

func TestService_Update_EmptySecrets(t *testing.T) {
	svc := NewService(&stubSecretRepo{}, &stubFolderRepo{}, &stubEnvRepo{}, testCipher(t))
	if err := svc.Update(context.Background(), UpdateInput{Secrets: nil}, "u"); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam, got %v", err)
	}
}

func TestService_Update_InvalidParam(t *testing.T) {
	dev, _, groupID := newUpdateEnvGroup(t)
	secrets, _, _ := newUpdateSecretGroup(t, groupID, dev, &folderdomain.Folder{})

	secretRepo := &stubSecretRepo{
		withTx: func(ctx context.Context, fn func(ctx context.Context) error) error { return fn(ctx) },
		listByGroup: func(ctx context.Context, gid uuid.UUID) ([]*secretdomain.Secret, error) {
			return secrets, nil
		},
	}
	svc := NewService(secretRepo, &stubFolderRepo{}, &stubEnvRepo{}, testCipher(t))

	cases := []UpdateInput{
		{CommitMsg: "msg", Secrets: []UpdateItemInput{{GroupID: uuid.Nil, Key: "K"}}},
		{CommitMsg: "msg", Secrets: []UpdateItemInput{{GroupID: groupID, Values: []UpdateValueInput{{SecretID: uuid.Nil, Value: "v"}}}}},
		{Secrets: []UpdateItemInput{{GroupID: groupID}}},
	}
	for _, in := range cases {
		if err := svc.Update(context.Background(), in, "u"); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("expected ErrInvalidParam for %+v, got %v", in, err)
		}
	}
}

func TestService_Update_NotFound(t *testing.T) {
	secretRepo := &stubSecretRepo{
		withTx: func(ctx context.Context, fn func(ctx context.Context) error) error { return fn(ctx) },
		listByGroup: func(ctx context.Context, gid uuid.UUID) ([]*secretdomain.Secret, error) {
			return nil, nil // 不存在
		},
	}
	svc := NewService(secretRepo, &stubFolderRepo{}, &stubEnvRepo{}, testCipher(t))
	err := svc.Update(context.Background(), UpdateInput{CommitMsg: "msg", Secrets: []UpdateItemInput{
		{GroupID: uuid.New(), Key: "K"},
	}}, "u")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_Update_SecretNotUnderGroup(t *testing.T) {
	dev, _, groupID := newUpdateEnvGroup(t)
	secrets, _, _ := newUpdateSecretGroup(t, groupID, dev, &folderdomain.Folder{})
	otherSecretID := uuid.New()

	secretRepo := &stubSecretRepo{
		withTx: func(ctx context.Context, fn func(ctx context.Context) error) error { return fn(ctx) },
		listByGroup: func(ctx context.Context, gid uuid.UUID) ([]*secretdomain.Secret, error) {
			return secrets, nil
		},
	}
	svc := NewService(secretRepo, &stubFolderRepo{}, &stubEnvRepo{}, testCipher(t))
	err := svc.Update(context.Background(), UpdateInput{CommitMsg: "msg", Secrets: []UpdateItemInput{
		{GroupID: groupID, Key: "K", Values: []UpdateValueInput{{SecretID: otherSecretID, Value: "v"}}},
	}}, "u")
	if !errors.Is(err, ErrSecretNotUnderGroup) {
		t.Fatalf("expected ErrSecretNotUnderGroup, got %v", err)
	}
}

func TestService_Update_UpdateValueError(t *testing.T) {
	dev, _, groupID := newUpdateEnvGroup(t)
	secrets, _, _ := newUpdateSecretGroup(t, groupID, dev, &folderdomain.Folder{})

	dbErr := errors.New("update failed")
	remarkCalled := false
	secretRepo := &stubSecretRepo{
		withTx: func(ctx context.Context, fn func(ctx context.Context) error) error { return fn(ctx) },
		listByGroup: func(ctx context.Context, gid uuid.UUID) ([]*secretdomain.Secret, error) {
			return secrets, nil
		},
		updateRemarkByGroup: func(ctx context.Context, gid uuid.UUID, remark, by string, at time.Time) (int64, error) {
			remarkCalled = true
			return 2, nil
		},
		updateValueByIDs: func(ctx context.Context, items []secretdomain.ValueUpdateItem, by string, at time.Time) error {
			return dbErr
		},
	}
	svc := NewService(secretRepo, &stubFolderRepo{}, &stubEnvRepo{}, testCipher(t))
	err := svc.Update(context.Background(), UpdateInput{CommitMsg: "msg", Secrets: []UpdateItemInput{
		{GroupID: groupID, Key: "K", Remark: "r", Values: []UpdateValueInput{{SecretID: secrets[0].ID, FolderID: dev.ID, Value: "v"}}},
	}}, "u")
	if !errors.Is(err, dbErr) {
		t.Fatalf("expected dbErr, got %v", err)
	}
	// remark 已调用，但 update value 失败 → 事务返回 error；由 WithTx 触发回滚（验证：调用链路走到 updateValueByIDs）
	if !remarkCalled {
		t.Fatal("remark should have been called before updateValueByIDs")
	}
}

func TestService_Update_TransactionRollback(t *testing.T) {
	// 两个 item：第一个成功（remark + value），第二个 ListByGroupID 失败 → 整体回滚
	dev1, _, groupID1 := newUpdateEnvGroup(t)
	_, _, groupID2 := newUpdateEnvGroup(t)
	secrets1, _, _ := newUpdateSecretGroup(t, groupID1, dev1, &folderdomain.Folder{})

	secondErr := errors.New("second item failed")
	remarkCalls := 0
	valueCalls := 0
	secretRepo := &stubSecretRepo{
		withTx: func(ctx context.Context, fn func(ctx context.Context) error) error {
			// 模拟真实事务：fn 返回 error 时"事务回滚"，但 stub 不真实执行 SQL；通过 fn 是否被调用验证事务边界
			return fn(ctx)
		},
		listByGroup: func(ctx context.Context, gid uuid.UUID) ([]*secretdomain.Secret, error) {
			if gid == groupID2 {
				return nil, secondErr
			}
			return secrets1, nil
		},
		updateRemarkByGroup: func(ctx context.Context, gid uuid.UUID, remark, by string, at time.Time) (int64, error) {
			remarkCalls++
			return 2, nil
		},
		updateValueByIDs: func(ctx context.Context, items []secretdomain.ValueUpdateItem, by string, at time.Time) error {
			valueCalls++
			return nil
		},
	}
	svc := NewService(secretRepo, &stubFolderRepo{}, &stubEnvRepo{}, testCipher(t))
	err := svc.Update(context.Background(), UpdateInput{CommitMsg: "msg", Secrets: []UpdateItemInput{
		{GroupID: groupID1, Key: "K1", Remark: "r1"},
		{GroupID: groupID2, Key: "K2", Remark: "r2"},
	}}, "u")
	if !errors.Is(err, secondErr) {
		t.Fatalf("expected secondErr, got %v", err)
	}
	// 第一个 item 的 remark 应该被调用过（在事务内），整体失败由 WithTx 模拟回滚（生产代码中通过 tx.Commit/Rollback 处理）
	if remarkCalls != 1 {
		t.Fatalf("expected 1 remark call (item1), got %d", remarkCalls)
	}
	if valueCalls != 0 {
		t.Fatalf("expected 0 value calls (no values in inputs), got %d", valueCalls)
	}
}
