package audit

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	auditdomain "env-vault/internal/domain/audit"
)

var ErrTransactionUnavailable = errors.New("audit transaction unavailable")

// Transactor lets application services commit a business write and its success
// audit event atomically. Persistence adapters pass the transaction through ctx.
type Transactor interface {
	WithTx(ctx context.Context, fn func(context.Context) error) error
}

// ResourceEventInput contains the common, non-sensitive identity of an audit event.
type ResourceEventInput struct {
	ActionCode   string
	ResultCode   string
	ResourceType string
	ResourceID   string
	ResourceName string
	ScopeType    string
	ScopeID      string
	Operator     string
	Changes      []auditdomain.Change
	Detail       map[string]any
}

// NewResourceEvent builds a resource audit event. Transport and actor metadata are
// enriched by Service.Record from the trusted entry context.
func NewResourceEvent(in ResourceEventInput) *auditdomain.Event {
	return &auditdomain.Event{
		ActionCode: strings.TrimSpace(in.ActionCode), ResultCode: in.ResultCode,
		ActorType: auditdomain.ActorTypeUser, ResourceType: strings.TrimSpace(in.ResourceType),
		ResourceID: strings.TrimSpace(in.ResourceID), ResourceName: strings.TrimSpace(in.ResourceName),
		ScopeType: strings.TrimSpace(in.ScopeType), ScopeID: strings.TrimSpace(in.ScopeID),
		CreateBy: strings.TrimSpace(in.Operator), ChangeDetail: in.Changes, EventDetail: in.Detail,
	}
}

// ChangedFields returns only fields whose before/after values differ.
func ChangedFields(values ...auditdomain.Change) []auditdomain.Change {
	changes := make([]auditdomain.Change, 0, len(values))
	for _, change := range values {
		if reflect.DeepEqual(change.Before, change.After) {
			continue
		}
		change.Changed = true
		changes = append(changes, change)
	}
	return changes
}

// MarkFailure adds a safe business error to an event. Unknown errors are never
// copied into the audit payload because they may contain SQL or infrastructure data.
func MarkFailure(event *auditdomain.Event, operationErr error, knownErrors ...error) *auditdomain.Event {
	if event == nil {
		return nil
	}
	event.ResultCode = auditdomain.ResultFailure
	event.FailureCode = "internal_error"
	event.FailureReason = "internal error"
	for _, known := range knownErrors {
		if known != nil && errors.Is(operationErr, known) {
			event.FailureCode = "business_error"
			event.FailureReason = known.Error()
			break
		}
	}
	return event
}

// RunWrite executes a resource mutation. When auditing is enabled, the business
// write and success event share one database transaction; a failed attempt is
// recorded separately after the business transaction rolls back.
func RunWrite[T any](
	ctx context.Context,
	recorder auditdomain.Recorder,
	transactor Transactor,
	requireTransaction bool,
	work func(context.Context) (T, *auditdomain.Event, error),
	failure func(error) *auditdomain.Event,
) (result T, resultErr error) {
	transactionRequired := requireTransaction || recorder != nil
	run := func(workCtx context.Context) error {
		var event *auditdomain.Event
		result, event, resultErr = work(workCtx)
		if resultErr != nil {
			return resultErr
		}
		if recorder != nil && event != nil {
			if err := recorder.Record(workCtx, event); err != nil {
				return fmt.Errorf("record success audit: %w", err)
			}
		}
		return nil
	}

	if transactionRequired {
		if transactor == nil {
			resultErr = ErrTransactionUnavailable
		} else {
			resultErr = transactor.WithTx(ctx, run)
		}
	} else {
		resultErr = run(ctx)
	}

	if resultErr != nil && recorder != nil && failure != nil {
		if event := failure(resultErr); event != nil {
			if auditErr := recorder.Record(ctx, event); auditErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("record failure audit: %w", auditErr))
			}
		}
	}
	return result, resultErr
}

// RunWriteBatch is RunWrite's multi-event variant for one request that mutates
// several independently addressable resources, such as batch environment creation.
func RunWriteBatch[T any](
	ctx context.Context,
	recorder auditdomain.Recorder,
	transactor Transactor,
	requireTransaction bool,
	work func(context.Context) (T, []*auditdomain.Event, error),
	failure func(error) []*auditdomain.Event,
) (result T, resultErr error) {
	transactionRequired := requireTransaction || recorder != nil
	run := func(workCtx context.Context) error {
		var events []*auditdomain.Event
		result, events, resultErr = work(workCtx)
		if resultErr != nil {
			return resultErr
		}
		if recorder != nil && len(events) > 0 {
			if err := recorder.RecordBatch(workCtx, events); err != nil {
				return fmt.Errorf("record success audit: %w", err)
			}
		}
		return nil
	}
	if transactionRequired {
		if transactor == nil {
			resultErr = ErrTransactionUnavailable
		} else {
			resultErr = transactor.WithTx(ctx, run)
		}
	} else {
		resultErr = run(ctx)
	}
	if resultErr != nil && recorder != nil && failure != nil {
		if events := failure(resultErr); len(events) > 0 {
			if auditErr := recorder.RecordBatch(ctx, events); auditErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("record failure audit: %w", auditErr))
			}
		}
	}
	return result, resultErr
}

// RunRead records both successful and failed resource reads before returning.
func RunRead[T any](
	ctx context.Context,
	recorder auditdomain.Recorder,
	work func(context.Context) (T, error),
	event func(T, error) *auditdomain.Event,
) (result T, resultErr error) {
	result, resultErr = work(ctx)
	if recorder == nil || event == nil {
		return result, resultErr
	}
	auditEvent := event(result, resultErr)
	if auditEvent == nil {
		return result, resultErr
	}
	if auditErr := recorder.Record(ctx, auditEvent); auditErr != nil {
		resultErr = errors.Join(resultErr, fmt.Errorf("record read audit: %w", auditErr))
	}
	return result, resultErr
}
