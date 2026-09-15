// Package search 集中维护共享密钥的检索，不参与密钥写入或版本管理
package search

import (
	"context"
	"errors"
	"time"

	tagdomain "env-vault/internal/domain/tag"
	"env-vault/pkg/page"
	"github.com/google/uuid"
)

var (
	ErrInvalidInput = errors.New("搜索参数不合法")
	ErrShortKeyword = errors.New("短关键词搜索请先选择项目或文件夹")
	ErrEnvironment  = errors.New("同一项目请指定有效环境，跨项目或租户、组织范围不能指定环境")
	ErrScope        = errors.New("所选项目或文件夹不存在或已删除，请刷新范围")
	ErrDecrypt      = errors.New("decrypt secret value failed")
	ErrTimeout      = errors.New("搜索超时，请缩小范围或补充关键词后重试")
)

// Scope 使用资源身份表达范围，folder 对应跨环境的 folderGroupId
type Scope struct {
	Type string    `json:"scopeType"`
	ID   uuid.UUID `json:"scopeId"`
}

// Input 的分页参数已由 HTTP 入口归一化，UserID 只来自认证上下文
type Input struct {
	Scopes   []Scope
	EnvList  []string
	Keyword  string
	PageNum  int
	PageSize int
	UserID   string
}

// ResourceName 是路径中一个资源的身份和展示名称
type ResourceName struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// FolderPath 中的 ID 是逻辑目录 groupId，不是具体环境的 folderId
type FolderPath struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Code string    `json:"code"`
}

// Location 保存实际所属路径，Folders 从一级目录排列到当前目录
type Location struct {
	Tenant       ResourceName `json:"tenant"`
	Organization ResourceName `json:"organization"`
	Project      ResourceName `json:"project"`
	Folders      []FolderPath `json:"folders"`
}

// EnvironmentValue 只表示当前环境的值与版本，不混用组级更新时间
type EnvironmentValue struct {
	SecretID uuid.UUID `json:"secretId"`
	EnvID    uuid.UUID `json:"envId"`
	EnvCode  string    `json:"envCode"`
	EnvName  string    `json:"envName"`
	OrderNo  float64   `json:"orderNo"`
	Value    string    `json:"value"`
	Version  int       `json:"version"`
	UpdateAt time.Time `json:"updateAt"`
}

// Secret 是固定的检索展示项，一个逻辑密钥组只占一个分页名额
type Secret struct {
	GroupID uuid.UUID           `json:"groupId"`
	Key     string              `json:"key"`
	Remark  string              `json:"remark"`
	Scope   Location            `json:"scope"`
	TagList []tagdomain.Summary `json:"tagList"`
	Values  []EnvironmentValue  `json:"values"`
}

// Searcher 是 HTTP 入口唯一依赖的检索能力
type Searcher interface {
	Search(context.Context, Input) (page.Response[Secret], error)
}

// Decryptor 复用系统主密钥模块，不读取配置中的静态 AES 值
type Decryptor interface{ Decrypt(string) (string, error) }

// TagReader 复用已有组级标签批量读取，并通过上下文参与同一快照
type TagReader interface {
	ListGroupTags(context.Context, []uuid.UUID) (map[uuid.UUID][]tagdomain.Summary, error)
}

// storedValue 的密文仅在模块内传递，不进入 HTTP 响应结构
type storedValue struct {
	EnvironmentValue
	Ciphertext string
}

type storedGroup struct {
	Secret
	Environments []storedValue
}

type storedPage struct {
	Total  int64
	Groups []storedGroup
}

type reader interface {
	read(context.Context, Input) (storedPage, error)
}
