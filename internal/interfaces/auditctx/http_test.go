package auditctx

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	auditapp "env-vault/internal/application/audit"
	auditdomain "env-vault/internal/domain/audit"
	"env-vault/pkg/logger"
	"env-vault/pkg/userctx"
)

type captureRepository struct {
	event *auditdomain.Event
}

func (r *captureRepository) CreateBatch(_ context.Context, events []*auditdomain.Event) error {
	r.event = events[0]
	return nil
}

func (*captureRepository) List(context.Context, auditdomain.ListFilter) ([]*auditdomain.Event, int64, error) {
	return nil, 0, nil
}

func TestHTTPPreservesRequestContextAndAuditMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	type requestKey struct{}
	requestCtx, cancel := context.WithTimeout(context.WithValue(context.Background(), requestKey{}, "request-value"), time.Minute)
	defer cancel()
	r := gin.New()
	var auditCtx context.Context
	repo := &captureRepository{}
	r.POST("/api/v1/crypto/encrypt", func(c *gin.Context) {
		c.Set(string(logger.TraceIDKey), "trace-test")
		userctx.Set(c, &userctx.User{UserID: "user-test", Name: "Tester"})
		auditCtx = HTTP(c)
		err := auditapp.NewService(repo, nil).Record(auditCtx, &auditdomain.Event{
			ResourceType: "user", ResourceID: "user-test", ScopeType: "user", ScopeID: "user-test",
			ActionCode: "user.crypto.encrypt", ResultCode: auditdomain.ResultSuccess,
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	req := httptest.NewRequest("POST", "/api/v1/crypto/encrypt", nil).WithContext(requestCtx)
	r.ServeHTTP(httptest.NewRecorder(), req)
	if auditCtx.Value(requestKey{}) != "request-value" || auditCtx.Value(logger.TraceIDKey) != "trace-test" {
		t.Error("request values or trace ID were lost")
	}
	wantDeadline, _ := requestCtx.Deadline()
	if got, ok := auditCtx.Deadline(); !ok || got != wantDeadline {
		t.Error("request deadline was lost")
	}
	event := repo.event
	if event == nil || event.CreateBy != "user-test" || event.CreateByName != "Tester" ||
		event.CorrelationID != "trace-test" || event.OperationName != "POST /api/v1/crypto/encrypt" ||
		event.EntryType != auditdomain.EntryTypeHTTP {
		t.Fatal("trusted HTTP audit metadata was lost")
	}
	cancel()
	if !errors.Is(auditCtx.Err(), context.Canceled) {
		t.Error("request cancellation was lost")
	}
}
