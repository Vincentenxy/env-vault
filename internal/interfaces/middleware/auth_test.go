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

	"env-vault/pkg/response"
)

type stubBlockChecker struct {
	blocked bool
	err     error
	userID  string
}

func (s *stubBlockChecker) IsBlocked(_ context.Context, userID string) (bool, error) {
	s.userID = userID
	return s.blocked, s.err
}

func TestAuth_BlockedUserReturnsForbidden(t *testing.T) {
	privateKey, publicKey := testRSAKeyPair(t)
	checker := &stubBlockChecker{blocked: true}
	auth, err := Auth(publicKey, checker)
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
		"exp":         time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	return signed
}
