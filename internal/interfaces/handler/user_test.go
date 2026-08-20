package handler

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	userapp "env-vault/internal/application/user"
	userdomain "env-vault/internal/domain/user"
	"env-vault/pkg/userctx"
)

type stubUserService struct {
	updateFn func(ctx context.Context, in userapp.UpdateInput) (*userdomain.User, error)
}

func (s *stubUserService) Update(ctx context.Context, in userapp.UpdateInput) (*userdomain.User, error) {
	if s.updateFn != nil {
		return s.updateFn(ctx, in)
	}
	return nil, nil
}

func (s *stubUserService) GetNickname(ctx context.Context, userID string) (string, error) {
	return "", nil
}

func (s *stubUserService) WarmUp(ctx context.Context) (int, error) {
	return 0, nil
}

func newUserTestEngine(svc userapp.IService, authUser *userctx.User) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if authUser != nil {
			userctx.Set(c, authUser)
		}
		c.Next()
	})
	h := NewUserHandler(svc)
	r.POST("/api/v1/user/update", h.Update)
	return r
}

func TestUserHandler_Update_UsesJWTUserID(t *testing.T) {
	tenantID := uuid.New()
	orgID := uuid.New()
	internalID := uuid.New()
	svc := &stubUserService{updateFn: func(ctx context.Context, in userapp.UpdateInput) (*userdomain.User, error) {
		if in.UserID != "jwt-user-id" {
			t.Fatalf("expected JWT user id, got %q", in.UserID)
		}
		if in.Nickname != "Tester" || in.Username != "tester" || in.TenantID != tenantID || in.OrgID != orgID {
			t.Fatalf("unexpected update input: %+v", in)
		}
		return &userdomain.User{
			ID: internalID, UserID: in.UserID, Nickname: in.Nickname, Username: in.Username,
			Email: in.Email, Phone: in.Phone, TenantID: in.TenantID, OrgID: in.OrgID,
		}, nil
	}}
	r := newUserTestEngine(svc, &userctx.User{UserID: "jwt-user-id"})
	w := doJSON(t, r, http.MethodPost, "/api/v1/user/update", map[string]any{
		"userId":   "forged-user-id",
		"nickname": "Tester", "username": "tester", "email": "a@example.com", "phone": "13800000000",
		"tenantId": tenantID, "orgId": orgID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("unexpected HTTP status: %d", w.Code)
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
	data := body["data"].(map[string]any)
	if data["id"] != internalID.String() || data["userId"] != "jwt-user-id" {
		t.Fatalf("unexpected response data: %+v", data)
	}
}

func TestUserHandler_Update_RequiresAuthentication(t *testing.T) {
	r := newUserTestEngine(&stubUserService{}, nil)
	w := doJSON(t, r, http.MethodPost, "/api/v1/user/update", map[string]any{})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != http.StatusUnauthorized {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
}

func TestUserHandler_Update_ValidatesRequiredFields(t *testing.T) {
	r := newUserTestEngine(&stubUserService{}, &userctx.User{UserID: "u-1"})
	w := doJSON(t, r, http.MethodPost, "/api/v1/user/update", map[string]any{
		"nickname": "", "username": "tester", "tenantId": uuid.New(), "orgId": uuid.New(),
	})
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 || body["msg"] != "invalid params" {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
}

func TestUserHandler_Update_MapsBusinessAndInternalErrors(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantMsg string
	}{
		{name: "not found", err: userapp.ErrNotFound, wantMsg: "user not found"},
		{name: "internal", err: errors.New("database failed"), wantMsg: "internal error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &stubUserService{updateFn: func(ctx context.Context, in userapp.UpdateInput) (*userdomain.User, error) {
				return nil, tt.err
			}}
			r := newUserTestEngine(svc, &userctx.User{UserID: "u-1"})
			w := doJSON(t, r, http.MethodPost, "/api/v1/user/update", map[string]any{
				"nickname": "Tester", "username": "tester", "tenantId": uuid.New(), "orgId": uuid.New(),
			})
			body := decodeBody(t, w)
			if body["code"].(float64) != -1 || body["msg"] != tt.wantMsg {
				t.Fatalf("unexpected response: %s", w.Body.String())
			}
		})
	}
}
