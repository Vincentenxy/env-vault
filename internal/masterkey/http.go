package masterkey

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"env-vault/pkg/logger"
	"env-vault/pkg/response"
	"env-vault/pkg/userctx"
)

const maxShareRequestBodySize = 16 * 1024

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

// HTTPHandler 提供主密钥启动阶段使用的受认证接口
type HTTPHandler struct {
	manager *Manager
}

// NewHTTPHandler 创建主密钥 HTTP 处理器
func NewHTTPHandler(manager *Manager) *HTTPHandler {
	return &HTTPHandler{manager: manager}
}

// RegisterRoutes 在受认证路由组中注册主密钥接口
func RegisterRoutes(group *gin.RouterGroup, manager *Manager) {
	// 主密钥是独立系统资源，状态查询和分片提交共用同一个资源前缀
	handler := NewHTTPHandler(manager)
	masterKeyGroup := group.Group("/masterKey")
	masterKeyGroup.GET("/status", handler.GetStatus)
	masterKeyGroup.POST("/share", handler.SubmitShare)
}

// GetStatus 查询不包含任何密钥和分片内容的运行状态
func (h *HTTPHandler) GetStatus(c *gin.Context) {
	response.Success(c, h.statusResponse())
}

// SubmitShare 校验并累计一份分片，达到阈值时恢复并激活主密钥
func (h *HTTPHandler) SubmitShare(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxShareRequestBodySize)

	var req SubmitShareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	if strings.TrimSpace(req.Share) == "" {
		response.Error(c, "密钥分片不能为空")
		return
	}

	if err := h.manager.SubmitShare(req.Share); err != nil {
		h.audit(c, "rejected")
		h.respondRestoreError(c, err)
		req.Share = ""
		return
	}
	req.Share = ""
	h.audit(c, "accepted")
	response.Success(c, h.statusResponse())
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

func (h *HTTPHandler) audit(c *gin.Context, result string) {
	userID := ""
	if user := userctx.FromContext(c); user != nil {
		userID = user.UserID
	}
	logger.Info(c, "master key share submitted", zap.String("userId", userID), zap.String("result", result))
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
