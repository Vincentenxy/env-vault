# env-vault 权限模块骨架 —— 实施 TODO

> 目标：在 handler 入口加 ABAC 权限校验；模块内聚在 `internal/permission/`；骨架 + fail-closed 框架 + 占位 DSL（真 DSL 待定）。
>
> 用户确认项：mode=`audit`、handler 内 lazy 加载（不挂中间件）、fail-closed、同进程 module（未来 go.mod replace 拆分）。

---

## 一、新增文件清单

### 1.1 `pkg/permctx/` — 权限上下文（保留为可选项）

| 文件 | 功能 |
|------|------|
| `pkg/permctx/permctx.go` | 定义 `Permission{UserID, Roles, Codes, ResCodes, Attrs, Scopes, Loaded, LoadErr, LoadedAt}` 结构；提供 `Set/FromContext/MustFromContext` 仿 [`pkg/userctx/userctx.go`](pkg/userctx/userctx.go)；提供 `HasRole/HasCode/HasCodeOn` 便捷判定 |
| `pkg/permctx/permctx_test.go` | 单元测试 |

### 1.2 `internal/permission/domain/permission/` — 领域层（接口与契约）

| 文件 | 功能 |
|------|------|
| `internal/permission/domain/permission/permission.go` | 定义领域对象：`Subject`（求值主体）、`Decision`（决策结果）、`Provider`（数据源接口）、`Parser`（DSL 解析器接口）、`Expr`（表达式接口）、`Evaluator`（求值入口）；定义领域错误 `ErrConditionEmpty/ErrConditionSyntax/ErrUnknownFunc/ErrEvaluate` |
| `internal/permission/domain/permission/predicate.go` | 定义 `PredicateFunc` 与 `PredicateSet`，作为「原子谓词」契约；提供 `Call` 方法（未注册函数 → `ErrUnknownFunc`） |

### 1.3 `internal/permission/application/permission/` — 应用层

| 文件 | 功能 |
|------|------|
| `internal/permission/application/permission/permission.go` | 定义 `IService` 接口（`Check/CheckSubject/Mode`）；`Service` 实现；`Mode(off/audit/enforce)` 三态；`Options{Mode, SuperRoles}`；应用错误 `ErrDenied/ErrNoSubject/ErrInvalidCondition`；`NewService(provider, evaluator, options)` 工厂 |
| `internal/permission/application/permission/permission_test.go` | 三态 mode 决策表测试 + `Loaded=false` 分支 + panic recover |

### 1.4 `internal/permission/infrastructure/evaluator/` — 求值器实现

| 文件 | 功能 |
|------|------|
| `internal/permission/infrastructure/evaluator/evaluator.go` | `Options{Kind}` 工厂；`Kind` 非法 → 启动报错；`defer recover()` 兜底 panic → `ErrEvaluate` |
| `internal/permission/infrastructure/evaluator/parser_minimal.go` | **占位解析器**：仅识别扁平 `code("x") / code("x","y") / role("x")` + 单一 `AND|OR`；括号、嵌套、混用、未闭合引号、未知函数一律 `ErrConditionSyntax` |
| `internal/permission/infrastructure/evaluator/predicate.go` | 内置 `code/role` 谓词实现；`BuiltinPredicates()` 返回 `PredicateSet` |
| `internal/permission/infrastructure/evaluator/evaluator_test.go` | 覆盖：解析失败 deny、未知函数 deny、AND-OR 混用 deny、panic recover |

### 1.5 `internal/permission/infrastructure/persistence/permission/` — 数据源

| 文件 | 功能 |
|------|------|
| `internal/permission/infrastructure/persistence/permission/static.go` | `StaticProvider`（骨架期数据源）：读 yaml/配置的静态 `Subject` 表 + 内存 TTL 缓存；**不落 DB**（[AGENT.md](AGENT.md) 3.1 表设计需先审批） |

### 1.6 `internal/permission/interfaces/handler/` — 接口层（核心入口）

