# env-vault

环境变量 / 密钥管理服务（项目初期，业务功能建设中）。

基于 Go + Gin 的 DDD 分层 Web 服务，使用 PostgreSQL 持久化、Redis 缓存，最终部署于 Kubernetes。

## 技术栈

| 组件 | 选型 | 版本 |
|------|------|------|
| 语言 | Go | 1.26 |
| Web 框架 | Gin | v1.12 |
| 配置 | Viper | v1.21 |
| 日志 | Zap（统一封装于 `pkg/logger`） | v1.28 |
| 数据库 | PostgreSQL（持久化）+ Redis（缓存） | — |
| 部署 | Kubernetes + Docker（根目录 Dockerfile 多阶段构建） | — |

## 项目结构

```
env-vault/
├── cmd/
│   ├── main.go                          # 服务入口（加载配置 → 初始化日志 → 启动 HTTP → 优雅退出）
│   └── masterkey/                       # 主密钥分片离线生成工具
├── configs/config.yaml                  # 配置文件（Viper 解析，支持环境变量覆盖）
├── design/database.md                   # 数据库表设计文档（SQL 代码块格式）
├── internal/
│   ├── domain/                          # 领域层：模型、仓储接口
│   ├── application/                     # 应用层：用例编排
│   ├── interfaces/                      # 接口层
│   │   ├── handler/                     #   HTTP 处理器
│   │   ├── middleware/                  #   中间件（request-id / 访问日志 / 认证）
│   │   └── router/                      #   路由注册（pub / 认证分组）
│   └── infrastructure/                  # 基础设施层（config / db / redis / jwt）
├── pkg/
│   ├── logger/                          # 全局统一日志模块（zap 封装）
│   └── response/                        # 统一响应封装
├── docs/                                # swaggo 生成文档（不提交版本库）
├── AGENT.md                             # 开发规范（所有开发必须遵循）
└── Dockerfile                           # 多阶段构建
```

## 运行

```bash
# 本地运行（默认读取 ./configs/config.yaml）
go run ./cmd

# 环境变量覆盖配置，例如修改端口
SERVER_PORT=9090 go run ./cmd

# Docker 构建
docker build -t env-vault:latest .
```

健康检查：`GET /api/v1/pub/health`

## 主密钥分片生成工具

`cmd/masterkey` 是独立的离线工具，用于生成 EnvVault 主密钥的 3-of-5 Shamir 分片。工具一次输出同一批次的 5 个 `EVS1` Token，系统启动时任选其中 3 个提交到 `/masterKey` 页面即可恢复主密钥。

工具只向标准输出写入分片 Token，不输出完整主密钥，不写数据库、Redis 或日志，也不接受命令行明文主密钥参数。

### 构建工具

直接通过源码运行：

```bash
go run ./cmd/masterkey help
```

构建独立程序：

```bash
# Linux / macOS
go build -o env-vault-masterkey ./cmd/masterkey

# Windows PowerShell
go build -o env-vault-masterkey.exe ./cmd/masterkey
```

### 生成全新的主密钥分片

首次安装且数据库中没有历史密文时，使用 `generate` 生成新的 AES-256 主密钥并拆成 5 份：

```bash
go run ./cmd/masterkey generate
```

输出格式如下，每一行冒号后是可以直接填入页面的完整 Token：

```text
EnvVault 主密钥分片（共 5 份，恢复需要任意 3 份）
1: EVS1.<token-1>
2: EVS1.<token-2>
3: EVS1.<token-3>
4: EVS1.<token-4>
5: EVS1.<token-5>
```

需要结构化输出时使用 JSON：

```bash
go run ./cmd/masterkey generate --format json
```

`generate` 不输出生成过程中的完整主密钥。主密钥只能通过同一批次任意 3 份分片恢复。

### 拆分已有主密钥

数据库已经存在 Secret 密文时，不能生成新的随机主密钥，必须将加密这些数据时使用的原 Base64 主密钥拆分。否则系统虽然可以进入 Ready 状态，但历史密文无法解密。

主密钥只允许通过环境变量传入，默认变量名为 `ENV_VAULT_MASTER_KEY`。

Windows PowerShell：

```powershell
$env:ENV_VAULT_MASTER_KEY = '<原 security.encryption_key>'
go run ./cmd/masterkey split
Remove-Item Env:ENV_VAULT_MASTER_KEY
```

Linux / macOS：

