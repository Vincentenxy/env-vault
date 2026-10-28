package response_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"env-vault/pkg/permission"
	"env-vault/pkg/response"
)

func TestAbortPermissionError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"authentication", permission.Unauthenticated("NO_USER", "private-data"), 401},
		{"denied", fmt.Errorf("wrapped: %w", permission.Denied("DENIED", "private-data")), 403},
		{"evaluation", permission.EvaluationError("FAILED", "private-data", errors.New("private-cause")), 500},
		{"zero failure", &permission.Failure{}, 500},
		{"unrelated", errors.New("business error"), 204},
		{"nil", nil, 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/test", func(c *gin.Context) {
				if response.AbortPermissionError(c, tc.err) {
					return
				}
				c.Status(204)
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/test", nil))
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d", w.Code, tc.status)
			}
			if tc.status == 204 {
				return
			}
			var body response.Response
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != tc.status || body.Data != nil || strings.Contains(w.Body.String(), "private") {
				t.Fatal("permission response must use the standard status and must not expose internal messages")
			}
		})
	}
}
