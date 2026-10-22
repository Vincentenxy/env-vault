package masterkey

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestUserCryptoPolicies(t *testing.T) {
	m := userCryptoManager(t, 1)
	restored := userCryptoManager(t, 1)
	for _, tc := range []struct {
		name   string
		policy DecryptPolicy
		allow  []string
		deny   []string
	}{
		{"default", DecryptPolicy{}, []string{"owner"}, []string{"reader", "stranger"}},
		{"self", DecryptPolicy{Mode: DecryptSelf}, []string{"owner"}, []string{"reader"}},
		{"authenticated", DecryptPolicy{Mode: DecryptAuthenticated}, []string{"owner", "reader", "stranger"}, nil},
		{"users", DecryptPolicy{Mode: DecryptUsers, AllowedUserIDs: []string{"reader", "other", "reader"}}, []string{"reader", "other"}, []string{"owner", "stranger"}},
		{"users including owner", DecryptPolicy{Mode: DecryptUsers, AllowedUserIDs: []string{"reader", "owner"}}, []string{"reader", "owner"}, []string{"stranger"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, input := range []string{"", " \n中文\t\x00 ", strings.Repeat("\x00", MaxUserPlaintextBytes)} {
				encrypted, err := m.EncryptForUserWithPolicy("owner", input, tc.policy)
				if err != nil || !strings.HasPrefix(encrypted, UserPolicyCipherPrefix) {
					t.Fatalf("encrypt: %v", err)
				}
				second, err := m.EncryptForUserWithPolicy("owner", input, tc.policy)
				if err != nil || second == encrypted {
					t.Fatalf("fresh randomness missing: %v", err)
				}
				for _, id := range tc.allow {
					got, err := restored.DecryptForUser(id, encrypted)
					if err != nil || got != input {
						t.Fatalf("%s cannot decrypt restored data: %v", id, err)
					}
					if _, err := userCryptoManager(t, 2).DecryptForUser(id, encrypted); !errors.Is(err, ErrInvalidCiphertext) {
						t.Fatal("different master key must not decrypt")
					}
				}
				for _, id := range tc.deny {
					if got, err := restored.DecryptForUser(id, encrypted); got != "" || !errors.Is(err, ErrInvalidCiphertext) {
						t.Fatalf("%s must not decrypt", id)
					}
				}
				if got, err := restored.DecryptForUser("", encrypted); got != "" || !errors.Is(err, ErrUserRequired) {
					t.Fatal("all modes still require authentication")
				}
			}
		})
	}
}

func TestUserCryptoPolicyIntegrity(t *testing.T) {
	m := userCryptoManager(t, 1)
	encrypted, err := m.EncryptForUserWithPolicy("owner", "protected-content", DecryptPolicy{
		Mode: DecryptUsers, AllowedUserIDs: []string{"reader"},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encrypted, UserPolicyCipherPrefix))
	if err != nil {
		t.Fatal(err)
	}
	// Flip every byte, covering salt, nonce, encrypted owner/policy/content and tag.
	for i := range raw {
		changed := append([]byte(nil), raw...)
		changed[i] ^= 1
		for _, data := range [][]byte{changed, raw[:i]} {
			damaged := UserPolicyCipherPrefix + base64.RawURLEncoding.EncodeToString(data)
			if got, err := m.DecryptForUser("reader", damaged); got != "" || !errors.Is(err, ErrInvalidCiphertext) {
				t.Fatalf("changed/truncated byte %d returned data", i)
			}
		}
	}
	for _, damaged := range []string{
		strings.Replace(encrypted, UserPolicyCipherPrefix, UserCipherPrefix, 1),
		strings.Replace(encrypted, UserPolicyCipherPrefix, "evu:v3:", 1),
		encrypted + "\n", encrypted + "=", UserPolicyCipherPrefix + "AA", UserPolicyCipherPrefix + "!",
		UserPolicyCipherPrefix + strings.Repeat("A", MaxPolicyCiphertextBytes),
	} {
		if got, err := m.DecryptForUser("reader", damaged); got != "" || !errors.Is(err, ErrInvalidCiphertext) {
			t.Fatal("invalid envelope accepted")
		}
	}
	legacy, err := m.EncryptForUser("owner", "legacy-content")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := m.DecryptForUser("owner", legacy); err != nil || got != "legacy-content" {
		t.Fatal("v1 compatibility lost")
	}
	if _, err := m.DecryptForUser("reader", legacy); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatal("v1 ownership restriction lost")
	}
}

func TestUserCryptoPolicyValidation(t *testing.T) {
	m := userCryptoManager(t, 1)
	for _, policy := range []DecryptPolicy{
		{Mode: "public"}, {Mode: "SELF"}, {Mode: DecryptUsers},
		{Mode: DecryptSelf, AllowedUserIDs: []string{"reader"}},
		{Mode: DecryptAuthenticated, AllowedUserIDs: []string{"reader"}},
		{AllowedUserIDs: []string{"reader"}},
		{Mode: DecryptUsers, AllowedUserIDs: []string{""}},
		{Mode: DecryptUsers, AllowedUserIDs: []string{" reader"}},
		{Mode: DecryptUsers, AllowedUserIDs: []string{"a\nb"}},
		{Mode: DecryptUsers, AllowedUserIDs: []string{strings.Repeat("x", MaxCryptoUserIDBytes+1)}},
		{Mode: DecryptUsers, AllowedUserIDs: make([]string, MaxDecryptUsers+1)},
	} {
		if got, err := m.EncryptForUserWithPolicy("owner", "text", policy); got != "" || !errors.Is(err, ErrInvalidDecryptPolicy) {
			t.Fatalf("invalid policy accepted: %v", err)
		}
	}
	for _, plaintext := range []string{strings.Repeat("a", MaxUserPlaintextBytes+1), string([]byte{0xff})} {
		if _, err := m.EncryptForUserWithPolicy("owner", plaintext, DecryptPolicy{}); !errors.Is(err, ErrInvalidPlaintext) {
			t.Fatal(err)
		}
	}
	if _, err := m.EncryptForUserWithPolicy("", "text", DecryptPolicy{}); !errors.Is(err, ErrUserRequired) {
		t.Fatal(err)
	}
	if _, err := NewManager().EncryptForUserWithPolicy("owner", "text", DecryptPolicy{}); !errors.Is(err, ErrNotReady) {
		t.Fatal(err)
	}
}
