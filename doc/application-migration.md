# 统一应用模型初始化运行指南

当前版本使用单一初始化脚本建立应用边界模型。本文只描述首次上线的新建空库初始化流程。

## 适用范围

首次上线使用数据库名 `nexus-authorization`。`migrations/000_create_database.sql` 由 PostgreSQL 管理员连接维护库 `postgres` 执行一次；`migrations/001_init.sql` 由数据库拥有者连接空库执行。初始化脚本只创建当前完整 schema。

## 初始化数据库

管理员首次建库：

```bash
psql 'postgres://postgres:ADMIN_PASSWORD@db.example.com:5432/postgres?sslmode=require' \
  -X -v ON_ERROR_STOP=1 -f migrations/000_create_database.sql
```

`000_create_database.sql` 的内容是 `CREATE DATABASE "nexus-authorization";`，不能放进事务。建库脚本默认将数据库归执行建库的管理员所有。使用独立应用用户时，先由管理员创建该用户，再转移数据库所有权，例如：

```bash
psql 'postgres://postgres:ADMIN_PASSWORD@db.example.com:5432/postgres?sslmode=require' \
  -X -v ON_ERROR_STOP=1 -c 'ALTER DATABASE "nexus-authorization" OWNER TO "nexus-authorization";'
```

随后将 `NEXUSAUTHORIZATION_DATABASE_URL` 指向这个已存在的空库，并由库拥有者执行初始化：

```bash
export NEXUSAUTHORIZATION_DATABASE_URL='postgres://nexus-authorization:CHANGE_ME@db.example.com:5432/nexus-authorization?sslmode=require'
sh src/scripts/init-db.sh
```

`init-db.sh` 只执行 `migrations/001_init.sql`。该脚本自带 `BEGIN` / `COMMIT`，不要再包一层事务；它创建 `pgcrypto`、4 个枚举、12 张表（含兼容 API 目录）、约束、4 个触发器及其函数、7 个显式索引，包含 `applications.url`，不插入业务种子数据，也不创建 `schema_migrations`。`src/scripts/migrate.sh` 仅作为转调 `init-db.sh` 的兼容入口，首次上线只使用 `init-db.sh`。完整说明见 [数据库初始化](../migrations/README.md)。

初始化执行完成后，应用、本地服务资源、角色、菜单、用户绑定和发布快照通过当前 API 创建和发布。NexusAuth 服务资源不导入或持久化到 `service_resources`，由配置的远程目录在管理读取和发布构建时提供。

## 直接启动服务

本地开发直接启动 Go 后端和 Vite 前端，不使用 Docker Compose：

```bash
cd src
go run ./cmd/api-server
```

另开终端启动 Dashboard：

```bash
cd dashboard
npm install
npm run dev
```

默认后端地址由 `src/configs/app.yaml` 决定，Dashboard 默认运行在 `http://localhost:5274`，并通过 Vite 代理访问后端。

## 应用配置流程

1. 创建或选择业务应用。
2. 在应用下关联当前目录确认已启用的服务资源 key；关联本身不会授予 API 权限。应用关联表和 API 端点只保存 `service_resource_key`，没有服务资源外键。
3. 选择已关联的服务资源并导入 Swagger，或维护 API 目录。
4. 创建应用角色，授予已关联服务中的 API，并配置菜单授权。
5. 为 `subject + domain` 绑定应用角色。
6. 发布应用快照。
7. 使用 `appKey` 和 `serviceResource` 调用 `POST /v1/authorize` 验证权限。

API、角色、菜单或用户角色绑定的草稿变更要在下一次应用发布后才影响运行时鉴权。发布快照只包含配置目录返回的 active service keys；运行时按请求读取活动发布快照，不调用 NexusAuth。应用启用状态每次请求即时校验，停用应用后相关管理接口和 `POST /v1/authorize` 立即因找不到启用应用而失败，不需要重新发布；服务解除关联、接口/角色/菜单停用等改动仍需重新发布才反映到运行时快照。远程资源暂时不可用时，已有关联以 inactive key-only 占位返回以便移除，新关联必须通过 active 目录校验。当前没有历史发布版本回滚接口。

## API 兼容范围

当前支持应用范围 API：

- `GET/POST /v1/applications`、`PUT /v1/applications/{key}`；
- `GET/PUT /v1/applications/{key}/service-resources`；
- `/v1/rbac-v1/*`，使用 `app_key` 查询参数；
- `POST /v1/authorize`，请求包含 `appKey`、`serviceResource`、`subject`、`domain`、`method` 和 `path`。

旧的 `/v1/roles`、`/v1/menus`、`/v1/users/{userID}/roles` 和 `/v1/authorization/roles` 服务范围入口已退役，不应继续接入或恢复。共享服务资源和 API 目录接口仍可用于目录维护；其中 `/v1/authorization/api-endpoints*` 仍注册在路由表和 OpenAPI 文档中，读写 `authorization_api_endpoints`，与 RBAC v1 的 `api_endpoints` 是两套数据。

## 验证

初始化完成后可以运行：

```bash
cd src
go test ./...
go test ./internal/data/db/repo -run TestApplicationPermissionIntegration -v

cd ../sdk
go test ./...

cd ../dashboard
npm run build
```

集成测试需要将 `NEXUSAUTHORIZATION_TEST_DATABASE_URL` 指向由 `migrations/001_init.sql` 初始化的可丢弃数据库；测试会创建随机标识的数据并在结束时清理。不要指向生产数据库。

## 初始化失败回滚

初始化失败由 `001_init.sql` 自带的 `BEGIN` / `COMMIT` 自动回滚，不会保留半成品 schema。上线后的数据备份与恢复按单独的运维计划执行，并同时恢复与目标版本匹配的后端、Dashboard 和 SDK。
