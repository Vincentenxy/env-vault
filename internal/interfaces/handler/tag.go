package handler

import (
	tagapp "env-vault/internal/application/tag"
	domain "env-vault/internal/domain/tag"
	"env-vault/pkg/page"
	"env-vault/pkg/response"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type TagHandler struct{ svc tagapp.IService }

func NewTagHandler(svc tagapp.IService) *TagHandler { return &TagHandler{svc: svc} }

type TagRequest = tagapp.Input
type TagListRequest struct {
	TenantID uuid.UUID `json:"tenantId"`
	Keyword  string    `json:"keyword"`
	page.Request
}

func (h *TagHandler) respond(c *gin.Context, data any, err error) {
	if err == nil {
		response.Success(c, data)
		return
	}
	switch {
	case errors.Is(err, tagapp.ErrInvalidParam), errors.Is(err, tagapp.ErrTenantNotFound), errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrCodeExists):
		response.Error(c, err.Error())
	default:
		response.Error(c, "internal error")
	}
}

// Create godoc
// @Summary Create a tenant tag
// @Tags tag
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body TagRequest true "Tag; allowValueSearch defaults to true"
// @Success 200 {object} response.Response
// @Router /api/v1/tag/create [post]
func (h *TagHandler) Create(c *gin.Context) {
	var req TagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	item, err := h.svc.Create(withHTTPAuditContext(c), req, operator(c))
	h.respond(c, item, err)
}

// Update godoc
// @Summary Update a tag; code and tenant cannot change
// @Tags tag
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body TagRequest true "Tag update; omitted allowValueSearch preserves its value"
// @Success 200 {object} response.Response
// @Router /api/v1/tag/update [post]
func (h *TagHandler) Update(c *gin.Context) {
	var req TagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	item, err := h.svc.Update(withHTTPAuditContext(c), req, operator(c))
	h.respond(c, item, err)
}

// Delete godoc
// @Summary Soft-delete a tenant tag
// @Tags tag
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body TagRequest true "tenantId and id"
// @Success 200 {object} response.Response
// @Router /api/v1/tag/delete [post]
func (h *TagHandler) Delete(c *gin.Context) {
	var req TagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	h.respond(c, nil, h.svc.Delete(withHTTPAuditContext(c), req, operator(c)))
}

// Info godoc
// @Summary Get a tenant tag
// @Tags tag
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body TagRequest true "tenantId and id"
// @Success 200 {object} response.Response
// @Router /api/v1/tag/info [post]
func (h *TagHandler) Info(c *gin.Context) {
	var req TagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	item, err := h.svc.Info(withHTTPAuditContext(c), req)
	h.respond(c, item, err)
}

// List godoc
// @Summary List tenant tags
// @Tags tag
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body TagListRequest true "Tenant and normalized pagination"
// @Success 200 {object} response.Response
// @Router /api/v1/tag/list [post]
func (h *TagHandler) List(c *gin.Context) {
	var req TagListRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	req.Normalize()
	items, total, err := h.svc.List(withHTTPAuditContext(c), domain.Filter{TenantID: req.TenantID, Keyword: req.Keyword, PageNum: req.PageNum, PageSize: req.PageSize})
	h.respond(c, page.Response[*domain.Tag]{Total: total, List: items}, err)
}
