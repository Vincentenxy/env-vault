package search

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// 显式开启时才生成大规模虚构数据，不连接业务库，不修改已有表
func TestPostgresSearchPerformance(t *testing.T) {
	if os.Getenv("ENV_VAULT_SEARCH_PERF") != "1" {
		t.Skip("set ENV_VAULT_SEARCH_PERF=1 to measure 100k/1m environment rows")
	}
	for _, size := range []int{100_000, 1_000_000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			db := testDatabase(t)
			f := testFixture(t, db)
			if err := db.Exec("CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public").Error; err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			// 同组四套环境共享 Key 和备注，少量关键词只命中一组
			err := db.Exec(`INSERT INTO secret_info(id,group_id,folder_id,env_code,key,remark)
				SELECT md5('row-'||n)::uuid,md5('group-'||((n-1)/4))::uuid,
					(ARRAY[?::uuid,?::uuid,?::uuid,?::uuid])[((n-1)%4)+1],
					(ARRAY['dev','test','sim','prod'])[((n-1)%4)+1],
					CASE WHEN (n-1)/4=42 THEN 'RARE_KEY_XYZ' ELSE 'SERVICE_'||((n-1)/4) END,
					CASE WHEN (n-1)/4%100=0 THEN '数据库连接配置' ELSE 'runtime configuration' END
				FROM generate_series(1,?) AS n`, f.folders[0], f.folders[1], f.folders[2], f.folders[3], size).Error
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("seed rows=%d elapsed=%s", size, time.Since(started))
			started = time.Now()
			for _, query := range []string{
				"CREATE INDEX search_key_trgm ON secret_info USING gin(key gin_trgm_ops) WHERE is_deleted=false",
				"CREATE INDEX search_remark_trgm ON secret_info USING gin(remark gin_trgm_ops) WHERE is_deleted=false",
				"ANALYZE secret_info",
				"ANALYZE folder_info",
				"ANALYZE environment_info",
				"ANALYZE project_info",
				"ANALYZE organization_info",
				"ANALYZE tenant_info",
			} {
				if err := db.Exec(query).Error; err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("create indexes elapsed=%s", time.Since(started))
			repo := &repository{db: db}
			for _, term := range []string{"RARE_KEY_XYZ", "数据库", "configuration", "does-not-exist"} {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				started = time.Now()
				result, err := repo.read(ctx, Input{Keyword: term, PageNum: 1, PageSize: 20, UserID: "benchmark"})
				cancel()
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("rows=%d term=%q total=%d groups=%d elapsed=%s", size, term, result.Total, len(result.Groups), time.Since(started))
			}
			// 同时验证真实 OR 查询能使用三元组索引，不通过禁用顺序扫描伪造执行计划
			query := candidates(db, Input{Keyword: "RARE_KEY_XYZ", UserID: "benchmark"}, true).Select("DISTINCT s.group_id")
			rows, err := db.Raw("EXPLAIN (ANALYZE, BUFFERS) SELECT count(*) FROM (?) AS matched", query).Rows()
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var plan []string
			for rows.Next() {
				var line string
				if err := rows.Scan(&line); err != nil {
					t.Fatal(err)
				}
				plan = append(plan, line)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(plan, "\n")
			if !strings.Contains(joined, "search_key_trgm") || !strings.Contains(joined, "search_remark_trgm") {
				t.Fatalf("selective query did not use both search indexes:\n%s", joined)
			}
			t.Logf("selective plan:\n%s", joined)
		})
	}
}
