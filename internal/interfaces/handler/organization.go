package handler

import (
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	orgapp "env-vault/internal/application/organization"
	orgdomain "env-vault/internal/domain/organization"
	"env-vault/pkg/page"
	"env-vault/pkg/response"
)

// OrganizationHandler 组织 HTTP 处理器（依赖应用层 IService 接口，便于单测）
type OrganizationHandler struct {
	svc orgapp.IService
}

// NewOrganizationHandler 创建组织处理器
func NewOrganizationHandler(svc orgapp.IService) *OrganizationHandler {
	return &OrganizationHandler{svc: svc}
}

// OrganizationDTO 组织响应数据（JSON 小驼峰）
type OrganizationDTO struct {
	ID       uuid.UUID `json:"id"`
	Code     string    `json:"code"`
	Name     string    `json:"name"`
	Remark   string    `json:"remark"`
	TenantID uuid.UUID `json:"tenantId"`
	Manager  string    `json:"manager"`
	CreateBy string    `json:"createBy"`
	UpdateBy string    `json:"updateBy"`
	CreateAt time.Time `json:"createAt"`
	UpdateAt time.Time `json:"updateAt"`
}

// CreateRequest 创建组织请求
type CreateOrgRequest struct {
	Code     string    `json:"code"`
	Name     string    `json:"name"`
	Remark   string    `json:"remark"`
	TenantID uuid.UUID `json:"tenantId"`
	Manager  string    `json:"manager,omitempty"`
}

// UpdateRequest 更新组织请求
type UpdateOrgRequest struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Remark string    `json:"remark"`
}

// DeleteRequest 删除组织请求
type DeleteOrgRequest struct {
	ID uuid.UUID `json:"id"`
}

// DetailRequest 组织详情请求
type DetailOrgRequest struct {
	ID uuid.UUID `json:"id"`
}

// ListRequest 组织列表请求（分页字段 pageNum/pageSize 来自内嵌的 page.Request）
type ListOrgRequest struct {
	Code     string     `json:"code,omitempty"`
	Name     string     `json:"name,omitempty"`
	TenantID *uuid.UUID `json:"tenantId,omitempty"`
	page.Request
}

// WithProjectResponse 组织项目树响应。
type WithProjectResponse struct {
	OrgList []OrganizationWithProjectsDTO `json:"orgList"`
}

// OrganizationWithProjectsDTO 组织及其项目摘要。
type OrganizationWithProjectsDTO struct {
	ID          uuid.UUID           `json:"id"`
	Name        string              `json:"name"`
	Manager     string              `json:"manager"`
	ProjectList []ProjectSummaryDTO `json:"projectList"`
}

// ProjectSummaryDTO 项目摘要。
type ProjectSummaryDTO struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Manager string    `json:"manager"`
}

// Create 创建组织
func (h *OrganizationHandler) Create(c *gin.Context) {
	var req CreateOrgRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	if req.Code == "" || req.Name == "" || req.TenantID == uuid.Nil {
		response.Error(c, "invalid params")
		return
	}

	o, err := h.svc.Create(c, orgapp.CreateInput{
		Code:     req.Code,
		Name:     req.Name,
		Remark:   req.Remark,
		TenantID: req.TenantID,
		Manager:  req.Manager,
	}, operator(c))
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, toOrgDTO(o))
}

// Update 更新组织
func (h *OrganizationHandler) Update(c *gin.Context) {
	var req UpdateOrgRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	if req.ID == uuid.Nil || req.Name == "" {
		response.Error(c, "invalid params")
		return
	}

	o, err := h.svc.Update(c, orgapp.UpdateInput{
		ID:     req.ID,
		Name:   req.Name,
		Remark: req.Remark,
	}, operator(c))
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, toOrgDTO(o))
}

// Delete 删除组织（软删除）
func (h *OrganizationHandler) Delete(c *gin.Context) {
	var req DeleteOrgRequest
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

// Detail 组织详情
func (h *OrganizationHandler) Detail(c *gin.Context) {
	var req DetailOrgRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	if req.ID == uuid.Nil {
		response.Error(c, "invalid params")
		return
	}

	o, err := h.svc.GetByID(c, req.ID)
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, toOrgDTO(o))
}

// List 组织列表（分页）
func (h *OrganizationHandler) List(c *gin.Context) {
	var req ListOrgRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	req.Normalize()

	orgs, total, err := h.svc.List(c, orgapp.ListInput{
		Code:     req.Code,
		Name:     req.Name,
		TenantID: req.TenantID,
		PageNum:  req.PageNum,
		PageSize: req.PageSize,
	})
	h.respondError(c, err)
	if err != nil {
		return
	}

	list := make([]OrganizationDTO, 0, len(orgs))
	for _, o := range orgs {
		list = append(list, *toOrgDTO(o))
	}
	response.Success(c, page.Response[OrganizationDTO]{
		Total: total,
		List:  list,
	})
}

// WithProject 返回全部组织及组织下的项目。
func (h *OrganizationHandler) WithProject(c *gin.Context) {
	orgs, err := h.svc.ListWithProjects(c, orgapp.WithProjectsInput{UserID: operator(c)})
	h.respondError(c, err)
	if err != nil {
		return
	}

	orgList := make([]OrganizationWithProjectsDTO, 0, len(orgs))
	for _, org := range orgs {
		projects := make([]ProjectSummaryDTO, 0, len(org.ProjectList))
		for _, project := range org.ProjectList {
			projects = append(projects, ProjectSummaryDTO{ID: project.ID, Name: project.Name, Manager: project.Manager})
		}
		orgList = append(orgList, OrganizationWithProjectsDTO{
			ID:          org.ID,
			Name:        org.Name,
			Manager:     org.Manager,
			ProjectList: projects,
		})
	}
	response.Success(c, WithProjectResponse{OrgList: orgList})
}

// respondError 应用层错误统一映射为业务错误码
func (h *OrganizationHandler) respondError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	switch {
	case errors.Is(err, orgapp.ErrCodeExists),
		errors.Is(err, orgapp.ErrNotFound),
		errors.Is(err, orgapp.ErrInvalidParam):
		// 通用业务错误：统一 code=-1，msg 由 service 给出
		response.Error(c, err.Error())
	default:
		response.Error(c, "internal error")
	}
}

// toOrgDTO 领域模型转响应 DTO
func toOrgDTO(o *orgdomain.Organization) *OrganizationDTO {
	return &OrganizationDTO{
		ID:       o.ID,
		Code:     o.Code,
		Name:     o.Name,
		Remark:   o.Remark,
		TenantID: o.TenantID,
		Manager:  o.Manager,
		CreateBy: o.CreateBy,
		UpdateBy: o.UpdateBy,
		CreateAt: o.CreateAt,
		UpdateAt: o.UpdateAt,
	}
}
