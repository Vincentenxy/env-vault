package application

import (
	"context"
	"errors"
	"testing"
)

type nicknameResolverFunc func(context.Context, string) (string, error)

func (f nicknameResolverFunc) GetNickname(ctx context.Context, userID string) (string, error) {
	return f(ctx, userID)
}

func TestResolveNickname(t *testing.T) {
	resolver := nicknameResolverFunc(func(_ context.Context, userID string) (string, error) {
		if userID == "manager-1" {
			return "管理员一", nil
		}
		return "", errors.New("user not found")
	})

	if got := ResolveNickname(context.Background(), resolver, " manager-1 "); got != "管理员一" {
		t.Fatalf("expected resolved nickname, got %q", got)
	}
	if got := ResolveNickname(context.Background(), resolver, "missing"); got != "" {
		t.Fatalf("resolver error must return empty nickname, got %q", got)
	}
	if got := ResolveNickname(context.Background(), nil, "manager-1"); got != "" {
		t.Fatalf("nil resolver must return empty nickname, got %q", got)
	}
}
