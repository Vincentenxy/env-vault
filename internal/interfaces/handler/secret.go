package handler

import (
	"errors"
	"strings"
	"time"

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

// UpdateSecretValueRequest 更新时单个环境下的值入参
type UpdateSecretValueRequest struct {
	SecretID uuid.UUID `json:"secretId"` // 必填：secret_info.id，唯一定位具体环境实例
	EnvCode  string    `json:"envCode"`  // 透传字段，不参与业务逻辑
	FolderID uuid.UUID `json:"folderId"` // 透传字段，不参与业务逻辑
	Value    string    `json:"value"`    // 必填：明文
}

// UpdateSecretItemRequest 更新时单个 secret 的入参
type UpdateSecretItemRequest struct {
	GroupID   uuid.UUID                  `json:"groupId"` // 必填：secret 业务组 ID
	Key       string                     `json:"key"`     // 透传展示，不校验
	Remark    string                     `json:"remark"`  // 非空时整组同步更新
	CommitMsg string                     `json:"commitMsg"`
	Values    []UpdateSecretValueRequest `json:"values"` // 非空时按 secretId 逐条更新
}

// UpdateSecretRequest 批量更新 secrets 请求（整请求单事务）
type UpdateSecretRequest struct {
	CommitMsg string                    `json:"commitMsg"`
	Secrets   []UpdateSecretItemRequest `json:"secrets"`
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

// SecretHistoryRequest 历史查询请求，优先级 secretId > batchId > groupId
type SecretHistoryRequest struct {
	SecretID uuid.UUID `json:"secretId"`
	BatchID  uuid.UUID `json:"batchId"`
	GroupID  uuid.UUID `json:"groupId"`
	PageNum  int       `json:"pageNum"`
	PageSize int       `json:"pageSize"`
}

// SecretValueDTO 单个环境下的值（解密后，values 的 key 即 env code）
type SecretValueDTO struct {
	SecretID  uuid.UUID `json:"secretId"`
	FolderID  uuid.UUID `json:"folderId"`
	Value     string    `json:"value"`
	Version   int       `json:"version"`
	ValueType string    `json:"valueType"`
}

// SecretHistoryDTO 单条解密后的 value 历史版本
type SecretHistoryDTO struct {
	ID        uuid.UUID `json:"id"`
	SecretID  uuid.UUID `json:"secretId"`
	BatchID   uuid.UUID `json:"batchId"`
	GroupID   uuid.UUID `json:"groupId"`
	FolderID  uuid.UUID `json:"folderId"`
	EnvCode   string    `json:"envCode"`
	Value     string    `json:"value"`
	ValueType string    `json:"valueType"`
	Version   int       `json:"version"`
	CommitMsg string    `json:"commitMsg"`
	CreateBy  string    `json:"createBy"`
	CreateAt  time.Time `json:"createAt"`
}

type SecretHistoryResponse struct {
	Total       int64              `json:"total"`
	HistoryList []SecretHistoryDTO `json:"historyList"`
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

	created, err := h.svc.Create(c, secretapp.CreateInput{SecretList: items, BatchID: uuid.New()}, operator(c))
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

// Update 批量更新 secrets（整请求单事务）：
//   - 按 groupId 定位 secret 业务组
//   - remark 非空：整组同步更新（不影响 version）
//   - values 非空：按 secretId 精确定位，仅实际变化时更新 value/version+1
//   - key 字段透传，不参与业务校验
func (h *SecretHandler) Update(c *gin.Context) {
	var req UpdateSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	// 字段校验（统一在 handler 层完成）
	if len(req.Secrets) == 0 {
		response.Error(c, "invalid params")
		return
	}
	for _, item := range req.Secrets {
		if item.GroupID == uuid.Nil || (strings.TrimSpace(req.CommitMsg) == "" && strings.TrimSpace(item.CommitMsg) == "") {
			response.Error(c, "invalid params")
			return
		}
		for _, v := range item.Values {
			if v.SecretID == uuid.Nil {
				response.Error(c, "invalid params")
				return
			}
		}
	}

	items := make([]secretapp.UpdateItemInput, 0, len(req.Secrets))
	for _, item := range req.Secrets {
		values := make([]secretapp.UpdateValueInput, 0, len(item.Values))
		for _, v := range item.Values {
			values = append(values, secretapp.UpdateValueInput{
				SecretID: v.SecretID,
				EnvCode:  v.EnvCode,
				FolderID: v.FolderID,
				Value:    v.Value,
			})
		}
		items = append(items, secretapp.UpdateItemInput{
			GroupID:   item.GroupID,
			Key:       item.Key,
			Remark:    item.Remark,
			CommitMsg: item.CommitMsg,
			Values:    values,
		})
	}

	batchID := uuid.New()
	err := h.svc.Update(c, secretapp.UpdateInput{
		BatchID:   batchID,
		CommitMsg: req.CommitMsg,
		Secrets:   items,
	}, operator(c))
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, map[string]any{"batchId": batchID})
}

// History 查询 value 历史版本
func (h *SecretHandler) History(c *gin.Context) {
	var req SecretHistoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	views, total, err := h.svc.History(c, secretapp.HistoryInput{
		SecretID: req.SecretID,
		BatchID:  req.BatchID,
		GroupID:  req.GroupID,
		PageNum:  req.PageNum,
		PageSize: req.PageSize,
	})
	h.respondError(c, err)
	if err != nil {
		return
	}

	list := make([]SecretHistoryDTO, 0, len(views))
	for _, view := range views {
		list = append(list, SecretHistoryDTO{
			ID:        view.ID,
			SecretID:  view.SecretID,
			BatchID:   view.BatchID,
			GroupID:   view.GroupID,
			FolderID:  view.FolderID,
			EnvCode:   view.EnvCode,
			Value:     view.Value,
			ValueType: view.ValueType,
			Version:   view.Version,
			CommitMsg: view.CommitMsg,
			CreateBy:  view.CreateBy,
			CreateAt:  view.CreateAt,
		})
	}
	response.Success(c, SecretHistoryResponse{Total: total, HistoryList: list})
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
		errors.Is(err, secretapp.ErrDecrypt),
		errors.Is(err, secretapp.ErrSecretNotUnderGroup),
		errors.Is(err, secretapp.ErrHistoryGroupNotSupported),
		errors.Is(err, secretapp.ErrVersionConflict):
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
			SecretID:  sv.SecretID,
			FolderID:  sv.FolderID,
			Value:     sv.Value,
			Version:   sv.Version,
			ValueType: sv.ValueType,
		}
	}
	return SecretViewDTO{
		GroupID: v.GroupID,
		Key:     v.Key,
		Remark:  v.Remark,
		Values:  values,
	}
}
