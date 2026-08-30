// Package project 项目应用层：用例编排与 DTO。
package project

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	app "env-vault/internal/application"
	auditapp "env-vault/internal/application/audit"
	auditdomain "env-vault/internal/domain/audit"
	envdomain "env-vault/internal/domain/environment"
	projdomain "env-vault/internal/domain/project"
)

// 业务错误（应用层显式定义，handler 映射为业务错误码）
var (
	ErrInvalidParam                 = errors.New("invalid param")
	ErrCodeExists                   = errors.New("project code already exists under org")
	ErrNotFound                     = errors.New("project not found")
	ErrEnvironmentCodeDuplicated    = errors.New("duplicate environment code in request")
	ErrManagerNotOrganizationMember = errors.New("project manager must belong to the project organization")
)

// CreateEnvironmentInput 创建项目时的环境入参。
type CreateEnvironmentInput struct {
	Code        string
	Name        string
	Remark      string
	IsCheckPerm bool
}

// CreateInput 创建项目入参
type CreateInput struct {
	Code    string
	Name    string
	Remark  string
	OrgID   uuid.UUID
	Manager string
	// Environments 可选；缺省或为空时只创建项目，非空时与项目一并创建。
	Environments []CreateEnvironmentInput
}

// UpdateInput 更新项目入参
type UpdateInput struct {
	ID      uuid.UUID
	Name    string
	Remark  string
	Manager string
}

// ListInput 项目列表查询入参
type ListInput struct {
	Code     string
	Name     string
	OrgID    *uuid.UUID
	PageNum  int
	PageSize int
}

// IService 项目应用服务接口（handler 仅依赖接口，便于单测替换实现）
type IService interface {
	Create(ctx context.Context, in CreateInput, operator string) (*projdomain.Project, error)
	Update(ctx context.Context, in UpdateInput, operator string) (*projdomain.Project, error)
	Delete(ctx context.Context, id uuid.UUID, operator string) error
	GetByID(ctx context.Context, id uuid.UUID) (*projdomain.Project, error)
	List(ctx context.Context, in ListInput) ([]*projdomain.Project, int64, error)
}

// Service 项目应用服务实现
type Service struct {
	repo                      projdomain.Repository
	envRepo                   envdomain.Repository
	nameResolver              app.NicknameResolver
	auditRecorder             auditdomain.Recorder
	managerEligibilityChecker ManagerEligibilityChecker
}

// ManagerEligibilityChecker 校验项目管理员是否为项目所属组织的内部成员。
type ManagerEligibilityChecker interface {
	IsOrganizationMember(ctx context.Context, userID string, orgID uuid.UUID) (bool, error)
}

// WithAuditRecorder enables strongly consistent project operation auditing.
func (s *Service) WithAuditRecorder(recorder auditdomain.Recorder) *Service {
	s.auditRecorder = recorder
	return s
}

// Option 配置项目应用服务的可选依赖。
type Option func(*Service)

// WithEnvironmentRepository 配置环境仓储。
func WithEnvironmentRepository(repo envdomain.Repository) Option {
	return func(service *Service) { service.envRepo = repo }
}

// WithNicknameResolver 配置用户姓名查询服务。
func WithNicknameResolver(resolver app.NicknameResolver) Option {
	return func(service *Service) { service.nameResolver = resolver }
}

// WithManagerEligibilityChecker 配置项目管理员资格校验。
func WithManagerEligibilityChecker(checker ManagerEligibilityChecker) Option {
	return func(service *Service) { service.managerEligibilityChecker = checker }
}

// NewService 创建项目应用服务
func NewService(repo projdomain.Repository, options ...Option) *Service {
	service := &Service{repo: repo}
	for _, option := range options {
		option(service)
	}
	return service
}

// 确保 Service 满足 IService 编译期断言
var _ IService = (*Service)(nil)

