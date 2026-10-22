# 应用边界 RBAC v1 接口参考

> 文档日期：2026-10-14
> 目标读者：接入权限中心的业务服务、API Gateway 与前端开发人员
> 说明：本文按当前工作区代码描述已实现的 HTTP 接口与行为，包含实际字段、分页、错误、认证、鉴权、主体菜单、缓存生效和 SCIM。设计动机见 [应用边界 API RBAC 设计方案](rbac-v1-design.md)，系统全貌见 [当前系统概览](current-system-overview.md)。

## 1. 模型与范围

业务应用（Application）是菜单、角色和主体角色绑定的边界。服务资源（ServiceResource）维护共享的 API 目录；一个应用显式关联多个服务资源。运行时鉴权同时指定应用和服务资源：

```text
Subject + Domain -> Application -> Role -> API Endpoint
                                      \-> Menu
```

- 角色和菜单属于应用；角色只能授予应用已关联服务中的 API。
- 菜单直接属于应用，不要求创建 OAuth 客户端。
- 应用停用立即生效；API、角色、菜单和主体角色绑定在保存提交后立即生效。
- 服务 API 鉴权模式由 `redis.enabled` 选择，默认 `true`。启用时，每个「应用 key＋主体＋服务＋归一化 domain」读取一条 Redis `SubjectAPIPolicy` 记录，不读取数据库、不回源；记录缺失、未 ready 或 Redis 不可用时直接拒绝。显式禁用时不创建 Redis 客户端，也不发布记录，运行时鉴权和模拟改用数据库 current-policy。菜单和全策略适配器始终走独立的 current-policy 路径。详见 [权限生效设计](permission-lifecycle.md)、[鉴权匹配与性能](authorization-matching.md) 和 [Redis 接入说明](redis-integration.md)。

当前版本不包含显式 `deny`、策略文档、策略审批、数据行级/字段级权限、ABAC、用户组、组织架构，也不提供发布版本、历史快照和版本回滚接口。

## 2. 认证

### 2.1 管理接口

除 `/api/v1/authorize` 外的 `/api/v1/*` 路由受服务器配置的认证中间件（`auth.Middleware`）保护，接受两种身份：

- 管理端浏览器登录建立的加密会话 Cookie；
- `Authorization: Bearer <access token>`（由身份平台签发的访问令牌）。

认证失败返回原始 JSON，不套用成功/错误响应包裹：

```json
{"error":"authentication required"}
```

Bearer 令牌非法时额外返回 `WWW-Authenticate: Bearer error="invalid_token"` 与 `{"error":"invalid_token"}`。认证中间件未启用时，`/api/v1/*` 直接放行，仅用于本地或测试环境。

身份相关端点：`GET /api/auth/config`、`GET /api/auth/login`、`GET /api/auth/me`、`POST /api/auth/logout`。OIDC 回调是唯一不挂在 `/api` 前缀下的端点，固定为 `GET /signin-oidc`。`GET /api/auth/me` 的 `result` 为 `{isAuthenticated: true, user: {sub, name, email, picture, email_verified}}`（可选资料字段可能省略），未登录返回 `401` 与认证错误包裹。该用户投影不包含业务 Domain；Domain 由接入服务的可信上下文提供，可省略并使用空默认域，只有需要按具名域隔离时才显式传入。

### 2.2 服务到服务鉴权

默认生产和开发 Compose 已移除独立服务凭证配置，因此当前 `POST /api/v1/authorize` 返回 `401`。统一 Open API Token 管理与校验尚未接入；下面说明的是仍保留的旧服务凭证兼容机制。

`POST /api/v1/authorize` 不使用管理端会话，要求请求头携带配置的服务凭证：

```http
POST /api/v1/authorize
Authorization: Bearer <configured service credential>
Content-Type: application/json
```

