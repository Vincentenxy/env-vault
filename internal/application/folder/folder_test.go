package folder

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	envdomain "env-vault/internal/domain/environment"
	folderdomain "env-vault/internal/domain/folder"
)

// stubFolderRepo 内存实现的文件夹 Repository，便于 application 层单测
type stubFolderRepo struct {
	createBatch     func(ctx context.Context, folders []*folderdomain.Folder) error
	updateByIDs     func(ctx context.Context, ids []uuid.UUID, name, remark, updateBy string, updateAt time.Time) (int64, error)
	deleteByEnvCode func(ctx context.Context, envIDs []uuid.UUID, code, deleteBy string) (int64, error)
	getByID         func(ctx context.Context, id uuid.UUID) (*folderdomain.Folder, error)
	getByEnvIDsCode func(ctx context.Context, envIDs []uuid.UUID, code string) (*folderdomain.Folder, error)
	getByEnvCode    func(ctx context.Context, envID uuid.UUID, code string) (*folderdomain.Folder, error)
	getByParentCode func(ctx context.Context, parentID uuid.UUID, code string) (*folderdomain.Folder, error)
	listTopByEnvIDs func(ctx context.Context, envIDs []uuid.UUID, filter folderdomain.ListFilter) ([]*folderdomain.Folder, int64, error)
}

func (s *stubFolderRepo) CreateBatch(ctx context.Context, folders []*folderdomain.Folder) error {
	if s.createBatch != nil {
		return s.createBatch(ctx, folders)
	}
	return nil
}
func (s *stubFolderRepo) UpdateByIDs(ctx context.Context, ids []uuid.UUID, name, remark, updateBy string, updateAt time.Time) (int64, error) {
	if s.updateByIDs != nil {
		return s.updateByIDs(ctx, ids, name, remark, updateBy, updateAt)
	}
	return int64(len(ids)), nil
}
func (s *stubFolderRepo) DeleteByEnvIDsCode(ctx context.Context, envIDs []uuid.UUID, code, deleteBy string) (int64, error) {
	if s.deleteByEnvCode != nil {
		return s.deleteByEnvCode(ctx, envIDs, code, deleteBy)
	}
	return 1, nil
}
func (s *stubFolderRepo) GetByID(ctx context.Context, id uuid.UUID) (*folderdomain.Folder, error) {
	if s.getByID != nil {
		return s.getByID(ctx, id)
	}
	return nil, nil
}
func (s *stubFolderRepo) GetByEnvIDsCode(ctx context.Context, envIDs []uuid.UUID, code string) (*folderdomain.Folder, error) {
	if s.getByEnvIDsCode != nil {
		return s.getByEnvIDsCode(ctx, envIDs, code)
	}
	return nil, nil
}
func (s *stubFolderRepo) GetByEnvCode(ctx context.Context, envID uuid.UUID, code string) (*folderdomain.Folder, error) {
	if s.getByEnvCode != nil {
		return s.getByEnvCode(ctx, envID, code)
	}
	return nil, nil
}
func (s *stubFolderRepo) GetByParentCode(ctx context.Context, parentID uuid.UUID, code string) (*folderdomain.Folder, error) {
	if s.getByParentCode != nil {
		return s.getByParentCode(ctx, parentID, code)
	}
	return nil, nil
}
func (s *stubFolderRepo) ListTopByEnvIDs(ctx context.Context, envIDs []uuid.UUID, filter folderdomain.ListFilter) ([]*folderdomain.Folder, int64, error) {
	if s.listTopByEnvIDs != nil {
		return s.listTopByEnvIDs(ctx, envIDs, filter)
	}
	return nil, 0, nil
}

// stubEnvRepo 内存实现的环境 Repository（folder 应用服务跨环境编排依赖）
type stubEnvRepo struct {
	list    func(ctx context.Context, projectID uuid.UUID) ([]*envdomain.Environment, error)
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
	if s.list != nil {
		return s.list(ctx, projectID)
	}
	return nil, nil
}

func newTestEnvs(projectID uuid.UUID, n int) []*envdomain.Environment {
	envs := make([]*envdomain.Environment, 0, n)
	for i := 0; i < n; i++ {
		envs = append(envs, &envdomain.Environment{
			ID:        uuid.New(),
			ProjectID: projectID,
		})
	}
	return envs
}

