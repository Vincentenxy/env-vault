// Package tenant 租户领域层：领域模型、仓储接口。
package tenant

import (
	"context"
	"time"

	"github.com/google/uuid"

	orgdomain "env-vault/internal/domain/organization"
)

// Tenant 租户领域模型。租户是系统内最顶级实体（类比公司）。
type Tenant struct {
	ID          uuid.UUID
	Code        string
	Name        string
	Remark      string
	Manager     string
	ManagerName string
	OrgCount    int64
	MemberCount int64
	IsDeleted   bool
	DeleteAt    *time.Time
	DeleteBy    string
	CreateBy    string
	UpdateBy    string
	CreateAt    time.Time
	UpdateAt    time.Time
}

// ListFilter 租户列表查询过滤条件
type ListFilter struct {
	Code     string // 编码模糊匹配（可选）
	Name     string // 名称模糊匹配（可选）
	PageNum  int    // 页码，从 1 开始
	PageSize int    // 每页条数
}

// AccessibleFilter 当前用户可访问租户查询条件，UserID 预留给后续权限过滤。
type AccessibleFilter struct {
	UserID string
}

// TenantWithOrgProjects 租户及其组织、项目树。
type TenantWithOrgProjects struct {
	ID      uuid.UUID
	Name    string
	Manager string
	OrgList []*orgdomain.OrganizationWithProjects
}

// Repository 租户仓储接口（领域层定义，基础设施层实现）
type Repository interface {
	// Create 创建租户
	Create(ctx context.Context, tenant *Tenant) error
	// Update 更新租户（按 ID）
	Update(ctx context.Context, tenant *Tenant) error
	// Delete 软删除租户（按 ID）
	Delete(ctx context.Context, id uuid.UUID, deleteBy string) error
	// GetByID 按 ID 查询租户（不含已删除）
	GetByID(ctx context.Context, id uuid.UUID) (*Tenant, error)
	// GetByCode 按编码查询租户（不含已删除），不存在返回 nil, nil
	GetByCode(ctx context.Context, code string) (*Tenant, error)
	// List 分页查询租户列表（不含已删除），返回列表与总数
	List(ctx context.Context, filter ListFilter) ([]*Tenant, int64, error)
	// ListAccessible 查询当前用户可访问的租户；当前返回全部，后续扩展权限 SQL
	ListAccessible(ctx context.Context, filter AccessibleFilter) ([]*Tenant, error)
}
