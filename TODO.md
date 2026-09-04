# 待办事项

- [ ] 部署一套高可用pgsql: [cnpg](https://cloudnative-pg.io/documentation/1.20/)

- [ ] 生产入口将 ingress-nginx Service从单节点域名加 NodePort切换为 LoadBalancer、虚拟 IP或其他多节点入口。当前 Controller已有三个跨节点副本，但 `efficient-poc.qiuer.net` 只解析到一个控制节点，该节点不可达时外部流量仍会中断。

- [ ] `/api/v1/user/allocate` 执行 `remove` 前，检查用户负责的租户、组织、项目和文件夹等资源；必须先将管理员职责移交给其他用户后才能移除。当前应用层已预留 `ResponsibilityChecker` 扩展点，待负责人移交规则和上下级关系确定后实现。

- [ ] 增加用户锁定/解锁接口。更新 `user_info.is_blocked` 后必须同步刷新 Redis `user:blocked` 状态缓存；多实例部署时还需保证各实例及时观察到状态变化。

- [ ] `/api/v1/secret/history` 接入环境权限模型。当前请求已传递用户 ID，历史分页、批次和分组目标查询均预留 SQL 权限 scope；权限表落地后在 scope 中过滤无权环境，并与 `envList` 条件取交集。

- [ ] 为个人访问令牌（PAT）增加细粒度权限范围。后续在创建页面增加权限设置入口，后端增加 scope 模型，并在认证后按路由和资源执行授权校验；当前 PAT 权限与所属用户一致。

## 项目协作与分享

- [ ] 开发项目分享入口和分享链接接受流程。当前仅完成项目用户关系的有效期模型与协作项目展示，不提供同租户全部用户搜索，也不提供主动邀请页面。
- [ ] 接入独立权限中心，落地项目读取、Secret 读取/修改、成员管理和分享管理权限。后端每个资源接口必须执行权限校验，不能只依赖前端隐藏项目；过期关系必须拒绝访问。
- [ ] 将分享范围从项目级细化到环境、文件夹和操作类型，并在权限中心维护最终授权范围；前端根据授权范围控制可见环境、目录和操作入口。
- [ ] 补全分享审计动作：邀请、接受、续期、撤销和到期。审计详情需记录邀请人、被邀请人、项目及授权范围，但禁止记录任何 Secret 值；到期事件由后续定时任务或权限中心产生。
- [ ] 增加协作关系到期通知和可选清理任务。到期关系默认保留且 `is_deleted = false`，便于续期和审计；仅主动撤销时软删除。
- [ ] 权限中心接入后增加资源管理员和超管的协作关系列表、查询与撤销能力，并覆盖已删除资源和已过期关系的审计查询。

## 审计日志后续能力

审计事件已经由后端统一写入 `audit_event_log`，租户、组织、项目、文件夹、环境、共享 Secret、用户分配、登录认证、主密钥和个人密钥等操作均有对应事件模型。当前前端只完成部分资源的局部查看，以下入口和查询能力仍待开发：

- [ ] 个人密钥 `personalSecret` 操作记录入口。在个人信息页面的个人密钥区域增加“操作记录”查看，区别于现有的个人密钥历史版本；至少展示创建、修改、删除、查看和历史版本读取记录，禁止展示明文或密文。

- [ ] 用户 `user` 操作记录入口。在用户管理页面增加用户资料修改、用户列表查询和成员分配的操作记录查看；成员分配记录应关联到实际租户、组织或项目资源。

- [ ] 主密钥 `masterKey` 操作记录入口。在主密钥管理页面增加状态查询、分片提交、分片校验失败和恢复失败的操作记录查看；绝不展示密钥分片内容、主密钥或其派生材料。

- [ ] 登录与认证 `auth` 日志管理页面。为登录成功、密码错误、Token 无效、用户锁定、认证失败等事件提供管理端查询入口，不在登录页面直接展示审计历史。

- [ ] 已删除资源的日志查看。资源软删除后卡片入口会消失，日志必须继续保留；需要通过全局日志中心、资源回收站或资源 ID 查询已删除租户、组织、项目、文件夹和 Secret 的历史操作。

- [ ] 全系统统一审计日志中心。增加跨资源分页查询页面，支持按资源类型、资源 ID、操作动作、操作人、结果、时间范围、批次 ID和请求关联 ID筛选，并支持查看失败原因和安全的字段变更详情。

- [ ] 为统一审计日志中心补充后端全局查询接口。当前 `/api/v1/audit/list` 要求 `resourceType + resourceId`，全局查询需要独立的分页输入和索引友好的筛选条件；接入独立权限管控系统后，再按 `scopeType + scopeId` 过滤资源管理员和超管可见范围。

- [ ] 审计日志查询自身是否记录 `audit.read`。确定全局日志中心上线后的查询行为是否需要留下“谁查看了哪些审计记录”的二次审计事件，并避免因记录查询日志造成递归写入。

## 主密钥后续能力

- [ ] 按需将 Web Nginx的 API代理能力拆分为独立 `env-vault-gateway`。Gateway 使用独立镜像、Deployment 和 Service，统一负责 `/api/**` 到普通 `env-vault` Service 的代理及 502、503、504时到 `env-vault-bootstrap` 的启动回退；`env-vault-web` 只保留 Vue静态资源，Ingress按 API和页面路径分流。拆分时必须保持同域名访问、`code=-2` 语义、三副本高可用、健康探针和 PodDisruptionBudget。



EnvVault 主密钥分片（共 5 份，恢复需要任意 3 份）
EVS1.eyJrZXlTZXRJZCI6ImZiNDg5Yzc1LWJkMTYtNDk1Ny05MWVlLWNjNGUwODM5MDZjYSIsImluZGV4IjoxMjEsImRhdGEiOiJYZFRGOVNONXJlVHJGSDZHL2xqc2QySmFGZlpDM01nVlNpYzBrQ1ZnYmdONSIsImNoZWNrc3VtIjoiOTVhMmQwMDM0Yjk1ZTFjNzZkYzRjNGRkMTIxYTE0ODE2OGZjYjNlMGFmYzdkNzJmZTI2NWI3OWIxMWVjZjcxMCJ9
EVS1.eyJrZXlTZXRJZCI6ImZiNDg5Yzc1LWJkMTYtNDk1Ny05MWVlLWNjNGUwODM5MDZjYSIsImluZGV4IjoyMzMsImRhdGEiOiJTL1hhditrd0huVVJlYytUWHVqajc4YXlzQ05ZclpNRWZLRlo4Q09HQXpIcCIsImNoZWNrc3VtIjoiMDI4OGQ4NmM0Y2M1NDcyNmRkYTgzYTUyZjg5YzdmMGUwMjc5MzNlZTI3ZThiNjRkNmQxNTlhNWUxYjY5NzFhMiJ9
EVS1.eyJrZXlTZXRJZCI6ImZiNDg5Yzc1LWJkMTYtNDk1Ny05MWVlLWNjNGUwODM5MDZjYSIsImluZGV4IjoxODAsImRhdGEiOiI3ZUpvV0JUVFBoY3F5Vng4MzkxRFBhQTcxVEdKdzN0TWdxcU03eXBmS0M2MCIsImNoZWNrc3VtIjoiZGQyMDM2OTc2Mjg3NDkwZGNkZDJiZDczOWU5YWM5NzhiMWQyMDNjMDRlNmQ3OGJkMzg0ZDlhMzhlYjUyZmI5MyJ9
EVS1.eyJrZXlTZXRJZCI6ImZiNDg5Yzc1LWJkMTYtNDk1Ny05MWVlLWNjNGUwODM5MDZjYSIsImluZGV4Ijo5NCwiZGF0YSI6IjNiZmpWTGxzMHJQRmR1b1JtRGpueXBSKzVmeE4xWGxHc2tOWXpPaFQ3OEplIiwiY2hlY2tzdW0iOiI2NjdkNmE0MzRhYjM2YWYzMzc3MzNlZjg1ODNjYWVkZjJjZTFlYjg2N2JlMmI5NDg2NWJkNGFhOWJiODZlN2NmIn0
EVS1.eyJrZXlTZXRJZCI6ImZiNDg5Yzc1LWJkMTYtNDk1Ny05MWVlLWNjNGUwODM5MDZjYSIsImluZGV4Ijo4MywiZGF0YSI6InNqRUpMNGVkU3E3OWFnMVFPMVBEM3BvRXJyQjBQeTVhLzI2R0phTEk1YWRUIiwiY2hlY2tzdW0iOiJlMDkwMDc2ODA0OGI1NGRhOTBjMTljNzFiM2E5OWIwY2MxMDIxMjFkZWFkNTU5ZGQ2ZjZmZTQzMmNmZTA0NWFkIn0
