// Package project 项目应用层：用例编排与 DTO。
package project

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	projdomain "env-vault/internal/domain/project"
)

// 业务错误（应用层显式定义，handler 映射为业务错误码）
var (
	ErrInvalidParam = errors.New("invalid param")
	ErrCodeExists   = errors.New("project code already exists under org")
	ErrNotFound     = errors.New("project not found")
)

// CreateInput 创建项目入参
type CreateInput struct {
	Code   string
	Name   string
	Remark string
	OrgID  uuid.UUID
}

// UpdateInput 更新项目入参
type UpdateInput struct {
	ID     uuid.UUID
	Name   string
	Remark string
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
	repo projdomain.Repository
}

// NewService 创建项目应用服务
func NewService(repo projdomain.Repository) *Service {
	return &Service{repo: repo}
}

// 确保 Service 满足 IService 编译期断言
var _ IService = (*Service)(nil)

// Create 创建项目（业务校验：组织内编码唯一在代码层面显式检查）
func (s *Service) Create(ctx context.Context, in CreateInput, operator string) (*projdomain.Project, error) {
	if in.Code == "" || in.Name == "" || in.OrgID == uuid.Nil {
		return nil, ErrInvalidParam
	}

	existing, err := s.repo.GetByOrgCode(ctx, in.OrgID, in.Code)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrCodeExists
	}

	now := time.Now()
	p := &projdomain.Project{
		ID:       uuid.New(),
		Code:     in.Code,
		Name:     in.Name,
		Remark:   in.Remark,
		OrgID:    in.OrgID,
		CreateBy: operator,
		UpdateBy: operator,
		CreateAt: now,
		UpdateAt: now,
	}
	if err := s.repo.Create(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// Update 更新项目
func (s *Service) Update(ctx context.Context, in UpdateInput, operator string) (*projdomain.Project, error) {
	if in.ID == uuid.Nil || in.Name == "" {
		return nil, ErrInvalidParam
	}

	p, err := s.repo.GetByID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrNotFound
	}

	p.Name = in.Name
	p.Remark = in.Remark
	p.UpdateBy = operator
	p.UpdateAt = time.Now()

	if err := s.repo.Update(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// Delete 软删除项目
func (s *Service) Delete(ctx context.Context, id uuid.UUID, operator string) error {
	if id == uuid.Nil {
		return ErrInvalidParam
	}

	p, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if p == nil {
		return ErrNotFound
	}

	return s.repo.Delete(ctx, id, operator)
}

// GetByID 按 ID 查询项目
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (*projdomain.Project, error) {
	if id == uuid.Nil {
		return nil, ErrInvalidParam
	}

	p, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrNotFound
	}
	return p, nil
}

// List 分页查询项目列表
func (s *Service) List(ctx context.Context, in ListInput) ([]*projdomain.Project, int64, error) {
	if in.PageNum <= 0 {
		in.PageNum = 1
	}
	if in.PageSize <= 0 {
		in.PageSize = 20
	}
	if in.PageSize > 200 {
		in.PageSize = 200
	}

	return s.repo.List(ctx, projdomain.ListFilter{
		Code:     in.Code,
		Name:     in.Name,
		OrgID:    in.OrgID,
		PageNum:  in.PageNum,
		PageSize: in.PageSize,
	})
}
