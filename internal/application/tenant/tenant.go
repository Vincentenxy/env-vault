// Package tenant 租户应用层：用例编排与 DTO。
package tenant

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	app "env-vault/internal/application"
	auditapp "env-vault/internal/application/audit"
	auditdomain "env-vault/internal/domain/audit"
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
	repo          tenantdomain.Repository
	orgRepo       orgdomain.Repository
	nameResolver  app.NicknameResolver
	auditRecorder auditdomain.Recorder
}

// WithAuditRecorder enables strongly consistent tenant operation auditing.
func (s *Service) WithAuditRecorder(recorder auditdomain.Recorder) *Service {
	s.auditRecorder = recorder
	return s
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
	transactor, _ := s.repo.(auditapp.Transactor)
	t, err := auditapp.RunWrite(ctx, s.auditRecorder, transactor, false,
		func(writeCtx context.Context) (*tenantdomain.Tenant, *auditdomain.Event, error) {
			existing, err := s.repo.GetByCode(writeCtx, in.Code)
			if err != nil {
				return nil, nil, err
			}
			if existing != nil {
				return nil, nil, ErrCodeExists
			}
			now := time.Now()
			manager := strings.TrimSpace(in.Manager)
			if manager == "" {
				manager = operator
			}
			created := &tenantdomain.Tenant{ID: uuid.New(), Code: in.Code, Name: in.Name, Remark: in.Remark, Manager: manager, CreateBy: operator, UpdateBy: operator, CreateAt: now, UpdateAt: now}
			if err := s.repo.Create(writeCtx, created); err != nil {
				return nil, nil, err
			}
			return created, tenantEvent("tenant.create", auditdomain.ResultSuccess, created, uuid.Nil, "", operator, tenantChanges(nil, created), nil), nil
		},
		func(operationErr error) *auditdomain.Event {
			return tenantFailure(tenantEvent("tenant.create", auditdomain.ResultFailure, nil, uuid.Nil, in.Name, operator, nil, nil), operationErr)
		},
	)
	if err != nil {
		return nil, err
	}
	s.setManagerName(ctx, t)
	return t, nil
}

// Update 更新租户
func (s *Service) Update(ctx context.Context, in UpdateInput, operator string) (*tenantdomain.Tenant, error) {
	transactor, _ := s.repo.(auditapp.Transactor)
	t, err := auditapp.RunWrite(ctx, s.auditRecorder, transactor, false,
		func(writeCtx context.Context) (*tenantdomain.Tenant, *auditdomain.Event, error) {
			current, err := s.repo.GetByID(writeCtx, in.ID)
			if err != nil {
				return nil, nil, err
			}
			if current == nil {
				return nil, nil, ErrNotFound
			}
			before := *current
			current.Name = in.Name
			current.Remark = in.Remark
			if manager := strings.TrimSpace(in.Manager); manager != "" {
				current.Manager = manager
			}
			current.UpdateBy = operator
			current.UpdateAt = time.Now()
			if err := s.repo.Update(writeCtx, current); err != nil {
				return nil, nil, err
			}
			return current, tenantEvent("tenant.update", auditdomain.ResultSuccess, current, uuid.Nil, "", operator, tenantChanges(&before, current), nil), nil
		},
		func(operationErr error) *auditdomain.Event {
			return tenantFailure(tenantEvent("tenant.update", auditdomain.ResultFailure, nil, in.ID, in.Name, operator, nil, nil), operationErr)
		},
	)
	if err != nil {
		return nil, err
	}
	s.setManagerName(ctx, t)
	return t, nil
}

