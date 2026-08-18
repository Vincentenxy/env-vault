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

// txKey context 中携带事务句柄的 key（类型不导出避免跨包冲突）
type txKey struct{}

// withTxDB 从 ctx 中取出 *gorm.DB 事务句柄；没有则用原 db
func (r *Repository) withTxDB(ctx context.Context) *gorm.DB {
	if v := ctx.Value(txKey{}); v != nil {
		if tx, ok := v.(*gorm.DB); ok {
			return tx
		}
	}
	return r.db
}

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
	return r.withTxDB(ctx).WithContext(ctx).Create(&pos).Error
}

// DeleteByGroupID 按 group_id 软删除全部环境实例
func (r *Repository) DeleteByGroupID(ctx context.Context, groupID uuid.UUID, deleteBy string) (int64, error) {
	now := time.Now()
	result := r.withTxDB(ctx).WithContext(ctx).
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
	err := r.withTxDB(ctx).WithContext(ctx).
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
	err := r.withTxDB(ctx).WithContext(ctx).
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
	err := r.withTxDB(ctx).WithContext(ctx).
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
	err := r.withTxDB(ctx).WithContext(ctx).
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

// UpdateValueByIDs 按 ID 集合逐条更新 value_ciphertext 与 version（version = version + 1）。
// 若 ctx 已携带事务句柄则参与该事务，否则单条自动提交。
func (r *Repository) UpdateValueByIDs(ctx context.Context, items []secretdomain.ValueUpdateItem, updateBy string, updateAt time.Time) error {
	if len(items) == 0 {
		return nil
	}
	db := r.withTxDB(ctx).WithContext(ctx)
	for _, item := range items {
		res := db.Model(&secretPO{}).
			Where("id = ? AND version = ? AND is_deleted = false", item.ID, item.ExpectedVersion).
			Updates(map[string]any{
				"value_ciphertext": item.ValueCiphertext,
				"version":          gorm.Expr("version + ?", 1),
				"update_by":        updateBy,
				"update_at":        updateAt,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return secretdomain.ErrVersionConflict
		}
	}
	return nil
}

// UpdateRemarkByGroupID 按 group_id 全环境同步更新 remark（与 folder_repo.UpdateByGroupID 同模式）。
func (r *Repository) UpdateRemarkByGroupID(ctx context.Context, groupID uuid.UUID, remark, updateBy string, updateAt time.Time) (int64, error) {
	res := r.withTxDB(ctx).WithContext(ctx).
		Model(&secretPO{}).
		Where("group_id = ? AND is_deleted = false", groupID).
		Updates(map[string]any{
			"remark":    remark,
			"update_by": updateBy,
			"update_at": updateAt,
		})
	return res.RowsAffected, res.Error
}

// WithTx 在事务中执行 fn：把 *gorm.DB 事务句柄塞到 ctx 里传入 fn，fn 内调用本仓储的方法会自动复用该事务。
// fn 返回 nil 则提交事务；返回 error 则回滚。
func (r *Repository) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txCtx := context.WithValue(ctx, txKey{}, tx)
		return fn(txCtx)
	})
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
