package folder

import (
	"context"

	"github.com/google/uuid"

	folderdomain "env-vault/internal/domain/folder"
	"env-vault/internal/infrastructure/persistence"
)

// FindByEnvCode 只取两条即可识别编码歧义，避免目录读取随机选中同名的其他层级
func (r *Repository) FindByEnvCode(ctx context.Context, envID uuid.UUID, code string) ([]*folderdomain.Folder, error) {
	var rows []folderPO
	if err := persistence.TxDB(ctx, r.db).WithContext(ctx).
		Where("env_id = ? AND code = ? AND is_deleted = false", envID, code).
		Order("id ASC").Limit(2).Find(&rows).Error; err != nil {
		return nil, err
	}
	folders := make([]*folderdomain.Folder, 0, len(rows))
	for i := range rows {
		folders = append(folders, toDomain(&rows[i]))
	}
	return folders, nil
}
