package masterkey

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	MaxUserPlaintextBytes = 64 * 1024
	UserCipherPrefix      = "evu:v1:"
	// GCM stores a 12-byte random nonce and a 16-byte authentication tag.
	MaxUserCiphertextBytes = len(UserCipherPrefix) + ((MaxUserPlaintextBytes+28)*8+5)/6
	userCryptoPurpose      = "env-vault/user-crypto/v1"
)

var (
	ErrUserRequired      = errors.New("authenticated user is required")
	ErrInvalidPlaintext  = errors.New("plaintext must be valid UTF-8 and at most 65536 bytes")
	ErrInvalidCiphertext = errors.New("invalid ciphertext or current user is not allowed to decrypt")
)

// EncryptForUser retains the v1 format for legacy internal callers. New API calls use EncryptForUserWithPolicy.
func (m *Manager) EncryptForUser(userID, plaintext string) (string, error) {
	if len(plaintext) > MaxUserPlaintextBytes || !utf8.ValidString(plaintext) {
		return "", ErrInvalidPlaintext
	}
	aead, err := m.userAEAD(userID)
	if err != nil {
		return "", err
	}
	sealed := aead.Seal(nil, nil, []byte(plaintext), []byte(UserCipherPrefix))
	return UserCipherPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// DecryptForUser only accepts this API's versioned envelope, never database envelopes.
func (m *Manager) DecryptForUser(userID, ciphertext string) (string, error) {
	if strings.HasPrefix(ciphertext, UserPolicyCipherPrefix) {
		return m.decryptUserPolicy(userID, ciphertext)
	}
	aead, err := m.userAEAD(userID)
	if err != nil {
		return "", err
	}
	if len(ciphertext) > MaxUserCiphertextBytes || !strings.HasPrefix(ciphertext, UserCipherPrefix) {
		return "", ErrInvalidCiphertext
	}
	encoded := strings.TrimPrefix(ciphertext, UserCipherPrefix)
	sealed, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(sealed) < aead.Overhead() || len(sealed) > MaxUserPlaintextBytes+aead.Overhead() {
		return "", ErrInvalidCiphertext
	}
	// Reject alternate encodings, including embedded newlines accepted by base64 decoders.
	if base64.RawURLEncoding.EncodeToString(sealed) != encoded {
		return "", ErrInvalidCiphertext
	}
	plaintext, err := aead.Open(nil, nil, sealed, []byte(UserCipherPrefix))
	if err != nil {
		return "", ErrInvalidCiphertext
	}
	defer clear(plaintext)
	return string(plaintext), nil
}

func (m *Manager) userAEAD(userID string) (cipher.AEAD, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, ErrUserRequired
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cipher == nil {
		return nil, ErrNotReady
	}
	// The salt separates this capability from storage/recovery; the authenticated ID
	// binds ownership without returning the root key or accepting a caller-selected owner.
	key, err := hkdf.Key(sha256.New, m.masterKey, []byte(userCryptoPurpose), userID, 32)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}
