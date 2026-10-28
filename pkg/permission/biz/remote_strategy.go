package biz

import (
	"context"
	"errors"

	"env-vault/pkg/permission"
)

const RemoteStrategyKey = "envvault.remote-permission"

var errCheckerRequired = errors.New("backend permission checker is required")

// RemoteStrategy delegates business authorization to an injected backend check.
// The check must support concurrent reuse and never retain per-request state.
type RemoteStrategy struct {
	check CheckPermissionFunc
}

var (
	_ permission.Strategy      = (*RemoteStrategy)(nil)
	_ permission.NodeValidator = (*RemoteStrategy)(nil)
)

func NewRemoteStrategy(check CheckPermissionFunc) *RemoteStrategy {
	return &RemoteStrategy{check: check}
}

func (*RemoteStrategy) Key() string { return RemoteStrategyKey }

func (s *RemoteStrategy) Validate(_ permission.Node) error {
	if s == nil || s.check == nil {
		return errCheckerRequired
	}
	return nil
}

func (s *RemoteStrategy) Check(ctx context.Context, input permission.Context, _ permission.Node) (permission.Decision, error) {
	if s == nil || s.check == nil {
		return permission.Decision{}, errCheckerRequired
	}
	if err := ctx.Err(); err != nil {
		return permission.Decision{}, err
	}
	allowed, err := s.check(ctx, CheckRequest{
		Subject: input.Subject, Resource: input.Request.Resource, Action: input.Request.Action,
	})
	if err != nil {
		return permission.Decision{}, err
	}
	if !allowed {
		return permission.Decision{
			Outcome: permission.Deny, ReasonCode: "REMOTE_PERMISSION_DENIED", Message: "Permission denied",
		}, nil
	}
	return permission.Decision{Outcome: permission.Allow}, nil
}
