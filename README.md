# NexusAuthorization

基于业务应用划分权限边界的授权中心，支持服务资源、API、菜单、角色和用户授权；角色、绑定和目录保存后直接生效。

| 目录 / 文件 | 内容 |
| --- | --- |
| `dashboard/` | React / TypeScript 前端核心代码 |
| `src/` | Go 后端，保留现有 cmd、internal、configs、test 布局 |
| `ui/` | UI 设计源文件、导出图及视觉验证截图 |
| `doc/` | 使用、开发、部署、接口和设计文档 |
| `migrations/` | PostgreSQL 首次建库与完整 schema 初始化 SQL |
| `sdk/` | Go 业务服务接入 SDK |
| `docker-compose-dev.yml` | 本地前后端与 PostgreSQL |
| `docker-compose.yml` | 前后端连接外部 PostgreSQL |

文档入口：[标准使用手册](doc/user-guide.md) · [系统设计](doc/system-design.md) · [运维操作手册](doc/operations-manual.md) · [开发与二次开发](doc/development-guide.md) · [文档索引](doc/README.md) · [快速开始](doc/quick-start.md)。

当前系统设计和实现边界见 [系统设计](doc/system-design.md)；实际前端操作流程见 [UI 说明](doc/ui-design.md)。

NexusAuth 用户列表接入：[SCIM 只读用户查询](doc/nexusauth-scim-users.md)。

支持使用 go-redis 缓存应用当前保存的权限记录。缓存按应用分片，使用固定 key，不按用户或绑定逐条建 key，也没有业务版本或历史快照；保存即生效的事务与撤权规则见 [权限生效设计](doc/permission-lifecycle.md)，Redis key、缓存一致性和启用方式见 [Redis 接入说明](doc/redis-integration.md)。

API 鉴权使用静态路径哈希、参数路径 Trie 和稀疏角色位图，并按应用当前失效标记复用编译索引；数据结构、复杂度及实测结果见 [鉴权匹配与性能](doc/authorization-matching.md)。

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

`001_init.sql` 是第一次安装使用的完整初始 schema，自带 `BEGIN` / `COMMIT`，不要再包一层事务。第一次安装只使用 `migrations/000_create_database.sql` 和 `migrations/001_init.sql`；完整步骤见 [数据库初始化](migrations/README.md)。
