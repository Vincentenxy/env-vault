package masterkey

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	auditapp "env-vault/internal/application/audit"
	auditdomain "env-vault/internal/domain/audit"
	"env-vault/internal/interfaces/auditctx"
	"env-vault/pkg/logger"
	"env-vault/pkg/response"
)

const maxShareRequestBodySize = 16 * 1024

const (
	// InternalPeerTokenHeader 是当前阶段内部 Pod 身份校验使用的独立请求头。
	// 后续接入 mTLS 后，该令牌校验可替换为证书身份校验。
	InternalPeerTokenHeader    = "X-Env-Vault-Internal-Token"
	maxTransferRequestBodySize = 16 * 1024
)

// StatusResponse 表示可以安全返回给外部的主密钥状态
type StatusResponse struct {
	Ready           bool   `json:"ready"`           // 系统主密钥是否已经激活
	Source          Source `json:"source"`          // 当前主密钥的加载来源
	TotalShares     int    `json:"totalShares"`     // 系统生成的分片总数
	RequiredShares  int    `json:"requiredShares"`  // 恢复主密钥需要的分片数
	SubmittedShares int    `json:"submittedShares"` // 当前实例已经累计的分片数
	CanSubmit       bool   `json:"canSubmit"`       // 当前状态是否仍允许提交分片
}

// SubmitShareRequest 表示管理员单次提交的一份密钥分片
type SubmitShareRequest struct {
	Share string `json:"share"`
}

// TransferRequest 表示已就绪实例向当前实例请求主密钥信封。
// PublicKey 是请求方临时 RSA 公钥，可使用 PEM 或 Base64 DER 编码。
type TransferRequest struct {
	InstanceID string `json:"instanceId"`
	RequestID  string `json:"requestId"`
	PublicKey  string `json:"publicKey"`
	Algorithm  string `json:"algorithm"`
}

// TransferResponse 表示使用请求方临时公钥加密后的主密钥信封。
type TransferResponse struct {
	RequestID          string `json:"requestId"`
	EncryptedMasterKey string `json:"encryptedMasterKey"`
	KeyFingerprint     string `json:"keyFingerprint"`
	Algorithm          string `json:"algorithm"`
}

// HTTPHandler 提供主密钥启动阶段使用的受认证接口
type HTTPHandler struct {
	manager       *Manager
	auditRecorder auditdomain.Recorder
}

// NewHTTPHandler 创建主密钥 HTTP 处理器
func NewHTTPHandler(manager *Manager, recorders ...auditdomain.Recorder) *HTTPHandler {
	handler := &HTTPHandler{manager: manager}
	if len(recorders) > 0 {
		handler.auditRecorder = recorders[0]
	}
	return handler
}

// RegisterRoutes 在受认证路由组中注册主密钥接口
func RegisterRoutes(group *gin.RouterGroup, manager *Manager, recorders ...auditdomain.Recorder) {
	// 主密钥是独立系统资源，状态查询和分片提交共用同一个资源前缀
	handler := NewHTTPHandler(manager, recorders...)
	masterKeyGroup := group.Group("/masterKey")
	masterKeyGroup.GET("/status", handler.GetStatus)
	masterKeyGroup.POST("/share", handler.SubmitShare)
}

// RegisterInternalRoutes 注册仅供集群内部实例调用的主密钥传输接口。
// 该路由不挂载用户 JWT；当前使用独立内部令牌，后续可替换为 mTLS 身份校验。
func RegisterInternalRoutes(group *gin.RouterGroup, manager *Manager, peerToken string, recorders ...auditdomain.Recorder) {
	handler := NewHTTPHandler(manager, recorders...)
	group.Use(requireInternalPeerToken(peerToken))
	group.GET("/masterKey/ready", handler.Ready)
	group.POST("/masterKey/transfer", handler.Transfer)
}

// Ready 提供给 Kubernetes readinessProbe 的内部就绪检查，不返回主密钥或指纹。
func (h *HTTPHandler) Ready(c *gin.Context) {
	if !h.manager.Ready() {
		response.AbortWithHTTPStatusMessage(c, http.StatusServiceUnavailable, "系统主密钥尚未激活")
		return
	}
	response.Success(c, gin.H{"status": "ready"})
}

