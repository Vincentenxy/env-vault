# env-vault 项目开发规范

> 本文件是本项目所有开发工作的**硬性规范**，任何代码变更（包括 AI 生成）必须严格遵循。后续开发前请先阅读本文件。

---

## 0. 前后端联合工作目录

- 密钥管理模块：
  - 后端 `env-vault`：`/Users/vincent/GolandProjects/env-vault`
  - 前端 `env-vault-web`：`/Users/vincent/Desktop/codes.nosync/env-vault-web`
- 发布模块：
  - 后端 `publish-devops-api`：`/Users/vincent/IdeaProjects/efficient-platform/publish-devops-api`
  - 前端 `devops-frontend`：`/Users/vincent/Desktop/codes.nosync/devops-frontend`
- “修改发布模块前端”指修改 `devops-frontend`；“修改密钥管理平台前端”指修改 `env-vault-web`。
- 后续处理每一项需求时，都必须先判断影响对应模块的后端、前端或两端；涉及接口契约或完整用户流程时，需要同时检查对应的前后端仓库。
- 修改哪个仓库，就遵循哪个仓库内的智能体规范并执行对应测试；跨端修改需要分别完成验证。
- 四个仓库不在同一父目录下，切换仓库时使用上述绝对路径。

---

## 1. 接口规范

### 1.1 接口路径格式

所有对外接口统一格式：`/api/[版本号，如 v1]/[pub]/xxx/xxx/xxx`

- `/api/[版本号]/pub/...`：**无认证接口**，任何人可调用（如健康检查、分享链接）
- `/api/[版本号]/...`（不带 `pub` 前缀）：**认证接口**，必须携带合法凭证
- 路由分组与认证中间件必须以此前缀作为拦截依据
- HTTP 路径段由多个单词组成时统一使用小驼峰命名，不使用连字符或下划线，例如使用 `/masterKey/status`，禁止使用 `/master-key/status` 或 `/master_key/status`

#### 资源归属（强制）

- 版本号后的第一个业务路径段必须表示该能力所属的核心业务资源。历史、批次、搜索、详情等附属能力必须放在所属资源路径下，不得作为无明确归属的一级资源。
- 路径结构统一为：`/api/{version}/{resource}/{subresource}/{operation-or-mode}`。

| 类型 | 路径 |
|---|---|
| 正确 | `/api/v1/secret/history/batch` |
| 正确 | `/api/v1/project/member/allocate` |
| 错误 | `/api/v1/history/batch` |
| 错误 | `/api/v1/allocate` |

- 只有当某项能力具备独立的数据模型、生命周期、权限边界，并且会被多个业务资源共同使用时，才允许将其设计为一级资源。
- 新增接口评审时必须先明确资源归属，并保证后端路由、前端调用和接口文档中的路径一致。

### 1.2 请求方法

只允许使用 **GET / POST** 两种方法：

- **带参数的接口**：统一使用 `POST`
- **不带参数的接口**，或用于分享的链接：使用 `GET`

### 1.3 响应数据格式

所有接口（包括错误响应）body 必须返回如下结构，字段名为小驼峰：

```json
{
  "code": 0,
  "msg": "",
  "data": {}
}
```

**code 状态码约定**：

| 范围 | 含义 |
|------|------|
| `0` | 成功 |
| `-1` | **默认通用失败**：所有未单独定义业务码的失败一律返回 `-1`，msg 由 service / handler 给出可读信息 |
| `1 ~ 1000` | 系统预留失败状态码，其中 600 以内与 HTTP 状态码保持一致 |
| `10000 以上` | **特例业务码**：仅在客户端必须按业务码做差异化处理（如不同 UI 文案、特殊跳转、不同重试策略等）时使用，由各模块 owner 评审后才新增 |

**错误码分配原则**：

- **默认情况下所有业务错误都使用 `code=-1`**，msg 体现具体失败原因（"tenant code already exists"、"organization not found" 等）
- 只有"特例"才定义明确的业务码：例如支付场景必须区分 `余额不足 / 通道异常` 等不同分支、登录场景必须区分 `密码错误 / 账号锁定`、第三方对接必须区分 `限流 / 鉴权失败 / 服务降级` 等
- 各模块 handler 的 `respondError` 默认行为：
  ```go
  switch {
  case errors.Is(err, <任意业务错误>):
      response.Error(c, err.Error()) // code=-1
  default:
      response.Error(c, "internal error")
  }
  ```
- 新增业务码位时，必须在 AGENT.md 记录码位 + 使用场景 + 客户端处理方式，三者缺一不可

### 1.4 错误处理（硬性要求）

- `pkg/response` 包提供快速生成响应的方法（Success / Fail 等），常规错误统一调用这些方法
- **所有错误响应（含 400 / 401 / 403 / 404 / 500 等）body 永远返回标准 response 结构体**，且 body 内 `code`、`msg` 与 HTTP 状态码、HTTP 状态文本对应，例如：

```
HTTP 状态码：401
HTTP body：
{
  "code": 401,
  "msg": "Unauthorized",
  "data": null
}
```

- 认证失败、参数校验失败等场景禁止直接返回纯文本或框架默认错误页，必须经过统一封装

