package search_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"

	infraauth "env-vault/internal/infrastructure/auth"
	"env-vault/internal/infrastructure/config"
	"env-vault/internal/interfaces/router"
	"env-vault/internal/masterkey"
	"env-vault/internal/search"
	"env-vault/pkg/page"
)

// perfTrace 只观察测试实例的 SQL，不改变仓储行为或输出请求中的密文
type perfTrace struct {
	mu      sync.Mutex
	enabled bool
	times   map[string][]float64
	plans   map[string]string
}

func (p *perfTrace) LogMode(gormlog.LogLevel) gormlog.Interface { return p }
func (*perfTrace) Info(context.Context, string, ...any)         {}
func (*perfTrace) Warn(context.Context, string, ...any)         {}
func (*perfTrace) Error(context.Context, string, ...any)        {}
func (p *perfTrace) Trace(_ context.Context, started time.Time, fc func() (string, int64), _ error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.enabled {
		return
	}
	sqlText, _ := fc()
	lower := strings.ToLower(sqlText)
	phase := "other"
	switch {
	case strings.HasPrefix(lower, "insert into \"audit_event_log\""):
		phase = "audit"
	case strings.Contains(lower, "value_ciphertext"):
		phase = "values"
	case strings.Contains(lower, "count(") && strings.Contains(lower, "secret_info"):
		phase = "count"
	case strings.Contains(lower, "count("):
		phase = "validation"
	case strings.Contains(lower, "min(s.key)"):
		phase = "groupPage"
	case strings.Contains(lower, "secret_tag_relation"):
		phase = "tags"
	case strings.Contains(lower, "user_info"):
		phase = "user"
	}
	p.times[phase] = append(p.times[phase], float64(time.Since(started).Microseconds())/1000)
	if phase == "count" || phase == "groupPage" || phase == "values" || phase == "tags" {
		if _, exists := p.plans[phase]; !exists {
			p.plans[phase] = sqlText
		}
	}
}

type perfStats struct {
	Samples   int            `json:"samples"`
	Success   int            `json:"success"`
	Errors    map[string]int `json:"errors,omitempty"`
	P50       float64        `json:"p50Ms"`
	P95       float64        `json:"p95Ms"`
	P99       float64        `json:"p99Ms"`
	MeanBytes int            `json:"meanResponseBytes"`
	Elapsed   float64        `json:"elapsedSeconds"`
	RPS       float64        `json:"requestsPerSecond"`
}

// perfSummary 使用最近秩计算分位值，保留实际样本量，不做插值或虚构 P95
func perfSummary(times []float64, success, size int, errors map[string]int, elapsed time.Duration) perfStats {
	sort.Float64s(times)
	percentile := func(q float64) float64 {
		if len(times) == 0 {
			return 0
		}
		return times[max(0, int(math.Ceil(float64(len(times))*q))-1)]
	}
	result := perfStats{Samples: len(times), Success: success, Errors: errors,
		P50: percentile(.5), P95: percentile(.95), P99: percentile(.99), Elapsed: elapsed.Seconds()}
	if len(times) > 0 {
		result.MeanBytes = size / len(times)
	}
	if elapsed > 0 {
		result.RPS = float64(len(times)) / elapsed.Seconds()
	}
	return result
}

type perfEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type perfRun struct {
	Name          string               `json:"name"`
	Stats         perfStats            `json:"http"`
	ExpectedTotal int                  `json:"expectedTotal"`
	PageNum       int                  `json:"pageNum"`
	PageSize      int                  `json:"pageSize"`
	SQL           map[string]perfStats `json:"sqlPhases,omitempty"`
}

type perfScale struct {
	Rows   int       `json:"environmentRows"`
	Groups int       `json:"logicalGroups"`
	NoGIN  perfStats `json:"rareWithoutGIN"`
	Runs   []perfRun `json:"runs"`
}

type perfLoad struct {
	Concurrency    int                  `json:"concurrency"`
	WarmupSeconds  int                  `json:"warmupSeconds"`
	MeasureSeconds int                  `json:"measureSeconds"`
	HTTP           perfStats            `json:"http"`
	Cases          map[string]perfStats `json:"cases"`
	Pool           sql.DBStats          `json:"pool"`
	HeapBytes      uint64               `json:"heapBytes"`
	GCPauseNs      uint64               `json:"gcPauseNs"`
}

type perfReport struct {
	Started  time.Time      `json:"started"`
	Go       string         `json:"goVersion"`
	Postgres string         `json:"postgres"`
	Schema   string         `json:"schema"`
	Scales   []perfScale    `json:"scales"`
	Load     []perfLoad     `json:"load"`
	Extras   map[string]any `json:"extras"`
	Finished time.Time      `json:"finished"`
}

