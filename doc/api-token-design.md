# 开放 API Token 设计与实现

设计日期：2026-10-12；实现更新：2026-10-13。

API Token 统一由 NexusAuthorization 管理，采用随机不透明凭证，不使用 OAuth 协议。数据库保存 Token 哈希、归属、有效期和明确的授权；新版授权用 Token policy 引用服务作用域命名的权限组，Token 与策略、成功管理操作审计在同一个本地事务中写入。权限组目录单独维护，运行时按当前组映射与当前 API 目录实时求值，不保存授权快照。

## 管理边界

沿用现有应用模型：一个 Token 属于一个 `app_key`。要访问其他应用，应创建另一个 Token。新版授权用 Token policy 引用服务作用域命名的权限组，而不是在 Token 上直接保存逐条端点快照。

新版后台创建 Token 不再提供归属主体选择器。管理 HTTP `POST /v1/api-tokens` 忽略请求体里的 `owner_id`，由服务端用当前 `app_key` 作为内部归属元数据写入 `owner_id`，并把 `owner_type` 固定为 `service`；显式提交 `owner_type=user` 或任何非 `service` 值都会被拒绝。`owner_id` 只用于归属与审计，不代表被授权目标，也不参与权限判断，也不要求它是服务目录里的服务键。归属创建后不可更改。

每条 policy 的 `service_resource` 是被授权目标服务，必须当前应用已关联且启用；它与 Token 归属应用相互独立。归属对象由 `app_key`（应用）确定。服务目录读取只发生在创建／管理校验路径，内部 Authorize 校验保持纯本地、不新增远端依赖。

历史版本的域服务允许 `user` 和 `service`，后台创建接口现在只签发服务类型。已经存在的历史 `user` Token 仍可读取，编辑名称／有效期／策略，并可轮换，`owner_type`、`owner_id` 保持不变；它们不能被改写类型，运行时校验逻辑不变。

`owner_id` 是用于归属与审计的稳定标识。主体不自动继承用户角色，也不会因为填入某个用户 ID 就验证为该用户。

管理端使用现有 NexusAuth/OIDC 登录。开启 OIDC 时，只有 `api_tokens.admin_subjects` 中的管理员 subject 可以管理 Token；这份名单是可以授予应用 API 权限的受信管理员名单。第一版没有实现普通用户自助发放与用户权限交集。API Token 本身不能管理其他 Token。

开发模式关闭 OIDC 时遵循项目已有免登录模式，生产部署应开启 OIDC 并配置管理员 subject。

## 两类密钥及重启规则

| 凭证 | 用途 | 保存与变更 |
| --- | --- | --- |
| 页面生成的业务 API Token | 调用 NexusAuth 或 NexusAuthorization 的开放 API | 中心只存哈希；调用方保存明文。中心创建、编辑、启停、撤销立即影响后续校验，不需要重启 |
| 独立内部服务密钥 | NexusAuth 调用 NexusAuthorization 的内部校验接口 | 两端启动配置保存同一个随机密钥，不通过业务 Token 系统验证；配置改变后重启读取它的服务 |

调用方如果把业务 Token 明文放在自己的启动配置文件中，创建新 Token 后需要配置并重启**调用方**，因为本次不实现热加载；被调用服务不需要把所有业务 Token 写入配置。内部服务密钥与业务 Token 必须分别生成，不使用同一个字符串。

NexusAuthorization 调用 NexusAuth 时可以先在本地检查 Token，但 NexusAuth 必须自行校验收到的凭证，不能把调用方的预检查当作授权结果。

## 数据与生命周期

新增迁移：`migrations/003_api_tokens.sql`，随后必须执行 `migrations/004_api_permission_groups.sql`（`004` 依赖 `003`，并为 `api_tokens` 增加 `policies` 列）。

- `api_tokens`：应用外键、名称、UUID、显示前缀、SHA-256 哈希、主体、状态、过期时间、最近使用时间、创建人和时间，以及 `policies`（JSONB，默认 `[]`）保存 Token policy。
- `api_token_permissions`：旧版逐条端点授权（API 外键、服务、read/write、租户／项目范围，以及签发时的 HTTP 方法和路径快照）；仅在没有 policy 的历史 Token 上生效。
- `api_permission_groups`：服务作用域命名权限组，字段 `id`、`service_resource_key`、`code`、`name`、`action`（read/write）、`enabled`、`created_at`、`updated_at`，`(service_resource_key, code)` 唯一。
- `api_permission_group_endpoints`：权限组到 `api_endpoints` 的映射，主键 `(group_id, endpoint_id)`，组删除级联、端点删除受限；写入由触发器校验服务范围、启用状态和方法／动作一致性。
- `api_token_audit_logs`：创建、修改、启停、轮换、撤销成功操作的 Token ID、操作人、操作类型、时间，与管理写入同事务提交。本版未提供独立审计查询页面；拒绝事件使用现有 HTTP 状态日志。

