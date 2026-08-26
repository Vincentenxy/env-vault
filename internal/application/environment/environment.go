// Package environment 环境应用层：用例编排与 DTO。
package environment

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	envdomain "env-vault/internal/domain/environment"
	folderdomain "env-vault/internal/domain/folder"
	secretdomain "env-vault/internal/domain/secret"
)

// 业务错误（应用层显式定义，handler 映射为业务错误码）
var (
	ErrInvalidParam           = errors.New("invalid param")
	ErrCodeExists             = errors.New("environment code already exists under project")
	ErrCodeDuplicated         = errors.New("duplicate environment code in request")
	ErrNotFound               = errors.New("environment not found")
	ErrCloneUnavailable       = errors.New("environment resource clone dependencies unavailable")
	ErrInvalidFolderStructure = errors.New("invalid source folder structure")
)

// 排序号步长：新增环境接在已有最大排序号之后，便于后续中间插入
const orderNoStep = 10

const initialHistoryCommitMsg = "initial version"

type transactionRunner interface {
	WithTx(ctx context.Context, fn func(context.Context) error) error
}

type folderStructureRepository interface {
	CreateBatch(ctx context.Context, folders []*folderdomain.Folder) error
	ListByEnvID(ctx context.Context, envID uuid.UUID) ([]*folderdomain.Folder, error)
}

type secretStructureRepository interface {
	CreateBatch(ctx context.Context, secrets []*secretdomain.Secret) error
	ListByFolderIDs(ctx context.Context, folderIDs []uuid.UUID) ([]*secretdomain.Secret, error)
	CreateHistoryBatch(ctx context.Context, histories []*secretdomain.History) error
}

// secretCryptor 定义复制环境时创建空 Secret 所需的最小加密能力
type secretCryptor interface {
	Encrypt(plaintext string) (string, error)
}

// CreateItemInput 批量创建时单个环境的入参
type CreateItemInput struct {
	Code        string
	Name        string
	Remark      string
	OrderNo     int
	IsCheckPerm bool // 是否进行权限校验，默认 false
}

// CreateInput 批量创建环境入参（同属一个项目）
type CreateInput struct {
	ProjectID    uuid.UUID
	Environments []CreateItemInput
}

// UpdateInput 更新环境入参
type UpdateInput struct {
	ID          uuid.UUID
	Name        string
	Remark      string
	OrderNo     int  // 排序号，<=0 时保持原值（0 无业务意义）
	IsCheckPerm bool // 是否进行权限校验
}

// ListInput 环境列表查询入参（环境数量少，不分页，仅按项目查询全部）
type ListInput struct {
	ProjectID uuid.UUID
}

// IService 环境应用服务接口（handler 仅依赖接口，便于单测替换实现）
type IService interface {
	Create(ctx context.Context, in CreateInput, operator string) ([]*envdomain.Environment, error)
	Update(ctx context.Context, in UpdateInput, operator string) (*envdomain.Environment, error)
	Delete(ctx context.Context, id uuid.UUID, operator string) error
	GetByID(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error)
	List(ctx context.Context, in ListInput) ([]*envdomain.Environment, error)
}

// Service 环境应用服务实现
type Service struct {
	repo       envdomain.Repository
	folderRepo folderStructureRepository
	secretRepo secretStructureRepository
	cipher     secretCryptor
}

// Option 环境应用服务配置项
type Option func(*Service)

// WithResourceClone 配置新增环境时复制文件夹和 Secret 所需的依赖
func WithResourceClone(folderRepo folderStructureRepository, secretRepo secretStructureRepository, cipher secretCryptor) Option {
	return func(service *Service) {
		service.folderRepo = folderRepo
		service.secretRepo = secretRepo
		service.cipher = cipher
	}
}

// NewService 创建环境应用服务
func NewService(repo envdomain.Repository, options ...Option) *Service {
	service := &Service{repo: repo}
	for _, option := range options {
		option(service)
	}
	return service
}

// 确保 Service 满足 IService 编译期断言
var _ IService = (*Service)(nil)

