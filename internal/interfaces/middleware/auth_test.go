package middleware

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	infraauth "env-vault/internal/infrastructure/auth"
	"env-vault/pkg/response"
)

type stubBlockChecker struct {
	blocked bool
	err     error
	userID  string
}

type stubPersonalTokenChecker struct {
	active bool
	err    error
	jti    string
	userID string
}

func (s *stubPersonalTokenChecker) Validate(_ context.Context, jti, userID string) (bool, error) {
	s.jti = jti
	s.userID = userID
	return s.active, s.err
}

func (s *stubBlockChecker) IsBlocked(_ context.Context, userID string) (bool, error) {
	s.userID = userID
	return s.blocked, s.err
}

func TestAuth_BlockedUserReturnsForbidden(t *testing.T) {
	privateKey, publicKey := testRSAKeyPair(t)
	checker := &stubBlockChecker{blocked: true}
	auth, err := Auth([]JWTProvider{{
		Issuer: "env-vault-test", Audience: "env-vault-web", KeyID: "test-key", PublicKey: publicKey,
	}}, checker)
	if err != nil {
		t.Fatalf("Auth() error = %v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	handlerCalled := false
	r.GET("/protected", auth, func(c *gin.Context) {
		handlerCalled = true
		c.Status(http.StatusNoContent)
	})

	token := testJWT(t, privateKey, "blocked-user")
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden || handlerCalled || checker.userID != "blocked-user" {
		t.Fatalf("unexpected status=%d called=%v checkedUser=%q", w.Code, handlerCalled, checker.userID)
	}
	var body response.Response
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != http.StatusForbidden || body.Message != "用户被锁定" {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
}

func TestAuth_PersonalTokenRequiresActiveDatabaseRecord(t *testing.T) {
	privateKey, publicKey := testRSAKeyPair(t)
	checker := &stubPersonalTokenChecker{active: false}
	auth, err := AuthWithAudit([]JWTProvider{{
		Issuer: "env-vault-test", Audience: "env-vault-web", KeyID: "test-key", PublicKey: publicKey,
	}}, nil, nil, checker)
	if err != nil {
		t.Fatalf("AuthWithAudit() error = %v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/protected", auth, func(c *gin.Context) { c.Status(http.StatusNoContent) })

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"staffuserid": "user-1", "name": "Tester", "authSource": "personalToken",
		"tokenUse": "personalAccessToken", "jti": "30d71587-0b2b-4e18-88d1-913af9f334d8",
		"iss": "env-vault-test", "aud": "env-vault-web", "exp": time.Now().Add(time.Hour).Unix(),
	})
	token.Header["kid"] = "test-key"
	token.Header["typ"] = "env-vault-pat+jwt"
	signed, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized || checker.userID != "user-1" || checker.jti == "" {
		t.Fatalf("status=%d checkedUser=%q checkedJTI=%q", w.Code, checker.userID, checker.jti)
	}
}

// TestAuth_PersonalTokensRemainUsableWithoutLocalLogin 验证密码登录关闭后公司身份与 PAT 仍可访问业务接口
func TestAuth_PersonalTokensRemainUsableWithoutLocalLogin(t *testing.T) {
	localKey, localPublic := testRSAKeyPair(t)
	companyKey, companyPublic := testRSAKeyPair(t)
	issuer, err := infraauth.NewJWTIssuer(localKey, "env-vault-test", "env-vault-web", "test-key", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	localToken, _, err := issuer.Issue("user-1", "Tester")
	if err != nil {
		t.Fatal(err)
	}
	pat, jti, err := issuer.IssuePersonalAccessToken("user-1", "Tester", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	companyJWT := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"staffuserid": "user-1", "iss": "company", "aud": "company-web", "exp": time.Now().Add(time.Hour).Unix(),
	})
	companyToken, err := companyJWT.SignedString(companyKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		token        string
		localEnabled bool
		active       bool
		blocked      bool
		wantStatus   int
		checkPAT     bool
	}{
		{name: "company identity", token: companyToken, wantStatus: http.StatusNoContent},
		{name: "active PAT", token: pat, active: true, wantStatus: http.StatusNoContent, checkPAT: true},
		{name: "revoked PAT", token: pat, wantStatus: http.StatusUnauthorized, checkPAT: true},
		{name: "blocked PAT owner", token: pat, active: true, blocked: true, wantStatus: http.StatusForbidden},
		{name: "disabled local login token", token: localToken, wantStatus: http.StatusUnauthorized},
		{name: "enabled local login token", token: localToken, localEnabled: true, wantStatus: http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checker := &stubPersonalTokenChecker{active: tc.active}
			auth, err := AuthWithAudit([]JWTProvider{
				{Issuer: "env-vault-test", Audience: "env-vault-web", KeyID: "test-key", PublicKey: localPublic, PersonalTokensOnly: !tc.localEnabled},
				{Issuer: "company", Audience: "company-web", PublicKey: companyPublic},
			}, &stubBlockChecker{blocked: tc.blocked}, nil, checker)
			if err != nil {
				t.Fatal(err)
			}
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.POST("/api/v1/user/token/list", auth, func(c *gin.Context) { c.Status(http.StatusNoContent) })
			req := httptest.NewRequest(http.MethodPost, "/api/v1/user/token/list", nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantStatus)
			}
			if tc.checkPAT && (checker.jti != jti.String() || checker.userID != "user-1") {
				t.Error("PAT must pass database revocation and ownership checks")
			}
		})
	}
}

func testRSAKeyPair(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return privateKey, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func testJWT(t *testing.T, key *rsa.PrivateKey, userID string) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"staffuserid": userID,
		"name":        "Tester",
		"iss":         "env-vault-test",
		"aud":         "env-vault-web",
		"exp":         time.Now().Add(time.Hour).Unix(),
	})
	token.Header["kid"] = "test-key"
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	return signed
}
