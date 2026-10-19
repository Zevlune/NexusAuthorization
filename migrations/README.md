# NexusAuthorization 数据库初始化

当前项目按首次安装交付。全新空环境只需要执行两步：

1. PostgreSQL 管理员在维护库 `postgres` 中执行 `000_create_database.sql`，创建目标数据库；
2. 目标数据库 owner 执行 `001_init.sql`，创建 `nexusauthorization` schema 和完整业务结构。

仓库只维护一套完整初始 schema，不拆分第二套初始化 SQL。第一次安装请使用全新的空库。

| 文件 | 用途 | 执行连接 |
| --- | --- | --- |
| `000_create_database.sql` | 创建数据库 `nexus-authorization` | PostgreSQL 管理员连接维护库 `postgres` |
| `001_init.sql` | 创建 `nexusauthorization` schema、完整初始结构、约束、索引和业务保护触发器 | 数据库 owner 连接空库 `nexus-authorization` |

## 1. 创建数据库

从仓库根目录执行，管理员连接串中的主机、用户和密码按实际环境填写：

```sh
psql 'postgres://postgres:ADMIN_PASSWORD@db.example.com:5432/postgres?sslmode=require' \
  -X -v ON_ERROR_STOP=1 -f migrations/000_create_database.sql
```

建库脚本默认将数据库归执行建库的用户所有。如果应用使用独立数据库账号，先由管理员创建该账号，再将数据库 owner 改为实际账号：

```sql
ALTER DATABASE "nexus-authorization" OWNER TO "nexus-authorization";
```

数据库名包含连字符，SQL 中必须使用双引号。示例用户名只是占位符，可按实际账号替换。

## 2. 全新空库执行初始 schema

使用数据库 owner 连接刚创建的空库，直接执行完整初始化：

```sh
psql 'postgres://nexus-authorization:DB_PASSWORD@db.example.com:5432/nexus-authorization?sslmode=require' \
  -X -v ON_ERROR_STOP=1 -f migrations/001_init.sql
```

也可以通过仓库脚本执行：

```sh
export NEXUSAUTHORIZATION_DATABASE_URL='postgres://nexus-authorization:DB_PASSWORD@db.example.com:5432/nexus-authorization?sslmode=require'
sh src/scripts/init-db.sh
```

`001_init.sql` 自带 `BEGIN` / `COMMIT`，请在同一个数据库连接中完整执行，不要再包一层事务，也不要只执行光标所在的部分。脚本会创建并使用 `nexusauthorization` schema，应用连接池也固定使用该 schema。执行失败时应查看第一条错误并修正后重新对空库执行。

初始脚本创建：

- `pgcrypto` 扩展；
- 服务资源、业务应用、应用服务关联、API、菜单、角色和主体角色绑定表；
- API 端点、角色、菜单和主体角色绑定表；
- 应用边界校验、菜单父子关系校验和服务关联保护四条业务保护触发器；
- 唯一约束、查询索引和默认空域 `subject_role_bindings.domain = ''`。

`001_init.sql` 只创建业务表和四条业务保护触发器。它**不创建** `applications.policy_token`、`role_api_policy_tokens`、`service_api_policy_tokens`，也不创建任何缓存失效触发器、发布版本、历史快照或迁移记录表。Redis 启用时服务 API 授权以 Redis 为准，显式禁用时使用数据库 current-policy；`redis.enabled` 开关不需要数据库迁移或版本表。

脚本不插入应用、服务、角色、菜单或用户绑定等业务种子数据。初始化完成后，使用 Dashboard 或管理 API 创建第一套业务配置。

## 3. DBeaver 执行方式

1. 管理员连接维护库 `postgres`，开启自动提交，单独执行 `000_create_database.sql`。
2. 新建或切换到 `nexus-authorization` 连接，确认当前用户是数据库 owner。
3. 使用“执行 SQL 脚本”执行完整的 `001_init.sql`。
4. 关闭“每条语句后提交”，让脚本自己的顶层事务生效。
5. 如果发生错误，先查看日志中的第一条错误；不要只执行末尾 `COMMIT`，也不要删除脚本中的事务语句。

初始化后可用以下查询确认连接和核心表：

```sql
SELECT current_database(), current_schema(), current_setting('search_path');

SELECT expected.table_name,
       to_regclass(format('nexusauthorization.%I', expected.table_name)) AS actual_table
FROM (VALUES
    ('service_resources'),
    ('applications'),
    ('application_service_resources'),
    ('api_endpoints'),
    ('roles'),
    ('menus'),
    ('role_api_endpoint_bindings'),
    ('role_menu_bindings'),
    ('subject_role_bindings')
) AS expected(table_name)
ORDER BY expected.table_name;
```

默认配置下数据库应为 `nexus-authorization`、应用 schema 为 `nexusauthorization`，查询应返回 9 行且 `actual_table` 均非空。`current_schema()` 只有在当前会话的 `search_path` 已设置为该 schema 时才会显示 `nexusauthorization`；直接使用管理员会话时以 `to_regclass` 查询结果为准。

## 4. 本地开发

开发 Compose 使用空数据卷首次启动时自动执行挂载的 `001_init.sql`：

```sh
docker compose -f docker-compose-dev.yml up -d
docker compose -f docker-compose-dev.yml ps
```

默认 PostgreSQL 地址为 `localhost:55434`，数据库名为 `nexus-authorization`。已有数据卷不会因修改 `POSTGRES_DB` 自动改名，也不会再次执行初始化 SQL；需要重新初始化本地环境时，请确认数据可删除后再移除开发数据卷。

源码启动时，先让 `NEXUSAUTHORIZATION_DATABASE_URL` 指向已经完成初始化的空库或开发库，再运行：

```sh
cd src
go run ./cmd/api-server
```

## 5. 初始化后的第一套配置

初始化成功后：

1. 启动 API 和 Dashboard；
2. 登录并创建第一个业务应用；
3. 关联服务资源；
4. 维护或导入 API；
5. 创建角色并授予 API、菜单和按钮；
6. 分配用户角色；
7. 使用权限模拟验证允许和拒绝；
8. 再接入业务服务的 SDK 或 `/api/v1/authorize`。

保存成功后权限立即参与鉴权，没有发布按钮和发布步骤。服务 API 鉴权默认使用 Redis，必须设置非空 `NEXUSAUTHORIZATION_REDIS_URL`；连接失败、记录缺失、未 ready 或损坏时拒绝，不回退数据库。设置 `NEXUSAUTHORIZATION_REDIS_ENABLED=false` 后改用数据库 current-policy，不创建 Redis 客户端或发布记录；重启后生效。重新启用时，先设置 `NEXUSAUTHORIZATION_REDIS_ENABLED=true` 并重启 API，再在向该实例发送服务 API 流量前保存受影响的应用、服务、角色或绑定以重建记录，避免旧 Redis 记录落后于禁用期间的编辑。菜单 current-policy 路径始终不依赖 Redis。完整操作见 [用户使用手册](../doc/user-guide.md)，部署见 [运维操作手册](../doc/operations-manual.md)，系统结构见 [系统设计说明](../doc/system-design.md)。
