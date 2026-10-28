package secret

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	envdomain "env-vault/internal/domain/environment"
	folderdomain "env-vault/internal/domain/folder"
	secretdomain "env-vault/internal/domain/secret"
)

// TestReadFolderSelectorsAreScopedBeforeDecryption 验证三种入口均只读取选定环境，并保留合法空值
func TestReadFolderSelectorsAreScopedBeforeDecryption(t *testing.T) {
	projectID, folderGroupID, groupID := uuid.New(), uuid.New(), uuid.New()
	dev := &envdomain.Environment{ID: uuid.New(), ProjectID: projectID, Code: "dev"}
	prod := &envdomain.Environment{ID: uuid.New(), ProjectID: projectID, Code: "prod"}
	devFolder := &folderdomain.Folder{ID: uuid.New(), GroupID: folderGroupID, EnvID: dev.ID, Code: "global"}
	prodFolder := &folderdomain.Folder{ID: uuid.New(), GroupID: folderGroupID, EnvID: prod.ID, Code: "global"}
	for _, mode := range []string{"code", "group", "id"} {
		t.Run(mode, func(t *testing.T) {
			cipher := testCipher(t)
			emptyCiphertext, err := cipher.Encrypt("")
			if err != nil {
				t.Fatal(err)
			}
			updated := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
			queries := 0
			secretRepo := &stubSecretRepo{listByProjectFolder: func(_ context.Context, filter secretdomain.ProjectFolderListFilter) ([]*secretdomain.Secret, error) {
				queries++
				if filter.ProjectID != projectID || filter.FolderCode != "global" ||
					!reflect.DeepEqual(filter.FolderIDs, []uuid.UUID{devFolder.ID}) ||
					!reflect.DeepEqual(filter.EnvCodes, []string{"dev"}) ||
					!reflect.DeepEqual(filter.Keys, []string{"DB_%密码"}) {
					t.Fatalf("query was not scoped before decryption: %+v", filter)
				}
				return []*secretdomain.Secret{{ID: uuid.New(), GroupID: groupID, FolderID: devFolder.ID,
					EnvCode: "dev", Key: "DB_%密码", ValueCiphertext: emptyCiphertext, Version: 3, UpdateAt: updated}}, nil
			}}
			folders := &stubFolderRepo{
				getByID: func(_ context.Context, id uuid.UUID) (*folderdomain.Folder, error) {
					if id == devFolder.ID {
						return devFolder, nil
					}
					return nil, nil
				},
				listByGroupID: func(context.Context, uuid.UUID) ([]*folderdomain.Folder, error) {
					// 生产放在第一条，验证不能误用目录组代表记录的环境
					return []*folderdomain.Folder{prodFolder, devFolder}, nil
				},
				findByEnvCode: func(_ context.Context, id uuid.UUID, code string) ([]*folderdomain.Folder, error) {
					if id == dev.ID && code == "global" {
						return []*folderdomain.Folder{devFolder}, nil
					}
					return nil, nil
				},
			}
			envs := &stubEnvRepo{
				getByID: func(_ context.Context, id uuid.UUID) (*envdomain.Environment, error) {
					if id == dev.ID {
						return dev, nil
					}
					return prod, nil
				},
				getByProjectCode: func(_ context.Context, id uuid.UUID, code string) (*envdomain.Environment, error) {
					if id == projectID && code == "dev" {
						return dev, nil
					}
					return nil, nil
				},
			}
			in := FolderReadInput{EnvCode: "dev", KeyList: []string{"DB_%密码", "DB_%密码", " "}}
			switch mode {
			case "code":
				in.FolderCode, in.ProjectID = "global", projectID
			case "group":
				in.FolderGroupID = folderGroupID
			case "id":
				in.FolderID, in.EnvCode = devFolder.ID, ""
			}
			result, err := NewService(secretRepo, folders, envs, cipher).ReadFolder(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			if queries != 1 || result.FolderID != devFolder.ID || result.EnvCode != "dev" || len(result.Secrets) != 1 {
				t.Fatalf("unexpected folder result: %+v", result)
			}
			value := result.Secrets[0].Values["dev"]
			if len(result.Secrets[0].Values) != 1 || value.Value != "" || value.Version != 3 || !value.UpdateAt.Equal(updated) {
				t.Fatalf("empty value or version lost: %+v", value)
			}
		})
	}
}

// TestReadFolderRejectsAmbiguousCode 不同层级存在同编码时不得随机读取其中一个目录
func TestReadFolderRejectsAmbiguousCode(t *testing.T) {
	projectID, envID := uuid.New(), uuid.New()
	svc := NewService(&stubSecretRepo{listByProjectFolder: func(context.Context, secretdomain.ProjectFolderListFilter) ([]*secretdomain.Secret, error) {
		t.Fatal("ambiguous folder reached secret query")
		return nil, nil
	}}, &stubFolderRepo{findByEnvCode: func(context.Context, uuid.UUID, string) ([]*folderdomain.Folder, error) {
		return []*folderdomain.Folder{{ID: uuid.New(), EnvID: envID}, {ID: uuid.New(), EnvID: envID}}, nil
	}}, &stubEnvRepo{getByProjectCode: func(context.Context, uuid.UUID, string) (*envdomain.Environment, error) {
		return &envdomain.Environment{ID: envID, ProjectID: projectID, Code: "dev"}, nil
	}}, testCipher(t))
	if _, err := svc.ReadFolder(context.Background(), FolderReadInput{FolderCode: "app", ProjectID: projectID, EnvCode: "dev"}); !errors.Is(err, ErrFolderCodeAmbiguous) {
		t.Fatalf("expected ambiguous folder code error, got %v", err)
	}
}

// TestReadFolderRejectsConflictsBeforeQuery 防止冲突条件、未指定环境和跨项目条件落到密钥查询
func TestReadFolderRejectsConflictsBeforeQuery(t *testing.T) {
	projectID, folderID, envID := uuid.New(), uuid.New(), uuid.New()
	folder := &folderdomain.Folder{ID: folderID, GroupID: uuid.New(), EnvID: envID, Code: "global"}
	svc := NewService(&stubSecretRepo{listByProjectFolder: func(context.Context, secretdomain.ProjectFolderListFilter) ([]*secretdomain.Secret, error) {
		t.Fatal("invalid scope reached secret query")
		return nil, nil
	}}, &stubFolderRepo{getByID: func(context.Context, uuid.UUID) (*folderdomain.Folder, error) { return folder, nil }},
		&stubEnvRepo{getByID: func(context.Context, uuid.UUID) (*envdomain.Environment, error) {
			return &envdomain.Environment{ID: envID, ProjectID: projectID, Code: "dev"}, nil
		}}, testCipher(t))
	for _, in := range []FolderReadInput{
		{},
		{FolderCode: "global", ProjectID: projectID},
		{FolderGroupID: uuid.New()},
		{FolderID: folderID, FolderCode: "global", EnvCode: "dev", ProjectID: projectID},
		{FolderID: folderID, EnvCode: "prod"},
		{FolderID: folderID, ProjectID: uuid.New()},
	} {
		if _, err := svc.ReadFolder(context.Background(), in); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("expected invalid scope for %+v, got %v", in, err)
		}
	}
}

