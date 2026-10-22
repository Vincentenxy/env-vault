package masterkey

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	UserPolicyCipherPrefix = "evu:v2:"
	DecryptSelf            = "self"
	DecryptAuthenticated   = "authenticated"
	DecryptUsers           = "users"
	MaxDecryptUsers        = 100
	MaxCryptoUserIDBytes   = 128
	userPolicySaltBytes    = 32
	// The payload includes Base64-encoded content and bounded, JSON-escaped IDs.
	maxUserPolicyPayloadBytes = 256 * 1024
	MaxPolicyCiphertextBytes  = len(UserPolicyCipherPrefix) + ((maxUserPolicyPayloadBytes+userPolicySaltBytes+28)*8+5)/6
	userPolicyPurpose         = "env-vault/user-crypto/v2"
)

var ErrInvalidDecryptPolicy = errors.New("invalid decrypt policy: use self, authenticated, or users with 1-100 valid account IDs")

type DecryptPolicy struct {
	Mode           string
	AllowedUserIDs []string
}

type userCryptoPayload struct {
	OwnerID        string   `json:"ownerId"`
	Mode           string   `json:"decryptMode"`
	AllowedUserIDs []string `json:"allowedUserIds,omitempty"`
	Content        []byte   `json:"content"`
}

// EncryptForUserWithPolicy authenticates both the content and its immutable access policy.
func (m *Manager) EncryptForUserWithPolicy(userID, plaintext string, policy DecryptPolicy) (string, error) {
	if !validCryptoUserID(userID) {
		return "", ErrUserRequired
	}
	if len(plaintext) > MaxUserPlaintextBytes || !utf8.ValidString(plaintext) {
		return "", ErrInvalidPlaintext
	}
	policy, err := normalizeDecryptPolicy(policy)
	if err != nil {
		return "", err
	}
	payload := userCryptoPayload{OwnerID: userID, Mode: policy.Mode, AllowedUserIDs: policy.AllowedUserIDs, Content: []byte(plaintext)}
	defer clear(payload.Content)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	defer clear(encoded)
	if len(encoded) > maxUserPolicyPayloadBytes {
		return "", ErrInvalidPlaintext
	}
	salt := make([]byte, userPolicySaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	aead, err := m.userPolicyAEAD(salt)
	if err != nil {
		return "", err
	}
	sealed := aead.Seal(nil, nil, encoded, []byte(UserPolicyCipherPrefix))
	return UserPolicyCipherPrefix + base64.RawURLEncoding.EncodeToString(append(salt, sealed...)), nil
}

func (m *Manager) decryptUserPolicy(userID, ciphertext string) (string, error) {
	if !validCryptoUserID(userID) {
		return "", ErrUserRequired
	}
	if len(ciphertext) > MaxPolicyCiphertextBytes {
		return "", ErrInvalidCiphertext
	}
	encoded := strings.TrimPrefix(ciphertext, UserPolicyCipherPrefix)
	sealed, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(sealed) < userPolicySaltBytes+28 || base64.RawURLEncoding.EncodeToString(sealed) != encoded {
		return "", ErrInvalidCiphertext
	}
	aead, err := m.userPolicyAEAD(sealed[:userPolicySaltBytes])
	if err != nil {
		return "", err
	}
	data, err := aead.Open(nil, nil, sealed[userPolicySaltBytes:], []byte(UserPolicyCipherPrefix))
	if err != nil {
		return "", ErrInvalidCiphertext
	}
	defer clear(data)
	var payload userCryptoPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", ErrInvalidCiphertext
	}
	defer clear(payload.Content)
	policy, err := normalizeDecryptPolicy(DecryptPolicy{Mode: payload.Mode, AllowedUserIDs: payload.AllowedUserIDs})
	if err != nil || payload.Mode == "" || !validCryptoUserID(payload.OwnerID) ||
		len(payload.Content) > MaxUserPlaintextBytes || !utf8.Valid(payload.Content) {
		return "", ErrInvalidCiphertext
	}
	// The policy is consulted only after GCM authenticates the entire envelope.
	allowed := policy.Mode == DecryptAuthenticated ||
		(policy.Mode == DecryptSelf && userID == payload.OwnerID) ||
		(policy.Mode == DecryptUsers && slices.Contains(policy.AllowedUserIDs, userID))
	if !allowed {
		return "", ErrInvalidCiphertext
	}
	return string(payload.Content), nil
}

func normalizeDecryptPolicy(policy DecryptPolicy) (DecryptPolicy, error) {
	if policy.Mode == "" {
		policy.Mode = DecryptSelf
	}
	switch policy.Mode {
	case DecryptSelf, DecryptAuthenticated:
		if len(policy.AllowedUserIDs) != 0 {
			return DecryptPolicy{}, ErrInvalidDecryptPolicy
		}
	case DecryptUsers:
		if len(policy.AllowedUserIDs) == 0 || len(policy.AllowedUserIDs) > MaxDecryptUsers {
			return DecryptPolicy{}, ErrInvalidDecryptPolicy
		}
		ids := make([]string, 0, len(policy.AllowedUserIDs))
		for _, id := range policy.AllowedUserIDs {
			if !validCryptoUserID(id) {
				return DecryptPolicy{}, ErrInvalidDecryptPolicy
			}
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		policy.AllowedUserIDs = ids
	default:
		return DecryptPolicy{}, ErrInvalidDecryptPolicy
	}
	return policy, nil
}

func validCryptoUserID(id string) bool {
	return id != "" && len(id) <= MaxCryptoUserIDBytes && utf8.ValidString(id) &&
		strings.TrimSpace(id) == id && strings.IndexFunc(id, unicode.IsControl) == -1
}

func (m *Manager) userPolicyAEAD(salt []byte) (cipher.AEAD, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cipher == nil {
		return nil, ErrNotReady
	}
	// A fresh 256-bit salt gives every envelope an independent subkey; this purpose
	// cannot derive the keys used by v1 or by internal Secret storage.
	key, err := hkdf.Key(sha256.New, m.masterKey, salt, userPolicyPurpose, 32)
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
