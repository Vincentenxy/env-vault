// Package secret 密钥应用层：用例编排与 DTO。
//
// 核心约定：业务上"一个 secret"物理上展开为每个环境各一条记录（value 各不相同），
// 共享同一 group_id。创建时按 folderGroupId + envId 定位各环境下 folder 的 id 落库，
// 查询/删除均按 group_id 聚合操作（与 folder_info 业务组模式一致）。
package secret

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	envdomain "env-vault/internal/domain/environment"
	folderdomain "env-vault/internal/domain/folder"
	secretdomain "env-vault/internal/domain/secret"
	"env-vault/pkg/crypto"
)

// 业务错误（应用层显式定义，handler 映射为业务错误码）
var (
	ErrInvalidParam   = errors.New("invalid param")
	ErrNotFound       = errors.New("secret not found")
	ErrFolderNotFound = errors.New("folder not found")
	ErrEnvNotFound    = errors.New("env not found under folder")
	ErrKeyExists      = errors.New("secret key already exists under folder")
	ErrDecrypt        = errors.New("decrypt secret value failed")
)

// ValueItemInput 创建时单个环境下的值入参
type ValueItemInput struct {
	EnvID uuid.UUID
	Value string // 明文，入库前加密
}

// CreateItemInput 创建时单个 secret 的入参
type CreateItemInput struct {
	FolderGroupID uuid.UUID
	Key           string
	Remark        string
	Values        []ValueItemInput
}

// CreateInput 批量创建 secrets 入参
type CreateInput struct {
	SecretList []CreateItemInput
}

// SecretValueView 单个环境下的值视图（解密后，key 为 env code，无需 envId）
type SecretValueView struct {
	FolderID uuid.UUID
	Value    string
}

// SecretView 一个 secret 的聚合视图（跨环境），查询接口统一返回结构
type SecretView struct {
	GroupID uuid.UUID
	Key     string
	Remark  string
	Values  map[string]SecretValueView // key: env code
}

// IService 密钥应用服务接口（handler 仅依赖接口，便于单测替换实现）
type IService interface {
	Create(ctx context.Context, in CreateInput, operator string) ([]*secretdomain.Secret, error)
	ListByFolder(ctx context.Context, folderGroupID uuid.UUID) ([]SecretView, error)
	GetByGroup(ctx context.Context, groupID uuid.UUID) (*SecretView, error)
	Delete(ctx context.Context, groupID uuid.UUID, operator string) error
}

// Service 密钥应用服务实现（依赖密钥/文件夹/环境仓储与加解密器）
type Service struct {
	repo       secretdomain.Repository
	folderRepo folderdomain.Repository
	envRepo    envdomain.Repository
	cipher     *crypto.Cipher
}

// NewService 创建密钥应用服务
func NewService(repo secretdomain.Repository, folderRepo folderdomain.Repository, envRepo envdomain.Repository, cipher *crypto.Cipher) *Service {
	return &Service{repo: repo, folderRepo: folderRepo, envRepo: envRepo, cipher: cipher}
}

// 确保 Service 满足 IService 编译期断言
var _ IService = (*Service)(nil)

// Create 批量创建 secrets：每个 secret 按 folderGroupId 展开全部环境实例，按 envId 定位各环境下 folder 的 id 落库
func (s *Service) Create(ctx context.Context, in CreateInput, operator string) ([]*secretdomain.Secret, error) {
	if len(in.SecretList) == 0 {
		return nil, ErrInvalidParam
	}

	now := time.Now()
	created := make([]*secretdomain.Secret, 0)
	for _, item := range in.SecretList {
		batch, err := s.createOne(ctx, item, operator, now)
		if err != nil {
			return nil, err
		}
		created = append(created, batch...)
	}
	return created, nil
}

