// Package useraccesstoken defines personal access token models and persistence ports
package useraccesstoken

import (
	"context"
	"time"

	"github.com/google/uuid"
)

const (
	// AuthSource identifies personal tokens in JWT claims
	AuthSource = "personalToken"
	// TokenUse separates PATs from interactive access tokens
	TokenUse = "personalAccessToken"
	// TokenType is the required JWT typ header for PATs
	TokenType = "env-vault-pat+jwt"
)

// Token stores one encrypted personal access token owned by a user
type Token struct {
	ID              uuid.UUID
	OwnerID         uuid.UUID
	Name            string
	JTI             uuid.UUID
	TokenCiphertext string
	ExpiresAt       time.Time
	LastUsedAt      *time.Time
	IsDeleted       bool
	DeleteAt        *time.Time
	DeleteBy        string
	CreateBy        string
	UpdateBy        string
	CreateAt        time.Time
	UpdateAt        time.Time
}

// Repository persists personal access tokens and serializes per-user creation
type Repository interface {
	LockOwner(ctx context.Context, ownerID uuid.UUID) (bool, error)
	CountActiveByOwner(ctx context.Context, ownerID uuid.UUID, now time.Time) (int64, error)
	Create(ctx context.Context, token *Token) error
	GetByIDAndOwner(ctx context.Context, id, ownerID uuid.UUID) (*Token, error)
	GetUsableByJTI(ctx context.Context, jti uuid.UUID, now time.Time) (*Token, error)
	ListByOwner(ctx context.Context, ownerID uuid.UUID) ([]*Token, error)
	TouchLastUsed(ctx context.Context, id uuid.UUID, now, staleBefore time.Time) error
	SoftDelete(ctx context.Context, id, ownerID uuid.UUID, operator string, now time.Time) (int64, error)
	WithTx(ctx context.Context, fn func(context.Context) error) error
}