// GetStatus 查询不包含任何密钥和分片内容的运行状态
func (h *HTTPHandler) GetStatus(c *gin.Context) {
	status := h.statusResponse()
	if err := h.record(c, "masterKey.status.read", auditdomain.ResultSuccess, status, nil); err != nil {
		logger.Error(c, "record master key status audit failed", zap.Error(err))
		response.Error(c, "internal error")
		return
	}
	response.Success(c, status)
}

// SubmitShare 校验并累计一份分片，达到阈值时恢复并激活主密钥
func (h *HTTPHandler) SubmitShare(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxShareRequestBodySize)

	var req SubmitShareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		if auditErr := h.record(c, "masterKey.share.submit", auditdomain.ResultFailure, h.statusResponse(), ErrInvalidShare); auditErr != nil {
			logger.Error(c, "record invalid master key share audit failed", zap.Error(auditErr))
			response.Error(c, "internal error")
			return
		}
		response.BadRequest(c, err)
		return
	}
	if strings.TrimSpace(req.Share) == "" {
		if auditErr := h.record(c, "masterKey.share.submit", auditdomain.ResultFailure, h.statusResponse(), ErrInvalidShare); auditErr != nil {
			logger.Error(c, "record empty master key share audit failed", zap.Error(auditErr))
			response.Error(c, "internal error")
			return
		}
		response.Error(c, "密钥分片不能为空")
		return
	}

	var successAuditErr error
	err := h.manager.SubmitShareWithCommit(req.Share, func(status Status) error {
		successAuditErr = h.record(c, "masterKey.share.submit", auditdomain.ResultSuccess, toStatusResponse(status), nil)
		return successAuditErr
	})
	if err != nil {
		if successAuditErr != nil {
			logger.Error(c, "record accepted master key share audit failed", zap.Error(successAuditErr))
			response.Error(c, "internal error")
			req.Share = ""
			return
		}
		if auditErr := h.record(c, "masterKey.share.submit", auditdomain.ResultFailure, h.statusResponse(), err); auditErr != nil {
			logger.Error(c, "record rejected master key share audit failed", zap.Error(auditErr))
			response.Error(c, "internal error")
			req.Share = ""
			return
		}
		logger.Info(c, "master key share submitted", zap.String("result", "rejected"))
		h.respondRestoreError(c, err)
		req.Share = ""
		return
	}
	req.Share = ""
	logger.Info(c, "master key share submitted", zap.String("result", "accepted"))
	response.Success(c, h.statusResponse())
}

// Transfer 使用请求方临时 RSA 公钥返回加密后的主密钥信封。
// 明文主密钥不会进入 HTTP 响应、日志或审计记录。
func (h *HTTPHandler) Transfer(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxTransferRequestBodySize)

	var req TransferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		if auditErr := h.record(c, "masterKey.peer.transfer", auditdomain.ResultFailure, h.statusResponse(), ErrInvalidTransferRequest); auditErr != nil {
			logger.Error(c, "record master key peer transfer audit failed", zap.Error(auditErr))
			response.Error(c, "internal error")
			return
		}
		response.BadRequest(c, err)
		return
	}

	req.PublicKey = strings.TrimSpace(req.PublicKey)
	if req.PublicKey == "" {
		response.Error(c, "公钥不能为空")
		return
	}
	if req.Algorithm != "" && req.Algorithm != MasterKeyTransferAlgorithm {
		response.Error(c, "不支持的密钥传输算法")
		return
	}
	if req.RequestID == "" {
		req.RequestID = uuid.NewString()
	}
	if len(req.RequestID) > 128 || len(req.InstanceID) > 128 {
		response.Error(c, "请求标识过长")
		return
	}

	wrapper, err := h.manager.ExportWrappedKey(req.PublicKey)
	if err != nil {
		if auditErr := h.record(c, "masterKey.peer.transfer", auditdomain.ResultFailure, h.statusResponse(), err); auditErr != nil {
			logger.Error(c, "record master key peer transfer audit failed", zap.Error(auditErr))
			response.Error(c, "internal error")
			return
		}
		switch {
		case errors.Is(err, ErrNotReady):
			response.AbortWithHTTPStatusMessage(c, http.StatusServiceUnavailable, "系统主密钥尚未激活")
		case errors.Is(err, ErrInvalidTransferRequest):
			response.Error(c, "公钥无效")
		default:
			logger.Error(c, "wrap master key for peer failed", zap.Error(err))
			response.Error(c, "internal error")
		}
		return
	}

	if auditErr := h.record(c, "masterKey.peer.transfer", auditdomain.ResultSuccess, h.statusResponse(), nil); auditErr != nil {
		logger.Error(c, "record master key peer transfer audit failed", zap.Error(auditErr))
		response.Error(c, "internal error")
		return
	}
	response.Success(c, TransferResponse{
		RequestID: req.RequestID, EncryptedMasterKey: wrapper.EncryptedMasterKey,
		KeyFingerprint: wrapper.KeyFingerprint, Algorithm: wrapper.Algorithm,
	})
}

