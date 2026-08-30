package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	authapp "env-vault/internal/application/auth"
	"env-vault/pkg/logger"
	"env-vault/pkg/response"
)

const maxLoginRequestBodySize = 8 * 1024

// AuthHandler 提供系统本地认证接口
type AuthHandler struct {
	svc     authapp.IService
	limiter *loginLimiter
}

type loginFailureRecorder interface {
	RecordFailure(ctx context.Context, in authapp.LoginInput, operationErr error) error
}

// NewAuthHandler 创建本地认证处理器
func NewAuthHandler(svc authapp.IService) *AuthHandler {
	return &AuthHandler{svc: svc, limiter: newLoginLimiter(10, time.Minute)}
}

// LoginRequest 本地用户名密码登录请求
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse 本地登录成功响应
type LoginResponse struct {
	AccessToken string `json:"accessToken"`
	TokenType   string `json:"tokenType"`
	ExpiresIn   int64  `json:"expiresIn"`
}

// Login 使用全局用户名和密码签发 EnvVault JWT
// @Summary 本地用户登录
// @Tags auth
// @Accept json
// @Produce json
// @Param request body LoginRequest true "登录参数"
// @Success 200 {object} response.Response{data=LoginResponse}
// @Failure 401 {object} response.Response
// @Failure 403 {object} response.Response
// @Failure 429 {object} response.Response
// @Router /api/v1/pub/auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxLoginRequestBodySize)
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		if !h.recordRejected(c, req.Username, authapp.ErrInvalidRequest) {
			return
		}
		response.BadRequest(c, errors.New("username and password are required"))
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" || req.Password == "" || len(username) > 128 || len([]byte(req.Password)) > 1024 {
		if !h.recordRejected(c, username, authapp.ErrInvalidRequest) {
			return
		}
		response.BadRequest(c, errors.New("username or password format is invalid"))
		return
	}

	limitKey := c.ClientIP() + "\x00" + strings.ToLower(username)
	if !h.limiter.Allow(limitKey) {
		logger.Warn(c, "local login rate limited", zap.String("username", username))
		if !h.recordRejected(c, username, authapp.ErrRateLimited) {
			return
		}
		response.AbortWithHTTPStatusMessage(c, http.StatusTooManyRequests, "登录尝试过于频繁")
		return
	}

	result, err := h.svc.Login(withHTTPAuditContext(c), authapp.LoginInput{Username: username, Password: req.Password})
	req.Password = ""
	if err != nil {
		switch {
		case errors.Is(err, authapp.ErrInvalidCredentials):
			logger.Warn(c, "local login failed", zap.String("username", username), zap.String("result", "invalid_credentials"))
			response.AbortWithHTTPStatusMessage(c, http.StatusUnauthorized, "用户名或密码错误")
		case errors.Is(err, authapp.ErrUserBlocked):
			logger.Warn(c, "local login failed", zap.String("username", username), zap.String("result", "blocked"))
			response.AbortWithHTTPStatusMessage(c, http.StatusForbidden, "用户被锁定")
		default:
			logger.Error(c, "local login failed", zap.String("username", username), zap.String("result", "internal_error"), zap.Error(err))
			response.AbortWithHTTPStatusMessage(c, http.StatusInternalServerError, "internal error")
		}
		return
	}

	h.limiter.Reset(limitKey)
	logger.Info(c, "local login succeeded", zap.String("userId", result.User.UserID))
	expiresIn := max(int64(time.Until(result.ExpiresAt).Seconds()), 0)
	response.Success(c, LoginResponse{AccessToken: result.AccessToken, TokenType: "Bearer", ExpiresIn: expiresIn})
}

func (h *AuthHandler) recordRejected(c *gin.Context, username string, operationErr error) bool {
	recorder, ok := h.svc.(loginFailureRecorder)
	if !ok {
		return true
	}
	if err := recorder.RecordFailure(withHTTPAuditContext(c), authapp.LoginInput{Username: username}, operationErr); err != nil {
		logger.Error(c, "record rejected login audit failed", zap.Error(err))
		response.AbortWithHTTPStatusMessage(c, http.StatusInternalServerError, "internal error")
		return false
	}
	return true
}

type loginAttempt struct {
	count       int
	windowStart time.Time
}

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
	max      int
	window   time.Duration
}

func newLoginLimiter(maxAttempts int, window time.Duration) *loginLimiter {
	return &loginLimiter{attempts: make(map[string]loginAttempt), max: maxAttempts, window: window}
}

func (l *loginLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.attempts) > 10_000 {
		for attemptKey, attempt := range l.attempts {
			if now.Sub(attempt.windowStart) >= l.window {
				delete(l.attempts, attemptKey)
			}
		}
	}
	attempt := l.attempts[key]
	if attempt.windowStart.IsZero() || now.Sub(attempt.windowStart) >= l.window {
		attempt = loginAttempt{windowStart: now}
	}
	attempt.count++
	l.attempts[key] = attempt
	return attempt.count <= l.max
}

func (l *loginLimiter) Reset(key string) {
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}
