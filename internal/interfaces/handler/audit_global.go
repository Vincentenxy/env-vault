package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	auditapp "env-vault/internal/application/audit"
	"env-vault/pkg/page"
	"env-vault/pkg/response"
)

type AuditGlobalListRequest struct {
	ResourceType  string     `json:"resourceType"`
	ResourceID    string     `json:"resourceId"`
	ActionCode    string     `json:"actionCode"`
	ResultCode    string     `json:"resultCode"`
	CreateBy      string     `json:"createBy"`
	BatchID       *uuid.UUID `json:"batchId"`
	CorrelationID string     `json:"correlationId"`
	TraceID       string     `json:"traceId"`
	StartAt       *time.Time `json:"startAt"`
	EndAt         *time.Time `json:"endAt"`
	page.Request
}

// GlobalList queries immutable audit events independently of live resources.
// @Summary 全局审计日志分页查询
// @Description 可跨资源查询，包括已删除资源。时间为 RFC3339，筛选字段精确匹配。不返回 Secret 明文或密文。当前全局查看授权暂时默认通过，仍需 JWT 认证。
// @Tags audit
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body AuditGlobalListRequest true "全局日志筛选与分页"
// @Success 200 {object} response.Response{data=page.Response[AuditEventDTO]}
// @Failure 400 {object} response.Response
// @Failure 401 {object} response.Response
// @Failure 403 {object} response.Response
// @Failure 500 {object} response.Response
// @Router /api/v1/audit/global/list [post]
func (h *AuditHandler) GlobalList(c *gin.Context) {
	var req AuditGlobalListRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.AbortWithHTTPStatus(c, http.StatusBadRequest)
		return
	}
	req.Normalize()
	events, total, err := h.svc.GlobalList(c.Request.Context(), auditapp.GlobalListInput{
		ResourceType: req.ResourceType, ResourceID: req.ResourceID,
		ActionCode: req.ActionCode, ResultCode: req.ResultCode, CreateBy: req.CreateBy,
		BatchID: req.BatchID, CorrelationID: req.CorrelationID, TraceID: req.TraceID,
		StartAt: req.StartAt, EndAt: req.EndAt, PageNum: req.PageNum, PageSize: req.PageSize,
	})
	if err != nil {
		if response.AbortPermissionError(c, err) {
			return
		}
		if errors.Is(err, auditapp.ErrInvalidParam) {
			response.AbortWithHTTPStatus(c, http.StatusBadRequest)
			return
		}
		response.AbortWithHTTPStatus(c, http.StatusInternalServerError)
		return
	}
	list := make([]AuditEventDTO, 0, len(events))
	for _, event := range events {
		list = append(list, toAuditEventDTO(event))
	}
	response.Success(c, page.Response[AuditEventDTO]{Total: total, List: list})
}
