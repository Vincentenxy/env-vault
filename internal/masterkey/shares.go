package masterkey

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/hashicorp/vault/shamir"
)

const (
	// TotalShares 表示系统主密钥生成的分片总数
	TotalShares = 5
	// RequiredShares 表示恢复系统主密钥需要的分片数
	RequiredShares = 3

	// AES-256 主密钥固定为 32 字节
	masterKeySize = 32
	// shareTokenPrefix 同时标识 Token 类型和编码协议版本
	shareTokenPrefix = "EVS1."
)

var (
	// ErrInvalidShareCount 分片数量不符合恢复要求
	ErrInvalidShareCount = errors.New("exactly three shares are required")
	// ErrInvalidShare 分片格式或内容不合法
	ErrInvalidShare = errors.New("invalid master key share")
	// ErrShareSetMismatch 分片来自不同的生成批次
	ErrShareSetMismatch = errors.New("master key shares belong to different sets")
	// ErrDuplicateShare 分片编号重复
	ErrDuplicateShare = errors.New("duplicate master key share")
)

// shareContent 表示参与完整性校验的稳定字段
type shareContent struct {
	KeySetID string `json:"keySetId"` // 本次生成五份分片时使用的唯一批次 ID
	Index    int    `json:"index"`    // Shamir 原始分片携带的坐标编号
	Data     string `json:"data"`     // Base64 编码的 Shamir 原始分片
}

// shareEnvelope 表示 Base64URL 外层包装前的 JSON 结构
type shareEnvelope struct {
	KeySetID string `json:"keySetId"` // 本次生成五份分片时使用的唯一批次 ID
	Index    int    `json:"index"`    // Shamir 原始分片携带的坐标编号
	Data     string `json:"data"`     // Base64 编码的 Shamir 原始分片
	Checksum string `json:"checksum"` // 版本和内容字段的 SHA-256 校验值
}

// decodedShare 表示完成格式和完整性校验后的内部数据
type decodedShare struct {
	keySetID uuid.UUID // 已解析的分片生成批次 ID
	index    byte      // 已校验的分片编号
	data     []byte    // 可直接交给 Shamir Combine 的原始数据
}

// RestoreShares 使用同一批次的任意三个分片恢复并激活主密钥
func (m *Manager) RestoreShares(tokens []string) error {
	// 已经激活时优先拒绝请求，避免继续解析外部提交的敏感内容
	if m.Ready() {
		return ErrAlreadyActivated
	}
	// 本期采用一次提交任意三份的模式，不在内存累计部分分片
	if len(tokens) != RequiredShares {
		return ErrInvalidShareCount
	}

	// 逐份解析 Token，同时校验三份数据属于同一批次且编号不重复
	parts := make([][]byte, 0, RequiredShares)
	seenIndexes := make(map[byte]struct{}, RequiredShares)
	var keySetID uuid.UUID
	for i, token := range tokens {
		share, err := decodeShareToken(token)
		if err != nil {
			return fmt.Errorf("decode share %d: %w", i+1, err)
		}
		if i == 0 {
			keySetID = share.keySetID
		} else if share.keySetID != keySetID {
			return ErrShareSetMismatch
		}
		if _, exists := seenIndexes[share.index]; exists {
			return ErrDuplicateShare
		}
		seenIndexes[share.index] = struct{}{}
		parts = append(parts, share.data)
	}

	// HashiCorp Shamir Combine 根据任意三个有效坐标恢复原始字节
	key, err := shamir.Combine(parts)
	if err != nil {
		return fmt.Errorf("combine master key shares: %w", ErrInvalidShare)
	}
	// 恢复结果完成激活后尽快清理当前函数持有的字节切片
	defer clear(key)
	// Shamir 支持任意长度数据，此处额外限制为系统使用的 AES-256 密钥长度
	if len(key) != masterKeySize {
		return fmt.Errorf("%w: recovered key must be %d bytes", ErrInvalidShare, masterKeySize)
	}

	// 复用 Manager 的并发和单次激活保护，来源记录为分片恢复
	return m.Activate(base64.StdEncoding.EncodeToString(key), SourceShares)
}

