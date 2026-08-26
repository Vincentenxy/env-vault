package masterkey

import (
	"encoding/base64"
	"errors"
	"testing"

	"env-vault/pkg/crypto"
)

func TestGenerateShareTokens(t *testing.T) {
	tokens, err := GenerateShareTokens()
	if err != nil {
		t.Fatalf("GenerateShareTokens: %v", err)
	}
	assertGeneratedShareTokens(t, tokens)

	manager := NewManager()
	if err := manager.RestoreShares([]string{tokens[4], tokens[1], tokens[3]}); err != nil {
		t.Fatalf("RestoreShares: %v", err)
	}
	ciphertext, err := manager.Encrypt("generated-secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	plaintext, err := manager.Decrypt(ciphertext)
	if err != nil || plaintext != "generated-secret" {
		t.Fatalf("Decrypt = %q, err = %v", plaintext, err)
	}
}

func TestSplitShareTokensUsesExistingKey(t *testing.T) {
	key := []byte("12345678901234567890123456789012")
	keyBase64 := base64.StdEncoding.EncodeToString(key)
	tokens, err := SplitShareTokens(keyBase64)
	if err != nil {
		t.Fatalf("SplitShareTokens: %v", err)
	}
	assertGeneratedShareTokens(t, tokens)

	sourceCipher, err := crypto.New(keyBase64)
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	ciphertext, err := sourceCipher.Encrypt("existing-secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	manager := NewManager()
	if err := manager.RestoreShares(tokens[1:4]); err != nil {
		t.Fatalf("RestoreShares: %v", err)
	}
	plaintext, err := manager.Decrypt(ciphertext)
	if err != nil || plaintext != "existing-secret" {
		t.Fatalf("Decrypt = %q, err = %v", plaintext, err)
	}
}

func TestSplitShareTokensRejectsInvalidKey(t *testing.T) {
	tests := []string{
		"not-base64",
		base64.StdEncoding.EncodeToString(make([]byte, 16)),
	}
	for _, key := range tests {
		if _, err := SplitShareTokens(key); !errors.Is(err, ErrInvalidMasterKey) {
			t.Fatalf("SplitShareTokens error = %v, want ErrInvalidMasterKey", err)
		}
	}
}

func TestGeneratedShareSetsUseDifferentBatchIDs(t *testing.T) {
	first, err := GenerateShareTokens()
	if err != nil {
		t.Fatalf("first GenerateShareTokens: %v", err)
	}
	second, err := GenerateShareTokens()
	if err != nil {
		t.Fatalf("second GenerateShareTokens: %v", err)
	}
	firstShare, err := decodeShareToken(first[0])
	if err != nil {
		t.Fatalf("decode first share: %v", err)
	}
	secondShare, err := decodeShareToken(second[0])
	if err != nil {
		t.Fatalf("decode second share: %v", err)
	}
	if firstShare.keySetID == secondShare.keySetID {
		t.Fatal("generated share sets must use different key set IDs")
	}
}

func assertGeneratedShareTokens(t *testing.T, tokens []string) {
	t.Helper()
	if len(tokens) != TotalShares {
		t.Fatalf("token count = %d, want %d", len(tokens), TotalShares)
	}
	seen := make(map[byte]struct{}, TotalShares)
	var keySetID string
	for _, token := range tokens {
		share, err := decodeShareToken(token)
		if err != nil {
			t.Fatalf("decodeShareToken: %v", err)
		}
		if keySetID == "" {
			keySetID = share.keySetID.String()
		} else if share.keySetID.String() != keySetID {
			t.Fatal("generated shares must belong to one key set")
		}
		if _, exists := seen[share.index]; exists {
			t.Fatalf("duplicate share index %d", share.index)
		}
		seen[share.index] = struct{}{}
	}
}
