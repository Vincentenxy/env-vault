package handler

import (
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	userapp "env-vault/internal/application/user"
	userdomain "env-vault/internal/domain/user"
	"env-vault/pkg/page"
	"env-vault/pkg/response"
	"env-vault/pkg/userctx"
)

// UserHandler 用户信息处理器。
type UserHandler struct {
	svc userapp.IService
}

// NewUserHandler 创建用户信息处理器。
func NewUserHandler(svc userapp.IService) *UserHandler {
	return &UserHandler{svc: svc}
}

// UserUpdateRequest 当前认证用户资料更新请求。
type UserUpdateRequest struct {
	Nickname string    `json:"nickname"`
	Username string    `json:"username"`
	Email    string    `json:"email"`
	Phone    string    `json:"phone"`
	TenantID uuid.UUID `json:"tenantId"`
	OrgID    uuid.UUID `json:"orgId"`
}

// UserListRequest 用户列表请求，筛选优先级为 projectId > orgId > tenantId > undistributed。
type UserListRequest struct {
	TenantID      uuid.UUID `json:"tenantId"`
	OrgID         uuid.UUID `json:"orgId"`
	ProjectID     uuid.UUID `json:"projectId"`
	Undistributed bool      `json:"undistributed"`
}

// UserManagementListRequest 用户管理页面分页查询请求。
type UserManagementListRequest struct {
	TenantID uuid.UUID `json:"tenantId"`
	Keyword  string    `json:"keyword"`
	page.Request
}

// UserManagementUpdateRequest 用户管理页面更新指定用户的资料和直属归属。
// tenantId/orgId 允许为 null，表示清空对应归属。
type UserManagementUpdateRequest struct {
	UserID   string     `json:"userId"`
	Nickname string     `json:"nickname"`
	Username string     `json:"username"`
	Email    string     `json:"email"`
	Phone    string     `json:"phone"`
	TenantID *uuid.UUID `json:"tenantId"`
	OrgID    *uuid.UUID `json:"orgId"`
}

// UserAllocateRequest 用户批量分配请求。
type UserAllocateRequest struct {
	Type       string    `json:"type"`
	Operate    string    `json:"operate"`
	ResourceID uuid.UUID `json:"resourceId"`
	UserIDList []string  `json:"userIdList"`
}

// UserListItemDTO 用户列表项，只包含公开展示字段。
type UserListItemDTO struct {
	ID              uuid.UUID           `json:"id"`
	UserID          string              `json:"userId"`
	Nickname        string              `json:"nickname"`
	IsBlocked       bool                `json:"isBlocked"`
	ProjectRelation *ProjectRelationDTO `json:"projectRelation,omitempty"`
}

// ProjectRelationDTO 用户与当前查询项目的关系。
type ProjectRelationDTO struct {
	MemberType userdomain.ProjectMemberType `json:"memberType"`
	ExpireAt   *time.Time                   `json:"expireAt"`
}

// UserManagementListItemDTO 用户管理列表项，不包含密码哈希等认证敏感字段。
type UserManagementListItemDTO struct {
	ID         uuid.UUID `json:"id"`
	UserID     string    `json:"userId"`
	Nickname   string    `json:"nickname"`
	Username   string    `json:"username"`
	Email      string    `json:"email"`
	Phone      string    `json:"phone"`
	TenantID   uuid.UUID `json:"tenantId"`
	TenantName string    `json:"tenantName"`
	OrgID      uuid.UUID `json:"orgId"`
	OrgName    string    `json:"orgName"`
	IsBlocked  bool      `json:"isBlocked"`
	CreateAt   time.Time `json:"createAt"`
	UpdateAt   time.Time `json:"updateAt"`
}

// UserAllocateResponse 用户批量分配结果。
type UserAllocateResponse struct {
	AffectedCount int `json:"affectedCount"`
}