### 1.5 接口文档

- 使用 **swaggo**（swag 注解）方式编写接口文档，注解写在 handler 上，生成文件输出到 `docs/`（该目录不提交版本库）
- 同时维护一份符合 **OpenAPI 协议**的独立文档，用于对外分发（生成后导出，方便提供给外部）

### 1.6 分页规范（硬性要求）

所有分页接口统一使用 `pkg/page` 包定义的结构：

1. 所有 HTTP 分页请求 DTO 必须匿名嵌入 `page.Request`，禁止重复声明 `PageNum` / `PageSize` 或 `json:"pageNum"` / `json:"pageSize"`。
2. Handler 完成参数绑定后调用一次 `req.Normalize()`。分页默认值和上限处理只能在 Handler 层执行；特殊情况可不使用默认值，但必须在代码中说明。
3. `pageNum <= 0` 时取 `page.DefaultPageNum`，`pageSize <= 0` 时取 `page.DefaultPageSize`；`pageSize > page.MaxPageSize` 时取 `page.MaxPageSize`。
4. Application Service、Domain 和 Repository 禁止再次设置分页默认值、限制最大值或执行分页归一化，只接收并使用 Handler 已处理的分页参数；特殊情况必须在代码中说明。
5. 内部 Application Input 和 Domain Filter 可以保留 `PageNum` / `PageSize` 作为层间传递字段，但不得包含默认值处理；`page.Request` 只用于 HTTP 请求 DTO，避免 Domain 层依赖带 JSON 语义的结构。
6. 分页响应统一使用 `page.Response[T]`，只返回 `total` / `list`。特殊响应结构必须由项目 Owner 明确确认并在接口代码中注明例外。
7. 分页接口测试至少覆盖：缺省参数、负数页码、零值 `pageSize`、超过最大 `pageSize`，以及 Service 收到归一化参数。

```go
// 请求示例
type ListRequest struct {
    Code string `json:"code"`
    page.Request
}

// 响应示例
response.Success(c, page.Response[TenantDTO]{ Total: total, List: items })
```

---

## 2. 认证规范

- 认证方式：请求头携带 `Authorization: Bearer <token>`
- Token 类型：**JWT**，统一使用 RS256；必须校验签名、`iss`、`aud` 和 `exp`，禁止算法降级
- 认证来源：系统同时预留 EnvVault 本地认证和公司统一认证。EnvVault 本地登录使用自身 RSA 私钥签发 JWT；公司 JWT 仅使用公司公钥验签。两类密钥不得与 Secret 主密钥复用
- 密钥管理：JWT 私钥禁止提交仓库，必须通过环境变量或 Kubernetes Secret 挂载文件注入；公钥可以通过配置注入。多实例必须共享同一套本地签名私钥，禁止每次启动临时生成
- 本地登录：`POST /api/v1/pub/auth/login` 使用全局唯一 `username` 和密码认证。密码只允许保存 Argon2id PHC 格式哈希，禁止保存明文、可逆密文或使用 Secret 主密钥加密
- 认证实现：认证中间件在 router 层挂载，已实现：
  - 路径含 `/pub/` 前缀 → 跳过认证
  - 其余 `/api/` 路径 → 强制认证，失败统一返回 401 标准错误结构（见 1.4）
- **用户信息解析**（对应 Java UserContext 逻辑）：认证通过后写入 `pkg/userctx.User`：

| User 字段 | 来源 |
|-----------|------|
| `UserID` | claims.`staffuserid` |
| `Name` | claims.`name` |
| `Jwt` | 原始 token（下游透传用） |
| `Cookie` | 请求头 Cookie（下游透传用） |

- 业务代码获取用户：`userctx.MustFromContext(c)`，详见 README.md 认证章节
- 主密钥状态和单份分片提交接口虽然需要在系统未就绪时绕过 Ready 拦截，但仍必须经过 JWT 认证；只有 `/pub/auth/login` 是公开认证入口

---

## 3. 数据库规范

