// Package user 用户应用层：用户资料更新与姓名缓存查询。
package user

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	userdomain "env-vault/internal/domain/user"
	"env-vault/pkg/logger"
)

var (
	ErrInvalidParam   = errors.New("invalid param")
	ErrNotFound       = errors.New("user not found")
	ErrUsernameExists = errors.New("username already exists under tenant")
)

// UpdateInput 当前认证用户资料更新入参。
type UpdateInput struct {
	UserID   string
	Nickname string
	Username string
	Email    string
	Phone    string
	TenantID uuid.UUID
	OrgID    uuid.UUID
}

// ListInput 用户列表查询入参，筛选优先级为 projectId > orgId > tenantId > undistributed。
type ListInput struct {
	TenantID      uuid.UUID
	OrgID         uuid.UUID
	ProjectID     uuid.UUID
	Undistributed bool
}

// IService 用户应用服务接口。
type IService interface {
	Update(ctx context.Context, in UpdateInput) (*userdomain.User, error)
	List(ctx context.Context, in ListInput) ([]*userdomain.User, error)
	GetProfile(ctx context.Context, userID string) (*userdomain.User, error)
	GetNickname(ctx context.Context, userID string) (string, error)
	WarmUp(ctx context.Context) (int, error)
}

// Service 用户应用服务。
type Service struct {
	repo         userdomain.Repository
	profileCache userdomain.ProfileCache
	nameCache    userdomain.NameCache
	refreshMu    sync.Mutex
}

// NewService 创建用户应用服务。
func NewService(repo userdomain.Repository, profileCache userdomain.ProfileCache, nameCache userdomain.NameCache) *Service {
	return &Service{repo: repo, profileCache: profileCache, nameCache: nameCache}
}

var _ IService = (*Service)(nil)

// List 查询用户列表。查询只读取 id/user_id/nickname，避免敏感资料进入接口链路。
// TODO(user-list-cache): 用户新增/删除及项目绑定/解绑接口落地后，增加按筛选维度的 Redis 缓存，
// 并由这些写接口统一调用缓存失效方法，避免返回过期的用户归属或项目成员数据。
func (s *Service) List(ctx context.Context, in ListInput) ([]*userdomain.User, error) {
	filter := userdomain.ListFilter{}
	switch {
	case in.ProjectID != uuid.Nil:
		filter.ProjectID = in.ProjectID
	case in.OrgID != uuid.Nil:
		filter.OrgID = in.OrgID
	case in.TenantID != uuid.Nil:
		filter.TenantID = in.TenantID
	case in.Undistributed:
		filter.Undistributed = true
	}
	return s.repo.List(ctx, filter)
}

// GetProfile 按 JWT 中的外部用户 ID 查询用户资料，使用 Redis 缓存并在未命中时回源数据库。
func (s *Service) GetProfile(ctx context.Context, userID string) (*userdomain.User, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, ErrInvalidParam
	}

	if s.profileCache != nil {
		cached, err := s.profileCache.Get(ctx, userID)
		if err != nil {
			logger.Warn(ctx, "query user redis cache failed", zap.String("userId", userID), zap.Error(err))
		} else if cached != nil {
			if s.nameCache != nil {
				s.nameCache.Set(cached.UserID, cached.Nickname)
			}
			return cached, nil
		}
	}

	user, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrNotFound
	}

	if s.nameCache != nil {
		s.nameCache.Set(user.UserID, user.Nickname)
	}
	if s.profileCache != nil {
		if err := s.profileCache.Set(ctx, user); err != nil {
			logger.Warn(ctx, "backfill user redis cache failed", zap.String("userId", userID), zap.Error(err))
		}
	}
	return user, nil
}

// Update 按 JWT 中的外部用户 ID 更新已存在用户，并同步刷新本实例缓存和 Redis。
func (s *Service) Update(ctx context.Context, in UpdateInput) (*userdomain.User, error) {
	in.UserID = strings.TrimSpace(in.UserID)
	in.Nickname = strings.TrimSpace(in.Nickname)
	in.Username = strings.TrimSpace(in.Username)
	in.Email = strings.TrimSpace(in.Email)
	in.Phone = strings.TrimSpace(in.Phone)
	if in.UserID == "" || in.Nickname == "" || in.Username == "" || in.TenantID == uuid.Nil || in.OrgID == uuid.Nil {
		return nil, ErrInvalidParam
	}

	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	current, err := s.repo.GetByUserID(ctx, in.UserID)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, ErrNotFound
	}

	usernameOwner, err := s.repo.GetByTenantUsername(ctx, in.TenantID, in.Username)
	if err != nil {
		return nil, err
	}
	if usernameOwner != nil && usernameOwner.ID != current.ID {
		return nil, ErrUsernameExists
	}

	current.Nickname = in.Nickname
	current.Username = in.Username
	current.Email = in.Email
	current.Phone = in.Phone
	current.TenantID = in.TenantID
	current.OrgID = in.OrgID
	current.UpdateBy = in.UserID
	current.UpdateAt = time.Now()
	if err := s.repo.UpdateByUserID(ctx, current); err != nil {
		return nil, err
	}

	if s.nameCache != nil {
		s.nameCache.Set(current.UserID, current.Nickname)
	}
	if s.profileCache != nil {
		if err := s.profileCache.Set(ctx, current); err != nil {
			logger.Warn(ctx, "refresh user redis cache failed", zap.String("userId", current.UserID), zap.Error(err))
		}
	}
	return current, nil
}

// GetNickname 按内存、Redis、数据库顺序查询用户姓名，后级命中时回填前级缓存。
func (s *Service) GetNickname(ctx context.Context, userID string) (string, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "", ErrInvalidParam
	}

	if s.nameCache != nil {
		if nickname, ok := s.nameCache.Get(userID); ok {
			return nickname, nil
		}
	}

	user, err := s.GetProfile(ctx, userID)
	if err != nil {
		return "", err
	}
	return user.Nickname, nil
}

// WarmUp 从数据库加载全部有效用户，整体刷新本实例内存和 Redis。
func (s *Service) WarmUp(ctx context.Context) (int, error) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	users, err := s.repo.ListAll(ctx)
	if err != nil {
		return 0, err
	}
	if s.nameCache != nil {
		s.nameCache.Replace(users)
	}
	if s.profileCache != nil {
		if err := s.profileCache.Replace(ctx, users); err != nil {
			return len(users), err
		}
	}
	return len(users), nil
}
