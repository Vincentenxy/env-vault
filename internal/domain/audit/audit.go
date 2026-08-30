// Package audit defines immutable business audit events and persistence ports.
package audit

import (
	"context"
	"time"

	"github.com/google/uuid"
)

const (
	EventSourceServer   = "server"
	EventSourceClient   = "client"
	EventSourceExternal = "external"
	EventSourceSystem   = "system"

	EntryTypeHTTP     = "http"
	EntryTypeGRPC     = "grpc"
	EntryTypeSDK      = "sdk"
	EntryTypeInternal = "internal"
	EntryTypeJob      = "job"

	CallerTypeWeb     = "web"
	CallerTypeSDK     = "sdk"
	CallerTypeService = "service"
	CallerTypeCLI     = "cli"
	CallerTypeSystem  = "system"
	CallerTypeUnknown = "unknown"

	ActorTypeUser      = "user"
	ActorTypeAnonymous = "anonymous"
	ActorTypeService   = "service"
	ActorTypeSystem    = "system"

	ResultSuccess = "success"
	ResultFailure = "failure"
)

// Change is one safe field-level change. Secret values must only use Changed + Redacted.
type Change struct {
	Field    string `json:"field"`
	Before   any    `json:"before,omitempty"`
	After    any    `json:"after,omitempty"`
	Changed  bool   `json:"changed,omitempty"`
	Redacted bool   `json:"redacted"`
}

// Event is an append-only business audit fact.
type Event struct {
	ID               uuid.UUID
	EventSource      string
	SourceEventID    *uuid.UUID
	SourceOccurredAt *time.Time
	EntryType        string
	CallerType       string
	CallerName       string
	CallerVersion    string
	OperationName    string
	ActionCode       string
	ResultCode       string
	ActorType        string
	ResourceType     string
	ResourceID       string
	ResourceName     string
	ScopeType        string
	ScopeID          string
	BatchID          *uuid.UUID
	ChangeDetail     []Change
	EventDetail      map[string]any
	FailureCode      string
	FailureReason    string
	CorrelationID    string
	TraceID          string
	ProtocolStatus   string
	ProtocolDetail   map[string]any
	ClientIP         string
	UserAgent        string
	ExpireAt         time.Time
	CreateBy         string
	CreateByName     string
	CreateAt         time.Time
}

// ListFilter carries normalized pagination and optional event filters.
type ListFilter struct {
	ResourceType string
	ResourceID   string
	ActionCode   string
	ResultCode   string
	UserID       string
	Offset       int
	Limit        int
}

// Repository is the audit persistence port.
type Repository interface {
	CreateBatch(ctx context.Context, events []*Event) error
	List(ctx context.Context, filter ListFilter) ([]*Event, int64, error)
}

// Recorder is consumed by business application services without depending on audit adapters.
type Recorder interface {
	Record(ctx context.Context, event *Event) error
	RecordBatch(ctx context.Context, events []*Event) error
}
