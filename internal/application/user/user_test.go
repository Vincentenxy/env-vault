package user

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	userdomain "env-vault/internal/domain/user"
)

type stubUserRepo struct {
	updateByUserID      func(ctx context.Context, user *userdomain.User) error
	getByUserID         func(ctx context.Context, userID string) (*userdomain.User, error)
	getByTenantUsername func(ctx context.Context, tenantID uuid.UUID, username string) (*userdomain.User, error)
	listAll             func(ctx context.Context) ([]*userdomain.User, error)
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

func (s *stubUserRepo) GetByTenantUsername(ctx context.Context, tenantID uuid.UUID, username string) (*userdomain.User, error) {
	if s.getByTenantUsername != nil {
		return s.getByTenantUsername(ctx, tenantID, username)
	}
	return nil, nil
}

func (s *stubUserRepo) ListAll(ctx context.Context) ([]*userdomain.User, error) {
	if s.listAll != nil {
		return s.listAll(ctx)
	}
	return nil, nil
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
		getByTenantUsername: func(ctx context.Context, gotTenantID uuid.UUID, username string) (*userdomain.User, error) {
			if gotTenantID != tenantID || username != "new-login" {
				t.Fatalf("unexpected username lookup: %s %s", gotTenantID, username)
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
		getByTenantUsername: func(ctx context.Context, tenantID uuid.UUID, username string) (*userdomain.User, error) {
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

func TestService_WarmUp_ReplacesBothCaches(t *testing.T) {
	users := []*userdomain.User{
		{UserID: "u-1", Nickname: "one"},
		{UserID: "u-2", Nickname: "two"},
	}
	nameCache := newStubNameCache()
	redisReplaced := false
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
	svc := NewService(repo, profileCache, nameCache)
	count, err := svc.WarmUp(context.Background())
	if err != nil || count != 2 {
		t.Fatalf("unexpected warmup result count=%d err=%v", count, err)
	}
	if !redisReplaced || len(nameCache.replaced) != 2 {
		t.Fatalf("caches were not replaced: redis=%v memory=%d", redisReplaced, len(nameCache.replaced))
	}
}
