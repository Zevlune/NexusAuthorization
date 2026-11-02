# NexusAuthorization 系统设计说明

整理日期：2026-10-16。本文以当前工作区代码、`migrations/001_init.sql`、Docker 配置和现有测试为准，描述已经实现的系统行为。文中明确标记的限制不属于当前版本能力承诺。

## 1. 系统定位

NexusAuthorization 以业务应用组织权限配置，负责维护服务资源目录、API 端点、菜单与按钮、角色以及主体角色绑定，并为业务服务提供运行时授权判断。服务 API 的运行时权限合并所有启用应用对该服务的有效授权。

系统把“管理配置”和“运行时决策”放在同一个权限模型中：管理端保存成功后，数据在事务提交时直接生效；业务服务下一次调用鉴权接口时读取当前配置，不需要发布、审批或切换版本。

当前版本覆盖：

- 业务应用级隔离；
- 服务资源与 API 共享目录；
- 应用内角色、菜单、按钮和主体角色绑定；
- OIDC 管理端登录；
- NexusAuth 服务目录和 SCIM 用户目录接入；
- PostgreSQL 持久化业务配置；服务 API 授权默认以 Redis 为准，显式禁用后使用数据库 current-policy；
- Go SDK 和 HTTP 鉴权接口；
- Swagger/OpenAPI API 目录导入。

当前版本不包含显式 deny 策略、ABAC、数据行级权限、组织架构、用户组、策略审批、发布版本、历史快照和权限回滚。

## 2. 总体架构

```mermaid
flowchart LR
    Admin[管理员浏览器] --> Dashboard[React Dashboard]
    Dashboard -->|/api 反向代理| API[Go API Server]
    API --> Auth[OIDC 身份提供方]
    API --> PG[(PostgreSQL)]
    API --> Redis[(Redis 服务 API 授权，启用时)]
    API --> Catalog[NexusAuth 服务目录]
    API --> SCIM[NexusAuth SCIM 用户目录]
    Service[业务服务 / BFF / Gateway] -->|POST /api/v1/authorize| API
    Service -.Go SDK.-> API
```

组件职责如下：

| 组件 | 职责 | 状态管理 |
| --- | --- | --- |
| Dashboard | 登录后的应用选择、资源配置、角色编辑、用户分配和权限模拟 | 浏览器 session/local storage 保存页面状态；权限数据由 API 提供 |
| API Server | 认证、管理 API、授权决策、目录适配、事务边界和错误映射 | 无状态进程；会话密钥和数据库由外部存储提供，Redis 启用时再连接 Redis |
| PostgreSQL | 业务配置、约束和业务保护触发器 | 业务配置的唯一事实来源；Redis 禁用时也是服务 API current-policy 鉴权的数据来源；不保存缓存标记、revision 或发布历史 |
| Redis | 启用模式下按 `service + subject + domain` 保存完整策略 JSON String | 仅在 `redis.enabled=true` 时使用；直接读取并严格校验，不回源数据库或旧辅助 key |
| OIDC Provider | 管理端登录、用户身份和会话上游 | 外部身份系统 |
| Service Catalog | 服务资源目录 | local 模式使用本地表；nexusauth 模式从远程目录读取 |
| SCIM Directory | 管理端用户搜索和分页 | 用户资料不落库 |
| SDK | 业务服务内的失败关闭中间件和直接调用封装 | 不保存授权状态 |

API Server 通过 `src/internal/app/app.go` 完成装配，入口在 `src/cmd/api-server/main.go`。Dashboard 部署镜像由 Nginx 托管静态文件，将 `/api` 前缀转发到 API Server，并单独将 OIDC 回调 `/signin-oidc` 转发到 API Server。

## 3. 权限模型

应用是配置和归属边界，服务资源是可跨应用复用的 API 目录。应用关联服务只建立目录使用关系，不直接授予权限。运行时把同一用户、服务和 domain 下所有启用应用授予的 API 与角色合并为并集。

