package middleware

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	auditdomain "env-vault/internal/domain/audit"
	tokendomain "env-vault/internal/domain/useraccesstoken"
	"env-vault/internal/interfaces/auditctx"
	"env-vault/pkg/logger"
	"env-vault/pkg/response"
	"env-vault/pkg/userctx"

	"go.uber.org/zap"
)

// UserBlockChecker 查询认证用户是否已被锁定。
type UserBlockChecker interface {
	IsBlocked(ctx context.Context, userID string) (bool, error)
}

// PersonalTokenChecker validates PAT revocation and ownership after JWT verification
type PersonalTokenChecker interface {
	Validate(ctx context.Context, jti, userID string) (bool, error)
}

// JWTProvider 表示一个受信任 JWT 签发方及其验签公钥
type JWTProvider struct {
	Issuer    string
	Audience  string
	KeyID     string
	PublicKey string
}

type parsedJWTProvider struct {
	issuer    string
	audience  string
	keyID     string
	publicKey *rsa.PublicKey
}

// Auth JWT 认证中间件。
//
// 规则：
//   - 从请求头 Authorization: Bearer <token> 提取 JWT
//   - 使用配置的 RSA 公钥验签（仅允许 RS256/384/512）
//   - 解析成功后将用户信息（userId/name/jwt/cookie）写入 gin.Context
//   - JWT 认证失败返回 HTTP 401，锁定用户返回 HTTP 403，锁定状态查询异常返回 HTTP 500
//
// 每个 Provider 的 PublicKey 支持 base64 DER 或 PEM 格式。
func Auth(providers []JWTProvider, blockCheckers ...UserBlockChecker) (gin.HandlerFunc, error) {
	var blockChecker UserBlockChecker
	if len(blockCheckers) > 0 {
		blockChecker = blockCheckers[0]
	}
	return AuthWithAudit(providers, blockChecker, nil)
}

// AuthWithAudit builds the JWT middleware and persists every rejected
// authentication attempt without retaining token or credential material.
func AuthWithAudit(
	providers []JWTProvider,
	blockChecker UserBlockChecker,
	auditRecorder auditdomain.Recorder,
	personalTokenCheckers ...PersonalTokenChecker,
) (gin.HandlerFunc, error) {
	var personalTokenChecker PersonalTokenChecker
	if len(personalTokenCheckers) > 0 {
		personalTokenChecker = personalTokenCheckers[0]
	}
	parsedProviders := make([]parsedJWTProvider, 0, len(providers))
	for i, provider := range providers {
		issuer := strings.TrimSpace(provider.Issuer)
		audience := strings.TrimSpace(provider.Audience)
		keyID := strings.TrimSpace(provider.KeyID)
		if issuer == "" || audience == "" {
			return nil, fmt.Errorf("auth middleware init: provider %d issuer or audience is empty", i+1)
		}
		publicKey, err := parsePublicKey(provider.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("auth middleware init: provider %d: %w", i+1, err)
		}
		parsedProviders = append(parsedProviders, parsedJWTProvider{
			issuer: issuer, audience: audience, keyID: keyID, publicKey: publicKey,
		})
	}
	if len(parsedProviders) == 0 {
		return nil, errors.New("auth middleware init: no JWT provider configured")
	}

	return func(c *gin.Context) {
		tokenString, err := extractBearerToken(c.GetHeader("Authorization"))
		if err != nil {
			logger.Warn(c, "auth failed", zap.Error(err))
			if !recordAuthFailure(c, auditRecorder, "missing_or_invalid_authorization", "authentication failed", "") {
				return
			}
			response.AbortWithHTTPStatus(c, 401)
			return
		}

		verified, err := verifyJWT(tokenString, parsedProviders)
		if err != nil {
			logger.Warn(c, "auth failed: invalid token", zap.Error(err))
			if !recordAuthFailure(c, auditRecorder, "invalid_token", "authentication failed", "") {
				return
			}
			response.AbortWithHTTPStatus(c, 401)
			return
		}
		claims := verified.claims

		// 构建用户上下文（对应 Java createUserContext 逻辑）
		user := &userctx.User{
			UserID:     getClaimString(claims, "staffuserid"),
			Name:       getClaimString(claims, "name"),
			Jwt:        tokenString,
			AuthSource: getClaimString(claims, "authSource"),
			TokenUse:   getClaimString(claims, "tokenUse"),
			TokenID:    getClaimString(claims, "jti"),
		}
		if user.UserID == "" {
			user.UserID = getClaimString(claims, "sub")
		}
		if user.UserID == "" {
			logger.Warn(c, "auth failed: user ID claim is empty")
			if !recordAuthFailure(c, auditRecorder, "missing_user_id", "authentication failed", "") {
				return
			}
			response.AbortWithHTTPStatus(c, 401)
			return
		}
		if blockChecker != nil && user.UserID != "" {
			blocked, err := blockChecker.IsBlocked(c, user.UserID)
			if err != nil {
				logger.Error(c, "check user block status failed", zap.String("userId", user.UserID), zap.Error(err))
				if !recordAuthFailure(c, auditRecorder, "block_check_failed", "internal error", user.UserID) {
					return
				}
				response.AbortWithHTTPStatusMessage(c, 500, "internal error")
				return
			}
			if blocked {
				if !recordAuthFailure(c, auditRecorder, "user_blocked", "user is blocked", user.UserID) {
					return
				}
				response.AbortWithHTTPStatusMessage(c, 403, "用户被锁定")
				return
			}
		}

		// PATs must carry all type markers and remain active in the database
		isPersonalToken := user.AuthSource == tokendomain.AuthSource || user.TokenUse == tokendomain.TokenUse
		if isPersonalToken {
			if user.AuthSource != tokendomain.AuthSource || user.TokenUse != tokendomain.TokenUse ||
				verified.tokenType != tokendomain.TokenType || user.TokenID == "" {
				if !recordAuthFailure(c, auditRecorder, "invalid_personal_token_type", "authentication failed", user.UserID) {
					return
				}
				response.AbortWithHTTPStatus(c, 401)
				return
			}
			if personalTokenChecker == nil {
				logger.Error(c, "personal token checker is not configured")
				if !recordAuthFailure(c, auditRecorder, "personal_token_check_unavailable", "internal error", user.UserID) {
					return
				}
				response.AbortWithHTTPStatusMessage(c, 500, "internal error")
				return
			}
			active, checkErr := personalTokenChecker.Validate(c, user.TokenID, user.UserID)
			if checkErr != nil {
				logger.Error(c, "check personal token status failed", zap.String("userId", user.UserID), zap.Error(checkErr))
				if !recordAuthFailure(c, auditRecorder, "personal_token_check_failed", "internal error", user.UserID) {
					return
				}
				response.AbortWithHTTPStatusMessage(c, 500, "internal error")
				return
			}
			if !active {
				if !recordAuthFailure(c, auditRecorder, "personal_token_inactive", "authentication failed", user.UserID) {
					return
				}
				response.AbortWithHTTPStatus(c, 401)
				return
			}
		}
		userctx.Set(c, user)

		c.Next()
	}, nil
}

