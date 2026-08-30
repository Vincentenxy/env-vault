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
	GroupID        uuid.UUID  `json:"groupId"`
	Code           string     `json:"code"`
	Name           string     `json:"name"`
	EnvID          string     `json:"envId"`
	ParentFolderID *uuid.UUID `json:"parentFolderId"`
	Remark         string     `json:"remark"`
	Type           string     `json:"type"`
	Manager        string     `json:"manager"`
	KeyPattern     string     `json:"keyPattern"`
	ManagerName    string     `json:"managerName"`
	SecretCount    int64      `json:"secretCount"`
	FolderCount    *int64     `json:"folderCount"`
	CreateBy       string     `json:"createBy"`
	UpdateBy       string     `json:"updateBy"`
	CreateAt       time.Time  `json:"createAt"`
	UpdateAt       time.Time  `json:"updateAt"`
}

// CreateFolderRequest 创建文件夹请求：
//   - parentFolderId 为空 → 项目下创建顶级目录（项目下所有环境各一条，全环境共享 groupId）
//   - parentFolderId 非空 → 在 groups 目录下创建二级目录（全环境展开，全环境共享 groupId）
type CreateFolderRequest struct {
	ProjectID      uuid.UUID  `json:"projectId"`
	ParentFolderID *uuid.UUID `json:"parentFolderId"`
	Code           string     `json:"code"`
	Name           string     `json:"name"`
	Remark         string     `json:"remark"`
	Type           string     `json:"type"` // common/customer
	Manager        string     `json:"manager,omitempty"`
	KeyPattern     string     `json:"keyPattern,omitempty"`
}

// UpdateFolderRequest 更新文件夹请求（按 group_id 全环境同步）
type UpdateFolderRequest struct {
	GroupID    uuid.UUID `json:"groupId"`
	Name       string    `json:"name"`
	Remark     string    `json:"remark"`
	Manager    string    `json:"manager,omitempty"`
	KeyPattern *string   `json:"keyPattern,omitempty"`
}

// DeleteFolderRequest 删除文件夹请求（按 group_id 软删除全环境记录）
type DeleteFolderRequest struct {
	GroupID uuid.UUID `json:"groupId"`
}

// DetailFolderRequest 文件夹详情请求
type DetailFolderRequest struct {
	ID uuid.UUID `json:"id"`
}

// ListFolderRequest 文件夹列表请求（分页字段 pageNum/pageSize 来自内嵌的 page.Request）：
//   - parentFolderId 非空 → 查询该 parent 下的子目录（每 code 一条，全环境共享 group_id）
//   - parentFolderId 为空 → 查询项目下的顶级目录
type ListFolderRequest struct {
	ProjectID      uuid.UUID  `json:"projectId"`
	ParentFolderID *uuid.UUID `json:"parentFolderId,omitempty"`
	Code           string     `json:"code,omitempty"`
	Name           string     `json:"name,omitempty"`
	page.Request
}

