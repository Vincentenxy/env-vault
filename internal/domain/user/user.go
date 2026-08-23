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
	IsDeleted    bool
	DeleteAt     *time.Time
	DeleteBy     string
	CreateBy     string
	UpdateBy     string
	CreateAt     time.Time
	UpdateAt     time.Time
}

// ListFilter 用户列表筛选条件。上层已按 projectId > orgId > tenantId > undistributed 归一化。
type ListFilter struct {
	TenantID      uuid.UUID
	OrgID         uuid.UUID
	ProjectID     uuid.UUID
	Undistributed bool
}

// Repository 用户信息仓储接口。
type Repository interface {
	// UpdateByUserID 按外部用户 ID 更新已存在的用户。
	UpdateByUserID(ctx context.Context, user *User) error
	// GetByUserID 按外部用户 ID 查询未删除用户，不存在返回 nil, nil。
	GetByUserID(ctx context.Context, userID string) (*User, error)
	// GetByTenantUsername 按租户和登录名查询未删除用户，不存在返回 nil, nil。
	GetByTenantUsername(ctx context.Context, tenantID uuid.UUID, username string) (*User, error)
	// List 查询未删除用户，只返回列表展示所需的 id/userId/nickname 字段。
	List(ctx context.Context, filter ListFilter) ([]*User, error)
	// ListAll 查询全部未删除用户，用于启动时预热缓存。
	ListAll(ctx context.Context) ([]*User, error)
}

// ProfileCache 用户资料二级缓存接口。
type ProfileCache interface {
	Get(ctx context.Context, userID string) (*User, error)
	Set(ctx context.Context, user *User) error
	Replace(ctx context.Context, users []*User) error
}

// NameCache 用户姓名进程内缓存接口。
type NameCache interface {
	Get(userID string) (string, bool)
	Set(userID, nickname string)
	Replace(users []*User)
}
