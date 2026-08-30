// Package user 用户领域层：领域模型、仓储与缓存接口。
package user

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// User 用户领域模型。UserID 是外部系统用户标识，ID 是系统内部保留标识。
type User struct {
	ID           uuid.UUID
	UserID       string
	Nickname     string
	Username     string
	PasswordHash string
	Email        string
	Phone        string
	TenantID     uuid.UUID
	OrgID        uuid.UUID
	IsBlocked    bool
	IsDeleted    bool
	DeleteAt     *time.Time
	DeleteBy     string
	CreateBy     string
	UpdateBy     string
	CreateAt     time.Time
	UpdateAt     time.Time
	// ProjectRelation 仅在按 projectId 查询项目成员时返回。
	ProjectRelation *ProjectRelation
}

// ProjectMemberType 用户与当前查询项目的成员关系类型。
type ProjectMemberType string

const (
	ProjectMemberInternal ProjectMemberType = "internal"
	ProjectMemberExternal ProjectMemberType = "external"
)

// ProjectRelation 用户与当前查询项目的有效关系。
type ProjectRelation struct {
	MemberType ProjectMemberType
	ExpireAt   *time.Time
}

// AllocationType 用户归属资源类型。
type AllocationType string

const (
	AllocationTypeTenant  AllocationType = "tenant"
	AllocationTypeOrg     AllocationType = "org"
	AllocationTypeProject AllocationType = "project"
)

// AllocationOperation 用户归属变更操作。
type AllocationOperation string

const (
	AllocationOperationAdd    AllocationOperation = "add"
	AllocationOperationRemove AllocationOperation = "remove"
)

// AllocationChange 已完成资源层级解析的用户归属变更命令。
type AllocationChange struct {
	Type       AllocationType
	Operation  AllocationOperation
	ResourceID uuid.UUID
	UserIDs    []string
	TenantID   uuid.UUID
	OrgID      uuid.UUID
	ProjectID  uuid.UUID
	Operator   string
}

// ListFilter 用户列表筛选条件。上层已按 projectId > orgId > tenantId > undistributed 归一化。
type ListFilter struct {
	TenantID      uuid.UUID
	OrgID         uuid.UUID
	ProjectID     uuid.UUID
	Undistributed bool
}

// ManagementListFilter 用户管理页面分页筛选条件。
type ManagementListFilter struct {
	TenantID uuid.UUID
	Keyword  string
	PageNum  int
	PageSize int
}

// ManagementUser 用户管理列表项，附带所属租户和组织的展示名称。
type ManagementUser struct {
	User
	TenantName string
	OrgName    string
}

// ProfileProject 当前用户已分配项目的展示摘要。
type ProfileProject struct {
	ID   uuid.UUID
	Name string
}

// ProfileRelations 当前用户资料页需要实时查询的资源归属信息。
type ProfileRelations struct {
	TenantName string
	OrgName    string
	Projects   []ProfileProject
}

// Profile 当前用户基础资料及其资源归属。
type Profile struct {
	User
	TenantName string
	OrgName    string
	Projects   []ProfileProject
}

// ProfileRelationReader 查询不适合写入用户缓存的实时资源归属。
type ProfileRelationReader interface {
	GetProfileRelations(ctx context.Context, userID uuid.UUID) (*ProfileRelations, error)
}

// Repository 用户信息仓储接口。
type Repository interface {
	// UpdateByUserID 按外部用户 ID 更新已存在的用户。
	UpdateByUserID(ctx context.Context, user *User) error
	// GetByUserID 按外部用户 ID 查询未删除用户，不存在返回 nil, nil。
	GetByUserID(ctx context.Context, userID string) (*User, error)
	// GetByUsername 忽略大小写按全局登录名查询未删除用户，不存在返回 nil, nil。
	GetByUsername(ctx context.Context, username string) (*User, error)
	// UpdatePasswordHashByUsername 按全局登录名更新本地认证密码哈希。
	UpdatePasswordHashByUsername(ctx context.Context, username, passwordHash, operator string) error
	// List 查询未删除用户，只返回列表展示所需的 id/userId/nickname 字段。
	List(ctx context.Context, filter ListFilter) ([]*User, error)
	// ListManagement 分页查询用户管理列表，不返回密码哈希等认证敏感信息。
	ListManagement(ctx context.Context, filter ManagementListFilter) ([]*ManagementUser, int64, error)
	// ListAll 查询全部未删除用户，用于启动时预热缓存。
	ListAll(ctx context.Context) ([]*User, error)
	// Allocate 在单个事务内批量更新用户归属及项目成员关系。
	// 返回更新后的用户和不存在的外部用户 ID；存在缺失用户时不执行任何写入。
	Allocate(ctx context.Context, change AllocationChange) ([]*User, []string, error)
}

// ProfileCache 用户资料二级缓存接口。
type ProfileCache interface {
	Get(ctx context.Context, userID string) (*User, error)
	Set(ctx context.Context, user *User) error
	Replace(ctx context.Context, users []*User) error
}

// BlockStatusCache 用户锁定状态缓存。found=false 表示缓存未命中，需要回源数据库。
type BlockStatusCache interface {
	Get(ctx context.Context, userID string) (blocked bool, found bool, err error)
	Set(ctx context.Context, userID string, blocked bool) error
	Replace(ctx context.Context, users []*User) error
}

// NameCache 用户姓名进程内缓存接口。
type NameCache interface {
	Get(userID string) (string, bool)
	Set(userID, nickname string)
	Replace(users []*User)
}