// encodeShareToken 将系统生成的原始分片编码为可传输的版本化 Token
func encodeShareToken(keySetID uuid.UUID, data []byte) (string, error) {
	// 生成端只允许编码非空批次和 32 字节主密钥对应的原始分片
	if keySetID == uuid.Nil || len(data) != masterKeySize+shamir.ShareOverhead {
		return "", ErrInvalidShare
	}
	// HashiCorp Shamir 将坐标编号放在原始分片的最后一个字节
	index := data[len(data)-1]
	if index == 0 {
		return "", ErrInvalidShare
	}

	// 校验值只覆盖稳定内容字段，避免把 checksum 自身包含进计算
	content := shareContent{
		KeySetID: keySetID.String(),
		Index:    int(index),
		Data:     base64.StdEncoding.EncodeToString(data),
	}
	envelope := shareEnvelope{
		KeySetID: content.KeySetID,
		Index:    content.Index,
		Data:     content.Data,
		Checksum: hex.EncodeToString(shareChecksum(content)),
	}
	// JSON 提供结构化字段，Base64URL 将 JSON 包装成单行可粘贴 Token
	payload, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("encode master key share: %w", err)
	}
	return shareTokenPrefix + base64.RawURLEncoding.EncodeToString(payload), nil
}

// decodeShareToken 将外部提交的 Token 严格解析为 Shamir 原始分片
func decodeShareToken(token string) (*decodedShare, error) {
	// 允许管理员粘贴时带首尾空白，但不接受未知协议版本
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, shareTokenPrefix) {
		return nil, fmt.Errorf("%w: unsupported token version", ErrInvalidShare)
	}

	// 第一层解码 Base64URL，第二层使用严格 JSON 解析字段
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, shareTokenPrefix))
	if err != nil {
		return nil, fmt.Errorf("%w: decode token", ErrInvalidShare)
	}
	var envelope shareEnvelope
	if err := decodeStrictJSON(payload, &envelope); err != nil {
		return nil, fmt.Errorf("%w: decode payload", ErrInvalidShare)
	}

	// 批次 ID 必须是非空且使用标准小写 UUID 字符串
	keySetID, err := uuid.Parse(envelope.KeySetID)
	if err != nil || keySetID == uuid.Nil || keySetID.String() != envelope.KeySetID {
		return nil, fmt.Errorf("%w: invalid key set ID", ErrInvalidShare)
	}
	// GF(256) 中有效的非零分片编号范围为 1 到 255
	if envelope.Index < 1 || envelope.Index > 255 {
		return nil, fmt.Errorf("%w: invalid share index", ErrInvalidShare)
	}

	// 使用常量时间比较检查内容是否在保存或传输过程中损坏
	content := shareContent{
		KeySetID: envelope.KeySetID,
		Index:    envelope.Index,
		Data:     envelope.Data,
	}
	providedChecksum, err := hex.DecodeString(envelope.Checksum)
	if err != nil || len(providedChecksum) != sha256.Size || subtle.ConstantTimeCompare(providedChecksum, shareChecksum(content)) != 1 {
		return nil, fmt.Errorf("%w: checksum mismatch", ErrInvalidShare)
	}

	// 32 字节密钥的 Shamir 原始分片包含 32 字节数据和 1 字节编号
	data, err := base64.StdEncoding.DecodeString(envelope.Data)
	if err != nil || len(data) != masterKeySize+shamir.ShareOverhead {
		return nil, fmt.Errorf("%w: invalid share data", ErrInvalidShare)
	}
	// 外层 index 必须与原始分片末尾编号一致，防止元数据和分片错配
	if data[len(data)-1] != byte(envelope.Index) {
		return nil, fmt.Errorf("%w: share index mismatch", ErrInvalidShare)
	}

	return &decodedShare{
		keySetID: keySetID,
		index:    byte(envelope.Index),
		data:     data,
	}, nil
}

// shareChecksum 计算 Token 版本和稳定内容字段的完整性校验值
func shareChecksum(content shareContent) []byte {
	// shareContent 只包含不会变化的基础类型，JSON 序列化不会失败
	payload, _ := json.Marshal(content)
	// 版本前缀加入摘要，防止未来不同协议版本复用同一个校验结果
	hash := sha256.New()
	_, _ = hash.Write([]byte(shareTokenPrefix))
	_, _ = hash.Write(payload)
	return hash.Sum(nil)
}

// decodeStrictJSON 拒绝未知字段和尾随 JSON，避免错误 Token 被宽松解析
func decodeStrictJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	// Token 协议通过 EVS1 版本演进，当前版本不接受额外字段
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	// 第一次 Decode 后必须直接到达输入末尾
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON content")
	}
	return nil
}
