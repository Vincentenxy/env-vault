package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	auditapp "env-vault/internal/application/audit"
	userapp "env-vault/internal/application/user"
	tokenapp "env-vault/internal/application/useraccesstoken"
	auditdomain "env-vault/internal/domain/audit"
	userdomain "env-vault/internal/domain/user"
	infraauth "env-vault/internal/infrastructure/auth"
	"env-vault/pkg/logger"
	"env-vault/pkg/response"
	"env-vault/pkg/userctx"
)

type authContextWaiter struct{ started chan context.Context }

func (w *authContextWaiter) wait(ctx context.Context) error {
	w.started <- ctx
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Minute):
		return errors.New("authentication dependency did not observe request cancellation")
	}
}

type authContextUserRepository struct {
	userdomain.Repository
	waiter *authContextWaiter
}

func (r authContextUserRepository) GetByUserID(ctx context.Context, userID string) (*userdomain.User, error) {
	if r.waiter != nil {
		return nil, r.waiter.wait(ctx)
	}
	return &userdomain.User{ID: uuid.New(), UserID: userID}, nil
}

type authContextAuditRepository struct {
	auditdomain.Repository
	waiter *authContextWaiter
}

func (r authContextAuditRepository) CreateBatch(ctx context.Context, _ []*auditdomain.Event) error {
	return r.waiter.wait(ctx)
}

func TestAuthRequestContextCancellation(t *testing.T) {
	key, public := testRSAKeyPair(t)
	issuer, err := infraauth.NewJWTIssuer(key, "context-test", "context-client", "context-key", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"blockCheck", "personalTokenOwnerAudit", "authenticationFailureAudit"} {
		for _, mode := range []string{"cancel", "deadline"} {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					waiter := &authContextWaiter{started: make(chan context.Context)}
					userRepo := authContextUserRepository{}
					if stage == "blockCheck" {
						userRepo.waiter = waiter
					}
					audit := auditapp.NewService(authContextAuditRepository{waiter: waiter}, nil)
					users := userapp.NewService(userRepo, nil, nil).WithAuditRecorder(audit)
					personalTokens := tokenapp.NewService(nil, users, nil, nil)
					var failureAudit auditdomain.Recorder
					if stage == "authenticationFailureAudit" {
						failureAudit = audit
					}
					auth, err := AuthWithAudit([]JWTProvider{{
						Issuer: "context-test", Audience: "context-client", KeyID: "context-key",
						PublicKey: public, PersonalTokensOnly: true,
					}}, users, failureAudit, personalTokens)
					if err != nil {
						t.Fatal(err)
					}
					pat, _, err := issuer.IssuePersonalAccessToken("context-user", "Tester", time.Now().Add(time.Hour))
					if err != nil {
						t.Fatal(err)
					}
					if stage == "authenticationFailureAudit" {
						pat = "invalid-token"
					}
					gin.SetMode(gin.TestMode)
					r := gin.New()
					var requestContext *gin.Context
					r.Use(RequestID(), func(c *gin.Context) { requestContext = c })
					handlerCalled := false
					r.POST("/api/v1/crypto/encrypt", auth, func(c *gin.Context) {
						handlerCalled = true
						c.Status(http.StatusNoContent)
					})
					type requestKey struct{}
					ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), requestKey{}, "request-value"), time.Second)
					defer cancel()
					req := httptest.NewRequest(http.MethodPost, "/api/v1/crypto/encrypt", nil).WithContext(ctx)
					req.Header.Set("Authorization", "Bearer "+pat)
					req.Header.Set("x-request-id", "auth-context-trace")
					w := httptest.NewRecorder()
					done := make(chan struct{})
					go func() {
						r.ServeHTTP(w, req)
						close(done)
					}()
					dependencyCtx := <-waiter.started
					prematureUser := userctx.FromContext(requestContext)
					wantErr := context.DeadlineExceeded
					if mode == "cancel" {
						wantErr = context.Canceled
						cancel()
					}
					<-done
					if !errors.Is(dependencyCtx.Err(), wantErr) {
						t.Errorf("dependency context error = %v, want %v", dependencyCtx.Err(), wantErr)
					}
					wantDeadline, _ := ctx.Deadline()
					if got, ok := dependencyCtx.Deadline(); !ok || got != wantDeadline {
						t.Error("request deadline was lost")
					}
					if dependencyCtx.Value(requestKey{}) != "request-value" || dependencyCtx.Value(logger.TraceIDKey) != "auth-context-trace" {
						t.Error("request values or trace ID were lost")
					}
					if prematureUser != nil || userctx.FromContext(requestContext) != nil || handlerCalled {
						t.Error("failed authentication must not publish an identity or invoke the handler")
					}
					var result response.Response
					if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					if w.Code != http.StatusInternalServerError || result.Code != http.StatusInternalServerError || result.Data != nil {
						t.Error("canceled authentication must fail closed without returning data")
					}
				})
			})
		}
	}
}