- **PostgreSQL**：持久化存储
- **Redis**：缓存
- 所有数据库表设计必须记录在 `design/database.md` 中，保持与实现同步
- 文档格式：Markdown，表结构使用 **SQL 代码块**（```sql）包裹，附索引说明
- 所有可变业务表必须包含以下通用字段（数据库列名为下划线风格）；只追加、不更新、不删除的历史表仅保留 `create_at` / `create_by`：

| 列名 | 说明 |
|------|------|
| `create_at` | 创建时间 |
| `update_at` | 更新时间 |
| `create_by` | 创建人 |
| `update_by` | 更新人 |
| `delete_at` | 删除时间（软删除，未删除为 NULL） |
| `delete_by` | 删除人 |
| `is_deleted` | 软删除标记（boolean，默认 false） |

- **软删除**：所有可变业务表采用软删除（`is_deleted` + `delete_at` + `delete_by`），查询默认过滤已删除数据，禁止物理删除；只追加历史表不提供删除能力

### 3.1 表设计硬性规则

1. **禁止使用外键依赖**：表间关联关系全部在代码层面维护
2. **禁止数据库侧特殊校验**：除 NOT NULL / 默认值外，不加 CHECK、UNIQUE 之外的数据库约束（必要的查询索引除外），业务校验全部放在代码层面显式实现
3. **表设计先行审批流程（强制）**：
   - 所有涉及数据库表的需求，**必须先完成表设计并写入 `design/database.md`，提交给项目 Owner 审核**
   - **Owner 确认后才能开始动手开发功能代码**
   - 未获确认前，禁止编写任何与该表相关的 model / repository / handler 代码
4. **避免数据库保留关键字**：表名、列名不得使用 PG 保留字（如 `user`、`order`、`comment` 等需评估，必要时换用同义词或加后缀）
5. **信息类表统一 `_info` 后缀**：如 `tenant_info`、`organization_info`、`system_info`
6. **实体层级**：租户（tenant_info）→ 组织（organization_info）→ 系统（system_info）。租户类比公司、组织类比部门、系统类比业务系统，上层是下层的归属

---

## 4. 命名规范

- **Go 结构体字段 / JSON 字段**：小驼峰（如 `createAt`、`userName`）
- **数据库列名**：下划线（如 `create_at`、`user_name`）
- **人员名称响应字段**：接口返回人员标识及其名称时，名称字段统一使用对应标识字段加 `Name` 后缀，不得另造名称；例如 `createBy` 对应 `createByName`、`updateBy` 对应 `updateByName`、`manager` 对应 `managerName`
- 其余命名遵循 Go 官方规范（包名小写、导出标识符大驼峰等）

---

## 5. 日志规范（硬性要求）

- **全局日志只允许使用 `pkg/logger` 模块**（zap 封装），禁止直接使用 `fmt.Println` / `log.Println` / zap 全局 Logger
- 日志格式：人类可读 + 级别带颜色（INFO 绿色系、ERROR 红色等），时间格式 `2006-01-02T15:04:05.000-07:00`
- **trace-id 透传**：HTTP 入口由 `middleware.RequestID()` 处理 `x-request-id` 请求头（携带则透传、缺失则生成、响应头回写）；日志中有 trace-id 时打印，没有则不打印
- 使用方式（详见 README.md 日志章节）：
  - HTTP 请求场景：`logger.Info(c, "msg", zap.Xxx(...))`，传入 gin.Context 自动携带 trace-id
  - 无 context 场景：`logger.L().Info("msg", ...)`
- 级别使用：Debug / Info / Warn / Error，生产环境（release 模式）输出 JSON 格式且仅 Info 及以上级别

### 5.1 业务审计日志阶段性边界（已确认）

- 审计查询当前只要求通过认证，资源级查看权限暂不在本项目内实现；后续必须接入独立权限管控系统，再按审计事件的 `scope_type` / `scope_id` 完成资源管理员和超管授权。禁止自行增加一套临时权限模型，也禁止将当前空的 `applyPermissionFilter` 误认为已经完成权限控制
- 普通业务接口在 Handler 层发生 JSON 转换、参数绑定或必填项校验失败时不写业务审计；登录、JWT 认证、主密钥分片等安全边界仍按各模块安全策略记录失败尝试
- 审计记录继续写入一年后的 `expire_at`，当前暂不实现保留期清理任务；后续清理任务必须分批执行并记录自身审计事件
- 本项目暂不内置数据库 migration，审计表 DDL 由外部数据库变更系统维护；代码发布前必须先完成对应数据库变更
- gRPC、SDK 和外部事件当前只保留统一审计模型，interceptor、adapter 和离线事件接收待相应能力开发时实现
- 业务写入与成功审计必须保持同事务；审计写入失败时业务操作失败。Secret 明文和密文均不得写入审计记录

---

## 6. 部署规范

- 项目最终部署在 **Kubernetes**
- 根目录维护 **Dockerfile**（多阶段构建，产出精简运行镜像）
- Dockerfile 需保证二进制可在无 Go 环境的容器中独立运行，配置文件以挂载或环境变量方式注入

---

## 附：当前项目结构基线

```
env-vault/
├── cmd/main.go                          # 入口
├── configs/config.yaml                  # 配置
├── design/database.md                   # 表结构设计文档（SQL 代码块格式）
├── docs/                                # swaggo 生成文档（不提交版本库）
├── internal/
│   ├── domain/                          # 领域层
│   ├── application/                     # 应用层
│   ├── interfaces/                      # 接口层（handler / router / middleware）
│   └── infrastructure/                  # 基础设施层（config / db / redis / jwt）
├── pkg/
│   ├── logger/                          # 全局统一日志（zap 封装，禁止绕过）
│   ├── page/                            # 统一分页结构（Request/Response）
│   ├── response/                        # 统一响应封装
│   └── userctx/                         # 认证用户上下文（JWT 解析结果存取）
└── Dockerfile                           # 多阶段构建，K8s 部署
```

## 附：已确认决策记录

- 表设计文档：Markdown + SQL 代码块格式（`design/database.md`）
- 接口文档：swaggo 注解生成 + 导出 OpenAPI 文档对外分发
- 业务错误码（10000+）：暂无分段规则，后续开发中逐步分段后补充到本文件
