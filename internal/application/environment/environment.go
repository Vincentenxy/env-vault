// Package environment 环境应用层：用例编排与 DTO。
package environment

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	envdomain "env-vault/internal/domain/environment"
)

// 业务错误（应用层显式定义，handler 映射为业务错误码）
var (
	ErrInvalidParam   = errors.New("invalid param")
	ErrCodeExists     = errors.New("environment code already exists under project")
	ErrCodeDuplicated = errors.New("duplicate environment code in request")
	ErrNotFound       = errors.New("environment not found")
)

// 排序号基数：批量创建时按列表顺序填充 orderNo（第 1 个 10，第 2 个 20……），便于后续中间插入
const orderNoStep = 10

// CreateItemInput 批量创建时单个环境的入参
type CreateItemInput struct {
	Code        string
	Name        string
	Remark      string
	IsCheckPerm bool // 是否进行权限校验，默认 false
}

// CreateInput 批量创建环境入参（同属一个项目）
type CreateInput struct {
	ProjectID    uuid.UUID
	Environments []CreateItemInput
}

// UpdateInput 更新环境入参
type UpdateInput struct {
	ID          uuid.UUID
	Name        string
	Remark      string
	OrderNo     int  // 排序号，<=0 时保持原值（0 无业务意义）
	IsCheckPerm bool // 是否进行权限校验
}

// ListInput 环境列表查询入参（环境数量少，不分页，仅按项目查询全部）
type ListInput struct {
	ProjectID uuid.UUID
}

// IService 环境应用服务接口（handler 仅依赖接口，便于单测替换实现）
type IService interface {
	Create(ctx context.Context, in CreateInput, operator string) ([]*envdomain.Environment, error)
	Update(ctx context.Context, in UpdateInput, operator string) (*envdomain.Environment, error)
	Delete(ctx context.Context, id uuid.UUID, operator string) error
	GetByID(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error)
	List(ctx context.Context, in ListInput) ([]*envdomain.Environment, error)
}

// Service 环境应用服务实现
type Service struct {
	repo envdomain.Repository
}

// NewService 创建环境应用服务
func NewService(repo envdomain.Repository) *Service {
	return &Service{repo: repo}
}

// 确保 Service 满足 IService 编译期断言
var _ IService = (*Service)(nil)

// Create 批量创建环境（业务校验：项目内编码唯一在代码层面显式检查；orderNo 按列表顺序填充：第 1 个 10，第 2 个 20……）
func (s *Service) Create(ctx context.Context, in CreateInput, operator string) ([]*envdomain.Environment, error) {
	if in.ProjectID == uuid.Nil {
		return nil, ErrInvalidParam
	}
	if len(in.Environments) == 0 {
		return nil, ErrInvalidParam
	}

	// 入参数组内编码去重校验
	seen := make(map[string]bool, len(in.Environments))
	for _, item := range in.Environments {
		if item.Code == "" || item.Name == "" {
			return nil, ErrInvalidParam
		}
		if seen[item.Code] {
			return nil, ErrCodeDuplicated
		}
		seen[item.Code] = true
	}

	// 与库中已有编码冲突校验（项目内编码唯一）
	for _, item := range in.Environments {
		existing, err := s.repo.GetByProjectCode(ctx, in.ProjectID, item.Code)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return nil, ErrCodeExists
		}
	}

	now := time.Now()
	environments := make([]*envdomain.Environment, 0, len(in.Environments))
	for i, item := range in.Environments {
		environments = append(environments, &envdomain.Environment{
			ID:          uuid.New(),
			Code:        item.Code,
			Name:        item.Name,
			Remark:      item.Remark,
			ProjectID:   in.ProjectID,
			OrderNo:     (i + 1) * orderNoStep,
			IsCheckPerm: item.IsCheckPerm,
			CreateBy:    operator,
			UpdateBy:    operator,
			CreateAt:    now,
			UpdateAt:    now,
		})
	}
	if err := s.repo.CreateBatch(ctx, environments); err != nil {
		return nil, err
	}
	return environments, nil
}

// Update 更新环境
func (s *Service) Update(ctx context.Context, in UpdateInput, operator string) (*envdomain.Environment, error) {
	if in.ID == uuid.Nil || in.Name == "" {
		return nil, ErrInvalidParam
	}

	e, err := s.repo.GetByID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, ErrNotFound
	}

	e.Name = in.Name
	e.Remark = in.Remark
	if in.OrderNo > 0 {
		e.OrderNo = in.OrderNo
	}
	e.IsCheckPerm = in.IsCheckPerm
	e.UpdateBy = operator
	e.UpdateAt = time.Now()

	if err := s.repo.Update(ctx, e); err != nil {
		return nil, err
	}
	return e, nil
}

// Delete 软删除环境
func (s *Service) Delete(ctx context.Context, id uuid.UUID, operator string) error {
	if id == uuid.Nil {
		return ErrInvalidParam
	}

	e, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if e == nil {
		return ErrNotFound
	}

	return s.repo.Delete(ctx, id, operator)
}

// GetByID 按 ID 查询环境
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error) {
	if id == uuid.Nil {
		return nil, ErrInvalidParam
	}

	e, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, ErrNotFound
	}
	return e, nil
}

// List 查询项目下全部环境（按排序号升序，不分页）
func (s *Service) List(ctx context.Context, in ListInput) ([]*envdomain.Environment, error) {
	if in.ProjectID == uuid.Nil {
		return nil, ErrInvalidParam
	}
	return s.repo.List(ctx, in.ProjectID)
}
