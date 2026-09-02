package auth

import (
	"crypto/rsa"
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	tokendomain "env-vault/internal/domain/useraccesstoken"
)

// JWTIssuer 使用 EnvVault 本地 RSA 私钥签发短期访问令牌
type JWTIssuer struct {
	privateKey *rsa.PrivateKey
	issuer     string
	audience   string
	keyID      string
	ttl        time.Duration
	now        func() time.Time
}

// NewJWTIssuer 创建本地 JWT 签发器
func NewJWTIssuer(privateKey *rsa.PrivateKey, issuer, audience, keyID string, ttl time.Duration) (*JWTIssuer, error) {
	issuer = strings.TrimSpace(issuer)
	audience = strings.TrimSpace(audience)
	keyID = strings.TrimSpace(keyID)
	if privateKey == nil || issuer == "" || audience == "" || keyID == "" || ttl <= 0 {
		return nil, errors.New("local JWT issuer config is invalid")
	}
	return &JWTIssuer{
		privateKey: privateKey,
		issuer:     issuer,
		audience:   audience,
		keyID:      keyID,
		ttl:        ttl,
		now:        time.Now,
	}, nil
}

type localClaims struct {
	StaffUserID string `json:"staffuserid"`
	Name        string `json:"name"`
	AuthSource  string `json:"authSource"`
	TokenUse    string `json:"tokenUse"`
	jwt.RegisteredClaims
}

const (
	// PersonalTokenAuthSource identifies a JWT issued for personal API access
	PersonalTokenAuthSource = tokendomain.AuthSource
	// PersonalTokenUse separates PATs from interactive login tokens
	PersonalTokenUse = tokendomain.TokenUse
	// PersonalTokenType prevents a PAT from being confused with another JWT kind
	PersonalTokenType = tokendomain.TokenType
)

// Issue 为本地认证用户生成 RS256 JWT
func (i *JWTIssuer) Issue(userID, name string) (string, time.Time, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "", time.Time{}, errors.New("JWT user ID is empty")
	}
	now := i.now()
	expiresAt := now.Add(i.ttl)
	claims := localClaims{
		StaffUserID: userID,
		Name:        strings.TrimSpace(name),
		AuthSource:  "local",
		TokenUse:    "accessToken",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    i.issuer,
			Subject:   userID,
			Audience:  jwt.ClaimStrings{i.audience},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			NotBefore: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = i.keyID
	signed, err := token.SignedString(i.privateKey)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, expiresAt, nil
}

// IssuePersonalAccessToken generates a revocable JWT with a caller-selected expiration
func (i *JWTIssuer) IssuePersonalAccessToken(userID, name string, expiresAt time.Time) (string, uuid.UUID, error) {
	userID = strings.TrimSpace(userID)
	now := i.now()
	if userID == "" || !expiresAt.After(now) {
		return "", uuid.Nil, errors.New("personal access token claims are invalid")
	}

	// PATs use the same identity claims as local login tokens and add an explicit token kind
	jti := uuid.New()
	claims := localClaims{
		StaffUserID: userID,
		Name:        strings.TrimSpace(name),
		AuthSource:  PersonalTokenAuthSource,
		TokenUse:    PersonalTokenUse,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: i.issuer, Subject: userID, Audience: jwt.ClaimStrings{i.audience},
			ExpiresAt: jwt.NewNumericDate(expiresAt), NotBefore: jwt.NewNumericDate(now),
			IssuedAt: jwt.NewNumericDate(now), ID: jti.String(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = i.keyID
	token.Header["typ"] = PersonalTokenType
	signed, err := token.SignedString(i.privateKey)
	if err != nil {
		return "", uuid.Nil, err
	}
	return signed, jti, nil
}
