package auth

import (
	"errors"
	"strings"

	"github.com/google/uuid"

	auditdomain "env-vault/internal/domain/audit"
)

func loginEvent(in LoginInput, result *LoginOutput, operationErr error) *auditdomain.Event {
	username := strings.TrimSpace(in.Username)
	event := &auditdomain.Event{
		ActionCode: "auth.login", ResultCode: auditdomain.ResultSuccess,
		ActorType: auditdomain.ActorTypeAnonymous, ResourceType: "userAccount",
		ResourceID: strings.ToLower(username), ResourceName: username,
		ScopeType: "system", ScopeID: "system",
	}
	if result != nil && result.User != nil {
		user := result.User
		event.ActorType = auditdomain.ActorTypeUser
		event.ResourceType = "user"
		event.ResourceID = user.UserID
		event.ResourceName = user.Nickname
		event.CreateBy = user.UserID
		event.CreateByName = user.Nickname
		if user.TenantID != uuid.Nil {
			event.ScopeType = "tenant"
			event.ScopeID = user.TenantID.String()
		}
	}
	if operationErr == nil {
		return event
	}
	event.ResultCode = auditdomain.ResultFailure
	event.FailureCode = "internal_error"
	event.FailureReason = "internal error"
	switch {
	case errors.Is(operationErr, ErrInvalidRequest):
		event.FailureCode, event.FailureReason = "invalid_request", ErrInvalidRequest.Error()
	case errors.Is(operationErr, ErrInvalidCredentials):
		event.FailureCode, event.FailureReason = "invalid_credentials", ErrInvalidCredentials.Error()
	case errors.Is(operationErr, ErrUserBlocked):
		event.FailureCode, event.FailureReason = "user_blocked", ErrUserBlocked.Error()
	case errors.Is(operationErr, ErrRateLimited):
		event.FailureCode, event.FailureReason = "rate_limited", ErrRateLimited.Error()
	}
	return event
}
