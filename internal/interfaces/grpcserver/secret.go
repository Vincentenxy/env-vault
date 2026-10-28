// Package grpcserver 提供应用读取接口的 gRPC 协议适配
package grpcserver

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	envvaultv1 "env-vault/api/envvault/v1"
	secretapp "env-vault/internal/application/secret"
	auditdomain "env-vault/internal/domain/audit"
)

// SecretReader 是协议层依赖的窄接口，不暴露密钥写入或历史查询能力
type SecretReader interface {
	ReadFolder(context.Context, secretapp.FolderReadInput) (*secretapp.FolderReadResult, error)
}

// secretHandler 将 protobuf 请求转换为应用输入，并记录最终读取结果
type secretHandler struct {
	envvaultv1.UnimplementedSecretServiceServer
	reader   SecretReader
	recorder auditdomain.Recorder
}

// ListSecrets 仅查询一个实际目录和环境，认证能力在后续 interceptor 中接入
func (h *secretHandler) ListSecrets(ctx context.Context, req *envvaultv1.ListSecretsRequest) (*envvaultv1.ListSecretsResponse, error) {
	in, err := parseListRequest(req)
	if err != nil {
		return nil, err
	}
	result, readErr := h.reader.ReadFolder(ctx, in)
	rpcErr := readError(readErr)
	if result == nil && rpcErr == nil {
		rpcErr = status.Error(codes.Internal, "internal error")
	}
	// 业务读取完成后再记录审计，确保协议状态与实际返回的 gRPC 状态一致
	if err := h.recordRead(ctx, in, result, rpcErr); err != nil {
		return nil, status.Error(codes.Internal, "record secret audit failed")
	}
	if rpcErr != nil {
		return nil, rpcErr
	}
	return toListResponse(result), nil
}

// parseListRequest 校验协议字段，参数错误不进入业务读取和业务审计
func parseListRequest(req *envvaultv1.ListSecretsRequest) (secretapp.FolderReadInput, error) {
	in := secretapp.FolderReadInput{}
	if req == nil {
		return in, status.Error(codes.InvalidArgument, "folder selector is required")
	}
	var err error
	if strings.TrimSpace(req.GetProjectId()) != "" {
		in.ProjectID, err = parseID(req.GetProjectId(), "projectId")
		if err != nil {
			return in, err
		}
	}
	in.EnvCode = strings.TrimSpace(req.GetEnvCode())
	in.KeyList = append([]string(nil), req.GetKeyList()...)
	switch folder := req.GetFolder().(type) {
	case *envvaultv1.ListSecretsRequest_FolderId:
		in.FolderID, err = parseID(folder.FolderId, "folderId")
	case *envvaultv1.ListSecretsRequest_FolderGroupId:
		in.FolderGroupID, err = parseID(folder.FolderGroupId, "folderGroupId")
		if err == nil && in.EnvCode == "" {
			err = status.Error(codes.InvalidArgument, "envCode is required with folderGroupId")
		}
	case *envvaultv1.ListSecretsRequest_FolderCode:
		in.FolderCode = strings.TrimSpace(folder.FolderCode)
		if in.FolderCode == "" || in.ProjectID == uuid.Nil || in.EnvCode == "" {
			err = status.Error(codes.InvalidArgument, "folderCode requires projectId and envCode")
		}
	default:
		err = status.Error(codes.InvalidArgument, "folderId, folderGroupId or folderCode is required")
	}
	return in, err
}

// parseID 拒绝空 UUID，避免被误当成未指定的查询条件
func parseID(value, field string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, status.Error(codes.InvalidArgument, field+" must be a non-zero UUID")
	}
	return id, nil
}

// readError 只暴露已知业务错误，数据库和解密内部异常不拼入响应
func readError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	case errors.Is(err, secretapp.ErrInvalidParam):
		return status.Error(codes.InvalidArgument, "folder does not match the specified project or environment")
	case errors.Is(err, secretapp.ErrFolderNotFound):
		return status.Error(codes.NotFound, secretapp.ErrFolderNotFound.Error())
	case errors.Is(err, secretapp.ErrEnvNotFound):
		return status.Error(codes.NotFound, secretapp.ErrEnvNotFound.Error())
	case errors.Is(err, secretapp.ErrFolderCodeAmbiguous):
		return status.Error(codes.FailedPrecondition, secretapp.ErrFolderCodeAmbiguous.Error())
	case errors.Is(err, secretapp.ErrDecrypt):
		return status.Error(codes.Internal, secretapp.ErrDecrypt.Error())
	default:
		return status.Error(codes.Internal, "internal error")
	}
}

// toListResponse 保留现有 secret/list 的字段含义，时间使用 protobuf Timestamp
func toListResponse(result *secretapp.FolderReadResult) *envvaultv1.ListSecretsResponse {
	response := &envvaultv1.ListSecretsResponse{
		Folder: &envvaultv1.FolderMetadata{
			FolderId: result.FolderID.String(), FolderGroupId: result.FolderGroupID.String(),
			FolderCode: result.FolderCode, ProjectId: result.ProjectID.String(), EnvCode: result.EnvCode,
		},
		SecretList: make([]*envvaultv1.Secret, 0, len(result.Secrets)),
	}
	for _, item := range result.Secrets {
		secret := &envvaultv1.Secret{
			GroupId: item.GroupID.String(), Key: item.Key, Remark: item.Remark,
			Values: make(map[string]*envvaultv1.SecretValue, len(item.Values)),
		}
		for envCode, value := range item.Values {
			converted := &envvaultv1.SecretValue{
				SecretId: value.SecretID.String(), FolderId: value.FolderID.String(),
				Value: value.Value, Version: int32(value.Version), ValueType: value.ValueType,
			}
			if !value.UpdateAt.IsZero() {
				converted.UpdateAt = timestamppb.New(value.UpdateAt)
			}
			secret.Values[envCode] = converted
		}
		for _, tag := range item.TagList {
			secret.TagList = append(secret.TagList, &envvaultv1.TagSummary{
				Id: tag.ID.String(), Code: tag.Code, Name: tag.Name, AllowValueSearch: tag.AllowValueSearch,
			})
		}
		response.SecretList = append(response.SecretList, secret)
	}
	return response
}
