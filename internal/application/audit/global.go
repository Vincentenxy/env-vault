package audit

import (
	"context"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	auditdomain "env-vault/internal/domain/audit"
	"env-vault/pkg/permission"
	permissionbiz "env-vault/pkg/permission/biz"
	"env-vault/pkg/userctx"
)

// GlobalListInput contains optional exact-match filters. Time bounds are inclusive.
// CreateBy is the searched operator, never the identity used for authorization.
type GlobalListInput struct {
	ResourceType  string
	ResourceID    string
	ActionCode    string
	ResultCode    string
	CreateBy      string
	BatchID       *uuid.UUID
	CorrelationID string
	TraceID       string
	StartAt       *time.Time
	EndAt         *time.Time
	PageNum       int
	PageSize      int
}

func (s *Service) WithGlobalPermissions(factory *permission.Factory) *Service {
	s.globalPermissions = factory
	return s
}

func (s *Service) GlobalList(ctx context.Context, in GlobalListInput) ([]*auditdomain.Event, int64, error) {
	user := userctx.FromContext(ctx)
	if user == nil || strings.TrimSpace(user.UserID) == "" {
		return nil, 0, permission.Unauthenticated("SUBJECT_REQUIRED", "Authentication required")
	}
	if err := s.globalPermissions.Check(ctx, permissionbiz.AuditGlobalRead, "global"); err != nil {
		return nil, 0, err
	}
	for _, field := range []*string{&in.ResourceType, &in.ResourceID, &in.ActionCode, &in.ResultCode, &in.CreateBy, &in.CorrelationID, &in.TraceID} {
		*field = strings.TrimSpace(*field)
		if utf8.RuneCountInString(*field) > 512 {
			return nil, 0, ErrInvalidParam
		}
	}
	if in.PageNum <= 0 || in.PageSize <= 0 || in.PageNum-1 > math.MaxInt/in.PageSize {
		return nil, 0, ErrInvalidParam
	}
	if in.ResultCode != "" && in.ResultCode != auditdomain.ResultSuccess && in.ResultCode != auditdomain.ResultFailure {
		return nil, 0, ErrInvalidParam
	}
	if (in.BatchID != nil && *in.BatchID == uuid.Nil) ||
		(in.StartAt != nil && in.StartAt.IsZero()) || (in.EndAt != nil && in.EndAt.IsZero()) ||
		(in.StartAt != nil && in.EndAt != nil && in.StartAt.After(*in.EndAt)) {
		return nil, 0, ErrInvalidParam
	}
	return s.repo.List(ctx, auditdomain.ListFilter{
		ResourceType: in.ResourceType, ResourceID: in.ResourceID,
		ActionCode: in.ActionCode, ResultCode: in.ResultCode, CreateBy: in.CreateBy,
		BatchID: in.BatchID, CorrelationID: in.CorrelationID, TraceID: in.TraceID,
		StartAt: in.StartAt, EndAt: in.EndAt,
		UserID: user.UserID,
		Offset: (in.PageNum - 1) * in.PageSize, Limit: in.PageSize,
	})
}
