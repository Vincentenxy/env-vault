package tenant

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	orgdomain "env-vault/internal/domain/organization"
	tenantdomain "env-vault/internal/domain/tenant"
)

type stubTenantRepo struct {
	getByCode      func(context.Context, string) (*tenantdomain.Tenant, error)
	getByID        func(context.Context, uuid.UUID) (*tenantdomain.Tenant, error)
	create         func(context.Context, *tenantdomain.Tenant) error
	update         func(context.Context, *tenantdomain.Tenant) error
	list           func(context.Context, tenantdomain.ListFilter) ([]*tenantdomain.Tenant, int64, error)
	listAccessible func(context.Context, tenantdomain.AccessibleFilter) ([]*tenantdomain.Tenant, error)
}

func (s *stubTenantRepo) Create(ctx context.Context, tenant *tenantdomain.Tenant) error {
	if s.create != nil {
		return s.create(ctx, tenant)
	}
	return nil
}
func (s *stubTenantRepo) Update(ctx context.Context, tenant *tenantdomain.Tenant) error {
	if s.update != nil {
		return s.update(ctx, tenant)
	}
	return nil
}
func (s *stubTenantRepo) Delete(context.Context, uuid.UUID, string) error { return nil }

func (s *stubTenantRepo) GetByID(ctx context.Context, id uuid.UUID) (*tenantdomain.Tenant, error) {
	if s.getByID != nil {
		return s.getByID(ctx, id)
	}
	return nil, nil
}

func (s *stubTenantRepo) GetByCode(ctx context.Context, code string) (*tenantdomain.Tenant, error) {
	if s.getByCode != nil {
		return s.getByCode(ctx, code)
	}
	return nil, nil
}

func (s *stubTenantRepo) List(ctx context.Context, filter tenantdomain.ListFilter) ([]*tenantdomain.Tenant, int64, error) {
	if s.list != nil {
		return s.list(ctx, filter)
	}
	return nil, 0, nil
}
func (s *stubTenantRepo) ListAccessible(ctx context.Context, filter tenantdomain.AccessibleFilter) ([]*tenantdomain.Tenant, error) {
	if s.listAccessible != nil {
		return s.listAccessible(ctx, filter)
	}
	return nil, nil
}

type stubOrgRepo struct {
	listWithProjects func(context.Context, orgdomain.WithProjectsFilter) ([]*orgdomain.OrganizationWithProjects, error)
}

type stubNameCache map[string]string

func (s stubNameCache) GetNickname(_ context.Context, userID string) (string, error) {
	name, ok := s[userID]
	if !ok {
		return "", errors.New("user not found")
	}
	return name, nil
}

func (s *stubOrgRepo) Create(context.Context, *orgdomain.Organization) error { return nil }
func (s *stubOrgRepo) Update(context.Context, *orgdomain.Organization) error { return nil }
func (s *stubOrgRepo) Delete(context.Context, uuid.UUID, string) error       { return nil }
func (s *stubOrgRepo) GetByID(context.Context, uuid.UUID) (*orgdomain.Organization, error) {
	return nil, nil
}
func (s *stubOrgRepo) GetByTenantCode(context.Context, uuid.UUID, string) (*orgdomain.Organization, error) {
	return nil, nil
}
func (s *stubOrgRepo) List(context.Context, orgdomain.ListFilter) ([]*orgdomain.Organization, int64, error) {
	return nil, 0, nil
}
func (s *stubOrgRepo) ListWithProjects(ctx context.Context, filter orgdomain.WithProjectsFilter) ([]*orgdomain.OrganizationWithProjects, error) {
	if s.listWithProjects != nil {
		return s.listWithProjects(ctx, filter)
	}
	return nil, nil
}

func TestService_Create_ManagerDefaultsToOperator(t *testing.T) {
	var created *tenantdomain.Tenant
	repo := &stubTenantRepo{
		create: func(ctx context.Context, tenant *tenantdomain.Tenant) error {
			created = tenant
			return nil
		},
	}

	got, err := NewService(repo, &stubOrgRepo{}, stubNameCache{"operator-1": "管理员一"}).Create(context.Background(), CreateInput{
		Code: "tenant-1", Name: "租户一",
	}, "operator-1")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if created == nil || got.Manager != "operator-1" || created.Manager != "operator-1" {
		t.Fatalf("manager should default to operator, got created=%+v result=%+v", created, got)
	}
	if got.ManagerName != "管理员一" {
		t.Fatalf("expected manager name from cache, got %q", got.ManagerName)
	}
}

func TestService_Update_Manager(t *testing.T) {
	id := uuid.New()
	existing := &tenantdomain.Tenant{ID: id, Name: "旧租户", Manager: "manager-old"}
	repo := &stubTenantRepo{
		getByID: func(_ context.Context, gotID uuid.UUID) (*tenantdomain.Tenant, error) {
			if gotID != id {
				t.Fatalf("unexpected id %s", gotID)
			}
			return existing, nil
		},
		update: func(_ context.Context, tenant *tenantdomain.Tenant) error {
			if tenant.Manager != "manager-new" {
				t.Fatalf("manager not updated: %+v", tenant)
			}
			return nil
		},
	}

	got, err := NewService(repo, nil).Update(context.Background(), UpdateInput{
		ID: id, Name: "新租户", Remark: "new-remark", Manager: " manager-new ",
	}, "operator")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got.Manager != "manager-new" {
		t.Fatalf("unexpected manager %q", got.Manager)
	}
}

