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
	"github.com/google/uuid"
)

// Request 提供范围、环境、标签和 Key 或备注条件，分页统一复用公共请求
type Request struct {
	Scopes    []Scope     `json:"scopes"`
	EnvList   []string    `json:"envList"`
	TagIDList []uuid.UUID `json:"tagIdList"`
	Keyword   string      `json:"keyword"`
	page.Request
}

// TagListRequest 按密钥范围加载实际存在绑定关系的标签候选
type TagListRequest struct {
	Scopes  []Scope  `json:"scopes"`
	EnvList []string `json:"envList"`
	Keyword string   `json:"keyword"`
	page.Request
}

// HTTPHandler 在已有认证与 Ready 中间件之后处理共享密钥检索
type HTTPHandler struct {
	service Searcher
	tags    TagOptionLister
}

func NewHTTPHandler(service Searcher) *HTTPHandler {
	handler := &HTTPHandler{service: service}
	handler.tags, _ = service.(TagOptionLister)
	return handler
}

// Search 搜索 Key、备注或标签，返回按逻辑密钥组分页的固定展示结构
// @Summary 搜索共享密钥 Key、备注或标签
// @Tags secret
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body Request true "范围、环境、字面量关键词、标签和分页"
// @Success 200 {object} response.Response{data=page.Response[Secret]}
// @Router /api/v1/secret/search [post]
func (h *HTTPHandler) Search(c *gin.Context) {
	user, ok := userctx.MustFromContext(c)
	if !ok || user.UserID == "" {
		response.AbortWithHTTPStatus(c, http.StatusUnauthorized)
		return
	}
	var req *Request
	// 未声明的筛选字段直接报错，避免调用者误以为条件已经生效
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
		Scopes: req.Scopes, EnvList: req.EnvList, TagIDs: req.TagIDList, Keyword: req.Keyword,
		PageNum: req.PageNum, PageSize: req.PageSize, UserID: user.UserID,
	})
	if err != nil {
		writeSearchError(c, err, "secret search failed")
		return
	}
	response.Success(c, result)
}

// ListTags 返回当前搜索范围内存在有效密钥绑定的标签
// @Summary 查询密钥检索标签候选
// @Tags secret
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body TagListRequest true "范围、环境、标签关键词和分页"
// @Success 200 {object} response.Response{data=page.Response[TagOption]}
// @Router /api/v1/secret/search/tag/list [post]
func (h *HTTPHandler) ListTags(c *gin.Context) {
	user, ok := userctx.MustFromContext(c)
	if !ok || user.UserID == "" {
		response.AbortWithHTTPStatus(c, http.StatusUnauthorized)
		return
	}
	if h.tags == nil {
		logger.Error(c, "secret search tag lister unavailable")
		response.Error(c, "internal error")
		return
	}
	var req *TagListRequest
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
	ctx, cancel := context.WithCancel(auditctx.HTTP(c))
	stop := context.AfterFunc(c.Request.Context(), cancel)
	defer stop()
	defer cancel()
	result, err := h.tags.ListTagOptions(ctx, TagOptionInput{
		Scopes: req.Scopes, EnvList: req.EnvList, Keyword: req.Keyword,
		PageNum: req.PageNum, PageSize: req.PageSize, UserID: user.UserID,
	})
	if err != nil {
		writeSearchError(c, err, "secret search tag list failed")
		return
	}
	response.Success(c, result)
}

// writeSearchError 只返回稳定业务文案，底层数据库错误不进入响应
func writeSearchError(c *gin.Context, err error, logMessage string) {
	for _, known := range []error{ErrInvalidInput, ErrShortKeyword, ErrEnvironment, ErrScope, ErrTag, ErrDecrypt, ErrTimeout} {
		if errors.Is(err, known) {
			response.Error(c, known.Error())
			return
		}
	}
	if errors.Is(err, context.Canceled) {
		response.Error(c, "搜索已取消")
		return
	}
	logger.Error(c, logMessage)
	response.Error(c, "internal error")
}
