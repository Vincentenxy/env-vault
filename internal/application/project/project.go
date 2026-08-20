// Package project 项目应用层：用例编排与 DTO。
package project

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	envdomain "env-vault/internal/domain/environment"
	projdomain "env-vault/internal/domain/project"
)

// 业务错误（应用层显式定义，handler 映射为业务错误码）
var (
	ErrInvalidParam              = errors.New("invalid param")
	ErrCodeExists                = errors.New("project code already exists under org")
	ErrNotFound                  = errors.New("project not found")
	ErrEnvironmentCodeDuplicated = errors.New("duplicate environment code in request")
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
	Code   string
	Name   string
	Remark string
	OrgID  uuid.UUID
	// Environments 可选；缺省或为空时只创建项目，非空时与项目一并创建。
	Environments []CreateEnvironmentInput
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
	repo    projdomain.Repository
	envRepo envdomain.Repository
}

// NewService 创建项目应用服务
func NewService(repo projdomain.Repository, envRepos ...envdomain.Repository) *Service {
	var envRepo envdomain.Repository
	if len(envRepos) > 0 {
		envRepo = envRepos[0]
	}
	return &Service{repo: repo, envRepo: envRepo}
}

// 确保 Service 满足 IService 编译期断言
var _ IService = (*Service)(nil)

// Create 创建项目（业务校验：组织内编码唯一在代码层面显式检查）
// 入参必填性校验已在 handler 层完成，service 仅负责业务编排。
func (s *Service) Create(ctx context.Context, in CreateInput, operator string) (*projdomain.Project, error) {
	if in.Code == "" || in.Name == "" || in.OrgID == uuid.Nil {
		return nil, ErrInvalidParam
	}
	if err := validateCreateEnvironments(in.Environments); err != nil {
		return nil, err
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
	create := func(createCtx context.Context) error {
		if err := s.repo.Create(createCtx, p); err != nil {
			return err
		}
		if len(in.Environments) == 0 {
			return nil
		}
		if s.envRepo == nil {
			return ErrInvalidParam
		}

		now := time.Now()
		environments := make([]*envdomain.Environment, 0, len(in.Environments))
		for i, item := range in.Environments {
			environments = append(environments, &envdomain.Environment{
				ID:          uuid.New(),
				Code:        item.Code,
				Name:        item.Name,
				Remark:      item.Remark,
				ProjectID:   p.ID,
				OrderNo:     (i + 1) * 10,
				IsCheckPerm: item.IsCheckPerm,
				CreateBy:    operator,
				UpdateBy:    operator,
				CreateAt:    now,
				UpdateAt:    now,
			})
		}
		return s.envRepo.CreateBatch(createCtx, environments)
	}

	if len(in.Environments) == 0 {
		if err := create(ctx); err != nil {
			return nil, err
		}
		return p, nil
	}

	txRepo, ok := s.repo.(interface {
		WithTx(context.Context, func(context.Context) error) error
	})
	if !ok {
		return nil, ErrInvalidParam
	}
	if err := txRepo.WithTx(ctx, create); err != nil {
		return nil, err
	}
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
