package grpcserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	envvaultv1 "env-vault/api/envvault/v1"
	auditapp "env-vault/internal/application/audit"
	secretapp "env-vault/internal/application/secret"
	auditdomain "env-vault/internal/domain/audit"
	tagdomain "env-vault/internal/domain/tag"
	"env-vault/internal/masterkey"
)

// readerFunc 通过真实的 gRPC 客户端验证协议，不要求测试启动数据库
type readerFunc func(context.Context, secretapp.FolderReadInput) (*secretapp.FolderReadResult, error)

func (f readerFunc) ReadFolder(ctx context.Context, in secretapp.FolderReadInput) (*secretapp.FolderReadResult, error) {
	return f(ctx, in)
}

type capturedAudit struct {
	events []*auditdomain.Event
	err    error
}

func (r *capturedAudit) CreateBatch(_ context.Context, events []*auditdomain.Event) error {
	r.events = append(r.events, events...)
	return r.err
}

func (r *capturedAudit) List(context.Context, auditdomain.ListFilter) ([]*auditdomain.Event, int64, error) {
	return nil, 0, nil
}

// testClient 使用内存监听器传输真实 HTTP/2 和 protobuf 消息
func testClient(t *testing.T, reader SecretReader, ready Readiness, recorder auditdomain.Recorder) envvaultv1.SecretServiceClient {
	t.Helper()
	server, err := New(reader, ready, recorder)
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1024 * 1024)
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.NewClient("passthrough:///envvault-test", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		_ = listener.Close()
	})
	return envvaultv1.NewSecretServiceClient(conn)
}

// TestListSecretsReadinessAndWireContract 验证同一实例激活主密钥后即可读取，且值与版本正确传输
func TestListSecretsReadinessAndWireContract(t *testing.T) {
	manager := masterkey.NewManager()
	projectID, folderID, folderGroupID, secretID, groupID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	updated := time.Date(2026, 10, 28, 8, 0, 0, 123000000, time.UTC)
	calls := 0
	reader := readerFunc(func(_ context.Context, in secretapp.FolderReadInput) (*secretapp.FolderReadResult, error) {
		calls++
		if in.FolderCode != "global" || in.ProjectID != projectID || in.EnvCode != "dev" {
			t.Errorf("wrong query passed to application: %+v", in)
		}
		return &secretapp.FolderReadResult{
			FolderID: folderID, FolderGroupID: folderGroupID, FolderCode: "global", ProjectID: projectID, EnvCode: "dev",
			Secrets: []secretapp.SecretView{{GroupID: groupID, Key: "DB_PASSWORD", Remark: "数据库",
				Values: map[string]secretapp.SecretValueView{"dev": {SecretID: secretID, FolderID: folderID,
					Value: "wire-value-must-not-enter-audit", Version: 7, ValueType: "string", UpdateAt: updated}},
				TagList: []tagdomain.Summary{{ID: uuid.New(), Code: "database", Name: "数据库"}},
			}},
		}, nil
	})
	auditRepo := &capturedAudit{}
	client := testClient(t, reader, manager, auditapp.NewService(auditRepo, nil))
	req := &envvaultv1.ListSecretsRequest{Folder: &envvaultv1.ListSecretsRequest_FolderCode{FolderCode: "global"},
		ProjectId: projectID.String(), EnvCode: "dev"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := client.ListSecrets(ctx, req); status.Code(err) != codes.Unavailable || calls != 0 {
		t.Fatalf("unready request reached application: calls=%d error=%v", calls, err)
	}
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	if err := manager.Activate(key, masterkey.SourceConfig); err != nil {
		t.Fatal(err)
	}
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("x-request-id", "grpc-wire-test",
		"authorization", "credential-must-not-enter-audit", "x-user-id", "spoofed-user"))
	var headers metadata.MD
	response, err := client.ListSecrets(ctx, req, grpc.Header(&headers))
	if err != nil {
		t.Fatal(err)
	}
	if response.GetFolder().GetFolderId() != folderID.String() || len(response.GetSecretList()) != 1 || calls != 1 {
		t.Fatalf("unexpected response: %+v", response)
	}
	secret := response.SecretList[0]
	value := secret.Values["dev"]
	if len(secret.Values) != 1 || value.GetValue() != "wire-value-must-not-enter-audit" || value.GetVersion() != 7 ||
		value.GetSecretId() != secretID.String() || !value.GetUpdateAt().AsTime().Equal(updated) || len(secret.TagList) != 1 {
		t.Fatalf("wire contract changed: %+v", secret)
	}
	requestIDs := headers.Get("x-request-id")
	if len(requestIDs) != 1 || requestIDs[0] != "grpc-wire-test" || len(auditRepo.events) != 1 {
		t.Fatalf("missing request correlation or audit: headers=%v events=%d", headers, len(auditRepo.events))
	}
	event := auditRepo.events[0]
	if event.EntryType != auditdomain.EntryTypeGRPC || event.ActorType != auditdomain.ActorTypeAnonymous ||
		event.CreateBy != "" || event.ProtocolStatus != "OK" || event.CorrelationID != "grpc-wire-test" ||
		event.ScopeID != folderGroupID.String() {
		t.Fatalf("wrong audit identity or protocol metadata: %+v", event)
	}
	encoded, _ := json.Marshal(event)
	for _, forbidden := range []string{"wire-value-must-not-enter-audit", "credential-must-not-enter-audit", "spoofed-user"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("sensitive request data entered audit: %s", forbidden)
		}
	}
}

