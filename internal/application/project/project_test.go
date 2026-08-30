package project

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	envdomain "env-vault/internal/domain/environment"
	projdomain "env-vault/internal/domain/project"
)

type testTxKey struct{}

// stubRepo 内存实现的 Repository，便于 application 层单测
type stubRepo struct {
	getByOrgCode func(ctx context.Context, orgID uuid.UUID, code string) (*projdomain.Project, error)
	getByID      func(ctx context.Context, id uuid.UUID) (*projdomain.Project, error)
	create       func(ctx context.Context, p *projdomain.Project) error
	update       func(ctx context.Context, p *projdomain.Project) error
	delete       func(ctx context.Context, id uuid.UUID, deleteBy string) error
	list         func(ctx context.Context, filter projdomain.ListFilter) ([]*projdomain.Project, int64, error)
	withTx       func(ctx context.Context, fn func(context.Context) error) error
}

func (s *stubRepo) Create(ctx context.Context, p *projdomain.Project) error {
	if s.create != nil {
		return s.create(ctx, p)
	}
	return nil
}
func (s *stubRepo) Update(ctx context.Context, p *projdomain.Project) error {
	if s.update != nil {
		return s.update(ctx, p)
	}
	return nil
}
func (s *stubRepo) Delete(ctx context.Context, id uuid.UUID, deleteBy string) error {
	if s.delete != nil {
		return s.delete(ctx, id, deleteBy)
	}
	return nil
}
func (s *stubRepo) GetByID(ctx context.Context, id uuid.UUID) (*projdomain.Project, error) {
	if s.getByID != nil {
		return s.getByID(ctx, id)
	}
	return nil, nil
}
func (s *stubRepo) GetByOrgCode(ctx context.Context, orgID uuid.UUID, code string) (*projdomain.Project, error) {
	if s.getByOrgCode != nil {
		return s.getByOrgCode(ctx, orgID, code)
	}
	return nil, nil
}
func (s *stubRepo) List(ctx context.Context, filter projdomain.ListFilter) ([]*projdomain.Project, int64, error) {
	if s.list != nil {
		return s.list(ctx, filter)
	}
	return nil, 0, nil
}

func (s *stubRepo) WithTx(ctx context.Context, fn func(context.Context) error) error {
	if s.withTx != nil {
		return s.withTx(ctx, fn)
	}
	return fn(ctx)
}

type stubEnvironmentRepo struct {
	createBatch func(ctx context.Context, environments []*envdomain.Environment) error
}

type managerEligibilityFunc func(context.Context, string, uuid.UUID) (bool, error)

func (f managerEligibilityFunc) IsOrganizationMember(ctx context.Context, userID string, orgID uuid.UUID) (bool, error) {
	return f(ctx, userID, orgID)
}

func (s *stubEnvironmentRepo) CreateBatch(ctx context.Context, environments []*envdomain.Environment) error {
	if s.createBatch != nil {
		return s.createBatch(ctx, environments)
	}
	return nil
}
func (s *stubEnvironmentRepo) Update(context.Context, *envdomain.Environment) error { return nil }
func (s *stubEnvironmentRepo) Delete(context.Context, uuid.UUID, string) error      { return nil }
func (s *stubEnvironmentRepo) GetByID(context.Context, uuid.UUID) (*envdomain.Environment, error) {
	return nil, nil
}
func (s *stubEnvironmentRepo) GetByProjectCode(context.Context, uuid.UUID, string) (*envdomain.Environment, error) {
	return nil, nil
}
func (s *stubEnvironmentRepo) List(context.Context, uuid.UUID) ([]*envdomain.Environment, error) {
	return nil, nil
}

func newTestProject(orgID uuid.UUID, code string) *projdomain.Project {
	now := time.Now()
	return &projdomain.Project{
		ID:       uuid.New(),
		Code:     code,
		Name:     "name-" + code,
		Remark:   "",
		OrgID:    orgID,
		CreateAt: now,
		UpdateAt: now,
	}
}

func TestService_Create_Success(t *testing.T) {
	orgID := uuid.New()
	repo := &stubRepo{
		getByOrgCode: func(ctx context.Context, oid uuid.UUID, code string) (*projdomain.Project, error) {
			if oid != orgID || code != "p-001" {
				t.Fatalf("unexpected lookup args org=%v code=%s", oid, code)
			}
			return nil, nil
		},
		create: func(ctx context.Context, p *projdomain.Project) error {
			if p.Code != "p-001" || p.Name != "电商平台" || p.OrgID != orgID {
				t.Fatalf("unexpected create payload: %+v", p)
			}
			if p.ID == uuid.Nil {
				t.Fatal("ID should be generated")
			}
			return nil
		},
	}
	svc := NewService(repo)
	got, err := svc.Create(context.Background(), CreateInput{
		Code: "p-001", Name: "电商平台", OrgID: orgID,
	}, "operator-1")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if got.CreateBy != "operator-1" || got.UpdateBy != "operator-1" {
		t.Fatalf("operator not propagated: %+v", got)
	}
	if got.Manager != "operator-1" {
		t.Fatalf("manager should default to operator, got %q", got.Manager)
	}
}

