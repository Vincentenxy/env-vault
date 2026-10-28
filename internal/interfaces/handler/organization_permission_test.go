package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	orgapp "env-vault/internal/application/organization"
	orgdomain "env-vault/internal/domain/organization"
	"env-vault/internal/interfaces/middleware"
	"env-vault/pkg/logger"
	permissionbiz "env-vault/pkg/permission/biz"
	"env-vault/pkg/userctx"
)

type organizationPermissionRepo struct {
	orgdomain.Repository
	org *orgdomain.Organization
}

func (r organizationPermissionRepo) GetByID(context.Context, uuid.UUID) (*orgdomain.Organization, error) {
	return r.org, nil
}

func TestOrganizationInfoTenantAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name           string
		status         int
		allowed        bool
		backendErr     error
		missingUser    bool
		missingTenant  bool
		unimplemented  bool
		missingFactory bool
	}{
		{name: "allowed", status: http.StatusOK, allowed: true},
		{name: "denied", status: http.StatusForbidden},
		{name: "backend failure", status: http.StatusInternalServerError, backendErr: errors.New("private backend data")},
		{name: "unimplemented", status: http.StatusInternalServerError, unimplemented: true},
		{name: "unauthenticated", status: http.StatusUnauthorized, missingUser: true},
		{name: "tenant missing", status: http.StatusInternalServerError, missingTenant: true},
		{name: "factory missing", status: http.StatusInternalServerError, missingFactory: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			org := &orgdomain.Organization{ID: uuid.New(), TenantID: uuid.New(), Name: "private organization"}
			if tc.missingTenant {
				org.TenantID = uuid.Nil
			}
			calls := 0
			check := permissionbiz.CheckPermissionFunc(func(ctx context.Context, request permissionbiz.CheckRequest) (bool, error) {
				calls++
				if request.Subject.ID != "u-1" || request.Subject.Type != "user" ||
					request.Resource.Type != "envvault.tenant" || request.Resource.ID != org.TenantID.String() || request.Action != "access" {
					t.Fatalf("permission check must use authenticated user and stored tenant: %+v", request)
				}
				if ctx.Value(logger.TraceIDKey) != "org-permission-trace" {
					t.Fatal("trace ID was lost")
				}
				return tc.allowed, tc.backendErr
			})
			if tc.unimplemented {
				check = permissionbiz.CheckBackendPermission
			}
			permissions, err := permissionbiz.NewTenantAccessFactory(check)
			if err != nil {
				t.Fatal(err)
			}
			svc := orgapp.NewService(organizationPermissionRepo{org: org})
			if !tc.missingFactory {
				svc.WithPermissions(permissions)
			}
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(middleware.RequestID(), func(c *gin.Context) {
				if !tc.missingUser {
					userctx.Set(c, testUser())
				}
			})
			r.POST("/api/v1/org/info", NewOrganizationHandler(svc).Detail)
			payload, err := json.Marshal(map[string]any{"id": org.ID, "tenantId": uuid.New(), "userId": "forged-user"})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/org/info", bytes.NewReader(payload))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("x-request-id", "org-permission-trace")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, request)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			body := decodeBody(t, w)
			if tc.status == http.StatusOK {
				if body["code"] != float64(0) || body["data"].(map[string]any)["id"] != org.ID.String() {
					t.Fatalf("unexpected detail: %v", body)
				}
			} else if body["code"] != float64(tc.status) || body["msg"] != http.StatusText(tc.status) || body["data"] != nil {
				t.Fatalf("failed authorization must return a standard error without details: %v", body)
			}
			wantCalls := 1
			if tc.missingUser || tc.missingTenant || tc.unimplemented || tc.missingFactory {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatalf("backend check called %d times, want %d", calls, wantCalls)
			}
		})
	}
}
