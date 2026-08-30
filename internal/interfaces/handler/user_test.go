package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	userapp "env-vault/internal/application/user"
	userdomain "env-vault/internal/domain/user"
	"env-vault/pkg/userctx"
)

type stubUserService struct {
	updateFn           func(ctx context.Context, in userapp.UpdateInput) (*userdomain.User, error)
	listFn             func(ctx context.Context, in userapp.ListInput) ([]*userdomain.User, error)
	manageListFn       func(ctx context.Context, in userapp.ManagementListInput) ([]*userdomain.ManagementUser, int64, error)
	allocateFn         func(ctx context.Context, in userapp.AllocateInput) (int, error)
	getProfileFn       func(ctx context.Context, userID string) (*userdomain.User, error)
	getProfileDetailFn func(ctx context.Context, userID string) (*userdomain.Profile, error)
	isBlockedFn        func(ctx context.Context, userID string) (bool, error)
}

func (s *stubUserService) Allocate(ctx context.Context, in userapp.AllocateInput) (int, error) {
	if s.allocateFn != nil {
		return s.allocateFn(ctx, in)
	}
	return 0, nil
}

func (s *stubUserService) Update(ctx context.Context, in userapp.UpdateInput) (*userdomain.User, error) {
	if s.updateFn != nil {
		return s.updateFn(ctx, in)
	}
	return nil, nil
}

func (s *stubUserService) List(ctx context.Context, in userapp.ListInput) ([]*userdomain.User, error) {
	if s.listFn != nil {
		return s.listFn(ctx, in)
	}
	return nil, nil
}

func (s *stubUserService) ListManagement(ctx context.Context, in userapp.ManagementListInput) ([]*userdomain.ManagementUser, int64, error) {
	if s.manageListFn != nil {
		return s.manageListFn(ctx, in)
	}
	return nil, 0, nil
}

func (s *stubUserService) GetProfile(ctx context.Context, userID string) (*userdomain.User, error) {
	if s.getProfileFn != nil {
		return s.getProfileFn(ctx, userID)
	}
	return nil, nil
}

func (s *stubUserService) GetProfileDetail(ctx context.Context, userID string) (*userdomain.Profile, error) {
	if s.getProfileDetailFn != nil {
		return s.getProfileDetailFn(ctx, userID)
	}
	if s.getProfileFn == nil {
		return nil, nil
	}
	user, err := s.getProfileFn(ctx, userID)
	if err != nil || user == nil {
		return nil, err
	}
	return &userdomain.Profile{User: *user}, nil
}

func (s *stubUserService) GetNickname(ctx context.Context, userID string) (string, error) {
	return "", nil
}

func (s *stubUserService) IsBlocked(ctx context.Context, userID string) (bool, error) {
	if s.isBlockedFn != nil {
		return s.isBlockedFn(ctx, userID)
	}
	return false, nil
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
	r.GET("/api/v1/auth/me", h.Me)
	r.POST("/api/v1/user/update", h.Update)
	r.POST("/api/v1/user/list", h.List)
	r.POST("/api/v1/user/manage/list", h.ManageList)
	r.POST("/api/v1/user/allocate", h.Allocate)
	return r
}