| 文件 | 功能 |
|------|------|
| `internal/permission/interfaces/handler/permission.go` | `PermissionHandler` 结构（依赖 `IService`）；`Check(c, condition) error`（**约定：返回 non-nil 时已写 403 + Abort**）；`CheckCode(c, code, resource...)` 类型安全糖；`Require(condition)` 路由级中间件糖；`Cond(format, args...)` 安全拼接；`WithDenyFunc/WithLogger/WithSubjectID` Option 注入 |
| `internal/permission/interfaces/handler/permission_test.go` | 三态 mode + lazy load + 拒绝路径 |

### 1.7 `internal/permission/README.md`

模块说明：分层、依赖约束、DSL 现状、拆分步骤。

---

## 二、修改文件清单

| 文件 | 改动内容 |
|------|----------|
| [`internal/infrastructure/config/config.go`](internal/infrastructure/config/config.go) | 加 `PermissionConfig{Mode, Evaluator, Source, SuperRoles, CacheTTL, StaticSubjects}` 子段 + `StaticSubject` 子结构；所有字段加 `mapstructure` tag |
| [`configs/config.yaml`](configs/config.yaml) | 加 `permission:` 段：`mode: "audit"`、`evaluator: "minimal"`、`source: "static"`、`super_roles: ["admin"]`、`cache_ttl: "5m"`、`static_subjects:` 含一个 `u_test_admin` 全码演示用户 |
| [`internal/interfaces/router/router.go`](internal/interfaces/router/router.go) | 装配 `permProvider → permEval → permSvc → permHandler`；构造 3 个业务 handler 时传入 `permHandler`；不挂中间件（链路保持 `RequestID → GinLogger → Recovery → Auth → handler`） |
| [`internal/interfaces/handler/tenant.go`](internal/interfaces/handler/tenant.go) | `TenantHandler` 加 `perm *permhandler.PermissionHandler` 字段；构造函数加 `perm` 参数；5 个方法在 `ShouldBindJSON` 之后、svc 调用之前调 `perm.CheckCode`；导入别名 `permhandler` |
| [`internal/interfaces/handler/organization.go`](internal/interfaces/handler/organization.go) | 同上结构；`Org.Create` 用 `req.TenantID` 做资源级检查 |
| [`internal/interfaces/handler/project.go`](internal/interfaces/handler/project.go) | 同上结构；`Project.Create` 用 `req.OrgID` 做资源级检查 |
| [`internal/interfaces/handler/permission_codes.go`](internal/interfaces/handler/permission_codes.go) **（新增）** | 定义全部权限码常量：`PermTenantCreate/Update/Delete/Detail/List`、`PermOrgCreate/Update/Delete/Detail/List`、`PermProjectCreate/Update/Delete/Detail/List`（命名 `<资源>:<动作>`） |
| [`internal/interfaces/handler/permstub_test.go`](internal/interfaces/handler/permstub_test.go) **（新增）** | 共享测试辅助：`stubPermService{ checkFn, mode, gotConds }`；`allowPerm/denyPerm/badCondPerm` 工厂 |
| [`internal/interfaces/handler/organization_test.go`](internal/interfaces/handler/organization_test.go) | `newOrgTestEngine` 加 `perm` 参数；旧用例传 `allowPerm()`；新增 3 条权限用例（allow/deny/badCond） |
| [`internal/interfaces/handler/project_test.go`](internal/interfaces/handler/project_test.go) | 同上结构，新增 3 条权限用例 |
| [`AGENT.md`](AGENT.md) | 新增「权限规范」章节：码位登记表、`mode/evaluator/source` 取值、模块拆分约束、fail-closed 硬性约定 |
| [`README.md`](README.md) | 新增「权限」章节：调用模式示例、`Check/CheckCode` 用法、mode 切换说明 |

---

## 三、handler 中权限码与 condition 一览

