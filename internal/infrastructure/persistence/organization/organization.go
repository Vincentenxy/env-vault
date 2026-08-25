// Package organization 组织仓储 PostgreSQL 实现（GORM）。
package organization

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	orgdomain "env-vault/internal/domain/organization"
)

// orgPO organization_info 表持久化对象（数据库列名下划线）
type orgPO struct {
	ID           uuid.UUID  `gorm:"column:id;primaryKey"`
	Code         string     `gorm:"column:code"`
	Name         string     `gorm:"column:name"`
	Remark       string     `gorm:"column:remark"`
	TenantID     uuid.UUID  `gorm:"column:tenant_id"`
	Manager      string     `gorm:"column:manager"`
	ProjectCount int64      `gorm:"column:project_count;->"`
	MemberCount  int64      `gorm:"column:member_count;->"`
	IsDeleted    bool       `gorm:"column:is_deleted"`
	DeleteAt     *time.Time `gorm:"column:delete_at"`
	DeleteBy     string     `gorm:"column:delete_by"`
	CreateBy     string     `gorm:"column:create_by"`
	UpdateBy     string     `gorm:"column:update_by"`
	CreateAt     time.Time  `gorm:"column:create_at"`
	UpdateAt     time.Time  `gorm:"column:update_at"`
}

type orgProjectRow struct {
	OrgID          uuid.UUID      `gorm:"column:org_id"`
	OrgName        string         `gorm:"column:org_name"`
	OrgManager     string         `gorm:"column:org_manager"`
	TenantID       uuid.UUID      `gorm:"column:tenant_id"`
	ProjectID      uuid.NullUUID  `gorm:"column:project_id"`
	ProjectName    sql.NullString `gorm:"column:project_name"`
	ProjectManager sql.NullString `gorm:"column:project_manager"`
}

// TableName 指定表名
func (orgPO) TableName() string {
	return "organization_info"
}

// Repository 组织仓储 GORM 实现
type Repository struct {
	db *gorm.DB
}

// NewRepository 创建组织仓储
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// Create 创建组织
func (r *Repository) Create(ctx context.Context, o *orgdomain.Organization) error {
	return r.db.WithContext(ctx).Create(toPO(o)).Error
}

// Update 更新组织（按 ID）
func (r *Repository) Update(ctx context.Context, o *orgdomain.Organization) error {
	po := toPO(o)
	return r.db.WithContext(ctx).
		Model(&orgPO{}).
		Where("id = ? AND is_deleted = false", po.ID).
		Updates(map[string]any{
			"name":      po.Name,
			"remark":    po.Remark,
			"manager":   po.Manager,
			"update_by": po.UpdateBy,
			"update_at": po.UpdateAt,
		}).Error
}

// Delete 软删除组织（按 ID）
func (r *Repository) Delete(ctx context.Context, id uuid.UUID, deleteBy string) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&orgPO{}).
		Where("id = ? AND is_deleted = false", id).
		Updates(map[string]any{
			"is_deleted": true,
			"delete_at":  now,
			"delete_by":  deleteBy,
			"update_at":  now,
			"update_by":  deleteBy,
		}).Error
}

