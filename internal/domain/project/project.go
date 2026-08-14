// Package project 项目领域层：领域模型、仓储接口。
package project

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Project 项目领域模型。项目归属组织之下（一个组织承接多个具体项目），组织内编码唯一。
type Project struct {
	ID        uuid.UUID
	Code      string
	Name      string
	Remark    string
	OrgID     uuid.UUID
	IsDeleted bool
	DeleteAt  *time.Time
	DeleteBy  string
	CreateBy  string
	UpdateBy  string
	CreateAt  time.Time
	UpdateAt  time.Time
}

// ListFilter 项目列表查询过滤条件
type ListFilter struct {
	Code     string     // 编码模糊匹配（可选）
	Name     string     // 名称模糊匹配（可选）
	OrgID    *uuid.UUID // 限定组织（可选，nil 表示不限）
	PageNum  int
	PageSize int
}

// Repository 项目仓储接口（领域层定义，基础设施层实现）
type Repository interface {
	// Create 创建项目
	Create(ctx context.Context, p *Project) error
	// Update 更新项目（按 ID）
	Update(ctx context.Context, p *Project) error
	// Delete 软删除项目（按 ID）
	Delete(ctx context.Context, id uuid.UUID, deleteBy string) error
	// GetByID 按 ID 查询项目（不含已删除）
	GetByID(ctx context.Context, id uuid.UUID) (*Project, error)
	// GetByOrgCode 按组织 + 编码查询项目（不含已删除），不存在返回 nil, nil
	GetByOrgCode(ctx context.Context, orgID uuid.UUID, code string) (*Project, error)
	// List 分页查询项目列表（不含已删除），返回列表与总数
	List(ctx context.Context, filter ListFilter) ([]*Project, int64, error)
}
