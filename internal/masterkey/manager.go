// Package masterkey 提供系统主密钥的生命周期和动态加解密能力
package masterkey

import (
	"errors"
	"fmt"
	"sync"

	"env-vault/pkg/crypto"
)

var (
	// ErrNotReady 主密钥尚未激活
	ErrNotReady = errors.New("master key is not ready")
	// ErrAlreadyActivated 主密钥已经激活且不允许替换
	ErrAlreadyActivated = errors.New("master key is already activated")
	// ErrInvalidSource 主密钥来源不合法
	ErrInvalidSource = errors.New("master key source is invalid")
)

// Source 表示主密钥的加载来源
type Source string

const (
	// SourceUnknown 表示主密钥尚未激活
	SourceUnknown Source = ""
	// SourceConfig 表示主密钥来自开发环境配置
	SourceConfig Source = "config"
	// SourceShares 表示主密钥由 Shamir 分片恢复
	SourceShares Source = "shares"
	// SourcePeer 表示主密钥来自已经就绪的其他实例
	SourcePeer Source = "peer"
)

// Cryptor 定义 Secret 数据需要的加解密能力
type Cryptor interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// Status 表示当前主密钥状态
type Status struct {
	Ready           bool   // 是否已经加载可用的主密钥
	Source          Source // 当前主密钥的加载来源
	SubmittedShares int    // 当前实例已经累计的合法分片数量
}

// Manager 管理主密钥的单次激活和动态加解密
type Manager struct {
	mu            sync.RWMutex    // 保护激活状态和待恢复分片的并发读写
	cipher        Cryptor         // 激活后创建的 AES-256-GCM 加解密器
	source        Source          // 成功激活当前加解密器的密钥来源
	shareSetID    string          // 第一份合法分片确定的恢复批次
	pendingShares map[byte][]byte // 激活前暂存在当前进程内存中的不同分片
}

// 编译期确认 Manager 实现 Secret 服务需要的加解密接口
var _ Cryptor = (*Manager)(nil)

// NewManager 创建未激活的主密钥管理器
func NewManager() *Manager {
	return &Manager{}
}

// LoadConfigFallback 根据显式开关加载配置文件中的开发密钥
func (m *Manager) LoadConfigFallback(enabled bool, keyBase64 string) error {
	// 生产环境关闭开关时忽略配置文件中的密钥内容
	if !enabled {
		return nil
	}

	// 配置回退和分片恢复共用同一套单次激活保护
	return m.Activate(keyBase64, SourceConfig)
}

// Activate 使用 Base64 编码的 AES-256 密钥激活管理器
func (m *Manager) Activate(keyBase64 string, source Source) error {
	// 未知来源不能用于激活，避免状态接口无法说明密钥来源
	if source == SourceUnknown {
		return ErrInvalidSource
	}

	// 创建和替换 cipher 必须串行，确保并发请求只能有一个成功
	m.mu.Lock()
	defer m.mu.Unlock()

	// 当前阶段不支持在线换密钥，已经激活后禁止覆盖
	if m.cipher != nil {
		return ErrAlreadyActivated
	}

	// crypto.New 同时完成 Base64 解码和 32 字节 AES 密钥长度校验
	cipher, err := crypto.New(keyBase64)
	if err != nil {
		return fmt.Errorf("activate master key: %w", err)
	}

	// cipher 和来源在同一个写锁内提交，对外只会看到完整状态
	m.cipher = cipher
	m.source = source
	m.clearPendingSharesLocked()
	return nil
}

// Ready 返回主密钥是否已经激活
func (m *Manager) Ready() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cipher != nil
}

// Status 返回不包含主密钥内容的运行状态
func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Status{
		Ready:           m.cipher != nil,
		Source:          m.source,
		SubmittedShares: len(m.pendingShares),
	}
}

func (m *Manager) clearPendingSharesLocked() {
	for index, share := range m.pendingShares {
		clear(share)
		delete(m.pendingShares, index)
	}
	m.pendingShares = nil
	m.shareSetID = ""
}

// Encrypt 使用已经激活的主密钥加密明文
func (m *Manager) Encrypt(plaintext string) (string, error) {
	// 每次调用时获取当前加解密器，使服务启动时不必提前持有固定密钥
	cipher, err := m.activeCipher()
	if err != nil {
		return "", err
	}
	return cipher.Encrypt(plaintext)
}

// Decrypt 使用已经激活的主密钥解密密文
func (m *Manager) Decrypt(ciphertext string) (string, error) {
	// 未激活时在进入底层 AES 解密前返回统一错误
	cipher, err := m.activeCipher()
	if err != nil {
		return "", err
	}
	return cipher.Decrypt(ciphertext)
}

// activeCipher 并发安全地读取已经激活的加解密器
func (m *Manager) activeCipher() (Cryptor, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// cipher 是否存在是系统就绪状态的唯一判断依据
	if m.cipher == nil {
		return nil, ErrNotReady
	}
	return m.cipher, nil
}
