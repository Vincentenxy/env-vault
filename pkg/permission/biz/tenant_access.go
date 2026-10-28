package biz

import (
	"context"

	"github.com/google/uuid"

	"env-vault/pkg/permission"
	"env-vault/pkg/userctx"
)

const TenantAccess = "envvault.tenant.access"

// NewTenantAccessFactory registers the tenant access chain. Resource IDs supplied
// to this chain must come from trusted resource records, not client tenant hints.
func NewTenantAccessFactory(check CheckPermissionFunc) (*permission.Factory, error) {
	return permission.NewFactory(
		permission.ContextFactoryFunc(tenantAccessContext),
		[]permission.Strategy{NewRemoteStrategy(check)},
		[]permission.ChainDefinition{{
			Key: TenantAccess, ResourceType: "envvault.tenant", Action: "access",
			Nodes: []permission.Node{{Key: "tenant-permission", StrategyKey: RemoteStrategyKey}},
		}},
	)
}

func tenantAccessContext(ctx context.Context, request permission.Request) (permission.Context, error) {
	user := userctx.FromContext(ctx)
	if user == nil {
		return permission.Context{}, permission.Unauthenticated("SUBJECT_REQUIRED", "Authentication required")
	}
	tenantID, err := uuid.Parse(request.Resource.ID)
	if err != nil || tenantID == uuid.Nil {
		return permission.Context{}, permission.EvaluationError("INVALID_TENANT", "Permission evaluation failed", nil)
	}
	return permission.Context{
		Subject: permission.Subject{Type: "user", ID: user.UserID}, Request: request,
	}, nil
}
