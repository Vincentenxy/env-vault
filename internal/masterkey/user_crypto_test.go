package masterkey

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func userCryptoManager(t *testing.T, key byte) *Manager {
	t.Helper()
	m := NewManager()
	if err := m.Activate(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{key}, 32)), SourceShares); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestUserCryptoRoundTripAndRecovery(t *testing.T) {
	m := userCryptoManager(t, 1)
	restored := userCryptoManager(t, 1)
	for _, input := range []string{"", "  中文\nline two\t\x00  ", strings.Repeat("a", MaxUserPlaintextBytes)} {
		first, err := m.EncryptForUser("account-a", input)
		if err != nil {
			t.Fatal(err)
		}
		second, err := m.EncryptForUser("account-a", input)
		if err != nil || first == second {
			t.Fatalf("encryption must use fresh randomness: %v", err)
		}
		got, err := restored.DecryptForUser("account-a", first)
		if err != nil || got != input {
			t.Fatalf("restored manager failed round trip: %v", err)
		}
		if _, err := restored.DecryptForUser("account-b", first); !errors.Is(err, ErrInvalidCiphertext) {
			t.Fatalf("cross-account decrypt: %v", err)
		}
		if _, err := userCryptoManager(t, 2).DecryptForUser("account-a", first); !errors.Is(err, ErrInvalidCiphertext) {
			t.Fatalf("different master key decrypt: %v", err)
		}
	}
}

func TestUserCryptoRejectsStorageEnvelopesAndTampering(t *testing.T) {
	m := userCryptoManager(t, 1)
	storage, err := m.Encrypt("internal-secret")
	if err != nil {
		t.Fatal(err)
	}
	// Repack a real database ciphertext into the public format. Key separation must
	// prevent decryption even when an attacker knows both envelope formats.
	var envelope struct{ Data, Nonce string }
	if err := json.Unmarshal([]byte(storage), &envelope); err != nil {
		t.Fatal(err)
	}
	nonce, _ := base64.StdEncoding.DecodeString(envelope.Nonce)
	data, _ := base64.StdEncoding.DecodeString(envelope.Data)
	repacked := UserCipherPrefix + base64.RawURLEncoding.EncodeToString(append(nonce, data...))
	valid, err := m.EncryptForUser("account-a", "external-secret")
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(valid, UserCipherPrefix))
	sealed[len(sealed)-1] ^= 1
	tampered := UserCipherPrefix + base64.RawURLEncoding.EncodeToString(sealed)
	for _, value := range []string{
		"", storage, repacked, tampered, "evu:v2:" + strings.TrimPrefix(valid, UserCipherPrefix),
		UserCipherPrefix, UserCipherPrefix + "AA", UserCipherPrefix + "!invalid!",
		valid + "\n", valid + "=", strings.Repeat("x", MaxUserCiphertextBytes+1),
	} {
		if result, err := m.DecryptForUser("account-a", value); !errors.Is(err, ErrInvalidCiphertext) || result != "" {
			t.Fatalf("invalid input should fail without returning plaintext: %v", err)
		}
	}
}

func TestUserCryptoInputAndReadiness(t *testing.T) {
	m := NewManager()
	if _, err := m.EncryptForUser("account-a", "test"); !errors.Is(err, ErrNotReady) {
		t.Fatal(err)
	}
	if _, err := m.DecryptForUser("account-a", "test"); !errors.Is(err, ErrNotReady) {
		t.Fatal(err)
	}
	m = userCryptoManager(t, 1)
	for _, input := range []string{strings.Repeat("a", MaxUserPlaintextBytes+1), string([]byte{0xff})} {
		if _, err := m.EncryptForUser("account-a", input); !errors.Is(err, ErrInvalidPlaintext) {
			t.Fatal(err)
		}
	}
	if _, err := m.EncryptForUser(" ", "test"); !errors.Is(err, ErrUserRequired) {
		t.Fatal(err)
	}
	if _, err := m.DecryptForUser("", "test"); !errors.Is(err, ErrUserRequired) {
		t.Fatal(err)
	}
}

func FuzzUserCryptoDecrypt(f *testing.F) {
	f.Add("evu:v1:")
	f.Add("evu:v1:AA")
	f.Add("evu:v2:")
	f.Add("evu:v2:AA")
	f.Add(`{"data":"","nonce":"","algorithm":"AES-256-GCM"}`)
	m := NewManager()
	if err := m.Activate(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)), SourceShares); err != nil {
		f.Fatal(err)
	}
	valid, err := m.EncryptForUserWithPolicy("account-a", "test", DecryptPolicy{Mode: DecryptAuthenticated})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Fuzz(func(t *testing.T, input string) {
		_, _ = m.DecryptForUser("account-a", input)
	})
}
