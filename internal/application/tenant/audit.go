package tenant

import (
	"github.com/google/uuid"

	auditapp "env-vault/internal/application/audit"
	auditdomain "env-vault/internal/domain/audit"
	tenantdomain "env-vault/internal/domain/tenant"
)

const tenantResourceType = "tenant"

func tenantEvent(action, result string, tenant *tenantdomain.Tenant, fallbackID uuid.UUID, fallbackName, operator string, changes []auditdomain.Change, detail map[string]any) *auditdomain.Event {
	id := fallbackID
	name := fallbackName
	if tenant != nil {
		id = tenant.ID
		name = tenant.Name
	}
	event := auditapp.NewResourceEvent(auditapp.ResourceEventInput{
		ActionCode: action, ResultCode: result, ResourceType: tenantResourceType,
		ResourceID: uuidString(id), ResourceName: name, ScopeType: tenantResourceType,
		ScopeID: uuidString(id), Operator: operator, Changes: changes, Detail: detail,
	})
	return event
}

func tenantFailure(event *auditdomain.Event, err error) *auditdomain.Event {
	return auditapp.MarkFailure(event, err, ErrCodeExists, ErrNotFound, ErrInvalidParam, auditapp.ErrTransactionUnavailable)
}

func uuidString(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

func tenantChanges(before, after *tenantdomain.Tenant) []auditdomain.Change {
	if after == nil {
		return nil
	}
	if before == nil {
		return auditapp.ChangedFields(
			auditdomain.Change{Field: "code", After: after.Code},
			auditdomain.Change{Field: "name", After: after.Name},
			auditdomain.Change{Field: "remark", After: after.Remark},
			auditdomain.Change{Field: "manager", After: after.Manager},
		)
	}
	return auditapp.ChangedFields(
		auditdomain.Change{Field: "name", Before: before.Name, After: after.Name},
		auditdomain.Change{Field: "remark", Before: before.Remark, After: after.Remark},
		auditdomain.Change{Field: "manager", Before: before.Manager, After: after.Manager},
	)
}
