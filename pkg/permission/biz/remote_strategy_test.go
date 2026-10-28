package biz_test

import (
	"context"
	"errors"
	"testing"

	"env-vault/pkg/permission"
	"env-vault/pkg/permission/biz"
)

func trustedContext(_ context.Context, request permission.Request) (permission.Context, error) {
	return permission.Context{Subject: permission.Subject{Type: "user", ID: "trusted-user"}, Request: request}, nil
}

func chain() permission.ChainDefinition {
	return permission.ChainDefinition{
		Key: "test.secret.read", ResourceType: "envvault.secret.item", Action: "read",
		Nodes: []permission.Node{{Key: "backend", StrategyKey: biz.RemoteStrategyKey}},
	}
}

func factory(t *testing.T, check biz.CheckPermissionFunc) *permission.Factory {
	t.Helper()
	f, err := permission.NewFactory(permission.ContextFactoryFunc(trustedContext),
		[]permission.Strategy{biz.NewRemoteStrategy(check)}, []permission.ChainDefinition{chain()})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestRemoteStrategyUsesTrustedTargetAndPreservesFailureSemantics(t *testing.T) {
	dependencyErr := errors.New("private dependency error")
	for _, tc := range []struct {
		name    string
		allowed bool
		err     error
		kind    permission.FailureKind
	}{
		{name: "allow", allowed: true},
		{name: "deny", kind: permission.DeniedFailure},
		{name: "backend failure overrides allow", allowed: true, err: dependencyErr, kind: permission.EvaluationFailure},
		{name: "denial error is not a decision", err: permission.Denied("PRIVATE", "private message"), kind: permission.EvaluationFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			type contextKey struct{}
			ctx := context.WithValue(context.Background(), contextKey{}, "same-context")
			calls := 0
			f := factory(t, func(got context.Context, request biz.CheckRequest) (bool, error) {
				calls++
				if got != ctx || request.Subject.ID != "trusted-user" || request.Subject.Type != "user" ||
					request.Resource != (permission.Resource{Type: "envvault.secret.item", ID: "secret-1"}) || request.Action != "read" {
					t.Fatalf("incorrect permission request: %+v", request)
				}
				return tc.allowed, tc.err
			})
			err := f.Check(ctx, "test.secret.read", "secret-1", permission.Values{"userId": "untrusted-user", "action": "write"})
			if calls != 1 {
				t.Fatalf("backend check called %d times", calls)
			}
			if tc.kind == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var failure *permission.Failure
			if !errors.As(err, &failure) || failure.Kind() != tc.kind {
				t.Fatalf("error = %v, want %s", err, tc.kind)
			}
			if tc.err != nil && failure.Cause() != tc.err {
				t.Fatal("backend error cause was lost")
			}
		})
	}
}

func TestUnimplementedBackendFailsClosed(t *testing.T) {
	f := factory(t, biz.CheckBackendPermission)
	err := f.Check(context.Background(), "test.secret.read", "secret-1")
	var failure *permission.Failure
	if !errors.As(err, &failure) || failure.Kind() != permission.EvaluationFailure ||
		!errors.Is(failure.Cause(), biz.ErrBackendNotImplemented) {
		t.Fatalf("unimplemented backend must fail closed: %v", err)
	}
}

func TestMissingBackendCheckFailsRegistration(t *testing.T) {
	for _, strategy := range []*biz.RemoteStrategy{biz.NewRemoteStrategy(nil), new(biz.RemoteStrategy)} {
		f, err := permission.NewFactory(permission.ContextFactoryFunc(trustedContext),
			[]permission.Strategy{strategy}, []permission.ChainDefinition{chain()})
		if err == nil || f != nil {
			t.Fatal("missing backend check must fail registration")
		}
	}
}

func TestCanceledCheckDoesNotCallBackend(t *testing.T) {
	called := false
	s := biz.NewRemoteStrategy(func(context.Context, biz.CheckRequest) (bool, error) {
		called = true
		return true, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.Check(ctx, permission.Context{}, permission.Node{})
	if !errors.Is(err, context.Canceled) || called {
		t.Fatal("canceled checks must not call the backend")
	}
}
