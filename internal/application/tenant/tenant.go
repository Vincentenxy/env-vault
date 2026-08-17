// Package tenant 租户应用层：用例编排与 DTO。
package tenant

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"env-vault/internal/domain/tenant"
)

// 业务错误（应用层显式定义，handler 映射为业务错误码）
var (
	ErrCodeExists   = errors.New("tenant code already exists")
	ErrNotFound     = errors.New("tenant not found")
	ErrInvalidParam = errors.New("invalid param")
)

// CreateInput 创建租户入参
type CreateInput struct {
	Code   string
	Name   string
	Remark string
}

// UpdateInput 更新租户入参
type UpdateInput struct {
	ID     uuid.UUID
	Name   string
	Remark string
}

// ListInput 租户列表查询入参
type ListInput struct {
	Code     string
	Name     string
	PageNum  int
	PageSize int
}

// Service 租户应用服务
type Service struct {
	repo tenant.Repository
}

// NewService 创建租户应用服务
func NewService(repo tenant.Repository) *Service {
	return &Service{repo: repo}
}

// Create 创建租户（业务校验：编码唯一性在代码层面显式检查）
// 入参必填性校验已在 handler 层完成，service 仅负责业务编排。
func (s *Service) Create(ctx context.Context, in CreateInput, operator string) (*tenant.Tenant, error) {
	existing, err := s.repo.GetByCode(ctx, in.Code)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrCodeExists
	}

	now := time.Now()
	t := &tenant.Tenant{
		ID:       uuid.New(),
		Code:     in.Code,
		Name:     in.Name,
		Remark:   in.Remark,
		CreateBy: operator,
		UpdateBy: operator,
		CreateAt: now,
		UpdateAt: now,
	}
	if err := s.repo.Create(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

// Update 更新租户
func (s *Service) Update(ctx context.Context, in UpdateInput, operator string) (*tenant.Tenant, error) {
	t, err := s.repo.GetByID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, ErrNotFound
	}

	t.Name = in.Name
	t.Remark = in.Remark
	t.UpdateBy = operator
	t.UpdateAt = time.Now()

	if err := s.repo.Update(ctx, t); err != nil {
		return nil, err
	}
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
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (*tenant.Tenant, error) {
	t, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, ErrNotFound
	}
	return t, nil
}

// List 分页查询租户列表
func (s *Service) List(ctx context.Context, in ListInput) ([]*tenant.Tenant, int64, error) {
	if in.PageNum <= 0 {
		in.PageNum = 1
	}
	if in.PageSize <= 0 || in.PageSize > 200 {
		in.PageSize = 20
	}

	return s.repo.List(ctx, tenant.ListFilter{
		Code:     in.Code,
		Name:     in.Name,
		PageNum:  in.PageNum,
		PageSize: in.PageSize,
	})
}
