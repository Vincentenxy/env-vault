package grpcserver

import (
	"context"
	"net"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	envvaultv1 "env-vault/api/envvault/v1"
	auditapp "env-vault/internal/application/audit"
	secretapp "env-vault/internal/application/secret"
	auditdomain "env-vault/internal/domain/audit"
	"env-vault/pkg/logger"
)

// recordRead 不记录请求消息、metadata 或返回值，只保留资源标识和结果数量
func (h *secretHandler) recordRead(ctx context.Context, in secretapp.FolderReadInput, result *secretapp.FolderReadResult, rpcErr error) error {
	if h.recorder == nil {
		return nil
	}
	resourceID, resourceName := "", in.FolderCode
	scopeType, scopeID := "", ""
	if in.FolderID != uuid.Nil {
		resourceID = in.FolderID.String()
	}
	if in.FolderGroupID != uuid.Nil {
		scopeType, scopeID = "folder", in.FolderGroupID.String()
	}
	if in.ProjectID != uuid.Nil && scopeID == "" {
		scopeType, scopeID = "project", in.ProjectID.String()
	}
	count := 0
	if result != nil {
		resourceID, resourceName = result.FolderID.String(), result.FolderCode
		scopeType, scopeID = "folder", result.FolderGroupID.String()
		count = len(result.Secrets)
	}
	event := &auditdomain.Event{
		ActionCode: "secret.list", ResultCode: auditdomain.ResultSuccess,
		ResourceType: "secretCollection", ResourceID: resourceID, ResourceName: resourceName,
		ScopeType: scopeType, ScopeID: scopeID,
		// 当前没有应用认证，不接受客户端自报的应用或用户身份
		ActorType:   auditdomain.ActorTypeAnonymous,
		EventDetail: map[string]any{"resultCount": count, "keyFilterCount": len(in.KeyList)},
	}
	if rpcErr != nil {
		event.ResultCode = auditdomain.ResultFailure
		event.FailureCode = "grpc_" + strings.ToLower(status.Code(rpcErr).String())
		event.FailureReason = status.Convert(rpcErr).Message()
	}
	traceID, _ := ctx.Value(logger.TraceIDKey).(string)
	entry := auditapp.EntryContext{
		EventSource: auditdomain.EventSourceServer, EntryType: auditdomain.EntryTypeGRPC,
		CallerType: auditdomain.CallerTypeUnknown, ActorType: auditdomain.ActorTypeAnonymous,
		OperationName: envvaultv1.SecretService_ListSecrets_FullMethodName,
		CorrelationID: traceID, ProtocolStatus: status.Code(rpcErr).String(),
		ProtocolDetail: map[string]any{"service": "envvault.v1.SecretService", "method": "ListSecrets", "streamType": "unary"},
	}
	if remote, ok := peer.FromContext(ctx); ok && remote.Addr != nil {
		entry.ClientIP = remote.Addr.String()
		if host, _, err := net.SplitHostPort(entry.ClientIP); err == nil {
			entry.ClientIP = host
		}
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get("user-agent"); len(values) > 0 {
			entry.UserAgent = values[0]
		}
	}
	return h.recorder.Record(auditapp.WithEntryContext(ctx, entry), event)
}