func TestService_Create_CodeExists(t *testing.T) {
	orgID := uuid.New()
	repo := &stubRepo{
		getByOrgCode: func(ctx context.Context, oid uuid.UUID, code string) (*projdomain.Project, error) {
			return newTestProject(oid, code), nil
		},
	}
	svc := NewService(repo)
	_, err := svc.Create(context.Background(), CreateInput{
		Code: "p-001", Name: "电商平台", OrgID: orgID,
	}, "u")
	if !errors.Is(err, ErrCodeExists) {
		t.Fatalf("expected ErrCodeExists, got %v", err)
	}
}

func TestService_Create_RejectsExternalManager(t *testing.T) {
	orgID := uuid.New()
	repo := &stubRepo{
		getByOrgCode: func(context.Context, uuid.UUID, string) (*projdomain.Project, error) {
			return nil, nil
		},
		create: func(context.Context, *projdomain.Project) error {
			t.Fatal("external manager must be rejected before create")
			return nil
		},
	}
	checker := managerEligibilityFunc(func(_ context.Context, userID string, gotOrgID uuid.UUID) (bool, error) {
		if userID != "external-user" || gotOrgID != orgID {
			t.Fatalf("unexpected eligibility input user=%q org=%s", userID, gotOrgID)
		}
		return false, nil
	})

	_, err := NewService(repo, WithManagerEligibilityChecker(checker)).Create(context.Background(), CreateInput{
		Code: "p-001", Name: "电商平台", OrgID: orgID, Manager: "external-user",
	}, "operator-1")
	if !errors.Is(err, ErrManagerNotOrganizationMember) {
		t.Fatalf("expected ErrManagerNotOrganizationMember, got %v", err)
	}
}

func TestService_Create_WithEnvironments(t *testing.T) {
	orgID := uuid.New()
	var projectID uuid.UUID
	transactionCalled := false
	projectCreateCalled := false
	environmentCreateCalled := false

	repo := &stubRepo{
		getByOrgCode: func(context.Context, uuid.UUID, string) (*projdomain.Project, error) {
			return nil, nil
		},
		withTx: func(ctx context.Context, fn func(context.Context) error) error {
			transactionCalled = true
			return fn(context.WithValue(ctx, testTxKey{}, "transaction"))
		},
		create: func(ctx context.Context, p *projdomain.Project) error {
			projectCreateCalled = true
			projectID = p.ID
			if ctx.Value(testTxKey{}) != "transaction" {
				t.Fatal("project create did not receive transaction context")
			}
			return nil
		},
	}
	envRepo := &stubEnvironmentRepo{
		createBatch: func(ctx context.Context, environments []*envdomain.Environment) error {
			environmentCreateCalled = true
			if ctx.Value(testTxKey{}) != "transaction" {
				t.Fatal("environment create did not receive transaction context")
			}
			if len(environments) != 2 {
				t.Fatalf("expected 2 environments, got %d", len(environments))
			}
			for i, environment := range environments {
				if environment.ProjectID != projectID {
					t.Fatalf("environment project ID mismatch: %+v", environment)
				}
				if environment.OrderNo != (i+1)*10 {
					t.Fatalf("expected orderNo %d, got %d", (i+1)*10, environment.OrderNo)
				}
				if environment.CreateBy != "operator-1" || environment.UpdateBy != "operator-1" {
					t.Fatalf("operator not propagated: %+v", environment)
				}
			}
			return nil
		},
	}

	svc := NewService(repo, WithEnvironmentRepository(envRepo))
	got, err := svc.Create(context.Background(), CreateInput{
		Code: "p-001", Name: "电商平台", OrgID: orgID,
		Environments: []CreateEnvironmentInput{
			{Code: "dev", Name: "开发环境"},
			{Code: "test", Name: "测试环境", IsCheckPerm: true},
		},
	}, "operator-1")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if got.ID != projectID {
		t.Fatalf("unexpected project ID: %s", got.ID)
	}
	if !transactionCalled || !projectCreateCalled || !environmentCreateCalled {
		t.Fatalf("expected transaction, project create, and environment create to be called")
	}
}

func TestService_Update_Success(t *testing.T) {
	id := uuid.New()
	orgID := uuid.New()
	existing := newTestProject(orgID, "p-001")
	existing.ID = id

	repo := &stubRepo{
		getByID: func(ctx context.Context, gid uuid.UUID) (*projdomain.Project, error) {
			if gid != id {
				t.Fatalf("unexpected id %s", gid)
			}
			return existing, nil
		},
		update: func(ctx context.Context, p *projdomain.Project) error {
			if p.Name != "new-name" || p.Remark != "new-remark" || p.Manager != "manager-new" {
				t.Fatalf("fields not updated: %+v", p)
			}
			if p.UpdateBy != "operator-2" {
				t.Fatalf("updateBy not set")
			}
			return nil
		},
	}
	svc := NewService(repo)
	got, err := svc.Update(context.Background(), UpdateInput{
		ID: id, Name: "new-name", Remark: "new-remark", Manager: "manager-new",
	}, "operator-2")
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if got.Name != "new-name" || got.Remark != "new-remark" || got.Manager != "manager-new" {
		t.Fatalf("returned project not updated: %+v", got)
	}
}

