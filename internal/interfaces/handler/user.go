package handler

import (
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	userapp "env-vault/internal/application/user"
	userdomain "env-vault/internal/domain/user"
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

// UserListItemDTO 用户列表项，只包含公开展示字段。
type UserListItemDTO struct {
	ID       uuid.UUID `json:"id"`
	UserID   string    `json:"userId"`
	Nickname string    `json:"nickname"`
}

// UserDTO 用户资料响应，不包含密码哈希和软删除字段。
type UserDTO struct {
	ID       uuid.UUID `json:"id"`
	UserID   string    `json:"userId"`
	Nickname string    `json:"nickname"`
	Username string    `json:"username"`
	Email    string    `json:"email"`
	Phone    string    `json:"phone"`
	TenantID uuid.UUID `json:"tenantId"`
	OrgID    uuid.UUID `json:"orgId"`
	CreateBy string    `json:"createBy"`
	UpdateBy string    `json:"updateBy"`
	CreateAt time.Time `json:"createAt"`
	UpdateAt time.Time `json:"updateAt"`
}

// Me 获取当前认证用户资料。
// @Summary 获取当前用户信息
// @Description 从 JWT 获取当前用户标识，并返回数据库中的用户资料，不包含密码等敏感信息
// @Tags auth
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Response{data=UserDTO}
// @Failure 401 {object} response.Response
// @Router /api/v1/auth/me [get]
func (h *UserHandler) Me(c *gin.Context) {
	authUser, ok := userctx.MustFromContext(c)
	if !ok || authUser.UserID == "" {
		response.AbortWithHTTPStatus(c, 401)
		return
	}

	user, err := h.svc.GetProfile(c, authUser.UserID)
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, toUserDTO(user))
}

// List 查询用户列表。
func (h *UserHandler) List(c *gin.Context) {
	var req UserListRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	users, err := h.svc.List(c, userapp.ListInput{
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
		list = append(list, UserListItemDTO{
			ID:       user.ID,
			UserID:   user.UserID,
			Nickname: user.Nickname,
		})
	}
	response.Success(c, list)
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

	user, err := h.svc.Update(c, userapp.UpdateInput{
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
		errors.Is(err, userapp.ErrUsernameExists):
		response.Error(c, err.Error())
	default:
		response.Error(c, "internal error")
	}
}

func toUserDTO(user *userdomain.User) *UserDTO {
	return &UserDTO{
		ID:       user.ID,
		UserID:   user.UserID,
		Nickname: user.Nickname,
		Username: user.Username,
		Email:    user.Email,
		Phone:    user.Phone,
		TenantID: user.TenantID,
		OrgID:    user.OrgID,
		CreateBy: user.CreateBy,
		UpdateBy: user.UpdateBy,
		CreateAt: user.CreateAt,
		UpdateAt: user.UpdateAt,
	}
}
