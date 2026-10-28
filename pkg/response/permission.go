package response

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"env-vault/pkg/permission"
)

// AbortPermissionError handles permission failures through the standard HTTP
// response format. It returns false for nil or unrelated errors. Internal causes
// and strategy messages are never reflected into an HTTP response.
func AbortPermissionError(c *gin.Context, err error) bool {
	var failure *permission.Failure
	if !errors.As(err, &failure) || failure == nil {
		return false
	}
	status := http.StatusInternalServerError
	switch failure.Kind() {
	case permission.AuthenticationFailure:
		status = http.StatusUnauthorized
	case permission.DeniedFailure:
		status = http.StatusForbidden
	}
	AbortWithHTTPStatus(c, status)
	return true
}
