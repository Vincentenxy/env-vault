// Package personalsecret defines personal credential models and persistence ports.
package personalsecret

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrVersionConflict = errors.New("personal secret version conflict")

// Secret is the current version of a credential owned by one user.
type Secret struct {
	ID              uuid.UUID
	OwnerID         uuid.UUID
	Name            string
	CredentialType  string
	Account         string
	LoginURL        string
	ValueCiphertext string
	Remark          string
	Version         int
	IsDeleted       bool
	DeleteAt        *time.Time
	DeleteBy        string
	CreateBy        string
	UpdateBy        string
	CreateAt        time.Time
	UpdateAt        time.Time
}

// History is an immutable full snapshot of one personal credential version.
type History struct {
	ID               uuid.UUID
	PersonalSecretID uuid.UUID
	BatchID          uuid.UUID
	OwnerID          uuid.UUID
	Name             string
	CredentialType   string
	Account          string
	LoginURL         string
	ValueCiphertext  string
	Remark           string
	Version          int
	CommitMsg        string
	CreateBy         string
	CreateAt         time.Time
}

type ListFilter struct {
	OwnerID uuid.UUID
	Keyword string
	Offset  int
	Limit   int
}

type HistoryFilter struct {
	OwnerID          uuid.UUID
	PersonalSecretID uuid.UUID
	Offset           int
	Limit            int
}

// Repository persists current values and immutable history snapshots.
type Repository interface {
	Create(ctx context.Context, secret *Secret) error
	GetByIDAndOwner(ctx context.Context, id, ownerID uuid.UUID) (*Secret, error)
	List(ctx context.Context, filter ListFilter) ([]*Secret, int64, error)
	Update(ctx context.Context, secret *Secret, expectedVersion int) error
	SoftDelete(ctx context.Context, id, ownerID uuid.UUID, expectedVersion int, operator string, now time.Time) (int64, error)
	CreateHistory(ctx context.Context, history *History) error
	ListHistory(ctx context.Context, filter HistoryFilter) ([]*History, int64, error)
	GetHistoryByIDAndOwner(ctx context.Context, id, personalSecretID, ownerID uuid.UUID) (*History, error)
	WithTx(ctx context.Context, fn func(context.Context) error) error
}
