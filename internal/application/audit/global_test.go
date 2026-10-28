package audit

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	auditdomain "env-vault/internal/domain/audit"
	"env-vault/pkg/permission"
	permissionbiz "env-vault/pkg/permission/biz"
	"env-vault/pkg/userctx"
)

func auditCaller() context.Context {
	return userctx.WithUser(context.Background(), &userctx.User{UserID: "caller-1"})
}

func globalService(t *testing.T, repo auditdomain.Repository, check permissionbiz.CheckPermissionFunc) *Service {
	t.Helper()
	factory, err := permissionbiz.NewAuditAccessFactory(check)
	if err != nil {
		t.Fatal(err)
	}
	return NewService(repo, nil).WithGlobalPermissions(factory)
}

func TestGlobalList_TrustedCallerAndOptionalFilters(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	batchID := uuid.New()
	var captured auditdomain.ListFilter
	checked := false
	svc := globalService(t, &stubRepository{list: func(_ context.Context, filter auditdomain.ListFilter) ([]*auditdomain.Event, int64, error) {
		captured = filter
		return []*auditdomain.Event{{ResourceID: "deleted-id"}}, 1, nil
	}}, func(_ context.Context, req permissionbiz.CheckRequest) (bool, error) {
		checked = true
		if req.Subject.ID != "caller-1" || req.Resource.Type != "envvault.audit" || req.Resource.ID != "global" || req.Action != "global.read" {
			t.Fatalf("unexpected permission request: %+v", req)
		}
		return true, nil
	})
	items, total, err := svc.GlobalList(auditCaller(), GlobalListInput{
		ResourceID: " deleted-id ", ActionCode: " folder.delete ", CreateBy: " other-operator ",
		ResultCode: "success", BatchID: &batchID, CorrelationID: " req-1 ", TraceID: " trace-1 ",
		StartAt: &start, EndAt: &end, PageNum: 3, PageSize: 20,
	})
	if err != nil || total != 1 || len(items) != 1 || !checked {
		t.Fatalf("unexpected result: %v, %d, %v", items, total, err)
	}
	if captured.ResourceType != "" || captured.ResourceID != "deleted-id" || captured.ActionCode != "folder.delete" ||
		captured.UserID != "caller-1" || captured.CreateBy != "other-operator" || captured.Offset != 40 || captured.Limit != 20 ||
		captured.CorrelationID != "req-1" || captured.TraceID != "trace-1" || captured.BatchID != &batchID || captured.StartAt != &start || captured.EndAt != &end {
		t.Fatalf("unexpected filters: %+v", captured)
	}
}

func TestGlobalList_PermissionFailuresNeverQuery(t *testing.T) {
	for _, tt := range []struct {
		name  string
		ctx   context.Context
		check permissionbiz.CheckPermissionFunc
		kind  permission.FailureKind
	}{
		{"unauthenticated", context.Background(), permissionbiz.CheckAuditGlobalPermission, permission.AuthenticationFailure},
		{"empty subject", userctx.WithUser(context.Background(), &userctx.User{}), permissionbiz.CheckAuditGlobalPermission, permission.AuthenticationFailure},
		{"denied", auditCaller(), func(context.Context, permissionbiz.CheckRequest) (bool, error) { return false, nil }, permission.DeniedFailure},
		{"unavailable", auditCaller(), func(context.Context, permissionbiz.CheckRequest) (bool, error) { return false, errors.New("offline") }, permission.EvaluationFailure},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := globalService(t, &stubRepository{list: func(context.Context, auditdomain.ListFilter) ([]*auditdomain.Event, int64, error) {
				t.Fatal("permission failure reached repository")
				return nil, 0, nil
			}}, tt.check)
			_, _, err := svc.GlobalList(tt.ctx, GlobalListInput{PageNum: 1, PageSize: 20})
			var failure *permission.Failure
			if !errors.As(err, &failure) || failure.Kind() != tt.kind {
				t.Fatalf("unexpected failure: %v", err)
			}
		})
	}
}

func TestGlobalList_DefaultAllowStillRequiresRegisteredChain(t *testing.T) {
	svc := globalService(t, &stubRepository{}, permissionbiz.CheckAuditGlobalPermission)
	if _, _, err := svc.GlobalList(auditCaller(), GlobalListInput{PageNum: 1, PageSize: 20}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewService(&stubRepository{}, nil).GlobalList(auditCaller(), GlobalListInput{PageNum: 1, PageSize: 20}); err == nil {
		t.Fatal("missing permission chain allowed global read")
	}
}

func TestGlobalList_RejectsInvalidFilters(t *testing.T) {
	now, earlier := time.Now(), time.Now().Add(-time.Hour)
	zero := uuid.Nil
	for _, in := range []GlobalListInput{
		{PageNum: 0, PageSize: 20}, {PageNum: 1, PageSize: 0}, {PageNum: math.MaxInt, PageSize: 20},
		{PageNum: 1, PageSize: 20, ResultCode: "unknown"},
		{PageNum: 1, PageSize: 20, StartAt: &now, EndAt: &earlier},
		{PageNum: 1, PageSize: 20, BatchID: &zero},
		{PageNum: 1, PageSize: 20, ResourceID: strings.Repeat("x", 513)},
	} {
		svc := globalService(t, &stubRepository{list: func(context.Context, auditdomain.ListFilter) ([]*auditdomain.Event, int64, error) {
			t.Fatal("invalid filter reached repository")
			return nil, 0, nil
		}}, permissionbiz.CheckAuditGlobalPermission)
		if _, _, err := svc.GlobalList(auditCaller(), in); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("expected invalid params, got %v", err)
		}
	}
}
