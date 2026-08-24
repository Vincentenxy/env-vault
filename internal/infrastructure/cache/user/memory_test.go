package user

import (
	"testing"

	userdomain "env-vault/internal/domain/user"
)

func TestMemoryNameCache_SetAndReplace(t *testing.T) {
	cache := NewMemoryNameCache()
	cache.Set("u-1", "old")
	if name, ok := cache.Get("u-1"); !ok || name != "old" {
		t.Fatalf("unexpected cache value name=%q ok=%v", name, ok)
	}

	cache.Replace([]*userdomain.User{{UserID: "u-2", Nickname: "new"}})
	if _, ok := cache.Get("u-1"); ok {
		t.Fatal("Replace should remove stale entries")
	}
	if name, ok := cache.Get("u-2"); !ok || name != "new" {
		t.Fatalf("unexpected replacement value name=%q ok=%v", name, ok)
	}
}

func TestRedisProfileCache_NilClientIsNoOp(t *testing.T) {
	cache := NewRedisProfileCache(nil, "env_vault")
	if cache.key != "env_vault:user:info" {
		t.Fatalf("unexpected redis key: %s", cache.key)
	}
	user, err := cache.Get(t.Context(), "u-1")
	if err != nil || user != nil {
		t.Fatalf("unexpected nil-client get result user=%v err=%v", user, err)
	}
	if err := cache.Set(t.Context(), &userdomain.User{UserID: "u-1"}); err != nil {
		t.Fatalf("nil-client set should be no-op: %v", err)
	}
	if err := cache.Replace(t.Context(), nil); err != nil {
		t.Fatalf("nil-client replace should be no-op: %v", err)
	}
}

func TestRedisBlockStatusCache_NilClientIsNoOp(t *testing.T) {
	cache := NewRedisBlockStatusCache(nil, "env_vault")
	if cache.key != "env_vault:user:blocked" {
		t.Fatalf("unexpected redis key: %s", cache.key)
	}
	blocked, found, err := cache.Get(t.Context(), "u-1")
	if err != nil || found || blocked {
		t.Fatalf("unexpected nil-client result blocked=%v found=%v err=%v", blocked, found, err)
	}
	if err := cache.Set(t.Context(), "u-1", true); err != nil {
		t.Fatalf("nil-client set should be no-op: %v", err)
	}
	if err := cache.Replace(t.Context(), []*userdomain.User{{UserID: "u-1", IsBlocked: true}}); err != nil {
		t.Fatalf("nil-client replace should be no-op: %v", err)
	}
}

func TestCachedUser_PreservesBlockedStatus(t *testing.T) {
	user := &userdomain.User{UserID: "u-1", IsBlocked: true}
	if cached := toCachedUser(user); !cached.IsBlocked || !cached.toDomain().IsBlocked {
		t.Fatal("blocked status was not preserved in Redis profile payload")
	}
}
