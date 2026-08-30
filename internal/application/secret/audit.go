package secret

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	auditdomain "env-vault/internal/domain/audit"
	secretdomain "env-vault/internal/domain/secret"
)

const (
	auditActionCreate      = "secret.create"
	auditActionUpdate      = "secret.update"
	auditActionDelete      = "secret.delete"
	auditActionRead        = "secret.read"
	auditActionList        = "secret.list"
	auditActionHistoryRead = "secret.history.read"
)

func (s *Service) recordAudit(ctx context.Context, events []*auditdomain.Event) error {
	if s.auditRecorder == nil || len(events) == 0 {
		return nil
	}
	if err := s.auditRecorder.RecordBatch(ctx, events); err != nil {
		return fmt.Errorf("record secret audit: %w", err)
	}
	return nil
}

func (s *Service) recordCreateSuccess(
	ctx context.Context,
	created []*secretdomain.Secret,
	in CreateInput,
	batchID uuid.UUID,
	operator string,
) error {
	groupOrder := make([]uuid.UUID, 0, len(in.SecretList))
	byGroup := make(map[uuid.UUID][]*secretdomain.Secret)
	for _, sec := range created {
		if _, exists := byGroup[sec.GroupID]; !exists {
			groupOrder = append(groupOrder, sec.GroupID)
		}
		byGroup[sec.GroupID] = append(byGroup[sec.GroupID], sec)
	}
	events := make([]*auditdomain.Event, 0, len(groupOrder))
	for index, groupID := range groupOrder {
		secrets := byGroup[groupID]
		if len(secrets) == 0 {
			continue
		}
		item := CreateItemInput{}
		if index < len(in.SecretList) {
			item = in.SecretList[index]
		}
		sort.Slice(secrets, func(i, j int) bool { return secrets[i].EnvCode < secrets[j].EnvCode })
		changes := []auditdomain.Change{
			{Field: "key", After: secrets[0].Key, Redacted: false},
			{Field: "remark", After: secrets[0].Remark, Redacted: false},
		}
		versions := make(map[string]any, len(secrets))
		for _, sec := range secrets {
			changes = append(changes, auditdomain.Change{
				Field: "values." + sec.EnvCode, Changed: true, Redacted: true,
			})
			versions[sec.EnvCode] = sec.Version
		}
		events = append(events, newSecretAuditEvent(
			auditActionCreate, auditdomain.ResultSuccess, groupID.String(), secrets[0].Key,
			"folder", item.FolderGroupID.String(), &batchID, operator,
			changes,
			map[string]any{
				"environmentCount": len(secrets),
				"versions":         versions,
			},
		))
	}
	return s.recordAudit(ctx, events)
}

func (s *Service) recordCreateFailure(ctx context.Context, in CreateInput, batchID uuid.UUID, operator string, operationErr error) error {
	code, reason := safeAuditFailure(operationErr)
	events := make([]*auditdomain.Event, 0, len(in.SecretList))
	for _, item := range in.SecretList {
		event := newSecretAuditEvent(
			auditActionCreate, auditdomain.ResultFailure, "", item.Key,
			"folder", item.FolderGroupID.String(), &batchID, operator, nil,
			map[string]any{"requestedEnvironmentCount": len(item.Values)},
		)
		event.FailureCode = code
		event.FailureReason = reason
		events = append(events, event)
	}
	if len(events) == 0 {
		event := newSecretAuditEvent(
			auditActionCreate, auditdomain.ResultFailure, "", "", "", "", &batchID, operator, nil, nil,
		)
		event.FailureCode = code
		event.FailureReason = reason
		events = append(events, event)
	}
	return s.recordAudit(ctx, events)
}

func (s *Service) recordUpdateFailure(ctx context.Context, in UpdateInput, batchID uuid.UUID, operator string, operationErr error) error {
	code, reason := safeAuditFailure(operationErr)
	events := make([]*auditdomain.Event, 0, len(in.Secrets))
	for _, item := range in.Secrets {
		resourceID := ""
		if item.GroupID != uuid.Nil {
			resourceID = item.GroupID.String()
		}
		event := newSecretAuditEvent(
			auditActionUpdate, auditdomain.ResultFailure, resourceID, item.Key,
			"", "", &batchID, operator, nil,
			map[string]any{"requestedEnvironmentCount": len(item.Values)},
		)
		event.FailureCode = code
		event.FailureReason = reason
		events = append(events, event)
	}
	if len(events) == 0 {
		event := newSecretAuditEvent(
			auditActionUpdate, auditdomain.ResultFailure, "", "", "", "", &batchID, operator, nil, nil,
		)
		event.FailureCode = code
		event.FailureReason = reason
		events = append(events, event)
	}
	return s.recordAudit(ctx, events)
}

func (s *Service) recordDeleteFailure(ctx context.Context, groupID uuid.UUID, key, operator string, operationErr error) error {
	code, reason := safeAuditFailure(operationErr)
	resourceID := ""
	if groupID != uuid.Nil {
		resourceID = groupID.String()
	}
	event := newSecretAuditEvent(
		auditActionDelete, auditdomain.ResultFailure, resourceID, key,
		"", "", nil, operator, nil, nil,
	)
	event.FailureCode = code
	event.FailureReason = reason
	return s.recordAudit(ctx, []*auditdomain.Event{event})
}

