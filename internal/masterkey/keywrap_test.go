package masterkey

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestManagerExportAndActivateWrappedKey(t *testing.T) {
	privateKey := newPeerTestKey(t)
	publicDER := mustMarshalPublicKey(t, &privateKey.PublicKey)

	source := NewManager()
	if err := source.Activate(testKeyBase64, SourceShares); err != nil {
		t.Fatalf("activate source: %v", err)
	}
	wrapped, err := source.ExportWrappedKey(base64.StdEncoding.EncodeToString(publicDER))
	if err != nil {
		t.Fatalf("export wrapped key: %v", err)
	}
	if wrapped.Algorithm != MasterKeyTransferAlgorithm || wrapped.KeyFingerprint == "" || wrapped.EncryptedMasterKey == testKeyBase64 {
		t.Fatalf("unexpected wrapped key: %+v", wrapped)
	}

	target := NewManager()
	if err := target.ActivateWrappedKey(privateKey, wrapped); err != nil {
		t.Fatalf("activate wrapped key: %v", err)
	}
	if status := target.Status(); !status.Ready || status.Source != SourcePeer {
		t.Fatalf("unexpected target status: %+v", status)
	}

	ciphertext, err := source.Encrypt("peer-transfer-value")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	plaintext, err := target.Decrypt(ciphertext)
	if err != nil || plaintext != "peer-transfer-value" {
		t.Fatalf("decrypt = %q, err=%v", plaintext, err)
	}
}

func TestManagerExportWrappedKeyAfterShareRestore(t *testing.T) {
	privateKey := newPeerTestKey(t)
	publicDER := mustMarshalPublicKey(t, &privateKey.PublicKey)
	tokens, err := SplitShareTokens(testKeyBase64)
	if err != nil {
		t.Fatalf("split master key: %v", err)
	}

	source := NewManager()
	for index, token := range tokens[:RequiredShares] {
		if err := source.SubmitShare(token); err != nil {
			t.Fatalf("submit share %d: %v", index+1, err)
		}
	}
	wrapped, err := source.ExportWrappedKey(base64.StdEncoding.EncodeToString(publicDER))
	if err != nil {
		t.Fatalf("export wrapped key after share restore: %v", err)
	}

	sealed, err := base64.StdEncoding.DecodeString(wrapped.EncryptedMasterKey)
	if err != nil {
		t.Fatalf("decode encrypted key: %v", err)
	}
	key, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, privateKey, sealed, nil)
	if err != nil {
		t.Fatalf("decrypt encrypted key: %v", err)
	}
	if got := base64.StdEncoding.EncodeToString(key); got != testKeyBase64 {
		t.Fatalf("decrypted key = %q, want %q", got, testKeyBase64)
	}
	clear(key)
}

func TestManagerActivateWrappedKeyRejectsTampering(t *testing.T) {
	privateKey := newPeerTestKey(t)
	publicDER := mustMarshalPublicKey(t, &privateKey.PublicKey)
	source := NewManager()
	if err := source.Activate(testKeyBase64, SourceShares); err != nil {
		t.Fatalf("activate source: %v", err)
	}
	wrapped, err := source.ExportWrappedKey(base64.StdEncoding.EncodeToString(publicDER))
	if err != nil {
		t.Fatalf("export wrapped key: %v", err)
	}
	wrapped.KeyFingerprint = "sha256:" + strings.Repeat("0", 64)

	target := NewManager()
	if err := target.ActivateWrappedKey(privateKey, wrapped); err != ErrKeyFingerprintMismatch {
		t.Fatalf("activate tampered key error = %v, want %v", err, ErrKeyFingerprintMismatch)
	}
	if target.Ready() {
		t.Fatal("tampered transfer must not activate target")
	}
}

func TestManagerExportWrappedKeyValidation(t *testing.T) {
	manager := NewManager()
	if _, err := manager.ExportWrappedKey("invalid"); err != ErrNotReady && !strings.Contains(err.Error(), ErrInvalidTransferRequest.Error()) {
		t.Fatalf("not-ready invalid key error = %v", err)
	}
	if err := manager.Activate(testKeyBase64, SourceShares); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := manager.ExportWrappedKey("invalid"); !strings.Contains(err.Error(), ErrInvalidTransferRequest.Error()) {
		t.Fatalf("invalid public key error = %v", err)
	}
}

func TestHTTPHandlerTransfer(t *testing.T) {
	privateKey := newPeerTestKey(t)
	publicDER := mustMarshalPublicKey(t, &privateKey.PublicKey)
	manager := NewManager()
	if err := manager.Activate(testKeyBase64, SourceShares); err != nil {
		t.Fatalf("activate: %v", err)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	RegisterInternalRoutes(engine.Group("/internal/v1"), manager, "peer-secret")
	payload, _ := json.Marshal(TransferRequest{
		InstanceID: "env-vault-1", RequestID: "request-1",
		PublicKey: base64.StdEncoding.EncodeToString(publicDER),
	})
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/masterKey/transfer", strings.NewReader(string(payload)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(InternalPeerTokenHeader, "peer-secret")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var body httpTestResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != 0 {
		t.Fatalf("response code = %d, msg = %q", body.Code, body.Msg)
	}
	var response TransferResponse
	if err := json.Unmarshal(body.Data, &response); err != nil {
		t.Fatalf("decode transfer response: %v", err)
	}
	sealed, err := base64.StdEncoding.DecodeString(response.EncryptedMasterKey)
	if err != nil {
		t.Fatalf("decode encrypted key: %v", err)
	}
	key, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, privateKey, sealed, nil)
	if err != nil {
		t.Fatalf("decrypt encrypted key: %v", err)
	}
	if got := base64.StdEncoding.EncodeToString(key); got != testKeyBase64 {
		t.Fatalf("decrypted key = %q, want %q", got, testKeyBase64)
	}
	clear(key)
}

func TestHTTPHandlerTransferRequiresInternalToken(t *testing.T) {
	manager := NewManager()
	if err := manager.Activate(testKeyBase64, SourceShares); err != nil {
		t.Fatalf("activate: %v", err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	RegisterInternalRoutes(engine.Group("/internal/v1"), manager, "peer-secret")
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/masterKey/transfer", strings.NewReader(`{}`))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}

	engine = gin.New()
	RegisterInternalRoutes(engine.Group("/internal/v1"), manager, "")
	request = httptest.NewRequest(http.MethodPost, "/internal/v1/masterKey/transfer", strings.NewReader(`{}`))
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured token status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestHTTPHandlerReady(t *testing.T) {
	manager := NewManager()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	RegisterInternalRoutes(engine.Group("/internal/v1"), manager, "peer-secret")

	request := httptest.NewRequest(http.MethodGet, "/internal/v1/masterKey/ready", nil)
	request.Header.Set(InternalPeerTokenHeader, "peer-secret")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}

	if err := manager.Activate(testKeyBase64, SourceShares); err != nil {
		t.Fatalf("activate: %v", err)
	}
	request = httptest.NewRequest(http.MethodGet, "/internal/v1/masterKey/ready", nil)
	request.Header.Set(InternalPeerTokenHeader, "peer-secret")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"status":"ready"`) {
		t.Fatalf("ready response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func newPeerTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

func mustMarshalPublicKey(t *testing.T, key *rsa.PublicKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return der
}
