package masterkey

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"env-vault/pkg/response"
)

const maxShareRequestBodySize = 16 * 1024

// StatusResponse 表示可以安全返回给外部的主密钥状态
type StatusResponse struct {
	Ready          bool   `json:"ready"`          // 系统主密钥是否已经激活
	Source         Source `json:"source"`         // 当前主密钥的加载来源
	TotalShares    int    `json:"totalShares"`    // 系统生成的分片总数
	RequiredShares int    `json:"requiredShares"` // 恢复主密钥需要的分片数
}

// SubmitSharesRequest 表示管理员一次提交的三份密钥分片
type SubmitSharesRequest struct {
	Shares []string `json:"shares"`
}

// HTTPHandler 提供主密钥启动阶段使用的公开接口
type HTTPHandler struct {
	manager *Manager
}

// NewHTTPHandler 创建主密钥 HTTP 处理器
func NewHTTPHandler(manager *Manager) *HTTPHandler {
	return &HTTPHandler{manager: manager}
}

// RegisterRoutes 在公开路由组中注册主密钥接口
func RegisterRoutes(group *gin.RouterGroup, manager *Manager) {
	// 主密钥是独立系统资源，状态查询和分片提交共用同一个资源前缀
	handler := NewHTTPHandler(manager)
	masterKeyGroup := group.Group("/masterKey")
	masterKeyGroup.GET("/status", handler.GetStatus)
	masterKeyGroup.POST("/shares", handler.SubmitShares)
}

// GetStatus 查询不包含任何密钥和分片内容的运行状态
func (h *HTTPHandler) GetStatus(c *gin.Context) {
	response.Success(c, h.statusResponse())
}

// SubmitShares 使用一次提交的三份分片恢复并激活主密钥
func (h *HTTPHandler) SubmitShares(c *gin.Context) {
	// 未认证启动接口限制请求体大小，三份正常 Token 远小于该上限
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxShareRequestBodySize)

	var req SubmitSharesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	// 请求结束前移除切片对分片字符串的引用，不将内容写入日志或响应
	defer clear(req.Shares)

	// Handler 提前校验数量和空字符串，Manager 继续承担最终业务校验
	if len(req.Shares) != RequiredShares {
		response.Error(c, "必须提交三份密钥分片")
		return
	}
	for _, share := range req.Shares {
		if strings.TrimSpace(share) == "" {
			response.Error(c, "密钥分片不能为空")
			return
		}
	}

	if err := h.manager.RestoreShares(req.Shares); err != nil {
		h.respondRestoreError(c, err)
		return
	}
	response.Success(c, h.statusResponse())
}

// statusResponse 将内部状态转换为固定的公开响应结构
func (h *HTTPHandler) statusResponse() StatusResponse {
	// Manager.Status 在同一把读锁中取得 ready 和 source，避免返回组合错误的状态
	status := h.manager.Status()
	return StatusResponse{
		Ready:          status.Ready,
		Source:         status.Source,
		TotalShares:    TotalShares,
		RequiredShares: RequiredShares,
	}
}

// respondRestoreError 将内部恢复错误转换为不包含敏感信息的提示
func (h *HTTPHandler) respondRestoreError(c *gin.Context, err error) {
	// 对外只返回固定错误信息，避免底层解析错误携带敏感输入
	switch {
	case errors.Is(err, ErrAlreadyActivated):
		response.Error(c, "系统主密钥已经激活")
	case errors.Is(err, ErrInvalidShareCount):
		response.Error(c, "必须提交三份密钥分片")
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
