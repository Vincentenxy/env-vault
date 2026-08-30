// Package auth 提供系统本地用户名密码认证用例
package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	auditapp "env-vault/internal/application/audit"
	auditdomain "env-vault/internal/domain/audit"
	userdomain "env-vault/internal/domain/user"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrUserBlocked        = errors.New("user is blocked")
	ErrInvalidRequest     = errors.New("invalid login request")
	ErrRateLimited        = errors.New("login rate limited")
)

// PasswordVerifier 验证不可逆密码哈希
type PasswordVerifier interface {
	Verify(password, encodedHash string) (bool, error)
}

// TokenIssuer 为认证成功用户签发访问令牌
type TokenIssuer interface {
	Issue(userID, name string) (token string, expiresAt time.Time, err error)
}

// LoginInput 本地登录入参
type LoginInput struct {
	Username string
	Password string
}

// LoginOutput 本地登录结果
type LoginOutput struct {
	AccessToken string
	ExpiresAt   time.Time
	User        *userdomain.User
}

// IService 本地认证应用服务
type IService interface {
	Login(ctx context.Context, in LoginInput) (*LoginOutput, error)
}

// Service 本地认证应用服务实现
type Service struct {
	users         userdomain.Repository
	password      PasswordVerifier
	tokens        TokenIssuer
	auditRecorder auditdomain.Recorder
}

// NewService 创建本地认证服务
func NewService(users userdomain.Repository, password PasswordVerifier, tokens TokenIssuer) *Service {
	return &Service{users: users, password: password, tokens: tokens}
}

// WithAuditRecorder enables mandatory login attempt auditing.
func (s *Service) WithAuditRecorder(recorder auditdomain.Recorder) *Service {
	s.auditRecorder = recorder
	return s
}

var _ IService = (*Service)(nil)

// Login 校验全局用户名和密码，并为未锁定用户签发本地 JWT
func (s *Service) Login(ctx context.Context, in LoginInput) (*LoginOutput, error) {
	return auditapp.RunRead(ctx, s.auditRecorder,
		func(readCtx context.Context) (*LoginOutput, error) {
			return s.login(readCtx, in)
		},
		func(result *LoginOutput, err error) *auditdomain.Event {
			return loginEvent(in, result, err)
		},
	)
}

func (s *Service) login(ctx context.Context, in LoginInput) (*LoginOutput, error) {
	username := strings.TrimSpace(in.Username)
	if username == "" || in.Password == "" {
		return nil, ErrInvalidCredentials
	}

	user, err := s.users.GetByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	hash := ""
	if user != nil {
		hash = user.PasswordHash
	}
	matched, err := s.password.Verify(in.Password, hash)
	if err != nil {
		return nil, err
	}
	if user == nil || !matched {
		return nil, ErrInvalidCredentials
	}
	if user.IsBlocked {
		return nil, ErrUserBlocked
	}

	token, expiresAt, err := s.tokens.Issue(user.UserID, user.Nickname)
	if err != nil {
		return nil, err
	}
	return &LoginOutput{AccessToken: token, ExpiresAt: expiresAt, User: user}, nil
}

// RecordFailure records login attempts rejected by the HTTP adapter before the
// credential service runs, such as malformed or rate-limited requests.
func (s *Service) RecordFailure(ctx context.Context, in LoginInput, operationErr error) error {
	if s.auditRecorder == nil {
		return nil
	}
	if err := s.auditRecorder.Record(ctx, loginEvent(in, nil, operationErr)); err != nil {
		return fmt.Errorf("record login failure audit: %w", err)
	}
	return nil
}
