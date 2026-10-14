# 首次上线数据库初始化

默认数据库名为 `nexus-authorization`。SQL 标识符包含连字符，建库及修改所有者时必须使用双引号。

| 文件 | 用途 | 执行连接 |
| --- | --- | --- |
| `000_create_database.sql` | 首次创建数据库，独立于事务执行 | PostgreSQL 管理员连接维护库 `postgres` |
| `001_init.sql` | 一次性创建当前完整结构，包含应用地址 `applications.url` | 数据库拥有者连接空库 `nexus-authorization` |

## 外部 PostgreSQL

从仓库根目录执行。管理员连接串中的主机、用户和密码按实际环境填写：

```sh
psql 'postgres://postgres:ADMIN_PASSWORD@db.example.com:5432/postgres?sslmode=require' \
  -X -v ON_ERROR_STOP=1 -f migrations/000_create_database.sql
```

建库脚本默认将数据库归执行建库的用户所有。生产环境使用独立应用用户时，先由管理员创建该用户并授予它数据库所有权（将 `nexus-authorization` 替换为实际应用用户）：

```sql
ALTER DATABASE "nexus-authorization" OWNER TO "nexus-authorization";
```

将 `.env.example` 复制为 `.env`，设置 `NEXUSAUTHORIZATION_DATABASE_URL`，数据库路径使用 `/nexus-authorization`。数据库用户名可独立设置，无需与库名相同。随后导出配置并初始化：

```sh
set -a
. ./.env
set +a
sh src/scripts/init-db.sh
```

也可以直接执行 SQL：

```sh
psql "$NEXUSAUTHORIZATION_DATABASE_URL" -X -v ON_ERROR_STOP=1 \
  -f migrations/001_init.sql
```

`001_init.sql` 自带 `BEGIN` / `COMMIT`，出错时整次初始化回滚。它创建 pgcrypto 扩展、4 个枚举、12 张业务表、约束、4 个触发器及其函数、7 个索引，不插入业务种子数据、不创建迁移记录表。初始化用户需要创建这些对象和扩展的权限。

12 张表包含供兼容目录接口 `/v1/authorization/api-endpoints*` 使用的 `authorization_api_endpoints`。该表和同为服务目录作用域的 `api_endpoints` 是两套独立数据：它保存 `controller / method / path_template` 以及审计列和 `is_deleted` 软删除标记，不建立指向 `service_resources` 的外键，也不参与应用 RBAC 的角色、菜单、主体绑定和发布快照。仅当需要继续使用该兼容目录接口时才读写它。

## 在 DBeaver 中执行

1. 管理员连接维护库 `postgres`，开启自动提交，独立执行 `000_create_database.sql`。建库不能放在事务中，也不要与 `001_init.sql` 一起执行。
2. 新建或切换连接到 `nexus-authorization`，使用数据库拥有者打开完整的 `001_init.sql`。
3. 使用「执行 SQL 脚本」执行全文，确保开头的 `BEGIN;` 和最后的 `COMMIT;` 在同一连接中执行。不要只执行光标所在语句，也不要单独选中末尾的 `COMMIT;`。
4. 检查 SQL 脚本执行设置，禁用「每条语句后提交」（After each query）。本文件自己管理事务，选择不额外提交（No commit）；发生错误时停止执行并回滚，不要忽略错误继续执行。函数正文的 `BEGIN ... END` 属于 PL/pgSQL，不能按内部的分号手工拆开执行。

PostgreSQL 的 `there is no transaction in progress` 通常是提交或回滚时没有活动事务的警告，不能单凭它判断初始化失败。可能只执行了最后的 `COMMIT`，也可能客户端已经提前结束了事务；如果来自客户端错误处理发出的 `ROLLBACK`，还需查看它前面的第一条错误。脚本自身有一对顶层 `BEGIN` / `COMMIT`。

先看执行日志中的第一条错误，再运行以下只读查询核对连接和表的位置：

```sql
SELECT current_database(), current_schema(), current_setting('search_path');

SELECT expected.table_name,
       to_regclass(format('public.%I', expected.table_name)) AS actual_table
FROM (VALUES
    ('service_resources'),
    ('applications'),
    ('application_service_resources'),
    ('api_endpoints'),
    ('authorization_api_endpoints'),
    ('roles'),
    ('menus'),
    ('role_api_endpoint_bindings'),
    ('role_menu_bindings'),
    ('subject_role_bindings'),
    ('rbac_releases'),
    ('rbac_release_snapshots')
) AS expected(table_name)
ORDER BY expected.table_name;
```

默认配置下数据库应为 `nexus-authorization`、schema 为 `public`。第二个查询应返回 12 行且 `actual_table` 均非空；表齐全还不等于整个初始化完整成功，需确认日志无其他错误，并在 DBeaver 刷新后核对 SQL 中的 4 个枚举、4 个触发器、4 个触发器函数和 7 个显式索引（主键及唯一约束还会生成额外索引）。如有缺失或先前报错，先保留第一条错误和查询结果用于排查，不要直接对已有对象的库重跑初始化，也不要删掉事务语句来绕过提示。

## Docker 本地开发

```sh
cp .env.dev.example .env.dev
docker compose --env-file .env.dev -f docker-compose-dev.yml up -d
```

PostgreSQL 镜像在空数据卷首次启动时，依据 `POSTGRES_DB` 创建 `nexus-authorization` 并且只执行挂载的 `001_init.sql`。此流程不执行 `000_create_database.sql`，也不需要额外运行初始化脚本。

源码开发连接默认使用 `localhost:55434/nexus-authorization`，与开发 Compose 一致。自定义数据库名或端口时，通过 `NEXUSAUTHORIZATION_DATABASE_URL` 指定完整连接串。

## 初始化后的使用

初始化成功后启动后端和前端，再通过当前 API 或管理页面创建应用、角色、菜单和用户绑定并发布权限版本。服务资源可以来自本地目录或 NexusAuth 远程目录。

脚本用于首次上线的空数据库，重复运行会报错且不会覆盖现有数据。已有数据卷不会因修改 `POSTGRES_DB` 自动改名，也不会重新执行初始化 SQL。生产 Compose 不会创建外部数据库。`src/scripts/migrate.sh` 仅兼容旧命令并转调 `init-db.sh`，不再扫描增量 SQL。
