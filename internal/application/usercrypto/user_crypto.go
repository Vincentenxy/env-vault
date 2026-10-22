// Package usercrypto provides stateless encryption with authenticated access policies.
package usercrypto

import (
	"context"
	"errors"

	auditdomain "env-vault/internal/domain/audit"
	"env-vault/internal/masterkey"
)

var ErrAuditUnavailable = errors.New("crypto audit unavailable")

type Cryptor interface {
	EncryptForUserWithPolicy(userID, plaintext string, policy masterkey.DecryptPolicy) (string, error)
	DecryptForUser(userID, ciphertext string) (string, error)
}

type Service struct {
	cryptor Cryptor
	audit   auditdomain.Recorder
}

func NewService(cryptor Cryptor, recorder auditdomain.Recorder) *Service {
	return &Service{cryptor: cryptor, audit: recorder}
}

func (s *Service) Encrypt(ctx context.Context, userID, plaintext string, policy masterkey.DecryptPolicy) (string, error) {
	result, err := s.cryptor.EncryptForUserWithPolicy(userID, plaintext, policy)
	return s.finish(ctx, userID, "encrypt", len(plaintext), result, err)
}

func (s *Service) Decrypt(ctx context.Context, userID, ciphertext string) (string, error) {
	result, err := s.cryptor.DecryptForUser(userID, ciphertext)
	return s.finish(ctx, userID, "decrypt", len(ciphertext), result, err)
}

func (s *Service) finish(ctx context.Context, userID, operation string, size int, result string, operationErr error) (string, error) {
	if errors.Is(operationErr, masterkey.ErrUserRequired) {
		return "", operationErr
	}
	event := &auditdomain.Event{
		ResourceType: "user", ResourceID: userID, ScopeType: "user", ScopeID: userID,
		ActionCode: "user.crypto." + operation, ResultCode: auditdomain.ResultSuccess,
		ActorType: auditdomain.ActorTypeUser, CreateBy: userID,
		EventDetail: map[string]any{"inputBytes": size},
	}
	if operationErr != nil {
		event.ResultCode = auditdomain.ResultFailure
		event.FailureCode = "crypto_failed"
		// Never persist raw errors, caller input, or cryptographic output.
		event.FailureReason = "crypto operation failed"
	}
	// No business rows are written. Return the result only after its audit is durable.
	if s.audit == nil || s.audit.Record(ctx, event) != nil {
		return "", ErrAuditUnavailable
	}
	if operationErr != nil {
		return "", operationErr
	}
	return result, nil
}
