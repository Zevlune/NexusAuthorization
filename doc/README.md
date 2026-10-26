# NexusAuthorization 文档

以当前代码实现为准，整理日期：2026-10-16。

## 使用、初始化与部署

| 文档 | 内容 |
| --- | --- |
| [标准使用手册](user-guide.md) | 登录、选择应用、关联服务、API 与菜单、角色授权、分配用户、权限模拟、常见问题 |
| [运维操作手册](operations-manual.md) | 首次部署、Compose、环境变量、升级、备份恢复、排障和发布验收 |
| [快速开始](quick-start.md) | 本地体验、源码启动与首次上线入口 |
| [数据库初始化](../migrations/README.md) | 创建 `nexus-authorization` 空库并执行完整初始化 SQL |
| [部署与环境变量](operations-manual.md) | 首次部署、Compose、OIDC、服务目录、SCIM、Redis 开关和运行检查 |
| [NexusAuth SCIM 用户目录](nexusauth-scim-users.md) | 用户列表只读接入、分页与角色分配 |

## 实现与设计

| 文档 | 内容 |
| --- | --- |
| [系统设计说明](system-design.md) | 总体架构、权限模型、运行时鉴权、缓存一致性、安全边界和扩展点 |
| [开发与二次开发](development-guide.md) | 本地开发、代码分层、测试、数据库演进、SDK/前端扩展和发布构建 |
| [功能与数据模型](current-system-overview.md) | 当前功能、实际导航、数据流、保存即生效与删除语义 |
| [完整表结构](current-database-schema.md) | 与 `migrations/001_init.sql` 对应的表、字段、约束和触发器 |
| [应用边界 RBAC 设计](rbac-v1-design.md) | 应用隔离、共享 API 目录、授权关系与运行时判断 |
| [权限生效设计](permission-lifecycle.md) | 保存即生效、Redis 双模式、菜单 current-policy、缓存一致性与数据库升级 |
| [鉴权匹配与性能](authorization-matching.md) | 静态哈希、参数 Trie、稀疏角色位图、当前索引缓存及基准 |
| [Redis 接入说明](redis-integration.md) | Redis 开关、数据库 current-policy 模式、key 标准、故障语义与配置 |
| [UI 设计与操作流程](ui-design.md) | 真实页面、按钮、路由、角色编辑与用户分配、会话状态 |
| [前端开发](frontend-development.md) | 实际入口、路由、导航模型、构建、代理和测试 |
| [接口参考](reference-api-rbac-v1.md) | 当前注册接口、认证、请求响应、错误码与当前权限语义 |
| [Go SDK 接入](sdk-application-integration.md) | `Check`、`Require`、身份解析、应用/服务上下文与失败处理 |
| [.NET SDK 接入](../sdk/dotnet/README.md) | Builder、Redis / HTTP 实现、统一权限接口与 NuGet 打包 |

以上设计说明描述当前实现。尚未实现的能力会明确标记，不作为现有功能承诺。`ui/` 中的设计源文件与历史截图用于参考，实际操作以当前界面及使用手册为准。
