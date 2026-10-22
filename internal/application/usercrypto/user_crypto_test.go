package usercrypto

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	auditdomain "env-vault/internal/domain/audit"
	"env-vault/internal/masterkey"
)

type testCryptor struct{}

func (testCryptor) EncryptForUserWithPolicy(string, string, masterkey.DecryptPolicy) (string, error) {
	return "ciphertext", nil
}

func (testCryptor) DecryptForUser(string, string) (string, error) { return "plaintext", nil }

type stalledRecorder struct {
	err error
}

func (r *stalledRecorder) Record(ctx context.Context, event *auditdomain.Event) error {
	return r.RecordBatch(ctx, []*auditdomain.Event{event})
}

func (r *stalledRecorder) RecordBatch(ctx context.Context, _ []*auditdomain.Event) error {
	select {
	case <-ctx.Done():
		r.err = ctx.Err()
	case <-time.After(time.Minute):
		r.err = errors.New("audit was not bounded")
	}
	return r.err
}

func TestCryptoAuditTimeout(t *testing.T) {
	for _, operation := range []string{"encrypt", "decrypt"} {
		t.Run(operation, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				recorder := &stalledRecorder{}
				svc := NewService(testCryptor{}, recorder)
				start := time.Now()
				var result string
				var err error
				if operation == "encrypt" {
					result, err = svc.Encrypt(context.Background(), "user", "text", masterkey.DecryptPolicy{})
				} else {
					result, err = svc.Decrypt(context.Background(), "user", "ciphertext")
				}
				if result != "" || !errors.Is(err, ErrAuditUnavailable) {
					t.Fatal("audit timeout must fail closed without returning content")
				}
				if !errors.Is(recorder.err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
					t.Fatalf("audit exceeded 5 seconds: elapsed=%v, error=%v", time.Since(start), recorder.err)
				}
			})
		})
	}
}
