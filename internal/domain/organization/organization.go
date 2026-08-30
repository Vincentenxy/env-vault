// Package organization 组织领域层：领域模型、仓储接口。
package organization

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Organization 组织领域模型。组织归属租户之下（类比公司部门），租户内编码唯一。
type Organization struct {
	ID           uuid.UUID
	Code         string
	Name         string
	Remark       string
	TenantID     uuid.UUID
	Manager      string
	ManagerName  string
	ProjectCount int64
	MemberCount  int64
	IsDeleted    bool
	DeleteAt     *time.Time
	DeleteBy     string
	CreateBy     string
	UpdateBy     string
	CreateAt     time.Time
	UpdateAt     time.Time
}

// ListFilter 组织列表查询过滤条件
type ListFilter struct {
	Code     string     // 编码模糊匹配（可选）
	Name     string     // 名称模糊匹配（可选）
	TenantID *uuid.UUID // 限定租户（可选，nil 表示不限）
	PageNum  int
	PageSize int
}

// WithProjectsFilter 组织项目树查询过滤条件。
// OwnOrganizationOnly 用于密钥导航，仅返回用户主组织；其他管理树保持原有范围。
type WithProjectsFilter struct {
	UserID              string
	TenantIDs           []uuid.UUID
	OwnOrganizationOnly bool
}

// ProjectSummary 组织项目树中的项目摘要。
type ProjectSummary struct {
	ID      uuid.UUID
	Name    string
	Manager string
}

// OrganizationWithProjects 组织及其全部项目。
type OrganizationWithProjects struct {
	ID          uuid.UUID
	Name        string
	TenantID    uuid.UUID
	Manager     string
	ProjectList []ProjectSummary
}

// CollaborationProject 当前用户通过项目关系获得的跨组织协作项目。
// 所属组织不对调用方暴露，ExpireAt 为 nil 时表示长期有效。
type CollaborationProject struct {
	ID       uuid.UUID
	Name     string
	ExpireAt *time.Time
}

// WithProjectsResult 密钥管理项目导航所需的普通组织树与协作项目。
type WithProjectsResult struct {
	Organizations         []*OrganizationWithProjects
	CollaborationProjects []CollaborationProject
}

// Repository 组织仓储接口（领域层定义，基础设施层实现）
type Repository interface {
	// Create 创建组织
	Create(ctx context.Context, org *Organization) error
	// Update 更新组织（按 ID）
	Update(ctx context.Context, org *Organization) error
	// Delete 软删除组织（按 ID）
	Delete(ctx context.Context, id uuid.UUID, deleteBy string) error
	// GetByID 按 ID 查询组织（不含已删除）
	GetByID(ctx context.Context, id uuid.UUID) (*Organization, error)
	// GetByTenantCode 按租户 + 编码查询组织（不含已删除），不存在返回 nil, nil
	GetByTenantCode(ctx context.Context, tenantID uuid.UUID, code string) (*Organization, error)
	// List 分页查询组织列表（不含已删除），返回列表与总数
	List(ctx context.Context, filter ListFilter) ([]*Organization, int64, error)
	// ListWithProjects 查询全部组织及其项目；filter 为后续用户权限过滤预留
	ListWithProjects(ctx context.Context, filter WithProjectsFilter) ([]*OrganizationWithProjects, error)
	// ListCollaborationProjects 查询用户当前有效的跨组织协作项目，不返回所属组织信息。
	ListCollaborationProjects(ctx context.Context, userID string) ([]CollaborationProject, error)
}
