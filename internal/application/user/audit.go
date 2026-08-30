package user

import (
	"strings"

	"github.com/google/uuid"

	auditapp "env-vault/internal/application/audit"
	auditdomain "env-vault/internal/domain/audit"
	userdomain "env-vault/internal/domain/user"
)

const (
	userResourceType     = "user"
	userActionUpdate     = "user.update"
	userActionRead       = "user.read"
	userActionList       = "user.list"
	userActionManageList = "user.manage.list"
)

func userEvent(action, result string, user *userdomain.User, fallbackID, operator string, changes []auditdomain.Change, detail map[string]any) *auditdomain.Event {
	resourceID := strings.TrimSpace(fallbackID)
	resourceName := ""
	scopeType := "system"
	scopeID := "system"
	if user != nil {
		resourceID = user.UserID
		resourceName = user.Nickname
		if user.TenantID != uuid.Nil {
			scopeType = "tenant"
			scopeID = user.TenantID.String()
		}
	}
	return auditapp.NewResourceEvent(auditapp.ResourceEventInput{
		ActionCode: action, ResultCode: result, ResourceType: userResourceType,
		ResourceID: resourceID, ResourceName: resourceName, ScopeType: scopeType, ScopeID: scopeID,
		Operator: operator, Changes: changes, Detail: detail,
	})
}

func userFailure(event *auditdomain.Event, err error) *auditdomain.Event {
	return auditapp.MarkFailure(event, err, ErrInvalidParam, ErrNotFound, ErrUsernameExists, ErrTenantNotFound, ErrOrgNotFound, ErrProjectNotFound, auditapp.ErrTransactionUnavailable)
}

func userChanges(before, after *userdomain.User) []auditdomain.Change {
	if before == nil || after == nil {
		return nil
	}
	return auditapp.ChangedFields(
		auditdomain.Change{Field: "nickname", Before: before.Nickname, After: after.Nickname},
		auditdomain.Change{Field: "username", Before: before.Username, After: after.Username},
		auditdomain.Change{Field: "email", Before: before.Email, After: after.Email},
		auditdomain.Change{Field: "phone", Before: before.Phone, After: after.Phone},
		auditdomain.Change{Field: "tenantId", Before: before.TenantID, After: after.TenantID},
		auditdomain.Change{Field: "orgId", Before: before.OrgID, After: after.OrgID},
	)
}

func userListEvent(in ListInput, result string, count int, operationErr error) *auditdomain.Event {
	resourceID := "all"
	scopeType := "system"
	scopeID := "system"
	detail := map[string]any{"resultCount": count, "undistributed": in.Undistributed}
	switch {
	case in.ProjectID != uuid.Nil:
		resourceID, scopeType, scopeID = in.ProjectID.String(), "project", in.ProjectID.String()
		detail["projectId"] = in.ProjectID.String()
	case in.OrgID != uuid.Nil:
		resourceID, scopeType, scopeID = in.OrgID.String(), "organization", in.OrgID.String()
		detail["orgId"] = in.OrgID.String()
	case in.TenantID != uuid.Nil:
		resourceID, scopeType, scopeID = in.TenantID.String(), "tenant", in.TenantID.String()
		detail["tenantId"] = in.TenantID.String()
	case in.Undistributed:
		resourceID = "undistributed"
	}
	event := auditapp.NewResourceEvent(auditapp.ResourceEventInput{
		ActionCode: userActionList, ResultCode: result, ResourceType: "userCollection",
		ResourceID: resourceID, ScopeType: scopeType, ScopeID: scopeID, Detail: detail,
	})
	if operationErr != nil {
		return userFailure(event, operationErr)
	}
	return event
}

func userManagementListEvent(in ManagementListInput, result string, count int, operationErr error) *auditdomain.Event {
	resourceID := "all"
	scopeType := "system"
	scopeID := "system"
	if in.TenantID != uuid.Nil {
		resourceID = in.TenantID.String()
		scopeType = "tenant"
		scopeID = resourceID
	}
	event := auditapp.NewResourceEvent(auditapp.ResourceEventInput{
		ActionCode: userActionManageList, ResultCode: result, ResourceType: "userCollection",
		ResourceID: resourceID, ScopeType: scopeType, ScopeID: scopeID,
		Detail: map[string]any{
			"resultCount": count,
			"pageNum":     in.PageNum,
			"pageSize":    in.PageSize,
			"hasKeyword":  in.Keyword != "",
		},
	})
	if operationErr != nil {
		return userFailure(event, operationErr)
	}
	return event
}

func allocationEvent(in AllocateInput, result string, affected int) *auditdomain.Event {
	resourceType := string(userdomain.AllocationType(in.Type))
	if resourceType == string(userdomain.AllocationTypeOrg) {
		resourceType = "organization"
	}
	action := resourceType + ".member." + in.Operation
	resourceID := ""
	if in.ResourceID != uuid.Nil {
		resourceID = in.ResourceID.String()
	}
	changes := make([]auditdomain.Change, 0, len(in.UserIDs))
	for _, userID := range normalizeUserIDs(in.UserIDs) {
		change := auditdomain.Change{Field: "member." + userID, Changed: true}
		if in.Operation == string(userdomain.AllocationOperationAdd) {
			change.After = userID
		} else {
			change.Before = userID
		}
		changes = append(changes, change)
	}
	return auditapp.NewResourceEvent(auditapp.ResourceEventInput{
		ActionCode: action, ResultCode: result, ResourceType: resourceType,
		ResourceID: resourceID, ScopeType: resourceType, ScopeID: resourceID,
		Operator: in.Operator, Changes: changes, Detail: map[string]any{"requestedCount": len(in.UserIDs), "affectedCount": affected},
	})
}

func allocationFailure(event *auditdomain.Event, err error) *auditdomain.Event {
	return auditapp.MarkFailure(event, err, ErrInvalidParam, ErrNotFound, ErrTenantNotFound, ErrOrgNotFound, ErrProjectNotFound, auditapp.ErrTransactionUnavailable)
}
