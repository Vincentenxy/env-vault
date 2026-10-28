---
title: Java SDK 启动配置解析方案
subtitle: Spring Boot 3.x · 方案分析，尚未开发
lang: zh-CN
cols: 2
---
使用 SDK 在内存中补齐 Spring 配置。应用 JAR 和原始 YAML 文件无需改写。

## A 接入方式
```callout ok 推荐 Spring 内存配置源
应用引入 Starter，声明引用，并配置服务地址、项目、环境和 Token。
SDK 在创建业务 Bean 之前取齐配置。
数据库连接池等模块随后读取解析后的配置。
```
远端当前返回密钥键值，并非完整 YAML 文件。

## B 引用语法
建议沿用 Spring 的配置引用语法。
```yaml title="接入配置示意 · 尚未实现"
envvault:
  server-url: "${ENV_VAULT_SERVER_URL}"
  project-id: "${ENV_VAULT_PROJECT_ID}"
  env-code: "${ENV_VAULT_ENV}"
  token: "${ENV_VAULT_TOKEN}"
spring:
  datasource:
    username: "${envvault.global.DB_USERNAME}"
    password: "${envvault.global.DB_PASSWORD}"
```
`global` 表示当前项目的目录 code。
`DB_PASSWORD` 按密钥 key 精确匹配。
项目和环境从 SDK 配置读取。
`#{global.KEY}` 可通过自定义解析支持。
它与 Spring 表达式语法重叠，且在 YAML 中必须加引号。

## C 启动链路 {span=2}
```flow LR
[加载生效的本地配置] -> [收集引用并去重]
[收集引用并去重] -> [按目录批量取值]
[按目录批量取值] -> [校验必需值]
[校验必需值] -> [注册内存配置源]
[注册内存配置源] -> [绑定配置并创建 Bean]
```
首版使用 EnvironmentPostProcessor。
执行顺序排在 Boot 的 ConfigData 加载之后。
保留命令行、环境变量和配置文件的覆盖顺序。
读取属性时使用内存快照，不再请求后端。
首版不支持使用远端引用决定 Profile 或配置导入路径。

## D 复用现有接口
同一目录的 key 合并为一次请求。
```http title="已有接口的调用示意"
POST /api/v1/secret/list
Authorization: Bearer <PAT>
```
```json title="已有请求结构"
{
  "projectId": "<项目 UUID>",
  "folderCode": "global",
  "envList": ["dev"],
  "keyList": ["DB_USERNAME", "DB_PASSWORD"]
}
```
从 data.secretList 按 key 找到记录。
读取 values.dev.value，保留 version 作为诊断信息。
当前接口可满足首版需求。

## E 首版行为建议
| 场景 | 建议行为 |
|---|---|
| key 或目标环境不存在 | 停止启动并指出目录、环境和 key |
| key 存在，值为空字符串 | 保留空值，由配置校验决定是否合法 |
| 出现重复且无法确定的记录 | 报错，不随机选值 |
| HTTP 200 且 code=-2 | 按启动等待策略重试 |
| 网络异常或暂时性服务错误 | 有限重试，受总超时限制 |
| HTTP 401、403 或其他业务错误 | 明确失败，不无限重试 |
| 远端值更新 | 当前进程保持快照，重启后重新加载 |
日志记录请求标识、目录、key 和耗时。
日志不输出 Token 和密钥明文。

## F SDK 模块
```tree
env-vault-client
  env-vault-client-core | HTTP、认证、响应校验、批量读取
  env-vault-spring-boot-starter | 启动接入、引用发现、内存配置源
```
应用只需引入 Starter 并配置引用。
PAT 使用现有 Bearer 认证。
服务地址、环境、项目和 PAT 必须先在本地可用。
SDK 无需取得 AES 主密钥或密钥分片。

## G 什么时候生成外部文件
| 应用读取方式 | 接入建议 |
|---|---|
| Spring Environment、配置绑定 | 内存配置源 |
| 第三方库直接读取原始 YAML 文件 | JVM 启动前生成外部文件 |
| 需要完整的远端配置源 | 后续考虑 ConfigData 导入扩展 |
外部文件方案需要在 Spring 读取配置之前完成。
文件应通过 YAML 序列化生成，保留类型与特殊字符。
挂载到绝对路径 /config 时可显式指定：
```bash title="外部文件方案示意"
java -jar app.jar --spring.config.additional-location=file:/config/
```
Boot 默认目录包含工作目录下的 ./config/。
它与根目录 /config 并不总是同一个位置。

## H 设计依据
- [Spring Boot 3.5 环境扩展接口](https://docs.spring.io/spring-boot/3.5/api/java/org/springframework/boot/env/EnvironmentPostProcessor.html)
- [Spring Boot 启动前自定义环境](https://docs.spring.io/spring-boot/3.5/how-to/application.html#howto.application.customize-the-environment-or-application-context)
- [Spring 配置引用与表达式](https://docs.spring.io/spring-framework/reference/core/beans/annotation-config/value-annotations.html)
- [Spring Boot 外部配置及加载位置](https://docs.spring.io/spring-boot/3.5/reference/features/external-config.html)

本轮只完成方案分析。
实现前还需确定引用语法和具体 Boot 小版本的验证范围。
