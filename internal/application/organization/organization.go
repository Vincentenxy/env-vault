// Package organization 组织应用层：用例编排与 DTO。
package organization

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

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
	ID     uuid.UUID
	Name   string
	Remark string
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
	ListWithProjects(ctx context.Context, in WithProjectsInput) ([]*orgdomain.OrganizationWithProjects, error)
}

// Service 组织应用服务实现
type Service struct {
	repo orgdomain.Repository
}

// NewService 创建组织应用服务
func NewService(repo orgdomain.Repository) *Service {
	return &Service{repo: repo}
}

// 确保 Service 满足 IService 编译期断言
var _ IService = (*Service)(nil)

// Create 创建组织（业务校验：租户内编码唯一在代码层面显式检查）
// 入参必填性校验已在 handler 层完成，service 仅负责业务编排。
func (s *Service) Create(ctx context.Context, in CreateInput, operator string) (*orgdomain.Organization, error) {
	existing, err := s.repo.GetByTenantCode(ctx, in.TenantID, in.Code)
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
	o := &orgdomain.Organization{
		ID:       uuid.New(),
		Code:     in.Code,
		Name:     in.Name,
		Remark:   in.Remark,
		TenantID: in.TenantID,
		Manager:  manager,
		CreateBy: operator,
		UpdateBy: operator,
		CreateAt: now,
		UpdateAt: now,
	}
	if err := s.repo.Create(ctx, o); err != nil {
		return nil, err
	}
	return o, nil
}

// Update 更新组织
func (s *Service) Update(ctx context.Context, in UpdateInput, operator string) (*orgdomain.Organization, error) {
	o, err := s.repo.GetByID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, ErrNotFound
	}

	o.Name = in.Name
	o.Remark = in.Remark
	o.UpdateBy = operator
	o.UpdateAt = time.Now()

	if err := s.repo.Update(ctx, o); err != nil {
		return nil, err
	}
	return o, nil
}

// Delete 软删除组织
func (s *Service) Delete(ctx context.Context, id uuid.UUID, operator string) error {
	o, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if o == nil {
		return ErrNotFound
	}

	return s.repo.Delete(ctx, id, operator)
}

// GetByID 按 ID 查询组织
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (*orgdomain.Organization, error) {
	o, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, ErrNotFound
	}
	return o, nil
}

// List 分页查询组织列表
func (s *Service) List(ctx context.Context, in ListInput) ([]*orgdomain.Organization, int64, error) {
	if in.PageNum <= 0 {
		in.PageNum = 1
	}
	if in.PageSize <= 0 {
		in.PageSize = 20
	}
	if in.PageSize > 200 {
		in.PageSize = 200
	}

	return s.repo.List(ctx, orgdomain.ListFilter{
		Code:     in.Code,
		Name:     in.Name,
		TenantID: in.TenantID,
		PageNum:  in.PageNum,
		PageSize: in.PageSize,
	})
}

// ListWithProjects 查询全部组织及其项目。
// 当前不执行权限过滤，但透传 UserID，后续可直接在仓储 SQL 层增加权限关联条件。
func (s *Service) ListWithProjects(ctx context.Context, in WithProjectsInput) ([]*orgdomain.OrganizationWithProjects, error) {
	return s.repo.ListWithProjects(ctx, orgdomain.WithProjectsFilter{UserID: in.UserID})
}