func newTestFolder(envID uuid.UUID, code string, parentID *uuid.UUID) *folderdomain.Folder {
	now := time.Now()
	return &folderdomain.Folder{
		ID:             uuid.New(),
		Code:           code,
		Name:           "name-" + code,
		EnvID:          envID,
		ParentFolderID: parentID,
		Type:           folderdomain.TypeCommon,
		CreateAt:       now,
		UpdateAt:       now,
	}
}

func TestService_CreateTop_Success_AllEnvs(t *testing.T) {
	projectID := uuid.New()
	envs := newTestEnvs(projectID, 4)
	envIDs := []uuid.UUID{envs[0].ID, envs[1].ID, envs[2].ID, envs[3].ID}

	folderRepo := &stubFolderRepo{
		getByEnvIDsCode: func(ctx context.Context, ids []uuid.UUID, code string) (*folderdomain.Folder, error) {
			if len(ids) != 4 || code != "global" {
				t.Fatalf("unexpected lookup envIDs=%v code=%s", ids, code)
			}
			return nil, nil
		},
		createBatch: func(ctx context.Context, folders []*folderdomain.Folder) error {
			if len(folders) != 4 {
				t.Fatalf("expected 4 folders (one per env), got %d", len(folders))
			}
			for i, f := range folders {
				if f.EnvID != envs[i].ID || f.Code != "global" || f.ParentFolderID != nil {
					t.Fatalf("unexpected folder[%d]: %+v", i, f)
				}
				if f.ID == uuid.Nil {
					t.Fatal("ID should be generated")
				}
			}
			return nil
		},
	}
	envRepo := &stubEnvRepo{
		list: func(ctx context.Context, pid uuid.UUID) ([]*envdomain.Environment, error) {
			if pid != projectID {
				t.Fatalf("unexpected project %v", pid)
			}
			return envs, nil
		},
	}
	svc := NewService(folderRepo, envRepo)
	got, err := svc.CreateTop(context.Background(), CreateTopInput{
		ProjectID: projectID, Code: "global", Name: "全局目录", Type: folderdomain.TypeCommon,
	}, "operator-1")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 folders, got %d", len(got))
	}
	if got[0].CreateBy != "operator-1" || got[0].UpdateBy != "operator-1" {
		t.Fatalf("operator not propagated: %+v", got[0])
	}
	_ = envIDs
}

func TestService_CreateTop_CodeExists(t *testing.T) {
	projectID := uuid.New()
	envs := newTestEnvs(projectID, 2)
	folderRepo := &stubFolderRepo{
		getByEnvIDsCode: func(ctx context.Context, ids []uuid.UUID, code string) (*folderdomain.Folder, error) {
			return newTestFolder(ids[0], code, nil), nil
		},
	}
	envRepo := &stubEnvRepo{list: func(ctx context.Context, pid uuid.UUID) ([]*envdomain.Environment, error) {
		return envs, nil
	}}
	svc := NewService(folderRepo, envRepo)
	_, err := svc.CreateTop(context.Background(), CreateTopInput{
		ProjectID: projectID, Code: "groups", Name: "分组目录", Type: folderdomain.TypeCommon,
	}, "u")
	if !errors.Is(err, ErrCodeExists) {
		t.Fatalf("expected ErrCodeExists, got %v", err)
	}
}

func TestService_CreateTop_InvalidType(t *testing.T) {
	projectID := uuid.New()
	envRepo := &stubEnvRepo{list: func(ctx context.Context, pid uuid.UUID) ([]*envdomain.Environment, error) {
		return newTestEnvs(projectID, 1), nil
	}}
	svc := NewService(&stubFolderRepo{}, envRepo)
	_, err := svc.CreateTop(context.Background(), CreateTopInput{
		ProjectID: projectID, Code: "xxx", Name: "n", Type: "unknown",
	}, "u")
	if !errors.Is(err, ErrInvalidType) {
		t.Fatalf("expected ErrInvalidType, got %v", err)
	}
}

func TestService_CreateTop_CommonCodeInvalid(t *testing.T) {
	projectID := uuid.New()
	envRepo := &stubEnvRepo{list: func(ctx context.Context, pid uuid.UUID) ([]*envdomain.Environment, error) {
		return newTestEnvs(projectID, 1), nil
	}}
	svc := NewService(&stubFolderRepo{}, envRepo)
	// common 顶级目录仅支持 global / groups
	_, err := svc.CreateTop(context.Background(), CreateTopInput{
		ProjectID: projectID, Code: "other", Name: "n", Type: folderdomain.TypeCommon,
	}, "u")
	if !errors.Is(err, ErrCommonCodeInvalid) {
		t.Fatalf("expected ErrCommonCodeInvalid, got %v", err)
	}
}