Token 格式为 `nxa_<uuid>_<32字节随机secret的base64url>`。完整明文只在创建与轮换成功响应返回；列表、详情、错误与审计不返回明文或哈希。完整字符串计算 SHA-256，并以常量时间比较验证。

数据库状态为 `active / disabled / revoked`，过期由 `expires_at` 计算，前端显示 `expired`。过期优先于 active/disabled 展示，revoked 优先于过期。永久 Token 的 `expires_at` 为 NULL。支持永久、30 天、180 天、自定义日期。

停用可恢复，撤销不可恢复；过期 Token 可以编辑延长有效期。轮换只接受启用且未过期的 Token，生成新记录，并按 `revoke_old` 决定是否在同一事务内撤销旧记录。

最近使用时间按分钟节流，写入为尽力执行，最多等待 200ms，写入失败不改变授权结果。

## 权限组（Permission groups）

命名权限组是服务作用域的授权单元，独立于任何单个 Token 或应用维护，通过 `/v1/api-permission-groups` 管理。字段：

- `id`：组 UUID；
- `service_resource`：所属服务键；
- `code`：服务内唯一稳定编码，创建后不可改；
- `name`：展示名；
- `action`：`read` 或 `write`；
- `enabled`：停用后不再授予任何权限；
- `endpoint_ids`：该组显式映射的端点 UUID 集合，1–1000 个，去重，且必须属于同一服务、处于启用状态并满足方法规则。

组的可见范围由服务决定：同一服务下所有应用共享同一批组，任何一个应用编辑或停用组都会立即影响所有引用它的 Token。管理页面只展示当前应用已关联服务下的组；组不能跨服务复用。

方法规则：

- `read` 组只允许映射 `GET` / `HEAD` 端点。
- `write` 组可以显式映射任意受支持方法，包括 `GET`。`write` 表示这一组接口用于写入场景，不代表“整服务全方法授权”，也不自动包含服务下的其他接口；运行时对每个端点仍按实际 HTTP 方法判定 read/write。
- 受支持方法为 `GET` / `HEAD` / `POST` / `PUT` / `PATCH` / `DELETE`。`POST` 可以是“读形态”的接口，因此允许放进 `write` 组并按其实际方法授权；不能把 `POST` 人工改成 read，也不能把 `GET` 标成 write。

