// Package folder 文件夹仓储 PostgreSQL 实现（GORM）。
package folder

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	folderdomain "env-vault/internal/domain/folder"
)

// folderPO folder_info 表持久化对象（数据库列名下划线）
type folderPO struct {
	ID             uuid.UUID  `gorm:"column:id;primaryKey"`
	Code           string     `gorm:"column:code"`
	Name           string     `gorm:"column:name"`
	EnvID          uuid.UUID  `gorm:"column:env_id"`
	ParentFolderID *uuid.UUID `gorm:"column:parent_folder_id"`
	Remark         string     `gorm:"column:remark"`
	Type           string     `gorm:"column:type"`
	IsDeleted      bool       `gorm:"column:is_deleted"`
	DeleteAt       *time.Time `gorm:"column:delete_at"`
	DeleteBy       string     `gorm:"column:delete_by"`
	CreateBy       string     `gorm:"column:create_by"`
	UpdateBy       string     `gorm:"column:update_by"`
	CreateAt       time.Time  `gorm:"column:create_at"`
	UpdateAt       time.Time  `gorm:"column:update_at"`
}

// TableName 指定表名
func (folderPO) TableName() string {
	return "folder_info"
}

// Repository 文件夹仓储 GORM 实现
type Repository struct {
	db *gorm.DB
}

// NewRepository 创建文件夹仓储
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// CreateBatch 批量创建文件夹（GORM 对 slice 生成单条多行 INSERT，天然原子）
func (r *Repository) CreateBatch(ctx context.Context, folders []*folderdomain.Folder) error {
	if len(folders) == 0 {
		return nil
	}
	pos := make([]*folderPO, 0, len(folders))
	for _, f := range folders {
		pos = append(pos, toPO(f))
	}
	return r.db.WithContext(ctx).Create(&pos).Error
}

// UpdateByIDs 按 ID 集合批量更新 name/remark
func (r *Repository) UpdateByIDs(ctx context.Context, ids []uuid.UUID, name, remark, updateBy string, updateAt time.Time) (int64, error) {
	result := r.db.WithContext(ctx).
		Model(&folderPO{}).
		Where("id IN ? AND is_deleted = false", ids).
		Updates(map[string]any{
			"name":      name,
			"remark":    remark,
			"update_by": updateBy,
			"update_at": updateAt,
		})
	return result.RowsAffected, result.Error
}

// DeleteByEnvIDsCode 软删除指定环境集合下指定编码的文件夹（所有层级）
func (r *Repository) DeleteByEnvIDsCode(ctx context.Context, envIDs []uuid.UUID, code, deleteBy string) (int64, error) {
	now := time.Now()
	result := r.db.WithContext(ctx).
		Model(&folderPO{}).
		Where("env_id IN ? AND code = ? AND is_deleted = false", envIDs, code).
		Updates(map[string]any{
			"is_deleted": true,
			"delete_at":  now,
			"delete_by":  deleteBy,
			"update_at":  now,
			"update_by":  deleteBy,
		})
	return result.RowsAffected, result.Error
}

// GetByID 按 ID 查询文件夹（不含已删除）
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*folderdomain.Folder, error) {
	var po folderPO
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

// GetByEnvIDsCode 按环境集合 + 编码查询文件夹（不含已删除），不存在返回 nil, nil
func (r *Repository) GetByEnvIDsCode(ctx context.Context, envIDs []uuid.UUID, code string) (*folderdomain.Folder, error) {
	var po folderPO
	err := r.db.WithContext(ctx).
		Where("env_id IN ? AND code = ? AND is_deleted = false", envIDs, code).
		First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toDomain(&po), nil
}

// GetByEnvCode 按环境 + 编码查询文件夹（不含已删除），不存在返回 nil, nil
func (r *Repository) GetByEnvCode(ctx context.Context, envID uuid.UUID, code string) (*folderdomain.Folder, error) {
	var po folderPO
	err := r.db.WithContext(ctx).
		Where("env_id = ? AND code = ? AND is_deleted = false", envID, code).
		First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toDomain(&po), nil
}

// GetByParentCode 按父文件夹 + 编码查询文件夹（不含已删除），不存在返回 nil, nil
func (r *Repository) GetByParentCode(ctx context.Context, parentID uuid.UUID, code string) (*folderdomain.Folder, error) {
	var po folderPO
	err := r.db.WithContext(ctx).
		Where("parent_folder_id = ? AND code = ? AND is_deleted = false", parentID, code).
		First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toDomain(&po), nil
}

// ListTopByEnvIDs 分页查询环境集合下的顶级文件夹列表（不含已删除）
func (r *Repository) ListTopByEnvIDs(ctx context.Context, envIDs []uuid.UUID, filter folderdomain.ListFilter) ([]*folderdomain.Folder, int64, error) {
	query := r.db.WithContext(ctx).
		Model(&folderPO{}).
		Where("env_id IN ? AND parent_folder_id IS NULL AND is_deleted = false", envIDs)

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

	var pos []folderPO
	err := query.
		Order("create_at DESC").
		Offset((filter.PageNum - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Find(&pos).Error
	if err != nil {
		return nil, 0, err
	}

	folders := make([]*folderdomain.Folder, 0, len(pos))
	for i := range pos {
		folders = append(folders, toDomain(&pos[i]))
	}
	return folders, total, nil
}

// toPO 领域模型转持久化对象
func toPO(f *folderdomain.Folder) *folderPO {
	return &folderPO{
		ID:             f.ID,
		Code:           f.Code,
		Name:           f.Name,
		EnvID:          f.EnvID,
		ParentFolderID: f.ParentFolderID,
		Remark:         f.Remark,
		Type:           f.Type,
		IsDeleted:      f.IsDeleted,
		DeleteAt:       f.DeleteAt,
		DeleteBy:       f.DeleteBy,
		CreateBy:       f.CreateBy,
		UpdateBy:       f.UpdateBy,
		CreateAt:       f.CreateAt,
		UpdateAt:       f.UpdateAt,
	}
}

// toDomain 持久化对象转领域模型
func toDomain(po *folderPO) *folderdomain.Folder {
	return &folderdomain.Folder{
		ID:             po.ID,
		Code:           po.Code,
		Name:           po.Name,
		EnvID:          po.EnvID,
		ParentFolderID: po.ParentFolderID,
		Remark:         po.Remark,
		Type:           po.Type,
		IsDeleted:      po.IsDeleted,
		DeleteAt:       po.DeleteAt,
		DeleteBy:       po.DeleteBy,
		CreateBy:       po.CreateBy,
		UpdateBy:       po.UpdateBy,
		CreateAt:       po.CreateAt,
		UpdateAt:       po.UpdateAt,
	}
}