```text
Application
├── ApplicationServiceResource -> ServiceResource
├── Menu / Button
├── Role
│   ├── RoleApiEndpointBinding -> ApiEndpoint
│   └── RoleMenuBinding -> Menu / Button
└── SubjectRoleBinding(subject, domain) -> Role
```

运行时输入为：

```text
appKey + serviceResource + subject + [domain] + method + path
```

决策过程按以下顺序执行：

1. 以请求中的服务、用户和归一化 domain 定位合并策略；`appKey` 仍是请求上下文，但不是 Redis 隔离维度。
2. 合并所有仍启用且关联该应用服务的来源，计算 API 与角色并集；某个应用停用、解除关联或撤权不会删除其他来源的权限。
3. 在指定服务和 HTTP 方法下从完整 Catalog 匹配 API 路径。
4. 判断匹配 API 是否在合并后的 `apis` 中。
5. 返回 `allowed`、`effect`、`reason` 和命中的角色编码。

默认域用空字符串表示。省略、空白和兼容值 `global` 会归一为默认域；具名域保持独立。固定路径优先于参数路径，参数路径只匹配一个非空路径段，不提供任意深度通配符。

## 4. 管理配置流程

```mermaid
flowchart LR
    A[创建或选择应用] --> B[关联启用的服务资源]
    B --> C[维护或导入 API]
    C --> D[创建角色并授予 API]
    A --> E[维护菜单和按钮]
    D --> F[授予角色菜单/按钮]
    F --> G[为主体绑定角色]
    G --> H[权限模拟]
    H --> I[业务服务运行时鉴权]
```

管理 API 位于 `/api/v1` 下，除 `/api/v1/authorize` 外，在启用 OIDC 时先验证会话或 Bearer 管理令牌，再检查 Redis 中用户对管理服务接口的授权。RBAC v1 管理接口都要求 `app_key`；接口端点的创建、更新、导入和删除还需要 `service_resource`，且服务必须已经关联当前应用。

保存角色时，仓储层在同一个事务中校验角色、API、菜单和应用边界，并更新授权集合。主体角色替换接口按 `subject + domain` 原子替换完整角色集合，调用方需要提交希望保留的全部角色。

## 5. 数据设计

核心表包括：

| 表 | 作用 |
| --- | --- |
| `applications` | 业务应用 |
| `service_resources` | local 服务目录元数据 |
| `application_service_resources` | 应用与服务的关联 |
| `api_endpoints` | RBAC v1 共享 API 目录 |
| `roles` | 应用内角色 |
| `menus` | 应用菜单、按钮和目录节点 |
| `role_api_endpoint_bindings` | 角色到 API 的授权 |
| `role_menu_bindings` | 角色到菜单/按钮的授权 |
| `subject_role_bindings` | 主体、域与角色的绑定 |

数据库约束和仓储校验共同保证：应用内角色编码唯一，服务内接口组合唯一，角色与菜单不跨应用，角色只能授予应用已关联服务的 API，菜单父子关系不跨应用，按钮不能作为父节点。

数据库只保存业务配置，没有 `applications.policy_token`、角色/服务 revision、发布历史、Redis 索引或计数器。`001_init.sql` 只创建四条业务保护触发器：禁止停用有关联的服务资源、禁止解除仍有 API 授权的服务关联、禁止跨应用角色 API 授权、禁止跨应用角色菜单授权。Redis 模式下，一致性由控制面在保存后从同一个数据库快照全量重建，不依赖数据库标记，详见下节。

完整字段、约束和触发器说明见 [当前数据库结构](current-database-schema.md)。

## 6. 运行时授权与缓存