func (s *Service) recordReadResult(
	ctx context.Context,
	action, resourceType, resourceID, resourceName, scopeType, scopeID, operator string,
	detail map[string]any,
	operationErr error,
) error {
	result := auditdomain.ResultSuccess
	if operationErr != nil {
		result = auditdomain.ResultFailure
	}
	event := &auditdomain.Event{
		ActionCode: action, ResultCode: result, ActorType: auditdomain.ActorTypeUser,
		ResourceType: resourceType, ResourceID: resourceID, ResourceName: resourceName,
		ScopeType: scopeType, ScopeID: scopeID, CreateBy: operator, EventDetail: detail,
	}
	if operationErr != nil {
		event.FailureCode, event.FailureReason = safeAuditFailure(operationErr)
	}
	return s.recordAudit(ctx, []*auditdomain.Event{event})
}

func (s *Service) recordHistoryAudit(ctx context.Context, in HistoryInput, result *HistoryResult, operationErr error) error {
	detail := map[string]any{
		"environmentFilterCount": len(in.EnvList),
		"pageNum":                in.PageNum,
		"pageSize":               in.PageSize,
	}
	resultCode := auditdomain.ResultSuccess
	failureCode := ""
	failureReason := ""
	if operationErr != nil {
		resultCode = auditdomain.ResultFailure
		failureCode, failureReason = safeAuditFailure(operationErr)
	}

	events := make([]*auditdomain.Event, 0, 1)
	appendEvent := func(resourceType, resourceID, resourceName string, batchID *uuid.UUID) {
		event := &auditdomain.Event{
			ActionCode: auditActionHistoryRead, ResultCode: resultCode,
			ActorType: auditdomain.ActorTypeUser, ResourceType: resourceType,
			ResourceID: resourceID, ResourceName: resourceName, BatchID: batchID,
			EventDetail: detail, CreateBy: in.UserID,
			FailureCode: failureCode, FailureReason: failureReason,
		}
		events = append(events, event)
	}

	switch {
	case in.GroupID != uuid.Nil:
		appendEvent("secret", in.GroupID.String(), "", nil)
	case in.SecretID != uuid.Nil:
		if operationErr == nil && result != nil && len(result.HistoryList) > 0 {
			appendEvent("secret", result.HistoryList[0].GroupID.String(), "", nil)
		} else {
			appendEvent("secretInstance", in.SecretID.String(), "", nil)
		}
	case in.BatchID != uuid.Nil && operationErr == nil && result != nil && len(result.BatchHistories) > 0:
		for _, item := range result.BatchHistories {
			appendEvent("secret", item.GroupID.String(), item.Key, &in.BatchID)
		}
	case in.BatchID != uuid.Nil:
		appendEvent("secretBatch", in.BatchID.String(), "", &in.BatchID)
	default:
		appendEvent("secret", "", "", nil)
	}
	return s.recordAudit(ctx, events)
}

func newSecretAuditEvent(
	action, result, resourceID, resourceName, scopeType, scopeID string,
	batchID *uuid.UUID,
	operator string,
	changes []auditdomain.Change,
	detail map[string]any,
) *auditdomain.Event {
	return &auditdomain.Event{
		ActionCode: action, ResultCode: result, ActorType: auditdomain.ActorTypeUser,
		ResourceType: "secret", ResourceID: resourceID, ResourceName: strings.TrimSpace(resourceName),
		ScopeType: scopeType, ScopeID: scopeID, BatchID: batchID, ChangeDetail: changes,
		EventDetail: detail, CreateBy: operator,
	}
}

func safeAuditFailure(err error) (string, string) {
	switch {
	case errors.Is(err, ErrInvalidParam):
		return "invalid_param", ErrInvalidParam.Error()
	case errors.Is(err, ErrNotFound):
		return "secret_not_found", ErrNotFound.Error()
	case errors.Is(err, ErrFolderNotFound):
		return "folder_not_found", ErrFolderNotFound.Error()
	case errors.Is(err, ErrEnvNotFound):
		return "environment_not_found", ErrEnvNotFound.Error()
	case errors.Is(err, ErrKeyExists):
		return "secret_key_exists", ErrKeyExists.Error()
	case errors.Is(err, ErrKeyPatternMismatch):
		return "key_pattern_mismatch", ErrKeyPatternMismatch.Error()
	case errors.Is(err, ErrFolderPatternInvalid):
		return "folder_pattern_invalid", ErrFolderPatternInvalid.Error()
	case errors.Is(err, ErrDecrypt):
		return "decrypt_failed", ErrDecrypt.Error()
	case errors.Is(err, ErrSecretNotUnderGroup):
		return "secret_not_under_group", ErrSecretNotUnderGroup.Error()
	case errors.Is(err, ErrVersionConflict):
		return "version_conflict", ErrVersionConflict.Error()
	default:
		return "internal_error", "internal error"
	}
}
