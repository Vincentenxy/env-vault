package user

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	auditdomain "env-vault/internal/domain/audit"
	orgdomain "env-vault/internal/domain/organization"
	projectdomain "env-vault/internal/domain/project"
	tenantdomain "env-vault/internal/domain/tenant"
	userdomain "env-vault/internal/domain/user"
)

type auditTxMarker struct{}

type stubAuditRecorder struct {
	record func(context.Context, *auditdomain.Event) error
}

func (s *stubAuditRecorder) Record(ctx context.Context, event *auditdomain.Event) error {
	if s.record != nil {
		return s.record(ctx, event)
	}
	return nil
}

func (s *stubAuditRecorder) RecordBatch(ctx context.Context, events []*auditdomain.Event) error {
	for _, event := range events {
		if err := s.Record(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

type stubUserRepo struct {
	updateByUserID      func(ctx context.Context, user *userdomain.User) error
	getByUserID         func(ctx context.Context, userID string) (*userdomain.User, error)
	getByUsername       func(ctx context.Context, username string) (*userdomain.User, error)
	updatePasswordHash  func(ctx context.Context, username, passwordHash, operator string) error
	list                func(ctx context.Context, filter userdomain.ListFilter) ([]*userdomain.User, error)
	listManagement      func(ctx context.Context, filter userdomain.ManagementListFilter) ([]*userdomain.ManagementUser, int64, error)
	getProfileRelations func(ctx context.Context, userID uuid.UUID) (*userdomain.ProfileRelations, error)
	listAll             func(ctx context.Context) ([]*userdomain.User, error)
	allocate            func(ctx context.Context, change userdomain.AllocationChange) ([]*userdomain.User, []string, error)
	withTx              func(ctx context.Context, fn func(context.Context) error) error
}

func (s *stubUserRepo) WithTx(ctx context.Context, fn func(context.Context) error) error {
	if s.withTx != nil {
		return s.withTx(ctx, fn)
	}
	return fn(ctx)
}

func (s *stubUserRepo) UpdateByUserID(ctx context.Context, user *userdomain.User) error {
	if s.updateByUserID != nil {
		return s.updateByUserID(ctx, user)
	}
	return nil
}

func (s *stubUserRepo) GetByUserID(ctx context.Context, userID string) (*userdomain.User, error) {
	if s.getByUserID != nil {
		return s.getByUserID(ctx, userID)
	}
	return nil, nil
}

func (s *stubUserRepo) GetByUsername(ctx context.Context, username string) (*userdomain.User, error) {
	if s.getByUsername != nil {
		return s.getByUsername(ctx, username)
	}
	return nil, nil
}

func (s *stubUserRepo) UpdatePasswordHashByUsername(ctx context.Context, username, passwordHash, operator string) error {
	if s.updatePasswordHash != nil {
		return s.updatePasswordHash(ctx, username, passwordHash, operator)
	}
	return nil
}

func (s *stubUserRepo) List(ctx context.Context, filter userdomain.ListFilter) ([]*userdomain.User, error) {
	if s.list != nil {
		return s.list(ctx, filter)
	}
	return nil, nil
}

func (s *stubUserRepo) ListManagement(ctx context.Context, filter userdomain.ManagementListFilter) ([]*userdomain.ManagementUser, int64, error) {
	if s.listManagement != nil {
		return s.listManagement(ctx, filter)
	}
	return nil, 0, nil
}

func (s *stubUserRepo) GetProfileRelations(ctx context.Context, userID uuid.UUID) (*userdomain.ProfileRelations, error) {
	if s.getProfileRelations != nil {
		return s.getProfileRelations(ctx, userID)
	}
	return &userdomain.ProfileRelations{}, nil
}

func (s *stubUserRepo) ListAll(ctx context.Context) ([]*userdomain.User, error) {
	if s.listAll != nil {
		return s.listAll(ctx)
	}
	return nil, nil
}

func (s *stubUserRepo) Allocate(ctx context.Context, change userdomain.AllocationChange) ([]*userdomain.User, []string, error) {
	if s.allocate != nil {
		return s.allocate(ctx, change)
	}
	return nil, nil, nil
}

func TestServiceUpdateRecordsAuditInsideTransaction(t *testing.T) {
	user := &userdomain.User{
		ID: uuid.New(), UserID: "u-1", Nickname: "Before", Username: "vince",
		TenantID: uuid.New(), OrgID: uuid.New(),
	}
	repo := &stubUserRepo{
		getByUserID:   func(context.Context, string) (*userdomain.User, error) { return user, nil },
		getByUsername: func(context.Context, string) (*userdomain.User, error) { return user, nil },
		withTx: func(ctx context.Context, fn func(context.Context) error) error {
			return fn(context.WithValue(ctx, auditTxMarker{}, true))
		},
	}
	recorder := &stubAuditRecorder{record: func(ctx context.Context, event *auditdomain.Event) error {
		if marked, _ := ctx.Value(auditTxMarker{}).(bool); !marked {
			t.Fatal("user update audit was not written in business transaction")
		}
		if event.ActionCode != userActionUpdate || event.ResourceID != "u-1" || event.ResultCode != auditdomain.ResultSuccess {
			t.Fatalf("unexpected audit event: %+v", event)
		}
		if len(event.ChangeDetail) != 1 || event.ChangeDetail[0].Field != "nickname" {
			t.Fatalf("unexpected changes: %+v", event.ChangeDetail)
		}
		return nil
	}}
	svc := NewService(repo, nil, nil).WithAuditRecorder(recorder)
	updated, err := svc.Update(context.Background(), UpdateInput{
		UserID: "u-1", Nickname: "After", Username: "vince",
		TenantID: user.TenantID, OrgID: user.OrgID,
	})
	if err != nil || updated.Nickname != "After" {
		t.Fatalf("Update() user=%+v err=%v", updated, err)
	}
}

type stubProfileCache struct {
	getFn     func(ctx context.Context, userID string) (*userdomain.User, error)
	setFn     func(ctx context.Context, user *userdomain.User) error
	replaceFn func(ctx context.Context, users []*userdomain.User) error
}

func (s *stubProfileCache) Get(ctx context.Context, userID string) (*userdomain.User, error) {
	if s.getFn != nil {
		return s.getFn(ctx, userID)
	}
	return nil, nil
}

func (s *stubProfileCache) Set(ctx context.Context, user *userdomain.User) error {
	if s.setFn != nil {
		return s.setFn(ctx, user)
	}
	return nil
}

func (s *stubProfileCache) Replace(ctx context.Context, users []*userdomain.User) error {
	if s.replaceFn != nil {
		return s.replaceFn(ctx, users)
	}
	return nil
}

type stubNameCache struct {
	values   map[string]string
	replaced []*userdomain.User
}

type stubBlockCache struct {
	getFn     func(context.Context, string) (bool, bool, error)
	setFn     func(context.Context, string, bool) error
	replaceFn func(context.Context, []*userdomain.User) error
}

func (s *stubBlockCache) Get(ctx context.Context, userID string) (bool, bool, error) {
	if s.getFn != nil {
		return s.getFn(ctx, userID)
	}
	return false, false, nil
}

func (s *stubBlockCache) Set(ctx context.Context, userID string, blocked bool) error {
	if s.setFn != nil {
		return s.setFn(ctx, userID, blocked)
	}
	return nil
}

func (s *stubBlockCache) Replace(ctx context.Context, users []*userdomain.User) error {
	if s.replaceFn != nil {
		return s.replaceFn(ctx, users)
	}
	return nil
}

type stubTenantReader struct {
	get func(context.Context, uuid.UUID) (*tenantdomain.Tenant, error)
}

func (s stubTenantReader) GetByID(ctx context.Context, id uuid.UUID) (*tenantdomain.Tenant, error) {
	if s.get != nil {
		return s.get(ctx, id)
	}
	return nil, nil
}

type stubOrgReader struct {
	get func(context.Context, uuid.UUID) (*orgdomain.Organization, error)
}

func (s stubOrgReader) GetByID(ctx context.Context, id uuid.UUID) (*orgdomain.Organization, error) {
	if s.get != nil {
		return s.get(ctx, id)
	}
	return nil, nil
}

type stubProjectReader struct {
	get func(context.Context, uuid.UUID) (*projectdomain.Project, error)
}

func (s stubProjectReader) GetByID(ctx context.Context, id uuid.UUID) (*projectdomain.Project, error) {
	if s.get != nil {
		return s.get(ctx, id)
	}
	return nil, nil
}

func newStubNameCache() *stubNameCache {
	return &stubNameCache{values: make(map[string]string)}
}

func (s *stubNameCache) Get(userID string) (string, bool) {
	name, ok := s.values[userID]
	return name, ok
}

func (s *stubNameCache) Set(userID, nickname string) {
	s.values[userID] = nickname
}

func (s *stubNameCache) Replace(users []*userdomain.User) {
	s.replaced = users
	s.values = make(map[string]string, len(users))
	for _, user := range users {
		s.values[user.UserID] = user.Nickname
	}
}

func testDomainUser(userID string) *userdomain.User {
	now := time.Now()
	return &userdomain.User{
		ID: uuid.New(), UserID: userID, Nickname: "old name", Username: "old-login",
		TenantID: uuid.New(), OrgID: uuid.New(), CreateAt: now, UpdateAt: now,
	}
}

func TestService_List_AppliesFilterPriority(t *testing.T) {
	tenantID, orgID, projectID := uuid.New(), uuid.New(), uuid.New()
	tests := []struct {
		name string
		in   ListInput
		want userdomain.ListFilter
	}{
		{
			name: "project over all filters",
			in:   ListInput{TenantID: tenantID, OrgID: orgID, ProjectID: projectID, Undistributed: true},
			want: userdomain.ListFilter{ProjectID: projectID},
		},
		{
			name: "organization over tenant and undistributed",
			in:   ListInput{TenantID: tenantID, OrgID: orgID, Undistributed: true},
			want: userdomain.ListFilter{OrgID: orgID},
		},
		{
			name: "tenant over undistributed",
			in:   ListInput{TenantID: tenantID, Undistributed: true},
			want: userdomain.ListFilter{TenantID: tenantID},
		},
		{
			name: "undistributed",
			in:   ListInput{Undistributed: true},
			want: userdomain.ListFilter{Undistributed: true},
		},
		{
			name: "no filter returns all",
			in:   ListInput{},
			want: userdomain.ListFilter{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user := &userdomain.User{ID: uuid.New(), UserID: "u-1", Nickname: "User One"}
			repo := &stubUserRepo{list: func(_ context.Context, filter userdomain.ListFilter) ([]*userdomain.User, error) {
				if filter != tt.want {
					t.Fatalf("unexpected filter: got=%+v want=%+v", filter, tt.want)
				}
				return []*userdomain.User{user}, nil
			}}

			got, err := NewService(repo, nil, nil).List(context.Background(), tt.in)
			if err != nil || len(got) != 1 || got[0] != user {
				t.Fatalf("unexpected result users=%+v err=%v", got, err)
			}
		})
	}
}

func TestService_ListManagement_PassesNormalizedQueryToRepository(t *testing.T) {
	tenantID := uuid.New()
	want := &userdomain.ManagementUser{User: userdomain.User{ID: uuid.New(), UserID: "u-1"}}
	repo := &stubUserRepo{listManagement: func(_ context.Context, filter userdomain.ManagementListFilter) ([]*userdomain.ManagementUser, int64, error) {
		if filter.TenantID != tenantID || filter.Keyword != "tester" || filter.PageNum != 3 || filter.PageSize != 50 {
			t.Fatalf("unexpected management filter: %+v", filter)
		}
		return []*userdomain.ManagementUser{want}, 101, nil
	}}

	items, total, err := NewService(repo, nil, nil).ListManagement(context.Background(), ManagementListInput{
		TenantID: tenantID, Keyword: "  tester  ", PageNum: 3, PageSize: 50,
	})
	if err != nil || total != 101 || len(items) != 1 || items[0] != want {
		t.Fatalf("unexpected result items=%+v total=%d err=%v", items, total, err)
	}
}

func TestService_AllocateProject_ResolvesHierarchyAndRefreshesCache(t *testing.T) {
	tenantID, orgID, projectID := uuid.New(), uuid.New(), uuid.New()
	updated := &userdomain.User{ID: uuid.New(), UserID: "u-1", TenantID: tenantID, OrgID: orgID}
	repo := &stubUserRepo{allocate: func(_ context.Context, change userdomain.AllocationChange) ([]*userdomain.User, []string, error) {
		if change.Type != userdomain.AllocationTypeProject || change.Operation != userdomain.AllocationOperationAdd ||
			change.ProjectID != projectID || change.OrgID != orgID || change.TenantID != tenantID ||
			len(change.UserIDs) != 1 || change.UserIDs[0] != "u-1" || change.Operator != "operator" {
			t.Fatalf("unexpected allocation change: %+v", change)
		}
		return []*userdomain.User{updated}, nil, nil
	}}
	profileRefreshed := false
	profileCache := &stubProfileCache{setFn: func(_ context.Context, user *userdomain.User) error {
		profileRefreshed = user == updated
		return nil
	}}
	svc := NewService(repo, profileCache, nil, WithAllocationRepositories(
		stubTenantReader{},
		stubOrgReader{get: func(_ context.Context, id uuid.UUID) (*orgdomain.Organization, error) {
			if id != orgID {
				t.Fatalf("unexpected org id: %s", id)
			}
			return &orgdomain.Organization{ID: orgID, TenantID: tenantID}, nil
		}},
		stubProjectReader{get: func(_ context.Context, id uuid.UUID) (*projectdomain.Project, error) {
			if id != projectID {
				t.Fatalf("unexpected project id: %s", id)
			}
			return &projectdomain.Project{ID: projectID, OrgID: orgID}, nil
		}},
	))

	affected, err := svc.Allocate(context.Background(), AllocateInput{
		Type: "project", Operation: "add", ResourceID: projectID,
		UserIDs: []string{" u-1 ", "u-1"}, Operator: "operator",
	})
	if err != nil || affected != 1 || !profileRefreshed {
		t.Fatalf("unexpected allocation result affected=%d refreshed=%v err=%v", affected, profileRefreshed, err)
	}
}

func TestService_Allocate_ReturnsMissingUsersWithoutSuccess(t *testing.T) {
	tenantID := uuid.New()
	repo := &stubUserRepo{allocate: func(context.Context, userdomain.AllocationChange) ([]*userdomain.User, []string, error) {
		return nil, []string{"missing"}, nil
	}}
	svc := NewService(repo, nil, nil, WithAllocationRepositories(
		stubTenantReader{get: func(context.Context, uuid.UUID) (*tenantdomain.Tenant, error) {
			return &tenantdomain.Tenant{ID: tenantID}, nil
		}}, stubOrgReader{}, stubProjectReader{},
	))
	_, err := svc.Allocate(context.Background(), AllocateInput{
		Type: "tenant", Operation: "remove", ResourceID: tenantID,
		UserIDs: []string{"missing"}, Operator: "operator",
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_GetProfile_UsesCacheAndFallsBackToDatabase(t *testing.T) {
	t.Run("redis hit", func(t *testing.T) {
		cached := testDomainUser("u-1")
		profileCache := &stubProfileCache{getFn: func(_ context.Context, userID string) (*userdomain.User, error) {
			if userID != "u-1" {
				t.Fatalf("unexpected user id: %q", userID)
			}
			return cached, nil
		}}
		repo := &stubUserRepo{getByUserID: func(context.Context, string) (*userdomain.User, error) {
			t.Fatal("database must not be queried on cache hit")
			return nil, nil
		}}
		nameCache := newStubNameCache()

		got, err := NewService(repo, profileCache, nameCache).GetProfile(context.Background(), " u-1 ")
		if err != nil || got != cached {
			t.Fatalf("unexpected profile=%+v err=%v", got, err)
		}
		if name, ok := nameCache.Get("u-1"); !ok || name != cached.Nickname {
			t.Fatalf("name cache not backfilled: name=%q ok=%v", name, ok)
		}
	})

	t.Run("redis failure falls back to database", func(t *testing.T) {
		databaseUser := testDomainUser("u-2")
		cacheSet := false
		profileCache := &stubProfileCache{
			getFn: func(context.Context, string) (*userdomain.User, error) {
				return nil, errors.New("redis unavailable")
			},
			setFn: func(_ context.Context, user *userdomain.User) error {
				cacheSet = user == databaseUser
				return nil
			},
		}
		repo := &stubUserRepo{getByUserID: func(_ context.Context, userID string) (*userdomain.User, error) {
			if userID != "u-2" {
				t.Fatalf("unexpected user id: %q", userID)
			}
			return databaseUser, nil
		}}

		got, err := NewService(repo, profileCache, newStubNameCache()).GetProfile(context.Background(), "u-2")
		if err != nil || got != databaseUser || !cacheSet {
			t.Fatalf("unexpected profile=%+v cacheSet=%v err=%v", got, cacheSet, err)
		}
	})
}

func TestService_GetProfile_ValidatesAndHandlesMissingUser(t *testing.T) {
	svc := NewService(&stubUserRepo{}, nil, nil)
	if _, err := svc.GetProfile(context.Background(), "  "); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam, got %v", err)
	}
	if _, err := svc.GetProfile(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_GetProfileDetail_AddsCurrentResourceRelations(t *testing.T) {
	user := testDomainUser("u-1")
	projectID := uuid.New()
	repo := &stubUserRepo{
		getByUserID: func(context.Context, string) (*userdomain.User, error) {
			return user, nil
		},
		getProfileRelations: func(_ context.Context, userID uuid.UUID) (*userdomain.ProfileRelations, error) {
			if userID != user.ID {
				t.Fatalf("internal user id = %s", userID)
			}
			return &userdomain.ProfileRelations{
				TenantName: "平台中心",
				OrgName:    "研发部门",
				Projects:   []userdomain.ProfileProject{{ID: projectID, Name: "EnvVault"}},
			}, nil
		},
	}

	profile, err := NewService(repo, nil, nil).GetProfileDetail(context.Background(), "u-1")
	if err != nil {
		t.Fatalf("GetProfileDetail() error = %v", err)
	}
	if profile.UserID != "u-1" || profile.TenantName != "平台中心" || profile.OrgName != "研发部门" {
		t.Fatalf("unexpected profile: %+v", profile)
	}
	if len(profile.Projects) != 1 || profile.Projects[0].ID != projectID || profile.Projects[0].Name != "EnvVault" {
		t.Fatalf("unexpected projects: %+v", profile.Projects)
	}
}

func TestService_Update_UsesExternalUserIDAndRefreshesCaches(t *testing.T) {
	current := testDomainUser("external-1")
	tenantID := uuid.New()
	orgID := uuid.New()
	updated := false
	redisUpdated := false
	repo := &stubUserRepo{
		getByUserID: func(ctx context.Context, userID string) (*userdomain.User, error) {
			if userID != "external-1" {
				t.Fatalf("unexpected user id: %s", userID)
			}
			return current, nil
		},
		getByUsername: func(ctx context.Context, username string) (*userdomain.User, error) {
			if username != "new-login" {
				t.Fatalf("unexpected username lookup: %s", username)
			}
			return nil, nil
		},
		updateByUserID: func(ctx context.Context, user *userdomain.User) error {
			updated = true
			if user.UserID != "external-1" || user.Nickname != "New Name" || user.Username != "new-login" || user.UpdateBy != "external-1" {
				t.Fatalf("unexpected update payload: %+v", user)
			}
			if user.TenantID != tenantID || user.OrgID != orgID || user.Email != "user@example.com" || user.Phone != "13800000000" {
				t.Fatalf("profile fields not updated: %+v", user)
			}
			return nil
		},
	}
	profileCache := &stubProfileCache{setFn: func(ctx context.Context, user *userdomain.User) error {
		redisUpdated = true
		return nil
	}}
	nameCache := newStubNameCache()
	svc := NewService(repo, profileCache, nameCache)

	got, err := svc.Update(context.Background(), UpdateInput{
		UserID: " external-1 ", Nickname: " New Name ", Username: " new-login ",
		Email: " user@example.com ", Phone: " 13800000000 ", TenantID: tenantID, OrgID: orgID,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !updated || !redisUpdated {
		t.Fatalf("expected database and redis updates, db=%v redis=%v", updated, redisUpdated)
	}
	if name, ok := nameCache.Get("external-1"); !ok || name != "New Name" {
		t.Fatalf("memory cache not refreshed: name=%q ok=%v", name, ok)
	}
	if got != current {
		t.Fatal("expected updated domain object")
	}
}

func TestService_Update_RejectsUsernameOwnedByAnotherUser(t *testing.T) {
	current := testDomainUser("external-1")
	tenantID := uuid.New()
	repo := &stubUserRepo{
		getByUserID: func(ctx context.Context, userID string) (*userdomain.User, error) {
			return current, nil
		},
		getByUsername: func(ctx context.Context, username string) (*userdomain.User, error) {
			return testDomainUser("external-2"), nil
		},
		updateByUserID: func(ctx context.Context, user *userdomain.User) error {
			t.Fatal("database update should not be called")
			return nil
		},
	}
	svc := NewService(repo, &stubProfileCache{}, newStubNameCache())
	_, err := svc.Update(context.Background(), UpdateInput{
		UserID: "external-1", Nickname: "name", Username: "duplicate", TenantID: tenantID, OrgID: uuid.New(),
	})
	if !errors.Is(err, ErrUsernameExists) {
		t.Fatalf("expected ErrUsernameExists, got %v", err)
	}
}

func TestService_Update_NotFoundAndInvalidInput(t *testing.T) {
	svc := NewService(&stubUserRepo{}, &stubProfileCache{}, newStubNameCache())
	if _, err := svc.Update(context.Background(), UpdateInput{}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam, got %v", err)
	}
	_, err := svc.Update(context.Background(), UpdateInput{
		UserID: "missing", Nickname: "name", Username: "login", TenantID: uuid.New(), OrgID: uuid.New(),
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_Update_RedisFailureDoesNotFailDatabaseUpdate(t *testing.T) {
	current := testDomainUser("external-1")
	repo := &stubUserRepo{
		getByUserID: func(ctx context.Context, userID string) (*userdomain.User, error) { return current, nil },
	}
	profileCache := &stubProfileCache{setFn: func(ctx context.Context, user *userdomain.User) error {
		return errors.New("redis unavailable")
	}}
	svc := NewService(repo, profileCache, newStubNameCache())
	_, err := svc.Update(context.Background(), UpdateInput{
		UserID: "external-1", Nickname: "name", Username: "login", TenantID: uuid.New(), OrgID: uuid.New(),
	})
	if err != nil {
		t.Fatalf("cache failure should not fail update: %v", err)
	}
}

func TestService_GetNickname_CacheOrderAndBackfill(t *testing.T) {
	t.Run("memory hit", func(t *testing.T) {
		nameCache := newStubNameCache()
		nameCache.Set("u-1", "memory-name")
		profileCache := &stubProfileCache{getFn: func(ctx context.Context, userID string) (*userdomain.User, error) {
			t.Fatal("redis should not be queried")
			return nil, nil
		}}
		svc := NewService(&stubUserRepo{}, profileCache, nameCache)
		name, err := svc.GetNickname(context.Background(), "u-1")
		if err != nil || name != "memory-name" {
			t.Fatalf("unexpected result name=%q err=%v", name, err)
		}
	})

	t.Run("redis hit", func(t *testing.T) {
		nameCache := newStubNameCache()
		profileCache := &stubProfileCache{getFn: func(ctx context.Context, userID string) (*userdomain.User, error) {
			return &userdomain.User{UserID: userID, Nickname: "redis-name"}, nil
		}}
		repo := &stubUserRepo{getByUserID: func(ctx context.Context, userID string) (*userdomain.User, error) {
			t.Fatal("database should not be queried")
			return nil, nil
		}}
		svc := NewService(repo, profileCache, nameCache)
		name, err := svc.GetNickname(context.Background(), "u-1")
		if err != nil || name != "redis-name" {
			t.Fatalf("unexpected result name=%q err=%v", name, err)
		}
		if memoryName, ok := nameCache.Get("u-1"); !ok || memoryName != "redis-name" {
			t.Fatal("redis result was not backfilled to memory")
		}
	})

	t.Run("redis failure falls back to database", func(t *testing.T) {
		nameCache := newStubNameCache()
		redisSet := false
		profileCache := &stubProfileCache{
			getFn: func(ctx context.Context, userID string) (*userdomain.User, error) {
				return nil, errors.New("redis unavailable")
			},
			setFn: func(ctx context.Context, user *userdomain.User) error {
				redisSet = true
				return nil
			},
		}
		repo := &stubUserRepo{getByUserID: func(ctx context.Context, userID string) (*userdomain.User, error) {
			return &userdomain.User{UserID: userID, Nickname: "database-name"}, nil
		}}
		svc := NewService(repo, profileCache, nameCache)
		name, err := svc.GetNickname(context.Background(), "u-1")
		if err != nil || name != "database-name" {
			t.Fatalf("unexpected result name=%q err=%v", name, err)
		}
		if !redisSet {
			t.Fatal("database result was not backfilled to redis")
		}
	})
}

func TestService_GetNickname_DatabaseMiss(t *testing.T) {
	svc := NewService(&stubUserRepo{}, &stubProfileCache{}, newStubNameCache())
	_, err := svc.GetNickname(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_IsBlocked_UsesCacheAndFallsBackToDatabase(t *testing.T) {
	t.Run("cache hit", func(t *testing.T) {
		repo := &stubUserRepo{getByUserID: func(context.Context, string) (*userdomain.User, error) {
			t.Fatal("database must not be queried on block cache hit")
			return nil, nil
		}}
		cache := &stubBlockCache{getFn: func(context.Context, string) (bool, bool, error) {
			return true, true, nil
		}}
		blocked, err := NewService(repo, nil, nil, WithBlockStatusCache(cache)).IsBlocked(context.Background(), "u-1")
		if err != nil || !blocked {
			t.Fatalf("unexpected blocked=%v err=%v", blocked, err)
		}
	})

	t.Run("cache miss", func(t *testing.T) {
		backfilled := false
		cache := &stubBlockCache{setFn: func(_ context.Context, userID string, blocked bool) error {
			backfilled = userID == "u-2" && blocked
			return nil
		}}
		repo := &stubUserRepo{getByUserID: func(context.Context, string) (*userdomain.User, error) {
			return &userdomain.User{UserID: "u-2", IsBlocked: true}, nil
		}}
		blocked, err := NewService(repo, nil, nil, WithBlockStatusCache(cache)).IsBlocked(context.Background(), "u-2")
		if err != nil || !blocked || !backfilled {
			t.Fatalf("unexpected blocked=%v backfilled=%v err=%v", blocked, backfilled, err)
		}
	})
}

func TestService_WarmUp_ReplacesAllCaches(t *testing.T) {
	users := []*userdomain.User{
		{UserID: "u-1", Nickname: "one"},
		{UserID: "u-2", Nickname: "two"},
	}
	nameCache := newStubNameCache()
	redisReplaced := false
	blockReplaced := false
	profileCache := &stubProfileCache{replaceFn: func(ctx context.Context, got []*userdomain.User) error {
		redisReplaced = true
		if len(got) != len(users) {
			t.Fatalf("unexpected redis preload count: %d", len(got))
		}
		return nil
	}}
	repo := &stubUserRepo{listAll: func(ctx context.Context) ([]*userdomain.User, error) {
		return users, nil
	}}
	blockCache := &stubBlockCache{replaceFn: func(_ context.Context, got []*userdomain.User) error {
		blockReplaced = len(got) == len(users)
		return nil
	}}
	svc := NewService(repo, profileCache, nameCache, WithBlockStatusCache(blockCache))
	count, err := svc.WarmUp(context.Background())
	if err != nil || count != 2 {
		t.Fatalf("unexpected warmup result count=%d err=%v", count, err)
	}
	if !redisReplaced || !blockReplaced || len(nameCache.replaced) != 2 {
		t.Fatalf("caches were not replaced: redis=%v block=%v memory=%d", redisReplaced, blockReplaced, len(nameCache.replaced))
	}
}
