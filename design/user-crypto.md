# 对外文本加解密接口

## 范围与使用方式

为外部系统提供两个认证接口：

| 方法 | 路径 | 请求 | 成功返回的 data |
| --- | --- | --- | --- |
| POST | `/api/v1/crypto/encrypt` | `{"plaintext":"待加密文本","decryptMode":"authenticated"}` | `{"ciphertext":"evu:v2:..."}` |
| POST | `/api/v1/crypto/decrypt` | `{"ciphertext":"evu:v2:..."}` | `{"plaintext":"待加密文本"}` |

本版支持业务系统自行授权，以及 EnvVault 内置的本人/指定账号校验。**不保存策略，不增加策略表**；选择的规则随内容一起加密在密文内。创建人 ID 来自认证中间件，不允许请求体传入 userId。通用加解密作为独立的 `crypto` 接口分组，两种操作均强制认证，不提供 `pub` 入口，也不保留旧 `/api/v1/user/crypto` 路由。此分组不创建新的 Secret、Folder 或独立密钥资源。

推荐流程：业务系统选择 `authenticated` 加密并保存完整密文；业务后端判断查看权限后，用有效 Token 调用解密。授权名单稳定的场景可选择 `self` 或 `users`，由 EnvVault 按调用账号校验。所有生产调用使用 HTTPS。接口响应使用项目的 `{code,msg,data}` 结构，成功为 `code=0`；不能只根据 HTTP 200 判断成功。