type alwaysReady struct{}

func (alwaysReady) Ready() bool { return true }

// TestListSecretsParameterErrorsDoNotQuery 参数错误不得触发业务查询，也不得误写业务审计
func TestListSecretsParameterErrorsDoNotQuery(t *testing.T) {
	repo := &capturedAudit{}
	client := testClient(t, readerFunc(func(context.Context, secretapp.FolderReadInput) (*secretapp.FolderReadResult, error) {
		t.Error("invalid request reached application")
		return nil, nil
	}), alwaysReady{}, auditapp.NewService(repo, nil))
	for _, req := range []*envvaultv1.ListSecretsRequest{
		{},
		{Folder: &envvaultv1.ListSecretsRequest_FolderId{FolderId: "invalid"}},
		{Folder: &envvaultv1.ListSecretsRequest_FolderId{FolderId: uuid.Nil.String()}},
		{Folder: &envvaultv1.ListSecretsRequest_FolderGroupId{FolderGroupId: uuid.NewString()}},
		{Folder: &envvaultv1.ListSecretsRequest_FolderCode{FolderCode: "global"}, EnvCode: "dev"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := client.ListSecrets(ctx, req)
		cancel()
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid request returned %v", err)
		}
	}
	if len(repo.events) != 0 {
		t.Fatal("parameter validation must not write business audit")
	}
}

// TestListSecretsErrorsAndAuditFailure 验证状态映射、异常脱敏和审计失败时禁止返回密钥
func TestListSecretsErrorsAndAuditFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		auditErr error
		code     codes.Code
	}{
		{"folder missing", secretapp.ErrFolderNotFound, nil, codes.NotFound},
		{"environment missing", secretapp.ErrEnvNotFound, nil, codes.NotFound},
		{"scope conflict", secretapp.ErrInvalidParam, nil, codes.InvalidArgument},
		{"ambiguous code", secretapp.ErrFolderCodeAmbiguous, nil, codes.FailedPrecondition},
		{"database failure", errors.New("database-password-must-not-leak"), nil, codes.Internal},
		{"audit failure", nil, errors.New("audit unavailable"), codes.Internal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &capturedAudit{err: tc.auditErr}
			client := testClient(t, readerFunc(func(context.Context, secretapp.FolderReadInput) (*secretapp.FolderReadResult, error) {
				if tc.err != nil {
					return nil, tc.err
				}
				return &secretapp.FolderReadResult{FolderID: uuid.New(), FolderGroupID: uuid.New(), ProjectID: uuid.New()}, nil
			}), alwaysReady{}, auditapp.NewService(repo, nil))
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			response, err := client.ListSecrets(ctx, &envvaultv1.ListSecretsRequest{Folder: &envvaultv1.ListSecretsRequest_FolderId{FolderId: uuid.NewString()}})
			if response != nil || status.Code(err) != tc.code || strings.Contains(err.Error(), "database-password") {
				t.Fatalf("unexpected or unsafe failure: response=%v error=%v", response, err)
			}
			if len(repo.events) != 1 {
				t.Fatalf("expected one business audit, got %d", len(repo.events))
			}
			if tc.auditErr == nil && repo.events[0].ProtocolStatus != tc.code.String() {
				t.Fatalf("audit status differs from response: %+v", repo.events[0])
			}
		})
	}
}

// TestListSecretsPanicIsRedacted 防止异常携带的密钥内容通过协议返回
func TestListSecretsPanicIsRedacted(t *testing.T) {
	client := testClient(t, readerFunc(func(context.Context, secretapp.FolderReadInput) (*secretapp.FolderReadResult, error) {
		panic("panic-secret-must-not-leak")
	}), alwaysReady{}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := client.ListSecrets(ctx, &envvaultv1.ListSecretsRequest{Folder: &envvaultv1.ListSecretsRequest_FolderId{FolderId: uuid.NewString()}})
	if status.Code(err) != codes.Internal || strings.Contains(err.Error(), "panic-secret") {
		t.Fatalf("panic was not safely handled: %v", err)
	}
}