服务端用常量时间比较该凭证与配置项 `authorization.service_credential`；缺失、格式错误或凭证不符返回 `401`，正文为纯文本 `authentication required`，不进入业务处理。业务服务不能把浏览器会话当作服务凭证，也不能从用户输入读取角色。

## 3. 响应格式与错误

### 3.1 成功响应

除 `/api/v1/authorize` 成功时返回原始决策对象外，管理接口（含 `/api/v1/users`、模拟接口）使用统一包裹：

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
| `INVALID_ARGUMENT` | 400 | 请求字段缺失、格式错误、请求体超过 4 MiB 或违反业务前置条件 |
| `UNAUTHENTICATED` | 401 | `/api/v1/authorize` 缺少或提供了错误的服务凭证 |
| `NOT_FOUND` | 404 | 应用、服务、角色、菜单、Endpoint 不存在或不可用 |
| `ALREADY_EXISTS` | 409 | 唯一约束冲突，或同一服务与方法下已有匹配相同请求的路径模板 |
| `CONFLICT` | 409 | 跨应用引用、引用停用对象、移除仍被授权引用的服务关联等 |
| `UNSUPPORTED_MEDIA_TYPE` | 415 | 带 JSON 正文的请求未使用 `Content-Type: application/json` |
| `INTERNAL_ERROR` | 500 | 未分类的服务端错误；响应只返回 `internal server error`，原始错误写入服务端日志 |
| `PUBLISH_PENDING` | 503 | Redis 模式下保存已提交，但授权记录发布失败；后台对账会自动补发布，无需重复保存 |

所有带 JSON 正文的接口都要求 `Content-Type: application/json`（可带 `charset` 参数）。浏览器跨站只能在 CORS 预检通过后发送该类型，因此这一要求同时阻止跨站表单伪造请求。

`/api/v1/users` 在 SCIM 未配置时返回 `503 SCIM_NOT_CONFIGURED`，上游调用失败时返回 `502 SCIM_UPSTREAM_ERROR`，响应结构同为 `{success,errorCode,errorMessage,result}`。

## 4. 分页

只有应用范围的 API Endpoint 列表支持分页：

- `GET /api/v1/rbac-v1/endpoints?app_key=...&page=1&page_size=10`
- `page` 从 1 开始，缺省或小于 1 视为 1；
- `page_size` 缺省为 10，最大 100，超过按 100 处理；
- `result` 结构为 `{items, total, page, page_size}`。

应用列表、角色列表、主体角色列表和菜单列表不分页，均为一次性返回 `{items: [...]}`。

## 5. 应用与服务目录