func TestService_CreateTop_CustomerCodeAllowed(t *testing.T) {
	projectID := uuid.New()
	envs := newTestEnvs(projectID, 2)
	folderRepo := &stubFolderRepo{
		getByEnvIDsCode: func(ctx context.Context, ids []uuid.UUID, code string) (*folderdomain.Folder, error) {
			return nil, nil
		},
		createBatch: func(ctx context.Context, folders []*folderdomain.Folder) error {
			if folders[0].Type != folderdomain.TypeCustomer {
				t.Fatalf("expected customer type, got %s", folders[0].Type)
			}
			return nil
		},
	}
	envRepo := &stubEnvRepo{list: func(ctx context.Context, pid uuid.UUID) ([]*envdomain.Environment, error) {
		return envs, nil
	}}
	svc := NewService(folderRepo, envRepo)
	// customer 顶级目录 code 不受 global/groups 限制
	_, err := svc.CreateTop(context.Background(), CreateTopInput{
		ProjectID: projectID, Code: "user-1", Name: "用户目录", Type: folderdomain.TypeCustomer,
	}, "u")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
}

func TestService_CreateTop_NoEnvironment(t *testing.T) {
	projectID := uuid.New()
	folderRepo := &stubFolderRepo{
		getByEnvIDsCode: func(ctx context.Context, ids []uuid.UUID, code string) (*folderdomain.Folder, error) {
			t.Fatal("code check must not run when no env")
			return nil, nil
		},
	}
	envRepo := &stubEnvRepo{list: func(ctx context.Context, pid uuid.UUID) ([]*envdomain.Environment, error) {
		return nil, nil
	}}
	svc := NewService(folderRepo, envRepo)
	_, err := svc.CreateTop(context.Background(), CreateTopInput{
		ProjectID: projectID, Code: "global", Name: "n", Type: folderdomain.TypeCommon,
	}, "u")
	if !errors.Is(err, ErrNoEnvironment) {
		t.Fatalf("expected ErrNoEnvironment, got %v", err)
	}
}

func TestService_CreateTop_InvalidParam(t *testing.T) {
	svc := NewService(&stubFolderRepo{}, &stubEnvRepo{})
	cases := []CreateTopInput{
		{ProjectID: uuid.Nil, Code: "c", Name: "n", Type: folderdomain.TypeCommon},
		{ProjectID: uuid.New(), Code: "", Name: "n", Type: folderdomain.TypeCommon},
		{ProjectID: uuid.New(), Code: "c", Name: "", Type: folderdomain.TypeCommon},
	}
	for _, in := range cases {
		if _, err := svc.CreateTop(context.Background(), in, "u"); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("expected ErrInvalidParam for %+v, got %v", in, err)
		}
	}
}

func TestService_CreateSub_Success_AllEnvs(t *testing.T) {
	projectID := uuid.New()
	envs := newTestEnvs(projectID, 3)
	parentFolderID := uuid.New() // 入参：任意环境下 groups 的 id
	groupsIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}

	folderRepo := &stubFolderRepo{
		getByID: func(ctx context.Context, id uuid.UUID) (*folderdomain.Folder, error) {
			if id != parentFolderID {
				t.Fatalf("unexpected parent id %s", id)
			}
			f := newTestFolder(envs[0].ID, "groups", nil)
			f.ID = parentFolderID
			return f, nil
		},
		getByEnvCode: func(ctx context.Context, envID uuid.UUID, code string) (*folderdomain.Folder, error) {
			if code != "groups" {
				t.Fatalf("unexpected code %s", code)
			}
			for i, e := range envs {
				if envID == e.ID {
					g := newTestFolder(envID, "groups", nil)
					g.ID = groupsIDs[i]
					return g, nil
				}
			}
			t.Fatalf("unexpected env %s", envID)
			return nil, nil
		},
		getByParentCode: func(ctx context.Context, pid uuid.UUID, code string) (*folderdomain.Folder, error) {
			if code != "ob_efficient_cfg" {
				t.Fatalf("unexpected code %s", code)
			}
			return nil, nil
		},
		createBatch: func(ctx context.Context, folders []*folderdomain.Folder) error {
			if len(folders) != 3 {
				t.Fatalf("expected 3 folders, got %d", len(folders))
			}
			for i, f := range folders {
				if f.EnvID != envs[i].ID {
					t.Fatalf("folder[%d] env mismatch", i)
				}
				if f.ParentFolderID == nil || *f.ParentFolderID != groupsIDs[i] {
					t.Fatalf("folder[%d] parent mismatch: %+v", i, f)
				}
			}
			return nil
		},
	}
	envRepo := &stubEnvRepo{
		getByID: func(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error) {
			if id != envs[0].ID {
				t.Fatalf("unexpected env %s", id)
			}
			return envs[0], nil
		},
		list: func(ctx context.Context, pid uuid.UUID) ([]*envdomain.Environment, error) {
			if pid != projectID {
				t.Fatalf("unexpected project %v", pid)
			}
			return envs, nil
		},
	}
	svc := NewService(folderRepo, envRepo)
	got, err := svc.CreateSub(context.Background(), CreateSubInput{
		ParentFolderID: parentFolderID, Code: "ob_efficient_cfg", Name: "OB高效配置", Type: folderdomain.TypeCommon,
	}, "operator-1")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 folders, got %d", len(got))
	}
}

