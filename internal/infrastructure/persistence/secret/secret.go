// Package secret 密钥仓储 PostgreSQL 实现（GORM）。
package secret

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	secretdomain "env-vault/internal/domain/secret"
)

// secretPO secret_info 表持久化对象（数据库列名下划线）
type secretPO struct {
	ID              uuid.UUID  `gorm:"column:id;primaryKey"`
	GroupID         uuid.UUID  `gorm:"column:group_id"`
	FolderID        uuid.UUID  `gorm:"column:folder_id"`
	EnvCode         string     `gorm:"column:env_code"`
	Key             string     `gorm:"column:key"`
	ValueCiphertext string     `gorm:"column:value_ciphertext"`
	ValueType       string     `gorm:"column:value_type"`
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

// TableName 指定表名
func (secretPO) TableName() string {
	return "secret_info"
}

// Repository 密钥仓储 GORM 实现
type Repository struct {
	db *gorm.DB
}

// NewRepository 创建密钥仓储
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// CreateBatch 批量创建密钥（GORM 对 slice 生成单条多行 INSERT，天然原子）
func (r *Repository) CreateBatch(ctx context.Context, secrets []*secretdomain.Secret) error {
	if len(secrets) == 0 {
		return nil
	}
	pos := make([]*secretPO, 0, len(secrets))
	for _, s := range secrets {
		pos = append(pos, toPO(s))
	}
	return r.db.WithContext(ctx).Create(&pos).Error
}

// DeleteByGroupID 按 group_id 软删除全部环境实例
func (r *Repository) DeleteByGroupID(ctx context.Context, groupID uuid.UUID, deleteBy string) (int64, error) {
	now := time.Now()
	result := r.db.WithContext(ctx).
		Model(&secretPO{}).
		Where("group_id = ? AND is_deleted = false", groupID).
		Updates(map[string]any{
			"is_deleted": true,
			"delete_at":  now,
			"delete_by":  deleteBy,
			"update_at":  now,
			"update_by":  deleteBy,
		})
	return result.RowsAffected, result.Error
}

// GetByID 按 ID 查询密钥（不含已删除）
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*secretdomain.Secret, error) {
	var po secretPO
	err := r.db.WithContext(ctx).
		Where("id = ? AND is_deleted = false", id).
		First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toDomain(&po), nil
}

// GetByFolderIDsKey 按文件夹集合 + key 查询密钥（不含已删除），不存在返回 nil, nil
func (r *Repository) GetByFolderIDsKey(ctx context.Context, folderIDs []uuid.UUID, key string) (*secretdomain.Secret, error) {
	var po secretPO
	err := r.db.WithContext(ctx).
		Where("folder_id IN ? AND key = ? AND is_deleted = false", folderIDs, key).
		First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toDomain(&po), nil
}

// ListByFolderIDs 查询文件夹集合下的全部密钥（不含已删除，按创建时间倒序）
func (r *Repository) ListByFolderIDs(ctx context.Context, folderIDs []uuid.UUID) ([]*secretdomain.Secret, error) {
	var pos []secretPO
	err := r.db.WithContext(ctx).
		Where("folder_id IN ? AND is_deleted = false", folderIDs).
		Order("create_at DESC").
		Find(&pos).Error
	if err != nil {
		return nil, err
	}

	secrets := make([]*secretdomain.Secret, 0, len(pos))
	for i := range pos {
		secrets = append(secrets, toDomain(&pos[i]))
	}
	return secrets, nil
}

// ListByGroupID 按 group_id 查询业务组下的全部环境实例（不含已删除）
func (r *Repository) ListByGroupID(ctx context.Context, groupID uuid.UUID) ([]*secretdomain.Secret, error) {
	var pos []secretPO
	err := r.db.WithContext(ctx).
		Where("group_id = ? AND is_deleted = false", groupID).
		Find(&pos).Error
	if err != nil {
		return nil, err
	}

	secrets := make([]*secretdomain.Secret, 0, len(pos))
	for i := range pos {
		secrets = append(secrets, toDomain(&pos[i]))
	}
	return secrets, nil
}

// toPO 领域模型转持久化对象
func toPO(s *secretdomain.Secret) *secretPO {
	return &secretPO{
		ID:              s.ID,
		GroupID:         s.GroupID,
		FolderID:        s.FolderID,
		EnvCode:         s.EnvCode,
		Key:             s.Key,
		ValueCiphertext: s.ValueCiphertext,
		ValueType:       s.ValueType,
		Remark:          s.Remark,
		Version:         s.Version,
		IsDeleted:       s.IsDeleted,
		DeleteAt:        s.DeleteAt,
		DeleteBy:        s.DeleteBy,
		CreateBy:        s.CreateBy,
		UpdateBy:        s.UpdateBy,
		CreateAt:        s.CreateAt,
		UpdateAt:        s.UpdateAt,
	}
}

// toDomain 持久化对象转领域模型
func toDomain(po *secretPO) *secretdomain.Secret {
	return &secretdomain.Secret{
		ID:              po.ID,
		GroupID:         po.GroupID,
		FolderID:        po.FolderID,
		EnvCode:         po.EnvCode,
		Key:             po.Key,
		ValueCiphertext: po.ValueCiphertext,
		ValueType:       po.ValueType,
		Remark:          po.Remark,
		Version:         po.Version,
		IsDeleted:       po.IsDeleted,
		DeleteAt:        po.DeleteAt,
		DeleteBy:        po.DeleteBy,
		CreateBy:        po.CreateBy,
		UpdateBy:        po.UpdateBy,
		CreateAt:        po.CreateAt,
		UpdateAt:        po.UpdateAt,
	}
}
