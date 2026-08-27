package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	infraauth "env-vault/internal/infrastructure/auth"
)

func TestRunKeygenCreatesMatchingKeysWithoutOverwrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	var stdout bytes.Buffer
	if err := run([]string{"keygen", "--out-dir", dir, "--bits", "2048"}, &stdout, &bytes.Buffer{}, noEnv); err != nil {
		t.Fatalf("run keygen: %v", err)
	}
	privateValue, err := os.ReadFile(filepath.Join(dir, "jwt-private.pem"))
	if err != nil {
		t.Fatalf("read private key: %v", err)
	}
	publicValue, err := os.ReadFile(filepath.Join(dir, "jwt-public.pem"))
	if err != nil {
		t.Fatalf("read public key: %v", err)
	}
	privateKey, err := infraauth.ParseRSAPrivateKey(privateValue)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}
	publicKey, err := infraauth.ParseRSAPublicKey(publicValue)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	if !privateKey.PublicKey.Equal(publicKey) {
		t.Fatal("generated key pair does not match")
	}
	if err := run([]string{"keygen", "--out-dir", dir, "--bits", "2048"}, &bytes.Buffer{}, &bytes.Buffer{}, noEnv); err == nil {
		t.Fatal("keygen must not overwrite existing keys")
	}
}

func TestRunHashPasswordReadsEnvironment(t *testing.T) {
	lookup := func(name string) (string, bool) {
		if name != "TEST_PASSWORD" {
			t.Fatalf("unexpected environment name %q", name)
		}
		return "correct horse battery staple", true
	}
	var stdout bytes.Buffer
	if err := run([]string{"hash-password", "--password-env", "TEST_PASSWORD"}, &stdout, &bytes.Buffer{}, lookup); err != nil {
		t.Fatalf("run hash-password: %v", err)
	}
	hash := strings.TrimSpace(stdout.String())
	if strings.Contains(hash, "correct horse") {
		t.Fatal("password must not be written to output")
	}
	hasher, err := infraauth.NewPasswordHasher()
	if err != nil {
		t.Fatalf("new hasher: %v", err)
	}
	matched, err := hasher.Verify("correct horse battery staple", hash)
	if err != nil || !matched {
		t.Fatalf("generated hash does not verify: matched=%v err=%v", matched, err)
	}
}

func TestRunHashPasswordRejectsCommandLinePassword(t *testing.T) {
	err := run([]string{"hash-password", "--password", "secret"}, &bytes.Buffer{}, &bytes.Buffer{}, noEnv)
	if err == nil {
		t.Fatal("command-line password must be rejected")
	}
}

func noEnv(string) (string, bool) { return "", false }
