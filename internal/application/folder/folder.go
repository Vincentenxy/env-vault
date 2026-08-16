// Package folder 文件夹应用层：用例编排与 DTO。
//
// 核心约定：业务上"项目下的一个 folder"物理上展开为该项目每个环境下的各一条记录，
// 所有操作从逻辑上将这些记录视为一个整体（创建时全环境落库、删除时全环境删除）。
package folder

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	envdomain "env-vault/internal/domain/environment"
	folderdomain "env-vault/internal/domain/folder"
)

// 业务错误（应用层显式定义，handler 映射为业务错误码）
var (
	ErrInvalidParam      = errors.New("invalid param")
	ErrCodeExists        = errors.New("folder code already exists")
	ErrNotFound          = errors.New("folder not found")
	ErrInvalidType       = errors.New("invalid folder type, must be common or customer")
	ErrCommonCodeInvalid = errors.New("common top folder code must be global or groups")
	ErrParentNotAllowed  = errors.New("parent folder must be top-level groups")
	ErrNoEnvironment     = errors.New("no environment found under project")
	ErrGroupsNotFound    = errors.New("groups folder not found under environment")
)

// CreateTopInput 创建顶级文件夹入参（项目下所有环境各创建一条）
type CreateTopInput struct {
	ProjectID uuid.UUID
	Code      string
	Name      string
	Remark    string
	Type      string
}

// CreateSubInput 创建二级文件夹入参（在 groups 目录下创建，全环境展开）
type CreateSubInput struct {
	ParentFolderID uuid.UUID // 任意一个环境下 groups 的 id，据此定位项目
	Code           string
	Name           string
	Remark         string
	Type           string
}

// UpdateInput 批量更新文件夹入参（仅 name/remark，按各环境下的 id 集合）
type UpdateInput struct {
	IDList []uuid.UUID
	Name   string
	Remark string
}

// DeleteInput 删除文件夹入参（按项目 + 编码，删除所有环境下的记录）
type DeleteInput struct {
	ProjectID  uuid.UUID
	FolderCode string
}

// ListInput 文件夹列表查询入参（仅顶级目录）
type ListInput struct {
	ProjectID uuid.UUID
	Code      string
	Name      string
	PageNum   int
	PageSize  int
}

// IService 文件夹应用服务接口（handler 仅依赖接口，便于单测替换实现）
type IService interface {
	CreateTop(ctx context.Context, in CreateTopInput, operator string) ([]*folderdomain.Folder, error)
	CreateSub(ctx context.Context, in CreateSubInput, operator string) ([]*folderdomain.Folder, error)
	Update(ctx context.Context, in UpdateInput, operator string) error
	Delete(ctx context.Context, in DeleteInput, operator string) error
	GetByID(ctx context.Context, id uuid.UUID) (*folderdomain.Folder, error)
	List(ctx context.Context, in ListInput) ([]*folderdomain.Folder, int64, error)
}

// Service 文件夹应用服务实现（依赖文件夹仓储与环境仓储做跨环境编排）
type Service struct {
	repo    folderdomain.Repository
	envRepo envdomain.Repository
}

// NewService 创建文件夹应用服务
func NewService(repo folderdomain.Repository, envRepo envdomain.Repository) *Service {
	return &Service{repo: repo, envRepo: envRepo}
}

// 确保 Service 满足 IService 编译期断言
var _ IService = (*Service)(nil)

// CreateTop 创建顶级文件夹：项目下所有环境各创建一条，folder-code 项目内唯一
func (s *Service) CreateTop(ctx context.Context, in CreateTopInput, operator string) ([]*folderdomain.Folder, error) {
	if in.ProjectID == uuid.Nil || in.Code == "" || in.Name == "" {
		return nil, ErrInvalidParam
	}
	if err := validateTopType(in.Type, in.Code); err != nil {
		return nil, err
	}

	envs, err := s.projectEnvs(ctx, in.ProjectID)
	if err != nil {
		return nil, err
	}

	envIDs := make([]uuid.UUID, 0, len(envs))
	for _, e := range envs {
		envIDs = append(envIDs, e.ID)
	}

	// 项目内编码唯一校验（覆盖所有层级）
	existing, err := s.repo.GetByEnvIDsCode(ctx, envIDs, in.Code)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrCodeExists
	}

	now := time.Now()
	folders := make([]*folderdomain.Folder, 0, len(envs))
	for _, e := range envs {
		folders = append(folders, &folderdomain.Folder{
			ID:       uuid.New(),
			Code:     in.Code,
			Name:     in.Name,
			EnvID:    e.ID,
			Remark:   in.Remark,
			Type:     in.Type,
			CreateBy: operator,
			UpdateBy: operator,
			CreateAt: now,
			UpdateAt: now,
		})
	}
	if err := s.repo.CreateBatch(ctx, folders); err != nil {
		return nil, err
	}
	return folders, nil
}

