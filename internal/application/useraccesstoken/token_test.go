package useraccesstoken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	auditdomain "env-vault/internal/domain/audit"
	userdomain "env-vault/internal/domain/user"
	tokendomain "env-vault/internal/domain/useraccesstoken"
)

type memoryTokenRepository struct {
	mu    sync.Mutex
	items map[uuid.UUID]*tokendomain.Token
}

func newMemoryTokenRepository() *memoryTokenRepository {
	return &memoryTokenRepository{items: make(map[uuid.UUID]*tokendomain.Token)}
}

func (r *memoryTokenRepository) WithTx(ctx context.Context, fn func(context.Context) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fn(ctx)
}

func (r *memoryTokenRepository) LockOwner(context.Context, uuid.UUID) (bool, error) {
	return true, nil
}

func (r *memoryTokenRepository) CountActiveByOwner(_ context.Context, ownerID uuid.UUID, now time.Time) (int64, error) {
	var count int64
	for _, item := range r.items {
		if item.OwnerID == ownerID && !item.IsDeleted && item.ExpiresAt.After(now) {
			count++
		}
	}
	return count, nil
}

func (r *memoryTokenRepository) Create(_ context.Context, item *tokendomain.Token) error {
	copyItem := *item
	r.items[item.ID] = &copyItem
	return nil
}

func (r *memoryTokenRepository) GetByIDAndOwner(_ context.Context, id, ownerID uuid.UUID) (*tokendomain.Token, error) {
	item := r.items[id]
	if item == nil || item.OwnerID != ownerID || item.IsDeleted {
		return nil, nil
	}
	copyItem := *item
	return &copyItem, nil
}

func (r *memoryTokenRepository) GetUsableByJTI(_ context.Context, jti uuid.UUID, now time.Time) (*tokendomain.Token, error) {
	for _, item := range r.items {
		if item.JTI == jti && !item.IsDeleted && item.ExpiresAt.After(now) {
			copyItem := *item
			return &copyItem, nil
		}
	}
	return nil, nil
}

func (r *memoryTokenRepository) ListByOwner(_ context.Context, ownerID uuid.UUID) ([]*tokendomain.Token, error) {
	items := make([]*tokendomain.Token, 0, len(r.items))
	for _, item := range r.items {
		if item.OwnerID == ownerID && !item.IsDeleted {
			copyItem := *item
			items = append(items, &copyItem)
		}
	}
	return items, nil
}

func (r *memoryTokenRepository) TouchLastUsed(_ context.Context, id uuid.UUID, now, _ time.Time) error {
	item := r.items[id]
	if item != nil {
		item.LastUsedAt = &now
	}
	return nil
}

func (r *memoryTokenRepository) SoftDelete(_ context.Context, id, ownerID uuid.UUID, operator string, now time.Time) (int64, error) {
	item := r.items[id]
	if item == nil || item.OwnerID != ownerID || item.IsDeleted {
		return 0, nil
	}
	item.IsDeleted = true
	item.DeleteAt = &now
	item.DeleteBy = operator
	return 1, nil
}

type tokenTestUsers struct{ user *userdomain.User }

func (u tokenTestUsers) GetProfile(context.Context, string) (*userdomain.User, error) {
	return u.user, nil
}

type tokenTestIssuer struct{ calls atomic.Int64 }

func (i *tokenTestIssuer) IssuePersonalAccessToken(userID, _ string, _ time.Time) (string, uuid.UUID, error) {
	i.calls.Add(1)
	jti := uuid.New()
	return "jwt-" + userID + "-" + jti.String(), jti, nil
}

type tokenTestCipher struct{}

func (tokenTestCipher) Encrypt(value string) (string, error) { return "encrypted:" + value, nil }
func (tokenTestCipher) Decrypt(value string) (string, error) {
	if !strings.HasPrefix(value, "encrypted:") {
		return "", errors.New("invalid ciphertext")
	}
	return strings.TrimPrefix(value, "encrypted:"), nil
}

type tokenTestAuditRecorder struct {
	mu     sync.Mutex
	events []*auditdomain.Event
}

