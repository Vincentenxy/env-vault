package search_test

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"env-vault/internal/masterkey"
	"env-vault/internal/search"
)

// perfID 与 PostgreSQL md5(text)::uuid 保持一致，确保多轮测试使用相同数据分布
func perfID(value string) uuid.UUID { return uuid.UUID(md5.Sum([]byte(value))) }

// createPerfTables 直接复用已确认的表结构文档，只向专用测试 schema 建表
func createPerfTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", "design", "database.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"tenant_info", "organization_info", "user_info", "project_info", "project_user_relation",
		"environment_info", "folder_info", "secret_info", "tag_info", "secret_tag_relation", "audit_event_log"} {
		pattern := "(?s)```sql\\s*\\n(CREATE TABLE(?: IF NOT EXISTS)? " + regexp.QuoteMeta(table) + "\\s*\\(.*?)\\n```"
		match := regexp.MustCompile(pattern).FindSubmatch(source)
		if len(match) != 2 {
			t.Fatalf("missing documented DDL for %s", table)
		}
		if err := db.Exec(string(match[1])).Error; err != nil {
			t.Fatalf("create fixture table %s: %v", table, err)
		}
	}
}

// seedPerfHierarchy 构造两租户、八项目、每项目四个业务目录及四环境
func seedPerfHierarchy(t *testing.T, db *gorm.DB) {
	t.Helper()
	exec := func(sql string, args ...any) {
		t.Helper()
		if err := db.Exec(sql, args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	for tenant := 0; tenant < 2; tenant++ {
		exec("INSERT INTO tenant_info(id,code,name) VALUES (?,?,?)", perfID(fmt.Sprint("tenant-", tenant)), fmt.Sprint("tenant-", tenant), fmt.Sprint("Perf tenant ", tenant))
		exec("INSERT INTO organization_info(id,tenant_id,code,name) VALUES (?,?,?,?)", perfID(fmt.Sprint("org-", tenant)), perfID(fmt.Sprint("tenant-", tenant)), "engineering", "Engineering")
		for tag := 0; tag < 63; tag++ {
			exec("INSERT INTO tag_info(id,tenant_id,code,name,is_deleted) VALUES (?,?,?,?,?)", perfID(fmt.Sprintf("tag-%d-%d", tenant, tag)),
				perfID(fmt.Sprint("tenant-", tenant)), fmt.Sprintf("tag-%02d", tag), fmt.Sprintf("Perf tag %02d", tag), tag == 61)
		}
	}
	for project := 0; project < 8; project++ {
		exec("INSERT INTO project_info(id,org_id,code,name,is_deleted) VALUES (?,?,?,?,?)", perfID(fmt.Sprint("project-", project)),
			perfID(fmt.Sprint("org-", project/4)), fmt.Sprint("project-", project), fmt.Sprint("Perf project ", project), project == 7)
		for env, code := range []string{"dev", "test", "sim", "prod"} {
			envID := perfID(fmt.Sprintf("env-%d-%d", project, env))
			parentID := perfID(fmt.Sprintf("parent-%d-%d", project, env))
			exec("INSERT INTO environment_info(id,project_id,code,name,order_no) VALUES (?,?,?,?,?)", envID, perfID(fmt.Sprint("project-", project)), code, code, (env+1)*10)
			exec("INSERT INTO folder_info(id,group_id,env_id,code,name,type) VALUES (?,?,?,?,?,'common')", parentID, perfID(fmt.Sprintf("parent-group-%d", project)), envID, "groups", "Groups")
			for folder := 0; folder < 4; folder++ {
				exec("INSERT INTO folder_info(id,group_id,env_id,parent_folder_id,code,name,type,is_deleted) VALUES (?,?,?,?,?,?,'customer',?)",
					perfID(fmt.Sprintf("folder-%d-%d-%d", project, folder, env)), perfID(fmt.Sprintf("folder-group-%d-%d", project, folder)), envID,
					parentID, fmt.Sprintf("service-%d", folder), fmt.Sprintf("Service %d", folder), project == 6 && folder == 3)
			}
		}
	}
	exec("INSERT INTO user_info(id,user_id,nickname,username,tenant_id,org_id) VALUES (?,?,?,?,?,?)", perfID("user"), "search-perf-user", "Performance tester", "search-perf-user", perfID("tenant-0"), perfID("org-0"))
}

// seedPerfSecrets 将真实 AES 信封写入正式表结构，百万行使用小值，长值另设专项
func seedPerfSecrets(t *testing.T, db *gorm.DB, manager *masterkey.Manager, rows int) {
	t.Helper()
	if err := db.Exec("TRUNCATE secret_info, secret_tag_relation, audit_event_log").Error; err != nil {
		t.Fatal(err)
	}
	ciphers := make([]any, 4)
	for i := range ciphers {
		value, err := manager.Encrypt(fmt.Sprintf("synthetic-environment-%d-value", i))
		if err != nil {
			t.Fatal(err)
		}
		ciphers[i] = value
	}
	sql := `INSERT INTO secret_info(id,group_id,folder_id,env_code,key,remark,value_ciphertext,is_deleted)
		SELECT md5('row-'||n)::uuid,md5('group-'||g)::uuid,
			md5('folder-'||(g%8)||'-'||((g/8)%4)||'-'||e)::uuid,
			(ARRAY['dev','test','sim','prod'])[e+1],
			CASE WHEN g=42 THEN 'RARE_KEY_XYZ' WHEN g%100=1 THEN 'KEY_100%_READY!' ELSE 'SERVICE_'||g END,
			CASE WHEN g%100=0 THEN '数据库连接配置' ELSE 'runtime configuration' END,
			(ARRAY[?::text,?::text,?::text,?::text])[e+1],g%997=0
		FROM (SELECT n,(n-1)/4 AS g,(n-1)%4 AS e FROM generate_series(1,?::integer) AS n) AS generated`
	args := append(ciphers, rows)
	if err := db.Exec(sql, args...).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO secret_tag_relation(id,target_id,tag_id,target_type,is_deleted)
		SELECT md5('relation-primary-'||g)::uuid,md5('group-'||g)::uuid,
			md5('tag-'||((g%8)/4)||'-'||((g/8)%60))::uuid,'group',g%19=0
		FROM generate_series(0,?::integer-1) AS g WHERE g%5<>0`, rows/4).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO secret_tag_relation(id,target_id,tag_id,target_type)
		SELECT md5('relation-extra-'||g)::uuid,md5('group-'||g)::uuid,
			md5('tag-'||((g%8)/4)||'-60')::uuid,'group'
		FROM generate_series(0,?::integer-1) AS g WHERE g%7=0`, rows/4).Error; err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"secret_info", "secret_tag_relation", "tag_info", "folder_info", "environment_info", "project_info", "organization_info", "tenant_info", "user_info"} {
		if err := db.Exec("ANALYZE " + table).Error; err != nil {
			t.Fatal(err)
		}
	}
}

