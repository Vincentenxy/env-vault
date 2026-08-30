package handler

import (
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	personalapp "env-vault/internal/application/personalsecret"
	"env-vault/pkg/page"
	"env-vault/pkg/response"
)

type PersonalSecretHandler struct{ svc personalapp.IService }

func NewPersonalSecretHandler(svc personalapp.IService) *PersonalSecretHandler {
	return &PersonalSecretHandler{svc: svc}
}

type CreatePersonalSecretRequest struct {
	Name           string `json:"name"`
	CredentialType string `json:"credentialType"`
	Account        string `json:"account"`
	LoginURL       string `json:"loginUrl"`
	Value          string `json:"value"`
	Remark         string `json:"remark"`
	CommitMsg      string `json:"commitMsg"`
}

type UpdatePersonalSecretRequest struct {
	ID             uuid.UUID `json:"id"`
	Version        int       `json:"version"`
	Name           string    `json:"name"`
	CredentialType string    `json:"credentialType"`
	Account        string    `json:"account"`
	LoginURL       string    `json:"loginUrl"`
	Value          string    `json:"value"`
	Remark         string    `json:"remark"`
	CommitMsg      string    `json:"commitMsg"`
}

type DeletePersonalSecretRequest struct {
	ID      uuid.UUID `json:"id"`
	Version int       `json:"version"`
}

type ListPersonalSecretRequest struct {
	Keyword string `json:"keyword"`
	page.Request
}

type ManageListPersonalSecretRequest struct {
	UserID  string `json:"userId"`
	Keyword string `json:"keyword"`
	page.Request
}

type RevealPersonalSecretRequest struct {
	ID uuid.UUID `json:"id"`
}

type PersonalSecretHistoryRequest struct {
	PersonalSecretID uuid.UUID `json:"personalSecretId"`
	page.Request
}

type RevealPersonalSecretHistoryRequest struct {
	PersonalSecretID uuid.UUID `json:"personalSecretId"`
	HistoryID        uuid.UUID `json:"historyId"`
}

type PersonalSecretDTO struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	CredentialType string    `json:"credentialType"`
	Account        string    `json:"account"`
	LoginURL       string    `json:"loginUrl"`
	Remark         string    `json:"remark"`
	Version        int       `json:"version"`
	CreateBy       string    `json:"createBy"`
	CreateByName   string    `json:"createByName"`
	UpdateBy       string    `json:"updateBy"`
	UpdateByName   string    `json:"updateByName"`
	CreateAt       time.Time `json:"createAt"`
	UpdateAt       time.Time `json:"updateAt"`
}

type PersonalSecretListDTO struct {
	PersonalSecretDTO
	Value string `json:"value"`
}

type PersonalSecretHistoryDTO struct {
	ID               uuid.UUID `json:"id"`
	PersonalSecretID uuid.UUID `json:"personalSecretId"`
	BatchID          uuid.UUID `json:"batchId"`
	Name             string    `json:"name"`
	CredentialType   string    `json:"credentialType"`
	Account          string    `json:"account"`
	LoginURL         string    `json:"loginUrl"`
	Remark           string    `json:"remark"`
	Version          int       `json:"version"`
	CommitMsg        string    `json:"commitMsg"`
	CreateBy         string    `json:"createBy"`
	CreateByName     string    `json:"createByName"`
	CreateAt         time.Time `json:"createAt"`
}

type PersonalSecretRevealDTO struct {
	ID      uuid.UUID `json:"id"`
	Value   string    `json:"value"`
	Version int       `json:"version"`
}

type PersonalSecretHistoryRevealDTO struct {
	ID               uuid.UUID `json:"id"`
	PersonalSecretID uuid.UUID `json:"personalSecretId"`
	Value            string    `json:"value"`
	Version          int       `json:"version"`
}

// Create creates a personal credential for the authenticated user.
// @Summary 创建个人密钥
// @Tags user-secret
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body CreatePersonalSecretRequest true "个人密钥"
// @Success 200 {object} response.Response{data=PersonalSecretDTO}
// @Router /api/v1/user/secret/create [post]
func (h *PersonalSecretHandler) Create(c *gin.Context) {
	var req CreatePersonalSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	item, err := h.svc.Create(withHTTPAuditContext(c), personalapp.CreateInput{
		Name: req.Name, CredentialType: req.CredentialType, Account: req.Account,
		LoginURL: req.LoginURL, Value: req.Value, Remark: req.Remark,
		CommitMsg: req.CommitMsg, UserID: operator(c),
	})
	if !h.respondError(c, err) {
		return
	}
	response.Success(c, toPersonalSecretDTO(*item))
}

