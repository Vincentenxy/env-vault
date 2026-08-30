// Package audit validates, enriches, records, and queries business audit events.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"go.uber.org/zap"

	app "env-vault/internal/application"
	auditdomain "env-vault/internal/domain/audit"
	"env-vault/pkg/logger"
)

var (
	ErrInvalidParam     = errors.New("invalid audit params")
	ErrSensitivePayload = errors.New("audit payload contains sensitive data")
)

// EntryContext contains trusted adapter metadata shared by HTTP, gRPC, SDK, and jobs.
type EntryContext struct {
	EventSource    string
	EntryType      string
	CallerType     string
	CallerName     string
	CallerVersion  string
	OperationName  string
	CorrelationID  string
	TraceID        string
	ProtocolStatus string
	ProtocolDetail map[string]any
	ClientIP       string
	UserAgent      string
	ActorType      string
	ActorID        string
	ActorName      string
}

type entryContextKey struct{}

// WithEntryContext attaches trusted transport metadata to an application call.
func WithEntryContext(ctx context.Context, entry EntryContext) context.Context {
	return context.WithValue(ctx, entryContextKey{}, entry)
}

func entryFromContext(ctx context.Context) EntryContext {
	entry, _ := ctx.Value(entryContextKey{}).(EntryContext)
	return entry
}

// ListInput is the audit query application input. Pagination is normalized by the handler.
type ListInput struct {
	ResourceType string
	ResourceID   string
	ActionCode   string
	ResultCode   string
	UserID       string
	PageNum      int
	PageSize     int
}

// IService is used by the HTTP handler.
type IService interface {
	List(ctx context.Context, in ListInput) ([]*auditdomain.Event, int64, error)
}

// Service implements both the business recorder and audit query service.
type Service struct {
	repo         auditdomain.Repository
	nameResolver app.NicknameResolver
	now          func() time.Time
}

func NewService(repo auditdomain.Repository, resolver app.NicknameResolver) *Service {
	return &Service{repo: repo, nameResolver: resolver, now: time.Now}
}

var (
	_ auditdomain.Recorder = (*Service)(nil)
	_ IService             = (*Service)(nil)
)

func (s *Service) Record(ctx context.Context, event *auditdomain.Event) error {
	if event == nil {
		return ErrInvalidParam
	}
	return s.RecordBatch(ctx, []*auditdomain.Event{event})
}

func (s *Service) RecordBatch(ctx context.Context, events []*auditdomain.Event) error {
	if len(events) == 0 {
		return nil
	}

	entry := entryFromContext(ctx)
	now := s.now()
	for _, event := range events {
		if event == nil {
			return ErrInvalidParam
		}
		enrichEvent(ctx, s.nameResolver, event, entry, now)
		if err := validateEvent(event); err != nil {
			return err
		}
	}
	if err := s.repo.CreateBatch(ctx, events); err != nil {
		logger.Error(ctx, "write audit event failed", zap.Int("eventCount", len(events)), zap.Error(err))
		return fmt.Errorf("write audit event: %w", err)
	}
	return nil
}

func (s *Service) List(ctx context.Context, in ListInput) ([]*auditdomain.Event, int64, error) {
	in.ResourceType = strings.TrimSpace(in.ResourceType)
	in.ResourceID = strings.TrimSpace(in.ResourceID)
	in.ActionCode = strings.TrimSpace(in.ActionCode)
	in.ResultCode = strings.TrimSpace(in.ResultCode)
	if in.ResourceType == "" || in.ResourceID == "" || in.PageNum <= 0 || in.PageSize <= 0 {
		return nil, 0, ErrInvalidParam
	}
	if in.ResultCode != "" && in.ResultCode != auditdomain.ResultSuccess && in.ResultCode != auditdomain.ResultFailure {
		return nil, 0, ErrInvalidParam
	}
	return s.repo.List(ctx, auditdomain.ListFilter{
		ResourceType: in.ResourceType,
		ResourceID:   in.ResourceID,
		ActionCode:   in.ActionCode,
		ResultCode:   in.ResultCode,
		UserID:       strings.TrimSpace(in.UserID),
		Offset:       (in.PageNum - 1) * in.PageSize,
		Limit:        in.PageSize,
	})
}

