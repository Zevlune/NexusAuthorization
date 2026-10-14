# 应用边界 RBAC v1 接口参考

> 文档日期：2026-10-14
> 目标读者：接入权限中心的业务服务、API Gateway 与前端开发人员
> 说明：本文按当前工作区代码描述已实现的 HTTP 接口与行为，包含实际字段、分页、错误、认证、发布、鉴权、主体菜单和 SCIM。设计动机见 [应用边界 API RBAC 设计方案](rbac-v1-design.md)，系统全貌见 [当前系统概览](current-system-overview.md)。

## 1. 模型与范围

业务应用（Application）是菜单、角色、主体角色绑定和权限发布的边界。服务资源（ServiceResource）维护共享的 API 目录；一个应用显式关联多个服务资源。运行时鉴权同时指定应用和服务资源：

```text
Subject + Domain -> Application -> Role -> API Endpoint
                                      \-> Menu
```

- 角色和菜单属于应用；角色只能授予应用已关联服务中的 API。
- 菜单直接属于应用，不要求创建 OAuth 客户端。
- 应用停用立即生效；API、角色、菜单、主体角色绑定的修改必须重新发布才对运行时鉴权生效。
- 运行时鉴权只读取已发布的不可变快照；`GetActiveRBACRelease` 每次请求都从 PostgreSQL 读取发布行并解码 JSONB 快照，不存在常驻内存快照或原子指针热加载。

当前版本不包含显式 `deny`、策略文档、策略审批、数据行级/字段级权限、ABAC、用户组、组织架构、版本回滚接口和发布审批。

## 2. 认证

### 2.1 管理接口

除 `/v1/authorize` 外的 `/v1/*` 路由受服务器配置的认证中间件（`auth.Middleware`）保护，接受两种身份：

- 管理端浏览器登录建立的加密会话 Cookie；
- `Authorization: Bearer <access token>`（由身份平台签发的访问令牌）。

认证失败返回原始 JSON，不套用成功/错误响应包裹：

```json
{"error":"authentication required"}
```

Bearer 令牌非法时额外返回 `WWW-Authenticate: Bearer error="invalid_token"` 与 `{"error":"invalid_token"}`。认证中间件未启用时，`/v1/*` 直接放行，仅用于本地或测试环境。

身份相关端点：`GET /auth/config`、`GET /auth/login`、`GET /signin-oidc`、`GET /auth/me`、`POST /auth/logout`。`GET /auth/me` 的 `result` 为 `{isAuthenticated: true, user: {sub, name, email, picture, email_verified}}`（可选资料字段可能省略），未登录返回 `401` 与认证错误包裹。该用户投影不包含业务 Domain；Domain 由接入服务的可信上下文提供。

### 2.2 服务到服务鉴权

默认生产和开发 Compose 已移除独立服务凭证配置，因此当前 `POST /v1/authorize` 返回 `401`。统一 Open API Token 管理与校验尚未接入；下面说明的是仍保留的旧服务凭证兼容机制。

`POST /v1/authorize` 不使用管理端会话，要求请求头携带配置的服务凭证：

```http
POST /v1/authorize
Authorization: Bearer <configured service credential>
Content-Type: application/json
```

服务端用常量时间比较该凭证与配置项 `authorization.service_credential`；缺失、格式错误或凭证不符返回 `401`，正文为纯文本 `authentication required`，不进入业务处理。业务服务不能把浏览器会话当作服务凭证，也不能从用户输入读取角色。

## 3. 响应格式与错误

### 3.1 成功响应

除 `/v1/authorize` 成功时返回原始决策对象外，管理接口（含 `/v1/users`、模拟接口）使用统一包裹：

```json
{
  "success": true,
  "errorCode": null,
  "errorMessage": null,
  "result": {}
}
```

### 3.2 错误响应

```json
{
  "success": false,
  "errorCode": "INVALID_ARGUMENT",
  "errorMessage": "invalid argument: app_key is required",
  "result": null
}
```

| errorCode | HTTP 状态 | 触发条件 |
| --- | --- | --- |
| `INVALID_ARGUMENT` | 400 | 请求字段缺失、格式错误或违反业务前置条件 |
| `NOT_FOUND` | 404 | 应用、服务、角色、菜单、Endpoint、发布版本不存在或不可用 |
| `ALREADY_EXISTS` | 409 | 唯一约束冲突 |
| `CONFLICT` | 409 | 跨应用引用、引用停用对象、移除仍被授权引用的服务关联等 |
| `INTERNAL_ERROR` | 500 | 未分类的服务端错误 |

