# NexusAuthorization 文档

以当前代码实现为准，整理日期：2026-10-14。

## 使用与部署

| 文档 | 内容 |
| --- | --- |
| [标准使用手册](user-guide.md) | 登录、选择应用、关联服务、API 与菜单、角色授权、分配用户、发布与模拟、常见问题 |
| [快速开始](quick-start.md) | 本地体验、源码启动与首次上线入口 |
| [数据库初始化](../migrations/README.md) | 创建 `nexus-authorization` 空库并执行完整初始化 SQL |
| [Docker 部署与环境变量](docker-deployment-1.0.2.md) | 镜像部署、OIDC、服务目录、SCIM 和运行时服务凭证 |
| [2026-10-14 部署前验收](deployment-verification-2026-10-14.md) | 真实登录与浏览器操作、镜像验证、发现的问题和部署条件 |
| [NexusAuth SCIM 用户目录](nexusauth-scim-users.md) | 用户列表只读接入、分页与角色分配 |

## 实现与设计

| 文档 | 内容 |
| --- | --- |
| [实现与设计对照及清理记录](implementation-alignment.md) | 旧描述与最终实现的差异、删除范围、测试保留原则与验证结果 |
| [功能与数据模型](current-system-overview.md) | 当前功能、实际导航、数据流、删除和发布语义 |
| [完整表结构](current-database-schema.md) | 与 `migrations/001_init.sql` 对应的表、字段、约束和触发器 |
| [应用边界 RBAC 设计](rbac-v1-design.md) | 应用隔离、共享 API 目录、授权关系、发布与运行时判断 |
| [UI 设计与操作流程](ui-design.md) | 真实页面、按钮、路由、角色编辑与用户分配、会话状态 |
| [前端开发](frontend-development.md) | 实际入口、路由、导航模型、构建、代理和测试 |
| [接口参考](reference-api-rbac-v1.md) | 当前注册接口、认证、请求响应、错误码与快照语义 |
| [Go SDK 接入](sdk-application-integration.md) | `Check`、`Require`、身份解析、应用/服务上下文与失败处理 |

## 重构与首次上线说明

- [应用边界重构记录](application-refactor-plan.md)
- [首次上线与兼容范围](application-migration.md)

以上设计说明描述当前实现。尚未实现的能力会明确标记，不作为现有功能承诺。`ui/` 中的设计源文件与历史截图用于参考，实际操作以当前界面及使用手册为准。
