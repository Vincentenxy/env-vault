package tag

import (
	"context"
	auditdomain "env-vault/internal/domain/audit"
	domain "env-vault/internal/domain/tag"
	tenantdomain "env-vault/internal/domain/tenant"
	"errors"
	"github.com/google/uuid"
	"testing"
)

type testTenants struct{ missing bool }

func (r testTenants) GetByID(ctx context.Context, id uuid.UUID) (*tenantdomain.Tenant, error) {
	if r.missing {
		return nil, nil
	}
	return &tenantdomain.Tenant{ID: id}, nil
}

type memoryRepo struct{ items map[uuid.UUID]domain.Tag }

func (r *memoryRepo) Create(ctx context.Context, item *domain.Tag) error {
	for _, existing := range r.items {
		if existing.TenantID == item.TenantID && existing.Code == item.Code {
			return domain.ErrCodeExists
		}
	}
	r.items[item.ID] = *item
	return nil
}
func (r *memoryRepo) Get(ctx context.Context, tenantID, id uuid.UUID) (*domain.Tag, error) {
	item, ok := r.items[id]
	if !ok || item.TenantID != tenantID {
		return nil, domain.ErrNotFound
	}
	return &item, nil
}
func (r *memoryRepo) Update(ctx context.Context, item *domain.Tag) error {
	r.items[item.ID] = *item
	return nil
}
func (r *memoryRepo) Delete(ctx context.Context, tenantID, id uuid.UUID, operator string) error {
	if _, err := r.Get(ctx, tenantID, id); err != nil {
		return err
	}
	delete(r.items, id)
	return nil
}
func (r *memoryRepo) List(ctx context.Context, filter domain.Filter) ([]*domain.Tag, int64, error) {
	items := make([]*domain.Tag, 0)
	for _, item := range r.items {
		if item.TenantID == filter.TenantID {
			value := item
			items = append(items, &value)
		}
	}
	return items, int64(len(items)), nil
}
func (r *memoryRepo) WithTx(ctx context.Context, work func(context.Context) error) error {
	before := make(map[uuid.UUID]domain.Tag)
	for id, item := range r.items {
		before[id] = item
	}
	if err := work(ctx); err != nil {
		r.items = before
		return err
	}
	return nil
}

type brokenAudit struct{}

func (brokenAudit) Record(context.Context, *auditdomain.Event) error {
	return errors.New("audit unavailable")
}
func (brokenAudit) RecordBatch(context.Context, []*auditdomain.Event) error {
	return errors.New("audit unavailable")
}

func TestTagCRUD(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	otherTenant := uuid.New()
	repo := &memoryRepo{items: make(map[uuid.UUID]domain.Tag)}
	svc := NewService(repo, testTenants{}, nil)
	item, err := svc.Create(ctx, Input{TenantID: tenantID, Code: "ip", Name: " IP "}, "user")
	if err != nil || !item.AllowValueSearch || item.Name != "IP" {
		t.Fatalf("create: %+v %v", item, err)
	}
	deny := false
	item, err = svc.Update(ctx, Input{TenantID: tenantID, ID: item.ID, Name: "IP", AllowValueSearch: &deny}, "user")
	if err != nil || item.AllowValueSearch {
		t.Fatalf("explicit false lost: %+v %v", item, err)
	}
	item, err = svc.Update(ctx, Input{TenantID: tenantID, ID: item.ID, Name: "Updated"}, "user")
	if err != nil || item.AllowValueSearch {
		t.Fatalf("omitted policy must preserve false: %+v %v", item, err)
	}
	_, err = svc.Update(ctx, Input{TenantID: tenantID, ID: item.ID, Code: "renamed", Name: "IP"}, "user")
	if !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("code mutable: %v", err)
	}
	_, err = svc.Info(ctx, Input{TenantID: otherTenant, ID: item.ID})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross tenant read: %v", err)
	}
	_, err = svc.Update(ctx, Input{TenantID: otherTenant, ID: item.ID, Name: "wrong"}, "user")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross tenant update: %v", err)
	}
	if err = svc.Delete(ctx, Input{TenantID: otherTenant, ID: item.ID}, "user"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross tenant delete: %v", err)
	}
	_, err = svc.Create(ctx, Input{TenantID: tenantID, Code: "ip", Name: "duplicate"}, "user")
	if !errors.Is(err, domain.ErrCodeExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err = svc.Create(ctx, Input{TenantID: otherTenant, Code: "ip", Name: "other", AllowValueSearch: &deny}, "user"); err != nil {
		t.Fatal(err)
	}
	items, total, err := svc.List(ctx, domain.Filter{TenantID: tenantID, PageNum: 1, PageSize: 20})
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("list: %v %v", items, err)
	}
	if err = svc.Delete(ctx, Input{TenantID: tenantID, ID: item.ID}, "user"); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Info(ctx, Input{TenantID: tenantID, ID: item.ID}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = svc.Create(ctx, Input{TenantID: tenantID, Code: "ip", Name: "recreated"}, "user"); err != nil {
		t.Fatal(err)
	}
}
func TestTagValidationAndAuditRollback(t *testing.T) {
	ctx := context.Background()
	repo := &memoryRepo{items: make(map[uuid.UUID]domain.Tag)}
	input := Input{TenantID: uuid.New(), Code: "password", Name: "密码"}
	svc := NewService(repo, testTenants{missing: true}, nil)
	if _, err := svc.Create(ctx, input, "user"); !errors.Is(err, ErrTenantNotFound) {
		t.Fatal(err)
	}
	svc = NewService(repo, testTenants{}, brokenAudit{})
	if _, err := svc.Create(ctx, input, "user"); err == nil || len(repo.items) != 0 {
		t.Fatalf("audit failure must rollback: %v", err)
	}
	svc = NewService(repo, testTenants{}, nil)
	input.Code = "INVALID"
	if _, err := svc.Create(ctx, input, "user"); !errors.Is(err, ErrInvalidParam) {
		t.Fatal(err)
	}
	input.Code = "valid"
	input.Name = "  "
	if _, err := svc.Create(ctx, input, "user"); !errors.Is(err, ErrInvalidParam) {
		t.Fatal(err)
	}
}
