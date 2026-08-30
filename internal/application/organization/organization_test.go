package organization

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	orgdomain "env-vault/internal/domain/organization"
)

// stubRepo 内存实现的 Repository，便于 application 层单测
type stubRepo struct {
	getByTenantCode    func(ctx context.Context, tenantID uuid.UUID, code string) (*orgdomain.Organization, error)
	getByID            func(ctx context.Context, id uuid.UUID) (*orgdomain.Organization, error)
	create             func(ctx context.Context, org *orgdomain.Organization) error
	update             func(ctx context.Context, org *orgdomain.Organization) error
	delete             func(ctx context.Context, id uuid.UUID, deleteBy string) error
	list               func(ctx context.Context, filter orgdomain.ListFilter) ([]*orgdomain.Organization, int64, error)
	listWithProjects   func(ctx context.Context, filter orgdomain.WithProjectsFilter) ([]*orgdomain.OrganizationWithProjects, error)
	listCollaborations func(ctx context.Context, userID string) ([]orgdomain.CollaborationProject, error)
}

func (s *stubRepo) Create(ctx context.Context, o *orgdomain.Organization) error {
	if s.create != nil {
		return s.create(ctx, o)
	}
	return nil
}
func (s *stubRepo) Update(ctx context.Context, o *orgdomain.Organization) error {
	if s.update != nil {
		return s.update(ctx, o)
	}
	return nil
}
func (s *stubRepo) Delete(ctx context.Context, id uuid.UUID, deleteBy string) error {
	if s.delete != nil {
		return s.delete(ctx, id, deleteBy)
	}
	return nil
}
func (s *stubRepo) GetByID(ctx context.Context, id uuid.UUID) (*orgdomain.Organization, error) {
	if s.getByID != nil {
		return s.getByID(ctx, id)
	}
	return nil, nil
}
func (s *stubRepo) GetByTenantCode(ctx context.Context, tenantID uuid.UUID, code string) (*orgdomain.Organization, error) {
	if s.getByTenantCode != nil {
		return s.getByTenantCode(ctx, tenantID, code)
	}
	return nil, nil
}
func (s *stubRepo) List(ctx context.Context, filter orgdomain.ListFilter) ([]*orgdomain.Organization, int64, error) {
	if s.list != nil {
		return s.list(ctx, filter)
	}
	return nil, 0, nil
}
func (s *stubRepo) ListWithProjects(ctx context.Context, filter orgdomain.WithProjectsFilter) ([]*orgdomain.OrganizationWithProjects, error) {
	if s.listWithProjects != nil {
		return s.listWithProjects(ctx, filter)
	}
	return nil, nil
}
func (s *stubRepo) ListCollaborationProjects(ctx context.Context, userID string) ([]orgdomain.CollaborationProject, error) {
	if s.listCollaborations != nil {
		return s.listCollaborations(ctx, userID)
	}
	return nil, nil
}

func newTestOrg(tenantID uuid.UUID, code string) *orgdomain.Organization {
	now := time.Now()
	return &orgdomain.Organization{
		ID:       uuid.New(),
		Code:     code,
		Name:     "name-" + code,
		Remark:   "",
		TenantID: tenantID,
		CreateAt: now,
		UpdateAt: now,
	}
}

