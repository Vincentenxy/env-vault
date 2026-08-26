package handler

import (
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	envapp "env-vault/internal/application/environment"
	envdomain "env-vault/internal/domain/environment"
	"env-vault/pkg/response"
)

// EnvironmentHandler 环境 HTTP 处理器（依赖应用层 IService 接口，便于单测）
type EnvironmentHandler struct {
	svc envapp.IService
}

// NewEnvironmentHandler 创建环境处理器
func NewEnvironmentHandler(svc envapp.IService) *EnvironmentHandler {
	return &EnvironmentHandler{svc: svc}
}

// EnvironmentDTO 环境响应数据（JSON 小驼峰）
type EnvironmentDTO struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Remark      string    `json:"remark"`
	ProjectID   uuid.UUID `json:"projectId"`
	OrderNo     int       `json:"orderNo"`
	IsCheckPerm bool      `json:"isCheckPerm"`
	CreateBy    string    `json:"createBy"`
	UpdateBy    string    `json:"updateBy"`
	CreateAt    time.Time `json:"createAt"`
	UpdateAt    time.Time `json:"updateAt"`
}

// CreateEnvironmentItemRequest 批量创建时单个环境的入参
type CreateEnvironmentItemRequest struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Remark      string `json:"remark"`
	OrderNo     int    `json:"orderNo"`     // 可选，<=0 时由后端按已有最大排序号 + 10 生成
	IsCheckPerm bool   `json:"isCheckPerm"` // 是否进行权限校验，未传默认 false
}

// CreateEnvironmentRequest 批量创建环境请求
type CreateEnvironmentRequest struct {
	ProjectID    uuid.UUID                      `json:"projectId"`
	Environments []CreateEnvironmentItemRequest `json:"environments"`
}

// UpdateEnvironmentRequest 更新环境请求
type UpdateEnvironmentRequest struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Remark      string    `json:"remark"`
	OrderNo     int       `json:"orderNo"`     // 排序号，<=0 时保持原值
	IsCheckPerm bool      `json:"isCheckPerm"` // 是否进行权限校验
}

// DeleteEnvironmentRequest 删除环境请求
type DeleteEnvironmentRequest struct {
	ID uuid.UUID `json:"id"`
}

// DetailEnvironmentRequest 环境详情请求
type DetailEnvironmentRequest struct {
	ID uuid.UUID `json:"id"`
}

// ListEnvironmentRequest 环境列表请求（环境数量少，不分页，仅按项目查询全部）
type ListEnvironmentRequest struct {
	ProjectID uuid.UUID `json:"projectId"`
}

// Create 批量创建环境
func (h *EnvironmentHandler) Create(c *gin.Context) {
	var req CreateEnvironmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	items := make([]envapp.CreateItemInput, 0, len(req.Environments))
	for _, item := range req.Environments {
		items = append(items, envapp.CreateItemInput{
			Code:        item.Code,
			Name:        item.Name,
			Remark:      item.Remark,
			OrderNo:     item.OrderNo,
			IsCheckPerm: item.IsCheckPerm,
		})
	}

	environments, err := h.svc.Create(c, envapp.CreateInput{
		ProjectID:    req.ProjectID,
		Environments: items,
	}, operator(c))
	h.respondError(c, err)
	if err != nil {
		return
	}

	list := make([]EnvironmentDTO, 0, len(environments))
	for _, e := range environments {
		list = append(list, *toEnvironmentDTO(e))
	}
	response.Success(c, list)
}

// Update 更新环境
func (h *EnvironmentHandler) Update(c *gin.Context) {
	var req UpdateEnvironmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	e, err := h.svc.Update(c, envapp.UpdateInput{
		ID:          req.ID,
		Name:        req.Name,
		Remark:      req.Remark,
		OrderNo:     req.OrderNo,
		IsCheckPerm: req.IsCheckPerm,
	}, operator(c))
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, toEnvironmentDTO(e))
}

// Delete 删除环境（软删除）
func (h *EnvironmentHandler) Delete(c *gin.Context) {
	var req DeleteEnvironmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	err := h.svc.Delete(c, req.ID, operator(c))
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, nil)
}

// Detail 环境详情
func (h *EnvironmentHandler) Detail(c *gin.Context) {
	var req DetailEnvironmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	e, err := h.svc.GetByID(c, req.ID)
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, toEnvironmentDTO(e))
}

// List 环境列表（不分页，按排序号升序返回项目下全部环境）
func (h *EnvironmentHandler) List(c *gin.Context) {
	var req ListEnvironmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	environments, err := h.svc.List(c, envapp.ListInput{
		ProjectID: req.ProjectID,
	})
	h.respondError(c, err)
	if err != nil {
		return
	}

	list := make([]EnvironmentDTO, 0, len(environments))
	for _, e := range environments {
		list = append(list, *toEnvironmentDTO(e))
	}
	response.Success(c, list)
}

// respondError 应用层错误统一映射为业务错误码
func (h *EnvironmentHandler) respondError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	switch {
	case errors.Is(err, envapp.ErrCodeExists),
		errors.Is(err, envapp.ErrCodeDuplicated),
		errors.Is(err, envapp.ErrNotFound),
		errors.Is(err, envapp.ErrInvalidParam):
		// 通用业务错误：统一 code=-1，msg 由 service 给出
		response.Error(c, err.Error())
	default:
		response.Error(c, "internal error")
	}
}

// toEnvironmentDTO 领域模型转响应 DTO
func toEnvironmentDTO(e *envdomain.Environment) *EnvironmentDTO {
	return &EnvironmentDTO{
		ID:          e.ID,
		Code:        e.Code,
		Name:        e.Name,
		Remark:      e.Remark,
		ProjectID:   e.ProjectID,
		OrderNo:     e.OrderNo,
		IsCheckPerm: e.IsCheckPerm,
		CreateBy:    e.CreateBy,
		UpdateBy:    e.UpdateBy,
		CreateAt:    e.CreateAt,
		UpdateAt:    e.UpdateAt,
	}
}