// createOne 创建单个 secret（展开到全部环境）
func (s *Service) createOne(ctx context.Context, item CreateItemInput, operator string, now time.Time) ([]*secretdomain.Secret, error) {
	if item.FolderGroupID == uuid.Nil || item.Key == "" || len(item.Values) == 0 {
		return nil, ErrInvalidParam
	}

	// folder 业务组展开：全部环境实例
	folders, err := s.folderRepo.ListByGroupID(ctx, item.FolderGroupID)
	if err != nil {
		return nil, err
	}
	if len(folders) == 0 {
		return nil, ErrFolderNotFound
	}

	// envID -> folderID / envCode 映射（该环境下 folder 的具体 id 与 code，env.code 不可更新故可直接冗余落库）
	folderByEnv := make(map[uuid.UUID]uuid.UUID, len(folders))
	codeByEnv := make(map[uuid.UUID]string, len(folders))
	for _, f := range folders {
		folderByEnv[f.EnvID] = f.ID
		e, err := s.envRepo.GetByID(ctx, f.EnvID)
		if err != nil {
			return nil, err
		}
		if e == nil {
			return nil, ErrEnvNotFound
		}
		codeByEnv[f.EnvID] = e.Code
	}

	// 每个 value 的 envId 必须属于该 folder 组
	for _, v := range item.Values {
		if _, ok := folderByEnv[v.EnvID]; !ok {
			return nil, ErrEnvNotFound
		}
	}

	// 业务 folder 内 key 唯一校验
	folderIDs := make([]uuid.UUID, 0, len(folders))
	for _, f := range folders {
		folderIDs = append(folderIDs, f.ID)
	}
	existing, err := s.repo.GetByFolderIDsKey(ctx, folderIDs, item.Key)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrKeyExists
	}

	// 同一 secret 的所有环境实例共享一个 group_id
	groupID := uuid.New()
	secrets := make([]*secretdomain.Secret, 0, len(item.Values))
	for _, v := range item.Values {
		ciphertext, err := s.cipher.Encrypt(v.Value)
		if err != nil {
			return nil, err
		}
		secrets = append(secrets, &secretdomain.Secret{
			ID:              uuid.New(),
			GroupID:         groupID,
			FolderID:        folderByEnv[v.EnvID],
			EnvCode:         codeByEnv[v.EnvID],
			Key:             item.Key,
			ValueCiphertext: ciphertext,
			Remark:          item.Remark,
			Version:         1,
			CreateBy:        operator,
			UpdateBy:        operator,
			CreateAt:        now,
			UpdateAt:        now,
		})
	}

	if err := s.repo.CreateBatch(ctx, secrets); err != nil {
		return nil, err
	}
	return secrets, nil
}

// ListByFolder 查询1：按 folder 业务组查询其下全部 secrets（返回每个 secret 的聚合视图列表）
func (s *Service) ListByFolder(ctx context.Context, folderGroupID uuid.UUID) ([]SecretView, error) {
	if folderGroupID == uuid.Nil {
		return nil, ErrInvalidParam
	}

	folders, err := s.folderRepo.ListByGroupID(ctx, folderGroupID)
	if err != nil {
		return nil, err
	}
	if len(folders) == 0 {
		return nil, ErrFolderNotFound
	}

	folderIDs := make([]uuid.UUID, 0, len(folders))
	for _, f := range folders {
		folderIDs = append(folderIDs, f.ID)
	}

	secrets, err := s.repo.ListByFolderIDs(ctx, folderIDs)
	if err != nil {
		return nil, err
	}
	return s.buildViews(ctx, secrets)
}

// GetByGroup 查询2：按 secret 业务组查询所有环境下的值信息（聚合视图）
func (s *Service) GetByGroup(ctx context.Context, groupID uuid.UUID) (*SecretView, error) {
	if groupID == uuid.Nil {
		return nil, ErrInvalidParam
	}

	secrets, err := s.repo.ListByGroupID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if len(secrets) == 0 {
		return nil, ErrNotFound
	}

	views, err := s.buildViews(ctx, secrets)
	if err != nil {
		return nil, err
	}
	return &views[0], nil
}

// Delete 删除密钥：按 group_id 逻辑删除全部环境实例
func (s *Service) Delete(ctx context.Context, groupID uuid.UUID, operator string) error {
	if groupID == uuid.Nil {
		return ErrInvalidParam
	}

	secrets, err := s.repo.ListByGroupID(ctx, groupID)
	if err != nil {
		return err
	}
	if len(secrets) == 0 {
		return ErrNotFound
	}

	_, err = s.repo.DeleteByGroupID(ctx, groupID, operator)
	return err
}

// buildViews 将若干环境实例按 group_id 聚合为 SecretView 列表（解密 value，env code 作为 values 的 key）
// 聚合直接使用 secret 记录上的冗余列（folder_id / env_code），不再跳查 folder/env 表
func (s *Service) buildViews(ctx context.Context, secrets []*secretdomain.Secret) ([]SecretView, error) {
	byGroup := make(map[uuid.UUID]*SecretView)
	order := make([]uuid.UUID, 0) // 保持分组出现顺序

	for _, sec := range secrets {
		view, ok := byGroup[sec.GroupID]
		if !ok {
			view = &SecretView{
				GroupID: sec.GroupID,
				Key:     sec.Key,
				Remark:  sec.Remark,
				Values:  make(map[string]SecretValueView),
			}
			byGroup[sec.GroupID] = view
			order = append(order, sec.GroupID)
		}

		value, err := s.cipher.Decrypt(sec.ValueCiphertext)
		if err != nil {
			return nil, ErrDecrypt
		}

		view.Values[sec.EnvCode] = SecretValueView{
			FolderID: sec.FolderID,
			Value:    value,
		}
	}

	views := make([]SecretView, 0, len(order))
	for _, gid := range order {
		views = append(views, *byGroup[gid])
	}
	return views, nil
}
