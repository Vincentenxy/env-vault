package masterkey

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mathrand "math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"env-vault/pkg/logger"
)

const (
	peerTransferPath       = "/internal/v1/masterKey/transfer"
	peerRecoveryRSAKeyBits = 2048
	maxPeerResponseBody    = 32 * 1024
)

var (
	// ErrInvalidPeerRecoveryConfig 表示 Peer 自动恢复配置不完整或不安全
	ErrInvalidPeerRecoveryConfig = errors.New("invalid peer recovery config")
	// ErrPeerRecoveryRejected 表示 Peer 拒绝了当前恢复请求
	ErrPeerRecoveryRejected = errors.New("peer recovery rejected")
	// ErrPeerRecoveryProtocol 表示 Peer 响应不符合内部传输协议
	ErrPeerRecoveryProtocol = errors.New("invalid peer recovery response")
)

// PeerRecoveryConfig 定义从 Ready Pod 自动恢复主密钥所需的运行参数
type PeerRecoveryConfig struct {
	Enabled              bool
	BaseURL              string
	Token                string
	InstanceID           string
	RequestTimeout       time.Duration
	InitialRetryInterval time.Duration
	MaxRetryInterval     time.Duration
}

// PeerRecovery 从 Kubernetes Ready Service 获取加密主密钥并激活当前实例
type PeerRecovery struct {
	manager              *Manager
	enabled              bool
	endpoint             string
	token                string
	instanceID           string
	initialRetryInterval time.Duration
	maxRetryInterval     time.Duration
	client               *http.Client
}

type peerRecoveryResponse struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type retryablePeerError struct {
	err error
}

func (e *retryablePeerError) Error() string {
	return e.err.Error()
}

func (e *retryablePeerError) Unwrap() error {
	return e.err
}

// NewPeerRecovery 校验配置并创建 Peer 自动恢复模块
func NewPeerRecovery(manager *Manager, cfg PeerRecoveryConfig) (*PeerRecovery, error) {
	if manager == nil {
		return nil, fmt.Errorf("%w: manager is required", ErrInvalidPeerRecoveryConfig)
	}

	recovery := &PeerRecovery{manager: manager, enabled: cfg.Enabled}
	if !cfg.Enabled {
		return recovery, nil
	}

	baseURL, err := normalizePeerBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return nil, fmt.Errorf("%w: token is required", ErrInvalidPeerRecoveryConfig)
	}
	instanceID := strings.TrimSpace(cfg.InstanceID)
	if instanceID == "" || len(instanceID) > 128 {
		return nil, fmt.Errorf("%w: instance ID is required and must not exceed 128 bytes", ErrInvalidPeerRecoveryConfig)
	}
	if cfg.RequestTimeout <= 0 {
		return nil, fmt.Errorf("%w: request timeout must be positive", ErrInvalidPeerRecoveryConfig)
	}
	if cfg.InitialRetryInterval <= 0 || cfg.MaxRetryInterval <= 0 || cfg.InitialRetryInterval > cfg.MaxRetryInterval {
		return nil, fmt.Errorf("%w: retry intervals are invalid", ErrInvalidPeerRecoveryConfig)
	}

	recovery.endpoint = baseURL + peerTransferPath
	recovery.token = token
	recovery.instanceID = instanceID
	recovery.initialRetryInterval = cfg.InitialRetryInterval
	recovery.maxRetryInterval = cfg.MaxRetryInterval
	recovery.client = &http.Client{Timeout: cfg.RequestTimeout}
	return recovery, nil
}