// UserDTO 用户资料响应，不包含密码哈希和软删除字段。
type UserDTO struct {
	ID        uuid.UUID `json:"id"`
	UserID    string    `json:"userId"`
	Nickname  string    `json:"nickname"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	Phone     string    `json:"phone"`
	TenantID  uuid.UUID `json:"tenantId"`
	OrgID     uuid.UUID `json:"orgId"`
	IsBlocked bool      `json:"isBlocked"`
	CreateBy  string    `json:"createBy"`
	UpdateBy  string    `json:"updateBy"`
	CreateAt  time.Time `json:"createAt"`
	UpdateAt  time.Time `json:"updateAt"`
}

// UserProfileProjectDTO 当前用户已分配项目的展示摘要。
type UserProfileProjectDTO struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// UserProfileDTO 当前用户资料及其资源归属。
type UserProfileDTO struct {
	UserDTO
	TenantName  string                  `json:"tenantName"`
	OrgName     string                  `json:"orgName"`
	ProjectList []UserProfileProjectDTO `json:"projectList"`
}

// Me 获取当前认证用户资料。
// @Summary 获取当前用户信息
// @Description 从 JWT 获取当前用户标识，并返回数据库中的用户资料，不包含密码等敏感信息
// @Tags auth
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Response{data=UserProfileDTO}
// @Failure 401 {object} response.Response
// @Router /api/v1/auth/me [get]
func (h *UserHandler) Me(c *gin.Context) {
	authUser, ok := userctx.MustFromContext(c)
	if !ok || authUser.UserID == "" {
		response.AbortWithHTTPStatus(c, 401)
		return
	}

	profile, err := h.svc.GetProfileDetail(withHTTPAuditContext(c), authUser.UserID)
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, toUserProfileDTO(profile))
}

// List 查询用户列表。
func (h *UserHandler) List(c *gin.Context) {
	var req UserListRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	users, err := h.svc.List(withHTTPAuditContext(c), userapp.ListInput{
		TenantID:      req.TenantID,
		OrgID:         req.OrgID,
		ProjectID:     req.ProjectID,
		Undistributed: req.Undistributed,
	})
	h.respondError(c, err)
	if err != nil {
		return
	}

	list := make([]UserListItemDTO, 0, len(users))
	for _, user := range users {
		item := UserListItemDTO{
			ID:        user.ID,
			UserID:    user.UserID,
			Nickname:  user.Nickname,
			IsBlocked: user.IsBlocked,
		}
		if user.ProjectRelation != nil {
			item.ProjectRelation = &ProjectRelationDTO{
				MemberType: user.ProjectRelation.MemberType,
				ExpireAt:   user.ProjectRelation.ExpireAt,
			}
		}
		list = append(list, item)
	}
	response.Success(c, list)
}

// ManageList 分页查询用户管理列表。
// TODO(permission): 权限中心接入后，此接口必须由 user:manage 后端授权中间件保护。
// @Summary 查询用户管理列表
// @Tags user
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body UserManagementListRequest true "用户管理查询条件"
// @Success 200 {object} response.Response{data=page.Response[UserManagementListItemDTO]}
// @Router /api/v1/user/manage/list [post]
func (h *UserHandler) ManageList(c *gin.Context) {
	var req UserManagementListRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	req.Normalize()

	users, total, err := h.svc.ListManagement(withHTTPAuditContext(c), userapp.ManagementListInput{
		TenantID: req.TenantID,
		Keyword:  req.Keyword,
		PageNum:  req.PageNum,
		PageSize: req.PageSize,
	})
	h.respondError(c, err)
	if err != nil {
		return
	}

	list := make([]UserManagementListItemDTO, 0, len(users))
	for _, user := range users {
		list = append(list, toUserManagementListItemDTO(user))
	}
	response.Success(c, page.Response[UserManagementListItemDTO]{Total: total, List: list})
}

// ManageUpdate 更新指定用户的基础资料与直属租户、组织归属。
// TODO(permission): 权限中心接入后，此接口必须由 user:manage 后端授权中间件保护。
// @Summary 更新用户管理信息
// @Tags user
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body UserManagementUpdateRequest true "用户信息"
// @Success 200 {object} response.Response{data=UserDTO}
// @Router /api/v1/user/manage/update [post]
func (h *UserHandler) ManageUpdate(c *gin.Context) {
	var req UserManagementUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	if strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.Nickname) == "" {
		response.Error(c, "invalid params")
		return
	}

	tenantID, orgID := uuid.Nil, uuid.Nil
	if req.TenantID != nil {
		tenantID = *req.TenantID
	}
	if req.OrgID != nil {
		orgID = *req.OrgID
	}
	user, err := h.svc.UpdateManagement(withHTTPAuditContext(c), userapp.ManagementUpdateInput{
		UserID: req.UserID, Nickname: req.Nickname, Username: req.Username,
		Email: req.Email, Phone: req.Phone, TenantID: tenantID, OrgID: orgID,
		Operator: operator(c),
	})
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, toUserDTO(user))
}

// Allocate 批量分配或移除用户的租户、组织、项目归属。
func (h *UserHandler) Allocate(c *gin.Context) {
	var req UserAllocateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	affected, err := h.svc.Allocate(withHTTPAuditContext(c), userapp.AllocateInput{
		Type: req.Type, Operation: req.Operate, ResourceID: req.ResourceID,
		UserIDs: req.UserIDList, Operator: operator(c),
	})
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, UserAllocateResponse{AffectedCount: affected})
}

// Update 更新当前认证用户资料。
func (h *UserHandler) Update(c *gin.Context) {
	authUser, ok := userctx.MustFromContext(c)
	if !ok || authUser.UserID == "" {
		response.AbortWithHTTPStatus(c, 401)
		return
	}

	var req UserUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	if req.Nickname == "" || req.Username == "" || req.TenantID == uuid.Nil || req.OrgID == uuid.Nil {
		response.Error(c, "invalid params")
		return
	}

	user, err := h.svc.Update(withHTTPAuditContext(c), userapp.UpdateInput{
		UserID:   authUser.UserID,
		Nickname: req.Nickname,
		Username: req.Username,
		Email:    req.Email,
		Phone:    req.Phone,
		TenantID: req.TenantID,
		OrgID:    req.OrgID,
	})
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, toUserDTO(user))
}

func (h *UserHandler) respondError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	switch {
	case errors.Is(err, userapp.ErrInvalidParam),
		errors.Is(err, userapp.ErrNotFound),
		errors.Is(err, userapp.ErrUsernameExists),
		errors.Is(err, userapp.ErrTenantNotFound),
		errors.Is(err, userapp.ErrOrgNotFound),
		errors.Is(err, userapp.ErrOrgTenantMismatch),
		errors.Is(err, userapp.ErrProjectNotFound):
		response.Error(c, err.Error())
	default:
		response.Error(c, "internal error")
	}
}

func toUserDTO(user *userdomain.User) *UserDTO {
	return &UserDTO{
		ID:        user.ID,
		UserID:    user.UserID,
		Nickname:  user.Nickname,
		Username:  user.Username,
		Email:     user.Email,
		Phone:     user.Phone,
		TenantID:  user.TenantID,
		OrgID:     user.OrgID,
		IsBlocked: user.IsBlocked,
		CreateBy:  user.CreateBy,
		UpdateBy:  user.UpdateBy,
		CreateAt:  user.CreateAt,
		UpdateAt:  user.UpdateAt,
	}
}

func toUserProfileDTO(profile *userdomain.Profile) *UserProfileDTO {
	projects := make([]UserProfileProjectDTO, 0, len(profile.Projects))
	for _, project := range profile.Projects {
		projects = append(projects, UserProfileProjectDTO{ID: project.ID, Name: project.Name})
	}
	return &UserProfileDTO{
		UserDTO:     *toUserDTO(&profile.User),
		TenantName:  profile.TenantName,
		OrgName:     profile.OrgName,
		ProjectList: projects,
	}
}

func toUserManagementListItemDTO(user *userdomain.ManagementUser) UserManagementListItemDTO {
	return UserManagementListItemDTO{
		ID: user.ID, UserID: user.UserID, Nickname: user.Nickname, Username: user.Username,
		Email: user.Email, Phone: user.Phone, TenantID: user.TenantID, TenantName: user.TenantName,
		OrgID: user.OrgID, OrgName: user.OrgName, IsBlocked: user.IsBlocked,
		CreateAt: user.CreateAt, UpdateAt: user.UpdateAt,
	}
}
