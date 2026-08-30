package environment

import (
	"github.com/google/uuid"

	auditapp "env-vault/internal/application/audit"
	auditdomain "env-vault/internal/domain/audit"
	envdomain "env-vault/internal/domain/environment"
)

const environmentResourceType = "environment"

func environmentEvent(action, result string, environment *envdomain.Environment, fallbackID uuid.UUID, fallbackName, projectID, operator string, changes []auditdomain.Change, detail map[string]any) *auditdomain.Event {
	id := fallbackID
	name := fallbackName
	if environment != nil {
		id = environment.ID
		name = environment.Name
		projectID = environment.ProjectID.String()
	}
	resourceID := ""
	if id != uuid.Nil {
		resourceID = id.String()
	}
	return auditapp.NewResourceEvent(auditapp.ResourceEventInput{
		ActionCode: action, ResultCode: result, ResourceType: environmentResourceType,
		ResourceID: resourceID, ResourceName: name, ScopeType: "project", ScopeID: projectID,
		Operator: operator, Changes: changes, Detail: detail,
	})
}

func environmentFailure(event *auditdomain.Event, err error) *auditdomain.Event {
	return auditapp.MarkFailure(event, err, ErrInvalidParam, ErrCodeExists, ErrCodeDuplicated, ErrNotFound, ErrCloneUnavailable, ErrInvalidFolderStructure, auditapp.ErrTransactionUnavailable)
}

func environmentChanges(before, after *envdomain.Environment) []auditdomain.Change {
	if after == nil {
		return nil
	}
	if before == nil {
		return auditapp.ChangedFields(
			auditdomain.Change{Field: "code", After: after.Code},
			auditdomain.Change{Field: "name", After: after.Name},
			auditdomain.Change{Field: "remark", After: after.Remark},
			auditdomain.Change{Field: "orderNo", After: after.OrderNo},
			auditdomain.Change{Field: "isCheckPerm", After: after.IsCheckPerm},
		)
	}
	return auditapp.ChangedFields(
		auditdomain.Change{Field: "name", Before: before.Name, After: after.Name},
		auditdomain.Change{Field: "remark", Before: before.Remark, After: after.Remark},
		auditdomain.Change{Field: "orderNo", Before: before.OrderNo, After: after.OrderNo},
		auditdomain.Change{Field: "isCheckPerm", Before: before.IsCheckPerm, After: after.IsCheckPerm},
	)
}
