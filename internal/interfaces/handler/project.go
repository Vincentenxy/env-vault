package handler

import (
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	projapp "env-vault/internal/application/project"
	projdomain "env-vault/internal/domain/project"
	"env-vault/pkg/page"
	"env-vault/pkg/response"
)

// ProjectHandler 项目 HTTP 处理器（依赖应用层 IService 接口，便于单测）
type ProjectHandler struct {
	svc projapp.IService
}

// NewProjectHandler 创建项目处理器
func NewProjectHandler(svc projapp.IService) *ProjectHandler {
	return &ProjectHandler{svc: svc}
}

// ProjectDTO 项目响应数据（JSON 小驼峰）
type ProjectDTO struct {
	ID       uuid.UUID `json:"id"`
	Code     string    `json:"code"`
	Name     string    `json:"name"`
	Remark   string    `json:"remark"`
	OrgID    uuid.UUID `json:"orgId"`
	CreateBy string    `json:"createBy"`
	UpdateBy string    `json:"updateBy"`
	CreateAt time.Time `json:"createAt"`
	UpdateAt time.Time `json:"updateAt"`
}

// CreateRequest 创建项目请求
type CreateProjectRequest struct {
	Code         string                         `json:"code"`
	Name         string                         `json:"name"`
	Remark       string                         `json:"remark"`
	OrgID        uuid.UUID                      `json:"orgId"`
	Environments []CreateProjectEnvironmentItem `json:"environments,omitempty"`
}

// CreateProjectEnvironmentItem 创建项目时的环境配置。
type CreateProjectEnvironmentItem struct {
	Name        string `json:"name"`
	Code        string `json:"code"`
	Remark      string `json:"remark"`
	IsCheckPerm bool   `json:"isCheckPerm"`
}

// UpdateRequest 更新项目请求
type UpdateProjectRequest struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Remark string    `json:"remark"`
}

// DeleteRequest 删除项目请求
type DeleteProjectRequest struct {
	ID uuid.UUID `json:"id"`
}

// DetailRequest 项目详情请求
type DetailProjectRequest struct {
	ID uuid.UUID `json:"id"`
}

// ListRequest 项目列表请求（分页字段 pageNum/pageSize 来自内嵌的 page.Request）
type ListProjectRequest struct {
	Code  string     `json:"code,omitempty"`
	Name  string     `json:"name,omitempty"`
	OrgID *uuid.UUID `json:"orgId,omitempty"`
	page.Request
}

// Create 创建项目
func (h *ProjectHandler) Create(c *gin.Context) {
	var req CreateProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	if req.Code == "" || req.Name == "" || req.OrgID == uuid.Nil {
		response.Error(c, "invalid params")
		return
	}

	p, err := h.svc.Create(c, projapp.CreateInput{
		Code:         req.Code,
		Name:         req.Name,
		Remark:       req.Remark,
		OrgID:        req.OrgID,
		Environments: toCreateEnvironmentInputs(req.Environments),
	}, operator(c))
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, toProjectDTO(p))
}

func toCreateEnvironmentInputs(items []CreateProjectEnvironmentItem) []projapp.CreateEnvironmentInput {
	inputs := make([]projapp.CreateEnvironmentInput, 0, len(items))
	for _, item := range items {
		inputs = append(inputs, projapp.CreateEnvironmentInput{
			Name:        item.Name,
			Code:        item.Code,
			Remark:      item.Remark,
			IsCheckPerm: item.IsCheckPerm,
		})
	}
	return inputs
}

// Update 更新项目
func (h *ProjectHandler) Update(c *gin.Context) {
	var req UpdateProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	if req.ID == uuid.Nil || req.Name == "" {
		response.Error(c, "invalid params")
		return
	}

	p, err := h.svc.Update(c, projapp.UpdateInput{
		ID:     req.ID,
		Name:   req.Name,
		Remark: req.Remark,
	}, operator(c))
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, toProjectDTO(p))
}

// Delete 删除项目（软删除）
func (h *ProjectHandler) Delete(c *gin.Context) {
	var req DeleteProjectRequest
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

// Detail 项目详情
func (h *ProjectHandler) Detail(c *gin.Context) {
	var req DetailProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	if req.ID == uuid.Nil {
		response.Error(c, "invalid params")
		return
	}

	p, err := h.svc.GetByID(c, req.ID)
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, toProjectDTO(p))
}

// List 项目列表（分页）
func (h *ProjectHandler) List(c *gin.Context) {
	var req ListProjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	req.Normalize()

	projects, total, err := h.svc.List(c, projapp.ListInput{
		Code:     req.Code,
		Name:     req.Name,
		OrgID:    req.OrgID,
		PageNum:  req.PageNum,
		PageSize: req.PageSize,
	})
	h.respondError(c, err)
	if err != nil {
		return
	}

	list := make([]ProjectDTO, 0, len(projects))
	for _, p := range projects {
		list = append(list, *toProjectDTO(p))
	}
	response.Success(c, page.Response[ProjectDTO]{
		Total: total,
		List:  list,
	})
}

// respondError 应用层错误统一映射为业务错误码
func (h *ProjectHandler) respondError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	switch {
	case errors.Is(err, projapp.ErrCodeExists),
		errors.Is(err, projapp.ErrNotFound),
		errors.Is(err, projapp.ErrInvalidParam),
		errors.Is(err, projapp.ErrEnvironmentCodeDuplicated):
		// 通用业务错误：统一 code=-1，msg 由 service 给出
		response.Error(c, err.Error())
	default:
		response.Error(c, "internal error")
	}
}

// toProjectDTO 领域模型转响应 DTO
func toProjectDTO(p *projdomain.Project) *ProjectDTO {
	return &ProjectDTO{
		ID:       p.ID,
		Code:     p.Code,
		Name:     p.Name,
		Remark:   p.Remark,
		OrgID:    p.OrgID,
		CreateBy: p.CreateBy,
		UpdateBy: p.UpdateBy,
		CreateAt: p.CreateAt,
		UpdateAt: p.UpdateAt,
	}
}
