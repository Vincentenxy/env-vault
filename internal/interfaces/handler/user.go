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