func TestService_Update_NotFound(t *testing.T) {
	repo := &stubRepo{
		getByID: func(ctx context.Context, id uuid.UUID) (*projdomain.Project, error) {
			return nil, nil
		},
	}
	svc := NewService(repo)
	_, err := svc.Update(context.Background(), UpdateInput{ID: uuid.New(), Name: "n"}, "u")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_Update_RejectsExternalManager(t *testing.T) {
	project := newTestProject(uuid.New(), "p-001")
	repo := &stubRepo{
		getByID: func(context.Context, uuid.UUID) (*projdomain.Project, error) {
			return project, nil
		},
		update: func(context.Context, *projdomain.Project) error {
			t.Fatal("external manager must be rejected before update")
			return nil
		},
	}
	checker := managerEligibilityFunc(func(context.Context, string, uuid.UUID) (bool, error) {
		return false, nil
	})

	_, err := NewService(repo, WithManagerEligibilityChecker(checker)).Update(context.Background(), UpdateInput{
		ID: project.ID, Name: "new-name", Manager: "external-user",
	}, "operator-1")
	if !errors.Is(err, ErrManagerNotOrganizationMember) {
		t.Fatalf("expected ErrManagerNotOrganizationMember, got %v", err)
	}
}

func TestService_Delete_Success(t *testing.T) {
	id := uuid.New()
	orgID := uuid.New()
	called := false
	repo := &stubRepo{
		getByID: func(ctx context.Context, gid uuid.UUID) (*projdomain.Project, error) {
			if gid != id {
				t.Fatalf("unexpected id %s", gid)
			}
			return newTestProject(orgID, "c"), nil
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
		getByID: func(ctx context.Context, id uuid.UUID) (*projdomain.Project, error) {
			return nil, nil
		},
	}
	svc := NewService(repo)
	if err := svc.Delete(context.Background(), uuid.New(), "u"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_GetByID_Success(t *testing.T) {
	id := uuid.New()
	want := newTestProject(uuid.New(), "c")
	want.ID = id
	repo := &stubRepo{
		getByID: func(ctx context.Context, gid uuid.UUID) (*projdomain.Project, error) {
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
		getByID: func(ctx context.Context, id uuid.UUID) (*projdomain.Project, error) {
			return nil, nil
		},
	}
	svc := NewService(repo)
	if _, err := svc.GetByID(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_List_PassesFilters(t *testing.T) {
	orgID := uuid.New()
	captured := projdomain.ListFilter{}
	repo := &stubRepo{
		list: func(ctx context.Context, filter projdomain.ListFilter) ([]*projdomain.Project, int64, error) {
			captured = filter
			return nil, 0, nil
		},
	}
	svc := NewService(repo)
	_, _, _ = svc.List(context.Background(), ListInput{
		Code: "p", Name: "电商", OrgID: &orgID, PageNum: 2, PageSize: 50,
	})
	if captured.Code != "p" || captured.Name != "电商" {
		t.Fatalf("code/name filter lost: %+v", captured)
	}
	if captured.OrgID == nil || *captured.OrgID != orgID {
		t.Fatalf("orgID filter lost: %+v", captured)
	}
	if captured.PageNum != 2 || captured.PageSize != 50 {
		t.Fatalf("pagination lost: %+v", captured)
	}
}

type nicknameResolverFunc func(context.Context, string) (string, error)

func (f nicknameResolverFunc) GetNickname(ctx context.Context, userID string) (string, error) {
	return f(ctx, userID)
}

func TestService_List_EnrichesSummary(t *testing.T) {
	repo := &stubRepo{
		list: func(context.Context, projdomain.ListFilter) ([]*projdomain.Project, int64, error) {
			return []*projdomain.Project{{
				ID: uuid.New(), Manager: "manager-1", FolderCount: 6, MemberCount: 3,
			}}, 1, nil
		},
	}
	resolver := nicknameResolverFunc(func(context.Context, string) (string, error) {
		return "管理员一", nil
	})

	got, total, err := NewService(repo, WithNicknameResolver(resolver)).List(
		context.Background(), ListInput{PageNum: 1, PageSize: 20},
	)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if total != 1 || len(got) != 1 {
		t.Fatalf("unexpected result: total=%d projects=%+v", total, got)
	}
	if got[0].FolderCount != 6 || got[0].MemberCount != 3 || got[0].ManagerName != "管理员一" {
		t.Fatalf("unexpected project summary: %+v", got[0])
	}
}
