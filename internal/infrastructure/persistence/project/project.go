// Package project 项目仓储 PostgreSQL 实现（GORM）。
package project

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	projdomain "env-vault/internal/domain/project"
	"env-vault/internal/infrastructure/persistence"
)

// projectPO project_info 表持久化对象（数据库列名下划线）
type projectPO struct {
	ID        uuid.UUID  `gorm:"column:id;primaryKey"`
	Code      string     `gorm:"column:code"`
	Name      string     `gorm:"column:name"`
	Remark    string     `gorm:"column:remark"`
	OrgID     uuid.UUID  `gorm:"column:org_id"`
	Manager   string     `gorm:"column:manager"`
	IsDeleted bool       `gorm:"column:is_deleted"`
	DeleteAt  *time.Time `gorm:"column:delete_at"`
	DeleteBy  string     `gorm:"column:delete_by"`
	CreateBy  string     `gorm:"column:create_by"`
	UpdateBy  string     `gorm:"column:update_by"`
	CreateAt  time.Time  `gorm:"column:create_at"`
	UpdateAt  time.Time  `gorm:"column:update_at"`
}

// TableName 指定表名
func (projectPO) TableName() string {
	return "project_info"
}

// Repository 项目仓储 GORM 实现
type Repository struct {
	db *gorm.DB
}

// NewRepository 创建项目仓储
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// Create 创建项目
func (r *Repository) Create(ctx context.Context, p *projdomain.Project) error {
	return persistence.TxDB(ctx, r.db).WithContext(ctx).Create(toPO(p)).Error
}

// WithTx 在事务中执行跨项目与环境的创建操作。
func (r *Repository) WithTx(ctx context.Context, fn func(context.Context) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(persistence.WithTx(ctx, tx))
	})
}

// Update 更新项目（按 ID）
func (r *Repository) Update(ctx context.Context, p *projdomain.Project) error {
	po := toPO(p)
	return r.db.WithContext(ctx).
		Model(&projectPO{}).
		Where("id = ? AND is_deleted = false", po.ID).
		Updates(map[string]any{
			"name":      po.Name,
			"remark":    po.Remark,
			"update_by": po.UpdateBy,
			"update_at": po.UpdateAt,
		}).Error
}

// Delete 软删除项目（按 ID）
func (r *Repository) Delete(ctx context.Context, id uuid.UUID, deleteBy string) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&projectPO{}).
		Where("id = ? AND is_deleted = false", id).
		Updates(map[string]any{
			"is_deleted": true,
			"delete_at":  now,
			"delete_by":  deleteBy,
			"update_at":  now,
			"update_by":  deleteBy,
		}).Error
}

// GetByID 按 ID 查询项目（不含已删除）
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*projdomain.Project, error) {
	var po projectPO
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

// GetByOrgCode 按组织 + 编码查询项目（不含已删除）
func (r *Repository) GetByOrgCode(ctx context.Context, orgID uuid.UUID, code string) (*projdomain.Project, error) {
	var po projectPO
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND code = ? AND is_deleted = false", orgID, code).
		First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toDomain(&po), nil
}

// List 分页查询项目列表（不含已删除）
func (r *Repository) List(ctx context.Context, filter projdomain.ListFilter) ([]*projdomain.Project, int64, error) {
	query := r.db.WithContext(ctx).Model(&projectPO{}).Where("is_deleted = false")

	if filter.OrgID != nil {
		query = query.Where("org_id = ?", *filter.OrgID)
	}
	if filter.Code != "" {
		query = query.Where("code ILIKE ?", "%"+filter.Code+"%")
	}
	if filter.Name != "" {
		query = query.Where("name ILIKE ?", "%"+filter.Name+"%")
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var pos []projectPO
	err := query.
		Order("create_at DESC").
		Offset((filter.PageNum - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Find(&pos).Error
	if err != nil {
		return nil, 0, err
	}

	projects := make([]*projdomain.Project, 0, len(pos))
	for i := range pos {
		projects = append(projects, toDomain(&pos[i]))
	}
	return projects, total, nil
}

// toPO 领域模型转持久化对象
func toPO(p *projdomain.Project) *projectPO {
	return &projectPO{
		ID:        p.ID,
		Code:      p.Code,
		Name:      p.Name,
		Remark:    p.Remark,
		OrgID:     p.OrgID,
		Manager:   p.Manager,
		IsDeleted: p.IsDeleted,
		DeleteAt:  p.DeleteAt,
		DeleteBy:  p.DeleteBy,
		CreateBy:  p.CreateBy,
		UpdateBy:  p.UpdateBy,
		CreateAt:  p.CreateAt,
		UpdateAt:  p.UpdateAt,
	}
}

// toDomain 持久化对象转领域模型
func toDomain(po *projectPO) *projdomain.Project {
	return &projdomain.Project{
		ID:        po.ID,
		Code:      po.Code,
		Name:      po.Name,
		Remark:    po.Remark,
		OrgID:     po.OrgID,
		Manager:   po.Manager,
		IsDeleted: po.IsDeleted,
		DeleteAt:  po.DeleteAt,
		DeleteBy:  po.DeleteBy,
		CreateBy:  po.CreateBy,
		UpdateBy:  po.UpdateBy,
		CreateAt:  po.CreateAt,
		UpdateAt:  po.UpdateAt,
	}
}
