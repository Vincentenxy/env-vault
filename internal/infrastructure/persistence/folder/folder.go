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
	GroupID        uuid.UUID  `gorm:"column:group_id"`
	Code           string     `gorm:"column:code"`
	Name           string     `gorm:"column:name"`
	EnvID          uuid.UUID  `gorm:"column:env_id"`
	ParentFolderID *uuid.UUID `gorm:"column:parent_folder_id"`
	Remark         string     `gorm:"column:remark"`
	Type           string     `gorm:"column:type"`
	Manager        string     `gorm:"column:manager"`
	SecretCount    int64      `gorm:"column:secret_count;->"`
	FolderCount    *int64     `gorm:"column:folder_count;->"`
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

// UpdateByGroupID 按 group_id 全环境同步更新 name/remark/manager。
func (r *Repository) UpdateByGroupID(ctx context.Context, groupID uuid.UUID, name, remark, manager, updateBy string, updateAt time.Time) (int64, error) {
	updates := map[string]any{
		"name":      name,
		"remark":    remark,
		"update_by": updateBy,
		"update_at": updateAt,
	}
	if manager != "" {
		updates["manager"] = manager
	}

	result := r.db.WithContext(ctx).
		Model(&folderPO{}).
		Where("group_id = ? AND is_deleted = false", groupID).
		Updates(updates)
	return result.RowsAffected, result.Error
}

