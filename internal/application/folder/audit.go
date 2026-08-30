package folder

import (
	"github.com/google/uuid"

	auditapp "env-vault/internal/application/audit"
	auditdomain "env-vault/internal/domain/audit"
	folderdomain "env-vault/internal/domain/folder"
)

const folderResourceType = "folder"

func folderEvent(action, result string, folder *folderdomain.Folder, fallbackID uuid.UUID, fallbackName, projectID, operator string, changes []auditdomain.Change, detail map[string]any) *auditdomain.Event {
	id := fallbackID
	name := fallbackName
	if folder != nil {
		id = folder.GroupID
		name = folder.Name
	}
	resourceID := ""
	if id != uuid.Nil {
		resourceID = id.String()
	}
	return auditapp.NewResourceEvent(auditapp.ResourceEventInput{
		ActionCode: action, ResultCode: result, ResourceType: folderResourceType,
		ResourceID: resourceID, ResourceName: name, ScopeType: "project", ScopeID: projectID,
		Operator: operator, Changes: changes, Detail: detail,
	})
}

func folderFailure(event *auditdomain.Event, err error) *auditdomain.Event {
	return auditapp.MarkFailure(event, err, ErrInvalidParam, ErrCodeExists, ErrNotFound, ErrInvalidType, ErrCommonCodeInvalid, ErrParentNotAllowed, ErrNoEnvironment, ErrGroupsNotFound, ErrInvalidKeyPattern, auditapp.ErrTransactionUnavailable)
}

func folderChanges(before, after *folderdomain.Folder) []auditdomain.Change {
	if after == nil {
		return nil
	}
	if before == nil {
		return auditapp.ChangedFields(
			auditdomain.Change{Field: "code", After: after.Code},
			auditdomain.Change{Field: "name", After: after.Name},
			auditdomain.Change{Field: "remark", After: after.Remark},
			auditdomain.Change{Field: "type", After: after.Type},
			auditdomain.Change{Field: "manager", After: after.Manager},
			auditdomain.Change{Field: "keyPattern", After: after.KeyPattern},
		)
	}
	return auditapp.ChangedFields(
		auditdomain.Change{Field: "name", Before: before.Name, After: after.Name},
		auditdomain.Change{Field: "remark", Before: before.Remark, After: after.Remark},
		auditdomain.Change{Field: "manager", Before: before.Manager, After: after.Manager},
		auditdomain.Change{Field: "keyPattern", Before: before.KeyPattern, After: after.KeyPattern},
	)
}
