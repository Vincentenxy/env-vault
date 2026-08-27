// Package folder 文件夹领域层：领域模型、仓储接口。
package folder

import (
	"context"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// 目录类型
const (
	TypeCommon   = "common"   // 通用目录（顶级仅 global / groups）
	TypeCustomer = "customer" // 用户目录（仅一级）
)

// Folder 文件夹领域模型。文件夹归属环境之下，最多 2 层（parent_folder_id 为空为顶层）。
// 业务上"项目下的一个 folder"物理上展开为该项目每个环境下的各一条记录，操作时视为整体；
// 所有环境实例共享同一 group_id，作为"业务上是同一个 folder"的显式标识。
type Folder struct {
	ID             uuid.UUID
	GroupID        uuid.UUID // 业务组 ID：跨环境共享同一 group_id 的 folder 为一组
	Code           string
	Name           string
	EnvID          uuid.UUID
	ParentFolderID *uuid.UUID // nil 为顶层，非 nil 为二级
	Remark         string
	Type           string
	Manager        string
	KeyPattern     string // Secret key 完整匹配表达式，空字符串表示关闭格式校验
	ManagerName    string
	SecretCount    int64
	FolderCount    *int64
	IsDeleted      bool
	DeleteAt       *time.Time
	DeleteBy       string
	CreateBy       string
	UpdateBy       string
	CreateAt       time.Time
	UpdateAt       time.Time
}

// ListFilter 文件夹列表查询过滤条件
type ListFilter struct {
	Code     string // 编码模糊匹配（可选）
	Name     string // 名称模糊匹配（可选）
	PageNum  int
	PageSize int
}

// Repository 文件夹仓储接口（领域层定义，基础设施层实现）
type Repository interface {
	// CreateBatch 批量创建文件夹（每个环境各一条，group_id 全环境共享）
	CreateBatch(ctx context.Context, folders []*Folder) error
	// UpdateByGroupID 按 group_id 全环境同步更新 name/remark/manager/key_pattern（返回受影响行数）
	// manager 为空时保留原值，兼容未提交管理员字段的调用方。
	// keyPattern 为 nil 时保留原值，非 nil 时允许使用空字符串关闭校验
	UpdateByGroupID(ctx context.Context, groupID uuid.UUID, name, remark, manager string, keyPattern *string, updateBy string, updateAt time.Time) (int64, error)
	// DeleteByGroupID 按 group_id 软删除全环境下的记录（所有层级），返回受影响行数
	DeleteByGroupID(ctx context.Context, groupID uuid.UUID, deleteBy string) (int64, error)
	// GetByID 按 ID 查询文件夹（不含已删除）— Detail 接口：返回某环境下的具体记录
	GetByID(ctx context.Context, id uuid.UUID) (*Folder, error)
	// GetByGroupID 按 group_id 查询一条代表记录（不含已删除），不存在返回 nil, nil
	GetByGroupID(ctx context.Context, groupID uuid.UUID) (*Folder, error)
	// ListByGroupID 按 group_id 查询业务组下的全部环境实例（不含已删除），供子资源跨环境展开
	ListByGroupID(ctx context.Context, groupID uuid.UUID) ([]*Folder, error)
	// GetByEnvIDsCode 按环境集合 + 编码查询文件夹（不含已删除，CreateTop 中校验项目内编码唯一性），不存在返回 nil, nil
	GetByEnvIDsCode(ctx context.Context, envIDs []uuid.UUID, code string) (*Folder, error)
	// GetByEnvCode 按环境 + 编码查询文件夹（不含已删除，CreateSub 中定位 groups 顶级目录），不存在返回 nil, nil
	GetByEnvCode(ctx context.Context, envID uuid.UUID, code string) (*Folder, error)
	// GetByParentCode 按父文件夹 + 编码查询文件夹（不含已删除，CreateSub 中校验 groups 下编码唯一性），不存在返回 nil, nil
	GetByParentCode(ctx context.Context, parentID uuid.UUID, code string) (*Folder, error)
	// ListTopGroupIDsByEnvIDs 列出指定环境集合下的顶级 folder 的全部 group_id（去重），用于顶级 List 接口的中间步骤
	ListTopGroupIDsByEnvIDs(ctx context.Context, envIDs []uuid.UUID) ([]uuid.UUID, error)
	// ListSubGroupIDsByParentFolderID 按 parent_folder_id 列出所有子 folder 的 group_id（去重），用于子级 List 接口的中间步骤
	ListSubGroupIDsByParentFolderID(ctx context.Context, parentFolderID uuid.UUID) ([]uuid.UUID, error)
	// ListByGroupIDs 按 group_id 集合分页查询每 group_id 一条代表记录（屏蔽环境层级），用于顶级与子级 List 接口的最终步骤
	ListByGroupIDs(ctx context.Context, groupIDs []uuid.UUID, filter ListFilter) ([]*Folder, int64, error)
}

// ValidateKeyPattern 校验 Folder 的 Secret key 表达式是否可以编译
func ValidateKeyPattern(pattern string) error {
	if pattern == "" {
		return nil
	}
	_, err := compileFullKeyPattern(pattern)
	return err
}

// MatchKeyPattern 使用 Folder 表达式完整匹配 Secret key
func MatchKeyPattern(pattern, key string) (bool, error) {
	if pattern == "" {
		return true, nil
	}
	compiled, err := compileFullKeyPattern(pattern)
	if err != nil {
		return false, err
	}
	return compiled.MatchString(key), nil
}

// compileFullKeyPattern 在用户表达式外增加文本边界，避免未写锚点时发生子串匹配
func compileFullKeyPattern(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(`\A(` + pattern + `)\z`)
}
