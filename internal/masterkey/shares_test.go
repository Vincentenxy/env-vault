package masterkey

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/hashicorp/vault/shamir"

	"env-vault/pkg/crypto"
)

func TestManagerRestoreSharesWithAnyThreeShares(t *testing.T) {
	// 使用同一原始密钥生成一组 3-of-5 测试分片
	key := []byte("12345678901234567890123456789012")
	tokens := createShareTokens(t, key, uuid.New())
	sourceCipher, err := crypto.New(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatalf("create source cipher: %v", err)
	}
	ciphertext, err := sourceCipher.Encrypt("shared-secret")
	if err != nil {
		t.Fatalf("encrypt source value: %v", err)
	}

	// 枚举五份分片中全部十种三份组合
	for first := 0; first < TotalShares; first++ {
		for second := first + 1; second < TotalShares; second++ {
			for third := second + 1; third < TotalShares; third++ {
				name := fmt.Sprintf("%d-%d-%d", first, second, third)
				t.Run(name, func(t *testing.T) {
					manager := NewManager()
					// 主动打乱提交顺序，证明恢复逻辑不依赖输入位置
					selected := []string{tokens[third], tokens[first], tokens[second]}
					if err := manager.RestoreShares(selected); err != nil {
						t.Fatalf("RestoreShares: %v", err)
					}
					if status := manager.Status(); !status.Ready || status.Source != SourceShares {
						t.Fatalf("unexpected restored status: %+v", status)
					}
					plaintext, err := manager.Decrypt(ciphertext)
					if err != nil {
						t.Fatalf("Decrypt: %v", err)
					}
					if plaintext != "shared-secret" {
						t.Fatalf("Decrypt = %q, want %q", plaintext, "shared-secret")
					}
				})
			}
		}
	}
}

func TestManagerRestoreSharesRejectsInvalidInput(t *testing.T) {
	// 两个不同 keySetID 用于验证不同批次分片不能混用
	key := []byte("12345678901234567890123456789012")
	firstSet := createShareTokens(t, key, uuid.New())
	secondSet := createShareTokens(t, key, uuid.New())
	shortKeySet := createUncheckedShareTokens(t, []byte("1234567890123456"), uuid.New())

	// 表格覆盖恢复入口的全部主要输入校验
	tests := []struct {
		name   string
		shares []string
		err    error
	}{
		{name: "too few", shares: firstSet[:2], err: ErrInvalidShareCount},
		{name: "too many", shares: firstSet[:4], err: ErrInvalidShareCount},
		{name: "malformed", shares: []string{"invalid", firstSet[1], firstSet[2]}, err: ErrInvalidShare},
		{name: "checksum mismatch", shares: []string{corruptShareToken(t, firstSet[0]), firstSet[1], firstSet[2]}, err: ErrInvalidShare},
		{name: "different sets", shares: []string{firstSet[0], firstSet[1], secondSet[2]}, err: ErrShareSetMismatch},
		{name: "duplicate", shares: []string{firstSet[0], firstSet[0], firstSet[1]}, err: ErrDuplicateShare},
		{name: "wrong key size", shares: shortKeySet[:3], err: ErrInvalidShare},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewManager()
			err := manager.RestoreShares(tt.shares)
			if !errors.Is(err, tt.err) {
				t.Fatalf("RestoreShares error = %v, want %v", err, tt.err)
			}
			if manager.Ready() {
				t.Fatal("invalid shares must not activate manager")
			}
		})
	}
}

func TestManagerRestoreSharesRejectsActivatedManager(t *testing.T) {
	// 配置密钥先激活后，分片恢复不能覆盖当前主密钥
	manager := NewManager()
	if err := manager.LoadConfigFallback(true, testKeyBase64); err != nil {
		t.Fatalf("LoadConfigFallback: %v", err)
	}
	tokens := createShareTokens(t, []byte("12345678901234567890123456789012"), uuid.New())

	if err := manager.RestoreShares(tokens[:RequiredShares]); !errors.Is(err, ErrAlreadyActivated) {
		t.Fatalf("RestoreShares error = %v, want ErrAlreadyActivated", err)
	}
	if source := manager.Status().Source; source != SourceConfig {
		t.Fatalf("source = %q, want %q", source, SourceConfig)
	}
}

