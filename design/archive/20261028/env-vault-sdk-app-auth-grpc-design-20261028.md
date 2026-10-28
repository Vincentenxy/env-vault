---
title: 应用身份认证与 gRPC 接入方案
subtitle: Env Vault Java SDK · 设计讨论，尚未实现
lang: zh-CN
cols: 2
---
建议为应用提供独立凭证，并按目录和环境授权。SDK 可采用 gRPC 读取配置。

## A 谁在调用
| 调用方 | 身份凭证 | 服务端识别的主体 |
|---|---|---|
| 浏览器中的登录用户 | 本地登录或公司认证 Token | 用户 |
| 用户脚本 | 现有 PAT | 用户 |
| 部署中的 Java 应用 | 新增应用 Token | 应用 |
现有认证中间件按用户身份和用户锁定状态处理。
应用凭证需要独立的认证入口。
两种身份可以共享认证基础代码。
调用协议与身份类型是两个独立的设计维度。

## B Token 与授权
Token 证明调用方身份，目录 ID 指定目标资源。
知道目录 ID 不代表拥有访问权限。
授权按组合记录，避免扩大环境范围：
```text title="授权示意"
应用：order-service
凭证：部署环境使用的应用 Token
授权：服务目录 + dev + secret:read
授权：global 目录 + dev + secret:read
```
生产环境使用另一份凭证，并单独授权。
应用禁用、凭证到期或撤销后，拒绝新的读取请求。
应用请求不依赖员工浏览器登录会话。
Token 可使用高强度随机值，不强制使用 JWT。
标准化机器认证可在后续接入 OAuth Client Credentials。

## C 目录 ID 如何选择
| 标识 | 当前含义 | SDK 建议 |
|---|---|---|
| folderId | 某个环境的目录实例 | 适合单环境物理定位 |
| folderGroupId | 多个环境共享的逻辑目录 | 配合 envCode 使用 |
建议使用 folderGroupId + envCode。
项目和租户可由服务端从目录关系推导。
请求再带需要读取的 key 列表。
如果应用需要 global 与服务目录，必须分别获得授权。
当前 list 的 folderGroupId 分支会读取全部环境。
应用入口需要先限制环境，再查询和解密。

## D 拟议的读取链路 {span=2}
```flow LR
[Java SDK] -> [gRPC 入口]
[gRPC 入口] -> [主密钥就绪与应用认证]
[主密钥就绪与应用认证] -> [目录与环境授权]
[目录与环境授权] -> [共享配置读取逻辑]
[共享配置读取逻辑] -> [解密并记录审计]
[解密并记录审计] -> [返回 SDK 内存配置]
```
gRPC 只负责协议接入，不复制一套存储和解密代码。
审计主体记录为 service，并记录可信的应用 ID。
HTTP 与 gRPC 的应用读取入口共用授权规则。
现有主密钥未就绪拦截也必须覆盖 gRPC。

## E RPC 契约与失败处理
首版提供 BatchGet，一次读取多个目录中的指定 key。
Token 放入 metadata，不混入业务请求消息。
```text title="调用示意"
metadata:
  authorization: Bearer <应用 Token>
request:
  envCode: dev
  folders:
    - folderGroupId: <目录业务组 UUID>
      keyList: [DB_USERNAME, DB_PASSWORD]
```
| 场景 | 建议 gRPC 状态 |
|---|---|
| Token 无效或已撤销 | UNAUTHENTICATED |
| 未授权的目录或环境 | PERMISSION_DENIED |
| 主密钥未就绪 | UNAVAILABLE，附明确原因 |
| 请求参数有误 | INVALID_ARGUMENT |
SDK 设置单次调用 deadline 和启动总超时。
先批量取齐必需配置，再完成 Spring 配置绑定。

## F 协议收益与部署改动
gRPC 适合明确的 Protobuf 契约和多语言 SDK。
HTTP/2 与连接复用有利于连续调用。
启动读取的实际速度需要对比测试。
数据库查询、解密和审计仍然存在。
首版使用普通批量 RPC，配置订阅留待后续。

| 层级 | 需要补充的内容 |
|---|---|
| Go 后端 | gRPC 服务端、应用认证、授权及审计接入 |
| Java SDK | gRPC 客户端与现有启动解析设计 |
| Kubernetes | gRPC Service 端口与部署配置 |
| 外部访问 | 网关 gRPC 路由及 TLS |
集群内 SDK 可连接后端 Service 的 gRPC 端口。
外部 SDK 经网关连接后端 gRPC 端口。
现有 Web Nginx 的 HTTP/1.1 proxy_pass 无法直接复用。
应用身份、授权、查询和解密应先明确，再接入协议。

参考：[gRPC 认证](https://grpc.io/docs/guides/auth/)、[性能实践](https://grpc.io/docs/guides/performance/)、[超时](https://grpc.io/docs/guides/deadlines/)、[NGINX gRPC 转发](https://nginx.org/en/docs/http/ngx_http_grpc_module.html)、[OAuth Client Credentials](https://www.rfc-editor.org/rfc/rfc6749.html#section-4.4)
