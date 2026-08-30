package organization

import (
	"github.com/google/uuid"

	auditapp "env-vault/internal/application/audit"
	auditdomain "env-vault/internal/domain/audit"
	orgdomain "env-vault/internal/domain/organization"
)

const organizationResourceType = "organization"

func organizationEvent(action, result string, org *orgdomain.Organization, fallbackID uuid.UUID, fallbackName, scopeID, operator string, changes []auditdomain.Change, detail map[string]any) *auditdomain.Event {
	id := fallbackID
	name := fallbackName
	if org != nil {
		id = org.ID
		name = org.Name
		if scopeID == "" {
			scopeID = org.TenantID.String()
		}
	}
	resourceID := ""
	if id != uuid.Nil {
		resourceID = id.String()
	}
	return auditapp.NewResourceEvent(auditapp.ResourceEventInput{
		ActionCode: action, ResultCode: result, ResourceType: organizationResourceType,
		ResourceID: resourceID, ResourceName: name, ScopeType: "tenant", ScopeID: scopeID,
		Operator: operator, Changes: changes, Detail: detail,
	})
}

func organizationFailure(event *auditdomain.Event, err error) *auditdomain.Event {
	return auditapp.MarkFailure(event, err, ErrInvalidParam, ErrCodeExists, ErrNotFound, auditapp.ErrTransactionUnavailable)
}

func organizationChanges(before, after *orgdomain.Organization) []auditdomain.Change {
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
