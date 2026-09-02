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
│   ├── auth/                            # 本地 JWT 密钥和密码哈希离线工具
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

镜像不包含 `configs/config.yaml`，也不包含数据库密码、JWT 私钥或 Secret 主密钥。运行容器时必须挂载运行配置，或通过环境变量覆盖配置；这样可以避免凭证进入镜像层。

### Kubernetes 三副本部署

三副本 StatefulSet 清单位于 [deploy/k8s/env-vault-statefulset.yaml](deploy/k8s/env-vault-statefulset.yaml)。安装前必须完成以下修改：

- 将 `env-vault-runtime` 和 `env-vault-jwt` 中的 `REPLACE_ME` 替换为真实凭证；这些占位值不能直接用于生产环境。
- 将 ConfigMap 中的 PostgreSQL、Redis 服务地址改为实际地址，并确认 `ssl_mode` 与数据库配置一致。
- 将 StatefulSet 的 `image` 改为集群可拉取的镜像地址；使用私有镜像仓库时追加 `imagePullSecrets`。
- 3 个副本会通过 `topologySpreadConstraints` 分散到 3 个节点，因此集群至少需要 3 个可调度节点。

构建并推送镜像后应用清单。目标集群为 `linux/amd64`，在 Apple Silicon Mac 上必须显式指定目标平台，避免最终 Alpine 和 Go 二进制使用不同架构。每次部署使用新的不可变标签，防止 `imagePullPolicy: IfNotPresent` 继续使用节点上的旧镜像：

```bash
docker login harbor.gtjaqh.net
docker buildx build \
  --platform linux/amd64 \
  --build-arg BASE_IMAGE_REGISTRY=m.daocloud.io/docker.io \
  --build-arg GOPROXY=https://goproxy.cn,direct \
  -t harbor.gtjaqh.net/lucy-dev/env-vault:0.0.1-alpha.2 \
  --push \
  .

# 输出中必须包含 linux/amd64
docker buildx imagetools inspect harbor.gtjaqh.net/lucy-dev/env-vault:0.0.1-alpha.2

# 将 YAML 中的 image 修改为上述新标签后执行
kubectl apply -f deploy/k8s/env-vault-statefulset.yaml
kubectl rollout status statefulset/env-vault -n env-vault
kubectl -n env-vault get pods -o wide
```

`BASE_IMAGE_REGISTRY` 只控制 Dockerfile 使用的 Go 和 Alpine 基础镜像来源，默认值仍是 `docker.io`；`GOPROXY` 控制构建阶段的 Go 模块下载地址，默认值仍是 `https://proxy.golang.org,direct`。上面的命令通过 DaoCloud 和 goproxy.cn 处理国内网络下的公共依赖下载。本地已经存在同名镜像并不代表可以复用：Apple Silicon Mac 默认缓存的是 `linux/arm64`，构建集群镜像需要单独获取 `linux/amd64` 变体。

清单提供三个集群内地址：

- `env-vault.env-vault.svc.cluster.local:80`：正常业务流量，只选择 readiness probe 已通过的副本。
- `env-vault-headless.env-vault.svc.cluster.local:8090`：StatefulSet 的 Headless Service，用于后续 Pod 间发现。
- `env-vault-bootstrap.env-vault.svc.cluster.local:8090`：只指向 `env-vault-0`，用于启动阶段的登录、健康检查和主密钥分片提交；Ingress 只转发明确列出的启动接口，不对外暴露其他路径。

主密钥未激活时，业务接口仍由 Ready 中间件拦截。当前 StatefulSet 的 readiness probe 调用内部 `/internal/v1/masterKey/ready`，只有主密钥激活后副本才会加入正常业务 Service；Ingress 将 `/api/v1/pub/**` 和 `/api/v1/masterKey/**` 转发到只选择 Pod 0 的 bootstrap Service，其他 API 仍使用正常业务 Service。详细部署约束见 [design/deploy.md](design/deploy.md)。

健康检查：`GET /api/v1/pub/health`

## 本地认证初始化

EnvVault 本地认证使用独立 RSA 密钥签发 RS256 JWT，用户密码使用 Argon2id 哈希。JWT 私钥、用户密码和 Secret 主密钥是三类彼此独立的凭证，禁止复用。

### 生成 JWT 密钥

首次安装时离线生成一套长期使用的本地 JWT 密钥：

```bash
go run ./cmd/auth keygen --out-dir .local/auth
```

该命令默认生成 3072 位 RSA 密钥，私钥使用 PKCS#8 PEM 格式，公钥使用 PKIX PEM 格式。生成结果为 `jwt-private.pem` 和 `jwt-public.pem`，私钥权限为 `0600`，公钥权限为 `0644`，且拒绝覆盖已有文件。需要指定其他位数时可以使用 `--bits`，但不能低于 2048 位：

```bash
go run ./cmd/auth keygen --out-dir .local/auth --bits 4096
```

可以使用 OpenSSL 检查文件格式和私钥完整性，命令成功且没有错误输出即表示校验通过：

```bash
openssl pkey -in .local/auth/jwt-private.pem -check -noout
openssl pkey -pubin -in .local/auth/jwt-public.pem -noout
```

没有 Go 环境时，也可以直接使用 OpenSSL 生成等价的密钥文件。以下命令会覆盖同名文件，执行前必须确认目标目录中没有仍在使用的密钥：