func TestService_List_EnrichesTenantSummary(t *testing.T) {
	repo := &stubTenantRepo{
		list: func(context.Context, tenantdomain.ListFilter) ([]*tenantdomain.Tenant, int64, error) {
			return []*tenantdomain.Tenant{
				{ID: uuid.New(), Manager: "manager-1", OrgCount: 3, MemberCount: 12},
				{ID: uuid.New(), Manager: "missing", ManagerName: "stale", OrgCount: 1, MemberCount: 2},
			}, 2, nil
		},
	}

	got, total, err := NewService(repo, &stubOrgRepo{}, stubNameCache{"manager-1": "管理员一"}).List(
		context.Background(), ListInput{PageNum: 1, PageSize: 20},
	)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if total != 2 || len(got) != 2 {
		t.Fatalf("unexpected list result: total=%d tenants=%+v", total, got)
	}
	if got[0].OrgCount != 3 || got[0].MemberCount != 12 || got[0].ManagerName != "管理员一" {
		t.Fatalf("unexpected first tenant summary: %+v", got[0])
	}
	if got[1].ManagerName != "" {
		t.Fatalf("cache miss must return empty manager name, got %q", got[1].ManagerName)
	}
}

func TestService_ListWithOrgProjects(t *testing.T) {
	tenantOneID := uuid.New()
	tenantTwoID := uuid.New()
	orgID := uuid.New()
	projectID := uuid.New()

	tenantRepo := &stubTenantRepo{
		listAccessible: func(ctx context.Context, filter tenantdomain.AccessibleFilter) ([]*tenantdomain.Tenant, error) {
			if filter.UserID != "u-1" {
				t.Fatalf("expected user ID u-1, got %q", filter.UserID)
			}
			return []*tenantdomain.Tenant{
				{ID: tenantOneID, Name: "租户一", Manager: "manager-1"},
				{ID: tenantTwoID, Name: "租户二"},
			}, nil
		},
	}
	orgRepo := &stubOrgRepo{
		listWithProjects: func(ctx context.Context, filter orgdomain.WithProjectsFilter) ([]*orgdomain.OrganizationWithProjects, error) {
			if filter.UserID != "u-1" {
				t.Fatalf("expected user ID u-1, got %q", filter.UserID)
			}
			if len(filter.TenantIDs) != 2 || filter.TenantIDs[0] != tenantOneID || filter.TenantIDs[1] != tenantTwoID {
				t.Fatalf("unexpected tenant scope: %+v", filter.TenantIDs)
			}
			return []*orgdomain.OrganizationWithProjects{{
				ID: orgID, Name: "研发组", TenantID: tenantOneID,
				ProjectList: []orgdomain.ProjectSummary{{ID: projectID, Name: "效能平台"}},
			}}, nil
		},
	}

	got, err := NewService(tenantRepo, orgRepo, nil).ListWithOrgProjects(context.Background(), WithOrgProjectsInput{UserID: " u-1 "})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 tenants, got %d", len(got))
	}
	if got[0].ID != tenantOneID || len(got[0].OrgList) != 1 || got[0].OrgList[0].ID != orgID {
		t.Fatalf("unexpected first tenant tree: %+v", got[0])
	}
	if len(got[0].OrgList[0].ProjectList) != 1 || got[0].OrgList[0].ProjectList[0].ID != projectID {
		t.Fatalf("unexpected project tree: %+v", got[0].OrgList[0].ProjectList)
	}
	if got[1].ID != tenantTwoID || got[1].OrgList == nil || len(got[1].OrgList) != 0 {
		t.Fatalf("expected second tenant with empty org list, got %+v", got[1])
	}
}

func TestService_ListWithOrgProjects_NoTenant(t *testing.T) {
	orgCalled := false
	tenantRepo := &stubTenantRepo{
		listAccessible: func(context.Context, tenantdomain.AccessibleFilter) ([]*tenantdomain.Tenant, error) {
			return []*tenantdomain.Tenant{}, nil
		},
	}
	orgRepo := &stubOrgRepo{
		listWithProjects: func(context.Context, orgdomain.WithProjectsFilter) ([]*orgdomain.OrganizationWithProjects, error) {
			orgCalled = true
			return nil, nil
		},
	}

	got, err := NewService(tenantRepo, orgRepo, nil).ListWithOrgProjects(context.Background(), WithOrgProjectsInput{UserID: "u-1"})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("expected empty tenant list, got %+v", got)
	}
	if orgCalled {
		t.Fatal("organization query must not run without tenants")
	}
}

func TestService_ListWithOrgProjects_InvalidUserID(t *testing.T) {
	_, err := NewService(&stubTenantRepo{}, &stubOrgRepo{}, nil).ListWithOrgProjects(
		context.Background(), WithOrgProjectsInput{UserID: " "},
	)
	if !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam, got %v", err)
	}
}