func TestManagerSubmitShareAccumulatesAndClearsAfterActivation(t *testing.T) {
	manager := NewManager()
	tokens := createShareTokens(t, []byte("12345678901234567890123456789012"), uuid.New())
	for index, token := range tokens[:RequiredShares] {
		if err := manager.SubmitShare(token); err != nil {
			t.Fatalf("SubmitShare %d: %v", index+1, err)
		}
		status := manager.Status()
		if index < RequiredShares-1 && (status.Ready || status.SubmittedShares != index+1) {
			t.Fatalf("unexpected pending status after %d: %+v", index+1, status)
		}
	}
	status := manager.Status()
	if !status.Ready || status.Source != SourceShares || status.SubmittedShares != 0 {
		t.Fatalf("unexpected activated status: %+v", status)
	}
}

func TestManagerSubmitShareIsConcurrencySafe(t *testing.T) {
	manager := NewManager()
	tokens := createShareTokens(t, []byte("12345678901234567890123456789012"), uuid.New())
	var wait sync.WaitGroup
	errorsByIndex := make([]error, RequiredShares)
	for index := 0; index < RequiredShares; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			errorsByIndex[index] = manager.SubmitShare(tokens[index])
		}(index)
	}
	wait.Wait()
	for index, err := range errorsByIndex {
		if err != nil {
			t.Fatalf("SubmitShare %d: %v", index+1, err)
		}
	}
	if !manager.Ready() {
		t.Fatal("manager must be ready after concurrent distinct shares")
	}
}

func createShareTokens(t *testing.T, key []byte, keySetID uuid.UUID) []string {
	t.Helper()
	// 测试使用与生产恢复逻辑相同的 HashiCorp Shamir 实现
	parts, err := shamir.Split(key, TotalShares, RequiredShares)
	if err != nil {
		t.Fatalf("split key: %v", err)
	}
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		token, err := encodeShareToken(keySetID, part)
		if err != nil {
			t.Fatalf("encode share token: %v", err)
		}
		tokens = append(tokens, token)
	}
	return tokens
}

func createUncheckedShareTokens(t *testing.T, key []byte, keySetID uuid.UUID) []string {
	t.Helper()
	// 绕过生产编码器的 32 字节限制，构造校验完整但密钥长度错误的 Token
	parts, err := shamir.Split(key, TotalShares, RequiredShares)
	if err != nil {
		t.Fatalf("split key: %v", err)
	}
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		content := shareContent{
			KeySetID: keySetID.String(),
			Index:    int(part[len(part)-1]),
			Data:     base64.StdEncoding.EncodeToString(part),
		}
		envelope := shareEnvelope{
			KeySetID: content.KeySetID,
			Index:    content.Index,
			Data:     content.Data,
			Checksum: hex.EncodeToString(shareChecksum(content)),
		}
		payload, err := json.Marshal(envelope)
		if err != nil {
			t.Fatalf("encode envelope: %v", err)
		}
		tokens = append(tokens, shareTokenPrefix+base64.RawURLEncoding.EncodeToString(payload))
	}
	return tokens
}

func corruptShareToken(t *testing.T, token string) string {
	t.Helper()
	// 只修改分片数据且保留原校验值，用于验证损坏检测
	payload, err := base64.RawURLEncoding.DecodeString(token[len(shareTokenPrefix):])
	if err != nil {
		t.Fatalf("decode token: %v", err)
	}
	var envelope shareEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	envelope.Data = base64.StdEncoding.EncodeToString(make([]byte, masterKeySize+shamir.ShareOverhead))
	payload, err = json.Marshal(envelope)
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	return shareTokenPrefix + base64.RawURLEncoding.EncodeToString(payload)
}