- 已接入现有 Bearer JWT / PAT 认证。PAT 的撤销、过期及账号锁定沿用现有认证逻辑。
- `auth.local.enabled=false` 只停用本地密码登录和普通本地登录 JWT，PAT 仍可使用；EnvVault 的 RSA 签名配置仍必须保留，具体见 [认证配置](authentication.md#个人-token-与登录方式的边界)。
- 身份按现有系统的稳定 userId 区分，不按 Token 内容、昵称或租户区分。同一账号换 Token、改名、调整组织后仍能解密。不同认证来源仅在现有系统认定为同一 userId 时互通。
- `authenticated` 允许所有通过现有认证的账号解密，包括跨租户账号，不是匿名访问。当前接口已接认证、PAT 校验和账号锁定检查，尚未接入细粒度接口授权；业务系统负责限制最终查看人员。
- 调用方保存密文。服务端只执行加解密和写入脱敏审计，不保存业务原文或密文。
- 一个账号的所有有效凭证共享该账号的加解密能力。业务系统若用同一个服务账号代理多人请求，`self/users` 判断的是服务账号，不是最终查看人员。本版没有 Token 级细粒度授权、独立应用密钥、密文逐条撤销或自动过期。
- 本版只增加 Go 后端能力和接口文档，不修改前端页面或 Java SDK。外部系统可以直接发 HTTP 请求。

## 请求和响应

认证头：`Authorization: Bearer <有效Token>`，内容类型：`Content-Type: application/json`。

| 加密字段 | 必填 | 说明 |
| --- | --- | --- |
| `plaintext` | 是 | 待加密字符串，允许空串，最多 65,536 UTF-8 字节 |
| `decryptMode` | 否 | `self`（默认）、`authenticated`、`users` |
| `allowedUserIds` | 条件必填 | `users` 模式必须提供 1–100 个账号 ID；其他模式不得携带非空列表 |

| 解密范围 | 规则 |
| --- | --- |
| `self` | 仅加密时的账号可解密，换 Token 不影响 |
| `authenticated` | 任意通过现有认证的账号可解密，由业务后端决定谁能查看 |
| `users` | 仅列表内的账号可解密，创建人也必须在列表中才有权限 |

账号使用稳定 userId，不使用姓名；每个 ID 最多 128 UTF-8 字节，不允许空串、首尾空白或控制字符。重复 ID 会去重；不查询账号是否已存在，因此调用方应通过用户接口取得正确 ID。省略 `decryptMode` 或传空串时仍按 `self`，不会自动开放共享。

指定账号的加密示例：

```json
{
  "plaintext": "hello env-vault",
  "decryptMode": "users",
  "allowedUserIds": ["user-id-001", "user-id-002"]
}
```

加密：

```http
POST /api/v1/crypto/encrypt
Authorization: Bearer <有效Token>
Content-Type: application/json

{"plaintext":"hello env-vault","decryptMode":"authenticated"}
```

```json
{
  "code": 0,
  "msg": "success",
  "data": {"ciphertext": "evu:v2:<实际返回的密文>"}
}
```

解密时，将上一步的 `data.ciphertext` 完整传回：

```http
POST /api/v1/crypto/decrypt
Authorization: Bearer <符合密文规则的账号Token>
Content-Type: application/json

{"ciphertext":"evu:v2:<实际返回的密文>"}
```

```json
{
  "code": 0,
  "msg": "success",
  "data": {"plaintext": "hello env-vault"}
}
```

解密请求只接受 `ciphertext`，不能传入新的 `decryptMode` 或名单覆盖原规则。需要修改 `self/users` 的范围时，由当前有权解密的账号获取原文后重新加密，再替换业务系统中的密文；原来的密文不会因此失效。人员经常变化的场景应使用 `authenticated` 并在业务系统授权。

`plaintext` 必须是 JSON 字符串，允许空串，保留有效 UTF-8 文本中的空格、换行和控制字符，最多 65,536 字节；不是 65,536 个汉字。二进制文件需由调用方先转成 Base64 文本，转换后仍受此限制，本版不是文件加密接口。缺少必填字段、必填字段为 null、字段类型错误、未知字段或多个 JSON 值返回 400；范围非法或名单不符合上述规则返回业务码 -1。请求体中的非法 UTF-8 字节和未配对的 Unicode 代理项（如 `\ud800`）在 JSON 解码前拒绝并返回 400，不会静默替换字符；合法代理对、显式 U+FFFD 和转义后的字面文本仍正常保留。

密文是不可拆分的字符串，v2 长度上限 349,613 个 ASCII 字符（包含加密后的规则），v1 上限仍为 87,426。请求体上限 512 KiB，包含 JSON 转义开销。只接收 JSON 请求体，不通过 URL 参数传递原文或密文。成功及 Handler 产生的错误响应均带 `Cache-Control: no-store, private`。

## 可运行示例

准备 `ENV_VAULT_TOKEN` 环境变量，填入有效 Token；API 基址按实际后端部署调整，本地示例使用 `http://localhost:8090/api/v1`。示例依赖 curl 和 jq，仅使用演示数据。

```bash
ENV_VAULT_API_BASE=http://localhost:8090/api/v1

encrypt_response=$(curl --fail-with-body --silent --show-error \
  "$ENV_VAULT_API_BASE/crypto/encrypt" \
  -H "Authorization: Bearer $ENV_VAULT_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"plaintext":"hello env-vault","decryptMode":"authenticated"}')

# 业务失败也可能是 HTTP 200，必须检查 code。
ciphertext=$(printf '%s' "$encrypt_response" | \
  jq -er 'if .code == 0 then .data.ciphertext else error(.msg) end') || exit 1

decrypt_response=$(jq -n --arg text "$ciphertext" '{ciphertext:$text}' | \
  curl --fail-with-body --silent --show-error \
    "$ENV_VAULT_API_BASE/crypto/decrypt" \
    -H "Authorization: Bearer $ENV_VAULT_TOKEN" \
    -H 'Content-Type: application/json' --data-binary @-)

printf '%s' "$decrypt_response" | \
  jq -er 'if .code == 0 then .data.plaintext else error(.msg) end'
```

生产调用方应避免把响应体、Authorization、原文和密文写入调试日志、监控采样或错误上报。该示例最后一行只适合验证演示文本。

## 密码实现与恢复

- 使用 Go 标准库 AES-256-GCM，提供机密性和完整性校验；随机 nonce 由 `cipher.NewGCMWithRandomNonce` 生成。同一原文多次加密会产生不同密文。
- 新加密请求统一生成 v2：每条密文生成 32 字节随机 salt，以 HKDF-SHA256 从主密钥派生独立的 32 字节密钥，info 固定为 `env-vault/user-crypto/v2`。主密钥不会返回给调用方。
- v2 格式：`evu:v2:` 加上无填充 Base64URL 编码的 `salt(32 bytes) || nonce(12 bytes) || sealed(含16字节tag)`。前缀作为 GCM 附加认证数据。sealed 内同时保护创建人、解密范围、允许的账号列表和内容。内部 JSON 的内容字段使用 Base64，避免控制字符导致过度转义。调用方将整个值视作不透明字符串，不自行拼装。
- 先校验 GCM 完整性，再解析并执行内置规则，最后返回原文。salt、nonce、加密的内容/规则或校验标签被篡改、截断都将导致失败；更改版本前缀也无法绕过检查。无需另加可被攻击者重新计算的普通哈希。
- 完整性校验能发现损坏，不能恢复损坏的数据；恢复需要原密文或备份。它也不防止将整段密文替换成另一段合法密文或重放旧密文；与业务记录的对应关系和版本仍由调用方管理。
- 保留 v1 解密兼容：原格式仍按认证账号派生密钥，仅本人可解密，不能因升级扩大权限。新 API 不再生成 v1。
- 此派生用途与 Secret 数据库存储密钥隔离，禁止直接使用现有 `Manager.Decrypt` 对外解密。数据库 JSON 密文和其他版本前缀都会被拒绝；重包装数据库密文也无法通过认证。
- 密文解析检查长度、格式和认证标签；篡改、未获授权、错误主密钥统一返回 `invalid ciphertext or current user is not allowed to decrypt`，不返回中间数据。
- 重启、跨实例调用依赖恢复**同一主密钥**，调用人需符合密文原有规则，无需保存派生密钥或策略。主密钥未就绪时沿用 `code=-2`。
- 主密钥恢复沿用现有分片/实例恢复机制。丢失原主密钥后无法解密旧密文；更换账号 ID 会影响 `self/users` 授权。不要把换 Token 与换主密钥混淆。
- 现有主密钥管理器不支持在线轮换。`v1/v2` 是格式版本，不是可选择的密钥版本；没有增加轮换、旧密钥保留或批量重加密机制。v2 为每条密文生成独立随机派生密钥；大规模调用的限流、容量及监控仍需部署侧配置。

实现依据：[Go AES-GCM API](https://pkg.go.dev/crypto/cipher#NewGCMWithRandomNonce)、[Go HKDF API](https://pkg.go.dev/crypto/hkdf)。

## 审计与错误

复用现有审计表，不新增数据库表；部署环境必须已具备现有审计表。

- `resourceType=user`，`resourceId=当前userId`，`scopeType=user`。
- 操作为 `user.crypto.encrypt` / `user.crypto.decrypt`。
- 记录操作人、请求追踪信息、结果、输入字节数；不记录原文、密文、允许账号列表、派生密钥、Token 或未筛选的异常内容。
- 加解密审计中的账号和姓名来自已认证身份；Token 未携带姓名时仅记录账号，不额外查询用户缓存或数据库来补齐姓名，避免昵称查询影响审计写入的超时预算。
- 只有审计写入成功才返回加解密结果；审计失败返回 `code=-1, msg=internal error, data=null`。
- HTTP 请求的取消和截止时间会传递到审计数据库操作；加解密审计另有 5 秒上限，以较早的截止时间为准。取消或超时同样不返回加解密结果。
- 绑定失败沿用项目约定，不写业务审计。认证失败沿用认证中间件的安全审计。

| HTTP | code | 含义 |
| --- | --- | --- |
| 200 | 0 | 成功 |
| 200 | -1 | 明文超限、解密规则非法、密文无效/无权解密或内部错误；data 为 null |
| 200 | -2 | 主密钥未就绪，系统启动中 |
| 400 | 400 | JSON 或字段类型错误、缺少必填字段 |
| 401 | 401 | 未认证或 Token 不可用 |
| 403 | 403 | 当前账号被锁定 |
| 413 | 413 | JSON 请求体超过 512 KiB |
| 500 | 500 | 认证依赖不可用等现有中间件错误 |

错误响应仍为 `{code,msg,data}`。所有失败都不应继续消费 data 字段。

## 验证与后续边界

测试覆盖三种范围、默认仅本人、名单校验、创建人无隐式特权、解密请求不得覆盖规则、v1 兼容、逐字节篡改及截断、原文往返（空串、中文、空白、换行、最大长度）、随机密文、重建实例恢复、错误账号/主密钥、未知版本、内部密文重包装隔离、真实 JWT 中间件、请求体限制、主密钥未就绪、审计脱敏及审计失败不返回结果。

本版确认采用无策略存储的方式，业务系统负责动态授权，EnvVault 的内置规则用于人员范围较稳定的场景。批量加解密、应用密钥管理、轮换和按密文撤销均不在本版。
