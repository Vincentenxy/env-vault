// Package secret 密钥领域层：领域模型、仓储接口。
package secret

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Secret 密钥领域模型。密钥归属文件夹之下，表示一个 key=value 键值对。
// 业务上"一个 secret"物理上展开为该项目每个环境下的各一条记录（value 各不相同），
// 共享同一 group_id（与 folder_info 的业务组模式一致）。
type Secret struct {
	ID              uuid.UUID
	GroupID         uuid.UUID // 业务组 ID：同一 secret 的所有环境实例共享（跨环境定位）
	FolderID        uuid.UUID // 所属文件夹 ID（当前环境下 folder_info 的 id）
	EnvCode         string    // 冗余：所属环境的 code（创建时写入，env.code 不可更新，安全冗余）
	Key             string    // 键名（同一业务 folder 内唯一）
	ValueCiphertext string    // 加密后的值（JSON：data/nonce/algorithm）
	ValueType       string    // 值类型：number/string（预留，暂不启用）
	Remark          string    // 备注
	Version         int       // 版本号，数字递增
	IsDeleted       bool
	DeleteAt        *time.Time
	DeleteBy        string
	CreateBy        string
	UpdateBy        string
	CreateAt        time.Time
	UpdateAt        time.Time
}

// Repository 密钥仓储接口（领域层定义，基础设施层实现）
type Repository interface {
	// CreateBatch 批量创建密钥（每个环境各一条）
	CreateBatch(ctx context.Context, secrets []*Secret) error
	// DeleteByGroupID 按 group_id 软删除全部环境实例，返回受影响行数
	DeleteByGroupID(ctx context.Context, groupID uuid.UUID, deleteBy string) (int64, error)
	// GetByID 按 ID 查询密钥（不含已删除）
	GetByID(ctx context.Context, id uuid.UUID) (*Secret, error)
	// GetByFolderIDsKey 按文件夹集合 + key 查询密钥（不含已删除，业务 folder 内 key 唯一校验），不存在返回 nil, nil
	GetByFolderIDsKey(ctx context.Context, folderIDs []uuid.UUID, key string) (*Secret, error)
	// ListByFolderIDs 查询文件夹集合下的全部密钥（不含已删除，按创建时间倒序）
	ListByFolderIDs(ctx context.Context, folderIDs []uuid.UUID) ([]*Secret, error)
	// ListByGroupID 按 group_id 查询业务组下的全部环境实例（不含已删除）
	ListByGroupID(ctx context.Context, groupID uuid.UUID) ([]*Secret, error)
}
