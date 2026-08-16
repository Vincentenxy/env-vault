// Package folder 文件夹领域层：领域模型、仓储接口。
package folder

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// 目录类型
const (
	TypeCommon   = "common"   // 通用目录（顶级仅 global / groups）
	TypeCustomer = "customer" // 用户目录（仅一级）
)

// Folder 文件夹领域模型。文件夹归属环境之下，最多 2 层（parent_folder_id 为空为顶层）。
// 业务上"项目下的一个 folder"物理上展开为该项目每个环境下的各一条记录，操作时视为整体。
type Folder struct {
	ID             uuid.UUID
	Code           string
	Name           string
	EnvID          uuid.UUID
	ParentFolderID *uuid.UUID // nil 为顶层，非 nil 为二级
	Remark         string
	Type           string
	IsDeleted      bool
	DeleteAt       *time.Time
	DeleteBy       string
	CreateBy       string
	UpdateBy       string
	CreateAt       time.Time
	UpdateAt       time.Time
}

// ListFilter 文件夹列表查询过滤条件（仅顶级目录）
type ListFilter struct {
	Code     string // 编码模糊匹配（可选）
	Name     string // 名称模糊匹配（可选）
	PageNum  int
	PageSize int
}

// Repository 文件夹仓储接口（领域层定义，基础设施层实现）
type Repository interface {
	// CreateBatch 批量创建文件夹（每个环境各一条）
	CreateBatch(ctx context.Context, folders []*Folder) error
	// UpdateByIDs 按 ID 集合批量更新 name/remark（返回受影响行数）
	UpdateByIDs(ctx context.Context, ids []uuid.UUID, name, remark, updateBy string, updateAt time.Time) (int64, error)
	// DeleteByEnvIDsCode 软删除指定环境集合下指定编码的文件夹（所有层级），返回受影响行数
	DeleteByEnvIDsCode(ctx context.Context, envIDs []uuid.UUID, code, deleteBy string) (int64, error)
	// GetByID 按 ID 查询文件夹（不含已删除）
	GetByID(ctx context.Context, id uuid.UUID) (*Folder, error)
	// GetByEnvIDsCode 按环境集合 + 编码查询文件夹（不含已删除，项目内编码定位），不存在返回 nil, nil
	GetByEnvIDsCode(ctx context.Context, envIDs []uuid.UUID, code string) (*Folder, error)
	// GetByEnvCode 按环境 + 编码查询文件夹（不含已删除），不存在返回 nil, nil
	GetByEnvCode(ctx context.Context, envID uuid.UUID, code string) (*Folder, error)
	// GetByParentCode 按父文件夹 + 编码查询文件夹（不含已删除，groups 下编码定位），不存在返回 nil, nil
	GetByParentCode(ctx context.Context, parentID uuid.UUID, code string) (*Folder, error)
	// ListTopByEnvIDs 分页查询环境集合下的顶级文件夹列表（parent_folder_id 为空，不含已删除），返回列表与总数
	ListTopByEnvIDs(ctx context.Context, envIDs []uuid.UUID, filter ListFilter) ([]*Folder, int64, error)
}
