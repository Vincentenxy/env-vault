package handler

import (
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	tokenapp "env-vault/internal/application/useraccesstoken"
	"env-vault/pkg/response"
	"env-vault/pkg/userctx"
)

// UserAccessTokenHandler manages PATs owned by the authenticated user
type UserAccessTokenHandler struct{ svc tokenapp.IService }

func NewUserAccessTokenHandler(svc tokenapp.IService) *UserAccessTokenHandler {
	return &UserAccessTokenHandler{svc: svc}
}

// CreateUserAccessTokenRequest contains the token label and selected expiration
type CreateUserAccessTokenRequest struct {
	Name      string    `json:"name"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// DeleteUserAccessTokenRequest identifies one token owned by the current user
type DeleteUserAccessTokenRequest struct {
	ID uuid.UUID `json:"id"`
}

// UserAccessTokenDTO returns plaintext only to the authenticated token owner
type UserAccessTokenDTO struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	Token      string     `json:"token"`
	CreateAt   time.Time  `json:"createAt"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
}

// Create creates one PAT for the authenticated user
// @Summary 创建个人 Token
// @Tags user-token
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreateUserAccessTokenRequest true "个人 Token"
// @Success 200 {object} response.Response{data=UserAccessTokenDTO}
// @Router /api/v1/user/token/create [post]
func (h *UserAccessTokenHandler) Create(c *gin.Context) {
	var req CreateUserAccessTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	authUser, ok := userctx.MustFromContext(c)
	if !ok || authUser.UserID == "" {
		response.AbortWithHTTPStatus(c, 401)
		return
	}

	item, err := h.svc.Create(withHTTPAuditContext(c), tokenapp.CreateInput{
		Name: req.Name, ExpiresAt: req.ExpiresAt,
		UserID: authUser.UserID, TokenUse: authUser.TokenUse,
	})
	if !h.respondError(c, err) {
		return
	}
	setNoStore(c)
	response.Success(c, toUserAccessTokenDTO(*item))
}

// List returns all undeleted PATs with plaintext values for the current owner
// @Summary 查询个人 Token 列表
// @Tags user-token
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} response.Response{data=[]UserAccessTokenDTO}
// @Router /api/v1/user/token/list [post]
func (h *UserAccessTokenHandler) List(c *gin.Context) {
	items, err := h.svc.List(withHTTPAuditContext(c), tokenapp.ListInput{UserID: operator(c)})
	if !h.respondError(c, err) {
		return
	}
	list := make([]UserAccessTokenDTO, 0, len(items))
	for _, item := range items {
		list = append(list, toUserAccessTokenDTO(item))
	}
	setNoStore(c)
	response.Success(c, list)
}

// Delete soft-deletes one PAT owned by the authenticated user
// @Summary 删除个人 Token
// @Tags user-token
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body DeleteUserAccessTokenRequest true "个人 Token"
// @Success 200 {object} response.Response
// @Router /api/v1/user/token/delete [post]
func (h *UserAccessTokenHandler) Delete(c *gin.Context) {
	var req DeleteUserAccessTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	if !h.respondError(c, h.svc.Delete(withHTTPAuditContext(c), tokenapp.DeleteInput{ID: req.ID, UserID: operator(c)})) {
		return
	}
	response.Success(c, nil)
}

func (h *UserAccessTokenHandler) respondError(c *gin.Context, err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, tokenapp.ErrPATCannotCreate) {
		response.AbortWithHTTPStatusMessage(c, 403, "个人 Token 不能创建新的 Token")
		return false
	}
	switch {
	case errors.Is(err, tokenapp.ErrInvalidParam), errors.Is(err, tokenapp.ErrNotFound),
		errors.Is(err, tokenapp.ErrLimitReached), errors.Is(err, tokenapp.ErrIssueToken),
		errors.Is(err, tokenapp.ErrEncryptToken), errors.Is(err, tokenapp.ErrDecryptToken):
		response.Error(c, err.Error())
	default:
		response.Error(c, "internal error")
	}
	return false
}

func toUserAccessTokenDTO(item tokenapp.TokenView) UserAccessTokenDTO {
	return UserAccessTokenDTO{
		ID: item.ID, Name: item.Name, Token: item.Token,
		CreateAt: item.CreateAt, ExpiresAt: item.ExpiresAt, LastUsedAt: item.LastUsedAt,
	}
}
