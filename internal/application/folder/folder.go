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

// UpdateInput 批量更新文件夹入参（仅 name/remark，按 group_id 全环境同步）
type UpdateInput struct {
	GroupID uuid.UUID
	Name    string
	Remark  string
}

// DeleteInput 删除文件夹入参（按 group_id，软删除全环境记录）
type DeleteInput struct {
	GroupID uuid.UUID
}

// ListInput 文件夹列表查询入参：
//   - ParentFolderID 非空：查询该 parent 下的子目录（跨环境去重）
//   - ParentFolderID 为空：按 ProjectID 查询项目下顶级目录
type ListInput struct {
	ProjectID      uuid.UUID
	ParentFolderID *uuid.UUID
	Code           string
	Name           string
	PageNum        int
	PageSize       int
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

// CreateTop 创建顶级文件夹：项目下所有环境各创建一条，全环境共享同一 group_id
// 入参必填性校验已在 handler 层完成，service 仅负责业务编排。
func (s *Service) CreateTop(ctx context.Context, in CreateTopInput, operator string) ([]*folderdomain.Folder, error) {
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

	// 业务组 ID：一次性生成，全环境共享（标识"业务上是同一个 folder"）
	groupID := uuid.New()
	now := time.Now()
	folders := make([]*folderdomain.Folder, 0, len(envs))
	for _, e := range envs {
		folders = append(folders, &folderdomain.Folder{
			ID:       uuid.New(),
			GroupID:  groupID,
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
// 子 folder 与父 folder 分属不同业务实体，子 folder 自有独立的 group_id
// 入参必填性校验已在 handler 层完成，service 仅负责业务编排。
func (s *Service) CreateSub(ctx context.Context, in CreateSubInput, operator string) ([]*folderdomain.Folder, error) {
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

	// 子 folder 的业务组 ID：一次性生成，全环境共享
	groupID := uuid.New()
	now := time.Now()
	folders := make([]*folderdomain.Folder, 0, len(envs))
	for i, e := range envs {
		parentID := groupsIDs[i]
		folders = append(folders, &folderdomain.Folder{
			ID:             uuid.New(),
			GroupID:        groupID,
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

// Update 批量更新文件夹（按 group_id 全环境同步，仅 name/remark）
func (s *Service) Update(ctx context.Context, in UpdateInput, operator string) error {
	affected, err := s.repo.UpdateByGroupID(ctx, in.GroupID, in.Name, in.Remark, operator, time.Now())
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete 删除文件夹：按 group_id 软删除全环境记录
func (s *Service) Delete(ctx context.Context, in DeleteInput, operator string) error {
	// 先确认存在再删除（与其它资源删除语义一致）
	existing, err := s.repo.GetByGroupID(ctx, in.GroupID)
	if err != nil {
		return err
	}
	if existing == nil {
		return ErrNotFound
	}

	_, err = s.repo.DeleteByGroupID(ctx, in.GroupID, operator)
	return err
}

// GetByID 按 ID 查询文件夹
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (*folderdomain.Folder, error) {
	f, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, ErrNotFound
	}
	return f, nil
}

// List 分页查询文件夹列表（按 group_id 聚合，屏蔽环境层级）
// 入参必填性与分页归一化已在 handler 层完成。
//   - in.ParentFolderID 非空：查询该 parent 下的子目录
//   - in.ParentFolderID 为空：按 in.ProjectID 查询项目下顶级目录
func (s *Service) List(ctx context.Context, in ListInput) ([]*folderdomain.Folder, int64, error) {
	var groupIDs []uuid.UUID
	if in.ParentFolderID != nil && *in.ParentFolderID != uuid.Nil {
		// 子目录查询
		gids, err := s.repo.ListSubGroupIDsByParentFolderID(ctx, *in.ParentFolderID)
		if err != nil {
			return nil, 0, err
		}
		groupIDs = gids
	} else {
		// 顶级目录查询
		envs, err := s.projectEnvs(ctx, in.ProjectID)
		if err != nil {
			return nil, 0, err
		}

		envIDs := make([]uuid.UUID, 0, len(envs))
		for _, e := range envs {
			envIDs = append(envIDs, e.ID)
		}

		gids, err := s.repo.ListTopGroupIDsByEnvIDs(ctx, envIDs)
		if err != nil {
			return nil, 0, err
		}
		groupIDs = gids
	}

	if len(groupIDs) == 0 {
		return []*folderdomain.Folder{}, 0, nil
	}

	// 按 group_id 集合分页查询（每 group_id 一条代表记录）
	return s.repo.ListByGroupIDs(ctx, groupIDs, folderdomain.ListFilter{
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
