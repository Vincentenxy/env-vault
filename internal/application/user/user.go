// Package user 用户应用层：用户资料更新与姓名缓存查询。
package user

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	auditapp "env-vault/internal/application/audit"
	auditdomain "env-vault/internal/domain/audit"
	orgdomain "env-vault/internal/domain/organization"
	projectdomain "env-vault/internal/domain/project"
	tenantdomain "env-vault/internal/domain/tenant"
	userdomain "env-vault/internal/domain/user"
	"env-vault/pkg/logger"
)

var (
	ErrInvalidParam    = errors.New("invalid param")
	ErrNotFound        = errors.New("user not found")
	ErrUsernameExists  = errors.New("username already exists")
	ErrTenantNotFound  = errors.New("tenant not found")
	ErrOrgNotFound     = errors.New("organization not found")
	ErrProjectNotFound = errors.New("project not found")
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

// ManagementListInput 用户管理页面分页查询入参。
type ManagementListInput struct {
	TenantID uuid.UUID
	Keyword  string
	PageNum  int
	PageSize int
}

// AllocateInput 用户批量分配请求。
type AllocateInput struct {
	Type       string
	Operation  string
	ResourceID uuid.UUID
	UserIDs    []string
	Operator   string
}

type tenantReader interface {
	GetByID(ctx context.Context, id uuid.UUID) (*tenantdomain.Tenant, error)
}

type organizationReader interface {
	GetByID(ctx context.Context, id uuid.UUID) (*orgdomain.Organization, error)
}

type projectReader interface {
	GetByID(ctx context.Context, id uuid.UUID) (*projectdomain.Project, error)
}

// ResponsibilityChecker 为移除成员前的负责人移交校验预留。
type ResponsibilityChecker interface {
	CheckRemoval(ctx context.Context, resourceType userdomain.AllocationType, resourceID uuid.UUID, userIDs []string) error
}

// IService 用户应用服务接口。
type IService interface {
	Update(ctx context.Context, in UpdateInput) (*userdomain.User, error)
	List(ctx context.Context, in ListInput) ([]*userdomain.User, error)
	ListManagement(ctx context.Context, in ManagementListInput) ([]*userdomain.ManagementUser, int64, error)
	Allocate(ctx context.Context, in AllocateInput) (int, error)
	GetProfile(ctx context.Context, userID string) (*userdomain.User, error)
	GetProfileDetail(ctx context.Context, userID string) (*userdomain.Profile, error)
	GetNickname(ctx context.Context, userID string) (string, error)
	IsBlocked(ctx context.Context, userID string) (bool, error)
	WarmUp(ctx context.Context) (int, error)
}

// Service 用户应用服务。
type Service struct {
	repo                  userdomain.Repository
	profileCache          userdomain.ProfileCache
	nameCache             userdomain.NameCache
	blockCache            userdomain.BlockStatusCache
	tenantRepo            tenantReader
	orgRepo               organizationReader
	projectRepo           projectReader
	profileRelations      userdomain.ProfileRelationReader
	responsibilityChecker ResponsibilityChecker
	refreshMu             sync.Mutex
	auditRecorder         auditdomain.Recorder
}

// WithAuditRecorder enables resource member-allocation auditing.
func (s *Service) WithAuditRecorder(recorder auditdomain.Recorder) *Service {
	s.auditRecorder = recorder
	return s
}

// Option 用户服务可选依赖。
type Option func(*Service)

// WithBlockStatusCache 配置用户锁定状态 Redis 缓存。
func WithBlockStatusCache(cache userdomain.BlockStatusCache) Option {
	return func(s *Service) { s.blockCache = cache }
}

// WithAllocationRepositories 配置用户分配所需的资源查询仓储。
func WithAllocationRepositories(tenantRepo tenantReader, orgRepo organizationReader, projectRepo projectReader) Option {
	return func(s *Service) {
		s.tenantRepo = tenantRepo
		s.orgRepo = orgRepo
		s.projectRepo = projectRepo
	}
}

// WithResponsibilityChecker 配置移除用户前的负责人移交校验。
func WithResponsibilityChecker(checker ResponsibilityChecker) Option {
	return func(s *Service) { s.responsibilityChecker = checker }
}

// NewService 创建用户应用服务。
func NewService(repo userdomain.Repository, profileCache userdomain.ProfileCache, nameCache userdomain.NameCache, options ...Option) *Service {
	profileRelations, _ := repo.(userdomain.ProfileRelationReader)
	svc := &Service{
		repo: repo, profileCache: profileCache, nameCache: nameCache,
		profileRelations: profileRelations,
	}
	for _, option := range options {
		option(svc)
	}
	return svc
}

var _ IService = (*Service)(nil)

// List 查询用户列表。查询只读取 id/user_id/nickname，避免敏感资料进入接口链路。
// TODO(user-list-cache): 用户新增/删除及项目绑定/解绑接口落地后，增加按筛选维度的 Redis 缓存，
// 并由这些写接口统一调用缓存失效方法，避免返回过期的用户归属或项目成员数据。
func (s *Service) List(ctx context.Context, in ListInput) ([]*userdomain.User, error) {
	return auditapp.RunRead(ctx, s.auditRecorder,
		func(readCtx context.Context) ([]*userdomain.User, error) {
			return s.list(readCtx, in)
		},
		func(users []*userdomain.User, err error) *auditdomain.Event {
			result := auditdomain.ResultSuccess
			if err != nil {
				result = auditdomain.ResultFailure
			}
			return userListEvent(in, result, len(users), err)
		},
	)
}

func (s *Service) list(ctx context.Context, in ListInput) ([]*userdomain.User, error) {
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

// ListManagement 查询用户管理分页列表。分页参数已由 Handler 归一化。
func (s *Service) ListManagement(ctx context.Context, in ManagementListInput) ([]*userdomain.ManagementUser, int64, error) {
	type listResult struct {
		items []*userdomain.ManagementUser
		total int64
	}
	in.Keyword = strings.TrimSpace(in.Keyword)
	result, err := auditapp.RunRead(ctx, s.auditRecorder,
		func(readCtx context.Context) (listResult, error) {
			items, total, listErr := s.repo.ListManagement(readCtx, userdomain.ManagementListFilter{
				TenantID: in.TenantID,
				Keyword:  in.Keyword,
				PageNum:  in.PageNum,
				PageSize: in.PageSize,
			})
			return listResult{items: items, total: total}, listErr
		},
		func(result listResult, operationErr error) *auditdomain.Event {
			return userManagementListEvent(in, auditdomain.ResultSuccess, len(result.items), operationErr)
		},
	)
	if err != nil {
		return nil, 0, err
	}
	return result.items, result.total, nil
}

// Allocate 批量分配或移除用户的租户、组织、项目归属。
func (s *Service) Allocate(ctx context.Context, in AllocateInput) (int, error) {
	type allocationResult struct {
		affected int
		users    []*userdomain.User
	}
	if s.auditRecorder == nil {
		affected, users, err := s.allocate(ctx, in)
		if err == nil {
			s.refreshAllocatedUserCaches(ctx, users)
		}
		return affected, err
	}
	transactor, _ := s.repo.(auditapp.Transactor)
	result, err := auditapp.RunWrite(ctx, s.auditRecorder, transactor, true,
		func(writeCtx context.Context) (allocationResult, *auditdomain.Event, error) {
			affected, users, err := s.allocate(writeCtx, in)
			if err != nil {
				return allocationResult{}, nil, err
			}
			return allocationResult{affected: affected, users: users}, allocationEvent(in, auditdomain.ResultSuccess, affected), nil
		},
		func(operationErr error) *auditdomain.Event {
			return allocationFailure(allocationEvent(in, auditdomain.ResultFailure, 0), operationErr)
		},
	)
	if err != nil {
		return 0, err
	}
	s.refreshAllocatedUserCaches(ctx, result.users)
	return result.affected, nil
}

func (s *Service) allocate(ctx context.Context, in AllocateInput) (int, []*userdomain.User, error) {
	resourceType := userdomain.AllocationType(strings.TrimSpace(in.Type))
	operation := userdomain.AllocationOperation(strings.TrimSpace(in.Operation))
	operator := strings.TrimSpace(in.Operator)
	userIDs := normalizeUserIDs(in.UserIDs)
	if !validAllocationType(resourceType) || !validAllocationOperation(operation) ||
		in.ResourceID == uuid.Nil || len(userIDs) == 0 || operator == "" {
		return 0, nil, ErrInvalidParam
	}
	if s.tenantRepo == nil || s.orgRepo == nil || s.projectRepo == nil {
		return 0, nil, errors.New("user allocation repositories are not configured")
	}

	change := userdomain.AllocationChange{
		Type: resourceType, Operation: operation, ResourceID: in.ResourceID,
		UserIDs: userIDs, Operator: operator,
	}
	if err := s.resolveAllocationResource(ctx, &change); err != nil {
		return 0, nil, err
	}

	if operation == userdomain.AllocationOperationRemove && s.responsibilityChecker != nil {
		if err := s.responsibilityChecker.CheckRemoval(ctx, resourceType, in.ResourceID, userIDs); err != nil {
			return 0, nil, err
		}
	}

	users, missing, err := s.repo.Allocate(ctx, change)
	if err != nil {
		return 0, nil, err
	}
	if len(missing) > 0 {
		return 0, nil, fmt.Errorf("%w: %s", ErrNotFound, strings.Join(missing, ","))
	}
	return len(users), users, nil
}

func (s *Service) refreshAllocatedUserCaches(ctx context.Context, users []*userdomain.User) {
	for _, user := range users {
		if s.profileCache != nil {
			if err := s.profileCache.Set(ctx, user); err != nil {
				logger.Warn(ctx, "refresh allocated user redis cache failed", zap.String("userId", user.UserID), zap.Error(err))
			}
		}
	}
}

func (s *Service) resolveAllocationResource(ctx context.Context, change *userdomain.AllocationChange) error {
	switch change.Type {
	case userdomain.AllocationTypeTenant:
		tenant, err := s.tenantRepo.GetByID(ctx, change.ResourceID)
		if err != nil {
			return err
		}
		if tenant == nil {
			return ErrTenantNotFound
		}
		change.TenantID = tenant.ID

	case userdomain.AllocationTypeOrg:
		org, err := s.orgRepo.GetByID(ctx, change.ResourceID)
		if err != nil {
			return err
		}
		if org == nil {
			return ErrOrgNotFound
		}
		change.TenantID = org.TenantID
		change.OrgID = org.ID

	case userdomain.AllocationTypeProject:
		project, err := s.projectRepo.GetByID(ctx, change.ResourceID)
		if err != nil {
			return err
		}
		if project == nil {
			return ErrProjectNotFound
		}
		org, err := s.orgRepo.GetByID(ctx, project.OrgID)
		if err != nil {
			return err
		}
		if org == nil {
			return ErrOrgNotFound
		}
		change.TenantID = org.TenantID
		change.OrgID = org.ID
		change.ProjectID = project.ID
	}
	return nil
}

func normalizeUserIDs(userIDs []string) []string {
	result := make([]string, 0, len(userIDs))
	seen := make(map[string]struct{}, len(userIDs))
	for _, userID := range userIDs {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			continue
		}
		if _, exists := seen[userID]; exists {
			continue
		}
		seen[userID] = struct{}{}
		result = append(result, userID)
	}
	return result
}

func validAllocationType(value userdomain.AllocationType) bool {
	return value == userdomain.AllocationTypeTenant ||
		value == userdomain.AllocationTypeOrg ||
		value == userdomain.AllocationTypeProject
}

func validAllocationOperation(value userdomain.AllocationOperation) bool {
	return value == userdomain.AllocationOperationAdd || value == userdomain.AllocationOperationRemove
}

// GetProfile 按 JWT 中的外部用户 ID 查询用户资料，使用 Redis 缓存并在未命中时回源数据库。
func (s *Service) GetProfile(ctx context.Context, userID string) (*userdomain.User, error) {
	return auditapp.RunRead(ctx, s.auditRecorder,
		func(readCtx context.Context) (*userdomain.User, error) {
			return s.getProfile(readCtx, userID)
		},
		func(user *userdomain.User, err error) *auditdomain.Event {
			result := auditdomain.ResultSuccess
			if err != nil {
				result = auditdomain.ResultFailure
			}
			event := userEvent(userActionRead, result, user, userID, "", nil, nil)
			if err != nil {
				return userFailure(event, err)
			}
			return event
		},
	)
}

// GetProfileDetail 查询当前用户资料，并实时补充租户、组织和项目归属。
func (s *Service) GetProfileDetail(ctx context.Context, userID string) (*userdomain.Profile, error) {
	return auditapp.RunRead(ctx, s.auditRecorder,
		func(readCtx context.Context) (*userdomain.Profile, error) {
			user, err := s.getProfile(readCtx, userID)
			if err != nil {
				return nil, err
			}
			if s.profileRelations == nil {
				return nil, errors.New("user profile relation reader is not configured")
			}

			relations, err := s.profileRelations.GetProfileRelations(readCtx, user.ID)
			if err != nil {
				return nil, err
			}
			if relations == nil {
				relations = &userdomain.ProfileRelations{}
			}
			return &userdomain.Profile{
				User:       *user,
				TenantName: relations.TenantName,
				OrgName:    relations.OrgName,
				Projects:   relations.Projects,
			}, nil
		},
		func(profile *userdomain.Profile, err error) *auditdomain.Event {
			result := auditdomain.ResultSuccess
			var user *userdomain.User
			if profile != nil {
				user = &profile.User
			}
			if err != nil {
				result = auditdomain.ResultFailure
			}
			event := userEvent(userActionRead, result, user, userID, "", nil, nil)
			if err != nil {
				return userFailure(event, err)
			}
			return event
		},
	)
}

func (s *Service) getProfile(ctx context.Context, userID string) (*userdomain.User, error) {
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

	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	type updateResult struct {
		before *userdomain.User
		after  *userdomain.User
	}
	transactor, _ := s.repo.(auditapp.Transactor)
	result, err := auditapp.RunWrite(ctx, s.auditRecorder, transactor, false,
		func(writeCtx context.Context) (updateResult, *auditdomain.Event, error) {
			before, after, updateErr := s.updatePersisted(writeCtx, in)
			if updateErr != nil {
				return updateResult{}, nil, updateErr
			}
			return updateResult{before: before, after: after}, userEvent(
				userActionUpdate, auditdomain.ResultSuccess, after, in.UserID, in.UserID,
				userChanges(before, after), nil,
			), nil
		},
		func(operationErr error) *auditdomain.Event {
			return userFailure(userEvent(userActionUpdate, auditdomain.ResultFailure, nil, in.UserID, in.UserID, nil, nil), operationErr)
		},
	)
	if err != nil {
		return nil, err
	}
	s.refreshUserCaches(ctx, result.after)
	return result.after, nil
}

func (s *Service) updatePersisted(ctx context.Context, in UpdateInput) (*userdomain.User, *userdomain.User, error) {
	if in.UserID == "" || in.Nickname == "" || in.Username == "" || in.TenantID == uuid.Nil || in.OrgID == uuid.Nil {
		return nil, nil, ErrInvalidParam
	}

	current, err := s.repo.GetByUserID(ctx, in.UserID)
	if err != nil {
		return nil, nil, err
	}
	if current == nil {
		return nil, nil, ErrNotFound
	}

	usernameOwner, err := s.repo.GetByUsername(ctx, in.Username)
	if err != nil {
		return nil, nil, err
	}
	if usernameOwner != nil && usernameOwner.ID != current.ID {
		return nil, nil, ErrUsernameExists
	}

	before := *current
	current.Nickname = in.Nickname
	current.Username = in.Username
	current.Email = in.Email
	current.Phone = in.Phone
	current.TenantID = in.TenantID
	current.OrgID = in.OrgID
	current.UpdateBy = in.UserID
	current.UpdateAt = time.Now()
	if err := s.repo.UpdateByUserID(ctx, current); err != nil {
		return nil, nil, err
	}
	return &before, current, nil
}

func (s *Service) refreshUserCaches(ctx context.Context, current *userdomain.User) {
	if current == nil {
		return
	}
	if s.nameCache != nil {
		s.nameCache.Set(current.UserID, current.Nickname)
	}
	if s.profileCache != nil {
		if err := s.profileCache.Set(ctx, current); err != nil {
			logger.Warn(ctx, "refresh user redis cache failed", zap.String("userId", current.UserID), zap.Error(err))
		}
	}
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

	user, err := s.getProfile(ctx, userID)
	if err != nil {
		return "", err
	}
	return user.Nickname, nil
}

// IsBlocked 优先从 Redis 查询用户锁定状态，缓存未命中或异常时回源数据库。
func (s *Service) IsBlocked(ctx context.Context, userID string) (bool, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false, ErrInvalidParam
	}
	if s.blockCache != nil {
		blocked, found, err := s.blockCache.Get(ctx, userID)
		if err != nil {
			logger.Warn(ctx, "query user block status cache failed", zap.String("userId", userID), zap.Error(err))
		} else if found {
			return blocked, nil
		}
	}

	user, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		return false, err
	}
	if user == nil {
		return false, nil
	}
	if s.blockCache != nil {
		if err := s.blockCache.Set(ctx, userID, user.IsBlocked); err != nil {
			logger.Warn(ctx, "backfill user block status cache failed", zap.String("userId", userID), zap.Error(err))
		}
	}
	return user.IsBlocked, nil
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
	if s.blockCache != nil {
		if err := s.blockCache.Replace(ctx, users); err != nil {
			return len(users), err
		}
	}
	return len(users), nil
}
