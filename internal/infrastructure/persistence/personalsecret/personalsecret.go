// Package personalsecret implements personal credential persistence with GORM.
package personalsecret

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	personaldomain "env-vault/internal/domain/personalsecret"
	"env-vault/internal/infrastructure/persistence"
)

type secretPO struct {
	ID              uuid.UUID  `gorm:"column:id;primaryKey"`
	OwnerID         uuid.UUID  `gorm:"column:owner_id"`
	Name            string     `gorm:"column:name"`
	CredentialType  string     `gorm:"column:credential_type"`
	Account         string     `gorm:"column:account"`
	LoginURL        string     `gorm:"column:login_url"`
	ValueCiphertext string     `gorm:"column:value_ciphertext"`
	Remark          string     `gorm:"column:remark"`
	Version         int        `gorm:"column:version"`
	IsDeleted       bool       `gorm:"column:is_deleted"`
	DeleteAt        *time.Time `gorm:"column:delete_at"`
	DeleteBy        string     `gorm:"column:delete_by"`
	CreateBy        string     `gorm:"column:create_by"`
	UpdateBy        string     `gorm:"column:update_by"`
	CreateAt        time.Time  `gorm:"column:create_at"`
	UpdateAt        time.Time  `gorm:"column:update_at"`
}

func (secretPO) TableName() string { return "personal_secret_info" }

type historyPO struct {
	ID               uuid.UUID `gorm:"column:id;primaryKey"`
	PersonalSecretID uuid.UUID `gorm:"column:personal_secret_id"`
	BatchID          uuid.UUID `gorm:"column:batch_id"`
	OwnerID          uuid.UUID `gorm:"column:owner_id"`
	Name             string    `gorm:"column:name"`
	CredentialType   string    `gorm:"column:credential_type"`
	Account          string    `gorm:"column:account"`
	LoginURL         string    `gorm:"column:login_url"`
	ValueCiphertext  string    `gorm:"column:value_ciphertext"`
	Remark           string    `gorm:"column:remark"`
	Version          int       `gorm:"column:version"`
	CommitMsg        string    `gorm:"column:commit_msg"`
	CreateBy         string    `gorm:"column:create_by"`
	CreateAt         time.Time `gorm:"column:create_at"`
}

func (historyPO) TableName() string { return "personal_secret_info_history" }

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

func (r *Repository) dbFor(ctx context.Context) *gorm.DB {
	return persistence.TxDB(ctx, r.db).WithContext(ctx)
}

func (r *Repository) Create(ctx context.Context, secret *personaldomain.Secret) error {
	return r.dbFor(ctx).Create(toSecretPO(secret)).Error
}

func (r *Repository) GetByIDAndOwner(ctx context.Context, id, ownerID uuid.UUID) (*personaldomain.Secret, error) {
	var po secretPO
	err := r.dbFor(ctx).Where("id = ? AND owner_id = ? AND is_deleted = false", id, ownerID).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toSecretDomain(&po), nil
}

func (r *Repository) List(ctx context.Context, filter personaldomain.ListFilter) ([]*personaldomain.Secret, int64, error) {
	query := r.dbFor(ctx).Model(&secretPO{}).
		Where("owner_id = ? AND is_deleted = false", filter.OwnerID)
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		pattern := "%" + keyword + "%"
		query = query.Where("name ILIKE ? OR account ILIKE ? OR login_url ILIKE ?", pattern, pattern, pattern)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var pos []secretPO
	if err := query.Order("name ASC, id ASC").Offset(filter.Offset).Limit(filter.Limit).Find(&pos).Error; err != nil {
		return nil, 0, err
	}
	items := make([]*personaldomain.Secret, 0, len(pos))
	for i := range pos {
		items = append(items, toSecretDomain(&pos[i]))
	}
	return items, total, nil
}