func enrichEvent(ctx context.Context, resolver app.NicknameResolver, event *auditdomain.Event, entry EntryContext, now time.Time) {
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	if event.EventSource == "" {
		event.EventSource = firstNonEmpty(entry.EventSource, auditdomain.EventSourceServer)
	}
	if event.EntryType == "" {
		event.EntryType = firstNonEmpty(entry.EntryType, auditdomain.EntryTypeInternal)
	}
	if event.CallerType == "" {
		event.CallerType = firstNonEmpty(entry.CallerType, auditdomain.CallerTypeUnknown)
	}
	if event.CallerName == "" {
		event.CallerName = entry.CallerName
	}
	if event.CallerVersion == "" {
		event.CallerVersion = entry.CallerVersion
	}
	if event.OperationName == "" {
		event.OperationName = entry.OperationName
	}
	if event.ActorType == "" {
		event.ActorType = firstNonEmpty(entry.ActorType, auditdomain.ActorTypeUser)
	}
	if event.CreateBy == "" {
		event.CreateBy = entry.ActorID
	}
	if event.CreateByName == "" {
		event.CreateByName = firstNonEmpty(entry.ActorName, app.ResolveNickname(ctx, resolver, event.CreateBy))
	}
	if event.CorrelationID == "" {
		event.CorrelationID = entry.CorrelationID
	}
	if event.TraceID == "" {
		event.TraceID = entry.TraceID
	}
	if event.ProtocolStatus == "" {
		event.ProtocolStatus = entry.ProtocolStatus
	}
	if len(event.ProtocolDetail) == 0 {
		event.ProtocolDetail = cloneMap(entry.ProtocolDetail)
	}
	if event.ClientIP == "" {
		event.ClientIP = entry.ClientIP
	}
	if event.UserAgent == "" {
		event.UserAgent = entry.UserAgent
	}
	if event.ChangeDetail == nil {
		event.ChangeDetail = []auditdomain.Change{}
	}
	if event.EventDetail == nil {
		event.EventDetail = map[string]any{}
	}
	if event.ProtocolDetail == nil {
		event.ProtocolDetail = map[string]any{}
	}
	if event.CreateAt.IsZero() {
		event.CreateAt = now
	}
	if event.ExpireAt.IsZero() {
		event.ExpireAt = event.CreateAt.AddDate(1, 0, 0)
	}
}

func validateEvent(event *auditdomain.Event) error {
	if strings.TrimSpace(event.ActionCode) == "" || strings.TrimSpace(event.ResourceType) == "" {
		return ErrInvalidParam
	}
	if event.ResultCode != auditdomain.ResultSuccess && event.ResultCode != auditdomain.ResultFailure {
		return ErrInvalidParam
	}
	if event.ResultCode == auditdomain.ResultFailure && strings.TrimSpace(event.FailureCode) == "" {
		return ErrInvalidParam
	}
	for _, change := range event.ChangeDetail {
		field := strings.TrimSpace(change.Field)
		if field == "" {
			return ErrInvalidParam
		}
		if isSecretValueField(field) {
			if !change.Changed || !change.Redacted || change.Before != nil || change.After != nil {
				return ErrSensitivePayload
			}
		}
		if change.Redacted && (change.Before != nil || change.After != nil) {
			return ErrSensitivePayload
		}
		if err := validateJSONValue(change.Before); err != nil {
			return err
		}
		if err := validateJSONValue(change.After); err != nil {
			return err
		}
	}
	if err := validateJSONValue(event.EventDetail); err != nil {
		return err
	}
	return validateJSONValue(event.ProtocolDetail)
}

func validateJSONValue(value any) error {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ErrInvalidParam
	}
	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return ErrInvalidParam
	}
	return walkJSON(normalized)
}

func walkJSON(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if isSensitiveKey(key) {
				return ErrSensitivePayload
			}
			if err := walkJSON(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := walkJSON(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func isSecretValueField(field string) bool {
	normalized := strings.ToLower(strings.TrimSpace(field))
	return normalized == "value" || normalized == "values" || strings.HasPrefix(normalized, "values.")
}

func isSensitiveKey(key string) bool {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, key)
	for _, token := range []string{
		"password", "passwd", "pwd", "jwt", "accesstoken", "refreshtoken", "authorization",
		"cookie", "masterkey", "keyshare", "privatekey", "plaintext", "ciphertext",
		"connectionstring", "secretvalue",
	} {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return normalized == "value" || normalized == "values"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func cloneMap(source map[string]any) map[string]any {
	if len(source) == 0 {
		return map[string]any{}
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
