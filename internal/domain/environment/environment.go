// Package environment 环境领域层：领域模型、仓储接口。
package environment

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Environment 环境领域模型。环境归属项目之下（一个项目包含多个部署环境，如开发/测试/仿真/生产），项目内编码唯一。
type Environment struct {
	ID          uuid.UUID
	Code        string
	Name        string
	Remark      string
	ProjectID   uuid.UUID
	OrderNo     int  // 排序号（间隔预留：dev-10/test-20/sim-30/prod-40，便于后续中间插入）
	IsCheckPerm bool // 是否进行权限校验（dev/test 为 false，sim/prod 为 true）
	IsDeleted   bool
	DeleteAt    *time.Time
	DeleteBy    string
	CreateBy    string
	UpdateBy    string
	CreateAt    time.Time
	UpdateAt    time.Time
}

// Repository 环境仓储接口（领域层定义，基础设施层实现）
type Repository interface {
	// CreateBatch 批量创建环境
	CreateBatch(ctx context.Context, environments []*Environment) error
	// Update 更新环境（按 ID）
	Update(ctx context.Context, e *Environment) error
	// Delete 软删除环境（按 ID）
	Delete(ctx context.Context, id uuid.UUID, deleteBy string) error
	// GetByID 按 ID 查询环境（不含已删除）
	GetByID(ctx context.Context, id uuid.UUID) (*Environment, error)
	// GetByProjectCode 按项目 + 编码查询环境（不含已删除），不存在返回 nil, nil
	GetByProjectCode(ctx context.Context, projectID uuid.UUID, code string) (*Environment, error)
	// List 查询项目下全部环境（不含已删除，按排序号升序），环境数量少，不分页
	List(ctx context.Context, projectID uuid.UUID) ([]*Environment, error)
}
