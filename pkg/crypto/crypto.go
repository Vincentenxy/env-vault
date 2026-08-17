// Package crypto 提供 AES-256-GCM 对称加解密能力（secret value 密文存储）。
//
// 密文存储格式为 JSON 信封：{"data": "<base64 密文>", "nonce": "<base64 nonce>", "algorithm": "AES-256-GCM"}，
// 由 value_ciphertext 列整体存储，私钥通过配置 security.encryption_key 注入。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

// AlgorithmAES256GCM 固定算法标识
const AlgorithmAES256GCM = "AES-256-GCM"

// envelope 密文 JSON 信封结构
type envelope struct {
	Data      string `json:"data"`      // base64 密文
	Nonce     string `json:"nonce"`     // base64 nonce
	Algorithm string `json:"algorithm"` // 固定 AES-256-GCM
}

// Cipher AES-256-GCM 加解密器（密钥由配置注入）
type Cipher struct {
	aead cipher.AEAD
}

// New 创建加解密器，keyBase64 为 32 字节密钥的 base64 编码
func New(keyBase64 string) (*Cipher, error) {
	key, err := base64.StdEncoding.DecodeString(keyBase64)
	if err != nil {
		return nil, fmt.Errorf("decode encryption key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt 加密明文，返回 JSON 信封字符串（data/nonce/algorithm）
func (c *Cipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	sealed := c.aead.Seal(nil, nonce, []byte(plaintext), nil)
	raw, err := json.Marshal(envelope{
		Data:      base64.StdEncoding.EncodeToString(sealed),
		Nonce:     base64.StdEncoding.EncodeToString(nonce),
		Algorithm: AlgorithmAES256GCM,
	})
	if err != nil {
		return "", fmt.Errorf("marshal envelope: %w", err)
	}
	return string(raw), nil
}

// Decrypt 解密 JSON 信封字符串，返回明文；密文格式非法或密钥不匹配时返回错误
func (c *Cipher) Decrypt(cipherJSON string) (string, error) {
	var env envelope
	if err := json.Unmarshal([]byte(cipherJSON), &env); err != nil {
		return "", fmt.Errorf("unmarshal envelope: %w", err)
	}
	if env.Algorithm != AlgorithmAES256GCM {
		return "", fmt.Errorf("unsupported algorithm: %s", env.Algorithm)
	}

	sealed, err := base64.StdEncoding.DecodeString(env.Data)
	if err != nil {
		return "", fmt.Errorf("decode data: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(env.Nonce)
	if err != nil {
		return "", fmt.Errorf("decode nonce: %w", err)
	}

	plaintext, err := c.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt failed (wrong key or corrupted data): %w", err)
	}
	return string(plaintext), nil
}
