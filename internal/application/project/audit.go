package project

import (
	"github.com/google/uuid"

	auditapp "env-vault/internal/application/audit"
	auditdomain "env-vault/internal/domain/audit"
	projectdomain "env-vault/internal/domain/project"
)

const projectResourceType = "project"

func projectEvent(action, result string, project *projectdomain.Project, fallbackID uuid.UUID, fallbackName, scopeID, operator string, changes []auditdomain.Change, detail map[string]any) *auditdomain.Event {
	id := fallbackID
	name := fallbackName
	if project != nil {
		id = project.ID
		name = project.Name
	}
	resourceID := ""
	if id != uuid.Nil {
		resourceID = id.String()
	}
	if scopeID == "" {
		scopeID = resourceID
	}
	return auditapp.NewResourceEvent(auditapp.ResourceEventInput{
		ActionCode: action, ResultCode: result, ResourceType: projectResourceType,
		ResourceID: resourceID, ResourceName: name, ScopeType: projectResourceType,
		ScopeID: scopeID, Operator: operator, Changes: changes, Detail: detail,
	})
}

func projectFailure(event *auditdomain.Event, err error) *auditdomain.Event {
	return auditapp.MarkFailure(event, err, ErrInvalidParam, ErrCodeExists, ErrNotFound, ErrEnvironmentCodeDuplicated, auditapp.ErrTransactionUnavailable)
}

func projectChanges(before, after *projectdomain.Project) []auditdomain.Change {
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