func recordAuthFailure(c *gin.Context, recorder auditdomain.Recorder, failureCode, failureReason, userID string) bool {
	if recorder == nil {
		return true
	}
	route := c.FullPath()
	if route == "" {
		route = c.Request.URL.Path
	}
	event := &auditdomain.Event{
		ActionCode: "auth.authorize", ResultCode: auditdomain.ResultFailure,
		ActorType: auditdomain.ActorTypeAnonymous, ResourceType: "authentication",
		ResourceID: c.Request.Method + " " + route, ResourceName: route,
		ScopeType: "system", ScopeID: "system",
		FailureCode: failureCode, FailureReason: failureReason,
	}
	if userID != "" {
		event.ActorType = auditdomain.ActorTypeUser
		event.CreateBy = userID
	}
	if err := recorder.Record(auditctx.HTTP(c), event); err != nil {
		logger.Error(c, "record authentication failure audit failed", zap.Error(err))
		response.AbortWithHTTPStatusMessage(c, 500, "internal error")
		return false
	}
	return true
}

type verifiedJWT struct {
	claims    jwt.MapClaims
	tokenType string
}

func verifyJWT(tokenString string, providers []parsedJWTProvider) (*verifiedJWT, error) {
	var lastErr error
	for _, provider := range providers {
		claims := jwt.MapClaims{}
		options := []jwt.ParserOption{
			jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
			jwt.WithIssuer(provider.issuer),
			jwt.WithAudience(provider.audience),
			jwt.WithExpirationRequired(),
			jwt.WithLeeway(30 * time.Second),
		}
		token, err := jwt.ParseWithClaims(tokenString, &claims, func(token *jwt.Token) (any, error) {
			if provider.keyID != "" && getHeaderString(token, "kid") != provider.keyID {
				return nil, errors.New("JWT key ID does not match provider")
			}
			return provider.publicKey, nil
		}, options...)
		if err == nil && token.Valid {
			return &verifiedJWT{claims: claims, tokenType: getHeaderString(token, "typ")}, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("JWT did not match a trusted provider")
	}
	return nil, lastErr
}

func getHeaderString(token *jwt.Token, key string) string {
	if value, ok := token.Header[key].(string); ok {
		return value
	}
	return ""
}

// extractBearerToken 从 Authorization 头提取 Bearer token
func extractBearerToken(header string) (string, error) {
	if header == "" {
		return "", errors.New("missing Authorization header")
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", errors.New("invalid Authorization header format, expect: Bearer <token>")
	}
	return parts[1], nil
}

// parsePublicKey 解析配置中的公钥，兼容 base64(DER) 与 PEM 两种格式
func parsePublicKey(key string) (*rsa.PublicKey, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("JWT public key is empty")
	}

	// PEM 格式（包含 BEGIN 标记）
	if strings.Contains(key, "-----BEGIN") {
		return jwt.ParseRSAPublicKeyFromPEM([]byte(key))
	}

	// base64 编码的 DER 格式（Java 侧常见输出）
	der, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return nil, fmt.Errorf("decode base64 public key: %w", err)
	}
	return jwt.ParseRSAPublicKeyFromPEM(wrapPEM(der))
}

// wrapPEM 将 DER 公钥包装为 PEM 块
func wrapPEM(der []byte) []byte {
	encoded := base64.StdEncoding.EncodeToString(der)
	var sb strings.Builder
	sb.WriteString("-----BEGIN PUBLIC KEY-----\n")
	for i := 0; i < len(encoded); i += 64 {
		end := i + 64
		if end > len(encoded) {
			end = len(encoded)
		}
		sb.WriteString(encoded[i:end])
		sb.WriteString("\n")
	}
	sb.WriteString("-----END PUBLIC KEY-----\n")
	return []byte(sb.String())
}

// getClaimString 安全地从 claims 中获取字符串值
func getClaimString(claims jwt.MapClaims, key string) string {
	if v, ok := claims[key].(string); ok {
		return v
	}
	return ""
}
