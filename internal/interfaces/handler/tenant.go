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
	svc tenantapp.IService
}

// NewTenantHandler 创建租户处理器
func NewTenantHandler(svc tenantapp.IService) *TenantHandler {
	return &TenantHandler{svc: svc}
}

// TenantDTO 租户响应数据（JSON 小驼峰）
type TenantDTO struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Remark      string    `json:"remark"`
	Manager     string    `json:"manager"`
	ManagerName string    `json:"managerName"`
	OrgCount    int64     `json:"orgCount"`
	MemberCount int64     `json:"memberCount"`
	CreateBy    string    `json:"createBy"`
	UpdateBy    string    `json:"updateBy"`
	CreateAt    time.Time `json:"createAt"`
	UpdateAt    time.Time `json:"updateAt"`
}

// CreateRequest 创建租户请求
type CreateRequest struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	Remark  string `json:"remark"`
	Manager string `json:"manager,omitempty"`
}

// UpdateRequest 更新租户请求
type UpdateRequest struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Remark  string    `json:"remark"`
	Manager string    `json:"manager,omitempty"`
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

// WithOrgProjectResponse 租户组织项目树响应。
type WithOrgProjectResponse struct {
	TenantList []TenantWithOrgProjectsDTO `json:"tenantList"`
}

// TenantWithOrgProjectsDTO 租户及其组织项目树。
type TenantWithOrgProjectsDTO struct {
	Manager string                        `json:"manager"`
	ID      uuid.UUID                     `json:"id"`
	Name    string                        `json:"name"`
	OrgList []OrganizationWithProjectsDTO `json:"orgList"`
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

	t, err := h.svc.Create(withHTTPAuditContext(c), tenantapp.CreateInput{
		Code:    req.Code,
		Name:    req.Name,
		Remark:  req.Remark,
		Manager: req.Manager,
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

	t, err := h.svc.Update(withHTTPAuditContext(c), tenantapp.UpdateInput{
		ID:      req.ID,
		Name:    req.Name,
		Remark:  req.Remark,
		Manager: req.Manager,
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

	err := h.svc.Delete(withHTTPAuditContext(c), req.ID, operator(c))
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

	t, err := h.svc.GetByID(withHTTPAuditContext(c), req.ID)
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

	tenants, total, err := h.svc.List(withHTTPAuditContext(c), tenantapp.ListInput{
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

// WithOrgProject 查询租户下的组织和项目；当前返回全部，后续按用户权限过滤。
func (h *TenantHandler) WithOrgProject(c *gin.Context) {
	tenants, err := h.svc.ListWithOrgProjects(withHTTPAuditContext(c), tenantapp.WithOrgProjectsInput{UserID: operator(c)})
	h.respondError(c, err)
	if err != nil {
		return
	}

	tenantList := make([]TenantWithOrgProjectsDTO, 0, len(tenants))
	for _, tenant := range tenants {
		orgList := make([]OrganizationWithProjectsDTO, 0, len(tenant.OrgList))
		for _, org := range tenant.OrgList {
			projectList := make([]ProjectSummaryDTO, 0, len(org.ProjectList))
			for _, project := range org.ProjectList {
				projectList = append(projectList, ProjectSummaryDTO{ID: project.ID, Name: project.Name, Manager: project.Manager})
			}
			orgList = append(orgList, OrganizationWithProjectsDTO{
				ID:          org.ID,
				Name:        org.Name,
				Manager:     org.Manager,
				ProjectList: projectList,
			})
		}
		tenantList = append(tenantList, TenantWithOrgProjectsDTO{
			ID:      tenant.ID,
			Name:    tenant.Name,
			Manager: tenant.Manager,
			OrgList: orgList,
		})
	}
	response.Success(c, WithOrgProjectResponse{TenantList: tenantList})
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
		ID:          t.ID,
		Code:        t.Code,
		Name:        t.Name,
		Remark:      t.Remark,
		Manager:     t.Manager,
		ManagerName: t.ManagerName,
		OrgCount:    t.OrgCount,
		MemberCount: t.MemberCount,
		CreateBy:    t.CreateBy,
		UpdateBy:    t.UpdateBy,
		CreateAt:    t.CreateAt,
		UpdateAt:    t.UpdateAt,
	}
}
