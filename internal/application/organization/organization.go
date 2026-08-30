// Package organization 组织应用层：用例编排与 DTO。
package organization

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
)

// 业务错误（应用层显式定义，handler 映射为业务错误码）
var (
	ErrInvalidParam = errors.New("invalid param")
	ErrCodeExists   = errors.New("organization code already exists under tenant")
	ErrNotFound     = errors.New("organization not found")
)

// CreateInput 创建组织入参
type CreateInput struct {
	Code     string
	Name     string
	Remark   string
	TenantID uuid.UUID
	Manager  string
}

// UpdateInput 更新组织入参
type UpdateInput struct {
	ID      uuid.UUID
	Name    string
	Remark  string
	Manager string
}

// ListInput 组织列表查询入参
type ListInput struct {
	Code     string
	Name     string
	TenantID *uuid.UUID
	PageNum  int
	PageSize int
}

// WithProjectsInput 组织项目树查询入参。
type WithProjectsInput struct {
	UserID string
}

// IService 组织应用服务接口（handler 仅依赖接口，便于单测替换实现）
type IService interface {
	Create(ctx context.Context, in CreateInput, operator string) (*orgdomain.Organization, error)
	Update(ctx context.Context, in UpdateInput, operator string) (*orgdomain.Organization, error)
	Delete(ctx context.Context, id uuid.UUID, operator string) error
	GetByID(ctx context.Context, id uuid.UUID) (*orgdomain.Organization, error)
	List(ctx context.Context, in ListInput) ([]*orgdomain.Organization, int64, error)
	ListWithProjects(ctx context.Context, in WithProjectsInput) (*orgdomain.WithProjectsResult, error)
}

// Service 组织应用服务实现
type Service struct {
	repo          orgdomain.Repository
	nameResolver  app.NicknameResolver
	auditRecorder auditdomain.Recorder
}

func (s *Service) WithAuditRecorder(recorder auditdomain.Recorder) *Service {
	s.auditRecorder = recorder
	return s
}

// NewService 创建组织应用服务
func NewService(repo orgdomain.Repository, resolvers ...app.NicknameResolver) *Service {
	var resolver app.NicknameResolver
	if len(resolvers) > 0 {
		resolver = resolvers[0]
	}
	return &Service{repo: repo, nameResolver: resolver}
}

// 确保 Service 满足 IService 编译期断言
var _ IService = (*Service)(nil)

// Create 创建组织（业务校验：租户内编码唯一在代码层面显式检查）
// 入参必填性校验已在 handler 层完成，service 仅负责业务编排。
func (s *Service) Create(ctx context.Context, in CreateInput, operator string) (*orgdomain.Organization, error) {
	transactor, _ := s.repo.(auditapp.Transactor)
	o, err := auditapp.RunWrite(ctx, s.auditRecorder, transactor, false,
		func(writeCtx context.Context) (*orgdomain.Organization, *auditdomain.Event, error) {
			existing, err := s.repo.GetByTenantCode(writeCtx, in.TenantID, in.Code)
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
			created := &orgdomain.Organization{ID: uuid.New(), Code: in.Code, Name: in.Name, Remark: in.Remark, TenantID: in.TenantID, Manager: manager, CreateBy: operator, UpdateBy: operator, CreateAt: now, UpdateAt: now}
			if err := s.repo.Create(writeCtx, created); err != nil {
				return nil, nil, err
			}
			return created, organizationEvent("organization.create", auditdomain.ResultSuccess, created, uuid.Nil, "", in.TenantID.String(), operator, organizationChanges(nil, created), nil), nil
		},
		func(operationErr error) *auditdomain.Event {
			return organizationFailure(organizationEvent("organization.create", auditdomain.ResultFailure, nil, uuid.Nil, in.Name, in.TenantID.String(), operator, nil, nil), operationErr)
		},
	)
	if err != nil {
		return nil, err
	}
	s.setManagerName(ctx, o)
	return o, nil
}

// Update 更新组织
func (s *Service) Update(ctx context.Context, in UpdateInput, operator string) (*orgdomain.Organization, error) {
	transactor, _ := s.repo.(auditapp.Transactor)
	o, err := auditapp.RunWrite(ctx, s.auditRecorder, transactor, false,
		func(writeCtx context.Context) (*orgdomain.Organization, *auditdomain.Event, error) {
			current, err := s.repo.GetByID(writeCtx, in.ID)
			if err != nil {
				return nil, nil, err
			}
			if current == nil {
				return nil, nil, ErrNotFound
			}
			before := *current
			current.Name, current.Remark = in.Name, in.Remark
			if manager := strings.TrimSpace(in.Manager); manager != "" {
				current.Manager = manager
			}
			current.UpdateBy, current.UpdateAt = operator, time.Now()
			if err := s.repo.Update(writeCtx, current); err != nil {
				return nil, nil, err
			}
			return current, organizationEvent("organization.update", auditdomain.ResultSuccess, current, uuid.Nil, "", current.TenantID.String(), operator, organizationChanges(&before, current), nil), nil
		},
		func(operationErr error) *auditdomain.Event {
			return organizationFailure(organizationEvent("organization.update", auditdomain.ResultFailure, nil, in.ID, in.Name, "", operator, nil, nil), operationErr)
		},
	)
	if err != nil {
		return nil, err
	}
	s.setManagerName(ctx, o)
	return o, nil
}