func TestService_CreateSub_ParentNotGroups(t *testing.T) {
	// parent 是二级目录，不允许在其下再建
	subID := uuid.New()
	parentID := uuid.New()
	folderRepo := &stubFolderRepo{
		getByID: func(ctx context.Context, id uuid.UUID) (*folderdomain.Folder, error) {
			f := newTestFolder(uuid.New(), "ob_efficient_cfg", &parentID)
			f.ID = subID
			return f, nil
		},
	}
	svc := NewService(folderRepo, &stubEnvRepo{})
	_, err := svc.CreateSub(context.Background(), CreateSubInput{
		ParentFolderID: subID, Code: "x", Name: "n", Type: folderdomain.TypeCommon,
	}, "u")
	if !errors.Is(err, ErrParentNotAllowed) {
		t.Fatalf("expected ErrParentNotAllowed, got %v", err)
	}
}

func TestService_CreateSub_ParentNotFound(t *testing.T) {
	svc := NewService(&stubFolderRepo{
		getByID: func(ctx context.Context, id uuid.UUID) (*folderdomain.Folder, error) {
			return nil, nil
		},
	}, &stubEnvRepo{})
	_, err := svc.CreateSub(context.Background(), CreateSubInput{
		ParentFolderID: uuid.New(), Code: "x", Name: "n", Type: folderdomain.TypeCommon,
	}, "u")
	if !errors.Is(err, ErrParentNotAllowed) {
		t.Fatalf("expected ErrParentNotAllowed, got %v", err)
	}
}

func TestService_CreateSub_InvalidType(t *testing.T) {
	// 二级目录只允许 common（customer 仅一级）
	parentID := uuid.New()
	folderRepo := &stubFolderRepo{
		getByID: func(ctx context.Context, id uuid.UUID) (*folderdomain.Folder, error) {
			f := newTestFolder(uuid.New(), "groups", nil)
			f.ID = parentID
			return f, nil
		},
	}
	svc := NewService(folderRepo, &stubEnvRepo{})
	_, err := svc.CreateSub(context.Background(), CreateSubInput{
		ParentFolderID: parentID, Code: "x", Name: "n", Type: folderdomain.TypeCustomer,
	}, "u")
	if !errors.Is(err, ErrInvalidType) {
		t.Fatalf("expected ErrInvalidType, got %v", err)
	}
}

func TestService_CreateSub_CodeExistsUnderGroups(t *testing.T) {
	projectID := uuid.New()
	envs := newTestEnvs(projectID, 1)
	parentFolderID := uuid.New()
	groupsID := uuid.New()
	folderRepo := &stubFolderRepo{
		getByID: func(ctx context.Context, id uuid.UUID) (*folderdomain.Folder, error) {
			f := newTestFolder(envs[0].ID, "groups", nil)
			f.ID = parentFolderID
			return f, nil
		},
		getByEnvCode: func(ctx context.Context, envID uuid.UUID, code string) (*folderdomain.Folder, error) {
			g := newTestFolder(envID, "groups", nil)
			g.ID = groupsID
			return g, nil
		},
		getByParentCode: func(ctx context.Context, pid uuid.UUID, code string) (*folderdomain.Folder, error) {
			return newTestFolder(uuid.New(), code, &pid), nil
		},
	}
	envRepo := &stubEnvRepo{
		getByID: func(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error) {
			return envs[0], nil
		},
		list: func(ctx context.Context, pid uuid.UUID) ([]*envdomain.Environment, error) {
			return envs, nil
		},
	}
	svc := NewService(folderRepo, envRepo)
	_, err := svc.CreateSub(context.Background(), CreateSubInput{
		ParentFolderID: parentFolderID, Code: "ob_efficient_cfg", Name: "n", Type: folderdomain.TypeCommon,
	}, "u")
	if !errors.Is(err, ErrCodeExists) {
		t.Fatalf("expected ErrCodeExists, got %v", err)
	}
}

