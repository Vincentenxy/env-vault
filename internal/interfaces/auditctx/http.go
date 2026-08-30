package auditctx

import (
	"context"
	"strconv"

	"github.com/gin-gonic/gin"

	auditapp "env-vault/internal/application/audit"
	auditdomain "env-vault/internal/domain/audit"
	"env-vault/pkg/logger"
	"env-vault/pkg/userctx"
)

// HTTP attaches trusted transport and actor metadata to an application context.
func HTTP(c *gin.Context) context.Context {
	actorType := auditdomain.ActorTypeAnonymous
	actorID := ""
	actorName := ""
	if user, ok := userctx.MustFromContext(c); ok {
		actorType = auditdomain.ActorTypeUser
		actorID = user.UserID
		actorName = user.Name
	}
	correlationID := ""
	if value, exists := c.Get(string(logger.TraceIDKey)); exists {
		correlationID, _ = value.(string)
	}
	route := c.FullPath()
	if route == "" {
		route = c.Request.URL.Path
	}
	baseContext := context.WithValue(c, logger.TraceIDKey, correlationID)
	return auditapp.WithEntryContext(baseContext, auditapp.EntryContext{
		EventSource:    auditdomain.EventSourceServer,
		EntryType:      auditdomain.EntryTypeHTTP,
		CallerType:     auditdomain.CallerTypeWeb,
		OperationName:  c.Request.Method + " " + route,
		CorrelationID:  correlationID,
		ProtocolStatus: strconv.Itoa(c.Writer.Status()),
		ProtocolDetail: map[string]any{"method": c.Request.Method},
		ClientIP:       c.ClientIP(),
		UserAgent:      c.Request.UserAgent(),
		ActorType:      actorType,
		ActorID:        actorID,
		ActorName:      actorName,
	})
}