// Create 创建项目（业务校验：组织内编码唯一在代码层面显式检查）
// 入参必填性校验已在 handler 层完成，service 仅负责业务编排。
func (s *Service) Create(ctx context.Context, in CreateInput, operator string) (*projdomain.Project, error) {
	transactor, _ := s.repo.(auditapp.Transactor)
	p, err := auditapp.RunWrite(ctx, s.auditRecorder, transactor, len(in.Environments) > 0,
		func(writeCtx context.Context) (*projdomain.Project, *auditdomain.Event, error) {
			if in.Code == "" || in.Name == "" || in.OrgID == uuid.Nil {
				return nil, nil, ErrInvalidParam
			}
			if err := validateCreateEnvironments(in.Environments); err != nil {
				return nil, nil, err
			}
			existing, err := s.repo.GetByOrgCode(writeCtx, in.OrgID, in.Code)
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
			if err := s.validateManager(writeCtx, manager, in.OrgID); err != nil {
				return nil, nil, err
			}
			created := &projdomain.Project{ID: uuid.New(), Code: in.Code, Name: in.Name, Remark: in.Remark, OrgID: in.OrgID, Manager: manager, CreateBy: operator, UpdateBy: operator, CreateAt: now, UpdateAt: now}
			if err := s.repo.Create(writeCtx, created); err != nil {
				return nil, nil, err
			}
			if len(in.Environments) > 0 {
				if s.envRepo == nil {
					return nil, nil, ErrInvalidParam
				}
				environments := make([]*envdomain.Environment, 0, len(in.Environments))
				for i, item := range in.Environments {
					environments = append(environments, &envdomain.Environment{ID: uuid.New(), Code: item.Code, Name: item.Name, Remark: item.Remark, ProjectID: created.ID, OrderNo: (i + 1) * 10, IsCheckPerm: item.IsCheckPerm, CreateBy: operator, UpdateBy: operator, CreateAt: now, UpdateAt: now})
				}
				if err := s.envRepo.CreateBatch(writeCtx, environments); err != nil {
					return nil, nil, err
				}
			}
			return created, projectEvent("project.create", auditdomain.ResultSuccess, created, uuid.Nil, "", created.ID.String(), operator, projectChanges(nil, created), map[string]any{"environmentCount": len(in.Environments)}), nil
		},
		func(operationErr error) *auditdomain.Event {
			return projectFailure(projectEvent("project.create", auditdomain.ResultFailure, nil, uuid.Nil, in.Name, "", operator, nil, map[string]any{"environmentCount": len(in.Environments)}), operationErr)
		},
	)
	if err != nil {
		return nil, err
	}
	s.setManagerName(ctx, p)
	return p, nil
}

func validateCreateEnvironments(environments []CreateEnvironmentInput) error {
	seen := make(map[string]struct{}, len(environments))
	for _, item := range environments {
		if item.Code == "" || item.Name == "" {
			return ErrInvalidParam
		}
		if _, exists := seen[item.Code]; exists {
			return ErrEnvironmentCodeDuplicated
		}
		seen[item.Code] = struct{}{}
	}
	return nil
}

// Update 更新项目
func (s *Service) Update(ctx context.Context, in UpdateInput, operator string) (*projdomain.Project, error) {
	transactor, _ := s.repo.(auditapp.Transactor)
	p, err := auditapp.RunWrite(ctx, s.auditRecorder, transactor, false,
		func(writeCtx context.Context) (*projdomain.Project, *auditdomain.Event, error) {
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
				if err := s.validateManager(writeCtx, manager, current.OrgID); err != nil {
					return nil, nil, err
				}
				current.Manager = manager
			}
			current.UpdateBy, current.UpdateAt = operator, time.Now()
			if err := s.repo.Update(writeCtx, current); err != nil {
				return nil, nil, err
			}
			return current, projectEvent("project.update", auditdomain.ResultSuccess, current, uuid.Nil, "", current.ID.String(), operator, projectChanges(&before, current), nil), nil
		},
		func(operationErr error) *auditdomain.Event {
			return projectFailure(projectEvent("project.update", auditdomain.ResultFailure, nil, in.ID, in.Name, in.ID.String(), operator, nil, nil), operationErr)
		},
	)
	if err != nil {
		return nil, err
	}
	s.setManagerName(ctx, p)
	return p, nil
}

