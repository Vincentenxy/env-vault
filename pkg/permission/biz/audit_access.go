package biz

import (
	"context"
	"strings"

	"env-vault/pkg/permission"
	"env-vault/pkg/userctx"
)

const AuditGlobalRead = "envvault.audit.global.read"

// NewAuditAccessFactory registers only the global audit read boundary.
func NewAuditAccessFactory(check CheckPermissionFunc) (*permission.Factory, error) {
	return permission.NewFactory(
		permission.ContextFactoryFunc(func(ctx context.Context, request permission.Request) (permission.Context, error) {
			user := userctx.FromContext(ctx)
			if user == nil || strings.TrimSpace(user.UserID) == "" {
				return permission.Context{}, permission.Unauthenticated("SUBJECT_REQUIRED", "Authentication required")
			}
			return permission.Context{
				Subject: permission.Subject{Type: "user", ID: user.UserID}, Request: request,
			}, nil
		}),
		[]permission.Strategy{NewRemoteStrategy(check)},
		[]permission.ChainDefinition{{
			Key: AuditGlobalRead, ResourceType: "envvault.audit", Action: "global.read",
			Nodes: []permission.Node{{Key: "audit-global-read", StrategyKey: RemoteStrategyKey}},
		}},
	)
}

// CheckAuditGlobalPermission is the deliberately permissive first-stage policy
// approved for global audit viewing. Authentication is still required by the
// chain context and HTTP middleware. Do not reuse it for other business rules.
func CheckAuditGlobalPermission(ctx context.Context, _ CheckRequest) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// TODO: Delegate global audit authorization to the external permission center.
	return true, nil
}
