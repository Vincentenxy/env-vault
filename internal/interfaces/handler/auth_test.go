package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	authapp "env-vault/internal/application/auth"
	userdomain "env-vault/internal/domain/user"
	"env-vault/pkg/response"
)

type stubAuthService struct {
	login func(context.Context, authapp.LoginInput) (*authapp.LoginOutput, error)
}

func (s *stubAuthService) Login(ctx context.Context, input authapp.LoginInput) (*authapp.LoginOutput, error) {
	return s.login(ctx, input)
}

func newAuthTestEngine(service authapp.IService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/api/v1/pub/auth/login", NewAuthHandler(service).Login)
	return engine
}

func TestAuthHandlerLoginReturnsBearerTokenWithoutSensitiveUserData(t *testing.T) {
	service := &stubAuthService{login: func(_ context.Context, input authapp.LoginInput) (*authapp.LoginOutput, error) {
		if input.Username != "vince" || input.Password != "password" {
			t.Fatalf("unexpected input: %+v", input)
		}
		return &authapp.LoginOutput{
			AccessToken: "signed-token",
			ExpiresAt:   time.Now().Add(time.Hour),
			User:        &userdomain.User{UserID: "u-1", PasswordHash: "must-not-leak"},
		}, nil
	}}
	recorder := doJSON(t, newAuthTestEngine(service), http.MethodPost, "/api/v1/pub/auth/login", LoginRequest{
		Username: " vince ", Password: "password",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var body response.Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	encoded := recorder.Body.String()
	if body.Code != 0 || !containsAll(encoded, "signed-token", "Bearer", "expiresIn") ||
		containsAll(encoded, "must-not-leak") {
		t.Fatalf("unexpected response: %s", encoded)
	}
}

func TestAuthHandlerLoginMapsCredentialAndBlockedErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
	}{
		{name: "invalid", err: authapp.ErrInvalidCredentials, wantStatus: http.StatusUnauthorized, wantMsg: "用户名或密码错误"},
		{name: "blocked", err: authapp.ErrUserBlocked, wantStatus: http.StatusForbidden, wantMsg: "用户被锁定"},
		{name: "internal", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantMsg: "internal error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &stubAuthService{login: func(context.Context, authapp.LoginInput) (*authapp.LoginOutput, error) {
				return nil, tt.err
			}}
			recorder := doJSON(t, newAuthTestEngine(service), http.MethodPost, "/api/v1/pub/auth/login", LoginRequest{
				Username: "vince", Password: "password",
			})
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}
			var body response.Response
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Code != tt.wantStatus || body.Message != tt.wantMsg {
				t.Fatalf("unexpected response: %s", recorder.Body.String())
			}
		})
	}
}

func containsAll(value string, patterns ...string) bool {
	for _, pattern := range patterns {
		if !strings.Contains(value, pattern) {
			return false
		}
	}
	return true
}
