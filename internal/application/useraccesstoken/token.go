// Package useraccesstoken implements personal access token use cases
package useraccesstoken

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	auditapp "env-vault/internal/application/audit"
	auditdomain "env-vault/internal/domain/audit"
	userdomain "env-vault/internal/domain/user"
	tokendomain "env-vault/internal/domain/useraccesstoken"
)

const (
	// MaxActiveTokens limits the number of usable PATs owned by one user
	MaxActiveTokens = 10
	// PersonalTokenUse is copied from the trusted authentication context
	PersonalTokenUse      = tokendomain.TokenUse
	lastUsedWriteInterval = 5 * time.Minute
)

var (
	ErrInvalidParam    = errors.New("invalid personal access token params")
	ErrNotFound        = errors.New("personal access token not found")
	ErrLimitReached    = errors.New("personal access token limit reached")
	ErrPATCannotCreate = errors.New("personal access token cannot create another token")
	ErrIssueToken      = errors.New("personal access token signing failed")
	ErrEncryptToken    = errors.New("personal access token encryption failed")
	ErrDecryptToken    = errors.New("personal access token decryption failed")
)

// CreateInput contains trusted identity metadata and user-selected token properties
type CreateInput struct {
	Name      string
	ExpiresAt time.Time
	UserID    string
	TokenUse  string
}

// DeleteInput identifies one token owned by the authenticated user
type DeleteInput struct {
	ID     uuid.UUID
	UserID string
}

// ListInput scopes the list to the authenticated user
type ListInput struct {
	UserID string
}

// TokenView contains plaintext only at the application boundary for an authorized owner
type TokenView struct {
	ID         uuid.UUID
	Name       string
	Token      string
	ExpiresAt  time.Time
	LastUsedAt *time.Time
	CreateAt   time.Time
}

type userReader interface {
	GetProfile(ctx context.Context, userID string) (*userdomain.User, error)
}

type tokenIssuer interface {
	IssuePersonalAccessToken(userID, name string, expiresAt time.Time) (string, uuid.UUID, error)
}

type tokenCryptor interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// IService exposes owner-only PAT management and middleware validation
type IService interface {
	Create(context.Context, CreateInput) (*TokenView, error)
	List(context.Context, ListInput) ([]TokenView, error)
	Delete(context.Context, DeleteInput) error
	Validate(context.Context, string, string) (bool, error)
}

// Service coordinates token persistence, signing, encryption and audit events
type Service struct {
	repo          tokendomain.Repository
	users         userReader
	issuer        tokenIssuer
	cipher        tokenCryptor
	auditRecorder auditdomain.Recorder
	now           func() time.Time
}

func NewService(repo tokendomain.Repository, users userReader, issuer tokenIssuer, cipher tokenCryptor) *Service {
	return &Service{repo: repo, users: users, issuer: issuer, cipher: cipher, now: time.Now}
}

func (s *Service) WithAuditRecorder(recorder auditdomain.Recorder) *Service {
	s.auditRecorder = recorder
	return s
}

var _ IService = (*Service)(nil)