// Update updates metadata and optionally rotates the password. Empty value keeps the current password.
// @Summary 更新个人密钥
// @Tags user-secret
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body UpdatePersonalSecretRequest true "个人密钥"
// @Success 200 {object} response.Response{data=PersonalSecretDTO}
// @Router /api/v1/user/secret/update [post]
func (h *PersonalSecretHandler) Update(c *gin.Context) {
	var req UpdatePersonalSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	item, err := h.svc.Update(withHTTPAuditContext(c), personalapp.UpdateInput{
		ID: req.ID, Version: req.Version, Name: req.Name, CredentialType: req.CredentialType,
		Account: req.Account, LoginURL: req.LoginURL, Value: req.Value, Remark: req.Remark,
		CommitMsg: req.CommitMsg, UserID: operator(c),
	})
	if !h.respondError(c, err) {
		return
	}
	response.Success(c, toPersonalSecretDTO(*item))
}

// Delete soft-deletes one personal credential owned by the authenticated user.
// @Summary 删除个人密钥
// @Tags user-secret
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body DeletePersonalSecretRequest true "个人密钥"
// @Success 200 {object} response.Response
// @Router /api/v1/user/secret/delete [post]
func (h *PersonalSecretHandler) Delete(c *gin.Context) {
	var req DeletePersonalSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	err := h.svc.Delete(withHTTPAuditContext(c), personalapp.DeleteInput{
		ID: req.ID, Version: req.Version, UserID: operator(c),
	})
	if !h.respondError(c, err) {
		return
	}
	response.Success(c, nil)
}

// List returns the authenticated user's metadata and decrypted current values.
// @Summary 查询个人密钥列表
// @Tags user-secret
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ListPersonalSecretRequest true "查询条件"
// @Success 200 {object} response.Response{data=page.Response[PersonalSecretListDTO]}
// @Router /api/v1/user/secret/list [post]
func (h *PersonalSecretHandler) List(c *gin.Context) {
	var req ListPersonalSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	req.Normalize()
	items, total, err := h.svc.List(withHTTPAuditContext(c), personalapp.ListInput{
		Keyword: req.Keyword, UserID: operator(c), PageNum: req.PageNum, PageSize: req.PageSize,
	})
	if !h.respondError(c, err) {
		return
	}
	list := make([]PersonalSecretListDTO, 0, len(items))
	for _, item := range items {
		list = append(list, PersonalSecretListDTO{
			PersonalSecretDTO: toPersonalSecretDTO(item),
			Value:             item.Value,
		})
	}
	setNoStore(c)
	response.Success(c, page.Response[PersonalSecretListDTO]{Total: total, List: list})
}

// ManageList returns decrypted credentials owned by a locked user.
// TODO(permission): require user:manage after the permission center is connected.
// @Summary 查询已锁定用户的个人密钥
// @Tags user-secret
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body ManageListPersonalSecretRequest true "查询条件"
// @Success 200 {object} response.Response{data=page.Response[PersonalSecretListDTO]}
// @Router /api/v1/user/secret/manage/list [post]
func (h *PersonalSecretHandler) ManageList(c *gin.Context) {
	var req ManageListPersonalSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	req.Normalize()
	items, total, err := h.svc.ManageList(withHTTPAuditContext(c), personalapp.ManageListInput{
		TargetUserID: req.UserID, Keyword: req.Keyword, Operator: operator(c),
		PageNum: req.PageNum, PageSize: req.PageSize,
	})
	if !h.respondError(c, err) {
		return
	}
	list := make([]PersonalSecretListDTO, 0, len(items))
	for _, item := range items {
		list = append(list, PersonalSecretListDTO{
			PersonalSecretDTO: toPersonalSecretDTO(item),
			Value:             item.Value,
		})
	}
	setNoStore(c)
	response.Success(c, page.Response[PersonalSecretListDTO]{Total: total, List: list})
}