```bash
read -s ENV_VAULT_MASTER_KEY
export ENV_VAULT_MASTER_KEY
go run ./cmd/masterkey split
unset ENV_VAULT_MASTER_KEY
```

也可以指定其他环境变量名和 JSON 输出格式：

```bash
go run ./cmd/masterkey split --key-env MY_MASTER_KEY --format json
```

不要使用命令行参数、Shell 历史或共享脚本传递完整主密钥。

### 测试分片启动流程

1. 使用 `generate` 创建全新测试密钥，或者使用 `split` 拆分测试数据库原来的加密密钥。
2. 关闭配置密钥回退。可以修改 `configs/config.yaml`，也可以在启动进程中设置环境变量：

```powershell
$env:SECURITY_ALLOW_CONFIG_KEY_FALLBACK = 'false'
go run ./cmd
```

3. 查询状态接口，确认返回 `ready=false`：

```bash
curl http://localhost:8090/api/v1/pub/masterKey/status
```

4. 打开前端页面 `http://localhost:5173/masterKey`，从同一批次的 5 份分片中任选 3 份，分别粘贴冒号后的完整 `EVS1` Token。
5. 提交成功后，状态接口返回 `ready=true`、`source=shares`，普通业务接口恢复访问。

### 分片保管要求

- 5 份分片应分别交给不同管理员或存放在相互独立的安全位置。
- 任意 3 份分片可以恢复主密钥，少于 3 份无法恢复；不得把 3 份及以上分片保存在同一位置。
- 不要在 CI、工单、聊天记录、终端录屏或应用日志中输出分片。
- 重新执行工具会产生新的分片批次，不同批次的分片不能混用。
- 分片丢失导致可用数量少于 3 份时，主密钥和已有密文将无法恢复。

详细启动设计见 [design/master-key-loading.md](design/master-key-loading.md)。

## 接口设计规范

### 路径格式

所有对外接口统一格式：`/api/[版本号]/[pub]/xxx/xxx`

- `/api/v1/pub/...`：**无认证接口**，可随意调用
- `/api/v1/...`（不带 `pub`）：**认证接口**，请求头需携带 `Authorization: Bearer <token>`（JWT，公钥验签）

### 请求方法

只允许 GET / POST：

- 带参数的接口统一使用 `POST`
- 不带参数、或用于分享的链接使用 `GET`

### 响应格式

所有响应（含错误）body 均为统一结构：

```json
{
  "code": 0,
  "msg": "",
  "data": {}
}
```

**code 约定**：

| 范围 | 含义 |
|------|------|
| `0` | 成功 |
| `-1` | 通用失败 |
| `1 ~ 1000` | 系统预留失败状态码，600 以内与 HTTP 状态码保持一致 |
| `10000+` | 业务失败状态码（分段规则后续补充） |

**错误响应硬性要求**：400/401/403/404/500 等场景，body 内 `code`、`msg` 必须与 HTTP 状态码、标准状态文本对应，例如 HTTP 401 时：

```json
{ "code": 401, "msg": "Unauthorized", "data": null }
```

代码中统一调用 `pkg/response` 提供的方法：

```go
response.Success(c, data)                    // 成功
response.Fail(c, code, "msg")                // 业务失败
response.Error(c, "msg")                     // 通用失败（code=-1）
response.AbortWithHTTPStatus(c, 401)         // HTTP 错误（code/msg 自动对齐）
```

### 接口文档

- 使用 swaggo 注解编写（handler 上方注释），生成至 `docs/`
- 同时导出一份 OpenAPI 文档用于对外分发

## 数据库规范

- PostgreSQL 持久化，Redis 缓存
- 所有表设计记录在 [design/database.md](design/database.md)（Markdown + SQL 代码块），实现变更必须同步更新
- 所有表必须包含通用字段：`create_at` / `update_at` / `create_by` / `update_by`
- 命名：Go/JSON 字段小驼峰，数据库列名下划线

## 日志使用说明

全局日志**只允许**使用 `pkg/logger` 模块（基于 zap 封装），禁止直接使用 `fmt.Println`、`log.Println` 或 zap 全局 Logger。

### 特性

- 日志级别带颜色（debug 模式）：INFO 蓝绿色、ERROR 红色等
- 时间格式：`2026-08-12T16:35:39.051+08:00`，人类可读
- **trace-id 透传**：HTTP 请求自动处理 `x-request-id` 请求头——请求携带则透传，未携带自动生成；响应头回写。日志中有 trace-id 时打印，没有则不打印
- release 模式输出 JSON 格式，便于日志采集