// Delete 软删除组织
func (s *Service) Delete(ctx context.Context, id uuid.UUID, operator string) error {
	transactor, _ := s.repo.(auditapp.Transactor)
	_, err := auditapp.RunWrite(ctx, s.auditRecorder, transactor, false,
		func(writeCtx context.Context) (struct{}, *auditdomain.Event, error) {
			o, err := s.repo.GetByID(writeCtx, id)
			if err != nil {
				return struct{}{}, nil, err
			}
			if o == nil {
				return struct{}{}, nil, ErrNotFound
			}
			if err := s.repo.Delete(writeCtx, id, operator); err != nil {
				return struct{}{}, nil, err
			}
			return struct{}{}, organizationEvent("organization.delete", auditdomain.ResultSuccess, o, id, "", o.TenantID.String(), operator, nil, nil), nil
		},
		func(operationErr error) *auditdomain.Event {
			return organizationFailure(organizationEvent("organization.delete", auditdomain.ResultFailure, nil, id, "", "", operator, nil, nil), operationErr)
		},
	)
	return err
}

// GetByID 按 ID 查询组织
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (*orgdomain.Organization, error) {
	return auditapp.RunRead(ctx, s.auditRecorder,
		func(readCtx context.Context) (*orgdomain.Organization, error) {
			o, err := s.repo.GetByID(readCtx, id)
			if err != nil {
				return nil, err
			}
			if o == nil {
				return nil, ErrNotFound
			}
			s.setManagerName(readCtx, o)
			return o, nil
		},
		func(o *orgdomain.Organization, operationErr error) *auditdomain.Event {
			event := organizationEvent("organization.read", auditdomain.ResultSuccess, o, id, "", "", "", nil, nil)
			if operationErr != nil {
				return organizationFailure(event, operationErr)
			}
			return event
		},
	)
}

// List 分页查询组织列表；分页参数已由 Handler 归一化，Service 仅透传。
func (s *Service) List(ctx context.Context, in ListInput) ([]*orgdomain.Organization, int64, error) {
	type listResult struct {
		items []*orgdomain.Organization
		total int64
	}
	result, err := auditapp.RunRead(ctx, s.auditRecorder,
		func(readCtx context.Context) (listResult, error) {
			items, total, err := s.repo.List(readCtx, orgdomain.ListFilter{Code: in.Code, Name: in.Name, TenantID: in.TenantID, PageNum: in.PageNum, PageSize: in.PageSize})
			if err != nil {
				return listResult{}, err
			}
			for _, org := range items {
				s.setManagerName(readCtx, org)
			}
			return listResult{items: items, total: total}, nil
		},
		func(result listResult, operationErr error) *auditdomain.Event {
			scopeID := ""
			if in.TenantID != nil {
				scopeID = in.TenantID.String()
			}
			event := organizationEvent("organization.list", auditdomain.ResultSuccess, nil, uuid.Nil, "", scopeID, "", nil, map[string]any{"resultCount": len(result.items), "pageNum": in.PageNum, "pageSize": in.PageSize})
			if operationErr != nil {
				return organizationFailure(event, operationErr)
			}
			return event
		},
	)
	return result.items, result.total, err
}

func (s *Service) setManagerName(ctx context.Context, org *orgdomain.Organization) {
	if org != nil {
		org.ManagerName = app.ResolveNickname(ctx, s.nameResolver, org.Manager)
	}
}

// ListWithProjects 查询用户主组织下的项目及当前有效的跨组织协作项目。
func (s *Service) ListWithProjects(ctx context.Context, in WithProjectsInput) (*orgdomain.WithProjectsResult, error) {
	return auditapp.RunRead(ctx, s.auditRecorder,
		func(readCtx context.Context) (*orgdomain.WithProjectsResult, error) {
			organizations, err := s.repo.ListWithProjects(readCtx, orgdomain.WithProjectsFilter{
				UserID:              in.UserID,
				OwnOrganizationOnly: true,
			})
			if err != nil {
				return nil, err
			}
			collaborationProjects, err := s.repo.ListCollaborationProjects(readCtx, in.UserID)
			if err != nil {
				return nil, err
			}
			return &orgdomain.WithProjectsResult{
				Organizations:         organizations,
				CollaborationProjects: collaborationProjects,
			}, nil
		},
		func(result *orgdomain.WithProjectsResult, operationErr error) *auditdomain.Event {
			organizationCount, collaborationCount := 0, 0
			if result != nil {
				organizationCount = len(result.Organizations)
				collaborationCount = len(result.CollaborationProjects)
			}
			event := organizationEvent("organization.hierarchy.read", auditdomain.ResultSuccess, nil, uuid.Nil, "", "", "", nil, map[string]any{
				"resultCount":        organizationCount,
				"collaborationCount": collaborationCount,
			})
			if operationErr != nil {
				return organizationFailure(event, operationErr)
			}
			return event
		},
	)
}