// Delete 软删除租户
func (s *Service) Delete(ctx context.Context, id uuid.UUID, operator string) error {
	transactor, _ := s.repo.(auditapp.Transactor)
	_, err := auditapp.RunWrite(ctx, s.auditRecorder, transactor, false,
		func(writeCtx context.Context) (struct{}, *auditdomain.Event, error) {
			t, err := s.repo.GetByID(writeCtx, id)
			if err != nil {
				return struct{}{}, nil, err
			}
			if t == nil {
				return struct{}{}, nil, ErrNotFound
			}
			if err := s.repo.Delete(writeCtx, id, operator); err != nil {
				return struct{}{}, nil, err
			}
			return struct{}{}, tenantEvent("tenant.delete", auditdomain.ResultSuccess, t, id, "", operator, nil, nil), nil
		},
		func(operationErr error) *auditdomain.Event {
			return tenantFailure(tenantEvent("tenant.delete", auditdomain.ResultFailure, nil, id, "", operator, nil, nil), operationErr)
		},
	)
	return err
}

// GetByID 按 ID 查询租户
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (*tenantdomain.Tenant, error) {
	return auditapp.RunRead(ctx, s.auditRecorder,
		func(readCtx context.Context) (*tenantdomain.Tenant, error) {
			t, err := s.repo.GetByID(readCtx, id)
			if err != nil {
				return nil, err
			}
			if t == nil {
				return nil, ErrNotFound
			}
			s.setManagerName(readCtx, t)
			return t, nil
		},
		func(t *tenantdomain.Tenant, operationErr error) *auditdomain.Event {
			event := tenantEvent("tenant.read", auditdomain.ResultSuccess, t, id, "", "", nil, nil)
			if operationErr != nil {
				return tenantFailure(event, operationErr)
			}
			return event
		},
	)
}

// List 分页查询租户列表；分页参数已由 Handler 归一化，Service 仅透传。
func (s *Service) List(ctx context.Context, in ListInput) ([]*tenantdomain.Tenant, int64, error) {
	type listResult struct {
		items []*tenantdomain.Tenant
		total int64
	}
	result, err := auditapp.RunRead(ctx, s.auditRecorder,
		func(readCtx context.Context) (listResult, error) {
			items, total, err := s.repo.List(readCtx, tenantdomain.ListFilter{Code: in.Code, Name: in.Name, PageNum: in.PageNum, PageSize: in.PageSize})
			if err != nil {
				return listResult{}, err
			}
			for _, tenant := range items {
				s.setManagerName(readCtx, tenant)
			}
			return listResult{items: items, total: total}, nil
		},
		func(result listResult, operationErr error) *auditdomain.Event {
			event := tenantEvent("tenant.list", auditdomain.ResultSuccess, nil, uuid.Nil, "", "", nil, map[string]any{"resultCount": len(result.items), "pageNum": in.PageNum, "pageSize": in.PageSize})
			if operationErr != nil {
				return tenantFailure(event, operationErr)
			}
			return event
		},
	)
	return result.items, result.total, err
}

func (s *Service) setManagerName(ctx context.Context, tenant *tenantdomain.Tenant) {
	if tenant != nil {
		tenant.ManagerName = app.ResolveNickname(ctx, s.nameResolver, tenant.Manager)
	}
}

// ListWithOrgProjects 查询租户及其组织和项目；当前返回全部，后续按用户权限过滤。
func (s *Service) ListWithOrgProjects(ctx context.Context, in WithOrgProjectsInput) ([]*tenantdomain.TenantWithOrgProjects, error) {
	return auditapp.RunRead(ctx, s.auditRecorder,
		func(readCtx context.Context) ([]*tenantdomain.TenantWithOrgProjects, error) {
			return s.listWithOrgProjects(readCtx, in)
		},
		func(result []*tenantdomain.TenantWithOrgProjects, operationErr error) *auditdomain.Event {
			event := tenantEvent("tenant.hierarchy.read", auditdomain.ResultSuccess, nil, uuid.Nil, "", in.UserID, nil, map[string]any{"resultCount": len(result)})
			if operationErr != nil {
				return tenantFailure(event, operationErr)
			}
			return event
		},
	)
}

func (s *Service) listWithOrgProjects(ctx context.Context, in WithOrgProjectsInput) ([]*tenantdomain.TenantWithOrgProjects, error) {
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