// TestSearchPerformanceHTTP 显式开启后才运行，只允许连接本机专用性能数据库
func TestSearchPerformanceHTTP(t *testing.T) {
	if os.Getenv("ENV_VAULT_SEARCH_HTTP_PERF") != "1" {
		t.Skip("set ENV_VAULT_SEARCH_HTTP_PERF=1 with a dedicated database")
	}
	connection, err := pgx.ParseConfig(os.Getenv("ENV_VAULT_SEARCH_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	if connection.Database != "env_vault_search_perf" || (connection.Host != "127.0.0.1" && connection.Host != "localhost") {
		t.Fatal("HTTP performance test requires loopback and database env_vault_search_perf")
	}
	adminPool := stdlib.OpenDB(*connection)
	defer adminPool.Close()
	schema := "search_perf_http_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminPool.Exec("CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	if _, err := adminPool.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := adminPool.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Errorf("cleanup schema: %v", err)
		}
	}()
	connection.RuntimeParams["search_path"] = schema + ",public"
	pool := stdlib.OpenDB(*connection)
	pool.SetMaxOpenConns(25)
	pool.SetMaxIdleConns(5)
	defer pool.Close()
	trace := &perfTrace{}
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{Logger: trace})
	if err != nil {
		t.Fatal(err)
	}
	createPerfTables(t, db)
	seedPerfHierarchy(t, db)
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	keyBase64 := base64.StdEncoding.EncodeToString(key)
	manager := masterkey.NewManager()
	if err := manager.LoadConfigFallback(true, keyBase64); err != nil {
		t.Fatal(err)
	}
	privatePEM, publicPEM, err := infraauth.GenerateRSAKeyPair(2048)
	if err != nil {
		t.Fatal(err)
	}
	private, err := infraauth.ParseRSAPrivateKey(privatePEM)
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := infraauth.NewJWTIssuer(private, "search-performance", "search-performance-web", "test-key", 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := issuer.Issue("search-perf-user", "Performance tester")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Server: config.ServerConfig{Mode: gin.TestMode},
		Auth:   config.AuthConfig{Local: config.LocalAuthConfig{Enabled: true, Issuer: "search-performance", Audience: "search-performance-web", KeyID: "test-key", PrivateKey: string(privatePEM), PublicKey: string(publicPEM), AccessTokenTTL: 2 * time.Hour}},
		Security: config.SecurityConfig{AllowConfigKeyFallback: true, EncryptionKey: keyBase64, ReadyAllowlist: []config.ReadyAllowRouteConfig{
			{Method: "POST", Path: "/api/v1/pub/auth/login"}, {Method: "GET", Path: "/api/v1/masterKey/status"}, {Method: "POST", Path: "/api/v1/masterKey/share"},
		}},
		Redis: config.RedisConfig{KeyPrefix: schema},
	}
	redisClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:26379"})
	defer redisClient.Close()
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		t.Fatal("dedicated Redis on port 26379 required: ", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine, err := router.New(ctx, cfg, db, redisClient)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(engine)
	if addr := os.Getenv("ENV_VAULT_SEARCH_PERF_HTTP_ADDR"); addr != "" {
		if !strings.HasPrefix(addr, "127.0.0.1:") {
			t.Fatal("test HTTP address must use loopback")
		}
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		server.Listener.Close()
		server.Listener = listener
	}
	server.Start()
	defer server.Close()
	transport := &http.Transport{MaxIdleConns: 50, MaxIdleConnsPerHost: 30, MaxConnsPerHost: 30}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second}
	artifact := os.Getenv("ENV_VAULT_SEARCH_PERF_OUTPUT")
	if artifact == "" {
		artifact = filepath.Join("..", "..", ".local", "search-performance", "latest")
	}
	if err := os.MkdirAll(artifact, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, value any) {
		encoded, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(artifact, name), encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
	report := perfReport{Started: time.Now(), Go: runtime.Version(), Schema: schema, Extras: map[string]any{}}
	if err := db.Raw("SELECT version()").Scan(&report.Postgres).Error; err != nil {
		t.Fatal(err)
	}
	defer func() { report.Finished = time.Now(); write("http-results.json", report) }()
	write("runtime.json", map[string]any{"url": server.URL, "token": token, "schema": schema})
	call := func(ctx context.Context, scenario perfCase, validate bool, expected int, ids []uuid.UUID) (float64, int, string) {
		payload := any(scenario.Body)
		path := "/api/v1/secret/search"
		if scenario.Tags {
			path += "/tag/list"
			payload = search.TagListRequest{Scopes: scenario.Body.Scopes, EnvList: scenario.Body.EnvList, Keyword: scenario.Body.Keyword, Request: scenario.Body.Request}
		}
		encoded, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, "POST", server.URL+path, bytes.NewReader(encoded))
		if err != nil {
			return 0, 0, "build_request"
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		started := time.Now()
		response, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return float64(time.Since(started).Microseconds()) / 1000, 0, "cancelled"
			}
			return float64(time.Since(started).Microseconds()) / 1000, 0, "transport"
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		elapsed := float64(time.Since(started).Microseconds()) / 1000
		if readErr != nil {
			return elapsed, len(body), "read_body"
		}
		if response.StatusCode != 200 {
			return elapsed, len(body), fmt.Sprint("http_", response.StatusCode)
		}
		var envelope perfEnvelope
		if json.Unmarshal(body, &envelope) != nil {
			return elapsed, len(body), "invalid_json"
		}
		if envelope.Code != 0 {
			if envelope.Msg == search.ErrTimeout.Error() {
				return elapsed, len(body), "query_timeout"
			}
			return elapsed, len(body), fmt.Sprintf("code_%d_%s", envelope.Code, envelope.Msg)
		}
		if validate {
			if err := validatePerfData(envelope.Data, scenario, expected, ids); err != nil {
				return elapsed, len(body), "correctness_" + err.Error()
			}
		}
		return elapsed, len(body), ""
	}
	sample := func(scenario perfCase, rows, n int, traceSQL bool) (perfStats, map[string]perfStats) {
		expected, ids := perfExpected(rows, scenario)
		trace.mu.Lock()
		trace.enabled, trace.times, trace.plans = traceSQL, map[string][]float64{}, map[string]string{}
		trace.mu.Unlock()
		times, errors := []float64{}, map[string]int{}
		success, size := 0, 0
		started := time.Now()
		for i := 0; i < n; i++ {
			ms, bytes, failure := call(context.Background(), scenario, true, expected, ids)
			times, size = append(times, ms), size+bytes
			if failure == "" {
				success++
			} else {
				errors[failure]++
			}
		}
		trace.mu.Lock()
		trace.enabled = false
		phases := map[string]perfStats{}
		for phase, times := range trace.times {
			phases[phase] = perfSummary(times, len(times), 0, nil, 0)
		}
		plans := trace.plans
		trace.mu.Unlock()
		if traceSQL {
			for phase, statement := range plans {
				var lines []string
				if err := db.Raw("EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) " + statement).Scan(&lines).Error; err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(artifact, fmt.Sprintf("plan-%d-%s-%s.txt", rows, scenario.Name, phase)), []byte(strings.Join(lines, "\n")), 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
		stats := perfSummary(times, success, size, errors, time.Since(started))
		for failure := range errors {
			if failure != "query_timeout" {
				t.Errorf("rows=%d scenario=%s failure=%s count=%d", rows, scenario.Name, failure, errors[failure])
			}
		}
		return stats, phases
	}
	scales := []int{10_000, 100_000, 1_000_000}
	if os.Getenv("ENV_VAULT_SEARCH_PERF_SMOKE") == "1" {
		scales = []int{10_000}
	}
	for _, rows := range scales {
		t.Logf("seeding rows=%d", rows)
		for _, index := range []string{"perf_key_trgm", "perf_remark_trgm"} {
			if err := db.Exec("DROP INDEX IF EXISTS " + index).Error; err != nil {
				t.Fatal(err)
			}
		}
		seedPerfSecrets(t, db, manager, rows)
		scale := perfScale{Rows: rows, Groups: rows / 4}
		scale.NoGIN, _ = sample(perfCases(rows)[0], rows, 3, false)
		if err := db.Exec("CREATE INDEX perf_key_trgm ON secret_info USING gin(key gin_trgm_ops) WHERE is_deleted=false").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("CREATE INDEX perf_remark_trgm ON secret_info USING gin(remark gin_trgm_ops) WHERE is_deleted=false").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("ANALYZE secret_info").Error; err != nil {
			t.Fatal(err)
		}
		for _, scenario := range perfCases(rows) {
			expected, _ := perfExpected(rows, scenario)
			stats, phases := sample(scenario, rows, 10, true)
			scale.Runs = append(scale.Runs, perfRun{Name: scenario.Name, Stats: stats, ExpectedTotal: expected, PageNum: scenario.Body.PageNum, PageSize: scenario.Body.PageSize, SQL: phases})
			t.Logf("rows=%d scenario=%s n=%d total=%d P50=%.1fms P95=%.1fms errors=%v", rows, scenario.Name, stats.Samples, expected, stats.P50, stats.P95, stats.Errors)
			write("http-results.json", report)
		}
		report.Scales = append(report.Scales, scale)
		write("http-results.json", report)
	}
	if os.Getenv("ENV_VAULT_SEARCH_PERF_SMOKE") != "1" && !t.Failed() {
		// 混合负载包含常规查询和高命中率查询，按固定比例轮换以便复现
		cases := perfCases(1_000_000)
		mix := []perfCase{cases[0], cases[0], cases[0], cases[0], cases[6], cases[6], cases[11], cases[16], cases[3], cases[4]}
		report.Extras["loadMix"] = map[string]int{"rare": 40, "project": 20, "singleTag": 10, "tagOptions": 10, "frequent": 10, "browse": 10}
		for _, concurrency := range []int{1, 5, 10, 20} {
			t.Logf("load concurrency=%d warmup=30s measurement=180s", concurrency)
			runPerfLoad(call, mix, concurrency, 30*time.Second)
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			httpStats, perCase := runPerfLoad(call, mix, concurrency, 180*time.Second)
			runtime.ReadMemStats(&after)
			report.Load = append(report.Load, perfLoad{Concurrency: concurrency, WarmupSeconds: 30, MeasureSeconds: 180, HTTP: httpStats, Cases: perCase, Pool: pool.Stats(), HeapBytes: after.HeapAlloc, GCPauseNs: after.PauseTotalNs - before.PauseTotalNs})
			write("http-results.json", report)
			t.Logf("load concurrency=%d n=%d P95=%.1fms RPS=%.2f errors=%v", concurrency, httpStats.Samples, httpStats.P95, httpStats.RPS, httpStats.Errors)
			for failure, count := range httpStats.Errors {
				if failure != "query_timeout" {
					t.Errorf("unexpected load error %s: %d", failure, count)
				}
			}
			// 只要出现超时就停止继续加压，容量边界保留在报告中
			if len(httpStats.Errors) > 0 {
				report.Extras["stoppedAtConcurrency"] = concurrency
				break
			}
		}
		// 主动取消重查询后检查连接和查询是否能恢复，不把取消算成普通错误
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		_, _, failure := call(ctx, cases[4], false, 0, nil)
		cancel()
		var active int64
		if err := db.Raw("SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND state='active' AND pid<>pg_backend_pid() AND query NOT LIKE '%pg_stat_activity%'").Scan(&active).Error; err != nil {
			t.Fatal(err)
		}
		stats, _ := sample(cases[0], 1_000_000, 3, false)
		report.Extras["cancellation"] = map[string]any{"clientResult": failure, "otherActiveImmediately": active, "recovery": stats}
		if stats.Success != stats.Samples {
			t.Error("search did not recover after cancellation")
		}
	}
	if os.Getenv("ENV_VAULT_SEARCH_PERF_LARGE_VALUES") == "1" && !t.Failed() {
		runPerfLargeValues(t, db, manager, call, &report)
	}
	var auditCount int64
	if err := db.Table("audit_event_log").Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	report.Extras["auditRows"] = auditCount
	write("http-results.json", report)
	// 可选等待仅供同一测试会话中的真实浏览器验证，移除标记后自动释放 schema
	if os.Getenv("ENV_VAULT_SEARCH_PERF_HOLD") == "1" && !t.Failed() {
		hold := filepath.Join(artifact, "hold")
		if err := os.WriteFile(hold, []byte("remove to finish browser verification"), 0600); err != nil {
			t.Fatal(err)
		}
		t.Log("HTTP fixture available for browser verification")
		for {
			if _, err := os.Stat(hold); os.IsNotExist(err) {
				break
			}
			time.Sleep(time.Second)
		}
	}
}

// validatePerfData 检查独立计算的总数、组排序、跨环境值以及候选标签身份
func validatePerfData(data json.RawMessage, scenario perfCase, expected int, ids []uuid.UUID) error {
	if scenario.Tags {
		var result page.Response[search.TagOption]
		if err := json.Unmarshal(data, &result); err != nil {
			return err
		}
		if result.Total != int64(expected) || len(result.List) != len(ids) {
			return fmt.Errorf("tag_total_or_page")
		}
		for i, item := range result.List {
			if item.ID != ids[i] {
				return fmt.Errorf("tag_order_%d", i)
			}
		}
		return nil
	}
	var result page.Response[search.Secret]
	if err := json.Unmarshal(data, &result); err != nil {
		return err
	}
	if result.Total != int64(expected) || len(result.List) != len(ids) {
		return fmt.Errorf("total_or_page_got_%d_%d_want_%d_%d", result.Total, len(result.List), expected, len(ids))
	}
	for i, item := range result.List {
		if item.GroupID != ids[i] {
			return fmt.Errorf("group_order_%d", i)
		}
		g, err := strconv.Atoi(strings.TrimPrefix(item.Key, "SERVICE_"))
		if item.Key == "RARE_KEY_XYZ" {
			g, err = 42, nil
		} else if item.Key == "KEY_100%_READY!" {
			for candidate := 1; candidate < 250_000; candidate += 100 {
				if perfID(fmt.Sprint("group-", candidate)) == item.GroupID {
					g, err = candidate, nil
					break
				}
			}
		}
		if err != nil || perfID(fmt.Sprint("group-", g)) != item.GroupID {
			return fmt.Errorf("key_group_identity")
		}
		project, folder, tenant := g%8, (g/8)%4, (g%8)/4
		if item.Scope.Tenant.ID != perfID(fmt.Sprint("tenant-", tenant)) ||
			item.Scope.Project.ID != perfID(fmt.Sprint("project-", project)) ||
			len(item.Scope.Folders) != 2 || item.Scope.Folders[1].ID != perfID(fmt.Sprintf("folder-group-%d-%d", project, folder)) {
			return fmt.Errorf("scope_identity")
		}
		expectedTags := []int{}
		if g%5 != 0 && g%19 != 0 {
			expectedTags = append(expectedTags, (g/8)%60)
		}
		if g%7 == 0 {
			expectedTags = append(expectedTags, 60)
		}
		if len(item.TagList) != len(expectedTags) {
			return fmt.Errorf("tag_count")
		}
		for i, tag := range item.TagList {
			if tag.ID != perfID(fmt.Sprintf("tag-%d-%d", tenant, expectedTags[i])) {
				return fmt.Errorf("tag_identity")
			}
		}
		envs := scenario.Body.EnvList
		if len(envs) == 0 {
			envs = []string{"dev", "test", "sim", "prod"}
		}
		if len(item.Values) != len(envs) {
			return fmt.Errorf("environment_count")
		}
		seen := map[string]bool{}
		for _, value := range item.Values {
			n, ok := map[string]int{"dev": 0, "test": 1, "sim": 2, "prod": 3}[value.EnvCode]
			allowed := false
			for _, env := range envs {
				allowed = allowed || env == value.EnvCode
			}
			if !ok || !allowed || seen[value.EnvCode] || value.Value != fmt.Sprintf("synthetic-environment-%d-value", n) {
				return fmt.Errorf("environment_or_decrypted_value")
			}
			seen[value.EnvCode] = true
		}
	}
	return nil
}

// runPerfLoad 每个并发 worker 串行请求，记录真实响应后才发下一次，结束时等待在途请求
func runPerfLoad(call func(context.Context, perfCase, bool, int, []uuid.UUID) (float64, int, string), mix []perfCase, concurrency int, duration time.Duration) (perfStats, map[string]perfStats) {
	type result struct {
		name    string
		ms      float64
		size    int
		failure string
	}
	results := make(chan result, concurrency*2)
	deadline := time.Now().Add(duration)
	started := time.Now()
	var workers sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for iteration := worker; time.Now().Before(deadline); iteration++ {
				scenario := mix[iteration%len(mix)]
				ms, size, failure := call(context.Background(), scenario, false, 0, nil)
				results <- result{scenario.Name, ms, size, failure}
			}
		}(worker)
	}
	go func() { workers.Wait(); close(results) }()
	times := map[string][]float64{}
	errors := map[string]map[string]int{}
	sizes, successes := map[string]int{}, map[string]int{}
	for result := range results {
		for _, name := range []string{"all", result.name} {
			times[name] = append(times[name], result.ms)
			sizes[name] += result.size
			if errors[name] == nil {
				errors[name] = map[string]int{}
			}
			if result.failure == "" {
				successes[name]++
			} else {
				errors[name][result.failure]++
			}
		}
	}
	elapsed := time.Since(started)
	byCase := map[string]perfStats{}
	for name, values := range times {
		byCase[name] = perfSummary(values, successes[name], sizes[name], errors[name], elapsed)
	}
	all := byCase["all"]
	delete(byCase, "all")
	return all, byCase
}