这一结构与 Cloudflare API Token 的命名 permission group + token policy 模型同类，便于对照理解，但字段、作用域和校验与 Cloudflare 不同，不是等价实现：Cloudflare 参考 [List permission groups](https://developers.cloudflare.com/api/resources/user/subresources/tokens/subresources/permission_groups/methods/list/) 与 [Create Token](https://developers.cloudflare.com/api/resources/user/subresources/tokens/methods/create/)。

## 权限语义

新版 Token policy 结构：

```json
{
  "id": "<策略UUID>",
  "effect": "allow",
  "service_resource": "nexusauth",
  "tenant_id": "tenant-a",
  "project_id": "",
  "permission_group_ids": ["<权限组UUID>"]
}
```

- 只支持 `effect: "allow"`。一条 policy 绑定一个目标服务、一组权限组和租户／项目范围，权限组必须属于该 policy 的服务。每条 policy 最多 1000 个组，一个 Token 最多 100 条 policy，且不能与旧版 `permissions` 混用。
- 空 `tenant_id` 或 `project_id` 表示该维度不限；非空时必须与目标请求的真实范围完全一致。范围值是稳定业务键，本项目没有租户／项目注册表。
- 目标服务必须已经关联当前应用并启用，组和组内端点必须启用。应用停用、服务关联移除、组停用、端点停用都会拒绝相应访问。
- 组映射是“显式端点 + 实际方法”：运行时对每个被引用的组，连接当前 `api_permission_group_endpoints` 与当前 `api_endpoints` 得到具体端点，再按端点实际 HTTP 方法判定 read/write。它不做整服务通配，也不因为组是 `write` 就给服务内所有写接口授权。
- 运行时始终按“当前组映射 + 当前 API 目录”实时求值：被引用的组停用、被移除、端点被停用或服务解除关联后立即失效。policy 非空时不会回退到任何旧端点快照。
- 静态路径优先于参数路径；同优先级多条匹配、被停用的静态路径和不安全路径均拒绝。路径只支持整段 `{parameter}`，不支持 catch-all、正则或任意字符串通配符。
- 旧版逐条 `permissions` Token 仍然保留：当 Token 没有 policy（`policies` 为空）时，运行时按旧的 `endpoint_id + service_resource + action + tenant_id + project_id` 逐条授权求值，行为与之前一致。带 policy 的 Token 只走组解析，不混用旧权限。
- 接收方必须从固定服务配置、实际路由与实际数据归属解析 app/tenant/project，不能信任外部调用方随意声明的范围。接口授权通过之后仍要在业务查询中按租户／项目过滤数据。

## 管理 API

以下所有接口必填 `?app_key=<业务应用键>`，遵循现有 `{success,errorCode,errorMessage,result}` 响应封装。

| 方法 | 路径 | 行为 |
| --- | --- | --- |
| GET | `/v1/api-tokens` | 分页列表，支持 search、status、page、page_size |
| POST | `/v1/api-tokens` | 创建，201，result 包含 token 和一次性 raw_token |
| GET | `/v1/api-tokens/{id}` | 元数据与权限详情 |
| PUT | `/v1/api-tokens/{id}` | 更新名称、有效期、策略（或旧版逐条权限）；主体保持不变 |
| POST | `/v1/api-tokens/{id}/status` | `{ "status": "active" 或 "disabled" }` |
| POST | `/v1/api-tokens/{id}/revoke` | 不可恢复撤销 |
| POST | `/v1/api-tokens/{id}/rotate` | `{ "revoke_old": true 或 false }`，201，一次性新明文 |
| GET | `/v1/api-permission-groups` | 列出 `?app_key=` 关联启用服务下的权限组；可选 `?service_resource=` 过滤单个服务 |
| POST | `/v1/api-permission-groups` | 在 `?app_key=` 与 `?service_resource=` 指定服务下创建或更新权限组，新建 201、更新 200 |
| DELETE | `/v1/api-permission-groups/{id}` | 在 `?app_key=` 与 `?service_resource=` 下删除权限组；被 Token policy 引用时返回冲突 |

创建和更新请求使用权限组策略（新版主路径）：

```json
{
  "name": "SCIM 用户同步",
  "expires_at": null,
  "policies": [
    {
      "id": "<策略UUID，可省略由服务端生成>",
      "effect": "allow",
      "service_resource": "nexusauth",
      "tenant_id": "tenant-a",
      "project_id": "",
      "permission_group_ids": ["<权限组UUID>"]
    }
  ]
}
```

请求体不需要、也不应提供归属主体：服务端忽略 `owner_id`，用 `app_key` 填充 `owner_id`，并把 `owner_type` 固定为 `service`；显式 `owner_type` 非 `service` 会被拒绝。`id` 可省略，服务端补一个策略 UUID；`tenant_id` / `project_id` 留空表示该维度不限。

权限组维护请求（`service_resource` 由查询参数决定，创建后 `service_resource` 与 `code` 不可更改）：

```json
{
  "id": "<组UUID，省略表示新建>",
  "code": "scim-users-read",
  "name": "SCIM users read",
  "action": "read",
  "enabled": true,
  "endpoint_ids": ["<API目录中的UUID>"]
}
```

旧版逐条端点授权仍可用，但仅用于保留历史行为（不带 policy 的 Token）：

```json
{
  "name": "旧版端点授权 Token",
  "expires_at": null,
  "permissions": [
    {
      "endpoint_id": "<API目录中的UUID>",
      "service_resource": "nexusauth",
      "action": "read",
      "tenant_id": "tenant-a",
      "project_id": ""
    }
  ]
}
```

`endpoint_id` 是现有目录的真实 UUID，例如先登记 `GET /scim/v2/Users`，再在权限组里选择它。带 policy 的 Token 不能同时提交 `permissions`。列表的 status 支持 active/disabled/revoked/expired 有效状态筛选。管理响应和内部校验响应都使用 `Cache-Control: no-store`。

## 管理页面

工作台页面按独立路由组织，可直接分享与刷新：

- `/open-api-permissions` 维护开放 API 权限：新增／编辑接口标识、接口名称（列表展示与权限说明用的 `summary`）、HTTP 方法、路径、所属服务和启用状态；导入支持填写 OpenAPI 文档 URL，或直接上传 JSON / YAML 文件（最大 8MB）。导入写入共享服务目录，不会自动授予应用、角色或 Token 权限。
- `/api-permission-groups` 列表，`/api-permission-groups/create` 创建，`/api-permission-groups/:groupId/edit` 编辑：按服务维护命名权限组，选择 `read` / `write` 与所属服务内启用的接口。
- `/api-tokens` 列表，`/api-tokens/create` 创建，`/api-tokens/:tokenId/edit` 编辑名称与策略，`/api-tokens/:tokenId/permissions` 只维护权限策略。

创建页面不再有归属主体选择器，Token 类型固定显示为“服务类型”。创建／编辑页面把授权组织成 Permission policies：每条策略选择一个目标服务（须已关联当前应用）、若干权限组和租户／项目范围；页面提供“全部／只读／写入”过滤，但过滤只在该服务的权限组内筛选，不会生成通配授权，权限组的端点集合仍由权限组目录决定。历史不带 policy 的 Token 显示为旧版逐条端点授权模式；历史 `user` 归属显示为只读的“历史用户 Token”。

## 内部校验协议

NexusAuth 接收到 `Authorization: Bearer nxa_...` 后，在处理业务前调用：

```http
POST /internal/v1/api-tokens/authorize
X-Internal-Service-Key: <NexusAuth独立内部服务密钥>
Content-Type: application/json
```

```json
{
  "token": "<接收到的业务API Token>",
  "app_key": "identity-console",
  "service_resource": "nexusauth",
  "method": "GET",
  "path": "/scim/v2/Users",
  "tenant_id": "tenant-a",
  "project_id": ""
}
```

内部服务密钥只允许检查配置中绑定的 `service_resource`。该接口不走 OIDC、不走业务 Token 校验，也不查询 NexusAuth 服务目录，避免递归依赖。它使用中心数据库中的本地服务关联和端点数据；创建与管理时可同步远端服务目录。

正常完成评估时，无论业务 Token 被允许还是被拒绝，内部接口都返回 HTTP 200 的原始 Decision JSON，不使用管理接口的响应封装：

```json
{
  "active": true,
  "allowed": true,
  "reason": "allowed",
  "token_id": "<UUID>",
  "app_key": "identity-console",
  "owner_type": "service",
  "owner_id": "identity-console"
}
```

| 内部响应 | 接收方处理 |
| --- | --- |
| 200，active=false | 对业务调用方返回 401：格式错误、凭证错误、停用、撤销、过期等 |
| 200，active=true、allowed=false | 对业务调用方返回 403：有效但没有目标 API 或范围权限 |
| 200，两项为 true | 继续执行，并保留返回主体用于业务审计 |
| 400 | 接收服务构造了错误的目标参数，应作为接入错误处理 |
| 401 / 403 | 内部服务密钥错误或调用服务范围错误，应作为服务接入错误处理 |
| 503、超时、异常 JSON | 对业务调用方返回 503，拒绝放行 |

请求路径必须是 API 自身实际路径，包含前导斜杠，不包含 query/fragment；不要传 Dashboard 的 `/api` 代理前缀。凭证不得写入 URL、日志或追踪标签。内部接口通过私有后端地址访问，生产环境使用 HTTPS；Dashboard Nginx 不转发 `/api/internal/`。

不缓存认证和授权结果，每次检查 Token 哈希及实时状态。带 policy 的 Token 在每次校验时实时连接权限组映射与当前 API 目录，组停用、端点停用或服务解除关联立即生效，policy 非空时不读取任何旧快照。撤销与权限修改影响事务提交后开始的请求；已经取得允许结果的在途请求可以完成。它不依赖现有 RBAC 发布快照，不需要发布权限版本。

## NexusAuthorization 自身开放 API

同一个 `APITokenService` 在本地中间件校验，无需 HTTP 回调自身。开启 OIDC 时，普通管理 API 可以使用原有登录身份，支持范围内的开放 API 也接受 Bearer API Token。

当前接入范围：`/v1/rbac-v1/*` 的应用权限 API、`/v1/applications/{key}` 的当前应用操作、应用列表及服务列表的只读接口。列表使用 Token 所属应用过滤，query API 必须给出 app_key。应用路径里的 `{key}` 是实际应用边界。

共享 API 目录的写入／导入／删除、权限组管理、全局服务管理、旧版全局 API 和 Token 管理不能用应用 Token 操作。现有 `/v1/authorize` 继续使用其专用 `authorization.service_credential`，不复用本功能。当前本地接口没有可信租户／项目上下文，因此不会放行带非空租户／项目限制的授权。

调用自身接口前要把 NexusAuthorization 服务关联应用，并将对应方法／路径登记为端点；导入 Swagger 后只选择上面支持的路由。

## 交付范围

本仓库已经实现数据库迁移（含 `migrations/004_api_permission_groups.sql` 的服务作用域权限组目录）、后端管理与校验、应用 API 中间件、管理页面、公开 Swagger 和 Go 远程校验 SDK `sdk/apitoken`。

NexusAuth 位于另一个项目，本次没有修改它的 SCIM／开放 API 认证代码。接入时需要在 NexusAuth 增加调用上述内部接口的客户端，并在受保护业务接口前执行评估；现有 OpenApiCredential 不会自动变成中心 Token。配置与接入步骤见 [API Token 使用与接入](api-token-integration.md)。