// Reveal returns one plaintext password and explicitly disables HTTP caching.
// @Summary 查看个人密钥值
// @Tags user-secret
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body RevealPersonalSecretRequest true "个人密钥"
// @Success 200 {object} response.Response{data=PersonalSecretRevealDTO}
// @Router /api/v1/user/secret/reveal [post]
func (h *PersonalSecretHandler) Reveal(c *gin.Context) {
	var req RevealPersonalSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	item, err := h.svc.Reveal(withHTTPAuditContext(c), personalapp.RevealInput{ID: req.ID, UserID: operator(c)})
	if !h.respondError(c, err) {
		return
	}
	setNoStore(c)
	response.Success(c, PersonalSecretRevealDTO{ID: item.ID, Value: item.Value, Version: item.Version})
}

// History returns immutable version metadata owned by the authenticated user.
// @Summary 查询个人密钥历史
// @Tags user-secret
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body PersonalSecretHistoryRequest true "历史查询条件"
// @Success 200 {object} response.Response{data=page.Response[PersonalSecretHistoryDTO]}
// @Router /api/v1/user/secret/history [post]
func (h *PersonalSecretHandler) History(c *gin.Context) {
	var req PersonalSecretHistoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	req.Normalize()
	items, total, err := h.svc.History(withHTTPAuditContext(c), personalapp.HistoryInput{
		PersonalSecretID: req.PersonalSecretID, UserID: operator(c),
		PageNum: req.PageNum, PageSize: req.PageSize,
	})
	if !h.respondError(c, err) {
		return
	}
	list := make([]PersonalSecretHistoryDTO, 0, len(items))
	for _, item := range items {
		list = append(list, toPersonalSecretHistoryDTO(item))
	}
	setNoStore(c)
	response.Success(c, page.Response[PersonalSecretHistoryDTO]{Total: total, List: list})
}

// RevealHistory returns one historical plaintext value and disables HTTP caching.
// @Summary 查看个人密钥历史值
// @Tags user-secret
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body RevealPersonalSecretHistoryRequest true "历史版本"
// @Success 200 {object} response.Response{data=PersonalSecretHistoryRevealDTO}
// @Router /api/v1/user/secret/history/reveal [post]
func (h *PersonalSecretHandler) RevealHistory(c *gin.Context) {
	var req RevealPersonalSecretHistoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	item, err := h.svc.RevealHistory(withHTTPAuditContext(c), personalapp.RevealHistoryInput{
		PersonalSecretID: req.PersonalSecretID, HistoryID: req.HistoryID, UserID: operator(c),
	})
	if !h.respondError(c, err) {
		return
	}
	setNoStore(c)
	response.Success(c, PersonalSecretHistoryRevealDTO{
		ID: item.ID, PersonalSecretID: item.PersonalSecretID, Value: item.Value, Version: item.Version,
	})
}

func (h *PersonalSecretHandler) respondError(c *gin.Context, err error) bool {
	if err == nil {
		return true
	}
	switch {
	case errors.Is(err, personalapp.ErrInvalidParam), errors.Is(err, personalapp.ErrNotFound),
		errors.Is(err, personalapp.ErrEncrypt), errors.Is(err, personalapp.ErrDecrypt),
		errors.Is(err, personalapp.ErrOwnerNotBlocked), errors.Is(err, personalapp.ErrVersionConflict):
		response.Error(c, err.Error())
	default:
		response.Error(c, "internal error")
	}
	return false
}

func setNoStore(c *gin.Context) {
	c.Header("Cache-Control", "no-store, private")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
}

func toPersonalSecretDTO(item personalapp.SecretView) PersonalSecretDTO {
	return PersonalSecretDTO{
		ID: item.ID, Name: item.Name, CredentialType: item.CredentialType,
		Account: item.Account, LoginURL: item.LoginURL,
		Remark: item.Remark, Version: item.Version,
		CreateBy: item.CreateBy, CreateByName: item.CreateByName,
		UpdateBy: item.UpdateBy, UpdateByName: item.UpdateByName,
		CreateAt: item.CreateAt, UpdateAt: item.UpdateAt,
	}
}

func toPersonalSecretHistoryDTO(item personalapp.HistoryView) PersonalSecretHistoryDTO {
	return PersonalSecretHistoryDTO{
		ID: item.ID, PersonalSecretID: item.PersonalSecretID, BatchID: item.BatchID,
		Name: item.Name, CredentialType: item.CredentialType, Account: item.Account,
		LoginURL: item.LoginURL, Remark: item.Remark, Version: item.Version,
		CommitMsg: item.CommitMsg, CreateBy: item.CreateBy, CreateByName: item.CreateByName, CreateAt: item.CreateAt,
	}
}