func (r *tokenTestAuditRecorder) Record(_ context.Context, event *auditdomain.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	return nil
}

func (r *tokenTestAuditRecorder) RecordBatch(ctx context.Context, events []*auditdomain.Event) error {
	for _, event := range events {
		if err := r.Record(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func newTokenTestService(now time.Time) (*Service, *memoryTokenRepository, *tokenTestIssuer) {
	repo := newMemoryTokenRepository()
	issuer := &tokenTestIssuer{}
	svc := NewService(repo, tokenTestUsers{user: &userdomain.User{
		ID: uuid.New(), UserID: "user-1", Nickname: "Tester",
	}}, issuer, tokenTestCipher{})
	svc.now = func() time.Time { return now }
	return svc, repo, issuer
}

func TestCreateSerializesActiveTokenLimit(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	svc, _, issuer := newTokenTestService(now)

	start := make(chan struct{})
	results := make(chan error, MaxActiveTokens+1)
	var wg sync.WaitGroup
	for i := 0; i < MaxActiveTokens+1; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			_, err := svc.Create(context.Background(), CreateInput{
				Name: fmt.Sprintf("token-%d", index), ExpiresAt: now.Add(24 * time.Hour), UserID: "user-1",
			})
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)

	successes, limitFailures := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrLimitReached):
			limitFailures++
		default:
			t.Fatalf("unexpected create error: %v", err)
		}
	}
	if successes != MaxActiveTokens || limitFailures != 1 || issuer.calls.Load() != MaxActiveTokens {
		t.Fatalf("successes=%d limitFailures=%d issuerCalls=%d", successes, limitFailures, issuer.calls.Load())
	}
}

func TestPersonalTokenCannotCreateAnotherToken(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	svc, _, issuer := newTokenTestService(now)

	_, err := svc.Create(context.Background(), CreateInput{
		Name: "forbidden", ExpiresAt: now.Add(time.Hour), UserID: "user-1", TokenUse: PersonalTokenUse,
	})
	if !errors.Is(err, ErrPATCannotCreate) || issuer.calls.Load() != 0 {
		t.Fatalf("err=%v issuerCalls=%d", err, issuer.calls.Load())
	}
}

func TestDeleteRevokesTokenAndListReturnsPlaintext(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	svc, _, _ := newTokenTestService(now)
	created, err := svc.Create(context.Background(), CreateInput{
		Name: "system-a", ExpiresAt: now.Add(24 * time.Hour), UserID: "user-1",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	items, err := svc.List(context.Background(), ListInput{UserID: "user-1"})
	if err != nil || len(items) != 1 || items[0].Token != created.Token {
		t.Fatalf("List() items=%+v err=%v", items, err)
	}

	parts := strings.Split(created.Token, "-")
	jti := strings.Join(parts[3:], "-")
	active, err := svc.Validate(context.Background(), jti, "user-1")
	if err != nil || !active {
		t.Fatalf("Validate() active=%v err=%v", active, err)
	}
	if err := svc.Delete(context.Background(), DeleteInput{ID: created.ID, UserID: "user-1"}); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	active, err = svc.Validate(context.Background(), jti, "user-1")
	if err != nil || active {
		t.Fatalf("Validate() after delete active=%v err=%v", active, err)
	}
}

func TestAuditEventsNeverContainTokenValue(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	svc, _, _ := newTokenTestService(now)
	recorder := &tokenTestAuditRecorder{}
	svc.WithAuditRecorder(recorder)

	created, err := svc.Create(context.Background(), CreateInput{
		Name: "audit-safe", ExpiresAt: now.Add(24 * time.Hour), UserID: "user-1",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := svc.List(context.Background(), ListInput{UserID: "user-1"}); err != nil {
		t.Fatalf("List() error = %v", err)
	}
	payload, err := json.Marshal(recorder.events)
	if err != nil {
		t.Fatalf("marshal audit events: %v", err)
	}
	if strings.Contains(string(payload), created.Token) || strings.Contains(string(payload), "encrypted:") {
		t.Fatalf("audit payload contains token material: %s", payload)
	}
}
