# 2026-10-28 方案与验证记录归档

此目录保存讨论时生成的 Markdown 原稿及可独立打开的 HTML 页面，便于在 Git 中保留设计过程，不再依赖项目外的临时目录

这些文件是当时的快照，不代表所有内容均已实现，后续状态以项目当前代码、TODO 和下面的维护文档为准

| 内容 | Markdown 原稿 | HTML 页面 |
|---|---|---|
| Java SDK 初始方案 | [原稿](env-vault-java-sdk-design-20261028.md) | [页面](env-vault-java-sdk-design-20261028.html) |
| 本地与容器配置加载 | [原稿](env-vault-sdk-local-container-config-20261028.md) | [页面](env-vault-sdk-local-container-config-20261028.html) |
| 应用身份与 gRPC 方案 | [原稿](env-vault-sdk-app-auth-grpc-design-20261028.md) | [页面](env-vault-sdk-app-auth-grpc-design-20261028.html) |
| 待开发能力核对 | [原稿](env-vault-pending-20261028.md) | [页面](env-vault-pending-20261028.html) |
| 搜索实现状态 | [原稿](env-vault-search-status-20261028.md) | [页面](env-vault-search-status-20261028.html) |
| 搜索后续开发顺序 | [原稿](env-vault-search-order-20261028.md) | [页面](env-vault-search-order-20261028.html) |
| 搜索性能核验方案 | [原稿](env-vault-search-performance-plan-20261028.md) | [页面](env-vault-search-performance-plan-20261028.html) |
| 搜索性能核验结果 | [原稿](env-vault-search-performance-results-20261028.md) | [页面](env-vault-search-performance-results-20261028.html) |

## 当前维护入口

- [Java SDK 与应用接入设计](../../java-sdk.md)：`${...}` 引用、本地及容器加载、应用认证的阶段划分
- [gRPC 目录密钥读取](../../grpc-secret-read.md)：已经实现的 RPC 契约、启动和调用方式
- [全局审计日志](../../audit-global.md)：已实现的审计查询与权限边界
- [搜索设计](../../secret-search.md)、[性能方案](../../secret-search-performance.md)、[性能结果](../../secret-search-performance-results-20261028.md)
- [项目待办](../../../TODO.md)：持续维护的未完成工作

初始 SDK 页面使用 HTTP / PAT 举例，应用身份页面提出多目录 `BatchGet`，均属于早期设计讨论；当前实际实现是单目录、单环境的 gRPC `ListSecrets`，应用凭证与授权尚未实现，Java Starter 尚未开发
