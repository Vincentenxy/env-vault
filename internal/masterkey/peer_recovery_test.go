package masterkey

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPeerRecoveryRunActivatesFromReadyPeer(t *testing.T) {
	source := activatedPeerManager(t)
	var requests atomic.Int32
	server := newPeerRecoveryServer(t, source, func(w http.ResponseWriter, req TransferRequest, wrapped WrappedMasterKey) {
		requests.Add(1)
		if req.InstanceID != "env-vault-1" || req.Algorithm != MasterKeyTransferAlgorithm {
			t.Errorf("unexpected transfer request: %+v", req)
		}
		writePeerRecoverySuccess(t, w, req.RequestID, wrapped)
	})
	defer server.Close()

	target := NewManager()
	recovery := newTestPeerRecovery(t, target, server.URL, "peer-secret")
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
	assertPeerManagerMatches(t, source, target)
}

func TestPeerRecoveryRetriesTransientFailure(t *testing.T) {
	source := activatedPeerManager(t)
	var requests atomic.Int32
	server := newPeerRecoveryServer(t, source, func(w http.ResponseWriter, req TransferRequest, wrapped WrappedMasterKey) {
		if requests.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writePeerRecoverySuccess(t, w, req.RequestID, wrapped)
	})
	defer server.Close()

	target := NewManager()
	recovery := newTestPeerRecovery(t, target, server.URL, "peer-secret")
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests.Load() != 3 {
		t.Fatalf("requests = %d, want 3", requests.Load())
	}
	assertPeerManagerMatches(t, source, target)
}

func TestPeerRecoveryStopsOnAuthenticationFailure(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	target := NewManager()
	recovery := newTestPeerRecovery(t, target, server.URL, "wrong-secret")
	err := recovery.Run(context.Background())
	if !errors.Is(err, ErrPeerRecoveryRejected) {
		t.Fatalf("Run error = %v, want ErrPeerRecoveryRejected", err)
	}
	if requests.Load() != 1 || target.Ready() {
		t.Fatalf("requests=%d ready=%v", requests.Load(), target.Ready())
	}
}

func TestPeerRecoveryRejectsTamperedResponse(t *testing.T) {
	source := activatedPeerManager(t)
	server := newPeerRecoveryServer(t, source, func(w http.ResponseWriter, req TransferRequest, wrapped WrappedMasterKey) {
		wrapped.KeyFingerprint = "sha256:tampered"
		writePeerRecoverySuccess(t, w, req.RequestID, wrapped)
	})
	defer server.Close()

	target := NewManager()
	recovery := newTestPeerRecovery(t, target, server.URL, "peer-secret")
	err := recovery.Run(context.Background())
	if !errors.Is(err, ErrKeyFingerprintMismatch) {
		t.Fatalf("Run error = %v, want ErrKeyFingerprintMismatch", err)
	}
	if target.Ready() {
		t.Fatal("tampered response must not activate target")
	}
}

func TestPeerRecoveryRejectsMismatchedRequestID(t *testing.T) {
	source := activatedPeerManager(t)
	server := newPeerRecoveryServer(t, source, func(w http.ResponseWriter, _ TransferRequest, wrapped WrappedMasterKey) {
		writePeerRecoverySuccess(t, w, "different-request", wrapped)
	})
	defer server.Close()

	target := NewManager()
	recovery := newTestPeerRecovery(t, target, server.URL, "peer-secret")
	err := recovery.Run(context.Background())
	if !errors.Is(err, ErrPeerRecoveryProtocol) {
		t.Fatalf("Run error = %v, want ErrPeerRecoveryProtocol", err)
	}
	if target.Ready() {
		t.Fatal("mismatched request ID must not activate target")
	}
}

func TestPeerRecoveryAcceptsConcurrentActivation(t *testing.T) {
	source := activatedPeerManager(t)
	target := NewManager()
	server := newPeerRecoveryServer(t, source, func(w http.ResponseWriter, req TransferRequest, wrapped WrappedMasterKey) {
		if err := target.Activate(testKeyBase64, SourceShares); err != nil {
			t.Errorf("activate target concurrently: %v", err)
		}
		writePeerRecoverySuccess(t, w, req.RequestID, wrapped)
	})
	defer server.Close()

	recovery := newTestPeerRecovery(t, target, server.URL, "peer-secret")
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if status := target.Status(); !status.Ready || status.Source != SourceShares {
		t.Fatalf("unexpected concurrent activation status: %+v", status)
	}
}

