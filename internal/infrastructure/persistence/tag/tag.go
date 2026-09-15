package tag

import (
	"context"
	"errors"
	"strings"
	"time"

	domain "env-vault/internal/domain/tag"
	"env-vault/internal/infrastructure/persistence"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }
func (r *Repository) WithTx(ctx context.Context, work func(context.Context) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return work(persistence.WithTx(ctx, tx))
	})
}
func (r *Repository) query(ctx context.Context) *gorm.DB {
	return persistence.TxDB(ctx, r.db).WithContext(ctx).Table("tag_info").Where("is_deleted = false")
}
func (r *Repository) Create(ctx context.Context, item *domain.Tag) error {
	result := persistence.TxDB(ctx, r.db).WithContext(ctx).Table("tag_info").Clauses(clause.OnConflict{
		Columns:     []clause.Column{{Name: "tenant_id"}, {Name: "code"}},
		TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "is_deleted = false"}}},
		DoNothing:   true,
	}).Create(item)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return domain.ErrCodeExists
	}
	return nil
}
func (r *Repository) Get(ctx context.Context, tenantID, id uuid.UUID) (*domain.Tag, error) {
	var item domain.Tag
	err := r.query(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).Take(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrNotFound
	}
	return &item, err
}
func (r *Repository) Update(ctx context.Context, item *domain.Tag) error {
	result := r.query(ctx).Where("tenant_id = ? AND id = ?", item.TenantID, item.ID).Updates(map[string]any{
		"name": item.Name, "remark": item.Remark, "allow_value_search": item.AllowValueSearch,
		"update_by": item.UpdateBy, "update_at": item.UpdateAt,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}
func (r *Repository) Delete(ctx context.Context, tenantID, id uuid.UUID, operator string) error {
	return persistence.TxDB(ctx, r.db).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Table("tag_info").Where("is_deleted = false AND tenant_id = ? AND id = ?", tenantID, id).Updates(map[string]any{
			"is_deleted": true, "delete_by": operator, "delete_at": time.Now(), "update_by": operator, "update_at": time.Now(),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return domain.ErrNotFound
		}
		// 标签行更新先取得行锁，清理与绑定写入互斥
		return SoftDeleteRelations(tx.Where("tag_id = ?", id), operator)
	})
}
func (r *Repository) List(ctx context.Context, filter domain.Filter) ([]*domain.Tag, int64, error) {
	query := r.query(ctx).Where("tenant_id = ?", filter.TenantID)
	if filter.Keyword != "" {
		keyword := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(filter.Keyword) + "%"
		query = query.Where("(code ILIKE ? OR name ILIKE ?)", keyword, keyword)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]*domain.Tag, 0)
	err := query.Order("create_at DESC, id DESC").Offset((filter.PageNum - 1) * filter.PageSize).Limit(filter.PageSize).Find(&items).Error
	return items, total, err
}