// Create signs and stores one PAT while serializing the per-user token limit check
func (s *Service) Create(ctx context.Context, in CreateInput) (*TokenView, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.UserID = strings.TrimSpace(in.UserID)
	now := s.now()
	attempt := &tokendomain.Token{ID: uuid.New(), Name: in.Name, ExpiresAt: in.ExpiresAt, CreateBy: in.UserID}

	result, err := auditapp.RunWrite(ctx, s.auditRecorder, s.repo, true,
		func(writeCtx context.Context) (*TokenView, *auditdomain.Event, error) {
			// Validate trusted token kind and user input inside the audited operation
			if in.TokenUse == PersonalTokenUse {
				return nil, nil, ErrPATCannotCreate
			}
			if in.UserID == "" || in.Name == "" || utf8.RuneCountInString(in.Name) > 64 || !in.ExpiresAt.After(now) {
				return nil, nil, ErrInvalidParam
			}

			// Resolve the immutable internal owner ID from the authenticated external user ID
			owner, ownerErr := s.owner(writeCtx, in.UserID)
			if ownerErr != nil {
				return nil, nil, ownerErr
			}
			attempt.OwnerID = owner.ID

			// Lock the owner row before counting so concurrent creates cannot exceed the limit
			found, lockErr := s.repo.LockOwner(writeCtx, owner.ID)
			if lockErr != nil {
				return nil, nil, lockErr
			}
			if !found {
				return nil, nil, ErrNotFound
			}
			activeCount, countErr := s.repo.CountActiveByOwner(writeCtx, owner.ID, now)
			if countErr != nil {
				return nil, nil, countErr
			}
			if activeCount >= MaxActiveTokens {
				return nil, nil, ErrLimitReached
			}

			// Sign the JWT and encrypt the complete plaintext before persistence
			plaintext, jti, issueErr := s.issuer.IssuePersonalAccessToken(in.UserID, owner.Nickname, in.ExpiresAt)
			if issueErr != nil {
				return nil, nil, fmt.Errorf("%w: %v", ErrIssueToken, issueErr)
			}
			ciphertext, encryptErr := s.cipher.Encrypt(plaintext)
			if encryptErr != nil {
				return nil, nil, fmt.Errorf("%w: %v", ErrEncryptToken, encryptErr)
			}

			attempt.JTI = jti
			attempt.TokenCiphertext = ciphertext
			attempt.UpdateBy = in.UserID
			attempt.CreateAt = now
			attempt.UpdateAt = now
			if createErr := s.repo.Create(writeCtx, attempt); createErr != nil {
				return nil, nil, createErr
			}

			view := toView(attempt, plaintext)
			return &view, tokenEvent("userAccessToken.create", auditdomain.ResultSuccess, attempt, in.UserID,
				map[string]any{"jti": jti.String(), "expiresAt": in.ExpiresAt}), nil
		},
		func(operationErr error) *auditdomain.Event {
			return tokenFailure(tokenEvent("userAccessToken.create", auditdomain.ResultFailure, attempt, in.UserID, nil), operationErr)
		},
	)
	return result, err
}

// List returns all undeleted tokens and decrypts each value for its owner
func (s *Service) List(ctx context.Context, in ListInput) ([]TokenView, error) {
	in.UserID = strings.TrimSpace(in.UserID)
	return auditapp.RunRead(ctx, s.auditRecorder,
		func(readCtx context.Context) ([]TokenView, error) {
			if in.UserID == "" {
				return nil, ErrInvalidParam
			}
			owner, err := s.owner(readCtx, in.UserID)
			if err != nil {
				return nil, err
			}
			items, err := s.repo.ListByOwner(readCtx, owner.ID)
			if err != nil {
				return nil, err
			}
			views := make([]TokenView, 0, len(items))
			for _, item := range items {
				plaintext, decryptErr := s.cipher.Decrypt(item.TokenCiphertext)
				if decryptErr != nil {
					return nil, fmt.Errorf("%w: %v", ErrDecryptToken, decryptErr)
				}
				views = append(views, toView(item, plaintext))
			}
			return views, nil
		},
		func(items []TokenView, operationErr error) *auditdomain.Event {
			result := auditdomain.ResultSuccess
			if operationErr != nil {
				result = auditdomain.ResultFailure
			}
			event := tokenEvent("userAccessToken.list", result, nil, in.UserID, map[string]any{"count": len(items)})
			return tokenFailure(event, operationErr)
		},
	)
}

