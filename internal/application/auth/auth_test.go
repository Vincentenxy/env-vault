package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	auditdomain "env-vault/internal/domain/audit"
	userdomain "env-vault/internal/domain/user"
)

type stubAuditRecorder struct {
	events []*auditdomain.Event
}

func (s *stubAuditRecorder) Record(_ context.Context, event *auditdomain.Event) error {
	s.events = append(s.events, event)
	return nil
}

func (s *stubAuditRecorder) RecordBatch(ctx context.Context, events []*auditdomain.Event) error {
	for _, event := range events {
		if err := s.Record(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

type stubUserRepository struct {
	getByUsername func(context.Context, string) (*userdomain.User, error)
}

func (s *stubUserRepository) UpdateByUserID(context.Context, *userdomain.User) error { return nil }
func (s *stubUserRepository) UpdateManagement(context.Context, userdomain.ManagementUpdate) error {
	return nil
}
func (s *stubUserRepository) GetByUserID(context.Context, string) (*userdomain.User, error) {
	return nil, nil
}
func (s *stubUserRepository) GetByUsername(ctx context.Context, username string) (*userdomain.User, error) {
	if s.getByUsername != nil {
		return s.getByUsername(ctx, username)
	}
	return nil, nil
}
func (s *stubUserRepository) UpdatePasswordHashByUsername(context.Context, string, string, string) error {
	return nil
}
func (s *stubUserRepository) List(context.Context, userdomain.ListFilter) ([]*userdomain.User, error) {
	return nil, nil
}
func (s *stubUserRepository) ListManagement(context.Context, userdomain.ManagementListFilter) ([]*userdomain.ManagementUser, int64, error) {
	return nil, 0, nil
}
func (s *stubUserRepository) ListAll(context.Context) ([]*userdomain.User, error) { return nil, nil }
func (s *stubUserRepository) Allocate(context.Context, userdomain.AllocationChange) ([]*userdomain.User, []string, error) {
	return nil, nil, nil
}

type stubPasswordVerifier struct {
	matched bool
	hash    string
}

func (s *stubPasswordVerifier) Verify(_ string, hash string) (bool, error) {
	s.hash = hash
	return s.matched, nil
}

type stubTokenIssuer struct {
	userID string
}

func (s *stubTokenIssuer) Issue(userID, _ string) (string, time.Time, error) {
	s.userID = userID
	return "signed-token", time.Now().Add(time.Hour), nil
}

func TestServiceLoginIssuesTokenForValidUser(t *testing.T) {
	user := &userdomain.User{ID: uuid.New(), UserID: "u-1", Username: "Vince", Nickname: "Vince", PasswordHash: "encoded"}
	repo := &stubUserRepository{getByUsername: func(_ context.Context, username string) (*userdomain.User, error) {
		if username != "Vince" {
			t.Fatalf("username = %q", username)
		}
		return user, nil
	}}
	password := &stubPasswordVerifier{matched: true}
	tokens := &stubTokenIssuer{}
	result, err := NewService(repo, password, tokens).Login(context.Background(), LoginInput{
		Username: " Vince ", Password: "password",
	})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if result.AccessToken != "signed-token" || result.User != user || tokens.userID != "u-1" || password.hash != "encoded" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestServiceLoginDoesNotRevealMissingOrDisabledUser(t *testing.T) {
	tests := []struct {
		name string
		user *userdomain.User
	}{
		{name: "missing"},
		{name: "password disabled", user: &userdomain.User{UserID: "u-1", PasswordHash: ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			password := &stubPasswordVerifier{matched: false}
			repo := &stubUserRepository{getByUsername: func(context.Context, string) (*userdomain.User, error) {
				return tt.user, nil
			}}
			_, err := NewService(repo, password, &stubTokenIssuer{}).Login(context.Background(), LoginInput{Username: "user", Password: "wrong"})
			if !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("error = %v", err)
			}
			if tt.user == nil && password.hash != "" {
				t.Fatalf("missing user must use empty dummy hash marker, got %q", password.hash)
			}
		})
	}
}

func TestServiceLoginRejectsBlockedUser(t *testing.T) {
	user := &userdomain.User{UserID: "u-1", PasswordHash: "encoded", IsBlocked: true}
	repo := &stubUserRepository{getByUsername: func(context.Context, string) (*userdomain.User, error) { return user, nil }}
	_, err := NewService(repo, &stubPasswordVerifier{matched: true}, &stubTokenIssuer{}).Login(
		context.Background(), LoginInput{Username: "user", Password: "password"},
	)
	if !errors.Is(err, ErrUserBlocked) {
		t.Fatalf("error = %v", err)
	}
}

func TestServiceLoginAuditDoesNotContainPasswordOrToken(t *testing.T) {
	user := &userdomain.User{UserID: "u-1", Username: "vince", Nickname: "Vince", PasswordHash: "encoded"}
	repo := &stubUserRepository{getByUsername: func(context.Context, string) (*userdomain.User, error) { return user, nil }}
	recorder := &stubAuditRecorder{}
	result, err := NewService(repo, &stubPasswordVerifier{matched: true}, &stubTokenIssuer{}).
		WithAuditRecorder(recorder).
		Login(context.Background(), LoginInput{Username: "vince", Password: "password-must-not-leak"})
	if err != nil || result.AccessToken != "signed-token" {
		t.Fatalf("Login() result=%+v err=%v", result, err)
	}
	if len(recorder.events) != 1 || recorder.events[0].ActionCode != "auth.login" || recorder.events[0].ResourceID != "u-1" {
		t.Fatalf("unexpected audit events: %+v", recorder.events)
	}
	encoded, err := json.Marshal(recorder.events[0])
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	payload := string(encoded)
	if strings.Contains(payload, "password-must-not-leak") || strings.Contains(payload, "signed-token") || strings.Contains(payload, "encoded") {
		t.Fatalf("login secret leaked into audit event: %s", payload)
	}
}