func TestService_CreateSub_GroupsMissingInEnv(t *testing.T) {
	projectID := uuid.New()
	envs := newTestEnvs(projectID, 1)
	parentFolderID := uuid.New()
	folderRepo := &stubFolderRepo{
		getByID: func(ctx context.Context, id uuid.UUID) (*folderdomain.Folder, error) {
			f := newTestFolder(envs[0].ID, "groups", nil)
			f.ID = parentFolderID
			return f, nil
		},
		getByEnvCode: func(ctx context.Context, envID uuid.UUID, code string) (*folderdomain.Folder, error) {
			return nil, nil // 该环境下没有 groups
		},
	}
	envRepo := &stubEnvRepo{
		getByID: func(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error) {
			return envs[0], nil
		},
		list: func(ctx context.Context, pid uuid.UUID) ([]*envdomain.Environment, error) {
			return envs, nil
		},
	}
	svc := NewService(folderRepo, envRepo)
	_, err := svc.CreateSub(context.Background(), CreateSubInput{
		ParentFolderID: parentFolderID, Code: "x", Name: "n", Type: folderdomain.TypeCommon,
	}, "u")
	if !errors.Is(err, ErrGroupsNotFound) {
		t.Fatalf("expected ErrGroupsNotFound, got %v", err)
	}
}

func TestService_Update_Success(t *testing.T) {
	ids := []uuid.UUID{uuid.New(), uuid.New()}
	called := false
	folderRepo := &stubFolderRepo{
		updateByIDs: func(ctx context.Context, gids []uuid.UUID, name, remark, updateBy string, updateAt time.Time) (int64, error) {
			called = true
			if len(gids) != 2 || name != "new-name" || remark != "new-remark" {
				t.Fatalf("update args wrong: ids=%v name=%s remark=%s", gids, name, remark)
			}
			if updateBy != "operator" {
				t.Fatalf("updateBy not set: %s", updateBy)
			}
			return 2, nil
		},
	}
	svc := NewService(folderRepo, &stubEnvRepo{})
	if err := svc.Update(context.Background(), UpdateInput{
		IDList: ids, Name: "new-name", Remark: "new-remark",
	}, "operator"); err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if !called {
		t.Fatal("repo.UpdateByIDs not called")
	}
}

func TestService_Update_InvalidParam(t *testing.T) {
	svc := NewService(&stubFolderRepo{}, &stubEnvRepo{})
	if err := svc.Update(context.Background(), UpdateInput{IDList: nil, Name: "n"}, "u"); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam for empty idList")
	}
	if err := svc.Update(context.Background(), UpdateInput{IDList: []uuid.UUID{uuid.New()}, Name: ""}, "u"); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam for empty name")
	}
}