`/v1/users` 在 SCIM 未配置时返回 `503 SCIM_NOT_CONFIGURED`，上游调用失败时返回 `502 SCIM_UPSTREAM_ERROR`，响应结构同为 `{success,errorCode,errorMessage,result}`。

## 4. 分页

只有应用范围的 API Endpoint 列表支持分页：

- `GET /v1/rbac-v1/endpoints?app_key=...&page=1&page_size=10`
- `page` 从 1 开始，缺省或小于 1 视为 1；
- `page_size` 缺省为 10，最大 100，超过按 100 处理；
- `result` 结构为 `{items, total, page, page_size}`。

应用列表、角色列表、主体角色列表、菜单列表和发布列表不分页，均为一次性返回 `{items: [...]}`。

## 5. 应用与服务目录

### 5.1 应用

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/v1/applications` | 返回 `{items: Application[]}`，按显示名称排序 |
| POST | `/v1/applications` | 创建应用；成功返回 `201` 与 `Application` |
| PUT | `/v1/applications/{key}` | 更新已有应用；`key` 取自路径且不可修改 |

`Application` 字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | uuid | 应用标识 |
| `key` | string(<=128) | 应用标识，全局唯一 |
| `display_name` | string(<=200) | 显示名称 |
| `description` | string | 描述 |
| `url` | string | 可选应用地址 |
| `enabled` | bool | 应用启用状态 |

创建请求体：

```json
{
  "key": "business.portal",
  "display_name": "业务管理平台",
  "description": "",
  "url": "https://portal.example.com",
  "enabled": true
}
```

`url` 可省略或为空；填写时必须是绝对 HTTP(S) 地址，长度不超过 2048，不能携带用户名/密码，也不能包含反斜杠或空白字符。`enabled` 省略时默认 `true`。`PUT` 更新时若省略 `url` / `enabled`，沿用已有值；`key` 不存在返回 `404`。

应用停用后，所有需要查找该应用的接口（包括 `POST /v1/authorize` 和 `/v1/rbac-v1/*`）立即因找不到启用应用而返回 `404`，不需要重新发布。

### 5.2 应用服务关联

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/v1/applications/{key}/service-resources` | 返回 `{items: ServiceResource[]}` |
| PUT | `/v1/applications/{key}/service-resources` | 以 `{service_resource_keys: string[]}` 原子替换关联 |

`ServiceResource` 字段：`id`、`key`、`name`、`display_name`、`audience`、`description`、`is_active`、`source`、`created_at`。

规则：

- 新关联的 key 必须由当前目录确认存在且启用，否则返回 `409`。
- 目录不可用时，已有但当前目录查不到的关联以 `is_active=false` 的 key-only 占位返回，便于移除；不能通过新请求添加这类 key。
- 移除仍被任意角色 API 授权引用的服务关联时返回 `409`，需先移除相关授权。

### 5.3 服务资源目录

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/v1/service-resources` | 返回 `{items: ServiceResource[], writable: bool}` |
| POST | `/v1/service-resources` | 创建本地服务资源，请求 `key, display_name, audience, description, is_active` |
| GET | `/v1/service-resources/{key}` | 读取单个服务资源 |
| PUT | `/v1/service-resources/{key}` | 更新本地服务资源的展示信息与启停 |
| DELETE | `/v1/service-resources/{key}` | 删除本地服务资源 |

`source` 为 `local` 时读写本地目录；为 `nexusauth` 时 `List`/`Get` 直接读取远端目录，不写入本地 `service_resources`，也不回退到本地元数据。NexusAuth 资源拒绝写操作。

注意：删除接口在仓储层方法名为 `SoftDelete`，实际执行的是物理 `DELETE`。数据库触发器禁止删除仍被应用关联或 API 端点引用的服务资源，也禁止停用仍被应用关联的本地资源；错误映射为 `409`。

## 6. 应用范围 RBAC 配置

以下 `/v1/rbac-v1/*` 接口都要求 `app_key` 查询参数，作用于该应用。API 目录的写操作另要求 `service_resource`，且该服务必须已关联当前应用并处于启用状态。

### 6.1 API Endpoint

| 方法 | 路径 | 参数 | 说明 |
| --- | --- | --- | --- |
| GET | `/v1/rbac-v1/endpoints` | `app_key`，可选 `service_resource`、`page`、`page_size` | 分页返回应用已关联服务中的 API；不带 `service_resource` 时返回全部已关联服务的 API |
| POST | `/v1/rbac-v1/endpoints` | `app_key`、`service_resource` | 创建或更新 Endpoint；无 `id` 时创建并返回 `201`，有 `id` 时更新并返回 `200` |
| POST | `/v1/rbac-v1/endpoints/import-swagger` | `app_key`、`service_resource` | 从 `{swagger_url}` 导入，返回 `201` 与 `{total,created,skipped}` |
| DELETE | `/v1/rbac-v1/endpoints/{id}` | `app_key`、`service_resource` | 删除 Endpoint |

`RBACEndpoint` 字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | uuid | 主键；创建时省略 |
| `service_resource` | string | 归属服务 key，由查询参数确定 |
| `endpoint_key` | string | 接口标识；创建缺省时按 method 与 path 自动生成 |
| `method` | string | HTTP 方法，保存时转大写 |
| `path_template` | string | 以 `/` 开头的路径模板，如 `/books/{id}` |
| `module` | string | 模块 |
| `summary` | string | 摘要 |
| `source` | string | `manual` 或 `swagger`；缺省 `manual` |
| `enabled` | bool | 启停状态 |
| `last_discovered_at` | timestamp | 最近发现时间，可为空 |
| `created_at` / `updated_at` | timestamp | 时间戳 |

保存要求 `method` 非空且 `path_template` 以 `/` 开头。Swagger 导入按 `method + path_template` 跳过已存在接口，逐条写入，不是整批原子事务。

### 6.2 角色

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/v1/rbac-v1/roles` | 返回 `{items: RBACRole[]}` |
| POST | `/v1/rbac-v1/roles` | 创建或更新角色；无 `id` 创建返回 `201`，有 `id` 更新返回 `200` |
| DELETE | `/v1/rbac-v1/roles/{id}` | 删除角色 |

`RBACRole` 字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | uuid | 主键 |
| `application_id` | uuid | 所属应用 |
| `code` | string | 角色编码，应用内唯一 |
| `name` | string | 名称 |
| `description` | string | 描述 |
| `enabled` | bool | 启停状态 |
| `endpoint_ids` | uuid[] | 授予的 API Endpoint 集合 |
| `menu_ids` | uuid[] | 授予的菜单/按钮集合 |

保存要求 `code` 和 `name` 非空。`endpoint_ids` 中的 Endpoint 必须属于当前应用已关联且启用的服务，否则返回 `409`；`menu_ids` 必须属于当前应用，否则返回 `400`。角色删除为物理删除，会级联删除其授权和主体绑定。

API 授权与菜单/按钮授权是两组独立绑定，按钮不携带 API 关联，勾选按钮不会自动勾选 API。

### 6.3 菜单与按钮

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/v1/rbac-v1/menus` | 返回 `{items: ApplicationMenu[]}` |
| POST | `/v1/rbac-v1/menus` | 创建或更新菜单节点 |
| DELETE | `/v1/rbac-v1/menus/{id}` | 删除菜单节点 |

`ApplicationMenu` 字段：`id`、`application_id`、`parent_id`、`code`、`name`、`menu_type`、`route`、`component`、`icon`、`sort_order`、`enabled`。

- `menu_type` 取 `directory`、`menu` 或 `button`，其他值返回 `400`；`code` 和 `name` 必填。
- 菜单目录/菜单支持任意深度；按钮是叶子节点。
- 后端拒绝把按钮设为父节点，也拒绝把已有子节点的菜单改为按钮，并检测循环层级。
- 删除为物理删除；数据库不允许删除仍有子节点的菜单。

### 6.4 主体角色绑定

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/v1/rbac-v1/subject-roles` | 返回 `{items: SubjectRoleBinding[]}` |
| PUT | `/v1/rbac-v1/subject-roles` | 替换指定 `subject + domain` 的全部角色 |

`SubjectRoleBinding` 字段：`id`、`application_id`、`subject`、`domain`、`role_id`。

替换请求体：

```json
{
  "subject": "user-001",
  "domain": "global",
  "role_ids": ["<role-uuid>"]
}
```

`subject`、`domain` 必填；`role_ids` 必须属于当前应用，否则返回 `409`。替换是对该主体/域整组角色的覆盖，不是追加。`domain` 是授权上下文字符串；管理 API 必须显式提供非空值，当前用户分配 UI 固定提交 `global`。

## 7. 发布

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/v1/rbac-v1/releases` | 发布当前应用配置；返回 `201` 与 `RBACRelease` |
| GET | `/v1/rbac-v1/releases` | 返回 `{items: RBACRelease[]}`，按版本倒序 |

发布请求体仅含可选说明：

```json
{ "description": "2026-10-14 首次发布" }
```

`description` 为空时使用默认文案。发布流程：

```text
事务开始
-> 查找启用应用
-> SELECT ... FOR UPDATE 锁定应用行
-> 读取当前目录 active service keys
-> 用单条 SQL 构建应用当前状态的 JSONB 快照
-> 计算 SHA-256 checksum
-> 版本号 = 当前应用 max(version) + 1
-> 旧的 published 发布置为 superseded
-> 写入新 published 发布和快照
-> 提交事务
```

`RBACRelease` 在列表中字段为 `id`、`application_id`、`version`、`status`、`description`、`checksum`、`published_by`、`published_at`；`published_by` 取当前登录用户的审计标识；未配置认证服务时为 `system`，禁用 OIDC 的开发模式为 `development`。`snapshot` 字段仅在内部读取活动发布时填充，发布/列表响应不返回快照内容。

快照内容（键为 `app_key`、`application_id`、`service_resources`、`endpoints`、`roles`、`subject_roles`、`menus`）：

- `service_resources`：当前目录返回 active 服务 key；
- `endpoints`：这些服务的 Endpoint（`id`、`service_resource`、`method`、`path_template`、`enabled`）；
- `roles`：应用角色（`id`、`code`、`enabled`、`endpoint_ids`、`menu_ids`）；
- `subject_roles`：主体绑定（`subject`、`domain`、`role_id`）；
- `menus`：菜单树（`id`、`parent_id`、`code`、`name`、`menu_type`、`route`、`component`、`icon`、`sort_order`、`enabled`）。

发布不做额外的业务规则校验：唯一性、跨应用引用、服务关联等约束在写入阶段由仓储和数据库唯一约束/触发器保证；发布阶段若遇到应用不存在/停用、远端目录读取失败、快照构建或数据库写入失败，整个发布不会提交。发布对象是整个应用，不是单个用户或单个服务。

当前没有历史版本回滚接口，也没有按旧版本重新发布的入口：`GET /v1/rbac-v1/releases` 只读，历史发布不会被修改或删除。

## 8. 运行时鉴权

### 8.1 POST /v1/authorize

请求（服务凭证见 2.2）：

```json
{
  "appKey": "business.portal",
  "serviceResource": "order.service",
  "subject": "user-001",
  "domain": "global",
  "method": "GET",
  "path": "/api/orders/1001"
}
```

要求 `appKey`、`serviceResource` 非空，正文可解；业务校验要求 `service_resource`、`subject`、`domain`、`method` 非空且 `path` 以 `/` 开头，否则返回 `400`。成功返回 `200` 与决策对象。

### 8.2 POST /v1/rbac-v1/simulate

管理端模拟接口，`app_key` 由查询参数提供，正文与授权输入一致（`service_resource`、`subject`、`domain`、`method`、`path`），成功时在统一包裹的 `result` 中返回决策对象。模拟读取当前活动发布，不是未发布草稿。

### 8.3 决策对象与算法

```json
{
  "allowed": true,
  "effect": "allow",
  "reason": "allowed",
  "release_version": 3,
  "matched_roles": ["order.viewer"]
}
```

| 字段 | 说明 |
| --- | --- |
| `allowed` | 是否允许 |
| `effect` | `allow` 或 `deny` |
| `reason` | 判定原因 |
| `release_version` | 使用的发布版本号 |
| `matched_permissions` | 预留字段，当前实现不填充，序列化时省略 |
| `matched_roles` | 命中的角色编码 |

算法：

```text
1. 按 appKey 查找启用应用，读取该应用当前 status=published 的发布并存取 JSONB 快照。
2. 用 service_resource + method + path 匹配快照中的 Endpoint；固定路径段优先级高于参数段。
3. 查找 subject + domain 的全部主体角色绑定。
4. 展开启用角色授予的 Endpoint 集合。
5. 目标 Endpoint 在集合中 -> ALLOW，否则 DENY。
```

`reason` 取值：

| reason | 含义 |
| --- | --- |
| `allowed` | 命中授权 |
| `service_resource_required` | 缺少服务资源 |
| `endpoint_not_found` | 没有匹配的 Endpoint |
| `endpoint_disabled` | 匹配到但已停用 |
| `endpoint_not_granted` | Endpoint 存在但未被授予 |
| `ambiguous_endpoint` | 多个同优先级路径模板匹配，拒绝 |

未命中授权时决策为 `allowed=false`、`effect=deny`，`reason` 取上表中除 `allowed` 外的值。

路径参数按单个非空路径段匹配，例如 `/orders/{id}` 不匹配 `/orders/1/items`，也不提供任意深度通配。

应用停用或没有活动发布时，接口返回 `404`，属于错误而不是正常 deny 响应。

## 9. 主体菜单

```http
GET /v1/rbac-v1/subject-menus?app_key=business.portal&subject=user-001&domain=global
```

返回 `{items: ApplicationMenu[]}`，数据来自与运行时鉴权相同的活动发布快照：命中 `subject + domain` 的启用角色所授予的菜单/按钮，并补齐显示所需的启用祖先节点。父节点停用、缺失或存在循环时对应分支不返回。`app_key`、`subject`、`domain` 任一缺失返回 `400`。

前端可基于返回的按钮权限控制操作入口，后端 API 仍须独立鉴权。

## 10. SCIM 用户

```http
GET /v1/users?filter=userName%20eq%20%22alice%22&startIndex=1&count=100
```

权限中心只读代理 NexusAuth 的 SCIM 2.0 Users 接口，不维护本地用户。参数：

- `filter`：SCIM 过滤表达式，可省略；
- `startIndex`：整数，最小 1，缺省 1；
- `count`：整数，范围 0~200，缺省 100。

成功时统一包裹的 `result` 为 SCIM `ListResponse`：`schemas`、`totalResults`、`startIndex`、`itemsPerPage`、`resources`。`resources` 中每个用户为 SCIM 资源的只读子集（`id`、`externalId`、`userName`、`name`、`emails`、`active`）。响应带 `Cache-Control: no-store`。未配置目录返回 `503 SCIM_NOT_CONFIGURED`，上游失败返回 `502 SCIM_UPSTREAM_ERROR`，且不回传上游原始错误。

## 11. 数据模型

由 `migrations/001_init.sql` 创建的 12 张表（11 张应用 RBAC 及服务目录表，1 张兼容 API 目录表）：

```text
applications(id, app_key UNIQUE, display_name, description, url, enabled, ...)
service_resources(id, resource_key UNIQUE, display_name, audience, description, source, external_id, enabled, ...)
application_service_resources(application_id, service_resource_key, PRIMARY KEY(application_id, service_resource_key))
api_endpoints(id, service_resource_key, endpoint_key, method, path_template, module, summary, source, enabled, ...)
roles(id, application_id, code, name, description, enabled, ...)          UNIQUE(application_id, code)
menus(id, application_id, parent_id, menu_type, code, name, route, component, icon, sort_order, enabled, ...)
role_api_endpoint_bindings(role_id, endpoint_id, PRIMARY KEY(role_id, endpoint_id))
role_menu_bindings(role_id, menu_id, PRIMARY KEY(role_id, menu_id))
subject_role_bindings(id, application_id, subject, domain, role_id, ...)
rbac_releases(id, application_id, version, status, description, checksum, published_by, published_at, ...)
rbac_release_snapshots(release_id PRIMARY KEY, snapshot JSONB)
authorization_api_endpoints(id, service_resource, controller, method, path_template, summary, enabled, is_deleted, ...)
```

应用服务关联与 Endpoint 只保存 `service_resource_key`，不建立指向 `service_resources` 的外键；key 由目录校验。数据库触发器保证角色 API 关联的服务已关联应用、角色菜单属于同一应用、菜单父子同应用、角色/主体绑定不跨应用。

## 12. 共享目录遗留路由

服务器仍注册 `/v1/authorization/api-endpoints*` 的服务目录级 Endpoint 路由，并在 handler 路由测试与 OpenAPI 文档中保留：

| 方法 | 路径 |
| --- | --- |
| GET | `/v1/authorization/api-endpoints` |
| POST | `/v1/authorization/api-endpoints` |
| POST | `/v1/authorization/api-endpoints/import-swagger` |
| GET | `/v1/authorization/api-endpoints/{id}` |
| PUT | `/v1/authorization/api-endpoints/{id}` |
| DELETE | `/v1/authorization/api-endpoints/{id}` |

这些路由由 `biz.APIEndpointService` 提供，请求字段为 `service_resource, controller, method, path_template, summary, enabled`，其仓储读写 `authorization_api_endpoints`。它们与 RBAC v1 使用的 `api_endpoints` 表是两套数据：`migrations/001_init.sql` 同时创建这两个表，其中兼容目录保留审计字段与软删除。它们不是新的角色、菜单、主体绑定或发布入口，也不参与运行时发布快照；新工作台使用 `/v1/rbac-v1/*`。

## 13. 当前未实现

- 显式 `deny` 及 deny 优先级；
- 权限编码、策略文档、策略审批、审批流；
- 发布历史回滚接口、按旧版本重新发布、版本差异对比、草稿模拟；
- 数据行级/字段级权限、ABAC 条件、外部条件查询；
- 用户组、组织架构、用户资料 CRUD；
- 菜单/按钮与 API 的自动联动授权；
- 权限中心管理操作的细粒度管理员分权（当前仅做认证，不做管理权限矩阵）。