// requireInternalPeerToken 是 mTLS 接入前的临时集群内部认证边界。
// 未配置令牌时主动禁用传输能力，禁止意外暴露无认证的主密钥接口。
func requireInternalPeerToken(expected string) gin.HandlerFunc {
	expected = strings.TrimSpace(expected)
	return func(c *gin.Context) {
		if expected == "" {
			response.AbortWithHTTPStatusMessage(c, http.StatusServiceUnavailable, "内部主密钥传输未配置")
			return
		}
		provided := c.GetHeader(InternalPeerTokenHeader)
		if len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			response.AbortWithHTTPStatus(c, http.StatusUnauthorized)
			return
		}
		c.Next()
	}
}

// statusResponse 将内部状态转换为固定的公开响应结构
func (h *HTTPHandler) statusResponse() StatusResponse {
	// Manager.Status 在同一把读锁中取得 ready 和 source，避免返回组合错误的状态
	status := h.manager.Status()
	return StatusResponse{
		Ready:           status.Ready,
		Source:          status.Source,
		TotalShares:     TotalShares,
		RequiredShares:  RequiredShares,
		SubmittedShares: status.SubmittedShares,
		CanSubmit:       !status.Ready,
	}
}

func toStatusResponse(status Status) StatusResponse {
	return StatusResponse{
		Ready: status.Ready, Source: status.Source, TotalShares: TotalShares,
		RequiredShares: RequiredShares, SubmittedShares: status.SubmittedShares,
		CanSubmit: !status.Ready,
	}
}

func (h *HTTPHandler) record(c *gin.Context, action, result string, status StatusResponse, operationErr error) error {
	if h.auditRecorder == nil {
		return nil
	}
	event := auditapp.NewResourceEvent(auditapp.ResourceEventInput{
		ActionCode: action, ResultCode: result, ResourceType: "masterKey",
		ResourceID: "system", ResourceName: "系统主密钥", ScopeType: "system", ScopeID: "system",
		Detail: map[string]any{
			"ready": status.Ready, "source": status.Source,
			"submittedShares": status.SubmittedShares, "requiredShares": status.RequiredShares,
		},
	})
	if operationErr != nil {
		event = auditapp.MarkFailure(event, operationErr, ErrAlreadyActivated, ErrShareSetMismatch, ErrDuplicateShare, ErrInvalidShare, ErrInvalidTransferRequest, ErrKeyFingerprintMismatch, ErrNotReady)
	}
	return h.auditRecorder.Record(auditctx.HTTP(c), event)
}

// respondRestoreError 将内部恢复错误转换为不包含敏感信息的提示
func (h *HTTPHandler) respondRestoreError(c *gin.Context, err error) {
	// 对外只返回固定错误信息，避免底层解析错误携带敏感输入
	switch {
	case errors.Is(err, ErrAlreadyActivated):
		response.Error(c, "系统主密钥已经激活")
	case errors.Is(err, ErrShareSetMismatch):
		response.Error(c, "密钥分片不属于同一批次")
	case errors.Is(err, ErrDuplicateShare):
		response.Error(c, "密钥分片重复")
	case errors.Is(err, ErrInvalidShare):
		response.Error(c, "密钥分片无效")
	default:
		response.Error(c, "internal error")
	}
}