### 5.1 应用

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/applications` | 返回 `{items: Application[]}`，按显示名称排序 |
| POST | `/api/v1/applications` | 创建应用；成功返回 `201` 与 `Application` |
| PUT | `/api/v1/applications/{key}` | 更新已有应用；`key` 取自路径且不可修改 |

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

应用停用后，所有需要查找该应用的接口（包括 `POST /api/v1/authorize` 和 `/api/v1/rbac-v1/*`）立即因找不到启用应用而返回 `404`，不需要额外操作。

### 5.2 应用服务关联

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/applications/{key}/service-resources` | 返回 `{items: ServiceResource[]}` |
| PUT | `/api/v1/applications/{key}/service-resources` | 以 `{service_resource_keys: string[]}` 原子替换关联 |

`ServiceResource` 字段：`id`、`key`、`name`、`display_name`、`audience`、`description`、`is_active`、`source`、`created_at`。

规则：

- 新关联的 key 必须由当前目录确认存在且启用，否则返回 `409`。
- 目录不可用时，已有但当前目录查不到的关联以 `is_active=false` 的 key-only 占位返回，便于移除；不能通过新请求添加这类 key。
- 移除仍被任意角色 API 授权引用的服务关联时返回 `409`，需先移除相关授权。

### 5.3 服务资源目录

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/service-resources` | 返回 `{items: ServiceResource[], writable: bool}` |
| POST | `/api/v1/service-resources` | 创建本地服务资源，请求 `key, display_name, audience, description, is_active` |
| GET | `/api/v1/service-resources/{key}` | 读取单个服务资源 |
| PUT | `/api/v1/service-resources/{key}` | 更新本地服务资源的展示信息与启停 |
| DELETE | `/api/v1/service-resources/{key}` | 删除本地服务资源 |

`source` 为 `local` 时读写本地目录；为 `nexusauth` 时 `List`/`Get` 直接读取远端目录，不写入本地 `service_resources`，也不回退到本地元数据。NexusAuth 资源拒绝写操作。

注意：删除接口在仓储层方法名为 `SoftDelete`，实际执行的是物理 `DELETE`。数据库触发器禁止删除仍被应用关联或 API 端点引用的服务资源，也禁止停用仍被应用关联的本地资源；错误映射为 `409`。

## 6. 应用范围 RBAC 配置

以下 `/api/v1/rbac-v1/*` 接口都要求 `app_key` 查询参数，作用于该应用。API 目录的写操作另要求 `service_resource`，且该服务必须已关联当前应用并处于启用状态。

### 6.1 API Endpoint

| 方法 | 路径 | 参数 | 说明 |
| --- | --- | --- | --- |
| GET | `/api/v1/rbac-v1/endpoints` | `app_key`，可选 `service_resource`、`page`、`page_size` | 分页返回应用已关联服务中的 API；不带 `service_resource` 时返回全部已关联服务的 API |
| POST | `/api/v1/rbac-v1/endpoints` | `app_key`、`service_resource` | 创建或更新 Endpoint；无 `id` 时创建并返回 `201`，有 `id` 时更新并返回 `200` |
| POST | `/api/v1/rbac-v1/endpoints/import-swagger` | `app_key`、`service_resource` | 从 `{swagger_url}` 导入，返回 `201` 与 `{total,created,skipped}` |
| DELETE | `/api/v1/rbac-v1/endpoints/{id}` | `app_key`、`service_resource` | 删除 Endpoint |

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

保存要求 `method` 为标准 HTTP 方法（GET、POST、PUT、PATCH、DELETE、HEAD、OPTIONS、TRACE），`path_template` 以 `/` 开头且不超过 500 字符，`endpoint_key` 不超过 160 字符。同一服务与方法下，只差参数名或首尾斜杠的模板（如 `/orders/{id}` 与 `/orders/{orderId}/`）匹配相同的请求，运行时会被判为歧义而全部拒绝，因此保存时返回 `409`；更新同一条 Endpoint 时可以改参数名。更新时传入非空 `endpoint_key` 会修改接口标识，传空保留原值。自动生成的 `endpoint_key` 为「方法 + 截断后的路径 + 短哈希」，保证唯一且不超长。

Swagger 导入按 `method` 与上述路径形状跳过已存在接口（文档内重复的形状也只导入一次），逐条写入，不是整批原子事务；整批导入结束后只发布一次 Redis 记录。导入只访问管理员提供的地址，地址受 `swagger_import` 配置约束：始终禁止链路本地地址与云平台元数据地址，生产配置默认禁止回环地址，可用 `allowed_hosts` 进一步限定主机或网段；被拒绝时返回 `400`。

### 6.2 角色

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/rbac-v1/roles` | 返回 `{items: RBACRole[]}` |
| POST | `/api/v1/rbac-v1/roles` | 创建或更新角色；无 `id` 创建返回 `201`，有 `id` 更新返回 `200` |
| DELETE | `/api/v1/rbac-v1/roles/{id}` | 删除角色 |

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
| GET | `/api/v1/rbac-v1/menus` | 返回 `{items: ApplicationMenu[]}` |
| POST | `/api/v1/rbac-v1/menus` | 创建或更新菜单节点 |
| DELETE | `/api/v1/rbac-v1/menus/{id}` | 删除菜单节点 |

`ApplicationMenu` 字段：`id`、`application_id`、`parent_id`、`code`、`name`、`menu_type`、`route`、`component`、`icon`、`sort_order`、`enabled`。

- `menu_type` 取 `directory`、`menu` 或 `button`，其他值返回 `400`；`code` 和 `name` 必填。
- 菜单目录/菜单支持任意深度；按钮是叶子节点。
- 后端拒绝把按钮设为父节点，也拒绝把已有子节点的菜单改为按钮，并检测循环层级。
- 删除为物理删除；数据库不允许删除仍有子节点的菜单。

### 6.4 主体角色绑定

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/rbac-v1/subject-roles` | 返回 `{items: SubjectRoleBinding[]}` |
| PUT | `/api/v1/rbac-v1/subject-roles` | 替换指定 `subject + domain` 的全部角色；`domain` 省略时替换默认空域 |
| PUT | `/api/v1/rbac-v1/roles/{id}/subjects` | 为一个角色原子地增删成员：正文 `{domain, add, remove}`，只改动该角色，不影响这些主体的其他角色；整批只发布一次 |

`SubjectRoleBinding` 字段：`id`、`application_id`、`subject`、`domain`、`role_id`。

替换请求体：

```json
{
  "subject": "user-001",
  "domain": "",
  "role_ids": ["<role-uuid>"]
}
```

`subject` 必填，`domain` 可省略；省略或传空白按默认空域处理，旧值 `global` 也归一到同一默认空域。`role_ids` 必须属于当前应用，否则返回 `409`；重复的 ID 会被去重。替换是对该主体/域整组角色的覆盖，不是追加：两个调用方并发为同一主体替换不同角色时，后写入者会覆盖前者，需要只改一个角色时请使用下面的按角色增删接口。`domain` 是授权上下文字符串，非空具名值用于按租户或组织进一步隔离，不合并具名域、不扩大权限。

按角色增删成员的请求体：

```json
{
  "domain": "",
  "add": ["user-001", "user-002"],
  "remove": ["user-003"]
}
```

`add` 与 `remove` 可省略，主体不能为空，也不能同时出现在两边。角色不属于当前应用返回 `404`；给停用角色新增成员返回 `409`，从停用角色移除成员允许。默认空域的移除同时清理旧值 `global` 的绑定。管理端「分配用户」抽屉使用该接口，当前提交空域。

## 7. 保存生效与缓存

当前没有发布接口、发布版本、历史快照和回滚入口。`POST /api/v1/rbac-v1/releases` 与 `GET /api/v1/rbac-v1/releases` 已从路由和 OpenAPI 文档移除，调用返回 `404`。

保存流程：

```text
写入请求（角色 / 授权 / 菜单 / 绑定 / 服务关联 / API / 目录）
-> 仓储检查与数据库唯一约束、触发器校验
-> 提交当前业务表
-> Redis 启用：控制面把受影响应用置为 state=updating，删除旧记录、分批写入新记录
-> 全部写完后置为 state=ready
-> Redis 禁用：不创建 Redis 客户端、不发布，鉴权读取数据库 current-policy
-> 提交后开始的鉴权请求使用新权限
```

- Redis 启用时，每个「应用 key＋主体＋服务＋归一化 domain」的 `SubjectAPIPolicy` 是普通 JSON，无版本号、不设 TTL；保存的业务表写入与 Redis 重建由控制面顺序执行，数据库不保存任何缓存标记。
- Redis 启用时，受影响应用在重建期间 `state=updating`，并发鉴权直接拒绝；删除先于写入、闸门最后打开，不会读到半写集合。任何一步失败都保持 `updating`，下一次重建重试。
- Redis 启用时，角色 API/菜单授权、主体角色绑定、应用服务关联重建本应用；接口端点和服务资源变更重建所有关联该服务的应用；纯菜单保存只影响 current-policy 路径。Redis 禁用时不执行发布。
- Redis 启用时，鉴权（含权限模拟）只读 Redis，不读取数据库、不回源；记录缺失、`state=updating`、损坏或超时都拒绝。禁用时不连接 Redis，使用数据库 current-policy。
- 提交前已开始读取的在途请求可以完成其已读到的判断；提交并重建后开始的请求使用新权限。

菜单 current-policy 路径读取当前规范化业务表并计算内容摘要，在进程内编译，不受 Redis 开关控制。开关在重启后生效；从禁用切回启用时，先设置 `NEXUSAUTHORIZATION_REDIS_ENABLED=true` 并重启 API，再在向该实例发送服务 API 流量前保存受影响的应用、服务、角色或绑定，以重建 Redis 记录。事务、单飞、缓存容量和数据库升级见 [权限生效设计](permission-lifecycle.md) 与 [Redis 接入说明](redis-integration.md)。

## 8. 运行时鉴权

### 8.1 POST /api/v1/authorize

请求（服务凭证见 2.2）：

```json
{
  "appKey": "business.portal",
  "serviceResource": "order.service",
  "subject": "user-001",
  "domain": "",
  "method": "GET",
  "path": "/api/orders/1001"
}
```

要求 `appKey`、`serviceResource` 非空，正文可解；业务校验要求 `service_resource`、`subject`、`method` 非空且 `path` 以 `/` 开头，否则返回 `400`。`domain` 可省略，省略或传空白按默认空域归一，旧值 `global` 也视作同一默认域。成功返回 `200` 与决策对象。

### 8.2 POST /api/v1/rbac-v1/simulate

管理端模拟接口，`app_key` 由查询参数提供，正文与授权输入一致（`service_resource`、`subject`、`method`、`path`，`domain` 可选），成功时在统一包裹的 `result` 中返回决策对象。模拟读取应用当前已保存的配置，与运行时鉴权使用同一套逻辑。

### 8.3 决策对象与算法

```json
{
  "allowed": true,
  "effect": "allow",
  "reason": "allowed",
  "matched_roles": ["order.viewer"]
}
```

| 字段 | 说明 |
| --- | --- |
| `allowed` | 是否允许 |
| `effect` | `allow` 或 `deny` |
| `reason` | 判定原因 |
| `matched_permissions` | 预留字段，当前实现不填充，序列化时省略 |
| `matched_roles` | 命中的角色编码 |

算法（以下为 `redis.enabled=true`，也是省略 YAML 时的默认值）：

```text
1. 用 appKey、服务、subject 和归一化 domain 直接从 Redis 读取该身份的 SubjectAPIPolicy 记录（Lua 原子读取 state + record）。
2. 应用未 ready、记录损坏或读取出错 -> 拒绝，不回退数据库；应用已发布但该身份没有记录 -> 正常 DENY（endpoint_not_granted）；记录存在时校验大小、JSON 与身份，并复用内容哈希对应的本地编译索引。
3. 通过 service_resource + method 定位路由集合，使用静态哈希或参数 Trie 匹配 Endpoint，固定路径段更多者优先；同优先级不同 Endpoint 冲突时拒绝。
4. 记录已按 subject + 归一化 domain 聚合；`domain` 省略、空白或旧 `global` 都归一为空默认域。
5. 目标 Endpoint 在该用户的 API 授权并集内且服务启用 -> ALLOW，否则 DENY。
```

`redis.enabled=false` 或 `NEXUSAUTHORIZATION_REDIS_ENABLED=false` 时，步骤 1–2 改为读取数据库 current-policy；不创建 Redis 客户端，也不执行 Redis 发布。

`reason` 取值：

| reason | 含义 |
| --- | --- |
| `allowed` | 命中授权 |
| `service_resource_required` | 缺少服务资源 |
| `endpoint_not_found` | 没有匹配的 Endpoint |
| `endpoint_disabled` | 匹配到但已停用 |
| `endpoint_not_granted` | Endpoint 存在但未被授予 |
| `ambiguous_endpoint` | 多个同优先级路径模板匹配，拒绝 |
| `service_inactive` | Redis 模式：应用已停用，或服务在目录中已停用/不存在 |

未命中授权时决策为 `allowed=false`、`effect=deny`，`reason` 取上表中除 `allowed` 外的值。

路径参数按单个非空路径段匹配，例如 `/orders/{id}` 不匹配 `/orders/1/items`，也不提供任意深度通配。

应用不存在时接口返回 `404`，属于错误而不是正常 deny 响应；Redis 模式下应用从未发布（例如 Redis 刚清空、对账尚未完成）同样返回 `404`。服务未关联、主体未绑定角色、服务未启用、接口不存在或未被授予属于正常 deny 响应：Redis 模式下主体在该服务上没有记录时统一返回 `endpoint_not_granted`。应用停用时两种模式不同：数据库模式找不到启用应用返回 `404`，Redis 模式返回 deny `service_inactive`。

## 9. 主体菜单

```http
GET /api/v1/rbac-v1/subject-menus?app_key=business.portal&subject=user-001
```

返回 `{items: ApplicationMenu[]}`，数据来自与运行时鉴权相同的当前权限记录：命中 `subject + domain` 的启用角色所授予的菜单/按钮，并补齐显示所需的启用祖先节点。父节点停用、缺失或存在循环时对应分支不返回。`app_key`、`subject` 缺失返回 `400`；`domain` 可省略，省略或空白按默认空域处理，旧值 `global` 同样归一。

前端可基于返回的按钮权限控制操作入口，后端 API 仍须独立鉴权。

## 10. SCIM 用户

```http
GET /api/v1/users?filter=userName%20eq%20%22alice%22&startIndex=1&count=100
```

权限中心只读代理 NexusAuth 的 SCIM 2.0 Users 接口，不维护本地用户。参数：

- `filter`：SCIM 过滤表达式，可省略；
- `startIndex`：整数，最小 1，缺省 1；
- `count`：整数，范围 0~200，缺省 100。

成功时统一包裹的 `result` 为 SCIM `ListResponse`：`schemas`、`totalResults`、`startIndex`、`itemsPerPage`、`resources`。`resources` 中每个用户为 SCIM 资源的只读子集（`id`、`externalId`、`userName`、`name`、`emails`、`active`）。响应带 `Cache-Control: no-store`。未配置目录返回 `503 SCIM_NOT_CONFIGURED`，上游失败返回 `502 SCIM_UPSTREAM_ERROR`，且不回传上游原始错误。

## 11. 数据模型

由 `migrations/001_init.sql` 创建的 9 张应用 RBAC 及服务目录表：

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
```

应用服务关联与 Endpoint 只保存 `service_resource_key`，不建立指向 `service_resources` 的外键；key 由目录校验。数据库触发器保证角色 API 关联的服务已关联应用、角色菜单属于同一应用、菜单父子同应用，并保护服务资源启停与服务关联解除。数据库不保存 `policy_token`、角色/服务 revision 或发布历史；Redis 启用时服务 API 授权以 Redis 为准，禁用时使用数据库 current-policy。`subject_role_bindings.domain` 为 `NOT NULL DEFAULT ''`，默认域是空串，具名域按原值隔离。以上结构由首次初始化脚本直接创建。

## 12. 当前未实现

- 显式 `deny` 及 deny 优先级；
- 权限编码、策略文档、策略审批、审批流；
- 发布版本、历史快照、版本回滚接口、版本差异对比（当前设计不提供，保存即生效）；
- 数据行级/字段级权限、ABAC 条件、外部条件查询；
- 用户组、组织架构、用户资料 CRUD；
- 菜单/按钮与 API 的自动联动授权；
- 权限中心管理操作的细粒度管理员分权（当前仅做认证，不做管理权限矩阵）。
