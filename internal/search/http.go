package search

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"env-vault/internal/interfaces/auditctx"
	"env-vault/pkg/logger"
	"env-vault/pkg/page"
	"env-vault/pkg/response"
	"env-vault/pkg/userctx"
	"github.com/gin-gonic/gin"
)

// Request 只提供 Key、备注查询所需参数，分页统一复用公共请求
type Request struct {
	Scopes  []Scope  `json:"scopes"`
	EnvList []string `json:"envList"`
	Keyword string   `json:"keyword"`
	page.Request
}

// HTTPHandler 在已有认证与 Ready 中间件之后处理共享密钥检索
type HTTPHandler struct{ service Searcher }

func NewHTTPHandler(service Searcher) *HTTPHandler { return &HTTPHandler{service: service} }

// Search 搜索 Key 或备注，返回按逻辑密钥组分页的固定展示结构
// @Summary 搜索共享密钥 Key 或备注
// @Tags secret
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body Request true "范围、环境、字面量关键词和分页"
// @Success 200 {object} response.Response{data=page.Response[Secret]}
// @Router /api/v1/secret/search [post]
func (h *HTTPHandler) Search(c *gin.Context) {
	user, ok := userctx.MustFromContext(c)
	if !ok || user.UserID == "" {
		response.AbortWithHTTPStatus(c, http.StatusUnauthorized)
		return
	}
	var req *Request
	// 本阶段未实现的筛选字段直接报错，避免调用者误以为标签等条件已生效
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	if req == nil {
		response.Error(c, ErrInvalidInput.Error())
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		response.Error(c, ErrInvalidInput.Error())
		return
	}
	req.Normalize()
	// 保留审计上下文，同时显式绑定 HTTP 请求取消，不依赖 Gin 的 ContextWithFallback
	ctx, cancel := context.WithCancel(auditctx.HTTP(c))
	stop := context.AfterFunc(c.Request.Context(), cancel)
	defer stop()
	defer cancel()
	result, err := h.service.Search(ctx, Input{
		Scopes: req.Scopes, EnvList: req.EnvList, Keyword: req.Keyword,
		PageNum: req.PageNum, PageSize: req.PageSize, UserID: user.UserID,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidInput), errors.Is(err, ErrShortKeyword), errors.Is(err, ErrEnvironment), errors.Is(err, ErrScope), errors.Is(err, ErrDecrypt), errors.Is(err, ErrTimeout):
			// 固定错误文案避免 errors.Join 中的底层错误进入响应
			for _, known := range []error{ErrInvalidInput, ErrShortKeyword, ErrEnvironment, ErrScope, ErrDecrypt, ErrTimeout} {
				if errors.Is(err, known) {
					response.Error(c, known.Error())
					return
				}
			}
		case errors.Is(err, context.Canceled):
			response.Error(c, "搜索已取消")
		default:
			logger.Error(c, "secret search failed")
			response.Error(c, "internal error")
		}
		return
	}
	response.Success(c, result)
}
