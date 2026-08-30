package personalsecret

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	personaldomain "env-vault/internal/domain/personalsecret"
	userdomain "env-vault/internal/domain/user"
)

type memoryRepository struct {
	secrets   map[uuid.UUID]*personaldomain.Secret
	histories []*personaldomain.History
	lastList  personaldomain.ListFilter
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{secrets: make(map[uuid.UUID]*personaldomain.Secret)}
}

func (r *memoryRepository) Create(_ context.Context, item *personaldomain.Secret) error {
	copyItem := *item
	r.secrets[item.ID] = &copyItem
	return nil
}

func (r *memoryRepository) GetByIDAndOwner(_ context.Context, id, ownerID uuid.UUID) (*personaldomain.Secret, error) {
	item := r.secrets[id]
	if item == nil || item.OwnerID != ownerID || item.IsDeleted {
		return nil, nil
	}
	copyItem := *item
	return &copyItem, nil
}

func (r *memoryRepository) List(_ context.Context, filter personaldomain.ListFilter) ([]*personaldomain.Secret, int64, error) {
	r.lastList = filter
	items := make([]*personaldomain.Secret, 0)
	for _, item := range r.secrets {
		if item.OwnerID != filter.OwnerID || item.IsDeleted {
			continue
		}
		if filter.Keyword != "" && !strings.Contains(strings.ToLower(item.Name), strings.ToLower(filter.Keyword)) {
			continue
		}
		copyItem := *item
		items = append(items, &copyItem)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	total := int64(len(items))
	start := filter.Offset
	if start > len(items) {
		start = len(items)
	}
	end := start + filter.Limit
	if end > len(items) {
		end = len(items)
	}
	return items[start:end], total, nil
}

func (r *memoryRepository) Update(_ context.Context, item *personaldomain.Secret, expectedVersion int) error {
	current := r.secrets[item.ID]
	if current == nil || current.OwnerID != item.OwnerID || current.Version != expectedVersion || current.IsDeleted {
		return personaldomain.ErrVersionConflict
	}
	copyItem := *item
	r.secrets[item.ID] = &copyItem
	return nil
}

func (r *memoryRepository) SoftDelete(_ context.Context, id, ownerID uuid.UUID, expectedVersion int, operator string, now time.Time) (int64, error) {
	item := r.secrets[id]
	if item == nil || item.OwnerID != ownerID || item.Version != expectedVersion || item.IsDeleted {
		return 0, nil
	}
	item.IsDeleted = true
	item.DeleteAt = &now
	item.DeleteBy = operator
	return 1, nil
}

func (r *memoryRepository) CreateHistory(_ context.Context, item *personaldomain.History) error {
	copyItem := *item
	r.histories = append(r.histories, &copyItem)
	return nil
}

func (r *memoryRepository) ListHistory(_ context.Context, filter personaldomain.HistoryFilter) ([]*personaldomain.History, int64, error) {
	items := make([]*personaldomain.History, 0)
	for _, item := range r.histories {
		if item.OwnerID == filter.OwnerID && item.PersonalSecretID == filter.PersonalSecretID {
			copyItem := *item
			items = append(items, &copyItem)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Version > items[j].Version })
	total := int64(len(items))
	start := filter.Offset
	if start > len(items) {
		start = len(items)
	}
	end := start + filter.Limit
	if end > len(items) {
		end = len(items)
	}
	return items[start:end], total, nil
}

func (r *memoryRepository) GetHistoryByIDAndOwner(_ context.Context, id, personalSecretID, ownerID uuid.UUID) (*personaldomain.History, error) {
	for _, item := range r.histories {
		if item.ID == id && item.PersonalSecretID == personalSecretID && item.OwnerID == ownerID {
			copyItem := *item
			return &copyItem, nil
		}
	}
	return nil, nil
}

func (r *memoryRepository) WithTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type stubUsers struct{ users map[string]*userdomain.User }

func (s stubUsers) GetProfile(_ context.Context, userID string) (*userdomain.User, error) {
	return s.users[userID], nil
}

func (s stubUsers) GetNickname(_ context.Context, userID string) (string, error) {
	if user := s.users[userID]; user != nil {
		return user.Nickname, nil
	}
	return "", nil
}

type testCryptor struct{}

func (testCryptor) Encrypt(value string) (string, error) { return "encrypted:" + value, nil }
func (testCryptor) Decrypt(value string) (string, error) {
	return strings.TrimPrefix(value, "encrypted:"), nil
}

func TestCreateUpdateAndHistory(t *testing.T) {
	ownerID := uuid.New()
	repo := newMemoryRepository()
	svc := NewService(repo, stubUsers{users: map[string]*userdomain.User{
		"user-1": {ID: ownerID, UserID: "user-1", Nickname: "测试用户"},
	}}, testCryptor{})

	created, err := svc.Create(context.Background(), CreateInput{
		Name: "GitLab", Account: "vince", Value: "initial-password", Remark: "work", UserID: "user-1",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	stored := repo.secrets[created.ID]
	if stored.OwnerID != ownerID {
		t.Fatalf("owner ID = %s, want %s", stored.OwnerID, ownerID)
	}
	if stored.ValueCiphertext == "initial-password" || stored.ValueCiphertext == "" {
		t.Fatalf("password was not encrypted: %q", stored.ValueCiphertext)
	}
	if len(repo.histories) != 1 || repo.histories[0].Version != 1 {
		t.Fatalf("initial history = %#v", repo.histories)
	}

	updated, err := svc.Update(context.Background(), UpdateInput{
		ID: created.ID, Version: created.Version, Name: "GitLab Production", CredentialType: "password",
		Account: "vince", Remark: "work", CommitMsg: "rename", UserID: "user-1",
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Version != 2 || len(repo.histories) != 2 || repo.histories[1].Version != 2 {
		t.Fatalf("updated version = %d, histories = %d", updated.Version, len(repo.histories))
	}

	revealed, err := svc.Reveal(context.Background(), RevealInput{ID: created.ID, UserID: "user-1"})
	if err != nil {
		t.Fatalf("Reveal() error = %v", err)
	}
	if revealed.Value != "initial-password" {
		t.Fatalf("Reveal() value = %q", revealed.Value)
	}
	listed, listedTotal, err := svc.List(context.Background(), ListInput{
		UserID: "user-1", PageNum: 1, PageSize: 20,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if listedTotal != 1 || len(listed) != 1 || listed[0].Value != "initial-password" {
		t.Fatalf("List() total = %d, items = %#v", listedTotal, listed)
	}

	history, total, err := svc.History(context.Background(), HistoryInput{
		PersonalSecretID: created.ID, UserID: "user-1", PageNum: 1, PageSize: 20,
	})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if total != 2 || len(history) != 2 || history[0].Version != 2 {
		t.Fatalf("History() total = %d, items = %#v", total, history)
	}
	if history[0].CreateByName != "测试用户" {
		t.Fatalf("createByName = %q", history[0].CreateByName)
	}
	historyValue, err := svc.RevealHistory(context.Background(), RevealHistoryInput{
		PersonalSecretID: created.ID, HistoryID: history[0].ID, UserID: "user-1",
	})
	if err != nil {
		t.Fatalf("RevealHistory() error = %v", err)
	}
	if historyValue.Value != "initial-password" || historyValue.Version != 2 {
		t.Fatalf("RevealHistory() = %#v", historyValue)
	}
}

func TestOwnerIsolationAndNormalizedPaginationInput(t *testing.T) {
	repo := newMemoryRepository()
	owner1, owner2 := uuid.New(), uuid.New()
	users := stubUsers{users: map[string]*userdomain.User{
		"user-1": {ID: owner1, UserID: "user-1"},
		"user-2": {ID: owner2, UserID: "user-2"},
	}}
	svc := NewService(repo, users, testCryptor{})
	created, err := svc.Create(context.Background(), CreateInput{Name: "Server", Value: "value", UserID: "user-1"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := svc.Reveal(context.Background(), RevealInput{ID: created.ID, UserID: "user-2"}); err != ErrNotFound {
		t.Fatalf("cross-owner Reveal() error = %v, want %v", err, ErrNotFound)
	}
	if _, _, err := svc.History(context.Background(), HistoryInput{
		PersonalSecretID: created.ID, UserID: "user-2", PageNum: 1, PageSize: 20,
	}); err != ErrNotFound {
		t.Fatalf("cross-owner History() error = %v, want %v", err, ErrNotFound)
	}
	items, total, err := svc.List(context.Background(), ListInput{UserID: "user-1", PageNum: 2, PageSize: 7})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 0 || total != 1 || repo.lastList.Offset != 7 || repo.lastList.Limit != 7 {
		t.Fatalf("List() items=%d total=%d filter=%+v", len(items), total, repo.lastList)
	}
}

func TestManageListOnlyReturnsLockedUserSecrets(t *testing.T) {
	repo := newMemoryRepository()
	lockedOwnerID, activeOwnerID := uuid.New(), uuid.New()
	users := stubUsers{users: map[string]*userdomain.User{
		"locked-user": {ID: lockedOwnerID, UserID: "locked-user", Nickname: "离职用户", IsBlocked: true},
		"active-user": {ID: activeOwnerID, UserID: "active-user", Nickname: "在职用户"},
	}}
	secretID := uuid.New()
	repo.secrets[secretID] = &personaldomain.Secret{
		ID: secretID, OwnerID: lockedOwnerID, Name: "VPN", CredentialType: CredentialTypePassword,
		ValueCiphertext: "encrypted:locked-password", Version: 1,
	}
	svc := NewService(repo, users, testCryptor{})

	items, total, err := svc.ManageList(context.Background(), ManageListInput{
		TargetUserID: "locked-user", Operator: "admin-1", PageNum: 1, PageSize: 20,
	})
	if err != nil {
		t.Fatalf("ManageList() error = %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].Value != "locked-password" {
		t.Fatalf("ManageList() total=%d items=%#v", total, items)
	}

	_, _, err = svc.ManageList(context.Background(), ManageListInput{
		TargetUserID: "active-user", Operator: "admin-1", PageNum: 1, PageSize: 20,
	})
	if !errors.Is(err, ErrOwnerNotBlocked) {
		t.Fatalf("active user ManageList() error = %v, want %v", err, ErrOwnerNotBlocked)
	}
}
