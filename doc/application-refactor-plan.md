# 统一业务应用权限重构方案

## 已确认的设计

业务应用是菜单、角色、用户角色绑定和权限发布的边界。服务资源维护共享 API 目录；应用通过服务资源 key 多对多关联服务资源。角色只能授予所属应用已关联服务中的 API。菜单直接属于应用，不要求创建 OAuth 客户端。鉴权必须同时指定 appKey 和 serviceResource，默认拒绝，前端菜单权限不能代替后端 API 鉴权。

## 实施顺序与验收

- [x] 1. 将应用边界模型固化到单一初始化脚本 `migrations/001_init.sql`。本地重建前备份并清空旧数据库后，只执行该脚本；不再执行旧增量迁移、`002_applications.sql` 或旧数据种子。
- [x] 2. 增加应用创建、编辑、查询和服务关联接口；服务关联不能隐式授权。
- [x] 3. 重构 RBAC：角色、用户绑定、菜单属于应用；API 授权验证服务关联；服务目录共享；菜单树禁止跨应用父子关系。
- [x] 4. 按应用构建不可变快照，按配置目录过滤 active 服务 key，包含服务标识、API 定义、菜单和角色授权；事务化发布；运行时按应用和服务匹配；SDK 要求明确应用上下文。
- [x] 5. 控制台先选/建应用，再关联服务；Swagger 导入明确选择已关联服务；角色跨服务选择 API 并配置菜单；提供菜单管理和应用权限模拟。
- [x] 6. 验证 Go、SDK、前端构建，以及跨应用、跨服务同路径、未关联服务、菜单授权、发布与迁移行为；更新文档。

## API 契约

- `GET/POST /v1/applications`，`PUT /v1/applications/{key}`，请求/响应应用字段 `key, display_name, description, enabled`。
- `GET/PUT /v1/applications/{key}/service-resources`；GET 返回 `{items: ServiceResource[]}`，远程资源暂时不可用时返回 inactive key-only 占位；PUT 请求 `{service_resource_keys: string[]}`，原子替换关联；新 key 必须由当前目录确认 active，移除已有 API 授权引用的关联须拒绝。
- RBAC 管理接口保留 `/v1/rbac-v1` 路径，所有操作要求 `app_key` 查询参数。API 目录操作另带 `service_resource`；列表不带 service_resource 时列出所有已关联服务 API。
- Endpoint 响应增加 `service_resource`；Role 增加 `application_id, menu_ids`；角色不再以服务为隔离边界。
- 菜单 CRUD 使用 `/v1/rbac-v1/menus`，应用范围；字段 `id,parent_id,code,name,menu_type,route,component,icon,sort_order,enabled`。GET 返回 `{items: Menu[]}`，POST 保存，DELETE `/{id}`。
- `POST /v1/authorize` 请求 `appKey,serviceResource,subject,domain,method,path`。
- 模拟请求使用 `service_resource` 指定目标服务。
- `GET /v1/rbac-v1/subject-menus?app_key=...&subject=...&domain=...` 从已发布快照返回用户获授权菜单（包含显示所需祖先）。

## 数据与初始化策略

- `migrations/001_init.sql` 是当前版本唯一需要执行的数据库脚本，负责创建完整的应用边界 schema。
- 本地重建会清空 `permission_center` 中的现有数据；执行前先完成数据库备份，并确认可以从备份恢复。
- 不执行 `migrations/002_applications.sql`、任何旧的增量升级脚本或旧数据种子。旧数据恢复或导入属于单独的数据迁移任务。
- 旧版按服务资源隔离的角色、菜单、用户绑定、发布和模拟路由退役；共享服务资源和 API 目录管理保留。服务关联和 API 端点只保存 `service_resource_key`，不建立服务资源外键；新关联仅接受当前目录中存在且启用的服务资源。
- local 服务资源由本地目录维护；NexusAuth 服务资源的 `List`/`Get` 直接读取远程目录，不读取或写入本地元数据，也不作为本地缓存回退。已有远程关联在资源不可用时返回 inactive key-only 占位，便于移除。
- 发布快照固定当前 active 服务 key 及 API 定义，后续目录变化在下次应用发布后生效；运行时只读快照，不调用远程服务目录。


## 验证结果

- 后端完整 `go test ./...` 通过。
- SDK `go test ./...` 通过。
- 控制台 `npm run build` 通过（Vite 提示现有大 chunk）。
- `TestApplicationPermissionIntegration` 覆盖关联/解除保护、跨应用引用、不同服务同路径 API、菜单循环与祖先、不可变发布快照、并发发布；测试数据库应先由 `migrations/001_init.sql` 初始化。
- 增补静态路由优先于路径参数模板的鉴权测试，避免动态路径授权覆盖更具体接口。
- `git diff --check` 通过。数据库运行步骤见 `doc/application-migration.md`。
