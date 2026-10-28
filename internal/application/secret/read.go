package secret

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	envdomain "env-vault/internal/domain/environment"
	folderdomain "env-vault/internal/domain/folder"
	secretdomain "env-vault/internal/domain/secret"
)

// ErrFolderCodeAmbiguous 提示使用目录 ID 消除不同层级的重名
var ErrFolderCodeAmbiguous = errors.New("folder code is ambiguous; use folderId or folderGroupId")

// FolderReadInput 仅允许一种目录定位方式，逻辑目录和目录编码必须指定环境
type FolderReadInput struct {
	FolderID      uuid.UUID
	FolderGroupID uuid.UUID
	FolderCode    string
	ProjectID     uuid.UUID
	EnvCode       string
	KeyList       []string
}

// FolderReadResult 同时返回定位结果和密钥，便于协议层记录准确的资源审计
type FolderReadResult struct {
	FolderID      uuid.UUID
	FolderGroupID uuid.UUID
	FolderCode    string
	ProjectID     uuid.UUID
	EnvCode       string
	Secrets       []SecretView
}

// ReadFolder 为应用读取入口定位单个环境目录，复用现有仓储、解密和视图构建
// 协议层在调用完成后记录读取审计，以便带上实际的协议状态码
func (s *Service) ReadFolder(ctx context.Context, in FolderReadInput) (*FolderReadResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	in.FolderCode = strings.TrimSpace(in.FolderCode)
	in.EnvCode = strings.TrimSpace(in.EnvCode)
	in.KeyList = normalizeList(in.KeyList)
	selectors := 0
	if in.FolderID != uuid.Nil {
		selectors++
	}
	if in.FolderGroupID != uuid.Nil {
		selectors++
	}
	if in.FolderCode != "" {
		selectors++
	}
	if selectors != 1 || (in.FolderID == uuid.Nil && in.EnvCode == "") ||
		(in.FolderCode != "" && in.ProjectID == uuid.Nil) {
		return nil, ErrInvalidParam
	}

	folder, env, err := s.resolveReadFolder(ctx, in)
	if err != nil {
		return nil, err
	}
	// 同时约束实际 folder_id 和环境，在查询及解密之前排除其他环境的数据
	secrets, err := s.repo.ListByProjectFolder(ctx, secretdomain.ProjectFolderListFilter{
		ProjectID: env.ProjectID, FolderCode: folder.Code,
		FolderIDs: []uuid.UUID{folder.ID}, EnvCodes: []string{env.Code}, Keys: in.KeyList,
	})
	if err != nil {
		return nil, err
	}
	views, err := s.buildViews(ctx, secrets)
	if err != nil {
		return nil, err
	}
	return &FolderReadResult{
		FolderID: folder.ID, FolderGroupID: folder.GroupID, FolderCode: folder.Code,
		ProjectID: env.ProjectID, EnvCode: env.Code, Secrets: views,
	}, nil
}

// resolveReadFolder 将三种定位方式归一为确定的目录实例和环境
func (s *Service) resolveReadFolder(ctx context.Context, in FolderReadInput) (*folderdomain.Folder, *envdomain.Environment, error) {
	if in.FolderID != uuid.Nil {
		folder, err := s.folderRepo.GetByID(ctx, in.FolderID)
		if err != nil {
			return nil, nil, err
		}
		if folder == nil {
			return nil, nil, ErrFolderNotFound
		}
		env, err := s.envRepo.GetByID(ctx, folder.EnvID)
		if err != nil {
			return nil, nil, err
		}
		if env == nil {
			return nil, nil, ErrEnvNotFound
		}
		if (in.ProjectID != uuid.Nil && in.ProjectID != env.ProjectID) ||
			(in.EnvCode != "" && in.EnvCode != env.Code) {
			return nil, nil, ErrInvalidParam
		}
		return folder, env, nil
	}

	projectID := in.ProjectID
	var folders []*folderdomain.Folder
	if in.FolderGroupID != uuid.Nil {
		var err error
		folders, err = s.folderRepo.ListByGroupID(ctx, in.FolderGroupID)
		if err != nil {
			return nil, nil, err
		}
		if len(folders) == 0 {
			return nil, nil, ErrFolderNotFound
		}
		// 目录组的项目由数据库关系推导，不信任调用方自行指定的归属
		sourceEnv, err := s.envRepo.GetByID(ctx, folders[0].EnvID)
		if err != nil {
			return nil, nil, err
		}
		if sourceEnv == nil {
			return nil, nil, ErrEnvNotFound
		}
		if projectID != uuid.Nil && projectID != sourceEnv.ProjectID {
			return nil, nil, ErrInvalidParam
		}
		projectID = sourceEnv.ProjectID
	}
	env, err := s.envRepo.GetByProjectCode(ctx, projectID, in.EnvCode)
	if err != nil {
		return nil, nil, err
	}
	if env == nil {
		return nil, nil, ErrEnvNotFound
	}
	if in.FolderGroupID != uuid.Nil {
		for _, folder := range folders {
			if folder.EnvID == env.ID {
				return folder, env, nil
			}
		}
		return nil, nil, ErrFolderNotFound
	}
	folders, err = s.folderRepo.FindByEnvCode(ctx, env.ID, in.FolderCode)
	if err != nil {
		return nil, nil, err
	}
	if len(folders) == 0 {
		return nil, nil, ErrFolderNotFound
	}
	if len(folders) > 1 {
		return nil, nil, ErrFolderCodeAmbiguous
	}
	return folders[0], env, nil
}
