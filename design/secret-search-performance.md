# 密钥搜索性能核验方案

日期：2026-10-28

状态：2026-10-28 本轮本地验证已完成，覆盖数据库只读盘点、PostgreSQL 16.4 三个规模、完整 HTTP 并发、长值、代理和真实浏览器；已发现超时及 SQL 瓶颈，性能目标未全部达到，实际集群尚未核验

结果见 [密钥搜索性能核验结果](secret-search-performance-results-20261028.md)

## 1. 核验目标与边界

核验现有搜索在预期数据量和并发下的正确性、耗时、资源占用与取消行为，为标签分类统计提供基线

本轮对象：

- `POST /api/v1/secret/search`
- `POST /api/v1/secret/search/tag/list`
- 前端搜索提交到表格渲染的完整链路

不增加值检索、标签统计、全量导出或搜索引擎，不调整现有匹配语义

测试分两类环境：

- 专用测试库：创建隔离数据集，执行造数、索引对比、EXPLAIN ANALYZE 和并发测试
- 实际部署环境：先检查版本、索引、统计信息和现有请求耗时，再执行受控抽样；不向业务库写入测试数据，也不直接开展大规模压测

测试数据库版本与目标部署保持一致，当前仓库的 PostgreSQL 清单为 16.4；同时记录 CPU、内存、存储、连接池、Go 服务副本数及网络链路

## 2. 执行前能力与本轮补充范围

本节保留执行前的差距说明，实际完成范围和未达标项以本轮结果报告为准

| 内容 | 已有实现 | 本次需要补充 |
| --- | --- | --- |
| 仓储性能测试 | `internal/search/performance_test.go` | 重复采样、多范围、标签、深分页及并发 |
| 数据量 | 10 万、100 万条环境实例 | 小数据基线和分布不同的数据集 |
| 层级 | 单项目、四套环境 | 多租户、多项目、多目录、热点分布 |
| 文本匹配 | 少量命中、中文、高频词和无匹配 | 短词限定范围、普通符号、组合筛选 |
| 执行计划 | 稀有词 count 的 EXPLAIN ANALYZE | count、分页 ID、详情、标签候选的主要场景 |
| 密钥值 | 占位密文 | 完整接口使用真实加密的虚构值 |
| 标签 | 当前性能脚本未加载真实标签仓储 | 真实绑定、多标签及跨租户候选 |
| 阶段日志 | count、page、values、tags 的 Debug 耗时 | 解密、审计、序列化、HTTP 总耗时的可关联采样 |
| 真实接口 | 本轮尚未压测 | JWT、用户检查、解密、审计、响应传输 |

现有脚本直接测试 repository，使用 30 秒测试上下文，不等于实际搜索的默认 5 秒预算，也不覆盖前端体验

现有阶段日志不包含全部接口开销；审计中的 durationMs 在审计写入前取值，不能作为完整 HTTP 耗时

## 3. 第一阶段：环境和索引盘点

先完成只读检查，记录以下内容：

1. 当前数据库、schema、PostgreSQL 版本及应用实际连接目标
2. `secret_info` 物理环境实例数、逻辑 groupId 数和环境分布，先用统计估计值；精确统计放到专用测试环境或受控抽样中
3. `pg_trgm` 扩展、Key/remark GIN 索引及其 `indisvalid`、`indisready` 状态
4. group_id、folder_id、标签关联索引和层级关联条件
5. 数据表最近 ANALYZE 时间、估算行数、索引大小、数据库和应用连接池容量

只读索引检查示例，在确认连接目标后运行：

```sql
SELECT current_database(), current_user, current_schema(), version();

SELECT extname, extversion
FROM pg_extension
WHERE extname = 'pg_trgm';

SELECT n.nspname AS schema_name, t.relname AS table_name,
       i.relname AS index_name, x.indisvalid, x.indisready,
       pg_get_indexdef(i.oid) AS index_definition
FROM pg_index AS x
JOIN pg_class AS t ON t.oid = x.indrelid
JOIN pg_class AS i ON i.oid = x.indexrelid
JOIN pg_namespace AS n ON n.oid = t.relnamespace
WHERE n.nspname = current_schema()
  AND t.relname IN ('secret_info', 'secret_tag_relation', 'tag_info')
ORDER BY t.relname, i.relname;
```