func TestPeerRecoveryStopsWhenContextIsCancelled(t *testing.T) {
	requestSeen := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case requestSeen <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	recovery := newTestPeerRecovery(t, NewManager(), server.URL, "peer-secret")
	done := make(chan error, 1)
	go func() {
		done <- recovery.Run(ctx)
	}()
	<-requestSeen
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after context cancellation")
	}
}

func TestPeerRecoverySkipsRequestWhenManagerIsReady(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	manager := activatedPeerManager(t)
	recovery := newTestPeerRecovery(t, manager, server.URL, "peer-secret")
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d, want 0", requests.Load())
	}
}

func TestNewPeerRecoveryValidatesConfiguration(t *testing.T) {
	valid := PeerRecoveryConfig{
		Enabled: true, BaseURL: "http://env-vault", Token: "peer-secret", InstanceID: "env-vault-1",
		RequestTimeout: time.Second, InitialRetryInterval: time.Second, MaxRetryInterval: 5 * time.Second,
	}
	tests := []struct {
		name   string
		mutate func(*PeerRecoveryConfig)
	}{
		{name: "missing URL", mutate: func(cfg *PeerRecoveryConfig) { cfg.BaseURL = "" }},
		{name: "URL path", mutate: func(cfg *PeerRecoveryConfig) { cfg.BaseURL = "http://env-vault/path" }},
		{name: "missing token", mutate: func(cfg *PeerRecoveryConfig) { cfg.Token = "" }},
		{name: "missing instance", mutate: func(cfg *PeerRecoveryConfig) { cfg.InstanceID = "" }},
		{name: "missing timeout", mutate: func(cfg *PeerRecoveryConfig) { cfg.RequestTimeout = 0 }},
		{name: "invalid retry", mutate: func(cfg *PeerRecoveryConfig) { cfg.InitialRetryInterval = 10 * time.Second }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			if _, err := NewPeerRecovery(NewManager(), cfg); !errors.Is(err, ErrInvalidPeerRecoveryConfig) {
				t.Fatalf("NewPeerRecovery error = %v, want ErrInvalidPeerRecoveryConfig", err)
			}
		})
	}

	if recovery, err := NewPeerRecovery(NewManager(), PeerRecoveryConfig{}); err != nil || recovery.enabled {
		t.Fatalf("disabled recovery = %+v, err=%v", recovery, err)
	}
}

func newTestPeerRecovery(t *testing.T, manager *Manager, baseURL, token string) *PeerRecovery {
	t.Helper()
	recovery, err := NewPeerRecovery(manager, PeerRecoveryConfig{
		Enabled:              true,
		BaseURL:              baseURL,
		Token:                token,
		InstanceID:           "env-vault-1",
		RequestTimeout:       500 * time.Millisecond,
		InitialRetryInterval: time.Millisecond,
		MaxRetryInterval:     2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewPeerRecovery: %v", err)
	}
	return recovery
}

func activatedPeerManager(t *testing.T) *Manager {
	t.Helper()
	manager := NewManager()
	if err := manager.Activate(testKeyBase64, SourceShares); err != nil {
		t.Fatalf("activate source: %v", err)
	}
	return manager
}

func newPeerRecoveryServer(
	t *testing.T,
	manager *Manager,
	respond func(http.ResponseWriter, TransferRequest, WrappedMasterKey),
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != peerTransferPath {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Header.Get(InternalPeerTokenHeader) != "peer-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req TransferRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		wrapped, err := manager.ExportWrappedKey(req.PublicKey)
		if err != nil {
			t.Errorf("export wrapped key: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		respond(w, req, wrapped)
	}))
}

func writePeerRecoverySuccess(t *testing.T, w http.ResponseWriter, requestID string, wrapped WrappedMasterKey) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"code": 0,
		"msg":  "success",
		"data": TransferResponse{
			RequestID: requestID, EncryptedMasterKey: wrapped.EncryptedMasterKey,
			KeyFingerprint: wrapped.KeyFingerprint, Algorithm: wrapped.Algorithm,
		},
	}); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func assertPeerManagerMatches(t *testing.T, source, target *Manager) {
	t.Helper()
	if status := target.Status(); !status.Ready || status.Source != SourcePeer {
		t.Fatalf("unexpected target status: %+v", status)
	}
	ciphertext, err := source.Encrypt("peer-recovery-value")
	if err != nil {
		t.Fatalf("encrypt source value: %v", err)
	}
	plaintext, err := target.Decrypt(ciphertext)
	if err != nil || plaintext != "peer-recovery-value" {
		t.Fatalf("target decrypt = %q, err=%v", plaintext, err)
	}
}