func TestService_Update_NotFound(t *testing.T) {
	folderRepo := &stubFolderRepo{
		updateByIDs: func(ctx context.Context, ids []uuid.UUID, name, remark, updateBy string, updateAt time.Time) (int64, error) {
			return 0, nil
		},
	}
	svc := NewService(folderRepo, &stubEnvRepo{})
	err := svc.Update(context.Background(), UpdateInput{
		IDList: []uuid.UUID{uuid.New()}, Name: "n",
	}, "u")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_Delete_Success(t *testing.T) {
	projectID := uuid.New()
	envs := newTestEnvs(projectID, 2)
	folderRepo := &stubFolderRepo{
		getByEnvIDsCode: func(ctx context.Context, ids []uuid.UUID, code string) (*folderdomain.Folder, error) {
			if len(ids) != 2 || code != "global" {
				t.Fatalf("lookup args wrong: ids=%v code=%s", ids, code)
			}
			return newTestFolder(ids[0], code, nil), nil
		},
		deleteByEnvCode: func(ctx context.Context, ids []uuid.UUID, code, by string) (int64, error) {
			if len(ids) != 2 || code != "global" || by != "operator" {
				t.Fatalf("delete args wrong: ids=%v code=%s by=%s", ids, code, by)
			}
			return 2, nil
		},
	}
	envRepo := &stubEnvRepo{list: func(ctx context.Context, pid uuid.UUID) ([]*envdomain.Environment, error) {
		return envs, nil
	}}
	svc := NewService(folderRepo, envRepo)
	if err := svc.Delete(context.Background(), DeleteInput{
		ProjectID: projectID, FolderCode: "global",
	}, "operator"); err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
}

func TestService_Delete_NotFound(t *testing.T) {
	projectID := uuid.New()
	envs := newTestEnvs(projectID, 1)
	folderRepo := &stubFolderRepo{
		getByEnvIDsCode: func(ctx context.Context, ids []uuid.UUID, code string) (*folderdomain.Folder, error) {
			return nil, nil
		},
	}
	envRepo := &stubEnvRepo{list: func(ctx context.Context, pid uuid.UUID) ([]*envdomain.Environment, error) {
		return envs, nil
	}}
	svc := NewService(folderRepo, envRepo)
	err := svc.Delete(context.Background(), DeleteInput{
		ProjectID: projectID, FolderCode: "global",
	}, "u")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_Delete_InvalidParam(t *testing.T) {
	svc := NewService(&stubFolderRepo{}, &stubEnvRepo{})
	if err := svc.Delete(context.Background(), DeleteInput{ProjectID: uuid.Nil, FolderCode: "c"}, "u"); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam for nil project")
	}
	if err := svc.Delete(context.Background(), DeleteInput{ProjectID: uuid.New(), FolderCode: ""}, "u"); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam for empty code")
	}
}

func TestService_GetByID_NotFound(t *testing.T) {
	svc := NewService(&stubFolderRepo{}, &stubEnvRepo{})
	if _, err := svc.GetByID(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_List_Success_PassesFilters(t *testing.T) {
	projectID := uuid.New()
	envs := newTestEnvs(projectID, 2)
	envIDs := []uuid.UUID{envs[0].ID, envs[1].ID}
	captured := folderdomain.ListFilter{}
	folderRepo := &stubFolderRepo{
		listTopByEnvIDs: func(ctx context.Context, ids []uuid.UUID, filter folderdomain.ListFilter) ([]*folderdomain.Folder, int64, error) {
			if len(ids) != 2 {
				t.Fatalf("expected 2 env ids, got %v", ids)
			}
			captured = filter
			return []*folderdomain.Folder{newTestFolder(ids[0], "global", nil)}, 1, nil
		},
	}
	envRepo := &stubEnvRepo{list: func(ctx context.Context, pid uuid.UUID) ([]*envdomain.Environment, error) {
		return envs, nil
	}}
	svc := NewService(folderRepo, envRepo)
	_, total, err := svc.List(context.Background(), ListInput{
		ProjectID: projectID, Code: "glo", Name: "全局", PageNum: 2, PageSize: 50,
	})
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if total != 1 {
		t.Fatalf("expected total 1, got %d", total)
	}
	if captured.Code != "glo" || captured.Name != "全局" {
		t.Fatalf("filters lost: %+v", captured)
	}
	if captured.PageNum != 2 || captured.PageSize != 50 {
		t.Fatalf("pagination lost: %+v", captured)
	}
	_ = envIDs
}

func TestService_List_DefaultPagination(t *testing.T) {
	projectID := uuid.New()
	captured := folderdomain.ListFilter{}
	folderRepo := &stubFolderRepo{
		listTopByEnvIDs: func(ctx context.Context, ids []uuid.UUID, filter folderdomain.ListFilter) ([]*folderdomain.Folder, int64, error) {
			captured = filter
			return nil, 0, nil
		},
	}
	envRepo := &stubEnvRepo{list: func(ctx context.Context, pid uuid.UUID) ([]*envdomain.Environment, error) {
		return newTestEnvs(projectID, 1), nil
	}}
	svc := NewService(folderRepo, envRepo)
	_, _, _ = svc.List(context.Background(), ListInput{ProjectID: projectID})
	if captured.PageNum != 1 || captured.PageSize != 20 {
		t.Fatalf("expected default pageNum=1 pageSize=20, got %+v", captured)
	}
}

func TestService_List_InvalidParam(t *testing.T) {
	svc := NewService(&stubFolderRepo{}, &stubEnvRepo{})
	if _, _, err := svc.List(context.Background(), ListInput{ProjectID: uuid.Nil}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam, got %v", err)
	}
}