索引缺失或失效时先报告，再按数据库变更流程处理。DDL 见 `design/database.md` 的 Secret 搜索索引章节；不能仅凭索引同名存在判断已生效

`CREATE INDEX CONCURRENTLY` 分别执行，不放在事务块中。修复失效索引时需要单独核对，`IF NOT EXISTS` 不会自动修复失效索引

阶段产物：环境基线、索引清单、是否具备继续测试的条件

## 4. 第二阶段：专用数据库基线

### 4.1 数据规模与分布

| 环境实例数 | 四环境时的逻辑密钥组数 | 用途 |
| --- | --- | --- |
| 10,000 | 2,500 | 快速回归、验证造数和结果 |
| 100,000 | 25,000 | 常规规模测试 |
| 1,000,000 | 250,000 | 大规模边界和高命中率查询 |

组数与物理行数分别记录，避免把百万环境实例描述为百万个逻辑 Key

数据包含多租户、多项目、一级和二级目录，分别构造均匀分布与热点集中分布。添加未绑定标签、绑定单标签、多标签、跨租户同名标签、软删除数据

百万行基础数据使用较小的虚构值；16 KiB、64 KiB 等长值只放入专门的少量结果组，用于测量响应和解密成本，不把全部数据放大成长值

### 4.2 必测场景

| 场景 | 主要核对点 |
| --- | --- |
| 稀有 Key、无匹配 | GIN 索引、少量命中代价 |
| 中文备注三字词 | Unicode 输入、备注索引 |
| 高频词、大范围空关键词 | 精确计数和组分页代价 |
| 项目或目录内的两字词 | 合法短词查询在限定范围内的成本 |
| 大范围非法短词 | 在读取大数据前返回现有参数错误 |
| 租户、组织、项目、多个目录 | 范围过滤和去重 |
| 多项目、同项目多个环境 | 环境规则与结果完整性 |
| 单标签、多标签、标签加关键词 | EXISTS 筛选、租户隔离、组级去重 |
| 标签候选首页、搜索及续页 | 跨租户候选去重、总数与分页 |
| 首页和深页码 | 第 1、10、100 页及确实存在的更深页面 |
| 每页 20、100、200 组 | 大页请求的 SQL、响应大小和内存 |
| 较多环境、长值 | 返回环境数量和明文响应体积 |

以实际存在的结果页测试深分页，不能只测超过 total 后提前返回空页的情况

### 4.3 采样方式

- 固定造数种子和期望结果，测试前更新专用库统计信息
- 无索引与有索引对比仅在专用库执行，记录索引构建成本
- 每个代表性单请求场景预热后至少重复 30 次，记录中位数和 P95；尾部样本不足时不作稳定 P99 结论
- 首次执行与预热后执行分开记录，首次执行不能直接宣称是数据库冷缓存
- 记录 count、分页 ID、详情、标签阶段的耗时及行数
- 针对瓶颈执行 `EXPLAIN (ANALYZE, BUFFERS)`，检查扫描量、估算偏差、排序、临时文件和缓存读取
- 不通过关闭顺序扫描强制制造索引命中；高命中率时优化器选择顺序扫描可能合理

现有脚本可先复用以下命令建立旧基线，DSN 必须已配置为专用测试库：

```sh
ENV_VAULT_SEARCH_PERF=1 go test ./internal/search \
  -run '^TestPostgresSearchPerformance$' -count=1 -v
```

`ENV_VAULT_SEARCH_TEST_DSN` 通过环境提供。现有脚本需要创建隔离 schema 和 pg_trgm 扩展的权限，不能连接业务库运行

## 5. 第三阶段：完整接口并发测试

使用专用后端实例和测试数据库，按项目正式表结构准备数据和审计表，加载测试主密钥。使用测试 JWT、真实加密的虚构值和真实标签关联

需要覆盖 JWT 验签、用户锁定检查、数据库查询、值解密、审计写入、JSON 序列化和网络传输

