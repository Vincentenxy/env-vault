package handler

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	secretapp "env-vault/internal/application/secret"
	"env-vault/pkg/response"
)

// SecretHandler 密钥 HTTP 处理器（依赖应用层 IService 接口，便于单测）
type SecretHandler struct {
	svc secretapp.IService
}

// NewSecretHandler 创建密钥处理器
func NewSecretHandler(svc secretapp.IService) *SecretHandler {
	return &SecretHandler{svc: svc}
}

// CreateSecretValueRequest 创建时单个环境下的值入参
type CreateSecretValueRequest struct {
	EnvID uuid.UUID `json:"envId"`
	Value string    `json:"value"`
}

// CreateSecretItemRequest 创建时单个 secret 的入参
type CreateSecretItemRequest struct {
	FolderGroupID uuid.UUID                  `json:"folderGroupId"`
	Key           string                     `json:"key"`
	Remark        string                     `json:"remark"`
	Values        []CreateSecretValueRequest `json:"values"`
}

// CreateSecretRequest 批量创建 secrets 请求
type CreateSecretRequest struct {
	SecretList []CreateSecretItemRequest `json:"secretList"`
}

// ListSecretRequest 查询1请求：按 folder 业务组查询其下全部 secrets
type ListSecretRequest struct {
	FolderGroupID uuid.UUID `json:"folderGroupId"`
}

// DetailSecretRequest 查询2请求：按 secret 业务组查询
type DetailSecretRequest struct {
	GroupID uuid.UUID `json:"groupId"`
}

// DeleteSecretRequest 删除请求：按 group_id 逻辑删除全部环境实例
type DeleteSecretRequest struct {
	GroupID uuid.UUID `json:"groupId"`
}

// SecretValueDTO 单个环境下的值（解密后，values 的 key 即 env code）
type SecretValueDTO struct {
	FolderID uuid.UUID `json:"folderId"`
	Value    string    `json:"value"`
}

// SecretViewDTO 一个 secret 的聚合视图（values 的 key 为 env code）
type SecretViewDTO struct {
	GroupID uuid.UUID                 `json:"groupId"`
	Key     string                    `json:"key"`
	Remark  string                    `json:"remark"`
	Values  map[string]SecretValueDTO `json:"values"`
}

// Create 批量创建 secrets
func (h *SecretHandler) Create(c *gin.Context) {
	var req CreateSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	items := make([]secretapp.CreateItemInput, 0, len(req.SecretList))
	for _, item := range req.SecretList {
		values := make([]secretapp.ValueItemInput, 0, len(item.Values))
		for _, v := range item.Values {
			values = append(values, secretapp.ValueItemInput{
				EnvID: v.EnvID,
				Value: v.Value,
			})
		}
		items = append(items, secretapp.CreateItemInput{
			FolderGroupID: item.FolderGroupID,
			Key:           item.Key,
			Remark:        item.Remark,
			Values:        values,
		})
	}

	created, err := h.svc.Create(c, secretapp.CreateInput{SecretList: items}, operator(c))
	h.respondError(c, err)
	if err != nil {
		return
	}

	// 返回创建后的物理记录列表（密文），便于前端按 id 后续操作
	list := make([]SecretViewDTO, 0, len(created))
	for _, s := range created {
		list = append(list, SecretViewDTO{
			GroupID: s.GroupID,
			Key:     s.Key,
			Remark:  s.Remark,
		})
	}
	response.Success(c, list)
}

// List 查询1：按 folder 业务组查询其下全部 secrets 的聚合视图列表
func (h *SecretHandler) List(c *gin.Context) {
	var req ListSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	views, err := h.svc.ListByFolder(c, req.FolderGroupID)
	h.respondError(c, err)
	if err != nil {
		return
	}

	list := make([]SecretViewDTO, 0, len(views))
	for _, v := range views {
		list = append(list, toSecretViewDTO(v))
	}
	response.Success(c, map[string]any{"secretList": list})
}

// Detail 查询2：按 secret 业务组查询所有环境下的值信息（聚合视图）
func (h *SecretHandler) Detail(c *gin.Context) {
	var req DetailSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	view, err := h.svc.GetByGroup(c, req.GroupID)
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, toSecretViewDTO(*view))
}

// Delete 删除密钥（按 group_id 逻辑删除全部环境实例）
func (h *SecretHandler) Delete(c *gin.Context) {
	var req DeleteSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	err := h.svc.Delete(c, req.GroupID, operator(c))
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, nil)
}

// respondError 应用层错误统一映射为业务错误码
func (h *SecretHandler) respondError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	switch {
	case errors.Is(err, secretapp.ErrNotFound),
		errors.Is(err, secretapp.ErrInvalidParam),
		errors.Is(err, secretapp.ErrFolderNotFound),
		errors.Is(err, secretapp.ErrEnvNotFound),
		errors.Is(err, secretapp.ErrKeyExists),
		errors.Is(err, secretapp.ErrDecrypt):
		// 通用业务错误：统一 code=-1，msg 由 service 给出
		response.Error(c, err.Error())
	default:
		response.Error(c, "internal error")
	}
}

// toSecretViewDTO 聚合视图转响应 DTO
func toSecretViewDTO(v secretapp.SecretView) SecretViewDTO {
	values := make(map[string]SecretValueDTO, len(v.Values))
	for code, sv := range v.Values {
		values[code] = SecretValueDTO{
			FolderID: sv.FolderID,
			Value:    sv.Value,
		}
	}
	return SecretViewDTO{
		GroupID: v.GroupID,
		Key:     v.Key,
		Remark:  v.Remark,
		Values:  values,
	}
}
