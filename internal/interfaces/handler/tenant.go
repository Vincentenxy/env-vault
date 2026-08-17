package handler

import (
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	tenantapp "env-vault/internal/application/tenant"
	tenantdomain "env-vault/internal/domain/tenant"
	"env-vault/pkg/page"
	"env-vault/pkg/response"
	"env-vault/pkg/userctx"
)

// 租户 HTTP 处理器

// TenantHandler 租户 HTTP 处理器
type TenantHandler struct {
	svc *tenantapp.Service
}

// NewTenantHandler 创建租户处理器
func NewTenantHandler(svc *tenantapp.Service) *TenantHandler {
	return &TenantHandler{svc: svc}
}

// TenantDTO 租户响应数据（JSON 小驼峰）
type TenantDTO struct {
	ID       uuid.UUID `json:"id"`
	Code     string    `json:"code"`
	Name     string    `json:"name"`
	Remark   string    `json:"remark"`
	CreateBy string    `json:"createBy"`
	UpdateBy string    `json:"updateBy"`
	CreateAt time.Time `json:"createAt"`
	UpdateAt time.Time `json:"updateAt"`
}

// CreateRequest 创建租户请求
type CreateRequest struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Remark string `json:"remark"`
}

// UpdateRequest 更新租户请求
type UpdateRequest struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Remark string    `json:"remark"`
}

// DeleteRequest 删除租户请求
type DeleteRequest struct {
	ID uuid.UUID `json:"id"`
}

// DetailRequest 租户详情请求
type DetailRequest struct {
	ID uuid.UUID `json:"id"`
}

// ListRequest 租户列表请求（分页字段 pageNum/pageSize 来自内嵌的 page.Request）
type ListRequest struct {
	Code string `json:"code"`
	Name string `json:"name"`
	page.Request
}

// Create 创建租户
func (h *TenantHandler) Create(c *gin.Context) {
	var req CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	if req.Code == "" || req.Name == "" {
		response.Error(c, "invalid params")
		return
	}

	t, err := h.svc.Create(c, tenantapp.CreateInput{
		Code:   req.Code,
		Name:   req.Name,
		Remark: req.Remark,
	}, operator(c))
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, toDTO(t))
}

// Update 更新租户
func (h *TenantHandler) Update(c *gin.Context) {
	var req UpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	if req.ID == uuid.Nil || req.Name == "" {
		response.Error(c, "invalid params")
		return
	}

	t, err := h.svc.Update(c, tenantapp.UpdateInput{
		ID:     req.ID,
		Name:   req.Name,
		Remark: req.Remark,
	}, operator(c))
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, toDTO(t))
}

// Delete 删除租户（软删除）
func (h *TenantHandler) Delete(c *gin.Context) {
	var req DeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	if req.ID == uuid.Nil {
		response.Error(c, "invalid params")
		return
	}

	err := h.svc.Delete(c, req.ID, operator(c))
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, nil)
}

// Detail 租户详情
func (h *TenantHandler) Detail(c *gin.Context) {
	var req DetailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	if req.ID == uuid.Nil {
		response.Error(c, "invalid params")
		return
	}

	t, err := h.svc.GetByID(c, req.ID)
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, toDTO(t))
}

// List 租户列表（分页）
func (h *TenantHandler) List(c *gin.Context) {
	var req ListRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	req.Normalize()

	tenants, total, err := h.svc.List(c, tenantapp.ListInput{
		Code:     req.Code,
		Name:     req.Name,
		PageNum:  req.PageNum,
		PageSize: req.PageSize,
	})
	h.respondError(c, err)
	if err != nil {
		return
	}

	list := make([]TenantDTO, 0, len(tenants))
	for _, t := range tenants {
		list = append(list, *toDTO(t))
	}
	response.Success(c, page.Response[TenantDTO]{
		Total: total,
		List:  list,
	})
}

// respondError 应用层错误统一映射为业务错误码
func (h *TenantHandler) respondError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	switch {
	case errors.Is(err, tenantapp.ErrCodeExists),
		errors.Is(err, tenantapp.ErrNotFound),
		errors.Is(err, tenantapp.ErrInvalidParam):
		// 通用业务错误：统一 code=-1，msg 由 service 给出
		response.Error(c, err.Error())
	default:
		response.Error(c, "internal error")
	}
}

// operator 从认证上下文提取操作人 ID（无认证信息时为空串）
func operator(c *gin.Context) string {
	if u := userctx.FromContext(c); u != nil {
		return u.UserID
	}
	return ""
}

// toDTO 领域模型转响应 DTO
func toDTO(t *tenantdomain.Tenant) *TenantDTO {
	return &TenantDTO{
		ID:       t.ID,
		Code:     t.Code,
		Name:     t.Name,
		Remark:   t.Remark,
		CreateBy: t.CreateBy,
		UpdateBy: t.UpdateBy,
		CreateAt: t.CreateAt,
		UpdateAt: t.UpdateAt,
	}
}
