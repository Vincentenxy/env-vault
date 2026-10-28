---
title: SDK 在本地和容器中如何加载配置
subtitle: Spring Boot 3.x · 使用 ${...} · 设计说明
lang: zh-CN
cols: 2
---
本地和容器都在启动时解析引用。实际密钥放入内存，配置文件保留引用。

## A 文件与内存
```callout ok 无需生成替换后的 YAML
引用语法确定为 ${...}。
SDK 注册内存配置源，Spring 再解析应用配置。
src、target 和 JAR 内的 YAML 都保留引用。
```
```yaml title="应用配置示意"
spring:
  datasource:
    password: "${envvault.global.DB_PASSWORD}"
```
SDK 将后端值提供为 envvault.global.DB_PASSWORD。
数据库连接池最终取得实际密码。

## B 本地 IDE 启动 {span=2}
```flow LR
[编辑 src/main/resources 中的 YAML] -> [IDE 或 Maven 复制资源]
[IDE 或 Maven 复制资源] -> [运行时类路径中的 YAML]
[运行时类路径中的 YAML] -> [Spring 加载配置]
[Spring 加载配置] -> [SDK 批量获取密钥]
[SDK 批量获取密钥] -> [内存解析引用并绑定配置]
```
Maven 项目的运行时资源通常位于 target/classes。
IDE 使用的实际输出目录取决于运行配置。
复制资源属于常规构建，SDK 不负责改写输出文件。
只修改源码目录中的 YAML，随后构建并重新运行。

## C 容器中的目录
容器示意：JAR 与 config 目录同属 /app。
```tree
/app
  app.jar | 应用与 SDK
  config
    application.yaml | 可以继续包含 ${...} 引用
```
工作目录为 /app 时，Boot 默认查找 ./config/。
默认查找基于工作目录，并非自动基于 JAR 所在目录。
可显式指定路径，避免工作目录不同造成漏读：
```bash title="容器启动示意"
java -jar /app/app.jar \
  --spring.config.additional-location=file:/app/config/
```
外部配置同样在启动时由 SDK 解析。

## D 改动之后需要做什么
| 改动 | 操作 |
|---|---|
| 本地源码 YAML 的引用或配置项 | 常规资源复制后，重启应用 |
| Env Vault 内已有 key 的值 | 首版只需重启应用，无需重新编译 |
| 容器外部挂载的 YAML | 更新挂载内容后，重启应用 |
| JAR 内打包的 YAML 或 Java 代码 | 重新构建并部署应用 |
不手工编辑 target/classes 中的文件。
正常构建会覆盖该目录。
首版不承诺保存文件后自动刷新已创建的 Bean。

## E 实现约定与依据
本地和部署复用同一套 SDK 启动链路。
两种场景都先提供服务地址、项目、环境和 PAT。
这些参数可以由本地运行配置或部署参数提供。
本轮只补充设计，尚未开发 SDK。

- [Spring Boot 外部配置与加载位置](https://docs.spring.io/spring-boot/3.5/reference/features/external-config.html)
- [Spring Boot 启动前环境扩展](https://docs.spring.io/spring-boot/3.5/api/java/org/springframework/boot/env/EnvironmentPostProcessor.html)
- [Maven 资源复制](https://maven.apache.org/plugins/maven-resources-plugin/)