| Handler 方法 | 调用 | 实际 condition |
|---|---|---|
| `Tenant.Create` | `perm.CheckCode(c, PermTenantCreate)` | `code("tenant:create")` |
| `Tenant.Update` / `Tenant.Detail` | `perm.CheckCode(c, Perm…, req.ID.String())` | `code("tenant:update","<id>")` |
| `Tenant.Delete` | `perm.CheckCode(c, PermTenantDelete, req.ID.String())` | `code("tenant:delete","<id>")` |
| `Tenant.List` | `perm.CheckCode(c, PermTenantList)` | `code("tenant:list")` |
| `Org.Create` | `perm.CheckCode(c, PermOrgCreate, req.TenantID.String())` | `code("org:create","<tenantId>")` |
| `Org.Update/Delete/Detail` | `perm.CheckCode(c, PermOrg…, req.ID.String())` | `code("org:update","<id>")` |
| `Org.List` | `perm.CheckCode(c, PermOrgList)` | `code("org:list")` |
| `Project.Create` | `perm.CheckCode(c, PermProjectCreate, req.OrgID.String())` | `code("project:create","<orgId>")` |
| `Project.其余` | 同 Org 规则 | — |

---

## 四、实施步骤（按序）

| # | 步骤 | 关键文件 | 自检 |
|---|------|----------|------|
| 1 | 权限上下文 | `pkg/permctx/*` | `go test ./pkg/...` 通过 |
| 2 | 领域层契约 | `internal/permission/domain/permission/*` | 只有接口与错误，无实现 |
| 3 | 占位求值器 | `internal/permission/infrastructure/evaluator/*` | 单测：非法 condition 必 deny |
| 4 | 数据源占位 | `internal/permission/infrastructure/persistence/permission/static.go` | **不建表**（AGENT.md 3.1） |
| 5 | 应用服务 | `internal/permission/application/permission/*` | 三态 mode 决策表全覆盖 |
| 6 | 入口 handler | `internal/permission/interfaces/handler/permission.go(+_test)` | Check 返回 err 时已写 403 |
| 7 | 配置 + yaml | `config.go` + `configs/config.yaml` | 非法值启动失败 |
| 8 | router 装配 | `internal/interfaces/router/router.go` | `go build ./...` 通过 |
| 9 | 权限码常量 | `internal/interfaces/handler/permission_codes.go` | 码表与 §三 一致 |
| 10 | 三个 handler 接入 | `handler/{tenant,organization,project}.go` | 15 个方法各一次 Check |
| 11 | 测试改造 | `handler/permstub_test.go` + `{organization,project}_test.go` | 放行/拒绝/解析失败 各 3 条 |
| 12 | 文档 | `internal/permission/README.md`、`AGENT.md`、`README.md` | 说明 DSL 待定与 mode 切换 |

---

## 五、验证方式

### 5.1 现有测试不破

```bash
go build ./... && go vet ./... && go test ./... -count=1
```

构造函数改签名后编译器会强制暴露全部调用点；旧用例统一传 `allowPerm()`，行为不变。

### 5.2 权限拦截生效（切 `mode: enforce` 后）

```bash
# 有权限 → 200 {code:0}
curl -s -XPOST localhost:8090/api/v1/org/list -H "Authorization: Bearer $JWT" \
     -H 'Content-Type: application/json' -d '{"pageNum":1,"pageSize":10}'

# 无权限 → HTTP 403 + {"code":403,"msg":"Forbidden","data":null}
curl -s -i -XPOST localhost:8090/api/v1/org/delete -H "Authorization: Bearer $JWT" \
     -H 'Content-Type: application/json' -d '{"id":"<uuid>"}'
```

### 5.3 fail-closed 验证

- 单测：`TestService_Check_ParseError_Denied`、`TestService_Check_SubjectNotLoaded_Denied`、`TestService_Check_UnknownFunc_Denied`、`TestEvaluator_Panic_Recovered_Denied`
- 端到端：临时把某方法 condition 改成 `code(` 或 `notafunc("x")`，enforce 下必须 403 且日志出现 `invalid condition`
- 反向确认：`mode: audit` 时同样非法 condition 返回 200，日志 WARN（证明决策路径走到 deny，只是不拦截）

---

## 六、待确认（不阻塞，编码中可定）

1. `permission.mode` 在 release 模式下是否强制 enforce？（建议：启动校验 `server.mode==release && permission.mode!=enforce` → 启动失败）
2. 权限码命名 `<资源>:<动作>` 是否与既有权限中心对齐（如已有 `env-vault.org.update` 之类格式）
3. `code("x")` 全局码是否对所有资源生效——本期默认**不放宽**
4. tenant handler 目前无测试文件，是否本次一并补齐（顺手把构造函数改成 `IService`）