package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestJWTIssuerAddsCompatibleAndStandardClaims(t *testing.T) {
	privatePEM, _, err := GenerateRSAKeyPair(2048)
	if err != nil {
		t.Fatalf("GenerateRSAKeyPair: %v", err)
	}
	privateKey, err := ParseRSAPrivateKey(privatePEM)
	if err != nil {
		t.Fatalf("ParseRSAPrivateKey: %v", err)
	}
	issuer, err := NewJWTIssuer(privateKey, "env-vault", "env-vault-web", "local-v1", time.Hour)
	if err != nil {
		t.Fatalf("NewJWTIssuer: %v", err)
	}
	tokenString, expiresAt, err := issuer.Issue("u-1", "Vince")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenString, &claims, func(token *jwt.Token) (any, error) {
		return &privateKey.PublicKey, nil
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer("env-vault"), jwt.WithAudience("env-vault-web"), jwt.WithExpirationRequired())
	if err != nil || !token.Valid {
		t.Fatalf("parse issued token: valid=%v err=%v", token.Valid, err)
	}
	if claims["staffuserid"] != "u-1" || claims["sub"] != "u-1" || claims["name"] != "Vince" || claims["authSource"] != "local" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if token.Header["kid"] != "local-v1" || time.Until(expiresAt) <= 0 {
		t.Fatalf("unexpected header or expiry: header=%+v expiresAt=%s", token.Header, expiresAt)
	}
}

func TestJWTIssuerAddsPersonalAccessTokenMarkers(t *testing.T) {
	privatePEM, _, err := GenerateRSAKeyPair(2048)
	if err != nil {
		t.Fatalf("GenerateRSAKeyPair: %v", err)
	}
	privateKey, err := ParseRSAPrivateKey(privatePEM)
	if err != nil {
		t.Fatalf("ParseRSAPrivateKey: %v", err)
	}
	issuer, err := NewJWTIssuer(privateKey, "env-vault", "env-vault-web", "local-v1", time.Hour)
	if err != nil {
		t.Fatalf("NewJWTIssuer: %v", err)
	}
	expiresAt := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
	tokenString, jti, err := issuer.IssuePersonalAccessToken("u-1", "Vince", expiresAt)
	if err != nil {
		t.Fatalf("IssuePersonalAccessToken: %v", err)
	}
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenString, &claims, func(token *jwt.Token) (any, error) {
		return &privateKey.PublicKey, nil
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer("env-vault"), jwt.WithAudience("env-vault-web"), jwt.WithExpirationRequired())
	if err != nil || !token.Valid {
		t.Fatalf("parse issued token: valid=%v err=%v", token.Valid, err)
	}
	if claims["authSource"] != PersonalTokenAuthSource || claims["tokenUse"] != PersonalTokenUse || claims["jti"] != jti.String() {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if token.Header["typ"] != PersonalTokenType || token.Header["kid"] != "local-v1" {
		t.Fatalf("unexpected header: %+v", token.Header)
	}
}