输出示例（debug 模式）：

```
2026-08-12T16:55:44.656+08:00  INFO  middleware/request.go:38  http access  {"traceId": "3f05153c842ab2c03a7c0d8b9aa9d9ce", "method": "GET", "path": "/api/v1/pub/health", "status": 200, "cost": 293208, "clientIp": "::1"}
```

### 使用方式

**1. HTTP 请求场景（handler / middleware / 从 gin.Context 派生的调用链）**

直接传入 `*gin.Context`，trace-id 自动携带：

```go
import (
    "env-vault/pkg/logger"
    "go.uber.org/zap"
)

func (h *Handler) DoSomething(c *gin.Context) {
    logger.Info(c, "do something", zap.String("key", "value"))
    logger.Error(c, "something failed", zap.Error(err))
}
```

**2. 非 HTTP 场景（异步任务 / 启动阶段等无 context 场景）**

```go
// 启动阶段、无 trace-id
logger.L().Info("server started", zap.String("addr", ":8080"))

// 持有标准 context.Context 时（context 中带 trace-id 则自动打印）
logger.Info(ctx, "async job done", zap.Int("count", 3))
```

**3. 日志级别**

```go
logger.Debug(ctx, "debug msg", ...)   // 调试
logger.Info(ctx, "info msg", ...)     // 常规
logger.Warn(ctx, "warn msg", ...)     // 警告
logger.Error(ctx, "error msg", ...)   // 错误
```

### trace-id 机制说明

- 中间件 `middleware.RequestID()` 在每个请求入口读取 `X-Request-Id` 请求头：
  - 请求携带 → 透传使用该值
  - 未携带 → 自动生成 32 位十六进制 ID
- trace-id 存入 `gin.Context`，响应头回写 `X-Request-Id`
- 下游调用外部服务时应继续透传该值，保证链路可追踪

## 认证说明

### 认证机制

- 认证方式：请求头携带 `Authorization: Bearer <JWT>`
- 验签方式：**RSA 公钥验签**（仅允许 RS256/384/512，防止算法降级攻击），公钥通过配置注入：

```yaml
# configs/config.yaml
auth:
  jwt-public-key: <base64 编码的 DER 公钥，或 PEM 文本>
```

公钥兼容两种格式：base64 编码的 DER（Java 侧常见输出）和 PEM 文本（含 `-----BEGIN PUBLIC KEY-----` 标记）。

### 拦截规则

- `/api/v1/pub/...`：放行，不校验
- 其余 `/api/v1/...`：强制认证，任何失败（缺失 token / 格式错误 / 验签失败 / token 过期）统一返回：

```
HTTP 401
{ "code": 401, "msg": "Unauthorized", "data": null }
```

### 用户信息解析

认证通过后，中间件从 JWT claims 解析用户信息并写入请求上下文：

| User 字段 | 来源 | 说明 |
|-----------|------|------|
| `UserID` | claims.`staffuserid` | 用户 ID |
| `Name` | claims.`name` | 用户姓名 |
| `Jwt` | 原始 token | 用于向下游服务透传 |
| `Cookie` | 请求头 Cookie | 用于向下游服务透传 |

### 业务代码中使用

**1. Handler 中获取当前用户**

```go
import (
    "env-vault/pkg/response"
    "env-vault/pkg/userctx"
)

func (h *Handler) DoSomething(c *gin.Context) {
    user, ok := userctx.MustFromContext(c)
    if !ok {
        response.AbortWithHTTPStatus(c, 401)
        return
    }
    // user.UserID / user.Name / user.Jwt / user.Cookie
    response.Success(c, user)
}
```

**2. 调用下游服务时透传凭证**

```go
user := userctx.FromContext(c)
req.Header.Set("Authorization", "Bearer "+user.Jwt)
req.Header.Set("Cookie", user.Cookie)
```

**3. 注册新的认证接口**

路由注册在 router 的 `auth` 分组即自动带认证，无需额外处理：

```go
// internal/interfaces/router/router.go
auth := v1.Group("", authMiddleware)
auth.POST("/user/update", userHandler.Update)   // 需认证
```

无认证接口注册到 `pub` 分组即可。

## 开发规范

所有开发必须遵循 [AGENT.md](AGENT.md) 中的完整规范。


## 相关连接

- hashicorp/vault/shamir 算法
