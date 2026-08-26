package config

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSecurityConfig(t *testing.T) {
	dir := t.TempDir()
	content := []byte(`security:
  allow_config_key_fallback: true
  encryption_key: test-key
  ready_allowlist:
    - method: GET
      path: /api/v1/pub/masterKey/status
    - method: POST
      path: /api/v1/pub/masterKey/shares
`)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Security.AllowConfigKeyFallback {
		t.Fatal("AllowConfigKeyFallback = false, want true")
	}
	if cfg.Security.EncryptionKey != "test-key" {
		t.Fatalf("EncryptionKey = %q, want %q", cfg.Security.EncryptionKey, "test-key")
	}
	if len(cfg.Security.ReadyAllowlist) != 2 ||
		cfg.Security.ReadyAllowlist[0].Method != http.MethodGet ||
		cfg.Security.ReadyAllowlist[1].Path != "/api/v1/pub/masterKey/shares" {
		t.Fatalf("unexpected ReadyAllowlist: %+v", cfg.Security.ReadyAllowlist)
	}
}
