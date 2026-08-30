package handler

import (
	"context"

	"github.com/gin-gonic/gin"

	"env-vault/internal/interfaces/auditctx"
)

func withHTTPAuditContext(c *gin.Context) context.Context {
	return auditctx.HTTP(c)
}