// Run 持续尝试从任意 Ready Peer 恢复主密钥，成功或上下文取消后返回
func (r *PeerRecovery) Run(ctx context.Context) error {
	if !r.enabled || r.manager.Ready() {
		return nil
	}

	privateKey, err := rsa.GenerateKey(cryptorand.Reader, peerRecoveryRSAKeyBits)
	if err != nil {
		return fmt.Errorf("generate temporary peer key: %w", err)
	}
	publicKey, err := encodePeerPublicKey(&privateKey.PublicKey)
	if err != nil {
		return err
	}

	logger.Info(ctx, "master key peer recovery started", zap.String("instanceId", r.instanceID))
	retryInterval := r.initialRetryInterval
	for {
		if r.manager.Ready() {
			return nil
		}

		err = r.recoverOnce(ctx, privateKey, publicKey)
		if err == nil {
			logger.Info(ctx, "master key recovered from peer", zap.String("instanceId", r.instanceID))
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		var retryable *retryablePeerError
		if !errors.As(err, &retryable) {
			return err
		}
		logger.Warn(ctx, "master key peer recovery retrying",
			zap.String("instanceId", r.instanceID),
			zap.Duration("retryAfter", retryInterval),
			zap.Error(err),
		)
		if err := waitForPeerRetry(ctx, jitterPeerRetry(retryInterval)); err != nil {
			return err
		}
		retryInterval = nextPeerRetryInterval(retryInterval, r.maxRetryInterval)
	}
}

func (r *PeerRecovery) recoverOnce(ctx context.Context, privateKey *rsa.PrivateKey, publicKey string) error {
	requestID := uuid.NewString()
	payload, err := json.Marshal(TransferRequest{
		InstanceID: r.instanceID,
		RequestID:  requestID,
		PublicKey:  publicKey,
		Algorithm:  MasterKeyTransferAlgorithm,
	})
	if err != nil {
		return fmt.Errorf("marshal peer recovery request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create peer recovery request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(InternalPeerTokenHeader, r.token)

	resp, err := r.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &retryablePeerError{err: fmt.Errorf("request ready peer: %w", err)}
	}
	defer resp.Body.Close()

	body, err := readPeerResponseBody(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("%w: HTTP status %d", ErrPeerRecoveryRejected, resp.StatusCode)
		if isRetryablePeerStatus(resp.StatusCode) {
			return &retryablePeerError{err: err}
		}
		return err
	}

	var envelope peerRecoveryResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("%w: decode envelope", ErrPeerRecoveryProtocol)
	}
	if envelope.Code != 0 {
		return fmt.Errorf("%w: response code %d", ErrPeerRecoveryRejected, envelope.Code)
	}

	var transfer TransferResponse
	if err := json.Unmarshal(envelope.Data, &transfer); err != nil {
		return fmt.Errorf("%w: decode transfer data", ErrPeerRecoveryProtocol)
	}
	if transfer.RequestID != requestID {
		return fmt.Errorf("%w: request ID mismatch", ErrPeerRecoveryProtocol)
	}
	if transfer.Algorithm != MasterKeyTransferAlgorithm {
		return fmt.Errorf("%w: algorithm mismatch", ErrPeerRecoveryProtocol)
	}

	err = r.manager.ActivateWrappedKey(privateKey, WrappedMasterKey{
		EncryptedMasterKey: transfer.EncryptedMasterKey,
		KeyFingerprint:     transfer.KeyFingerprint,
		Algorithm:          transfer.Algorithm,
	})
	if errors.Is(err, ErrAlreadyActivated) && r.manager.Ready() {
		return nil
	}
	if err != nil {
		return fmt.Errorf("activate peer master key: %w", err)
	}
	return nil
}

func normalizePeerBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("%w: base URL must use HTTP or HTTPS", ErrInvalidPeerRecoveryConfig)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("%w: base URL must not contain credentials, path, query, or fragment", ErrInvalidPeerRecoveryConfig)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func encodePeerPublicKey(publicKey *rsa.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return "", fmt.Errorf("marshal temporary peer public key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(der), nil
}

func readPeerResponseBody(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxPeerResponseBody+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read response", ErrPeerRecoveryProtocol)
	}
	if len(body) > maxPeerResponseBody {
		return nil, fmt.Errorf("%w: response body is too large", ErrPeerRecoveryProtocol)
	}
	return body, nil
}

func isRetryablePeerStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

func waitForPeerRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func nextPeerRetryInterval(current, maximum time.Duration) time.Duration {
	if current >= maximum || current > maximum/2 {
		return maximum
	}
	return current * 2
}

func jitterPeerRetry(delay time.Duration) time.Duration {
	span := delay / 5
	if span <= 0 {
		return delay
	}
	return delay - span + time.Duration(mathrand.Int64N(int64(2*span)+1))
}