type perfCase struct {
	Name string
	Body search.Request
	Tags bool
}

// perfCases 覆盖文本、范围、环境、标签、真实深页和候选续页
func perfCases(rows int) []perfCase {
	request := func(keyword string, scopes []search.Scope, envs []string, tags []uuid.UUID, pageNum, pageSize int) search.Request {
		var req search.Request
		req.Keyword, req.Scopes, req.EnvList, req.TagIDList = keyword, scopes, envs, tags
		req.PageNum, req.PageSize = pageNum, pageSize
		return req
	}
	p0 := []search.Scope{{Type: "project", ID: perfID("project-0")}}
	global := func(keyword string) search.Request { return request(keyword, nil, nil, nil, 1, 20) }
	deep := 1000
	if rows == 10_000 {
		deep = 50
	} else if rows == 100_000 {
		deep = 500
	}
	return []perfCase{
		{Name: "rare", Body: global("RARE_KEY_XYZ")},
		{Name: "none", Body: global("does-not-exist")},
		{Name: "chinese", Body: global("数据库")},
		{Name: "frequent", Body: global("configuration")},
		{Name: "browse", Body: global("")},
		{Name: "literal", Body: global("100%")},
		{Name: "project", Body: request("SERVICE", p0, []string{"dev"}, nil, 1, 20)},
		{Name: "shortScoped", Body: request("SE", p0, []string{"dev", "test"}, nil, 1, 20)},
		{Name: "folder", Body: request("SERVICE", []search.Scope{{Type: "folder", ID: perfID("folder-group-0-0")}}, []string{"dev", "prod"}, nil, 1, 20)},
		{Name: "tenant", Body: request("SERVICE", []search.Scope{{Type: "tenant", ID: perfID("tenant-0")}}, nil, nil, 1, 20)},
		{Name: "multiProject", Body: request("SERVICE", []search.Scope{{Type: "project", ID: perfID("project-0")}, {Type: "project", ID: perfID("project-4")}}, nil, nil, 1, 20)},
		{Name: "singleTag", Body: request("", nil, nil, []uuid.UUID{perfID("tag-0-0")}, 1, 20)},
		{Name: "multiTag", Body: request("SERVICE", nil, nil, []uuid.UUID{perfID("tag-0-0"), perfID("tag-0-1")}, 1, 20)},
		{Name: "deepPage", Body: request("", nil, nil, nil, deep, 20)},
		{Name: "page100", Body: request("SERVICE", p0, []string{"dev"}, nil, 1, 100)},
		{Name: "page200", Body: request("SERVICE", p0, []string{"dev"}, nil, 1, 200)},
		{Name: "tagOptions", Body: global(""), Tags: true},
		{Name: "tagOptionsPage2", Body: request("", nil, nil, nil, 2, 50), Tags: true},
		{Name: "tagOptionsKeyword", Body: global("tag-01"), Tags: true},
	}
}

