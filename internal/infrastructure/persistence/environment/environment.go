// Package environment 环境仓储 PostgreSQL 实现（GORM）。
package environment

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	envdomain "env-vault/internal/domain/environment"
)

// environmentPO environment_info 表持久化对象（数据库列名下划线）
type environmentPO struct {
	ID          uuid.UUID  `gorm:"column:id;primaryKey"`
	Code        string     `gorm:"column:code"`
	Name        string     `gorm:"column:name"`
	Remark      string     `gorm:"column:remark"`
	ProjectID   uuid.UUID  `gorm:"column:project_id"`
	OrderNo     int        `gorm:"column:order_no"`
	IsCheckPerm bool       `gorm:"column:is_check_perm"`
	IsDeleted   bool       `gorm:"column:is_deleted"`
	DeleteAt    *time.Time `gorm:"column:delete_at"`
	DeleteBy    string     `gorm:"column:delete_by"`
	CreateBy    string     `gorm:"column:create_by"`
	UpdateBy    string     `gorm:"column:update_by"`
	CreateAt    time.Time  `gorm:"column:create_at"`
	UpdateAt    time.Time  `gorm:"column:update_at"`
}

// TableName 指定表名
func (environmentPO) TableName() string {
	return "environment_info"
}

// Repository 环境仓储 GORM 实现
type Repository struct {
	db *gorm.DB
}

// NewRepository 创建环境仓储
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// CreateBatch 批量创建环境（GORM 对 slice 生成单条多行 INSERT，天然原子）
func (r *Repository) CreateBatch(ctx context.Context, environments []*envdomain.Environment) error {
	if len(environments) == 0 {
		return nil
	}
	pos := make([]*environmentPO, 0, len(environments))
	for _, e := range environments {
		pos = append(pos, toPO(e))
	}
	return r.db.WithContext(ctx).Create(&pos).Error
}

// Update 更新环境（按 ID）
func (r *Repository) Update(ctx context.Context, e *envdomain.Environment) error {
	po := toPO(e)
	return r.db.WithContext(ctx).
		Model(&environmentPO{}).
		Where("id = ? AND is_deleted = false", po.ID).
		Updates(map[string]any{
			"name":          po.Name,
			"remark":        po.Remark,
			"order_no":      po.OrderNo,
			"is_check_perm": po.IsCheckPerm,
			"update_by":     po.UpdateBy,
			"update_at":     po.UpdateAt,
		}).Error
}

// Delete 软删除环境（按 ID）
func (r *Repository) Delete(ctx context.Context, id uuid.UUID, deleteBy string) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&environmentPO{}).
		Where("id = ? AND is_deleted = false", id).
		Updates(map[string]any{
			"is_deleted": true,
			"delete_at":  now,
			"delete_by":  deleteBy,
			"update_at":  now,
			"update_by":  deleteBy,
		}).Error
}

// GetByID 按 ID 查询环境（不含已删除）
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*envdomain.Environment, error) {
	var po environmentPO
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

// GetByProjectCode 按项目 + 编码查询环境（不含已删除）
func (r *Repository) GetByProjectCode(ctx context.Context, projectID uuid.UUID, code string) (*envdomain.Environment, error) {
	var po environmentPO
	err := r.db.WithContext(ctx).
		Where("project_id = ? AND code = ? AND is_deleted = false", projectID, code).
		First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toDomain(&po), nil
}

// List 查询项目下全部环境（不含已删除，按排序号升序，不分页）
func (r *Repository) List(ctx context.Context, projectID uuid.UUID) ([]*envdomain.Environment, error) {
	var pos []environmentPO
	err := r.db.WithContext(ctx).
		Where("project_id = ? AND is_deleted = false", projectID).
		Order("order_no ASC").
		Order("create_at DESC").
		Find(&pos).Error
	if err != nil {
		return nil, err
	}

	environments := make([]*envdomain.Environment, 0, len(pos))
	for i := range pos {
		environments = append(environments, toDomain(&pos[i]))
	}
	return environments, nil
}

// toPO 领域模型转持久化对象
func toPO(e *envdomain.Environment) *environmentPO {
	return &environmentPO{
		ID:          e.ID,
		Code:        e.Code,
		Name:        e.Name,
		Remark:      e.Remark,
		ProjectID:   e.ProjectID,
		OrderNo:     e.OrderNo,
		IsCheckPerm: e.IsCheckPerm,
		IsDeleted:   e.IsDeleted,
		DeleteAt:    e.DeleteAt,
		DeleteBy:    e.DeleteBy,
		CreateBy:    e.CreateBy,
		UpdateBy:    e.UpdateBy,
		CreateAt:    e.CreateAt,
		UpdateAt:    e.UpdateAt,
	}
}

// toDomain 持久化对象转领域模型
func toDomain(po *environmentPO) *envdomain.Environment {
	return &envdomain.Environment{
		ID:          po.ID,
		Code:        po.Code,
		Name:        po.Name,
		Remark:      po.Remark,
		ProjectID:   po.ProjectID,
		OrderNo:     po.OrderNo,
		IsCheckPerm: po.IsCheckPerm,
		IsDeleted:   po.IsDeleted,
		DeleteAt:    po.DeleteAt,
		DeleteBy:    po.DeleteBy,
		CreateBy:    po.CreateBy,
		UpdateBy:    po.UpdateBy,
		CreateAt:    po.CreateAt,
		UpdateAt:    po.UpdateAt,
	}
}
