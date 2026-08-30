// Package audit persists append-only audit events in PostgreSQL.
package audit

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	auditdomain "env-vault/internal/domain/audit"
	"env-vault/internal/infrastructure/persistence"
)

type jsonDocument []byte

func (j jsonDocument) Value() (driver.Value, error) {
	if len(j) == 0 {
		return "{}", nil
	}
	return string(j), nil
}

func (j *jsonDocument) Scan(value any) error {
	switch typed := value.(type) {
	case nil:
		*j = nil
	case []byte:
		*j = append((*j)[:0], typed...)
	case string:
		*j = append((*j)[:0], typed...)
	default:
		return fmt.Errorf("scan audit json from %T", value)
	}
	return nil
}

type auditEventPO struct {
	ID               uuid.UUID    `gorm:"column:id;primaryKey"`
	EventSource      string       `gorm:"column:event_source"`
	SourceEventID    *uuid.UUID   `gorm:"column:source_event_id"`
	SourceOccurredAt *time.Time   `gorm:"column:source_occurred_at"`
	EntryType        string       `gorm:"column:entry_type"`
	CallerType       string       `gorm:"column:caller_type"`
	CallerName       string       `gorm:"column:caller_name"`
	CallerVersion    string       `gorm:"column:caller_version"`
	OperationName    string       `gorm:"column:operation_name"`
	ActionCode       string       `gorm:"column:action_code"`
	ResultCode       string       `gorm:"column:result_code"`
	ActorType        string       `gorm:"column:actor_type"`
	ResourceType     string       `gorm:"column:resource_type"`
	ResourceID       string       `gorm:"column:resource_id"`
	ResourceName     string       `gorm:"column:resource_name"`
	ScopeType        string       `gorm:"column:scope_type"`
	ScopeID          string       `gorm:"column:scope_id"`
	BatchID          *uuid.UUID   `gorm:"column:batch_id"`
	ChangeDetail     jsonDocument `gorm:"column:change_detail;type:jsonb"`
	EventDetail      jsonDocument `gorm:"column:event_detail;type:jsonb"`
	FailureCode      string       `gorm:"column:failure_code"`
	FailureReason    string       `gorm:"column:failure_reason"`
	CorrelationID    string       `gorm:"column:correlation_id"`
	TraceID          string       `gorm:"column:trace_id"`
	ProtocolStatus   string       `gorm:"column:protocol_status"`
	ProtocolDetail   jsonDocument `gorm:"column:protocol_detail;type:jsonb"`
	ClientIP         string       `gorm:"column:client_ip"`
	UserAgent        string       `gorm:"column:user_agent"`
	ExpireAt         time.Time    `gorm:"column:expire_at"`
	CreateBy         string       `gorm:"column:create_by"`
	CreateByName     string       `gorm:"column:create_by_name"`
	CreateAt         time.Time    `gorm:"column:create_at"`
}

func (auditEventPO) TableName() string { return "audit_event_log" }

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

var _ auditdomain.Repository = (*Repository)(nil)

func (r *Repository) CreateBatch(ctx context.Context, events []*auditdomain.Event) error {
	if len(events) == 0 {
		return nil
	}
	pos := make([]auditEventPO, 0, len(events))
	for _, event := range events {
		po, err := toPO(event)
		if err != nil {
			return err
		}
		pos = append(pos, *po)
	}
	return persistence.TxDB(ctx, r.db).WithContext(ctx).Create(&pos).Error
}

func (r *Repository) List(ctx context.Context, filter auditdomain.ListFilter) ([]*auditdomain.Event, int64, error) {
	db := persistence.TxDB(ctx, r.db).WithContext(ctx).
		Model(&auditEventPO{}).
		Where("resource_type = ? AND resource_id = ?", filter.ResourceType, filter.ResourceID)
	if filter.ActionCode != "" {
		db = db.Where("action_code = ?", filter.ActionCode)
	}
	if filter.ResultCode != "" {
		db = db.Where("result_code = ?", filter.ResultCode)
	}
	db = applyPermissionFilter(db, filter.UserID)

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var pos []auditEventPO
	if err := db.Order("create_at DESC, id DESC").Offset(filter.Offset).Limit(filter.Limit).Find(&pos).Error; err != nil {
		return nil, 0, err
	}
	events := make([]*auditdomain.Event, 0, len(pos))
	for i := range pos {
		event, err := toDomain(&pos[i])
		if err != nil {
			return nil, 0, err
		}
		events = append(events, event)
	}
	return events, total, nil
}