并发按 1 → 5 → 10 → 20 逐级增加。每一级预热 30 秒，再测量 3 分钟；明显超出服务容量时停止加压，记录容量边界，避免把排队后的所有请求计作正常负载

先测每种代表性查询，再测试混合负载。混合比例在报告中声明为测试假设，后续可按实际使用分布调整

指标包括：

- HTTP 完整耗时 P50/P95/P99、吞吐量及实际完成样本数
- HTTP 状态和业务 code，HTTP 200 且 code=0 才算业务成功
- 参数拒绝、预期取消、搜索超时、未知错误分别计数
- 响应字节数、返回组数和环境实例数
- Go CPU、内存、GC，数据库 CPU、I/O、活跃连接、等待和连接池排队

专门测试请求取消和超时：SQL 应结束且连接可复用，不返回半页成功数据。取消也不能产生持续堆积的审计或数据库请求

搜索默认 5 秒超时约束查询及解密预算，不是整个 HTTP 链路的严格上限；审计和传输耗时需要独立核验

请求日志和报告只保存场景 ID、耗时、数量及大小，不保存 JWT、真实关键词、明文值或完整响应体

## 6. 第四阶段：部署链路与前端抽查

在相同场景下对比：

1. 直接访问后端测试实例
2. 经过实际部署的网关和 Web Nginx 访问
3. 浏览器提交搜索到表格显示

这三层分别定位后端、代理和前端渲染成本。浏览器验证首页、翻页、切换范围、旧请求取消、标签候选续页，以及最多允许组数下的多环境渲染

实际部署环境先做低频抽样核对。只有环境、数据和资源配额明确后，才把测试环境压测结论与部署实例容量关联

## 7. 建议验收标准

以下是第一轮建议目标，尚未测量，不能表述为当前已达到。测试前固定环境、数据集和负载后使用同一标准比较

| 项目 | 第一轮建议目标 |
| --- | --- |
| 常规搜索 | 每页 20 组、并发不超过 10 的代表性正常负载，完整接口 P95 不超过 1 秒 |
| 标签候选 | 同一正常负载下 P95 不超过 500 毫秒 |
| 高频词和深分页 | 单独报告；建议先以低并发 P95 不超过 3 秒为目标 |
| 正确性 | 组数、总数、环境、标签和软删除规则均满足预设结果，无重复和漏组 |
| 异常 | 正常负载无未知错误；预期超时和取消单独归类 |
| 容量边界 | 明确从哪个并发开始持续排队、超时或资源饱和 |

结果未达到建议目标时，按阶段耗时定位瓶颈，优先判断精确 count、深分页排序、详情体积、标签查询或审计写入的影响；不直接提高超时掩盖问题

前端体验和代理额外耗时单独报告。深分页或大页的目标是否需要调整，应依据明确的业务使用场景，而非测试后改指标以宣称通过

## 8. 实施内容与交付物

实施阶段预计新增或调整：

- 扩展 `internal/search/performance_test.go`：真实标签、范围、页码、重复采样和分阶段结果
- 增加独立的性能数据构造辅助代码，避免扩大常规集成测试的数据量
- 增加完整接口压测入口和结构化结果输出，可使用现有 Go HTTP 客户端能力，不增加业务接口
- 仅在现有日志不足以定位瓶颈时增加阶段观测，避免逐条值日志影响压测
- 将核验结果写入单独报告，记录版本、数据规模、条件、耗时、执行计划、资源占用及待优化项

报告至少输出以下对照：

`场景 × 数据量 × 并发 × 页码 × 每页组数 × P95 × 错误分类 × 响应大小`

完成后再决定是否优化现有搜索；没有明显瓶颈时，进入标签分类统计开发，不预先引入搜索引擎或修改分页契约

## 9. 本轮可复用的执行入口

新增的性能入口集中在 `internal/search` 的测试文件，不注册业务接口，也不修改业务数据库

| 文件 | 用途 |
| --- | --- |
| `performance_fixture_test.go` | 复用数据库设计文档建表，生成层级、标签、四环境密文和软删除数据，独立计算期望分页 |
| `performance_http_test.go` | 通过正式 Router、JWT 中间件、Redis 用户状态、搜索 Service 和审计仓储执行 HTTP 基线与闭环并发 |
| `performance_values_test.go` | 在单独运行中测量 16 KiB / 64 KiB 值、1 / 4 / 10 环境、20 / 200 组响应的成本 |

