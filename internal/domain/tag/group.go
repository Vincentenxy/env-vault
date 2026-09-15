package tag

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var ErrGroupNotFound = errors.New("secret group not found")
var ErrTagNotInTenant = errors.New("tag not found in secret tenant")

// Summary 是密钥查询返回的标签基础信息，不包含租户管理字段
type Summary struct {
	ID               uuid.UUID `json:"id"`
	Code             string    `json:"code"`
	Name             string    `json:"name"`
	AllowValueSearch bool      `json:"allowValueSearch"`
}

// SecretGroup 由实际资源层级解析标签归属，不接收客户端指定租户
type SecretGroup struct {
	GroupID       uuid.UUID
	FolderGroupID uuid.UUID
	TenantID      uuid.UUID
	Key           string
}

// GroupReader 批量读取标签，避免密钥列表逐条查询
type GroupReader interface {
	ListGroupTags(context.Context, []uuid.UUID) (map[uuid.UUID][]Summary, error)
}

// GroupRepository 只支持整组密钥的标签关联，写入在同一事务内完成
type GroupRepository interface {
	GroupReader
	WithTx(context.Context, func(context.Context) error) error
	GetSecretGroup(context.Context, uuid.UUID, bool) (*SecretGroup, error)
	ReplaceGroupTags(context.Context, *SecretGroup, []uuid.UUID, string) ([]Summary, []Summary, error)
}