func TestService_Create_Success(t *testing.T) {
	tenantID := uuid.New()
	repo := &stubRepo{
		getByTenantCode: func(ctx context.Context, tid uuid.UUID, code string) (*orgdomain.Organization, error) {
			if tid != tenantID || code != "org-001" {
				t.Fatalf("unexpected lookup args tenant=%v code=%s", tid, code)
			}
			return nil, nil
		},
		create: func(ctx context.Context, o *orgdomain.Organization) error {
			if o.Code != "org-001" || o.Name != "研发组" || o.TenantID != tenantID {
				t.Fatalf("unexpected create payload: %+v", o)
			}
			if o.ID == uuid.Nil {
				t.Fatal("ID should be generated")
			}
			return nil
		},
	}
	svc := NewService(repo)
	got, err := svc.Create(context.Background(), CreateInput{
		Code: "org-001", Name: "研发组", TenantID: tenantID,
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
	tenantID := uuid.New()
	repo := &stubRepo{
		getByTenantCode: func(ctx context.Context, tid uuid.UUID, code string) (*orgdomain.Organization, error) {
			return newTestOrg(tid, code), nil
		},
	}
	svc := NewService(repo)
	_, err := svc.Create(context.Background(), CreateInput{
		Code: "org-001", Name: "研发组", TenantID: tenantID,
	}, "u")
	if !errors.Is(err, ErrCodeExists) {
		t.Fatalf("expected ErrCodeExists, got %v", err)
	}
}

func TestService_Update_Success(t *testing.T) {
	id := uuid.New()
	tenantID := uuid.New()
	existing := newTestOrg(tenantID, "org-001")
	existing.ID = id

	repo := &stubRepo{
		getByID: func(ctx context.Context, gid uuid.UUID) (*orgdomain.Organization, error) {
			if gid != id {
				t.Fatalf("unexpected id %s", gid)
			}
			return existing, nil
		},
		update: func(ctx context.Context, o *orgdomain.Organization) error {
			if o.Name != "new-name" || o.Remark != "new-remark" || o.Manager != "manager-new" {
				t.Fatalf("fields not updated: %+v", o)
			}
			if o.UpdateBy != "operator-2" {
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
		t.Fatalf("returned org not updated: %+v", got)
	}
}

func TestService_Update_NotFound(t *testing.T) {
	repo := &stubRepo{
		getByID: func(ctx context.Context, id uuid.UUID) (*orgdomain.Organization, error) {
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
	tenantID := uuid.New()
	called := false
	repo := &stubRepo{
		getByID: func(ctx context.Context, gid uuid.UUID) (*orgdomain.Organization, error) {
			if gid != id {
				t.Fatalf("unexpected id %s", gid)
			}
			return newTestOrg(tenantID, "c"), nil
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
		getByID: func(ctx context.Context, id uuid.UUID) (*orgdomain.Organization, error) {
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
	want := newTestOrg(uuid.New(), "c")
	want.ID = id
	repo := &stubRepo{
		getByID: func(ctx context.Context, gid uuid.UUID) (*orgdomain.Organization, error) {
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
		getByID: func(ctx context.Context, id uuid.UUID) (*orgdomain.Organization, error) {
			return nil, nil
		},
	}
	svc := NewService(repo)
	if _, err := svc.GetByID(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_List_PassesFilters(t *testing.T) {
	tenantID := uuid.New()
	captured := orgdomain.ListFilter{}
	repo := &stubRepo{
		list: func(ctx context.Context, filter orgdomain.ListFilter) ([]*orgdomain.Organization, int64, error) {
			captured = filter
			return nil, 0, nil
		},
	}
	svc := NewService(repo)
	_, _, _ = svc.List(context.Background(), ListInput{
		Code: "org", Name: "研发", TenantID: &tenantID, PageNum: 2, PageSize: 50,
	})
	if captured.Code != "org" || captured.Name != "研发" {
		t.Fatalf("code/name filter lost: %+v", captured)
	}
	if captured.TenantID == nil || *captured.TenantID != tenantID {
		t.Fatalf("tenantID filter lost: %+v", captured)
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
		list: func(context.Context, orgdomain.ListFilter) ([]*orgdomain.Organization, int64, error) {
			return []*orgdomain.Organization{{
				ID: uuid.New(), Manager: "manager-1", ProjectCount: 4, MemberCount: 9,
			}}, 1, nil
		},
	}
	resolver := nicknameResolverFunc(func(context.Context, string) (string, error) {
		return "管理员一", nil
	})

	got, total, err := NewService(repo, resolver).List(context.Background(), ListInput{PageNum: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if total != 1 || len(got) != 1 {
		t.Fatalf("unexpected result: total=%d organizations=%+v", total, got)
	}
	if got[0].ProjectCount != 4 || got[0].MemberCount != 9 || got[0].ManagerName != "管理员一" {
		t.Fatalf("unexpected organization summary: %+v", got[0])
	}
}

func TestService_ListWithProjects_PassesPermissionSubject(t *testing.T) {
	var captured orgdomain.WithProjectsFilter
	var collaborationUserID string
	want := []*orgdomain.OrganizationWithProjects{{
		ID: uuid.New(), Name: "研发组", ProjectList: []orgdomain.ProjectSummary{{ID: uuid.New(), Name: "效能平台"}},
	}}
	wantCollaboration := orgdomain.CollaborationProject{ID: uuid.New(), Name: "协作平台"}
	repo := &stubRepo{
		listWithProjects: func(ctx context.Context, filter orgdomain.WithProjectsFilter) ([]*orgdomain.OrganizationWithProjects, error) {
			captured = filter
			return want, nil
		},
		listCollaborations: func(ctx context.Context, userID string) ([]orgdomain.CollaborationProject, error) {
			collaborationUserID = userID
			return []orgdomain.CollaborationProject{wantCollaboration}, nil
		},
	}

	got, err := NewService(repo).ListWithProjects(context.Background(), WithProjectsInput{UserID: "u-1"})
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	if captured.UserID != "u-1" || !captured.OwnOrganizationOnly || collaborationUserID != "u-1" {
		t.Fatalf("unexpected permission subjects: filter=%+v collaborationUserID=%q", captured, collaborationUserID)
	}
	if len(got.Organizations) != 1 || len(got.Organizations[0].ProjectList) != 1 ||
		len(got.CollaborationProjects) != 1 || got.CollaborationProjects[0].ID != wantCollaboration.ID {
		t.Fatalf("unexpected result: %+v", got)
	}
}