// Delete 软删除项目
func (s *Service) Delete(ctx context.Context, id uuid.UUID, operator string) error {
	transactor, _ := s.repo.(auditapp.Transactor)
	_, err := auditapp.RunWrite(ctx, s.auditRecorder, transactor, false,
		func(writeCtx context.Context) (struct{}, *auditdomain.Event, error) {
			p, err := s.repo.GetByID(writeCtx, id)
			if err != nil {
				return struct{}{}, nil, err
			}
			if p == nil {
				return struct{}{}, nil, ErrNotFound
			}
			if err := s.repo.Delete(writeCtx, id, operator); err != nil {
				return struct{}{}, nil, err
			}
			return struct{}{}, projectEvent("project.delete", auditdomain.ResultSuccess, p, id, "", id.String(), operator, nil, nil), nil
		},
		func(operationErr error) *auditdomain.Event {
			return projectFailure(projectEvent("project.delete", auditdomain.ResultFailure, nil, id, "", id.String(), operator, nil, nil), operationErr)
		},
	)
	return err
}

// GetByID 按 ID 查询项目
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (*projdomain.Project, error) {
	return auditapp.RunRead(ctx, s.auditRecorder,
		func(readCtx context.Context) (*projdomain.Project, error) {
			p, err := s.repo.GetByID(readCtx, id)
			if err != nil {
				return nil, err
			}
			if p == nil {
				return nil, ErrNotFound
			}
			s.setManagerName(readCtx, p)
			return p, nil
		},
		func(p *projdomain.Project, operationErr error) *auditdomain.Event {
			event := projectEvent("project.read", auditdomain.ResultSuccess, p, id, "", id.String(), "", nil, nil)
			if operationErr != nil {
				return projectFailure(event, operationErr)
			}
			return event
		},
	)
}

// List 分页查询项目列表；分页参数已由 Handler 归一化，Service 仅透传。
func (s *Service) List(ctx context.Context, in ListInput) ([]*projdomain.Project, int64, error) {
	type listResult struct {
		items []*projdomain.Project
		total int64
	}
	result, err := auditapp.RunRead(ctx, s.auditRecorder,
		func(readCtx context.Context) (listResult, error) {
			items, total, err := s.repo.List(readCtx, projdomain.ListFilter{Code: in.Code, Name: in.Name, OrgID: in.OrgID, PageNum: in.PageNum, PageSize: in.PageSize})
			if err != nil {
				return listResult{}, err
			}
			for _, project := range items {
				s.setManagerName(readCtx, project)
			}
			return listResult{items: items, total: total}, nil
		},
		func(result listResult, operationErr error) *auditdomain.Event {
			event := projectEvent("project.list", auditdomain.ResultSuccess, nil, uuid.Nil, "", "", "", nil, map[string]any{"resultCount": len(result.items), "pageNum": in.PageNum, "pageSize": in.PageSize})
			if operationErr != nil {
				return projectFailure(event, operationErr)
			}
			return event
		},
	)
	return result.items, result.total, err
}

func (s *Service) setManagerName(ctx context.Context, project *projdomain.Project) {
	if project != nil {
		project.ManagerName = app.ResolveNickname(ctx, s.nameResolver, project.Manager)
	}
}

func (s *Service) validateManager(ctx context.Context, manager string, orgID uuid.UUID) error {
	if s.managerEligibilityChecker == nil {
		return nil
	}
	eligible, err := s.managerEligibilityChecker.IsOrganizationMember(ctx, manager, orgID)
	if err != nil {
		return err
	}
	if !eligible {
		return ErrManagerNotOrganizationMember
	}
	return nil
}
