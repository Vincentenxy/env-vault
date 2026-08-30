package handler

import (
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	auditapp "env-vault/internal/application/audit"
	auditdomain "env-vault/internal/domain/audit"
	"env-vault/pkg/page"
	"env-vault/pkg/response"
)

type AuditHandler struct{ svc auditapp.IService }

func NewAuditHandler(svc auditapp.IService) *AuditHandler { return &AuditHandler{svc: svc} }

type AuditListRequest struct {
	ResourceType string `json:"resourceType"`
	ResourceID   string `json:"resourceId"`
	ActionCode   string `json:"actionCode"`
	ResultCode   string `json:"resultCode"`
	page.Request
}

type AuditChangeDTO struct {
	Field    string `json:"field"`
	Before   any    `json:"before,omitempty"`
	After    any    `json:"after,omitempty"`
	Changed  bool   `json:"changed,omitempty"`
	Redacted bool   `json:"redacted"`
}

type AuditEventDTO struct {
	ID            uuid.UUID        `json:"id"`
	EventSource   string           `json:"eventSource"`
	EntryType     string           `json:"entryType"`
	CallerType    string           `json:"callerType"`
	CallerName    string           `json:"callerName"`
	CallerVersion string           `json:"callerVersion"`
	OperationName string           `json:"operationName"`
	ActionCode    string           `json:"actionCode"`
	ResultCode    string           `json:"resultCode"`
	ActorType     string           `json:"actorType"`
	ResourceType  string           `json:"resourceType"`
	ResourceID    string           `json:"resourceId"`
	ResourceName  string           `json:"resourceName"`
	ScopeType     string           `json:"scopeType"`
	ScopeID       string           `json:"scopeId"`
	BatchID       *uuid.UUID       `json:"batchId"`
	ChangeDetail  []AuditChangeDTO `json:"changeDetail"`
	EventDetail   map[string]any   `json:"eventDetail"`
	FailureCode   string           `json:"failureCode"`
	FailureReason string           `json:"failureReason"`
	CorrelationID string           `json:"correlationId"`
	EntryStatus   string           `json:"protocolStatus"`
	CreateBy      string           `json:"createBy"`
	CreateByName  string           `json:"createByName"`
	CreateAt      time.Time        `json:"createAt"`
}

// List queries append-only audit events for one logical resource.
// @Summary 查询资源操作日志
// @Description 按资源类型和资源 ID 分页查询操作日志，不返回 Secret 明文或密文
// @Tags audit
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body AuditListRequest true "操作日志查询参数"
// @Success 200 {object} response.Response{data=page.Response[AuditEventDTO]}
// @Failure 401 {object} response.Response
// @Router /api/v1/audit/list [post]
func (h *AuditHandler) List(c *gin.Context) {
	var req AuditListRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err)
		return
	}
	req.Normalize()
	events, total, err := h.svc.List(c, auditapp.ListInput{
		ResourceType: req.ResourceType,
		ResourceID:   req.ResourceID,
		ActionCode:   req.ActionCode,
		ResultCode:   req.ResultCode,
		UserID:       operator(c),
		PageNum:      req.PageNum,
		PageSize:     req.PageSize,
	})
	if err != nil {
		if errors.Is(err, auditapp.ErrInvalidParam) {
			response.Error(c, err.Error())
			return
		}
		response.Error(c, "internal error")
		return
	}

	list := make([]AuditEventDTO, 0, len(events))
	for _, event := range events {
		list = append(list, toAuditEventDTO(event))
	}
	response.Success(c, page.Response[AuditEventDTO]{Total: total, List: list})
}

func toAuditEventDTO(event *auditdomain.Event) AuditEventDTO {
	changes := make([]AuditChangeDTO, 0, len(event.ChangeDetail))
	for _, change := range event.ChangeDetail {
		changes = append(changes, AuditChangeDTO{
			Field: change.Field, Before: change.Before, After: change.After,
			Changed: change.Changed, Redacted: change.Redacted,
		})
	}
	return AuditEventDTO{
		ID: event.ID, EventSource: event.EventSource, EntryType: event.EntryType,
		CallerType: event.CallerType, CallerName: event.CallerName, CallerVersion: event.CallerVersion,
		OperationName: event.OperationName, ActionCode: event.ActionCode, ResultCode: event.ResultCode,
		ActorType: event.ActorType, ResourceType: event.ResourceType, ResourceID: event.ResourceID,
		ResourceName: event.ResourceName, ScopeType: event.ScopeType, ScopeID: event.ScopeID,
		BatchID: event.BatchID, ChangeDetail: changes, EventDetail: event.EventDetail,
		FailureCode: event.FailureCode, FailureReason: event.FailureReason,
		CorrelationID: event.CorrelationID, EntryStatus: event.ProtocolStatus,
		CreateBy: event.CreateBy, CreateByName: event.CreateByName, CreateAt: event.CreateAt,
	}
}
