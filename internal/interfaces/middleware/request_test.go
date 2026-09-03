package middleware

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestShouldLogAccess(t *testing.T) {
	tests := []struct {
		name string
		mode string
		path string
		want bool
	}{
		{name: "debug health", mode: gin.DebugMode, path: "/api/v1/pub/health", want: true},
		{name: "debug readiness", mode: gin.DebugMode, path: "/internal/v1/masterKey/ready", want: true},
		{name: "release health", mode: gin.ReleaseMode, path: "/api/v1/pub/health", want: false},
		{name: "release readiness", mode: gin.ReleaseMode, path: "/internal/v1/masterKey/ready", want: false},
		{name: "release business API", mode: gin.ReleaseMode, path: "/api/v1/secret/list", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldLogAccess(tt.mode, tt.path); got != tt.want {
				t.Fatalf("shouldLogAccess(%q, %q) = %t, want %t", tt.mode, tt.path, got, tt.want)
			}
		})
	}
}