```bash
mkdir -p .local/auth
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 \
  -out .local/auth/jwt-private.pem
openssl pkey -in .local/auth/jwt-private.pem -pubout \
  -out .local/auth/jwt-public.pem
chmod 600 .local/auth/jwt-private.pem
chmod 644 .local/auth/jwt-public.pem
```

`.local/` 已被 Git 忽略；生产部署应把私钥放入 Kubernetes Secret，并通过挂载文件或环境变量注入。所有实例必须使用同一套密钥，不能在每次启动时重新生成。重新生成密钥后，旧私钥签发的 JWT 将无法通过新公钥验证。

默认开发配置读取：

```yaml
auth:
  local:
    private_key_file: ./.local/auth/jwt-private.pem
    public_key_file: ./.local/auth/jwt-public.pem
```

### 初始化用户密码

本地登录要求 `user_info.username` 全局唯一，并且 `password_hash` 已保存 Argon2id PHC 字符串。先执行 [design/database.md](design/database.md) 中的唯一索引：

```sql
CREATE UNIQUE INDEX IF NOT EXISTS uk_user_info_username_active
    ON user_info (lower(username))
    WHERE is_deleted = false AND username <> '';
```

通过环境变量向离线工具提供密码，避免把明文密码作为命令行参数传递。根据当前操作系统和 Shell 选择对应命令。

Windows PowerShell：

```powershell
$env:ENV_VAULT_PASSWORD = '<待设置密码>'
$passwordHash = go run ./cmd/auth hash-password
Remove-Item Env:ENV_VAULT_PASSWORD
$passwordHash
```

macOS `zsh`：

```zsh
read -s "ENV_VAULT_PASSWORD?请输入密码: "
echo
export ENV_VAULT_PASSWORD
passwordHash="$(go run ./cmd/auth hash-password)"
unset ENV_VAULT_PASSWORD
printf '%s\n' "$passwordHash"
```

Linux `bash`：

```bash
read -rsp '请输入密码: ' ENV_VAULT_PASSWORD
echo
export ENV_VAULT_PASSWORD
passwordHash="$(go run ./cmd/auth hash-password)"
unset ENV_VAULT_PASSWORD
printf '%s\n' "$passwordHash"
```

将输出的完整 PHC 字符串写入目标用户，不要修改字符串中的 `$`：

```sql
UPDATE user_info
SET password_hash = '<工具输出>', update_by = 'bootstrap', update_at = now()
WHERE lower(username) = lower('<登录用户名>') AND is_deleted = false;
```

`password_hash = ''` 表示未启用本地密码登录。登录接口为 `POST /api/v1/pub/auth/login`，请求只包含全局 `username` 和 `password`。公司统一认证启用后使用公司公钥作为第二个受信任签发方，不需要也不能使用公司的私钥。

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

3. 打开前端 `http://localhost:5173/login`，使用已经初始化密码的用户登录。
4. 系统未就绪时会进入等待页面。点击右上角密钥图标，从同一批次的 5 份分片中任选 3 份，每次提交一个完整 `EVS1` Token。
5. 第三份有效分片提交后，状态变为 `ready=true`、`source=shares`，前端自动进入业务页面。

### 分片保管要求

- 5 份分片应分别交给不同管理员或存放在相互独立的安全位置。
- 任意 3 份分片可以恢复主密钥，少于 3 份无法恢复；不得把 3 份及以上分片保存在同一位置。
- 不要在 CI、工单、聊天记录、终端录屏或应用日志中输出分片。
- 重新执行工具会产生新的分片批次，不同批次的分片不能混用。
- 分片丢失导致可用数量少于 3 份时，主密钥和已有密文将无法恢复。

详细启动设计见 [design/master-key-loading.md](design/master-key-loading.md)。

### 测试集群内部主密钥传输

`tools/master_key_transfer_test.py` 是一个不落盘的 Python 测试客户端。它会在内存中生成一次性 RSA-2048 密钥对，调用 `/internal/v1/masterKey/transfer`，使用临时私钥解密响应并校验主密钥指纹。需要 Python 3.9 或更高版本以及 `cryptography`：

```powershell
python -m pip install -r tools/requirements-master-key-test.txt
$env:SECURITY_MASTER_KEY_PEER_TOKEN = '<与服务端相同的内部令牌>'
python tools/master_key_transfer_test.py
Remove-Item Env:SECURITY_MASTER_KEY_PEER_TOKEN
```

默认服务地址为 `http://localhost:8090/internal/v1/masterKey/transfer`。服务端必须已经激活主密钥，并通过 `SECURITY_MASTER_KEY_PEER_TOKEN` 配置内部令牌。脚本默认只验证解密结果长度和指纹；如果需要进一步确认它对应当前测试密钥，可以临时设置 `ENV_VAULT_MASTER_KEY`，脚本会做常量时间比对，但不会打印该密钥：

```powershell
$env:ENV_VAULT_MASTER_KEY = '<当前 security.encryption_key 的 Base64 值>'
python tools/master_key_transfer_test.py
Remove-Item Env:ENV_VAULT_MASTER_KEY
```

内部令牌和主密钥都不要作为命令行参数传递，以免进入 Shell 历史。该客户端只用于当前阶段的内部令牌认证测试，后续切换 mTLS 后仍可复用请求体和解密校验逻辑。

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
