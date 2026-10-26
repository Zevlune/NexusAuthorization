# NexusAuthorization

基于业务应用划分权限边界的授权中心，支持服务资源、API、菜单、角色和用户授权；角色、绑定和目录保存后直接生效。

| 目录 / 文件 | 内容 |
| --- | --- |
| `dashboard/` | React / TypeScript 前端核心代码 |
| `src/` | Go 后端，保留现有 cmd、internal、configs、test 布局 |
| `ui/` | UI 设计源文件、导出图及视觉验证截图 |
| `doc/` | 使用、开发、部署、接口和设计文档 |
| `migrations/` | PostgreSQL 首次建库、完整 schema 与既有库升级脚本 |
| `sdk/` | Go 业务服务接入 SDK |
| `demo/` | .NET 10 单项目示例：Swagger、NexusAuth 登录与 StackExchange.Redis 鉴权 |
| `docker-compose-dev.yml` | 本地前后端与 PostgreSQL |
| `docker-compose.yml` | 前后端连接外部 PostgreSQL |

文档入口：[标准使用手册](doc/user-guide.md) · [系统设计](doc/system-design.md) · [运维操作手册](doc/operations-manual.md) · [开发与二次开发](doc/development-guide.md) · [文档索引](doc/README.md) · [快速开始](doc/quick-start.md)。

.NET 接入：[可复用 SDK（Redis / HTTP）](sdk/dotnet/README.md) · [Demo 启动与联调](demo/README.md)。

当前系统设计和实现边界见 [系统设计](doc/system-design.md)；实际前端操作流程见 [UI 说明](doc/ui-design.md)。

NexusAuth 用户列表接入：[SCIM 只读用户查询](doc/nexusauth-scim-users.md)。

服务 API 鉴权由 `redis.enabled` 选择来源，默认 `true`，省略 YAML 时也为 `true`。启用时，中心把一个用户、一个服务和归一化 domain 的完整策略写为 Redis String，key 为 `PREFIX:<QueryEscape(trim(service))>:<QueryEscape(trim(subject))>[:<QueryEscape(normalized domain)>]`。JSON 只含 `subject`、`domain`、`service_resource`、`service_active`、`catalog`、`apis` 和 `roles`，不含应用标识；权限合并所有启用应用对该服务的授权，具名 domain 相互隔离。Demo 和中心运行时直接严格解析该 String，不读 `state`/Lua，也不依赖 `rbac:api:app` 辅助 key。开发和生产默认前缀为 `nexusauthorization:dev`、`nexusauthorization:prod`，可按部署配置并要求中心与消费者一致。

控制面先取得 PostgreSQL session advisory lock，再在同一 repeatable-read 快照中合并全部来源；Redis 层预验证完整输出后，按专用前缀 `SCAN` 严格识别业务 key、删除本轮不存在的旧用户记录，最后逐用户 `SET` 完整 JSON 并覆盖同名旧结构。每条用户写入原子，环境或应用批次不整体原子；部分失败可能让旧权限保留到下一轮对账，接口返回 `PUBLISH_PENDING`。启动和每次定时对账都全量重建；不新增数据库表或 migration，也不需要 Redis 锁、索引或计数器。该模型不承诺撤权零窗口或强 fail-closed 闸门。`redis.enabled: false` 或 `NEXUSAUTHORIZATION_REDIS_ENABLED=false` 时，服务 API 改用数据库 current-policy，不创建 Redis 客户端，也不发布记录；菜单与全策略适配器始终走独立的 current-policy 路径。数据库只保存业务配置，不保存发布版本或历史。旧 Hash/state key 本轮不连接现网、不清理、不部署；切换和定向清理要求见 [Redis 接入说明](doc/redis-integration.md)，保存生效与失败语义见 [权限生效设计](doc/permission-lifecycle.md)。

API 鉴权使用静态路径哈希、参数路径 Trie 和角色位图。服务 API 入口按「用户 × 服务」聚合授权并复用进程内编译索引；菜单与全策略适配器走独立的 current-policy 编译索引。数据结构、复杂度及实测结果见 [鉴权匹配与性能](doc/authorization-matching.md)。

## 首次上线

首次上线使用空库。PostgreSQL 管理员连接维护库 `postgres` 执行一次建库脚本；使用独立应用用户时，再由管理员把数据库所有者改为该用户。`000_create_database.sql` 不能放进事务。

```sh
psql 'postgres://postgres:ADMIN_PASSWORD@db.example.com:5432/postgres?sslmode=require' \
  -X -v ON_ERROR_STOP=1 -f migrations/000_create_database.sql

psql 'postgres://postgres:ADMIN_PASSWORD@db.example.com:5432/postgres?sslmode=require' \
  -X -v ON_ERROR_STOP=1 -c 'ALTER DATABASE "nexus-authorization" OWNER TO "nexus-authorization";'
```

将 `.env.example` 复制为 `.env`，将其中的 `NEXUSAUTHORIZATION_DATABASE_URL` 指向已存在的空库，并由库拥有者执行完整初始化：

```sh
cp .env.example .env
# 编辑 .env，数据库路径使用 /nexus-authorization，用户名使用 nexus-authorization 或实际应用用户
set -a
. ./.env
set +a
sh src/scripts/init-db.sh

docker compose -f docker-compose.yml pull
docker compose -f docker-compose.yml up -d
```

`001_init.sql` 是第一次安装使用的完整初始 schema，自带 `BEGIN` / `COMMIT`，不要再包一层事务。脚本会创建并使用 PostgreSQL 的 `nexusauthorization` schema；第一次安装只使用 `migrations/000_create_database.sql` 和 `migrations/001_init.sql`。完整步骤见 [数据库初始化](migrations/README.md)。数据库初始化按首次上线处理：删除旧数据库后，在全新空库执行 `migrations/001_init.sql`。
