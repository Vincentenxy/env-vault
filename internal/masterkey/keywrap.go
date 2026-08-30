package masterkey

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

const (
	// MasterKeyTransferAlgorithm 是集群内部主密钥传输使用的固定算法。
	// 主密钥只有 32 字节，RSA-OAEP-SHA256 可在不暴露明文的情况下完成包装。
	MasterKeyTransferAlgorithm = "RSA-OAEP-SHA256"
	minPeerRSAKeyBits          = 2048
	maxPeerRSAKeyBits          = 16384
)

var (
	// ErrInvalidTransferRequest 表示内部密钥传输请求或密钥信封不合法。
	ErrInvalidTransferRequest = errors.New("invalid master key transfer request")
	// ErrKeyFingerprintMismatch 表示传输结果不是当前主密钥，或结果已被篡改。
	ErrKeyFingerprintMismatch = errors.New("master key fingerprint mismatch")
)

// WrappedMasterKey 是只包含加密密文的主密钥信封。
// EncryptedMasterKey 永远不是主密钥明文，而是使用请求方临时 RSA 公钥加密后的数据。
type WrappedMasterKey struct {
	EncryptedMasterKey string `json:"encryptedMasterKey"`
	KeyFingerprint     string `json:"keyFingerprint"`
	Algorithm          string `json:"algorithm"`
}

// ExportWrappedKey 使用请求方 RSA 公钥包装主密钥。
// 主密钥只在锁内复制一次，包装完成后释放临时副本；接口不会返回 Base64 明文主密钥。
func (m *Manager) ExportWrappedKey(publicKey string) (WrappedMasterKey, error) {
	m.mu.RLock()
	if m.cipher == nil || len(m.masterKey) != masterKeySize {
		m.mu.RUnlock()
		return WrappedMasterKey{}, ErrNotReady
	}
	key := append([]byte(nil), m.masterKey...)
	m.mu.RUnlock()
	defer clear(key)

	peerKey, err := parsePeerPublicKey(publicKey)
	if err != nil {
		return WrappedMasterKey{}, err
	}

	sealed, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, peerKey, key, nil)
	if err != nil {
		return WrappedMasterKey{}, fmt.Errorf("wrap master key: %w", err)
	}
	return WrappedMasterKey{
		EncryptedMasterKey: base64.StdEncoding.EncodeToString(sealed),
		KeyFingerprint:     masterKeyFingerprint(key),
		Algorithm:          MasterKeyTransferAlgorithm,
	}, nil
}

// ActivateWrappedKey 解开内部密钥信封并激活当前实例。
// 解密后的主密钥只在本方法内存活，激活失败也不会改变当前 Manager 状态。
func (m *Manager) ActivateWrappedKey(privateKey *rsa.PrivateKey, wrapped WrappedMasterKey) error {
	key, err := unwrapMasterKey(privateKey, wrapped)
	if err != nil {
		return err
	}
	defer clear(key)

	return m.Activate(base64.StdEncoding.EncodeToString(key), SourcePeer)
}

// decodeMasterKey 解码并校验固定长度的 AES-256 主密钥。
func decodeMasterKey(keyBase64 string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keyBase64))
	if err != nil {
		return nil, fmt.Errorf("%w: decode base64", ErrInvalidMasterKey)
	}
	if len(key) != masterKeySize {
		clear(key)
		return nil, fmt.Errorf("%w: key must be %d bytes", ErrInvalidMasterKey, masterKeySize)
	}
	return key, nil
}

func unwrapMasterKey(privateKey *rsa.PrivateKey, wrapped WrappedMasterKey) ([]byte, error) {
	if err := validatePeerPrivateKey(privateKey); err != nil {
		return nil, err
	}
	if strings.TrimSpace(wrapped.Algorithm) != MasterKeyTransferAlgorithm {
		return nil, fmt.Errorf("%w: unsupported algorithm", ErrInvalidTransferRequest)
	}
	sealed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(wrapped.EncryptedMasterKey))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid encrypted master key", ErrInvalidTransferRequest)
	}
	key, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, privateKey, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: decrypt master key", ErrInvalidTransferRequest)
	}
	if len(key) != masterKeySize {
		clear(key)
		return nil, fmt.Errorf("%w: unexpected master key length", ErrInvalidTransferRequest)
	}

	expected := masterKeyFingerprint(key)
	provided := strings.TrimSpace(wrapped.KeyFingerprint)
	if len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		clear(key)
		return nil, ErrKeyFingerprintMismatch
	}
	return key, nil
}

func parsePeerPublicKey(encoded string) (*rsa.PublicKey, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" || len(encoded) > 16*1024 {
		return nil, fmt.Errorf("%w: public key is empty or too large", ErrInvalidTransferRequest)
	}

	var der []byte
	if block, _ := pem.Decode([]byte(encoded)); block != nil {
		der = block.Bytes
	} else {
		var err error
		der, err = base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			der, err = base64.RawStdEncoding.DecodeString(encoded)
		}
		if err != nil {
			return nil, fmt.Errorf("%w: decode public key", ErrInvalidTransferRequest)
		}
	}

	if publicKey, err := x509.ParsePKIXPublicKey(der); err == nil {
		if rsaKey, ok := publicKey.(*rsa.PublicKey); ok {
			return validatePeerPublicKey(rsaKey)
		}
		return nil, fmt.Errorf("%w: public key is not RSA", ErrInvalidTransferRequest)
	}
	rsaKey, err := x509.ParsePKCS1PublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("%w: parse RSA public key", ErrInvalidTransferRequest)
	}
	return validatePeerPublicKey(rsaKey)
}

func validatePeerPublicKey(key *rsa.PublicKey) (*rsa.PublicKey, error) {
	if key == nil || key.N == nil || key.E <= 1 {
		return nil, fmt.Errorf("%w: malformed RSA public key", ErrInvalidTransferRequest)
	}
	if bits := key.N.BitLen(); bits < minPeerRSAKeyBits || bits > maxPeerRSAKeyBits {
		return nil, fmt.Errorf("%w: RSA key size must be between %d and %d bits", ErrInvalidTransferRequest, minPeerRSAKeyBits, maxPeerRSAKeyBits)
	}
	return key, nil
}

func validatePeerPrivateKey(key *rsa.PrivateKey) error {
	if key == nil || key.N == nil || key.E <= 1 {
		return fmt.Errorf("%w: malformed RSA private key", ErrInvalidTransferRequest)
	}
	if bits := key.N.BitLen(); bits < minPeerRSAKeyBits || bits > maxPeerRSAKeyBits {
		return fmt.Errorf("%w: RSA key size must be between %d and %d bits", ErrInvalidTransferRequest, minPeerRSAKeyBits, maxPeerRSAKeyBits)
	}
	return nil
}

func masterKeyFingerprint(key []byte) string {
	digest := sha256.Sum256(key)
	return "sha256:" + hex.EncodeToString(digest[:])
}