完整 HTTP 入口必须显式设置 `ENV_VAULT_SEARCH_HTTP_PERF=1`，同时拒绝非本机或库名不是 `env_vault_search_perf` 的 DSN
它在专用库创建随机 schema，结束时自动删除该 schema；扩展安装在专用库的 public schema
Redis 固定连接本机 `26379`，应使用专用实例，不能映射到业务 Redis

准备专用容器，示例仅绑定本机回环地址：

```sh
docker run -d --name env-vault-search-perf-pg \
  --cpus=4 --memory=3g --shm-size=256m \
  -p 127.0.0.1:25432:5432 \
  -e POSTGRES_USER=search_perf \
  -e POSTGRES_DB=env_vault_search_perf \
  -e POSTGRES_HOST_AUTH_METHOD=trust \
  postgres:16.4 \
  -c shared_buffers=256MB -c track_io_timing=on -c max_connections=100

docker run -d --name env-vault-search-perf-redis \
  --cpus=0.5 --memory=128m -p 127.0.0.1:26379:6379 \
  redis:7-alpine redis-server --appendonly no --save ''
```

上述免密码配置只适用于这个本机专用测试库。已有同名容器或端口时先核对归属，不能删除别的服务
等待 PostgreSQL 和 Redis 就绪后，在后端仓库执行：

```sh
export ENV_VAULT_SEARCH_TEST_DSN='host=127.0.0.1 port=25432 user=search_perf dbname=env_vault_search_perf sslmode=disable'

ENV_VAULT_SEARCH_HTTP_PERF=1 \
go test ./internal/search -run '^TestSearchPerformanceHTTP$' \
  -count=1 -timeout 45m -v
```

运行三个规模、每规模 19 个场景；基线每场景采样 10 次，无 GIN 的稀有词对照每规模采样 3 次
这是探索性小样本，基线 P95 按最近秩计算，10 次样本的 P95 实际取最大值，不能代替长期线上分位数
并发阶段每级预热 30 秒、采样 180 秒，等待在途请求完成后计入总耗时，出现错误后停止升压
混合请求比例为：稀有 Key 40%、项目范围 20%、单标签 10%、标签候选 10%、高频备注 10%、全范围浏览 10%

快速回归及长值专项分开运行，避免长值造数和响应污染百万行并发基线：

```sh
ENV_VAULT_SEARCH_HTTP_PERF=1 ENV_VAULT_SEARCH_PERF_SMOKE=1 \
go test ./internal/search -run '^TestSearchPerformanceHTTP$' \
  -count=1 -timeout 5m -v

ENV_VAULT_SEARCH_HTTP_PERF=1 ENV_VAULT_SEARCH_PERF_SMOKE=1 \
ENV_VAULT_SEARCH_PERF_LARGE_VALUES=1 \
go test ./internal/search -run '^TestSearchPerformanceHTTP$' \
  -count=1 -timeout 10m -v
```

默认结果保存在 `.local/search-performance/latest`，可用 `ENV_VAULT_SEARCH_PERF_OUTPUT` 设置独立的绝对输出目录
结构化结果为 `http-results.json`，执行计划为 `plan-规模-场景-阶段.txt`；请求/响应正文不写入结果
`runtime.json` 含临时测试 JWT，只供本次会话中浏览器代理使用，文件权限为 `0600`，不提交版本库，结束后删除
`ENV_VAULT_SEARCH_PERF_HTTP_ADDR=127.0.0.1:端口` 可固定测试服务地址；默认使用随机回环端口
可选 `ENV_VAULT_SEARCH_PERF_HOLD=1` 在测试后保留当前实例，删除输出目录中的 `hold` 标记结束并清理 schema

按示例名称清理专用容器及其匿名测试卷：

```sh
docker rm -f -v env-vault-search-perf-pg env-vault-search-perf-redis
unset ENV_VAULT_SEARCH_TEST_DSN
```

测试通过只表示功能断言符合预期，性能目标是否达到必须查看报告中的超时数量和分位数
