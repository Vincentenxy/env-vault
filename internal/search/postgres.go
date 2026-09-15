package search

import (
	"context"
	"database/sql"
	"time"

	tagdomain "env-vault/internal/domain/tag"
	"env-vault/internal/infrastructure/persistence"
	"env-vault/pkg/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type repository struct {
	db   *gorm.DB
	tags TagReader
}

type groupIdentity struct {
	GroupID       uuid.UUID
	FolderGroupID uuid.UUID
	ProjectID     uuid.UUID
	OrgID         uuid.UUID
	TenantID      uuid.UUID
	Key           string
}

// valueRow 仅为本页详情查询投影，密文不能被序列化到响应或写入日志
type valueRow struct {
	GroupID, SecretID, EnvID         uuid.UUID
	EnvCode, EnvName                 string
	OrderNo                          float64
	Version                          int
	UpdateAt                         time.Time
	ValueCiphertext                  string
	Key, Remark                      string
	TenantID, OrgID, ProjectID       uuid.UUID
	TenantName, OrgName, ProjectName string
	FolderGroupID                    uuid.UUID
	FolderName, FolderCode           string
	ParentGroupID                    *uuid.UUID
	ParentName, ParentCode           *string
}

// read 使用同一只读快照，防止总数、当前页与标签分别读到不同版本
func (r *repository) read(ctx context.Context, in Input) (result storedPage, err error) {
	// GORM 错误日志可能打印绑定的检索文本，模块只使用安全的阶段耗时日志
	db := r.db.Session(&gorm.Session{Logger: r.db.Logger.LogMode(gormlogger.Silent)}).WithContext(ctx)
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := validateEnvironments(tx, in); err != nil {
			return err
		}
		started := time.Now()
		if err := tx.Table("(?) AS matched", candidates(tx, in, true).Select("DISTINCT s.group_id")).Count(&result.Total).Error; err != nil {
			return err
		}
		logStage(ctx, "count", started)
		if result.Total == 0 {
			return nil
		}
		var groups []groupIdentity
		started = time.Now()
		err := candidates(tx, in, true).
			Select("s.group_id, f.group_id AS folder_group_id, p.id AS project_id, o.id AS org_id, t.id AS tenant_id, MIN(s.key) AS key").
			Group("s.group_id, f.group_id, p.id, o.id, t.id").
			Order("t.id, o.id, p.id, f.group_id, MIN(s.key), s.group_id").
			Offset((in.PageNum - 1) * in.PageSize).Limit(in.PageSize).Scan(&groups).Error
		if err != nil {
			return err
		}
		logStage(ctx, "page", started)
		if len(groups) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, 0, len(groups))
		for _, group := range groups {
			ids = append(ids, group.GroupID)
		}
		var rows []valueRow
		started = time.Now()
		err = candidates(tx, in, false).Where("s.group_id IN ?", ids).
			Select(`s.group_id, s.id AS secret_id, e.id AS env_id, e.code AS env_code, e.name AS env_name, e.order_no,
				s.value_ciphertext, s.version, s.update_at, s.key, s.remark,
				t.id AS tenant_id, t.name AS tenant_name, o.id AS org_id, o.name AS org_name, p.id AS project_id, p.name AS project_name,
				f.group_id AS folder_group_id, f.name AS folder_name, f.code AS folder_code,
				parent.group_id AS parent_group_id, parent.name AS parent_name, parent.code AS parent_code`).
			Order("e.order_no, e.code, s.id").Scan(&rows).Error
		if err != nil {
			return err
		}
		logStage(ctx, "values", started)
		started = time.Now()
		tags := map[uuid.UUID][]tagdomain.Summary{}
		if r.tags != nil {
			tags, err = r.tags.ListGroupTags(persistence.WithTx(ctx, tx), ids)
			if err != nil {
				return err
			}
		}
		logStage(ctx, "tags", started)
		result.Groups = assembleGroups(groups, rows, tags)
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return result, err
}

// assembleGroups 按分页时的顺序组装，环境行再多也不会改变组数与页边界
func assembleGroups(groups []groupIdentity, rows []valueRow, tags map[uuid.UUID][]tagdomain.Summary) []storedGroup {
	byID := map[uuid.UUID]*storedGroup{}
	for _, row := range rows {
		group := byID[row.GroupID]
		if group == nil {
			folders := []FolderPath{}
			if row.ParentGroupID != nil {
				folders = append(folders, FolderPath{ID: *row.ParentGroupID, Name: *row.ParentName, Code: *row.ParentCode})
			}
			folders = append(folders, FolderPath{ID: row.FolderGroupID, Name: row.FolderName, Code: row.FolderCode})
			tagList := tags[row.GroupID]
			if tagList == nil {
				tagList = []tagdomain.Summary{}
			}
			group = &storedGroup{Secret: Secret{GroupID: row.GroupID, Key: row.Key, Remark: row.Remark, TagList: tagList,
				Scope: Location{Tenant: ResourceName{ID: row.TenantID, Name: row.TenantName},
					Organization: ResourceName{ID: row.OrgID, Name: row.OrgName}, Project: ResourceName{ID: row.ProjectID, Name: row.ProjectName}, Folders: folders}}}
			byID[row.GroupID] = group
		}
		group.Environments = append(group.Environments, storedValue{Ciphertext: row.ValueCiphertext, EnvironmentValue: EnvironmentValue{
			SecretID: row.SecretID, EnvID: row.EnvID, EnvCode: row.EnvCode, EnvName: row.EnvName,
			OrderNo: row.OrderNo, Version: row.Version, UpdateAt: row.UpdateAt}})
	}
	result := make([]storedGroup, 0, len(groups))
	for _, group := range groups {
		if value := byID[group.GroupID]; value != nil {
			result = append(result, *value)
		}
	}
	return result
}

func logStage(ctx context.Context, stage string, started time.Time) {
	logger.Debug(ctx, "secret search query", zap.String("stage", stage), zap.Duration("duration", time.Since(started)))
}