// Create 批量创建环境，项目已有环境时同步复制文件夹和 Secret 结构
func (s *Service) Create(ctx context.Context, in CreateInput, operator string) ([]*envdomain.Environment, error) {
	if in.ProjectID == uuid.Nil {
		return nil, ErrInvalidParam
	}
	if len(in.Environments) == 0 {
		return nil, ErrInvalidParam
	}

	// 入参数组内编码去重校验
	seen := make(map[string]bool, len(in.Environments))
	for _, item := range in.Environments {
		if item.Code == "" || item.Name == "" {
			return nil, ErrInvalidParam
		}
		if seen[item.Code] {
			return nil, ErrCodeDuplicated
		}
		seen[item.Code] = true
	}

	// 与库中已有编码冲突校验（项目内编码唯一）
	for _, item := range in.Environments {
		existing, err := s.repo.GetByProjectCode(ctx, in.ProjectID, item.Code)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return nil, ErrCodeExists
		}
	}

	existingEnvironments, err := s.repo.List(ctx, in.ProjectID)
	if err != nil {
		return nil, err
	}

	maxOrderNo := 0
	for _, environment := range existingEnvironments {
		if environment.OrderNo > maxOrderNo {
			maxOrderNo = environment.OrderNo
		}
	}

	now := time.Now()
	environments := make([]*envdomain.Environment, 0, len(in.Environments))
	nextOrderNo := maxOrderNo
	for _, item := range in.Environments {
		orderNo := item.OrderNo
		if orderNo <= 0 {
			nextOrderNo += orderNoStep
			orderNo = nextOrderNo
		} else if orderNo > nextOrderNo {
			nextOrderNo = orderNo
		}
		environments = append(environments, &envdomain.Environment{
			ID:          uuid.New(),
			Code:        item.Code,
			Name:        item.Name,
			Remark:      item.Remark,
			ProjectID:   in.ProjectID,
			OrderNo:     orderNo,
			IsCheckPerm: item.IsCheckPerm,
			CreateBy:    operator,
			UpdateBy:    operator,
			CreateAt:    now,
			UpdateAt:    now,
		})
	}

	if len(existingEnvironments) == 0 {
		if err := s.repo.CreateBatch(ctx, environments); err != nil {
			return nil, err
		}
		return environments, nil
	}

	txRunner, ok := s.repo.(transactionRunner)
	if !ok || s.folderRepo == nil || s.secretRepo == nil || s.cipher == nil {
		return nil, ErrCloneUnavailable
	}

	batchID := uuid.New()
	err = txRunner.WithTx(ctx, func(txCtx context.Context) error {
		if err := s.repo.CreateBatch(txCtx, environments); err != nil {
			return err
		}
		return s.cloneResources(txCtx, existingEnvironments[0], environments, batchID, operator, now)
	})
	if err != nil {
		return nil, err
	}
	return environments, nil
}