`redis.enabled` 默认 `true`，省略 YAML 时也为 `true`。启用时，运行时服务 API 授权（`POST /api/v1/authorize` 和 `POST /api/v1/rbac-v1/simulate`）直接以 Redis 为来源。每个 `service + subject + normalized domain` 对应一个业务 String，key 为 `PREFIX:<QueryEscape(trim(service))>:<QueryEscape(trim(subject))>[:<QueryEscape(normalized domain)>]`；值为完整策略 JSON，字段为 `subject`、`domain`、`service_resource`、`service_active`、`catalog`、`apis` 和 `roles`。

策略不含应用标识。中心合并所有启用应用对该服务的用户权限，API 和角色取并集；某个应用停用、解除服务关联或撤权后重新计算，其他来源继续生效。空 domain 和 `global` 不追加后缀，具名 domain 使用不同 key 并保持隔离。单值上限 16 MiB，key 不设 TTL。

运行时直接 `GET` 业务 key 并严格校验类型、大小、JSON 和 `subject` / `domain` / `service_resource` 身份。缺失、类型错误、损坏、身份不匹配、超时或 Redis 异常都拒绝；不读取 `rbac:api:app:*`、`state`、Lua、锁或发布号，也不接受旧应用级 JSON fallback。启用模式要求启动时 `redis.url` 非空。

每次影响服务 API 权限的控制面保存后，控制面先取得 PostgreSQL session advisory lock，再在同一 `REPEATABLE READ` 快照中合并全部来源；锁持有到 Redis 发布结束，以串行化多个中心。Redis 层预验证完整输出后，按专用前缀 `SCAN` 严格识别业务 key，删除本轮快照中不存在的用户记录，然后逐用户 `SET` 完整 JSON。每条用户写入原子，环境或应用批次不整体原子；部分失败可能暂时保留旧权限，保存返回 `PUBLISH_PENDING`，由下一轮全量对账补发。启动和每次定时对账都全量重建，不使用增量请求号或发布号，不需要新表、migration、Redis 锁、索引或计数器。该模型没有应用级总闸门，不承诺撤权零窗口或强 fail-closed。纯菜单编辑仍走独立 current-policy，不触发 Redis 发布。

显式设置 `redis.enabled: false` 或 `NEXUSAUTHORIZATION_REDIS_ENABLED=false` 时，服务 API 使用数据库 current-policy 鉴权，不创建 Redis 客户端，也不发布 Redis 记录。开关在重启后生效；重新启用时，先设置 `redis.enabled=true` 或 `NEXUSAUTHORIZATION_REDIS_ENABLED=true` 并重启 API，启动全量重建完成后，再向该实例发送服务 API 流量。

菜单、全策略适配器和历史基准走独立的 current-policy 路径，不读取服务 API Redis 记录：每次请求在一条 MVCC 语句里读取当前规范化业务表，计算内容 SHA-256，进程内按应用复用编译索引（默认上限 64 条）。无论 Redis 开关如何，该路径都按数据库工作。应用级索引包含：

- 按 `service + method` 分组的路由集合；
- 固定路径的哈希查找；
- 参数路径的 Trie；
- 应用内角色的稀疏位图；
- `subject + domain` 到角色位图的索引。

应用级索引只服务于菜单与全策略路径，并随内容 token 或服务目录摘要失效。Redis 服务 API 入口直接读取 String，并在严格身份、大小和 JSON 校验后编译该服务的 Catalog 与用户 API 并集；内容变化时不得复用语义上不匹配的旧结果。Redis 模式不可用、String 缺失或内容无效时拒绝，不回源；Redis 禁用时服务 API 使用数据库 current-policy。菜单路径始终不依赖 Redis。

详细匹配规则和基准见 [鉴权匹配与性能](authorization-matching.md) 与 [Redis 接入说明](redis-integration.md)。

## 7. 认证与安全边界

管理端登录使用 OIDC。启用 OIDC 时必须配置 HTTPS 回调地址、OIDC client secret、会话密钥持久化目录和反向代理的 cookie 转发。

运行时 `/api/v1/authorize` 使用服务到服务 Bearer 凭证，不使用管理端浏览器 cookie。当前代码保留 `authorization.service_credential` 兼容校验；默认 Compose 未配置该凭证时，运行时鉴权接口保持关闭，等待统一 Open API Token 管理接入。

