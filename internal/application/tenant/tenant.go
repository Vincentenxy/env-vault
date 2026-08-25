// Package tenant 租户应用层：用例编排与 DTO。
package tenant

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	app "env-vault/internal/application"
	orgdomain "env-vault/internal/domain/organization"
	tenantdomain "env-vault/internal/domain/tenant"
)

// 业务错误（应用层显式定义，handler 映射为业务错误码）
var (
	ErrCodeExists   = errors.New("tenant code already exists")
	ErrNotFound     = errors.New("tenant not found")
	ErrInvalidParam = errors.New("invalid param")
)

// CreateInput 创建租户入参
type CreateInput struct {
	Code    string
	Name    string
	Remark  string
	Manager string
}

// UpdateInput 更新租户入参
type UpdateInput struct {
	ID      uuid.UUID
	Name    string
	Remark  string
	Manager string
}

// ListInput 租户列表查询入参
type ListInput struct {
	Code     string
	Name     string
	PageNum  int
	PageSize int
}

// WithOrgProjectsInput 当前用户的租户组织项目树查询入参。
type WithOrgProjectsInput struct {
	UserID string
}

// IService 租户应用服务接口。
type IService interface {
	Create(ctx context.Context, in CreateInput, operator string) (*tenantdomain.Tenant, error)
	Update(ctx context.Context, in UpdateInput, operator string) (*tenantdomain.Tenant, error)
	Delete(ctx context.Context, id uuid.UUID, operator string) error
	GetByID(ctx context.Context, id uuid.UUID) (*tenantdomain.Tenant, error)
	List(ctx context.Context, in ListInput) ([]*tenantdomain.Tenant, int64, error)
	ListWithOrgProjects(ctx context.Context, in WithOrgProjectsInput) ([]*tenantdomain.TenantWithOrgProjects, error)
}

// Service 租户应用服务
type Service struct {
	repo         tenantdomain.Repository
	orgRepo      orgdomain.Repository
	nameResolver app.NicknameResolver
}

// NewService 创建租户应用服务
func NewService(repo tenantdomain.Repository, orgRepo orgdomain.Repository, resolvers ...app.NicknameResolver) *Service {
	var resolver app.NicknameResolver
	if len(resolvers) > 0 {
		resolver = resolvers[0]
	}
	return &Service{repo: repo, orgRepo: orgRepo, nameResolver: resolver}
}

var _ IService = (*Service)(nil)

// Create 创建租户（业务校验：编码唯一性在代码层面显式检查）
// 入参必填性校验已在 handler 层完成，service 仅负责业务编排。
func (s *Service) Create(ctx context.Context, in CreateInput, operator string) (*tenantdomain.Tenant, error) {
	existing, err := s.repo.GetByCode(ctx, in.Code)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrCodeExists
	}

	now := time.Now()
	manager := strings.TrimSpace(in.Manager)
	if manager == "" {
		manager = operator
	}
	t := &tenantdomain.Tenant{
		ID:       uuid.New(),
		Code:     in.Code,
		Name:     in.Name,
		Remark:   in.Remark,
		Manager:  manager,
		CreateBy: operator,
		UpdateBy: operator,
		CreateAt: now,
		UpdateAt: now,
	}
	if err := s.repo.Create(ctx, t); err != nil {
		return nil, err
	}
	s.setManagerName(ctx, t)
	return t, nil
}

// Update 更新租户
func (s *Service) Update(ctx context.Context, in UpdateInput, operator string) (*tenantdomain.Tenant, error) {
	t, err := s.repo.GetByID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, ErrNotFound
	}

	t.Name = in.Name
	t.Remark = in.Remark
	if manager := strings.TrimSpace(in.Manager); manager != "" {
		t.Manager = manager
	}
	t.UpdateBy = operator
	t.UpdateAt = time.Now()

	if err := s.repo.Update(ctx, t); err != nil {
		return nil, err
	}
	s.setManagerName(ctx, t)
	return t, nil
}

// Delete 软删除租户
func (s *Service) Delete(ctx context.Context, id uuid.UUID, operator string) error {
	t, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if t == nil {
		return ErrNotFound
	}

	return s.repo.Delete(ctx, id, operator)
}

// GetByID 按 ID 查询租户
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (*tenantdomain.Tenant, error) {
	t, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, ErrNotFound
	}
	s.setManagerName(ctx, t)
	return t, nil
}

// List 分页查询租户列表；分页参数已由 Handler 归一化，Service 仅透传。
func (s *Service) List(ctx context.Context, in ListInput) ([]*tenantdomain.Tenant, int64, error) {
	tenants, total, err := s.repo.List(ctx, tenantdomain.ListFilter{
		Code:     in.Code,
		Name:     in.Name,
		PageNum:  in.PageNum,
		PageSize: in.PageSize,
	})
	if err != nil {
		return nil, 0, err
	}
	for _, tenant := range tenants {
		s.setManagerName(ctx, tenant)
	}
	return tenants, total, nil
}

func (s *Service) setManagerName(ctx context.Context, tenant *tenantdomain.Tenant) {
	if tenant != nil {
		tenant.ManagerName = app.ResolveNickname(ctx, s.nameResolver, tenant.Manager)
	}
}

// ListWithOrgProjects 查询租户及其组织和项目；当前返回全部，后续按用户权限过滤。
func (s *Service) ListWithOrgProjects(ctx context.Context, in WithOrgProjectsInput) ([]*tenantdomain.TenantWithOrgProjects, error) {
	in.UserID = strings.TrimSpace(in.UserID)
	if in.UserID == "" {
		return nil, ErrInvalidParam
	}

	tenants, err := s.repo.ListAccessible(ctx, tenantdomain.AccessibleFilter{UserID: in.UserID})
	if err != nil {
		return nil, err
	}
	result := make([]*tenantdomain.TenantWithOrgProjects, 0, len(tenants))
	if len(tenants) == 0 {
		return result, nil
	}

	tenantIDs := make([]uuid.UUID, 0, len(tenants))
	tenantIndexes := make(map[uuid.UUID]int, len(tenants))
	for _, tenant := range tenants {
		tenantIDs = append(tenantIDs, tenant.ID)
		tenantIndexes[tenant.ID] = len(result)
		result = append(result, &tenantdomain.TenantWithOrgProjects{
			ID:      tenant.ID,
			Name:    tenant.Name,
			Manager: tenant.Manager,
			OrgList: make([]*orgdomain.OrganizationWithProjects, 0),
		})
	}

	orgs, err := s.orgRepo.ListWithProjects(ctx, orgdomain.WithProjectsFilter{
		UserID:    in.UserID,
		TenantIDs: tenantIDs,
	})
	if err != nil {
		return nil, err
	}
	for _, org := range orgs {
		if index, ok := tenantIndexes[org.TenantID]; ok {
			result[index].OrgList = append(result[index].OrgList, org)
		}
	}
	return result, nil
}
