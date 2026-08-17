package crypto

import (
	"encoding/base64"
	"strings"
	"testing"
)

// testKeyBase64 测试用 32 字节密钥（base64）
const testKeyBase64 = "Pk6V+TnUEZO6R8WOklCSrI/iM4QKHc55VQQrrptmVfk="

func TestEncryptDecrypt_Roundtrip(t *testing.T) {
	c, err := New(testKeyBase64)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	plaintext := "mysql://root:secret@prod:3306/db"
	ciphertext, err := c.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if ciphertext == plaintext || strings.Contains(ciphertext, plaintext) {
		t.Fatalf("ciphertext must not contain plaintext: %s", ciphertext)
	}
	// 信封结构包含 data/nonce/algorithm 三个字段
	for _, field := range []string{`"data"`, `"nonce"`, `"algorithm"`, AlgorithmAES256GCM} {
		if !strings.Contains(ciphertext, field) {
			t.Fatalf("ciphertext missing %s: %s", field, ciphertext)
		}
	}

	got, err := c.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != plaintext {
		t.Fatalf("expected %q, got %q", plaintext, got)
	}
}

func TestEncrypt_RandomNonceEachTime(t *testing.T) {
	c, err := New(testKeyBase64)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a, _ := c.Encrypt("same-value")
	b, _ := c.Encrypt("same-value")
	if a == b {
		t.Fatalf("same plaintext must produce different ciphertext (random nonce)")
	}
}

func TestNew_InvalidKeyLength(t *testing.T) {
	// 16 字节密钥不合法（AES-256 需要 32 字节）
	shortKey := base64.StdEncoding.EncodeToString(make([]byte, 16))
	if _, err := New(shortKey); err == nil {
		t.Fatal("expected error for 16-byte key")
	}
	// 非 base64 字符串不合法
	if _, err := New("not-valid-base64!!!"); err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestDecrypt_InvalidEnvelope(t *testing.T) {
	c, err := New(testKeyBase64)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Decrypt("not json"); err == nil {
		t.Fatal("expected error for non-json ciphertext")
	}
	if _, err := c.Decrypt(`{"data":"AA==","nonce":"AA==","algorithm":"DES"}`); err == nil {
		t.Fatal("expected error for unsupported algorithm")
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	c1, _ := New(testKeyBase64)
	otherKey := base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012")) // 32 字节
	c2, err := New(otherKey)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ciphertext, err := c1.Encrypt("secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := c2.Decrypt(ciphertext); err == nil {
		t.Fatal("expected error when decrypting with wrong key")
	}
}
