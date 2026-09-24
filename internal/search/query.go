package search

import (
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// folderQuery 与当前目录模型一致，只允许一级目录及其二级目录
// 关联时检查所有祖先软删除和父目录环境，防止已删除目录下的密钥泄露到搜索
func folderQuery(db *gorm.DB) *gorm.DB {
	return db.Table("folder_info AS f").
		Joins("JOIN environment_info AS e ON e.id = f.env_id AND e.is_deleted = false").
		Joins("JOIN project_info AS p ON p.id = e.project_id AND p.is_deleted = false").
		Joins("JOIN organization_info AS o ON o.id = p.org_id AND o.is_deleted = false").
		Joins("JOIN tenant_info AS t ON t.id = o.tenant_id AND t.is_deleted = false").
		Joins("LEFT JOIN folder_info AS parent ON parent.id = f.parent_folder_id").
		Where("f.is_deleted = false").
		Where(`f.parent_folder_id IS NULL OR (parent.is_deleted = false
			AND parent.env_id = f.env_id AND parent.parent_folder_id IS NULL)`)
}

// candidates 不读取值，关键词留在有 GIN 索引的原始列上，不拼接两列或在 Go 中筛选
func candidates(db *gorm.DB, in Input, matchText bool) *gorm.DB {
	q := folderQuery(db).Joins("JOIN secret_info AS s ON s.folder_id = f.id AND s.is_deleted = false")
	q = applyScopes(q, in.Scopes)
	if len(in.EnvList) > 0 {
		q = q.Where("e.code IN ?", in.EnvList)
	}
	q = applyPermissionFilter(q, in.UserID)
	q = applyTagFilter(q, in.TagIDs)
	if matchText && in.Keyword != "" {
		pattern := containsPattern(in.Keyword)
		q = q.Where("(s.key ILIKE ? ESCAPE '!' OR s.remark ILIKE ? ESCAPE '!')", pattern, pattern)
	}
	return q
}

// applyTagFilter 使用 EXISTS 保持密钥环境行数量不变，多标签匹配任意一个
func applyTagFilter(q *gorm.DB, ids []uuid.UUID) *gorm.DB {
	if len(ids) == 0 {
		return q
	}
	return q.Where(`EXISTS (
		SELECT 1
		FROM secret_tag_relation AS search_relation
		JOIN tag_info AS search_tag ON search_tag.id = search_relation.tag_id
			AND search_tag.tenant_id = t.id AND search_tag.is_deleted = false
		WHERE search_relation.target_type = 'group'
			AND search_relation.target_id = s.group_id
			AND search_relation.is_deleted = false
			AND search_relation.tag_id IN ?
	)`, ids)
}

// applyScopes 将范围条件整体放在括号内，多个父子范围命中同一记录也不会增加行数
func applyScopes(q *gorm.DB, scopes []Scope) *gorm.DB {
	if len(scopes) == 0 {
		return q
	}
	ids := map[string][]uuid.UUID{}
	for _, scope := range scopes {
		ids[scope.Type] = append(ids[scope.Type], scope.ID)
	}
	conditions := []string{}
	args := []any{}
	for _, entry := range []struct{ kind, column string }{{"tenant", "t.id"}, {"org", "o.id"}, {"project", "p.id"}} {
		if len(ids[entry.kind]) > 0 {
			conditions = append(conditions, entry.column+" IN ?")
			args = append(args, ids[entry.kind])
		}
	}
	if len(ids["folder"]) > 0 {
		conditions = append(conditions, "f.group_id IN ? OR parent.group_id IN ?")
		args = append(args, ids["folder"], ids["folder"])
	}
	// 非空但无法识别的范围必须返回空，不能退化成无范围限制
	if len(conditions) == 0 {
		return q.Where("false")
	}
	return q.Where("("+strings.Join(conditions, " OR ")+")", args...)
}

// applyPermissionFilter 是资源权限唯一预留点，count、组分页和环境详情均经过此处
func applyPermissionFilter(q *gorm.DB, userID string) *gorm.DB {
	if strings.TrimSpace(userID) == "" {
		return q.Where("false")
	}
	// TODO(permission): 接入统一权限中心，当前与 secret/list 一样仅要求认证
	// 后续环境权限应与已有范围取交集，禁止空授权集合表示无限制
	return q
}

// validateEnvironments 根据真实资源归属判断能否共用环境，不能信任前端路径
func validateEnvironments(db *gorm.DB, in Input) error {
	projectIDs, folderIDs := []uuid.UUID{}, []uuid.UUID{}
	broadScope := false
	for _, scope := range in.Scopes {
		switch scope.Type {
		case "project":
			projectIDs = append(projectIDs, scope.ID)
		case "folder":
			folderIDs = append(folderIDs, scope.ID)
		default:
			broadScope = true
		}
	}
	if len(in.Scopes) == 0 {
		return nil
	}
	projects := map[uuid.UUID]bool{}
	if len(projectIDs) > 0 {
		var found []uuid.UUID
		err := db.Table("project_info AS p").
			Joins("JOIN organization_info AS o ON o.id = p.org_id AND o.is_deleted = false").
			Joins("JOIN tenant_info AS t ON t.id = o.tenant_id AND t.is_deleted = false").
			Where("p.id IN ? AND p.is_deleted = false", projectIDs).Pluck("p.id", &found).Error
		if err != nil {
			return err
		}
		if len(found) != len(projectIDs) {
			return ErrScope
		}
		for _, id := range found {
			projects[id] = true
		}
	}
	if len(folderIDs) > 0 {
		var found []struct{ GroupID, ProjectID uuid.UUID }
		err := folderQuery(db).Select("DISTINCT f.group_id, e.project_id").Where("f.group_id IN ?", folderIDs).Scan(&found).Error
		if err != nil {
			return err
		}
		if len(found) != len(folderIDs) {
			return ErrScope
		}
		for _, folder := range found {
			projects[folder.ProjectID] = true
		}
	}
	if broadScope || len(projects) != 1 {
		if len(in.EnvList) > 0 {
			return ErrEnvironment
		}
		return nil
	}
	if len(in.EnvList) == 0 {
		return ErrEnvironment
	}
	var projectID uuid.UUID
	for id := range projects {
		projectID = id
	}
	var count int64
	if err := db.Table("environment_info").Where("project_id = ? AND code IN ? AND is_deleted = false", projectID, in.EnvList).
		Distinct("code").Count(&count).Error; err != nil {
		return err
	}
	if count != int64(len(in.EnvList)) {
		return ErrEnvironment
	}
	return nil
}
