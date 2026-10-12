# 统一应用模型初始化运行指南

当前版本使用单一初始化脚本建立应用边界模型。它适用于本地开发环境和新建数据库；旧数据库不执行在线增量升级。

## 数据安全

本地重建会清空 `permission_center` 中的现有数据。执行前先停止后端和前端进程，完成数据库备份，并确认备份可以恢复。备份完成后重建数据库或清空目标 schema，再初始化新结构。

旧数据不会自动转换为应用模型。需要保留的旧数据只能从备份恢复，或作为单独的数据导入任务处理；不要把旧数据种子混入初始化流程。

## 初始化数据库

当前版本唯一需要执行的脚本是 `migrations/001_init.sql`：

```bash
psql "$NEXUSAUTHORIZATION_DATABASE_URL" \
  -v ON_ERROR_STOP=1 \
  --single-transaction \
  -f migrations/001_init.sql
```

不要执行 `migrations/002_applications.sql`、旧的增量升级脚本或旧服务范围数据种子。初始化脚本执行完成后，应用、本地服务资源、角色、菜单、用户绑定和发布快照通过当前 API 创建和发布。NexusAuth 服务资源不导入或持久化到 `service_resources`，由配置的远程目录在管理读取和发布构建时提供。

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

默认后端地址由 `src/configs/app.yaml` 决定，Dashboard 默认运行在 `http://localhost:5273`，并通过 Vite 代理访问后端。

## 应用配置流程

1. 创建或选择业务应用。
2. 在应用下关联当前目录确认已启用的服务资源 key；关联本身不会授予 API 权限。应用关联表和 API 端点只保存 `service_resource_key`，没有服务资源外键。
3. 选择已关联的服务资源并导入 Swagger，或维护 API 目录。
4. 创建应用角色，授予已关联服务中的 API，并配置菜单授权。
5. 为 `subject + domain` 绑定应用角色。
6. 发布应用快照。
7. 使用 `appKey` 和 `serviceResource` 调用 `POST /v1/authorize` 验证权限。

API、角色、菜单或用户角色绑定的草稿变更要在下一次应用发布后才影响运行时鉴权。发布快照只包含配置目录返回的 active service keys；运行时只读取已发布快照，不调用 NexusAuth。应用停用、服务解除关联和禁用对象均按当前快照及运行时校验生效。远程资源暂时不可用时，已有关联以 inactive key-only 占位返回以便移除，新关联必须通过 active 目录校验。

## API 兼容范围

当前支持应用范围 API：

- `GET/POST /v1/applications`、`PUT /v1/applications/{key}`；
- `GET/PUT /v1/applications/{key}/service-resources`；
- `/v1/rbac-v1/*`，使用 `app_key` 查询参数；
- `POST /v1/authorize`，请求包含 `appKey`、`serviceResource`、`subject`、`domain`、`method` 和 `path`。

旧的 `/v1/roles`、`/v1/menus`、`/v1/users/{userID}/roles` 和 `/v1/authorization/roles` 服务范围入口已退役，不应继续接入或恢复。共享服务资源和 API 目录接口仍可用于目录维护。

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

## 回滚

回滚使用重建前的数据库备份，并同时恢复与该备份匹配的后端、Dashboard 和 SDK 版本。不要在新 schema 上重新执行旧的增量迁移脚本。
