package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"env-vault/internal/masterkey"
	"env-vault/pkg/crypto"
)

func TestRunGenerateJSON(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run([]string{"generate", "--format", "json"}, &stdout, &stderr, noEnvironment); err != nil {
		t.Fatalf("run generate: %v, stderr=%s", err, stderr.String())
	}

	var output shareOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if output.TotalShares != masterkey.TotalShares || output.RequiredShares != masterkey.RequiredShares {
		t.Fatalf("unexpected share metadata: %+v", output)
	}
	assertRestorableTokens(t, output.Shares)
}

func TestRunSplitReadsExistingKeyFromEnvironment(t *testing.T) {
	key := []byte("12345678901234567890123456789012")
	keyBase64 := base64.StdEncoding.EncodeToString(key)
	lookupEnv := func(name string) (string, bool) {
		if name != "TEST_MASTER_KEY" {
			t.Fatalf("environment name = %q", name)
		}
		return keyBase64, true
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := run(
		[]string{"split", "--key-env", "TEST_MASTER_KEY", "--format", "json"},
		&stdout,
		&stderr,
		lookupEnv,
	)
	if err != nil {
		t.Fatalf("run split: %v, stderr=%s", err, stderr.String())
	}
	if strings.Contains(stdout.String(), keyBase64) {
		t.Fatal("output must not contain the complete master key")
	}

	var output shareOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	assertRestorableTokens(t, output.Shares)

	sourceCipher, err := crypto.New(keyBase64)
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	ciphertext, err := sourceCipher.Encrypt("existing-value")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	manager := masterkey.NewManager()
	if err := manager.RestoreShares(output.Shares[:masterkey.RequiredShares]); err != nil {
		t.Fatalf("RestoreShares: %v", err)
	}
	plaintext, err := manager.Decrypt(ciphertext)
	if err != nil || plaintext != "existing-value" {
		t.Fatalf("Decrypt = %q, err = %v", plaintext, err)
	}
}

func TestRunSplitRejectsMissingEnvironment(t *testing.T) {
	err := run([]string{"split"}, &bytes.Buffer{}, &bytes.Buffer{}, noEnvironment)
	if err == nil || !strings.Contains(err.Error(), defaultMasterKeyEnv) {
		t.Fatalf("error = %v", err)
	}
}

func TestRunSplitRejectsCommandLineMasterKey(t *testing.T) {
	err := run(
		[]string{"split", "--key", "sensitive"},
		&bytes.Buffer{},
		&bytes.Buffer{},
		noEnvironment,
	)
	if err == nil {
		t.Fatal("command-line master key must be rejected")
	}
}

func TestRunHelp(t *testing.T) {
	var stdout bytes.Buffer
	if err := run([]string{"help"}, &stdout, &bytes.Buffer{}, noEnvironment); err != nil {
		t.Fatalf("run help: %v", err)
	}
	if !strings.Contains(stdout.String(), "generate") || !strings.Contains(stdout.String(), "split") {
		t.Fatalf("unexpected help output: %s", stdout.String())
	}
}

func noEnvironment(string) (string, bool) {
	return "", false
}

func assertRestorableTokens(t *testing.T, tokens []string) {
	t.Helper()
	if len(tokens) != masterkey.TotalShares {
		t.Fatalf("token count = %d, want %d", len(tokens), masterkey.TotalShares)
	}
	manager := masterkey.NewManager()
	if err := manager.RestoreShares([]string{tokens[4], tokens[0], tokens[2]}); err != nil {
		t.Fatalf("RestoreShares: %v", err)
	}
}
