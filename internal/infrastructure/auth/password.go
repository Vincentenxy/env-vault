// Package auth 提供本地认证使用的密码哈希和 JWT 密钥能力
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 19 * 1024
	argonIterations  = 2
	argonParallelism = 1
	argonSaltLength  = 16
	argonKeyLength   = 32
	maxPasswordBytes = 1024
)

var ErrInvalidPasswordHash = errors.New("invalid password hash")

// PasswordHasher 使用 Argon2id PHC 格式生成和验证不可逆密码哈希
type PasswordHasher struct {
	dummyHash string
}

// NewPasswordHasher 创建密码哈希器，并生成不存在用户验证时使用的伪哈希
func NewPasswordHasher() (*PasswordHasher, error) {
	hasher := &PasswordHasher{}
	dummy, err := hasher.Hash("env-vault-dummy-password")
	if err != nil {
		return nil, err
	}
	hasher.dummyHash = dummy
	return hasher, nil
}

// Hash 为密码生成带独立随机 Salt 和参数的 Argon2id PHC 字符串
func (h *PasswordHasher) Hash(password string) (string, error) {
	if password == "" || len([]byte(password)) > maxPasswordBytes {
		return "", errors.New("password length is invalid")
	}

	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	defer clear(salt)

	hash := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	defer clear(hash)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory,
		argonIterations,
		argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// Verify 验证密码；空哈希仍执行一次 Argon2id，避免暴露用户是否存在
func (h *PasswordHasher) Verify(password, encodedHash string) (bool, error) {
	if len([]byte(password)) > maxPasswordBytes {
		return false, nil
	}
	usedDummy := strings.TrimSpace(encodedHash) == ""
	if usedDummy {
		encodedHash = h.dummyHash
	}

	params, salt, expected, err := parsePasswordHash(encodedHash)
	if err != nil {
		return false, err
	}
	defer clear(salt)
	defer clear(expected)

	actual := argon2.IDKey([]byte(password), salt, params.iterations, params.memory, params.parallelism, uint32(len(expected)))
	defer clear(actual)
	matched := subtle.ConstantTimeCompare(actual, expected) == 1
	return matched && !usedDummy, nil
}

type argonParams struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
}

func parsePasswordHash(encoded string) (argonParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return argonParams{}, nil, nil, ErrInvalidPasswordHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return argonParams{}, nil, nil, ErrInvalidPasswordHash
	}

	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil ||
		memory == 0 || memory > 256*1024 || iterations == 0 || iterations > 10 || parallelism == 0 || parallelism > 16 {
		return argonParams{}, nil, nil, ErrInvalidPasswordHash
	}

	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		clear(salt)
		return argonParams{}, nil, nil, ErrInvalidPasswordHash
	}
	hash, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(hash) < 16 || len(hash) > 64 {
		clear(salt)
		clear(hash)
		return argonParams{}, nil, nil, ErrInvalidPasswordHash
	}
	return argonParams{memory: memory, iterations: iterations, parallelism: parallelism}, salt, hash, nil
}
