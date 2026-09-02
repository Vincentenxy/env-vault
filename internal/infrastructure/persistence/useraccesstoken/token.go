// Package useraccesstoken implements personal access token persistence with GORM
package useraccesstoken

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	tokendomain "env-vault/internal/domain/useraccesstoken"
	"env-vault/internal/infrastructure/persistence"
)

// tokenPO maps the user_access_token_info table without exposing persistence tags to the domain
type tokenPO struct {
	ID              uuid.UUID  `gorm:"column:id;primaryKey"`
	OwnerID         uuid.UUID  `gorm:"column:owner_id"`
	Name            string     `gorm:"column:name"`
	JTI             uuid.UUID  `gorm:"column:jti"`
	TokenCiphertext string     `gorm:"column:token_ciphertext"`
	ExpiresAt       time.Time  `gorm:"column:expires_at"`
	LastUsedAt      *time.Time `gorm:"column:last_used_at"`
	IsDeleted       bool       `gorm:"column:is_deleted"`
	DeleteAt        *time.Time `gorm:"column:delete_at"`
	DeleteBy        string     `gorm:"column:delete_by"`
	CreateBy        string     `gorm:"column:create_by"`
	UpdateBy        string     `gorm:"column:update_by"`
	CreateAt        time.Time  `gorm:"column:create_at"`
	UpdateAt        time.Time  `gorm:"column:update_at"`
}

func (tokenPO) TableName() string { return "user_access_token_info" }

// Repository stores personal access tokens in PostgreSQL
type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

func (r *Repository) dbFor(ctx context.Context) *gorm.DB {
	return persistence.TxDB(ctx, r.db).WithContext(ctx)
}

// LockOwner serializes active-token counting and creation for one user
func (r *Repository) LockOwner(ctx context.Context, ownerID uuid.UUID) (bool, error) {
	var owner struct {
		ID uuid.UUID `gorm:"column:id"`
	}
	result := r.dbFor(ctx).Raw(
		"SELECT id FROM user_info WHERE id = ? AND is_deleted = false FOR UPDATE",
		ownerID,
	).Scan(&owner)
	if result.Error != nil {
		return false, result.Error
	}
	return owner.ID != uuid.Nil, nil
}

func (r *Repository) CountActiveByOwner(ctx context.Context, ownerID uuid.UUID, now time.Time) (int64, error) {
	var count int64
	err := r.dbFor(ctx).Model(&tokenPO{}).
		Where("owner_id = ? AND is_deleted = false AND expires_at > ?", ownerID, now).
		Count(&count).Error
	return count, err
}

func (r *Repository) Create(ctx context.Context, token *tokendomain.Token) error {
	return r.dbFor(ctx).Create(toPO(token)).Error
}

func (r *Repository) GetByIDAndOwner(ctx context.Context, id, ownerID uuid.UUID) (*tokendomain.Token, error) {
	var po tokenPO
	err := r.dbFor(ctx).
		Where("id = ? AND owner_id = ? AND is_deleted = false", id, ownerID).
		First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toDomain(&po), nil
}

func (r *Repository) GetUsableByJTI(ctx context.Context, jti uuid.UUID, now time.Time) (*tokendomain.Token, error) {
	var po tokenPO
	err := r.dbFor(ctx).
		Where("jti = ? AND is_deleted = false AND expires_at > ?", jti, now).
		First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toDomain(&po), nil
}

func (r *Repository) ListByOwner(ctx context.Context, ownerID uuid.UUID) ([]*tokendomain.Token, error) {
	var pos []tokenPO
	err := r.dbFor(ctx).
		Where("owner_id = ? AND is_deleted = false", ownerID).
		Order("create_at DESC, id DESC").
		Find(&pos).Error
	if err != nil {
		return nil, err
	}
	items := make([]*tokendomain.Token, 0, len(pos))
	for i := range pos {
		items = append(items, toDomain(&pos[i]))
	}
	return items, nil
}

// TouchLastUsed limits write amplification by updating at most once every five minutes
func (r *Repository) TouchLastUsed(ctx context.Context, id uuid.UUID, now, staleBefore time.Time) error {
	return r.dbFor(ctx).Model(&tokenPO{}).
		Where("id = ? AND is_deleted = false AND (last_used_at IS NULL OR last_used_at < ?)", id, staleBefore).
		Updates(map[string]any{"last_used_at": now, "update_at": now}).Error
}

func (r *Repository) SoftDelete(ctx context.Context, id, ownerID uuid.UUID, operator string, now time.Time) (int64, error) {
	result := r.dbFor(ctx).Model(&tokenPO{}).
		Where("id = ? AND owner_id = ? AND is_deleted = false", id, ownerID).
		Updates(map[string]any{
			"is_deleted": true, "delete_at": now, "delete_by": operator,
			"update_at": now, "update_by": operator,
		})
	return result.RowsAffected, result.Error
}

func (r *Repository) WithTx(ctx context.Context, fn func(context.Context) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(persistence.WithTx(ctx, tx))
	})
}

func toPO(item *tokendomain.Token) *tokenPO {
	return &tokenPO{
		ID: item.ID, OwnerID: item.OwnerID, Name: item.Name, JTI: item.JTI,
		TokenCiphertext: item.TokenCiphertext, ExpiresAt: item.ExpiresAt, LastUsedAt: item.LastUsedAt,
		IsDeleted: item.IsDeleted, DeleteAt: item.DeleteAt, DeleteBy: item.DeleteBy,
		CreateBy: item.CreateBy, UpdateBy: item.UpdateBy, CreateAt: item.CreateAt, UpdateAt: item.UpdateAt,
	}
}

func toDomain(po *tokenPO) *tokendomain.Token {
	return &tokendomain.Token{
		ID: po.ID, OwnerID: po.OwnerID, Name: po.Name, JTI: po.JTI,
		TokenCiphertext: po.TokenCiphertext, ExpiresAt: po.ExpiresAt, LastUsedAt: po.LastUsedAt,
		IsDeleted: po.IsDeleted, DeleteAt: po.DeleteAt, DeleteBy: po.DeleteBy,
		CreateBy: po.CreateBy, UpdateBy: po.UpdateBy, CreateAt: po.CreateAt, UpdateAt: po.UpdateAt,
	}
}
