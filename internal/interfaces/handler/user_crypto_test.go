package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	auditapp "env-vault/internal/application/audit"
	cryptoapp "env-vault/internal/application/usercrypto"
	auditdomain "env-vault/internal/domain/audit"
	infraauth "env-vault/internal/infrastructure/auth"
	"env-vault/internal/interfaces/middleware"
	"env-vault/internal/masterkey"
)

type cryptoAuditRepository struct {
	events []*auditdomain.Event
	err    error
}

func (r *cryptoAuditRepository) CreateBatch(_ context.Context, events []*auditdomain.Event) error {
	if r.err != nil {
		return r.err
	}
	r.events = append(r.events, events...)
	return nil
}

func (*cryptoAuditRepository) List(context.Context, auditdomain.ListFilter) ([]*auditdomain.Event, int64, error) {
	return nil, 0, nil
}

type cryptoHTTPResponse struct {
	Code int               `json:"code"`
	Msg  string            `json:"msg"`
	Data map[string]string `json:"data"`
}

func TestUserCryptoHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	privatePEM, publicPEM, err := infraauth.GenerateRSAKeyPair(2048)
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := infraauth.ParseRSAPrivateKey(privatePEM)
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := infraauth.NewJWTIssuer(privateKey, "crypto-test", "crypto-client", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tokenFor := func(user string) string {
		t.Helper()
		token, _, err := issuer.Issue(user, "Tester")
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	auth, err := middleware.Auth([]middleware.JWTProvider{{
		Issuer: "crypto-test", Audience: "crypto-client", KeyID: "test", PublicKey: string(publicPEM),
	}})
	if err != nil {
		t.Fatal(err)
	}
	m := masterkey.NewManager()
	repo := &cryptoAuditRepository{}
	h := NewUserCryptoHandler(cryptoapp.NewService(m, auditapp.NewService(repo, nil)))
	router := gin.New()
	ready, err := masterkey.NewReadyMiddleware(m, []masterkey.AllowedRoute{
		{Method: "POST", Path: "/api/v1/pub/auth/login"},
		{Method: "GET", Path: "/api/v1/masterKey/status"},
		{Method: "POST", Path: "/api/v1/masterKey/share"},
	})
	if err != nil {
		t.Fatal(err)
	}
	router.Use(ready)
	router.POST("/api/v1/crypto/encrypt", auth, h.Encrypt)
	router.POST("/api/v1/crypto/decrypt", auth, h.Decrypt)
	call := func(operation, token, body string, status, code int) cryptoHTTPResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/crypto/"+operation, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var response cryptoHTTPResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if w.Code != status || response.Code != code {
			t.Fatalf("%s: HTTP %d code %d msg %s", operation, w.Code, response.Code, response.Msg)
		}
		if code != 0 && response.Data != nil {
			t.Fatal("error response leaked data")
		}
		if code == 0 && !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("crypto response must not be cached")
		}
		return response
	}
	bodyFor := func(key, value string) string {
		body, err := json.Marshal(map[string]string{key: value})
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	token := tokenFor("account-a")
	call("encrypt", token, `{"plaintext":"test"}`, 200, -2)
	if err := m.Activate(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)), masterkey.SourceShares); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"encrypt", "decrypt"} {
		call(operation, "", `{}`, 401, 401)
		call(operation, "invalid-token", `{}`, 401, 401)
	}
	for _, value := range []string{"", "\n  中文\t\x00\"  ", strings.Repeat("\x00", masterkey.MaxUserPlaintextBytes)} {
		encrypted := call("encrypt", token, bodyFor("plaintext", value), 200, 0).Data["ciphertext"]
		decrypted := call("decrypt", tokenFor("account-a"), bodyFor("ciphertext", encrypted), 200, 0)
		if decrypted.Data["plaintext"] != value {
			t.Fatal("roundtrip changed input")
		}
		call("decrypt", tokenFor("account-b"), bodyFor("ciphertext", encrypted), 200, -1)
		encodedAudit, _ := json.Marshal(repo.events)
		if bytes.Contains(encodedAudit, []byte(encrypted)) || bytes.Contains(encodedAudit, []byte(`"plaintext"`)) {
			t.Fatal("audit leaked cryptographic payload")
		}
	}
	before := len(repo.events)
	for _, body := range []string{
		`{}`, `null`, `{"plaintext":null}`, `{"plaintext":42}`, `{"plaintext":"secret","userId":"account-b"}`,
		`{"plaintext":"secret"} {}`, `{"plaintext":"secret"} trailing`, `{"plaintext":`,
	} {
		call("encrypt", token, body, 400, 400)
	}
	call("decrypt", token, `{"ciphertext":null}`, 400, 400)
	call("encrypt", token, bodyFor("plaintext", strings.Repeat("x", maxCryptoRequestBytes)), 413, 413)
	if len(repo.events) != before {
		t.Fatal("binding failures should not create business audit events")
	}
	call("encrypt", token, bodyFor("plaintext", strings.Repeat("x", masterkey.MaxUserPlaintextBytes+1)), 200, -1)
	call("decrypt", token, `{"ciphertext":"evu:v1:AA"}`, 200, -1)
	last := repo.events[len(repo.events)-1]
	if last.ActionCode != "user.crypto.decrypt" || last.ResultCode != "failure" || last.ResourceID != "account-a" {
		t.Fatal("missing safe failure audit")
	}
	shared := call("encrypt", token, `{"plaintext":"shared-content","decryptMode":"authenticated"}`, 200, 0).Data["ciphertext"]
	if !strings.HasPrefix(shared, masterkey.UserPolicyCipherPrefix) {
		t.Fatal("new ciphertext must use v2")
	}
	for _, id := range []string{"account-a", "account-b", "account-c"} {
		if got := call("decrypt", tokenFor(id), bodyFor("ciphertext", shared), 200, 0); got.Data["plaintext"] != "shared-content" {
			t.Fatal("authenticated sharing failed")
		}
	}
	call("decrypt", "", bodyFor("ciphertext", shared), 401, 401)
	restricted := call("encrypt", token, `{"plaintext":"restricted-content","decryptMode":"users","allowedUserIds":["account-b"]}`, 200, 0).Data["ciphertext"]
	call("decrypt", tokenFor("account-b"), bodyFor("ciphertext", restricted), 200, 0)
	call("decrypt", tokenFor("account-c"), bodyFor("ciphertext", restricted), 200, -1)
	call("decrypt", token, bodyFor("ciphertext", restricted), 200, -1)
	override, _ := json.Marshal(map[string]any{"ciphertext": restricted, "decryptMode": "authenticated"})
	call("decrypt", token, string(override), 400, 400)
	legacy, err := m.EncryptForUser("account-a", "legacy-content")
	if err != nil {
		t.Fatal(err)
	}
	call("decrypt", token, bodyFor("ciphertext", legacy), 200, 0)
	call("decrypt", tokenFor("account-b"), bodyFor("ciphertext", legacy), 200, -1)
	for _, body := range []string{
		`{"plaintext":"text","decryptMode":"public"}`,
		`{"plaintext":"text","decryptMode":"users"}`,
		`{"plaintext":"text","decryptMode":"users","allowedUserIds":[""]}`,
		`{"plaintext":"text","decryptMode":"self","allowedUserIds":["account-b"]}`,
	} {
		call("encrypt", token, body, 200, -1)
	}
	encodedAudit, _ := json.Marshal(repo.events)
	for _, forbidden := range []string{"shared-content", "restricted-content", restricted, "allowedUserIds"} {
		if bytes.Contains(encodedAudit, []byte(forbidden)) {
			t.Fatal("audit leaked protected content or policy")
		}
	}
	encrypted := call("encrypt", token, `{"plaintext":"must-not-leak"}`, 200, 0).Data["ciphertext"]
	repo.err = errors.New("audit unavailable")
	call("encrypt", token, `{"plaintext":"must-not-leak"}`, 200, -1)
	call("decrypt", token, bodyFor("ciphertext", encrypted), 200, -1)
}
