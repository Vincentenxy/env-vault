package userctx_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"env-vault/pkg/userctx"
)

func TestSetPropagatesIdentityAndPreservesRequestContext(t *testing.T) {
	type requestKey struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), requestKey{}, "original"))
	defer cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/api/v1/org/info", nil).WithContext(ctx)
	user := &userctx.User{UserID: "trusted-user", Name: "Tester"}
	userctx.Set(c, user)
	if userctx.FromContext(c) != user || userctx.FromContext(c.Request.Context()) != user {
		t.Fatal("authenticated identity must be available in both contexts")
	}
	if c.Request.Context().Value(requestKey{}) != "original" {
		t.Fatal("original request values were lost")
	}
	cancel()
	if !errors.Is(c.Request.Context().Err(), context.Canceled) {
		t.Fatal("request cancellation was lost")
	}
}
