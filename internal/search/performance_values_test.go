package search_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"env-vault/internal/masterkey"
	"env-vault/internal/search"
)

// runPerfLargeValues 独立于百万行并发基线运行，核对真实解密与响应体积的成本
func runPerfLargeValues(t *testing.T, db *gorm.DB, manager *masterkey.Manager,
	call func(context.Context, perfCase, bool, int, []uuid.UUID) (float64, int, string), report *perfReport) {
	t.Helper()
	exec := func(statement string, args ...any) {
		t.Helper()
		if err := db.Exec(statement, args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	// 额外六个环境使用同一逻辑目录和组身份，保持一个组只有一个分页名额
	for env := 4; env < 10; env++ {
		code := fmt.Sprintf("extra%d", env)
		envID, parentID := perfID(fmt.Sprintf("large-env-%d", env)), perfID(fmt.Sprintf("large-parent-%d", env))
		exec("INSERT INTO environment_info(id,project_id,code,name,order_no) VALUES (?,?,?,?,?)", envID, perfID("project-0"), code, code, (env+1)*10)
		exec("INSERT INTO folder_info(id,group_id,env_id,code,name,type) VALUES (?,?,?,?,?,'common')", parentID, perfID("parent-group-0"), envID, "groups", "Groups")
		for folder := 0; folder < 4; folder++ {
			folderID := perfID(fmt.Sprintf("large-folder-%d-%d", env, folder))
			exec("INSERT INTO folder_info(id,group_id,env_id,parent_folder_id,code,name,type) VALUES (?,?,?,?,?,?,'customer')", folderID, perfID(fmt.Sprintf("folder-group-0-%d", folder)), envID, parentID, fmt.Sprintf("service-%d", folder), fmt.Sprintf("Service %d", folder))
			exec(`INSERT INTO secret_info(id,group_id,folder_id,env_code,key,remark,value_ciphertext,is_deleted)
				SELECT md5(s.id::text||?::text)::uuid,s.group_id,?::uuid,?::text,s.key,s.remark,s.value_ciphertext,s.is_deleted
				FROM secret_info s WHERE s.folder_id=?::uuid`, code, folderID, code, perfID(fmt.Sprintf("folder-0-%d-0", folder)))
		}
	}
	results := []map[string]any{}
	for _, valueBytes := range []int{16 * 1024, 64 * 1024} {
		cipher, err := manager.Encrypt(strings.Repeat("x", valueBytes))
		if err != nil {
			t.Fatal(err)
		}
		exec(`UPDATE secret_info s SET value_ciphertext=? FROM folder_info f JOIN environment_info e ON e.id=f.env_id WHERE s.folder_id=f.id AND e.project_id=?`, cipher, perfID("project-0"))
		exec("ANALYZE secret_info")
		for _, envCount := range []int{1, 4, 10} {
			for _, pageSize := range []int{20, 200} {
				var request search.Request
				request.Scopes = []search.Scope{{Type: "project", ID: perfID("project-0")}}
				request.Keyword = "SERVICE"
				request.PageNum, request.PageSize = 1, pageSize
				request.EnvList = []string{"dev"}
				if envCount >= 4 {
					request.EnvList = []string{"dev", "test", "sim", "prod"}
				}
				if envCount == 10 {
					for env := 4; env < 10; env++ {
						request.EnvList = append(request.EnvList, fmt.Sprintf("extra%d", env))
					}
				}
				scenario := perfCase{Name: "largeValue", Body: request}
				// 一次预热后采三次，结果标明小样本，不用于推导并发容量
				call(context.Background(), scenario, false, 0, nil)
				times, errors := []float64{}, map[string]int{}
				success, size := 0, 0
				started := time.Now()
				for sample := 0; sample < 3; sample++ {
					ms, n, failure := call(context.Background(), scenario, false, 0, nil)
					times, size = append(times, ms), size+n
					if failure == "" {
						success++
					} else {
						errors[failure]++
					}
				}
				stats := perfSummary(times, success, size, errors, time.Since(started))
				// 每个合成值都是 ASCII，响应至少包含完整的值字节数
				if success != 3 || stats.MeanBytes < valueBytes*envCount*pageSize {
					t.Errorf("large value response failed bytes=%d envs=%d pageSize=%d: %+v", valueBytes, envCount, pageSize, stats)
				}
				t.Logf("large value bytes=%d envs=%d pageSize=%d P95=%.1fms responseBytes=%d errors=%v", valueBytes, envCount, pageSize, stats.P95, stats.MeanBytes, stats.Errors)
				results = append(results, map[string]any{"valueBytes": valueBytes, "environments": envCount, "pageSize": pageSize, "http": stats})
			}
		}
	}
	report.Extras["largeValues"] = results
}
