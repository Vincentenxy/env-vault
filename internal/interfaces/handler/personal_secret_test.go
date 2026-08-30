package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	personalapp "env-vault/internal/application/personalsecret"
	"env-vault/pkg/page"
	"env-vault/pkg/userctx"
)

type pagingPersonalSecretService struct {
	received        personalapp.ListInput
	receivedManage  personalapp.ManageListInput
	receivedHistory personalapp.HistoryInput
}

func (*pagingPersonalSecretService) Create(context.Context, personalapp.CreateInput) (*personalapp.SecretView, error) {
	return nil, nil
}
func (*pagingPersonalSecretService) Update(context.Context, personalapp.UpdateInput) (*personalapp.SecretView, error) {
	return nil, nil
}
func (*pagingPersonalSecretService) Delete(context.Context, personalapp.DeleteInput) error {
	return nil
}
func (s *pagingPersonalSecretService) List(_ context.Context, in personalapp.ListInput) ([]personalapp.SecretView, int64, error) {
	s.received = in
	return []personalapp.SecretView{}, 0, nil
}
func (s *pagingPersonalSecretService) ManageList(_ context.Context, in personalapp.ManageListInput) ([]personalapp.SecretView, int64, error) {
	s.receivedManage = in
	return []personalapp.SecretView{}, 0, nil
}
func (*pagingPersonalSecretService) Reveal(context.Context, personalapp.RevealInput) (*personalapp.RevealView, error) {
	return &personalapp.RevealView{Value: "plain-value", Version: 1}, nil
}
func (s *pagingPersonalSecretService) History(_ context.Context, in personalapp.HistoryInput) ([]personalapp.HistoryView, int64, error) {
	s.receivedHistory = in
	return []personalapp.HistoryView{{Version: 1}}, 1, nil
}
func (*pagingPersonalSecretService) RevealHistory(context.Context, personalapp.RevealHistoryInput) (*personalapp.HistoryRevealView, error) {
	return nil, nil
}

func TestPersonalSecretHistoryNormalizesPaginationInHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &pagingPersonalSecretService{}
	handler := NewPersonalSecretHandler(svc)
	router := gin.New()
	router.POST("/api/v1/user/secret/history", func(c *gin.Context) {
		userctx.Set(c, &userctx.User{UserID: "user-1"})
		handler.History(c)
	})
	secretID := "11111111-1111-1111-1111-111111111111"
	body := []byte(`{"personalSecretId":"` + secretID + `","pageNum":-1,"pageSize":999}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/user/secret/history", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if svc.receivedHistory.PageNum != page.DefaultPageNum || svc.receivedHistory.PageSize != page.MaxPageSize {
		t.Fatalf("service received pageNum=%d pageSize=%d", svc.receivedHistory.PageNum, svc.receivedHistory.PageSize)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store, private" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if bytes.Contains(response.Body.Bytes(), []byte(`"value"`)) {
		t.Fatalf("history response must not contain plaintext value: %s", response.Body.String())
	}
}

func TestPersonalSecretRevealDisablesCaching(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewPersonalSecretHandler(&pagingPersonalSecretService{})
	router := gin.New()
	router.POST("/api/v1/user/secret/reveal", func(c *gin.Context) {
		userctx.Set(c, &userctx.User{UserID: "user-1"})
		handler.Reveal(c)
	})
	body := []byte(`{"id":"11111111-1111-1111-1111-111111111111"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/user/secret/reveal", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store, private" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := response.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma = %q", got)
	}
}

func TestPersonalSecretListNormalizesPaginationInHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name     string
		body     map[string]any
		wantPage int
		wantSize int
	}{
		{name: "defaults", body: map[string]any{}, wantPage: page.DefaultPageNum, wantSize: page.DefaultPageSize},
		{name: "negative page", body: map[string]any{"pageNum": -3, "pageSize": 12}, wantPage: page.DefaultPageNum, wantSize: 12},
		{name: "zero size", body: map[string]any{"pageNum": 3, "pageSize": 0}, wantPage: 3, wantSize: page.DefaultPageSize},
		{name: "maximum size", body: map[string]any{"pageNum": 2, "pageSize": page.MaxPageSize + 1}, wantPage: 2, wantSize: page.MaxPageSize},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &pagingPersonalSecretService{}
			handler := NewPersonalSecretHandler(svc)
			router := gin.New()
			router.POST("/api/v1/user/secret/list", func(c *gin.Context) {
				userctx.Set(c, &userctx.User{UserID: "user-1"})
				handler.List(c)
			})
			body, err := json.Marshal(tt.body)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/user/secret/list", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store, private" {
				t.Fatalf("Cache-Control = %q", got)
			}
			if svc.received.PageNum != tt.wantPage || svc.received.PageSize != tt.wantSize {
				t.Fatalf("service received pageNum=%d pageSize=%d, want %d/%d",
					svc.received.PageNum, svc.received.PageSize, tt.wantPage, tt.wantSize)
			}
		})
	}
}

func TestPersonalSecretManageListNormalizesPaginationInHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name     string
		body     map[string]any
		wantPage int
		wantSize int
	}{
		{name: "defaults", body: map[string]any{"userId": "locked-user"}, wantPage: page.DefaultPageNum, wantSize: page.DefaultPageSize},
		{name: "negative page", body: map[string]any{"userId": "locked-user", "pageNum": -3, "pageSize": 12}, wantPage: page.DefaultPageNum, wantSize: 12},
		{name: "zero size", body: map[string]any{"userId": "locked-user", "pageNum": 3, "pageSize": 0}, wantPage: 3, wantSize: page.DefaultPageSize},
		{name: "maximum size", body: map[string]any{"userId": "locked-user", "pageNum": 2, "pageSize": page.MaxPageSize + 1}, wantPage: 2, wantSize: page.MaxPageSize},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &pagingPersonalSecretService{}
			handler := NewPersonalSecretHandler(svc)
			router := gin.New()
			router.POST("/api/v1/user/secret/manage/list", func(c *gin.Context) {
				userctx.Set(c, &userctx.User{UserID: "admin-1"})
				handler.ManageList(c)
			})
			body, err := json.Marshal(tt.body)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/user/secret/manage/list", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store, private" {
				t.Fatalf("Cache-Control = %q", got)
			}
			if svc.receivedManage.TargetUserID != "locked-user" || svc.receivedManage.Operator != "admin-1" {
				t.Fatalf("service received target/operator = %q/%q", svc.receivedManage.TargetUserID, svc.receivedManage.Operator)
			}
			if svc.receivedManage.PageNum != tt.wantPage || svc.receivedManage.PageSize != tt.wantSize {
				t.Fatalf("service received pageNum=%d pageSize=%d, want %d/%d",
					svc.receivedManage.PageNum, svc.receivedManage.PageSize, tt.wantPage, tt.wantSize)
			}
		})
	}
}