func (s *Service) cloneResources(
	ctx context.Context,
	sourceEnvironment *envdomain.Environment,
	targetEnvironments []*envdomain.Environment,
	batchID uuid.UUID,
	operator string,
	now time.Time,
) error {
	sourceFolders, err := s.folderRepo.ListByEnvID(ctx, sourceEnvironment.ID)
	if err != nil {
		return err
	}
	if len(sourceFolders) == 0 {
		return nil
	}

	sourceFolderIDs := make([]uuid.UUID, 0, len(sourceFolders))
	for _, folder := range sourceFolders {
		sourceFolderIDs = append(sourceFolderIDs, folder.ID)
	}

	targetFolderIDs := make(map[uuid.UUID]map[uuid.UUID]uuid.UUID, len(targetEnvironments))
	for _, environment := range targetEnvironments {
		folderIDs := make(map[uuid.UUID]uuid.UUID, len(sourceFolders))
		for _, sourceFolder := range sourceFolders {
			folderIDs[sourceFolder.ID] = uuid.New()
		}
		targetFolderIDs[environment.ID] = folderIDs
	}

	newFolders := make([]*folderdomain.Folder, 0, len(sourceFolders)*len(targetEnvironments))
	for _, environment := range targetEnvironments {
		folderIDs := targetFolderIDs[environment.ID]
		for _, sourceFolder := range sourceFolders {
			var parentFolderID *uuid.UUID
			if sourceFolder.ParentFolderID != nil {
				mappedParentID, exists := folderIDs[*sourceFolder.ParentFolderID]
				if !exists {
					return ErrInvalidFolderStructure
				}
				parentFolderID = &mappedParentID
			}
			newFolders = append(newFolders, &folderdomain.Folder{
				ID:             folderIDs[sourceFolder.ID],
				GroupID:        sourceFolder.GroupID,
				Code:           sourceFolder.Code,
				Name:           sourceFolder.Name,
				EnvID:          environment.ID,
				ParentFolderID: parentFolderID,
				Remark:         sourceFolder.Remark,
				Type:           sourceFolder.Type,
				Manager:        sourceFolder.Manager,
				CreateBy:       operator,
				UpdateBy:       operator,
				CreateAt:       now,
				UpdateAt:       now,
			})
		}
	}
	if err := s.folderRepo.CreateBatch(ctx, newFolders); err != nil {
		return err
	}

	sourceSecrets, err := s.secretRepo.ListByFolderIDs(ctx, sourceFolderIDs)
	if err != nil {
		return err
	}
	if len(sourceSecrets) == 0 {
		return nil
	}

	newSecrets := make([]*secretdomain.Secret, 0, len(sourceSecrets)*len(targetEnvironments))
	histories := make([]*secretdomain.History, 0, len(sourceSecrets)*len(targetEnvironments))
	for _, environment := range targetEnvironments {
		folderIDs := targetFolderIDs[environment.ID]
		for _, sourceSecret := range sourceSecrets {
			folderID, exists := folderIDs[sourceSecret.FolderID]
			if !exists {
				return ErrInvalidFolderStructure
			}
			valueCiphertext, err := s.cipher.Encrypt("")
			if err != nil {
				return err
			}
			secret := &secretdomain.Secret{
				ID:              uuid.New(),
				GroupID:         sourceSecret.GroupID,
				FolderID:        folderID,
				EnvCode:         environment.Code,
				Key:             sourceSecret.Key,
				ValueCiphertext: valueCiphertext,
				ValueType:       sourceSecret.ValueType,
				Remark:          sourceSecret.Remark,
				Version:         1,
				CreateBy:        operator,
				UpdateBy:        operator,
				CreateAt:        now,
				UpdateAt:        now,
			}
			newSecrets = append(newSecrets, secret)
			histories = append(histories, &secretdomain.History{
				ID:              uuid.New(),
				SecretID:        secret.ID,
				BatchID:         batchID,
				GroupID:         secret.GroupID,
				FolderID:        secret.FolderID,
				EnvCode:         secret.EnvCode,
				ValueCiphertext: secret.ValueCiphertext,
				ValueType:       secret.ValueType,
				Version:         secret.Version,
				CommitMsg:       initialHistoryCommitMsg,
				CreateBy:        operator,
				CreateAt:        now,
			})
		}
	}
	if err := s.secretRepo.CreateBatch(ctx, newSecrets); err != nil {
		return err
	}
	return s.secretRepo.CreateHistoryBatch(ctx, histories)
}

// Update 更新环境
func (s *Service) Update(ctx context.Context, in UpdateInput, operator string) (*envdomain.Environment, error) {
	if in.ID == uuid.Nil || in.Name == "" {
		return nil, ErrInvalidParam
	}

	e, err := s.repo.GetByID(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, ErrNotFound
	}

	e.Name = in.Name
	e.Remark = in.Remark
	if in.OrderNo > 0 {
		e.OrderNo = in.OrderNo
	}
	e.IsCheckPerm = in.IsCheckPerm
	e.UpdateBy = operator
	e.UpdateAt = time.Now()

	if err := s.repo.Update(ctx, e); err != nil {
		return nil, err
	}
	return e, nil
}

// Delete 软删除环境
func (s *Service) Delete(ctx context.Context, id uuid.UUID, operator string) error {
	if id == uuid.Nil {
		return ErrInvalidParam
	}

	e, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if e == nil {
		return ErrNotFound
	}

	return s.repo.Delete(ctx, id, operator)
}

// GetByID 按 ID 查询环境
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error) {
	if id == uuid.Nil {
		return nil, ErrInvalidParam
	}

	e, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, ErrNotFound
	}
	return e, nil
}

// List 查询项目下全部环境（按排序号升序，不分页）
func (s *Service) List(ctx context.Context, in ListInput) ([]*envdomain.Environment, error) {
	if in.ProjectID == uuid.Nil {
		return nil, ErrInvalidParam
	}
	return s.repo.List(ctx, in.ProjectID)
}