func TestUserHandler_Me_UsesJWTUserIDAndOmitsSensitiveFields(t *testing.T) {
	internalID, tenantID, orgID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Second)
	projectID := uuid.New()
	svc := &stubUserService{getProfileDetailFn: func(_ context.Context, userID string) (*userdomain.Profile, error) {
		if userID != "jwt-user-id" {
			t.Fatalf("expected JWT user id, got %q", userID)
		}
		return &userdomain.Profile{
			User: userdomain.User{
				ID: internalID, UserID: userID, Nickname: "Tester", Username: "tester",
				PasswordHash: "secret-hash", Email: "tester@example.com", Phone: "13800000000",
				TenantID: tenantID, OrgID: orgID, CreateBy: "system", UpdateBy: userID,
				CreateAt: now, UpdateAt: now,
			},
			TenantName: "平台中心",
			OrgName:    "研发部门",
			Projects:   []userdomain.ProfileProject{{ID: projectID, Name: "EnvVault"}},
		}, nil
	}}

	r := newUserTestEngine(svc, &userctx.User{UserID: "jwt-user-id", Name: "JWT Name", Jwt: "secret-token"})
	w := doJSON(t, r, http.MethodGet, "/api/v1/auth/me", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("unexpected HTTP status: %d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
	data := body["data"].(map[string]any)
	if data["id"] != internalID.String() || data["userId"] != "jwt-user-id" || data["nickname"] != "Tester" {
		t.Fatalf("unexpected user data: %+v", data)
	}
	projects := data["projectList"].([]any)
	if data["tenantName"] != "平台中心" || data["orgName"] != "研发部门" || len(projects) != 1 {
		t.Fatalf("unexpected resource relations: %+v", data)
	}
	project := projects[0].(map[string]any)
	if project["id"] != projectID.String() || project["name"] != "EnvVault" {
		t.Fatalf("unexpected project: %+v", project)
	}
	for _, sensitive := range []string{"passwordHash", "isDeleted", "deleteAt", "deleteBy", "jwt"} {
		if _, exists := data[sensitive]; exists {
			t.Fatalf("sensitive field %q leaked: %+v", sensitive, data)
		}
	}
}

func TestUserHandler_Me_RequiresAuthentication(t *testing.T) {
	svc := &stubUserService{getProfileDetailFn: func(context.Context, string) (*userdomain.Profile, error) {
		t.Fatal("service must not be called without an authenticated user")
		return nil, nil
	}}
	r := newUserTestEngine(svc, nil)
	w := doJSON(t, r, http.MethodGet, "/api/v1/auth/me", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != http.StatusUnauthorized || body["msg"] != http.StatusText(http.StatusUnauthorized) {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
}

func TestUserHandler_Me_UserNotFound(t *testing.T) {
	svc := &stubUserService{getProfileDetailFn: func(context.Context, string) (*userdomain.Profile, error) {
		return nil, userapp.ErrNotFound
	}}
	r := newUserTestEngine(svc, &userctx.User{UserID: "missing-user"})
	w := doJSON(t, r, http.MethodGet, "/api/v1/auth/me", nil)
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 || body["msg"] != "user not found" {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
}

func TestUserHandler_List_ReturnsOnlyPublicFields(t *testing.T) {
	tenantID, orgID, projectID := uuid.New(), uuid.New(), uuid.New()
	internalID := uuid.New()
	expireAt := time.Now().Add(7 * 24 * time.Hour).UTC().Truncate(time.Second)
	svc := &stubUserService{listFn: func(_ context.Context, in userapp.ListInput) ([]*userdomain.User, error) {
		if in.TenantID != tenantID || in.OrgID != orgID || in.ProjectID != projectID || !in.Undistributed {
			t.Fatalf("list input not passed: %+v", in)
		}
		return []*userdomain.User{{
			ID: internalID, UserID: "external-1", Nickname: "User One",
			Username: "login-name", PasswordHash: "password-hash", Email: "private@example.com", Phone: "13800000000",
			IsBlocked: true,
			ProjectRelation: &userdomain.ProjectRelation{
				MemberType: userdomain.ProjectMemberExternal,
				ExpireAt:   &expireAt,
			},
		}}, nil
	}}

	r := newUserTestEngine(svc, &userctx.User{UserID: "operator"})
	w := doJSON(t, r, http.MethodPost, "/api/v1/user/list", map[string]any{
		"tenantId": tenantID, "orgId": orgID, "projectId": projectID, "undistributed": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("unexpected HTTP status: %d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if body["code"].(float64) != 0 {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
	list := body["data"].([]any)
	if len(list) != 1 {
		t.Fatalf("expected one user, got %+v", list)
	}
	item := list[0].(map[string]any)
	if len(item) != 5 || item["id"] != internalID.String() || item["userId"] != "external-1" ||
		item["nickname"] != "User One" || item["isBlocked"] != true {
		t.Fatalf("unexpected public user data: %+v", item)
	}
	relation := item["projectRelation"].(map[string]any)
	if relation["memberType"] != "external" || relation["expireAt"] != expireAt.Format(time.RFC3339) {
		t.Fatalf("unexpected project relation: %+v", relation)
	}
	for _, sensitive := range []string{"username", "passwordHash", "email", "phone", "tenantId", "orgId"} {
		if _, exists := item[sensitive]; exists {
			t.Fatalf("sensitive field %q leaked: %+v", sensitive, item)
		}
	}
}

func TestUserHandler_Allocate_PassesResourceAndOperator(t *testing.T) {
	resourceID := uuid.New()
	svc := &stubUserService{allocateFn: func(_ context.Context, in userapp.AllocateInput) (int, error) {
		if in.Type != "org" || in.Operation != "add" || in.ResourceID != resourceID ||
			in.Operator != "operator" || len(in.UserIDs) != 2 || in.UserIDs[0] != "u-1" {
			t.Fatalf("unexpected allocate input: %+v", in)
		}
		return 2, nil
	}}
	r := newUserTestEngine(svc, &userctx.User{UserID: "operator"})
	w := doJSON(t, r, http.MethodPost, "/api/v1/user/allocate", map[string]any{
		"type": "org", "operate": "add", "resourceId": resourceID,
		"userIdList": []string{"u-1", "u-2"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status=%d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	data := body["data"].(map[string]any)
	if body["code"] != float64(0) || data["affectedCount"] != float64(2) {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
}

func TestUserHandler_List_InvalidBody(t *testing.T) {
	svc := &stubUserService{listFn: func(context.Context, userapp.ListInput) ([]*userdomain.User, error) {
		t.Fatal("service must not be called on bind failure")
		return nil, nil
	}}
	r := newUserTestEngine(svc, &userctx.User{UserID: "operator"})
	w := doJSON(t, r, http.MethodPost, "/api/v1/user/list", strings.NewReader("{not json"))
	body := decodeBody(t, w)
	if body["code"].(float64) != -1 {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
}

func TestUserHandler_ManageList_ReturnsPagedNonSensitiveFields(t *testing.T) {
	tenantID, orgID, internalID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Second)
	svc := &stubUserService{manageListFn: func(_ context.Context, in userapp.ManagementListInput) ([]*userdomain.ManagementUser, int64, error) {
		if in.TenantID != tenantID || in.Keyword != "tester" || in.PageNum != 2 || in.PageSize != 10 {
			t.Fatalf("unexpected management list input: %+v", in)
		}
		return []*userdomain.ManagementUser{{
			User: userdomain.User{
				ID: internalID, UserID: "external-1", Nickname: "Tester", Username: "tester",
				PasswordHash: "must-not-leak", Email: "tester@example.com", Phone: "13800000000",
				TenantID: tenantID, OrgID: orgID, IsBlocked: true, CreateAt: now, UpdateAt: now,
			},
			TenantName: "Tenant One", OrgName: "Organization One",
		}}, 21, nil
	}}
	r := newUserTestEngine(svc, &userctx.User{UserID: "operator"})
	w := doJSON(t, r, http.MethodPost, "/api/v1/user/manage/list", map[string]any{
		"tenantId": tenantID, "keyword": "tester", "pageNum": 2, "pageSize": 10,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status=%d body=%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	data := body["data"].(map[string]any)
	if data["total"] != float64(21) {
		t.Fatalf("unexpected total: %+v", data)
	}
	item := data["list"].([]any)[0].(map[string]any)
	if item["id"] != internalID.String() || item["tenantName"] != "Tenant One" ||
		item["orgName"] != "Organization One" || item["isBlocked"] != true {
		t.Fatalf("unexpected management item: %+v", item)
	}
	for _, sensitive := range []string{"passwordHash", "isDeleted", "deleteAt", "deleteBy"} {
		if _, exists := item[sensitive]; exists {
			t.Fatalf("sensitive field %q leaked: %+v", sensitive, item)
		}
	}
}

func TestUserHandler_ManageList_NormalizesPagination(t *testing.T) {
	tests := []struct {
		name     string
		body     map[string]any
		wantPage int
		wantSize int
	}{
		{name: "defaults", body: map[string]any{}, wantPage: 1, wantSize: 20},
		{name: "negative page", body: map[string]any{"pageNum": -1, "pageSize": 10}, wantPage: 1, wantSize: 10},
		{name: "zero size", body: map[string]any{"pageNum": 3, "pageSize": 0}, wantPage: 3, wantSize: 20},
		{name: "size over maximum", body: map[string]any{"pageNum": 2, "pageSize": 1000}, wantPage: 2, wantSize: 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &stubUserService{manageListFn: func(_ context.Context, in userapp.ManagementListInput) ([]*userdomain.ManagementUser, int64, error) {
				if in.PageNum != tt.wantPage || in.PageSize != tt.wantSize {
					t.Fatalf("service received pageNum=%d pageSize=%d, want %d/%d", in.PageNum, in.PageSize, tt.wantPage, tt.wantSize)
				}
				return []*userdomain.ManagementUser{}, 0, nil
			}}
			r := newUserTestEngine(svc, &userctx.User{UserID: "operator"})
			w := doJSON(t, r, http.MethodPost, "/api/v1/user/manage/list", tt.body)
			if w.Code != http.StatusOK {
				t.Fatalf("unexpected status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
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
