package audit

import (
	"context"
	"errors"
	"testing"

	auditdomain "env-vault/internal/domain/audit"
)

type transactionContextKey struct{}

type stubTransactor struct {
	called bool
	err    error
}

func (s *stubTransactor) WithTx(ctx context.Context, fn func(context.Context) error) error {
	s.called = true
	s.err = fn(context.WithValue(ctx, transactionContextKey{}, true))
	return s.err
}

type stubRecorder struct {
	events          []*auditdomain.Event
	recordedInTx    []bool
	failSuccessOnce bool
}

func (s *stubRecorder) Record(ctx context.Context, event *auditdomain.Event) error {
	s.events = append(s.events, event)
	inTx, _ := ctx.Value(transactionContextKey{}).(bool)
	s.recordedInTx = append(s.recordedInTx, inTx)
	if s.failSuccessOnce && event.ResultCode == auditdomain.ResultSuccess {
		s.failSuccessOnce = false
		return errors.New("audit database unavailable")
	}
	return nil
}

func (s *stubRecorder) RecordBatch(ctx context.Context, events []*auditdomain.Event) error {
	for _, event := range events {
		if err := s.Record(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func TestRunWriteRecordsSuccessInsideTransaction(t *testing.T) {
	transactor := &stubTransactor{}
	recorder := &stubRecorder{}
	result, err := RunWrite(context.Background(), recorder, transactor, false,
		func(ctx context.Context) (string, *auditdomain.Event, error) {
			if inTx, _ := ctx.Value(transactionContextKey{}).(bool); !inTx {
				t.Fatal("work did not receive transaction context")
			}
			return "created", &auditdomain.Event{ActionCode: "tenant.create", ResultCode: auditdomain.ResultSuccess}, nil
		},
		nil,
	)
	if err != nil || result != "created" {
		t.Fatalf("RunWrite() result=%q err=%v", result, err)
	}
	if !transactor.called || len(recorder.events) != 1 || !recorder.recordedInTx[0] {
		t.Fatalf("transaction=%v events=%d recordedInTx=%v", transactor.called, len(recorder.events), recorder.recordedInTx)
	}
}

func TestRunWriteAuditFailureFailsTransactionAndRecordsFailureAttempt(t *testing.T) {
	transactor := &stubTransactor{}
	recorder := &stubRecorder{failSuccessOnce: true}
	_, err := RunWrite(context.Background(), recorder, transactor, false,
		func(context.Context) (struct{}, *auditdomain.Event, error) {
			return struct{}{}, &auditdomain.Event{ActionCode: "project.update", ResultCode: auditdomain.ResultSuccess}, nil
		},
		func(operationErr error) *auditdomain.Event {
			if operationErr == nil {
				t.Fatal("failure event did not receive transaction error")
			}
			return &auditdomain.Event{ActionCode: "project.update", ResultCode: auditdomain.ResultFailure}
		},
	)
	if err == nil || transactor.err == nil {
		t.Fatalf("expected audit error to fail transaction, err=%v txErr=%v", err, transactor.err)
	}
	if len(recorder.events) != 2 || !recorder.recordedInTx[0] || recorder.recordedInTx[1] {
		t.Fatalf("events=%d recordedInTx=%v", len(recorder.events), recorder.recordedInTx)
	}
}

func TestRunWriteBusinessFailureRecordsFailureOutsideTransaction(t *testing.T) {
	businessErr := errors.New("duplicate")
	transactor := &stubTransactor{}
	recorder := &stubRecorder{}
	_, err := RunWrite(context.Background(), recorder, transactor, false,
		func(context.Context) (struct{}, *auditdomain.Event, error) {
			return struct{}{}, nil, businessErr
		},
		func(error) *auditdomain.Event {
			return &auditdomain.Event{ActionCode: "folder.create", ResultCode: auditdomain.ResultFailure}
		},
	)
	if !errors.Is(err, businessErr) {
		t.Fatalf("RunWrite() error=%v, want business error", err)
	}
	if len(recorder.events) != 1 || recorder.recordedInTx[0] {
		t.Fatalf("events=%d recordedInTx=%v", len(recorder.events), recorder.recordedInTx)
	}
}