// DeleteByGroupID 按 group_id 软删除全环境下的记录（所有层级）
func (r *Repository) DeleteByGroupID(ctx context.Context, groupID uuid.UUID, deleteBy string) (int64, error) {
	now := time.Now()
	result := r.db.WithContext(ctx).
		Model(&folderPO{}).
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

// GetByID 按 ID 查询文件夹（不含已删除）
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*folderdomain.Folder, error) {
	var po folderPO
	err := withStats(r.db.WithContext(ctx)).
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

// GetByGroupID 按 group_id 查询一条代表记录（不含已删除），不存在返回 nil, nil
// 代表记录选取：同 group_id 内 ORDER BY update_at DESC, create_at ASC 第一条
func (r *Repository) GetByGroupID(ctx context.Context, groupID uuid.UUID) (*folderdomain.Folder, error) {
	var po folderPO
	err := r.db.WithContext(ctx).
		Where("group_id = ? AND is_deleted = false", groupID).
		Order("update_at DESC, create_at ASC").
		First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return toDomain(&po), nil
}

// ListByGroupID 按 group_id 查询业务组下的全部环境实例（不含已删除）
func (r *Repository) ListByGroupID(ctx context.Context, groupID uuid.UUID) ([]*folderdomain.Folder, error) {
	var pos []folderPO
	err := r.db.WithContext(ctx).
		Where("group_id = ? AND is_deleted = false", groupID).
		Find(&pos).Error
	if err != nil {
		return nil, err
	}

	folders := make([]*folderdomain.Folder, 0, len(pos))
	for i := range pos {
		folders = append(folders, toDomain(&pos[i]))
	}
	return folders, nil
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

// ListTopGroupIDsByEnvIDs 列出指定环境集合下的顶级 folder 的全部 group_id（去重）
func (r *Repository) ListTopGroupIDsByEnvIDs(ctx context.Context, envIDs []uuid.UUID) ([]uuid.UUID, error) {
	var groupIDs []uuid.UUID
	err := r.db.WithContext(ctx).
		Model(&folderPO{}).
		Where("env_id IN ? AND parent_folder_id IS NULL AND is_deleted = false", envIDs).
		Distinct("group_id").
		Pluck("group_id", &groupIDs).Error
	if err != nil {
		return nil, err
	}
	return groupIDs, nil
}

// ListSubGroupIDsByParentFolderID 列出指定 parent folder 下的所有子 folder 的 group_id（去重）。
func (r *Repository) ListSubGroupIDsByParentFolderID(ctx context.Context, parentFolderID uuid.UUID) ([]uuid.UUID, error) {
	var subGroupIDs []uuid.UUID
	err := r.db.WithContext(ctx).Model(&folderPO{}).
		Where("parent_folder_id = ? AND is_deleted = false", parentFolderID).
		Distinct("group_id").
		Pluck("group_id", &subGroupIDs).Error
	if err != nil {
		return nil, err
	}
	return subGroupIDs, nil
}

// ListByGroupIDs 按 group_id 集合分页查询每 group_id 一条代表记录（屏蔽环境层级）。
// groupIDs 已由顶级或子级入口限定，此处不再按 parent_folder_id 限制目录层级。
//   - total = COUNT(DISTINCT group_id) （与 filter.Code / filter.Name 协同过滤）
//   - 分页作用于 group_id 集合
//   - 顺序：先按 code 字典序，每 code 内按 update_at DESC, create_at ASC 取代表
func (r *Repository) ListByGroupIDs(ctx context.Context, groupIDs []uuid.UUID, filter folderdomain.ListFilter) ([]*folderdomain.Folder, int64, error) {
	if len(groupIDs) == 0 {
		return []*folderdomain.Folder{}, 0, nil
	}

	// 1. total = COUNT(DISTINCT group_id) 应用过滤
	var total int64
	countQ := r.db.WithContext(ctx).Model(&folderPO{}).
		Where("group_id IN ? AND is_deleted = false", groupIDs)
	if filter.Code != "" {
		countQ = countQ.Where("code ILIKE ?", "%"+filter.Code+"%")
	}
	if filter.Name != "" {
		countQ = countQ.Where("name ILIKE ?", "%"+filter.Name+"%")
	}
	if err := countQ.Distinct("group_id").Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 2. 按 code 字典序取本页涉及的 code 子集（带分页 + 过滤）
	var codes []string
	codeQ := r.db.WithContext(ctx).Model(&folderPO{}).
		Select("DISTINCT code").
		Where("group_id IN ? AND is_deleted = false", groupIDs)
	if filter.Code != "" {
		codeQ = codeQ.Where("code ILIKE ?", "%"+filter.Code+"%")
	}
	if filter.Name != "" {
		codeQ = codeQ.Where("name ILIKE ?", "%"+filter.Name+"%")
	}
	if err := codeQ.
		Order("code ASC").
		Offset((filter.PageNum-1)*filter.PageSize).
		Limit(filter.PageSize).
		Pluck("code", &codes).Error; err != nil {
		return nil, 0, err
	}

	if len(codes) == 0 {
		return []*folderdomain.Folder{}, total, nil
	}

	// 3. 取这些 code 对应的 group_id（每 code 取一个；多条同 code 共享同一 group_id，自然只返回 1 个）
	var pageGroupIDs []uuid.UUID
	if err := r.db.WithContext(ctx).Model(&folderPO{}).
		Select("DISTINCT group_id").
		Where("group_id IN ? AND code IN ? AND is_deleted = false", groupIDs, codes).
		Pluck("group_id", &pageGroupIDs).Error; err != nil {
		return nil, 0, err
	}

	if len(pageGroupIDs) == 0 {
		return []*folderdomain.Folder{}, total, nil
	}

	// 3. 每 group_id 取代表记录：update_at DESC, create_at ASC 第一条，并按该环境记录统计子资源。
	// PostgreSQL DISTINCT ON 按 group_id 分组，与 ORDER BY 起始列匹配实现"每组一行"
	var pos []folderPO
	err := r.db.WithContext(ctx).Raw(
		`WITH representative AS (
             SELECT DISTINCT ON (group_id) *
             FROM folder_info
             WHERE group_id IN ? AND is_deleted = false
             ORDER BY group_id ASC, update_at DESC, create_at ASC
         )
         SELECT representative.*,
                (SELECT COUNT(*) FROM secret_info AS s
                 WHERE s.folder_id = representative.id AND s.is_deleted = false) AS secret_count,
                CASE WHEN representative.type = 'common' THEN
                    (SELECT COUNT(*) FROM folder_info AS child
                     WHERE child.parent_folder_id = representative.id AND child.is_deleted = false)
                ELSE NULL END AS folder_count
         FROM representative
         ORDER BY representative.group_id ASC`,
		pageGroupIDs,
	).Scan(&pos).Error
	if err != nil {
		return nil, 0, err
	}

	folders := make([]*folderdomain.Folder, 0, len(pos))
	for i := range pos {
		folders = append(folders, toDomain(&pos[i]))
	}
	return folders, total, nil
}

// withStats 按文件夹所在的单个环境统计有效密钥和直接子目录。
func withStats(query *gorm.DB) *gorm.DB {
	return query.Select(`folder_info.*,
        (SELECT COUNT(*) FROM secret_info AS s
         WHERE s.folder_id = folder_info.id AND s.is_deleted = false) AS secret_count,
        CASE WHEN folder_info.type = 'common' THEN
            (SELECT COUNT(*) FROM folder_info AS child
             WHERE child.parent_folder_id = folder_info.id AND child.is_deleted = false)
        ELSE NULL END AS folder_count`)
}

// toPO 领域模型转持久化对象
func toPO(f *folderdomain.Folder) *folderPO {
	return &folderPO{
		ID:             f.ID,
		GroupID:        f.GroupID,
		Code:           f.Code,
		Name:           f.Name,
		EnvID:          f.EnvID,
		ParentFolderID: f.ParentFolderID,
		Remark:         f.Remark,
		Type:           f.Type,
		Manager:        f.Manager,
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
		GroupID:        po.GroupID,
		Code:           po.Code,
		Name:           po.Name,
		EnvID:          po.EnvID,
		ParentFolderID: po.ParentFolderID,
		Remark:         po.Remark,
		Type:           po.Type,
		Manager:        po.Manager,
		SecretCount:    po.SecretCount,
		FolderCount:    po.FolderCount,
		IsDeleted:      po.IsDeleted,
		DeleteAt:       po.DeleteAt,
		DeleteBy:       po.DeleteBy,
		CreateBy:       po.CreateBy,
		UpdateBy:       po.UpdateBy,
		CreateAt:       po.CreateAt,
		UpdateAt:       po.UpdateAt,
	}
}
