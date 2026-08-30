// Package tenant 租户仓储 PostgreSQL 实现（GORM）。
package tenant

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	tenantdomain "env-vault/internal/domain/tenant"
	"env-vault/internal/infrastructure/persistence"
)

// tenantPO tenant_info 表持久化对象（数据库列名下划线）
type tenantPO struct {
	ID          uuid.UUID  `gorm:"column:id;primaryKey"`
	Code        string     `gorm:"column:code"`
	Name        string     `gorm:"column:name"`
	Remark      string     `gorm:"column:remark"`
	Manager     string     `gorm:"column:manager"`
	OrgCount    int64      `gorm:"column:org_count;->"`
	MemberCount int64      `gorm:"column:member_count;->"`
	IsDeleted   bool       `gorm:"column:is_deleted"`
	DeleteAt    *time.Time `gorm:"column:delete_at"`
	DeleteBy    string     `gorm:"column:delete_by"`
	CreateBy    string     `gorm:"column:create_by"`
	UpdateBy    string     `gorm:"column:update_by"`
	CreateAt    time.Time  `gorm:"column:create_at"`
	UpdateAt    time.Time  `gorm:"column:update_at"`
}

// TableName 指定表名
func (tenantPO) TableName() string {
	return "tenant_info"
}

// Repository 租户仓储 GORM 实现
type Repository struct {
	db *gorm.DB
}

// NewRepository 创建租户仓储
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) WithTx(ctx context.Context, fn func(context.Context) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(persistence.WithTx(ctx, tx))
	})
}

// Create 创建租户
func (r *Repository) Create(ctx context.Context, t *tenantdomain.Tenant) error {
	po := toPO(t)
	return persistence.TxDB(ctx, r.db).WithContext(ctx).Create(po).Error
}

// Update 更新租户（按 ID）
func (r *Repository) Update(ctx context.Context, t *tenantdomain.Tenant) error {
	po := toPO(t)
	return persistence.TxDB(ctx, r.db).WithContext(ctx).
		Model(&tenantPO{}).
		Where("id = ? AND is_deleted = false", po.ID).
		Updates(map[string]any{
			"name":      po.Name,
			"remark":    po.Remark,
			"manager":   po.Manager,
			"update_by": po.UpdateBy,
			"update_at": po.UpdateAt,
		}).Error
}

// Delete 软删除租户（按 ID）
func (r *Repository) Delete(ctx context.Context, id uuid.UUID, deleteBy string) error {
	now := time.Now()
	return persistence.TxDB(ctx, r.db).WithContext(ctx).
		Model(&tenantPO{}).
		Where("id = ? AND is_deleted = false", id).
		Updates(map[string]any{
			"is_deleted": true,
			"delete_at":  now,
			"delete_by":  deleteBy,
			"update_at":  now,
			"update_by":  deleteBy,
		}).Error
}

// GetByID 按 ID 查询租户（不含已删除）
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*tenantdomain.Tenant, error) {
	var po tenantPO
	err := withCounts(persistence.TxDB(ctx, r.db).WithContext(ctx)).
		Where("id = ? AND is_deleted = false", id).
		Take(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toDomain(&po), nil
}

// GetByCode 按编码查询租户（不含已删除）
func (r *Repository) GetByCode(ctx context.Context, code string) (*tenantdomain.Tenant, error) {
	var po tenantPO
	err := persistence.TxDB(ctx, r.db).WithContext(ctx).
		Where("code = ? AND is_deleted = false", code).
		First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toDomain(&po), nil
}

// List 分页查询租户列表（不含已删除）
func (r *Repository) List(ctx context.Context, filter tenantdomain.ListFilter) ([]*tenantdomain.Tenant, int64, error) {
	query := persistence.TxDB(ctx, r.db).WithContext(ctx).Model(&tenantPO{}).Where("is_deleted = false")

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

	var pos []tenantPO
	err := withCounts(query).
		Order("create_at DESC").
		Offset((filter.PageNum - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Find(&pos).Error
	if err != nil {
		return nil, 0, err
	}

	tenants := make([]*tenantdomain.Tenant, 0, len(pos))
	for i := range pos {
		tenants = append(tenants, toDomain(&pos[i]))
	}
	return tenants, total, nil
}

// ListAccessible 查询当前用户可访问的未删除租户；权限未启用时返回全部租户。
func (r *Repository) ListAccessible(ctx context.Context, filter tenantdomain.AccessibleFilter) ([]*tenantdomain.Tenant, error) {
	query := persistence.TxDB(ctx, r.db).WithContext(ctx).
		Table("tenant_info AS t").
		Select("DISTINCT t.*").
		Where("t.is_deleted = false")
	query = applyAccessiblePermissionFilter(query, filter)

	var pos []tenantPO
	if err := query.Order("t.create_at DESC").Scan(&pos).Error; err != nil {
		return nil, err
	}

	tenants := make([]*tenantdomain.Tenant, 0, len(pos))
	for i := range pos {
		tenants = append(tenants, toDomain(&pos[i]))
	}
	return tenants, nil
}

func applyAccessiblePermissionFilter(query *gorm.DB, filter tenantdomain.AccessibleFilter) *gorm.DB {
	// TODO(permission): 权限表落地后，在这里替换为用户可访问租户的权限 JOIN。
	// 当前阶段明确返回全部租户，不使用 user_info 提前限制查询范围。
	_ = filter.UserID
	return query
}

// withCounts 为租户查询附加有效组织数和成员数，避免列表逐条统计。
func withCounts(query *gorm.DB) *gorm.DB {
	return query.Select(`tenant_info.*,
        (SELECT COUNT(*) FROM organization_info AS o
         WHERE o.tenant_id = tenant_info.id AND o.is_deleted = false) AS org_count,
        (SELECT COUNT(*) FROM user_info AS u
         WHERE u.tenant_id = tenant_info.id AND u.is_deleted = false) AS member_count`)
}

// toPO 领域模型转持久化对象
func toPO(t *tenantdomain.Tenant) *tenantPO {
	return &tenantPO{
		ID:        t.ID,
		Code:      t.Code,
		Name:      t.Name,
		Remark:    t.Remark,
		Manager:   t.Manager,
		IsDeleted: t.IsDeleted,
		DeleteAt:  t.DeleteAt,
		DeleteBy:  t.DeleteBy,
		CreateBy:  t.CreateBy,
		UpdateBy:  t.UpdateBy,
		CreateAt:  t.CreateAt,
		UpdateAt:  t.UpdateAt,
	}
}

// toDomain 持久化对象转领域模型
func toDomain(po *tenantPO) *tenantdomain.Tenant {
	return &tenantdomain.Tenant{
		ID:          po.ID,
		Code:        po.Code,
		Name:        po.Name,
		Remark:      po.Remark,
		Manager:     po.Manager,
		OrgCount:    po.OrgCount,
		MemberCount: po.MemberCount,
		IsDeleted:   po.IsDeleted,
		DeleteAt:    po.DeleteAt,
		DeleteBy:    po.DeleteBy,
		CreateBy:    po.CreateBy,
		UpdateBy:    po.UpdateBy,
		CreateAt:    po.CreateAt,
		UpdateAt:    po.UpdateAt,
	}
}