// applyPermissionFilter is the reserved resource-manager permission boundary.
func applyPermissionFilter(db *gorm.DB, _ string) *gorm.DB { return db }

func toPO(event *auditdomain.Event) (*auditEventPO, error) {
	if event == nil {
		return nil, errors.New("nil audit event")
	}
	changes, err := json.Marshal(event.ChangeDetail)
	if err != nil {
		return nil, fmt.Errorf("marshal audit changes: %w", err)
	}
	detail, err := json.Marshal(event.EventDetail)
	if err != nil {
		return nil, fmt.Errorf("marshal audit detail: %w", err)
	}
	protocol, err := json.Marshal(event.ProtocolDetail)
	if err != nil {
		return nil, fmt.Errorf("marshal audit protocol detail: %w", err)
	}
	return &auditEventPO{
		ID: event.ID, EventSource: event.EventSource, SourceEventID: event.SourceEventID,
		SourceOccurredAt: event.SourceOccurredAt, EntryType: event.EntryType, CallerType: event.CallerType,
		CallerName: event.CallerName, CallerVersion: event.CallerVersion, OperationName: event.OperationName,
		ActionCode: event.ActionCode, ResultCode: event.ResultCode, ActorType: event.ActorType,
		ResourceType: event.ResourceType, ResourceID: event.ResourceID, ResourceName: event.ResourceName,
		ScopeType: event.ScopeType, ScopeID: event.ScopeID, BatchID: event.BatchID,
		ChangeDetail: changes, EventDetail: detail, FailureCode: event.FailureCode,
		FailureReason: event.FailureReason, CorrelationID: event.CorrelationID, TraceID: event.TraceID,
		ProtocolStatus: event.ProtocolStatus, ProtocolDetail: protocol, ClientIP: event.ClientIP,
		UserAgent: event.UserAgent, ExpireAt: event.ExpireAt, CreateBy: event.CreateBy,
		CreateByName: event.CreateByName, CreateAt: event.CreateAt,
	}, nil
}

func toDomain(po *auditEventPO) (*auditdomain.Event, error) {
	var changes []auditdomain.Change
	if len(po.ChangeDetail) > 0 {
		if err := json.Unmarshal(po.ChangeDetail, &changes); err != nil {
			return nil, fmt.Errorf("unmarshal audit changes: %w", err)
		}
	}
	if changes == nil {
		changes = []auditdomain.Change{}
	}
	detail := map[string]any{}
	if len(po.EventDetail) > 0 {
		if err := json.Unmarshal(po.EventDetail, &detail); err != nil {
			return nil, fmt.Errorf("unmarshal audit detail: %w", err)
		}
	}
	protocol := map[string]any{}
	if len(po.ProtocolDetail) > 0 {
		if err := json.Unmarshal(po.ProtocolDetail, &protocol); err != nil {
			return nil, fmt.Errorf("unmarshal audit protocol detail: %w", err)
		}
	}
	return &auditdomain.Event{
		ID: po.ID, EventSource: po.EventSource, SourceEventID: po.SourceEventID,
		SourceOccurredAt: po.SourceOccurredAt, EntryType: po.EntryType, CallerType: po.CallerType,
		CallerName: po.CallerName, CallerVersion: po.CallerVersion, OperationName: po.OperationName,
		ActionCode: po.ActionCode, ResultCode: po.ResultCode, ActorType: po.ActorType,
		ResourceType: po.ResourceType, ResourceID: po.ResourceID, ResourceName: po.ResourceName,
		ScopeType: po.ScopeType, ScopeID: po.ScopeID, BatchID: po.BatchID, ChangeDetail: changes,
		EventDetail: detail, FailureCode: po.FailureCode, FailureReason: po.FailureReason,
		CorrelationID: po.CorrelationID, TraceID: po.TraceID, ProtocolStatus: po.ProtocolStatus,
		ProtocolDetail: protocol, ClientIP: po.ClientIP, UserAgent: po.UserAgent,
		ExpireAt: po.ExpireAt, CreateBy: po.CreateBy, CreateByName: po.CreateByName, CreateAt: po.CreateAt,
	}, nil
}