// Create 创建文件夹（按 parentFolderId 区分顶级 / 二级）
func (h *FolderHandler) Create(c *gin.Context) {
	var req CreateFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	// 字段校验（统一在 handler 层完成）
	if req.Code == "" || req.Name == "" || req.Type == "" {
		response.Error(c, "invalid params")
		return
	}
	if req.ParentFolderID == nil || *req.ParentFolderID == uuid.Nil {
		// 顶级：需 ProjectID；Type=common 时 Code 必须是 global/groups
		if req.ProjectID == uuid.Nil {
			response.Error(c, "invalid params")
			return
		}
		if err := validateTopType(req.Type, req.Code); err != nil {
			response.Error(c, err.Error())
			return
		}
	} else if req.Type != folderdomain.TypeCommon {
		// 二级：Type 必须为 common
		response.Error(c, folderapp.ErrInvalidType.Error())
		return
	}

	operator := operator(c)
	var folders []*folderdomain.Folder
	var err error
	if req.ParentFolderID == nil || *req.ParentFolderID == uuid.Nil {
		// 创建接口1：项目下创建顶级目录，默认在所有环境创建
		folders, err = h.svc.CreateTop(withHTTPAuditContext(c), folderapp.CreateTopInput{
			ProjectID:  req.ProjectID,
			Code:       req.Code,
			Name:       req.Name,
			Remark:     req.Remark,
			Type:       req.Type,
			Manager:    req.Manager,
			KeyPattern: req.KeyPattern,
		}, operator)
	} else {
		// 创建接口2：在 groups 目录下创建二级目录
		folders, err = h.svc.CreateSub(withHTTPAuditContext(c), folderapp.CreateSubInput{
			ParentFolderID: *req.ParentFolderID,
			Code:           req.Code,
			Name:           req.Name,
			Remark:         req.Remark,
			Type:           req.Type,
			Manager:        req.Manager,
			KeyPattern:     req.KeyPattern,
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

// Update 更新文件夹（按 group_id 同步各环境下的目录）
func (h *FolderHandler) Update(c *gin.Context) {
	var req UpdateFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	if req.GroupID == uuid.Nil && (req.Name == "" || req.Remark == "") {
		response.Error(c, "invalid params")
		return
	}

	err := h.svc.Update(withHTTPAuditContext(c), folderapp.UpdateInput{
		GroupID:    req.GroupID,
		Name:       req.Name,
		Remark:     req.Remark,
		Manager:    req.Manager,
		KeyPattern: req.KeyPattern,
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

	if req.GroupID == uuid.Nil {
		response.Error(c, "invalid params")
		return
	}

	err := h.svc.Delete(withHTTPAuditContext(c), folderapp.DeleteInput{
		GroupID: req.GroupID,
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

	if req.ID == uuid.Nil {
		response.Error(c, "invalid params")
		return
	}

	f, err := h.svc.GetByID(withHTTPAuditContext(c), req.ID)
	h.respondError(c, err)
	if err != nil {
		return
	}
	response.Success(c, toFolderDTO(f))
}

// List 文件夹列表（分页）：
//   - parentFolderId 非空 → 该 parent 下的子目录
//   - parentFolderId 为空 → 项目下的顶级目录（需 projectId）
func (h *FolderHandler) List(c *gin.Context) {
	var req ListFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}

	if req.ParentFolderID == nil || *req.ParentFolderID == uuid.Nil {
		// 顶级目录：projectId 必填
		if req.ProjectID == uuid.Nil {
			response.Error(c, "invalid params")
			return
		}
	} else if req.ProjectID == uuid.Nil {
		// 子目录：projectId 可选；不传时填零值，service 不依赖
		req.ProjectID = uuid.Nil
	}
	req.Normalize()

	folders, total, err := h.svc.List(withHTTPAuditContext(c), folderapp.ListInput{
		ProjectID:      req.ProjectID,
		ParentFolderID: req.ParentFolderID,
		Code:           req.Code,
		Name:           req.Name,
		PageNum:        req.PageNum,
		PageSize:       req.PageSize,
	})
	h.respondError(c, err)
	if err != nil {
		return
	}

	list := make([]FolderDTO, 0, len(folders))
	for _, f := range folders {
		// List 按 groupId 聚合后只返回一条代表记录，环境 ID 没有确定的业务含义。
		// 保留字段以兼容前端结构，但不暴露代表记录所属的任意环境。
		dto := toFolderDTO(f)
		dto.EnvID = ""
		list = append(list, *dto)
	}
	response.Success(c, page.Response[FolderDTO]{
		Total: total,
		List:  list,
	})
}

// validateTopType 顶级目录类型校验：type 必须合法；common 顶级目录仅支持 global / groups
func validateTopType(t, code string) error {
	switch t {
	case folderdomain.TypeCommon:
		if code != "global" && code != "groups" {
			return folderapp.ErrCommonCodeInvalid
		}
	case folderdomain.TypeCustomer:
		// customer 仅一级，顶级创建合法
	default:
		return folderapp.ErrInvalidType
	}
	return nil
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
		errors.Is(err, folderapp.ErrGroupsNotFound),
		errors.Is(err, folderapp.ErrInvalidKeyPattern):
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
		GroupID:        f.GroupID,
		Code:           f.Code,
		Name:           f.Name,
		EnvID:          f.EnvID.String(),
		ParentFolderID: f.ParentFolderID,
		Remark:         f.Remark,
		Type:           f.Type,
		Manager:        f.Manager,
		KeyPattern:     f.KeyPattern,
		ManagerName:    f.ManagerName,
		SecretCount:    f.SecretCount,
		FolderCount:    f.FolderCount,
		CreateBy:       f.CreateBy,
		UpdateBy:       f.UpdateBy,
		CreateAt:       f.CreateAt,
		UpdateAt:       f.UpdateAt,
	}
}
