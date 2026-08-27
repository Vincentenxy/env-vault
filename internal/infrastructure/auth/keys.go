package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
)

// LoadKeyMaterial 从内联配置或挂载文件读取密钥，文件配置优先
func LoadKeyMaterial(inline, file string) ([]byte, error) {
	if path := strings.TrimSpace(file); path != "" {
		value, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read key file %s: %w", path, err)
		}
		return value, nil
	}
	if value := strings.TrimSpace(inline); value != "" {
		return []byte(value), nil
	}
	return nil, errors.New("key material is empty")
}

// ParseRSAPrivateKey 解析 PEM 或 Base64 DER 编码的 RSA 私钥
func ParseRSAPrivateKey(value []byte) (*rsa.PrivateKey, error) {
	der := decodePEMOrBase64(value)
	if len(der) == 0 {
		return nil, errors.New("RSA private key is invalid")
	}
	if key, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, rsaKey.Validate()
		}
	}
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, key.Validate()
	}
	return nil, errors.New("RSA private key is invalid")
}

// ParseRSAPublicKey 解析 PEM 或 Base64 DER 编码的 RSA 公钥
func ParseRSAPublicKey(value []byte) (*rsa.PublicKey, error) {
	der := decodePEMOrBase64(value)
	if len(der) == 0 {
		return nil, errors.New("RSA public key is invalid")
	}
	if key, err := x509.ParsePKIXPublicKey(der); err == nil {
		if rsaKey, ok := key.(*rsa.PublicKey); ok {
			return rsaKey, nil
		}
	}
	if key, err := x509.ParsePKCS1PublicKey(der); err == nil {
		return key, nil
	}
	return nil, errors.New("RSA public key is invalid")
}

// GenerateRSAKeyPair 生成 PKCS#8 私钥和 PKIX 公钥 PEM
func GenerateRSAKeyPair(bits int) (privatePEM, publicPEM []byte, err error) {
	if bits < 2048 {
		return nil, nil, errors.New("RSA key size must be at least 2048 bits")
	}
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return nil, nil, fmt.Errorf("generate RSA key: %w", err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal RSA private key: %w", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal RSA public key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), nil
}

func decodePEMOrBase64(value []byte) []byte {
	trimmed := strings.TrimSpace(string(value))
	if block, _ := pem.Decode([]byte(trimmed)); block != nil {
		return block.Bytes
	}
	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil
	}
	return decoded
}
