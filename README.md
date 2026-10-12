# NexusAuthorization

基于业务应用划分权限边界的授权中心，支持服务资源、API、菜单、角色、用户授权及版本发布。

| 目录 / 文件 | 内容 |
| --- | --- |
| `dashboard/` | React / TypeScript 前端核心代码 |
| `src/` | Go 后端，保留现有 cmd、internal、configs、test 布局 |
| `ui/` | UI 设计源文件、导出图及视觉验证截图 |
| `doc/` | 使用、开发、部署、接口和设计文档 |
| `migrations/` | PostgreSQL 初始化和升级 SQL |
| `sdk/` | Go 业务服务接入 SDK |
| `docker-compose-dev.yml` | 本地前后端与 PostgreSQL |
| `docker-compose.yml` | 前后端连接外部 PostgreSQL |

文档入口：[使用文档](doc/README.md) · [快速开始](doc/quick-start.md) · [Docker 部署与环境变量](doc/docker-deployment-1.0.2.md)。
