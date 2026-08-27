package auth

import (
	"crypto/rsa"
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
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
	jwt.RegisteredClaims
}

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
