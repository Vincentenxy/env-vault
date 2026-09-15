package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"env-vault/pkg/page"
	"env-vault/pkg/userctx"
	"github.com/gin-gonic/gin"
)

type searchFunc func(context.Context, Input) (page.Response[Secret], error)

func (f searchFunc) Search(ctx context.Context, in Input) (page.Response[Secret], error) {
	return f(ctx, in)
}

func TestHTTPPaginationAndIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		body      string
		num, size int
	}{{`{}`, 1, 20}, {`{"pageNum":-2,"pageSize":-1}`, 1, 20}, {`{"pageNum":2,"pageSize":0}`, 2, 20}, {`{"pageNum":3,"pageSize":9999}`, 3, 200}} {
		t.Run(test.body, func(t *testing.T) {
			h := NewHTTPHandler(searchFunc(func(_ context.Context, in Input) (page.Response[Secret], error) {
				if in.PageNum != test.num || in.PageSize != test.size || in.UserID != "trusted" {
					t.Fatalf("bad input: %+v", in)
				}
				return page.Response[Secret]{List: []Secret{}}, nil
			}))
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/api/v1/secret/search", strings.NewReader(test.body))
			userctx.Set(c, &userctx.User{UserID: "trusted"})
			h.Search(c)
			var response struct {
				Code int
				Data map[string]json.RawMessage
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Code != 0 || len(response.Data) != 2 || string(response.Data["list"]) != "[]" {
				t.Fatalf("bad page response: %s", w.Body.String())
			}
		})
	}
}

func TestHTTPRejectsUnauthenticatedAndMalformedJSON(t *testing.T) {
	h := NewHTTPHandler(searchFunc(func(context.Context, Input) (page.Response[Secret], error) {
		t.Fatal("service should not be called")
		return page.Response[Secret]{}, nil
	}))
	for _, authenticated := range []bool{false, true} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"scopes":[{"scopeId":"invalid"}]}`))
		if authenticated {
			userctx.Set(c, &userctx.User{UserID: "trusted"})
		}
		h.Search(c)
		if !authenticated && w.Code != 401 {
			t.Fatal("missing 401")
		}
		if authenticated && !strings.Contains(w.Body.String(), `"code":-1`) {
			t.Fatal("missing bad parameter response")
		}
	}
}

func TestHTTPUsesRequestCancellationAndSafeErrors(t *testing.T) {
	h := NewHTTPHandler(searchFunc(func(ctx context.Context, _ Input) (page.Response[Secret], error) {
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
			t.Fatal("request cancellation not propagated")
		}
		return page.Response[Secret]{}, fmt.Errorf("sensitive-error: %w", ErrTimeout)
	}))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{}`)).WithContext(ctx)
	userctx.Set(c, &userctx.User{UserID: "trusted"})
	h.Search(c)
	if strings.Contains(w.Body.String(), "sensitive-error") || !strings.Contains(w.Body.String(), ErrTimeout.Error()) {
		t.Fatal("unsafe error")
	}
}

func TestHTTPRejectsUnsupportedFiltersAndTrailingJSON(t *testing.T) {
	h := NewHTTPHandler(searchFunc(func(context.Context, Input) (page.Response[Secret], error) {
		t.Fatal("unsupported request reached service")
		return page.Response[Secret]{}, nil
	}))
	for _, body := range []string{`{"tagIdList":["ignored"]}`, `{"scopes":[{"scopeType":"tenant","scopeId":"8b85e3fa-c15a-480f-b4a2-000000000010","extra":true}]}`, `{} {}`, `null`} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
		userctx.Set(c, &userctx.User{UserID: "trusted"})
		h.Search(c)
		if !strings.Contains(w.Body.String(), `"code":-1`) {
			t.Fatalf("accepted %s", body)
		}
	}
}