// GetByID 按 ID 查询组织（不含已删除）
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*orgdomain.Organization, error) {
	var po orgPO
	err := withCounts(r.db.WithContext(ctx)).
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

// GetByTenantCode 按租户 + 编码查询组织（不含已删除）
func (r *Repository) GetByTenantCode(ctx context.Context, tenantID uuid.UUID, code string) (*orgdomain.Organization, error) {
	var po orgPO
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND code = ? AND is_deleted = false", tenantID, code).
		First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toDomain(&po), nil
}

// List 分页查询组织列表（不含已删除）
func (r *Repository) List(ctx context.Context, filter orgdomain.ListFilter) ([]*orgdomain.Organization, int64, error) {
	query := r.db.WithContext(ctx).Model(&orgPO{}).Where("is_deleted = false")

	if filter.TenantID != nil {
		query = query.Where("tenant_id = ?", *filter.TenantID)
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

	var pos []orgPO
	err := withCounts(query).
		Order("create_at DESC").
		Offset((filter.PageNum - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Find(&pos).Error
	if err != nil {
		return nil, 0, err
	}

	orgs := make([]*orgdomain.Organization, 0, len(pos))
	for i := range pos {
		orgs = append(orgs, toDomain(&pos[i]))
	}
	return orgs, total, nil
}

// ListWithProjects 查询全部未删除组织及其全部未删除项目。
func (r *Repository) ListWithProjects(ctx context.Context, filter orgdomain.WithProjectsFilter) ([]*orgdomain.OrganizationWithProjects, error) {
	query := r.db.WithContext(ctx).
		Table("organization_info AS o").
		Select("o.id AS org_id, o.name AS org_name, o.manager AS org_manager, o.tenant_id, p.id AS project_id, p.name AS project_name, p.manager AS project_manager").
		Joins("LEFT JOIN project_info AS p ON p.org_id = o.id AND p.is_deleted = false").
		Where("o.is_deleted = false")
	if len(filter.TenantIDs) > 0 {
		query = query.Where("o.tenant_id IN ?", filter.TenantIDs)
	}
	query = applyWithProjectsPermissionFilter(query, filter)

	var rows []orgProjectRow
	if err := query.
		Order("o.create_at DESC").
		Order("p.create_at DESC").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	result := make([]*orgdomain.OrganizationWithProjects, 0)
	orgIndexes := make(map[uuid.UUID]int)
	for _, row := range rows {
		index, exists := orgIndexes[row.OrgID]
		if !exists {
			index = len(result)
			orgIndexes[row.OrgID] = index
			result = append(result, &orgdomain.OrganizationWithProjects{
				ID:          row.OrgID,
				Name:        row.OrgName,
				TenantID:    row.TenantID,
				Manager:     row.OrgManager,
				ProjectList: make([]orgdomain.ProjectSummary, 0),
			})
		}
		if row.ProjectID.Valid {
			result[index].ProjectList = append(result[index].ProjectList, orgdomain.ProjectSummary{
				ID:      row.ProjectID.UUID,
				Name:    row.ProjectName.String,
				Manager: row.ProjectManager.String,
			})
		}
	}
	return result, nil
}

func applyWithProjectsPermissionFilter(query *gorm.DB, filter orgdomain.WithProjectsFilter) *gorm.DB {
	// TODO(permission): 权限表确定后，在这里按 filter.UserID 追加 JOIN/WHERE 条件。
	// 当前阶段明确返回所有组织及项目，不启用用户权限过滤。
	_ = filter.UserID
	return query
}

// withCounts 为普通组织查询附加有效项目数和成员数。
func withCounts(query *gorm.DB) *gorm.DB {
	return query.Select(`organization_info.*,
        (SELECT COUNT(*) FROM project_info AS p
         WHERE p.org_id = organization_info.id AND p.is_deleted = false) AS project_count,
        (SELECT COUNT(*) FROM user_info AS u
         WHERE u.org_id = organization_info.id AND u.is_deleted = false) AS member_count`)
}

// toPO 领域模型转持久化对象
func toPO(o *orgdomain.Organization) *orgPO {
	return &orgPO{
		ID:        o.ID,
		Code:      o.Code,
		Name:      o.Name,
		Remark:    o.Remark,
		TenantID:  o.TenantID,
		Manager:   o.Manager,
		IsDeleted: o.IsDeleted,
		DeleteAt:  o.DeleteAt,
		DeleteBy:  o.DeleteBy,
		CreateBy:  o.CreateBy,
		UpdateBy:  o.UpdateBy,
		CreateAt:  o.CreateAt,
		UpdateAt:  o.UpdateAt,
	}
}

// toDomain 持久化对象转领域模型
func toDomain(po *orgPO) *orgdomain.Organization {
	return &orgdomain.Organization{
		ID:           po.ID,
		Code:         po.Code,
		Name:         po.Name,
		Remark:       po.Remark,
		TenantID:     po.TenantID,
		Manager:      po.Manager,
		ProjectCount: po.ProjectCount,
		MemberCount:  po.MemberCount,
		IsDeleted:    po.IsDeleted,
		DeleteAt:     po.DeleteAt,
		DeleteBy:     po.DeleteBy,
		CreateBy:     po.CreateBy,
		UpdateBy:     po.UpdateBy,
		CreateAt:     po.CreateAt,
		UpdateAt:     po.UpdateAt,
	}
}
