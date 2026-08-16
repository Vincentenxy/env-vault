package handler

import (
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	folderapp "env-vault/internal/application/folder"
	folderdomain "env-vault/internal/domain/folder"
	"env-vault/pkg/page"
	"env-vault/pkg/response"
)

// FolderHandler 文件夹 HTTP 处理器（依赖应用层 IService 接口，便于单测）
type FolderHandler struct {
	svc folderapp.IService
}

// NewFolderHandler 创建文件夹处理器
func NewFolderHandler(svc folderapp.IService) *FolderHandler {
	return &FolderHandler{svc: svc}
}

// FolderDTO 文件夹响应数据（JSON 小驼峰）
type FolderDTO struct {
	ID             uuid.UUID  `json:"id"`
	Code           string     `json:"code"`
	Name           string     `json:"name"`
	EnvID          uuid.UUID  `json:"envId"`
	ParentFolderID *uuid.UUID `json:"parentFolderId"`
	Remark         string     `json:"remark"`
	Type           string     `json:"type"`
	CreateBy       string     `json:"createBy"`
	UpdateBy       string     `json:"updateBy"`
	CreateAt       time.Time  `json:"createAt"`
	UpdateAt       time.Time  `json:"updateAt"`
}

// CreateFolderRequest 创建文件夹请求：
//   - parentFolderId 为空 → 项目下创建顶级目录（项目下所有环境各一条）
//   - parentFolderId 非空 → 在 groups 目录下创建二级目录（全环境展开）
type CreateFolderRequest struct {
	ProjectID      uuid.UUID  `json:"projectId"`
	ParentFolderID *uuid.UUID `json:"parentFolderId"`
	Code           string     `json:"code"`
	Name           string     `json:"name"`
	Remark         string     `json:"remark"`
	Type           string     `json:"type"`
}

// UpdateFolderRequest 更新文件夹请求（仅 name/remark，按各环境下的 id 集合）
type UpdateFolderRequest struct {
	IDList []uuid.UUID `json:"idList"`
	Name   string      `json:"name"`
	Remark string      `json:"remark"`
}

// DeleteFolderRequest 删除文件夹请求（按项目 + 编码删除所有环境下的记录）
type DeleteFolderRequest struct {
	ProjectID  uuid.UUID `json:"projectId"`
	FolderCode string    `json:"folderCode"`
}

// DetailFolderRequest 文件夹详情请求
type DetailFolderRequest struct {
	ID uuid.UUID `json:"id"`
}

// ListFolderRequest 文件夹列表请求（分页字段 pageNum/pageSize 来自内嵌的 page.Request，仅顶级目录）
type ListFolderRequest struct {
	ProjectID uuid.UUID `json:"projectId"`
	Code      string    `json:"code,omitempty"`
	Name      string    `json:"name,omitempty"`
	page.Request
}

// Create 创建文件夹（按 parentFolderId 区分顶级 / 二级）
func (h *FolderHandler) Create(c *gin.Context) {
	var req CreateFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	operator := operator(c)
	var folders []*folderdomain.Folder
	var err error
	if req.ParentFolderID == nil || *req.ParentFolderID == uuid.Nil {
		// 创建接口1：项目下创建顶级目录，默认在所有环境创建
		folders, err = h.svc.CreateTop(c, folderapp.CreateTopInput{
			ProjectID: req.ProjectID,
			Code:      req.Code,
			Name:      req.Name,
			Remark:    req.Remark,
			Type:      req.Type,
		}, operator)
	} else {
		// 创建接口2：在 groups 目录下创建二级目录
		folders, err = h.svc.CreateSub(c, folderapp.CreateSubInput{
			ParentFolderID: *req.ParentFolderID,
			Code:           req.Code,
			Name:           req.Name,
			Remark:         req.Remark,
			Type:           req.Type,
		}, operator)
	}
	h.respondError(c, err)
	if err != nil {
		return
	}

	list := make([]FolderDTO, 0, len(folders))
	for _, f := range folders {
		list = append(list, *toFolderDTO(f))
	}
	response.Success(c, list)
}

// Update 更新文件夹（仅 name/remark，按各环境下的 id 集合）
func (h *FolderHandler) Update(c *gin.Context) {
	var req UpdateFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	err := h.svc.Update(c, folderapp.UpdateInput{
		IDList: req.IDList,
		Name:   req.Name,
		Remark: req.Remark,
	}, operator(c))
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, nil)
}

// Delete 删除文件夹（按项目 + 编码删除所有环境下的记录，软删除）
func (h *FolderHandler) Delete(c *gin.Context) {
	var req DeleteFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	err := h.svc.Delete(c, folderapp.DeleteInput{
		ProjectID:  req.ProjectID,
		FolderCode: req.FolderCode,
	}, operator(c))
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, nil)
}

// Detail 文件夹详情
func (h *FolderHandler) Detail(c *gin.Context) {
	var req DetailFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	f, err := h.svc.GetByID(c, req.ID)
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, toFolderDTO(f))
}

// List 文件夹列表（分页，仅顶级目录）
func (h *FolderHandler) List(c *gin.Context) {
	var req ListFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	req.Normalize()

	folders, total, err := h.svc.List(c, folderapp.ListInput{
		ProjectID: req.ProjectID,
		Code:      req.Code,
		Name:      req.Name,
		PageNum:   req.PageNum,
		PageSize:  req.PageSize,
	})
	h.respondError(c, err)
	if err != nil {
		return
	}

	list := make([]FolderDTO, 0, len(folders))
	for _, f := range folders {
		list = append(list, *toFolderDTO(f))
	}
	response.Success(c, page.Response[FolderDTO]{
		Total: total,
		List:  list,
	})
}

// respondError 应用层错误统一映射为业务错误码
func (h *FolderHandler) respondError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	switch {
	case errors.Is(err, folderapp.ErrCodeExists),
		errors.Is(err, folderapp.ErrNotFound),
		errors.Is(err, folderapp.ErrInvalidParam),
		errors.Is(err, folderapp.ErrInvalidType),
		errors.Is(err, folderapp.ErrCommonCodeInvalid),
		errors.Is(err, folderapp.ErrParentNotAllowed),
		errors.Is(err, folderapp.ErrNoEnvironment),
		errors.Is(err, folderapp.ErrGroupsNotFound):
		// 通用业务错误：统一 code=-1，msg 由 service 给出
		response.Error(c, err.Error())
	default:
		response.Error(c, "internal error")
	}
}

// toFolderDTO 领域模型转响应 DTO
func toFolderDTO(f *folderdomain.Folder) *FolderDTO {
	return &FolderDTO{
		ID:             f.ID,
		Code:           f.Code,
		Name:           f.Name,
		EnvID:          f.EnvID,
		ParentFolderID: f.ParentFolderID,
		Remark:         f.Remark,
		Type:           f.Type,
		CreateBy:       f.CreateBy,
		UpdateBy:       f.UpdateBy,
		CreateAt:       f.CreateAt,
		UpdateAt:       f.UpdateAt,
	}
}