// Delete soft-deletes one PAT so future jti validation fails immediately
func (s *Service) Delete(ctx context.Context, in DeleteInput) error {
	in.UserID = strings.TrimSpace(in.UserID)
	_, err := auditapp.RunWrite(ctx, s.auditRecorder, s.repo, true,
		func(writeCtx context.Context) (struct{}, *auditdomain.Event, error) {
			if in.ID == uuid.Nil || in.UserID == "" {
				return struct{}{}, nil, ErrInvalidParam
			}
			owner, ownerErr := s.owner(writeCtx, in.UserID)
			if ownerErr != nil {
				return struct{}{}, nil, ownerErr
			}
			current, readErr := s.repo.GetByIDAndOwner(writeCtx, in.ID, owner.ID)
			if readErr != nil {
				return struct{}{}, nil, readErr
			}
			if current == nil {
				return struct{}{}, nil, ErrNotFound
			}
			affected, deleteErr := s.repo.SoftDelete(writeCtx, in.ID, owner.ID, in.UserID, s.now())
			if deleteErr != nil {
				return struct{}{}, nil, deleteErr
			}
			if affected != 1 {
				return struct{}{}, nil, ErrNotFound
			}
			return struct{}{}, tokenEvent("userAccessToken.delete", auditdomain.ResultSuccess, current, in.UserID,
				map[string]any{"jti": current.JTI.String()}), nil
		},
		func(operationErr error) *auditdomain.Event {
			attempt := &tokendomain.Token{ID: in.ID, CreateBy: in.UserID}
			return tokenFailure(tokenEvent("userAccessToken.delete", auditdomain.ResultFailure, attempt, in.UserID, nil), operationErr)
		},
	)
	return err
}

// Validate checks database revocation state after JWT signature validation
func (s *Service) Validate(ctx context.Context, jtiValue, userID string) (bool, error) {
	jti, err := uuid.Parse(strings.TrimSpace(jtiValue))
	if err != nil || strings.TrimSpace(userID) == "" {
		return false, nil
	}
	owner, err := s.owner(ctx, userID)
	if err != nil {
		return false, err
	}
	now := s.now()
	item, err := s.repo.GetUsableByJTI(ctx, jti, now)
	if err != nil {
		return false, err
	}
	if item == nil || item.OwnerID != owner.ID {
		return false, nil
	}

	// Refresh last-used metadata only when its five-minute write interval has elapsed
	staleBefore := now.Add(-lastUsedWriteInterval)
	if item.LastUsedAt == nil || item.LastUsedAt.Before(staleBefore) {
		if err := s.repo.TouchLastUsed(ctx, item.ID, now, staleBefore); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (s *Service) owner(ctx context.Context, userID string) (*userdomain.User, error) {
	if s.users == nil {
		return nil, ErrNotFound
	}
	user, err := s.users.GetProfile(ctx, strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}
	if user == nil || user.ID == uuid.Nil || user.IsDeleted {
		return nil, ErrNotFound
	}
	return user, nil
}

func toView(item *tokendomain.Token, plaintext string) TokenView {
	return TokenView{
		ID: item.ID, Name: item.Name, Token: plaintext, ExpiresAt: item.ExpiresAt,
		LastUsedAt: item.LastUsedAt, CreateAt: item.CreateAt,
	}
}

func tokenEvent(action, result string, token *tokendomain.Token, operator string, detail map[string]any) *auditdomain.Event {
	resourceID, resourceName, scopeID := "", "", ""
	if token != nil {
		resourceID, resourceName, scopeID = token.ID.String(), token.Name, token.OwnerID.String()
	}
	return auditapp.NewResourceEvent(auditapp.ResourceEventInput{
		ActionCode: action, ResultCode: result, ResourceType: "userAccessToken",
		ResourceID: resourceID, ResourceName: resourceName, ScopeType: "user",
		ScopeID: scopeID, Operator: operator, Detail: detail,
	})
}

func tokenFailure(event *auditdomain.Event, operationErr error) *auditdomain.Event {
	if operationErr == nil {
		return event
	}
	return auditapp.MarkFailure(event, operationErr, ErrInvalidParam, ErrNotFound, ErrLimitReached,
		ErrPATCannotCreate, ErrIssueToken, ErrEncryptToken, ErrDecryptToken, auditapp.ErrTransactionUnavailable)
}