业务服务必须把 `appKey` 和 `serviceResource` 作为可信部署配置，把 `subject` 从已验证的身份上下文解析出来。不能从查询参数、请求体中的角色字段或用户可控 Header 推断授权身份。SDK 的 `Require` 在身份缺失、远程错误、非法决策和 deny 时均不调用下游处理器。

管理端 API 的错误返回统一为 `success / errorCode / errorMessage / result`；运行时鉴权成功时返回原始决策对象，认证失败返回 `401`。健康检查 `/api/healthz` 只表示 HTTP 进程存活，不代表数据库、OIDC、SCIM 或远程目录均可用。

## 8. 一致性与失败语义

- PostgreSQL 事务提交前的写入不会对新请求可见；提交成功后配置立即生效。
- Redis 模式下事务回滚时不重建 Redis，服务 API 记录保持上一份一致状态；禁用模式不执行 Redis 重建。
- 已经读到旧 String 的在途请求可能完成旧判断。全量替换没有环境级原子事务，部分发布失败时某些旧权限可能保留到下一轮对账。
- Redis 模式下，控制面先取 PostgreSQL session advisory lock，再使用同一个 repeatable-read 快照合并；Redis 层验证完整输出后才删除旧 key 和写入新 String，锁覆盖整个发布。
- 每用户 `SET` 原子，但环境或应用批次不整体原子。保存发布失败返回 `PUBLISH_PENDING`；该模型不承诺撤权零窗口或强 fail-closed 闸门。
- Redis 模式下服务 API 鉴权直接读取业务 String；缺失、类型错误、损坏、身份不匹配或 Redis 错误都拒绝，不回源数据库；显式禁用后使用数据库 current-policy。
- 目录读取失败时不能用无法验证的旧服务目录放行请求。
- Swagger 导入逐条写入，不是整批原子事务；中途失败时已成功写入的端点会保留，重试会跳过已存在的 `method + path_template`。
- 删除服务资源、取消应用服务关联、删除有子节点菜单等操作在引用未清理时返回冲突。
- SCIM 只影响用户搜索和分配界面；SCIM 不可用不会删除数据库中已有的主体绑定。

## 9. 可扩展点

二次开发优先沿以下边界扩展：

1. 在 `internal/biz` 增加业务规则，在 `internal/data/db/repo` 增加持久化，不要在 HTTP Handler 中直接写 SQL。
2. 新增管理接口时同步更新路由、统一错误映射、Swagger 文档、前端 API 封装和接口测试。
3. 新增权限维度时同时设计数据库约束、current-policy 策略编译、Redis String JSON、全量合并与发布、模拟接口和 SDK 输入，避免只改管理端表单。
4. 新增外部目录时实现 `ServiceResourceCatalog` 或独立客户端，并在启动配置校验阶段失败，而不是在运行时静默降级。
5. 新增数据库字段或约束时先修改完整初始 schema，并在独立 PostgreSQL 空库上验证初始化结果。

## 10. 已知限制

- 当前默认 `/api/v1/authorize` 没有配置服务凭证，统一 Open API Token 管理接入前需要按部署环境配置服务到服务认证。
- Dashboard 的权限中心侧栏是前端内置导航；它与业务应用的菜单资源是两套数据。
- 用户资料和组织关系由 NexusAuth 管理，权限中心只保存主体字符串和域。
- Redis 模式按 `用户 × 域 × 服务` 保存完整 JSON；大角色、完整 Catalog 和大量用户会产生 Redis 空间与全量发布开销。多个应用的 API 与角色取并集，源应用的停用或撤权需要完整重算。
- 没有审计事件查询页面、策略版本、审批和回滚。
- 健康检查是存活检查，不是完整依赖探针。

配套的配置、接口和操作入口见 [文档索引](README.md)。
