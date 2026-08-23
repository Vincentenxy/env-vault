// Package secret 密钥领域层：领域模型、仓储接口。
package secret

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrVersionConflict = errors.New("secret version conflict")

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

// ValueUpdateItem 按 ID 更新单条 secret 的密文（version 由仓储内部 +1）
type ValueUpdateItem struct {
	ID              uuid.UUID
	ValueCiphertext string
	ExpectedVersion int
}

// ProjectFolderListFilter 按项目、文件夹编码和环境编码查询 secrets。
// Keys 为空表示返回匹配目录和环境下的全部 key；非空时按 key 精确过滤。
type ProjectFolderListFilter struct {
	ProjectID  uuid.UUID
	FolderCode string
	EnvCodes   []string
	Keys       []string
}

// History 密钥值的不可变历史版本快照
type History struct {
	ID              uuid.UUID
	SecretID        uuid.UUID
	BatchID         uuid.UUID
	GroupID         uuid.UUID
	FolderID        uuid.UUID
	EnvID           uuid.UUID // 批次查询投影字段，不写入历史表
	EnvCode         string
	Key             string // 批次查询投影字段，不写入历史表
	Remark          string // 批次查询投影字段，不写入历史表
	ValueCiphertext string
	ValueType       string
	Version         int
	CommitMsg       string
	CreateBy        string
	CreateAt        time.Time
}

// HistoryTarget 表示一个逻辑 Secret 在某个环境下的物理记录。
type HistoryTarget struct {
	EnvID    uuid.UUID
	SecretID uuid.UUID
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
	// ListByProjectFolder 按 project_id + folder code + env code 查询 secrets，可选 key 精确过滤
	ListByProjectFolder(ctx context.Context, filter ProjectFolderListFilter) ([]*Secret, error)
	// UpdateValueByIDs 按 ID 集合逐条更新 value_ciphertext 与 version（version = version + 1）、update_by、update_at
	UpdateValueByIDs(ctx context.Context, items []ValueUpdateItem, updateBy string, updateAt time.Time) error
	// UpdateRemarkByGroupID 按 group_id 全环境同步更新 remark，返回受影响行数
	UpdateRemarkByGroupID(ctx context.Context, groupID uuid.UUID, remark, updateBy string, updateAt time.Time) (int64, error)
	// CreateHistoryBatch 批量写入不可变 value 历史快照
	CreateHistoryBatch(ctx context.Context, histories []*History) error
	// ListHistoryBySecretID 按具体环境实例分页查询历史
	ListHistoryBySecretID(ctx context.Context, secretID uuid.UUID, offset, limit int) ([]*History, int64, error)
	// ListHistoryTargetsByGroupID 查询逻辑 Secret 下各环境对应的 env_id 与 secret_id
	ListHistoryTargetsByGroupID(ctx context.Context, groupID uuid.UUID) ([]HistoryTarget, error)
	// ListHistoryByBatchID 查询一次变更批次的全部历史
	ListHistoryByBatchID(ctx context.Context, batchID uuid.UUID) ([]*History, error)
	// WithTx 在事务中执行 fn：fn 收到的 ctx 透传事务句柄，内部方法须通过 ctx 拿 tx
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}