func (r *Repository) Update(ctx context.Context, secret *personaldomain.Secret, expectedVersion int) error {
	result := r.dbFor(ctx).Model(&secretPO{}).
		Where("id = ? AND owner_id = ? AND version = ? AND is_deleted = false", secret.ID, secret.OwnerID, expectedVersion).
		Updates(map[string]any{
			"name": secret.Name, "credential_type": secret.CredentialType,
			"account": secret.Account, "login_url": secret.LoginURL,
			"value_ciphertext": secret.ValueCiphertext, "remark": secret.Remark,
			"version": secret.Version, "update_by": secret.UpdateBy, "update_at": secret.UpdateAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return personaldomain.ErrVersionConflict
	}
	return nil
}

func (r *Repository) SoftDelete(ctx context.Context, id, ownerID uuid.UUID, expectedVersion int, operator string, now time.Time) (int64, error) {
	result := r.dbFor(ctx).Model(&secretPO{}).
		Where("id = ? AND owner_id = ? AND version = ? AND is_deleted = false", id, ownerID, expectedVersion).
		Updates(map[string]any{
			"is_deleted": true, "delete_at": now, "delete_by": operator,
			"update_at": now, "update_by": operator,
		})
	return result.RowsAffected, result.Error
}

func (r *Repository) CreateHistory(ctx context.Context, history *personaldomain.History) error {
	return r.dbFor(ctx).Create(toHistoryPO(history)).Error
}

func (r *Repository) ListHistory(ctx context.Context, filter personaldomain.HistoryFilter) ([]*personaldomain.History, int64, error) {
	query := r.dbFor(ctx).Model(&historyPO{}).
		Where("personal_secret_id = ? AND owner_id = ?", filter.PersonalSecretID, filter.OwnerID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var pos []historyPO
	if err := query.Order("version DESC, create_at DESC, id DESC").Offset(filter.Offset).Limit(filter.Limit).Find(&pos).Error; err != nil {
		return nil, 0, err
	}
	items := make([]*personaldomain.History, 0, len(pos))
	for i := range pos {
		items = append(items, toHistoryDomain(&pos[i]))
	}
	return items, total, nil
}

func (r *Repository) GetHistoryByIDAndOwner(ctx context.Context, id, personalSecretID, ownerID uuid.UUID) (*personaldomain.History, error) {
	var po historyPO
	err := r.dbFor(ctx).
		Where("id = ? AND personal_secret_id = ? AND owner_id = ?", id, personalSecretID, ownerID).
		First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toHistoryDomain(&po), nil
}

func (r *Repository) WithTx(ctx context.Context, fn func(context.Context) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(persistence.WithTx(ctx, tx))
	})
}

func toSecretPO(item *personaldomain.Secret) *secretPO {
	return &secretPO{
		ID: item.ID, OwnerID: item.OwnerID, Name: item.Name, CredentialType: item.CredentialType,
		Account: item.Account, LoginURL: item.LoginURL, ValueCiphertext: item.ValueCiphertext,
		Remark: item.Remark, Version: item.Version, IsDeleted: item.IsDeleted,
		DeleteAt: item.DeleteAt, DeleteBy: item.DeleteBy, CreateBy: item.CreateBy,
		UpdateBy: item.UpdateBy, CreateAt: item.CreateAt, UpdateAt: item.UpdateAt,
	}
}

func toSecretDomain(po *secretPO) *personaldomain.Secret {
	return &personaldomain.Secret{
		ID: po.ID, OwnerID: po.OwnerID, Name: po.Name, CredentialType: po.CredentialType,
		Account: po.Account, LoginURL: po.LoginURL, ValueCiphertext: po.ValueCiphertext,
		Remark: po.Remark, Version: po.Version, IsDeleted: po.IsDeleted,
		DeleteAt: po.DeleteAt, DeleteBy: po.DeleteBy, CreateBy: po.CreateBy,
		UpdateBy: po.UpdateBy, CreateAt: po.CreateAt, UpdateAt: po.UpdateAt,
	}
}

func toHistoryPO(item *personaldomain.History) *historyPO {
	return &historyPO{
		ID: item.ID, PersonalSecretID: item.PersonalSecretID, BatchID: item.BatchID,
		OwnerID: item.OwnerID, Name: item.Name, CredentialType: item.CredentialType,
		Account: item.Account, LoginURL: item.LoginURL, ValueCiphertext: item.ValueCiphertext,
		Remark: item.Remark, Version: item.Version, CommitMsg: item.CommitMsg,
		CreateBy: item.CreateBy, CreateAt: item.CreateAt,
	}
}

func toHistoryDomain(po *historyPO) *personaldomain.History {
	return &personaldomain.History{
		ID: po.ID, PersonalSecretID: po.PersonalSecretID, BatchID: po.BatchID,
		OwnerID: po.OwnerID, Name: po.Name, CredentialType: po.CredentialType,
		Account: po.Account, LoginURL: po.LoginURL, ValueCiphertext: po.ValueCiphertext,
		Remark: po.Remark, Version: po.Version, CommitMsg: po.CommitMsg,
		CreateBy: po.CreateBy, CreateAt: po.CreateAt,
	}
}