// TestReadFolderEmptyAndMissingDistinguished 空目录成功返回空列表，不存在的目录返回明确错误
func TestReadFolderEmptyAndMissingDistinguished(t *testing.T) {
	folderID, envID, projectID := uuid.New(), uuid.New(), uuid.New()
	folders := &stubFolderRepo{getByID: func(_ context.Context, id uuid.UUID) (*folderdomain.Folder, error) {
		if id == folderID {
			return &folderdomain.Folder{ID: folderID, GroupID: uuid.New(), EnvID: envID, Code: "empty"}, nil
		}
		return nil, nil
	}}
	envs := &stubEnvRepo{getByID: func(context.Context, uuid.UUID) (*envdomain.Environment, error) {
		return &envdomain.Environment{ID: envID, ProjectID: projectID, Code: "dev"}, nil
	}}
	svc := NewService(&stubSecretRepo{}, folders, envs, testCipher(t))
	result, err := svc.ReadFolder(context.Background(), FolderReadInput{FolderID: folderID})
	if err != nil || result == nil || len(result.Secrets) != 0 {
		t.Fatalf("empty folder should succeed: %+v %v", result, err)
	}
	if _, err := svc.ReadFolder(context.Background(), FolderReadInput{FolderID: uuid.New()}); !errors.Is(err, ErrFolderNotFound) {
		t.Fatalf("missing folder should fail: %v", err)
	}
}