// CreateSub 创建二级文件夹：入参为任意环境下 groups 的 id，定位项目后在全环境 groups 下各创建一条
func (s *Service) CreateSub(ctx context.Context, in CreateSubInput, operator string) ([]*folderdomain.Folder, error) {
	if in.ParentFolderID == uuid.Nil || in.Code == "" || in.Name == "" {
		return nil, ErrInvalidParam
	}
	// 二级目录仅支持 common 类型（customer 仅一级）
	if in.Type != folderdomain.TypeCommon {
		return nil, ErrInvalidType
	}

	// 上级必须是顶级 groups 目录
	parent, err := s.repo.GetByID(ctx, in.ParentFolderID)
	if err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, ErrParentNotAllowed
	}
	if parent.ParentFolderID != nil || parent.Type != folderdomain.TypeCommon || parent.Code != "groups" {
		return nil, ErrParentNotAllowed
	}

	// 通过上级环境定位项目，再展开全部环境
	parentEnv, err := s.envRepo.GetByID(ctx, parent.EnvID)
	if err != nil {
		return nil, err
	}
	if parentEnv == nil {
		return nil, ErrParentNotAllowed
	}
	envs, err := s.projectEnvs(ctx, parentEnv.ProjectID)
	if err != nil {
		return nil, err
	}

	// 找出每个环境下的 groups 顶级目录，并校验 groups 下编码唯一
	groupsIDs := make([]uuid.UUID, 0, len(envs))
	for _, e := range envs {
		groups, err := s.repo.GetByEnvCode(ctx, e.ID, "groups")
		if err != nil {
			return nil, err
		}
		if groups == nil {
			return nil, ErrGroupsNotFound
		}
		existing, err := s.repo.GetByParentCode(ctx, groups.ID, in.Code)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return nil, ErrCodeExists
		}
		groupsIDs = append(groupsIDs, groups.ID)
	}

	now := time.Now()
	folders := make([]*folderdomain.Folder, 0, len(envs))
	for i, e := range envs {
		parentID := groupsIDs[i]
		folders = append(folders, &folderdomain.Folder{
			ID:             uuid.New(),
			Code:           in.Code,
			Name:           in.Name,
			EnvID:          e.ID,
			ParentFolderID: &parentID,
			Remark:         in.Remark,
			Type:           in.Type,
			CreateBy:       operator,
			UpdateBy:       operator,
			CreateAt:       now,
			UpdateAt:       now,
		})
	}
	if err := s.repo.CreateBatch(ctx, folders); err != nil {
		return nil, err
	}
	return folders, nil
}

// Update 批量更新文件夹（按各环境下的 id 集合，仅 name/remark）
func (s *Service) Update(ctx context.Context, in UpdateInput, operator string) error {
	if len(in.IDList) == 0 || in.Name == "" {
		return ErrInvalidParam
	}

	affected, err := s.repo.UpdateByIDs(ctx, in.IDList, in.Name, in.Remark, operator, time.Now())
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete 删除文件夹：按项目 + 编码删除所有环境下的记录（逻辑删除）
func (s *Service) Delete(ctx context.Context, in DeleteInput, operator string) error {
	if in.ProjectID == uuid.Nil || in.FolderCode == "" {
		return ErrInvalidParam
	}

	envs, err := s.projectEnvs(ctx, in.ProjectID)
	if err != nil {
		return err
	}

	envIDs := make([]uuid.UUID, 0, len(envs))
	for _, e := range envs {
		envIDs = append(envIDs, e.ID)
	}

	// 先确认存在再删除（与其它资源删除语义一致）
	existing, err := s.repo.GetByEnvIDsCode(ctx, envIDs, in.FolderCode)
	if err != nil {
		return err
	}
	if existing == nil {
		return ErrNotFound
	}

	_, err = s.repo.DeleteByEnvIDsCode(ctx, envIDs, in.FolderCode, operator)
	return err
}

// GetByID 按 ID 查询文件夹
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (*folderdomain.Folder, error) {
	if id == uuid.Nil {
		return nil, ErrInvalidParam
	}

	f, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, ErrNotFound
	}
	return f, nil
}

// List 分页查询项目下顶级文件夹列表
func (s *Service) List(ctx context.Context, in ListInput) ([]*folderdomain.Folder, int64, error) {
	if in.ProjectID == uuid.Nil {
		return nil, 0, ErrInvalidParam
	}
	if in.PageNum <= 0 {
		in.PageNum = 1
	}
	if in.PageSize <= 0 {
		in.PageSize = 20
	}
	if in.PageSize > 200 {
		in.PageSize = 200
	}

	envs, err := s.projectEnvs(ctx, in.ProjectID)
	if err != nil {
		return nil, 0, err
	}

	envIDs := make([]uuid.UUID, 0, len(envs))
	for _, e := range envs {
		envIDs = append(envIDs, e.ID)
	}

	return s.repo.ListTopByEnvIDs(ctx, envIDs, folderdomain.ListFilter{
		Code:     in.Code,
		Name:     in.Name,
		PageNum:  in.PageNum,
		PageSize: in.PageSize,
	})
}

// projectEnvs 查询项目下全部环境，为空时返回 ErrNoEnvironment
func (s *Service) projectEnvs(ctx context.Context, projectID uuid.UUID) ([]*envdomain.Environment, error) {
	envs, err := s.envRepo.List(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if len(envs) == 0 {
		return nil, ErrNoEnvironment
	}
	return envs, nil
}

// validateTopType 校验顶级文件夹类型：type 必须合法；common 顶级目录仅支持 global / groups
func validateTopType(t, code string) error {
	switch t {
	case folderdomain.TypeCommon:
		if code != "global" && code != "groups" {
			return ErrCommonCodeInvalid
		}
	case folderdomain.TypeCustomer:
		// customer 仅一级，顶级创建合法
	default:
		return ErrInvalidType
	}
	return nil
}