// perfExpected 使用独立的 Go 匹配逻辑计算期望结果，不复用被测 SQL
func perfExpected(rows int, scenario perfCase) (int, []uuid.UUID) {
	type match struct {
		id    uuid.UUID
		order []string
	}
	groups := []match{}
	tags := map[uuid.UUID][]string{}
	for g := 0; g < rows/4; g++ {
		p, f := g%8, (g/8)%4
		tenant := p / 4
		if g%997 == 0 || p == 7 || (p == 6 && f == 3) {
			continue
		}
		allowed := len(scenario.Body.Scopes) == 0
		for _, scope := range scenario.Body.Scopes {
			allowed = allowed || (scope.Type == "tenant" && scope.ID == perfID(fmt.Sprint("tenant-", tenant))) ||
				(scope.Type == "project" && scope.ID == perfID(fmt.Sprint("project-", p))) ||
				(scope.Type == "folder" && scope.ID == perfID(fmt.Sprintf("folder-group-%d-%d", p, f)))
		}
		if !allowed {
			continue
		}
		bound := []int{}
		if g%5 != 0 && g%19 != 0 {
			bound = append(bound, (g/8)%60)
		}
		if g%7 == 0 {
			bound = append(bound, 60)
		}
		if scenario.Tags {
			for _, tag := range bound {
				name, code := fmt.Sprintf("Perf tag %02d", tag), fmt.Sprintf("tag-%02d", tag)
				if strings.Contains(strings.ToLower(name), strings.ToLower(scenario.Body.Keyword)) || strings.Contains(code, strings.ToLower(scenario.Body.Keyword)) {
					id := perfID(fmt.Sprintf("tag-%d-%d", tenant, tag))
					tags[id] = []string{fmt.Sprintf("Perf tenant %d", tenant), name, code, id.String()}
				}
			}
			continue
		}
		if len(scenario.Body.TagIDList) > 0 {
			matchTag := false
			for _, tag := range bound {
				for _, requested := range scenario.Body.TagIDList {
					matchTag = matchTag || requested == perfID(fmt.Sprintf("tag-%d-%d", tenant, tag))
				}
			}
			if !matchTag {
				continue
			}
		}
		key, remark := fmt.Sprint("SERVICE_", g), "runtime configuration"
		if g == 42 {
			key = "RARE_KEY_XYZ"
		} else if g%100 == 1 {
			key = "KEY_100%_READY!"
		}
		if g%100 == 0 {
			remark = "数据库连接配置"
		}
		term := strings.ToLower(scenario.Body.Keyword)
		if !strings.Contains(strings.ToLower(key), term) && !strings.Contains(strings.ToLower(remark), term) {
			continue
		}
		id := perfID(fmt.Sprint("group-", g))
		order := []string{perfID(fmt.Sprint("tenant-", tenant)).String(), perfID(fmt.Sprint("org-", tenant)).String(),
			perfID(fmt.Sprint("project-", p)).String(), perfID(fmt.Sprintf("folder-group-%d-%d", p, f)).String(), key, id.String()}
		groups = append(groups, match{id: id, order: order})
	}
	if scenario.Tags {
		groups = nil
		for id, order := range tags {
			groups = append(groups, match{id: id, order: order})
		}
	}
	// 逐字段比较，避免拼接分隔符改变 SERVICE_1 与 SERVICE_10 的前缀顺序
	sort.Slice(groups, func(i, j int) bool {
		for field := range groups[i].order {
			if groups[i].order[field] != groups[j].order[field] {
				return groups[i].order[field] < groups[j].order[field]
			}
		}
		return false
	})
	total := len(groups)
	start := min(total, (scenario.Body.PageNum-1)*scenario.Body.PageSize)
	end := min(total, start+scenario.Body.PageSize)
	ids := make([]uuid.UUID, 0, end-start)
	for _, item := range groups[start:end] {
		ids = append(ids, item.id)
	}
	return total, ids
}
