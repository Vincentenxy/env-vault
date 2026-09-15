package handler

import (
	"context"
	"errors"

	secretapp "env-vault/internal/application/secret"
	tagdomain "env-vault/internal/domain/tag"
	"env-vault/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// SecretTagService 定义不读取密钥明文的标签用例
type SecretTagService interface {
	Info(context.Context, uuid.UUID) (*secretapp.GroupTags, error)
	Update(context.Context, uuid.UUID, []uuid.UUID, string) (*secretapp.GroupTags, error)
}

// SecretTagHandler 处理整组密钥的标签维护
type SecretTagHandler struct{ svc SecretTagService }

func NewSecretTagHandler(svc SecretTagService) *SecretTagHandler {
	return &SecretTagHandler{svc: svc}
}

// UpdateSecretTagsRequest 完整替换标签，空数组表示清空，不能省略 tagIdList
type UpdateSecretTagsRequest struct {
	GroupID   uuid.UUID   `json:"groupId" binding:"required"`
	TagIDList []uuid.UUID `json:"tagIdList"`
}

// Info 查询密钥标签及实际所属租户
// @Summary 查询整组密钥标签
// @Tags secret
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body DetailSecretRequest true "Secret groupId"
// @Success 200 {object} response.Response{data=secretapp.GroupTags}
// @Router /api/v1/secret/tag/info [post]
func (h *SecretTagHandler) Info(c *gin.Context) {
	var req DetailSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	result, err := h.svc.Info(withHTTPAuditContext(c), req.GroupID)
	if err != nil {
		h.respondError(c, err)
		return
	}
	response.Success(c, result)
}

// Update 保存整组标签，不修改 value 版本
// @Summary 替换整组密钥标签
// @Tags secret
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body UpdateSecretTagsRequest true "tagIdList 必填，空数组解绑全部"
// @Success 200 {object} response.Response{data=secretapp.GroupTags}
// @Router /api/v1/secret/tag/update [post]
func (h *SecretTagHandler) Update(c *gin.Context) {
	var req UpdateSecretTagsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	if req.TagIDList == nil {
		response.BadRequest(c, errors.New("tagIdList is required; use [] to clear tags"))
		return
	}
	result, err := h.svc.Update(withHTTPAuditContext(c), req.GroupID, req.TagIDList, operator(c))
	if err != nil {
		h.respondError(c, err)
		return
	}
	response.Success(c, result)
}

func (h *SecretTagHandler) respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, secretapp.ErrInvalidParam), errors.Is(err, tagdomain.ErrGroupNotFound), errors.Is(err, tagdomain.ErrTagNotInTenant):
		response.Error(c, err.Error())
	default:
		response.Error(c, "internal error")
	}
}
