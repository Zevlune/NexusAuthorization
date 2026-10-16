# NexusAuthorization 系统设计说明

整理日期：2026-10-16。本文以当前工作区代码、`migrations/001_init.sql`、Docker 配置和现有测试为准，描述已经实现的系统行为。文中明确标记的限制不属于当前版本能力承诺。

## 1. 系统定位

NexusAuthorization 是按业务应用隔离权限边界的授权中心，负责维护服务资源目录、API 端点、菜单与按钮、角色以及主体角色绑定，并为业务服务提供运行时授权判断。

系统把“管理配置”和“运行时决策”放在同一个权限模型中：管理端保存成功后，数据在事务提交时直接生效；业务服务下一次调用鉴权接口时读取当前配置，不需要发布、审批或切换版本。

当前版本覆盖：

- 业务应用级隔离；
- 服务资源与 API 共享目录；
- 应用内角色、菜单、按钮和主体角色绑定；
- OIDC 管理端登录；
- NexusAuth 服务目录和 SCIM 用户目录接入；
- PostgreSQL 持久化和可选 Redis 当前策略缓存；
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
    API -.可选.-> Redis[(Redis 当前策略缓存)]
    API --> Catalog[NexusAuth 服务目录]
    API --> SCIM[NexusAuth SCIM 用户目录]
    Service[业务服务 / BFF / Gateway] -->|POST /api/v1/authorize| API
    Service -.Go SDK.-> API
```

组件职责如下：

| 组件 | 职责 | 状态管理 |
| --- | --- | --- |
| Dashboard | 登录后的应用选择、资源配置、角色编辑、用户分配和权限模拟 | 浏览器 session/local storage 保存页面状态；权限数据由 API 提供 |
| API Server | 认证、管理 API、授权决策、目录适配、事务边界和错误映射 | 无状态进程；会话密钥和数据库/Redis 由外部存储提供 |
| PostgreSQL | 权限主数据、约束、触发器、缓存失效标记 | 权限数据的唯一事实来源 |
| Redis | 可选的应用当前策略记录缓存 | 缓存失效或读取失败时回 PostgreSQL；不保存历史版本 |
| OIDC Provider | 管理端登录、用户身份和会话上游 | 外部身份系统 |
| Service Catalog | 服务资源目录 | local 模式使用本地表；nexusauth 模式从远程目录读取 |
| SCIM Directory | 管理端用户搜索和分页 | 用户资料不落库 |
| SDK | 业务服务内的失败关闭中间件和直接调用封装 | 不保存授权状态 |

API Server 通过 `src/internal/app/app.go` 完成装配，入口在 `src/cmd/api-server/main.go`。Dashboard 部署镜像由 Nginx 托管静态文件，将 `/api` 前缀转发到 API Server，并单独将 OIDC 回调 `/signin-oidc` 转发到 API Server。

## 3. 权限模型

应用是隔离边界，服务资源是可跨应用复用的 API 目录。应用关联服务只建立目录使用关系，不直接授予权限。

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

1. 查找并确认应用处于启用状态。
2. 确认服务资源在应用的当前关联集合中，且目录中仍然有效。
3. 按 `subject + domain` 查找主体绑定的启用角色。
4. 在指定服务和 HTTP 方法下匹配 API 路径。
5. 判断匹配的 API 是否被主体角色授予。
6. 返回 `allowed`、`effect`、`reason` 和命中的角色编码。

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

管理 API 位于 `/api/v1` 下，除 `/api/v1/authorize` 外默认受 OIDC 会话或 Bearer 管理令牌保护。RBAC v1 管理接口都要求 `app_key`；接口端点的创建、更新、导入和删除还需要 `service_resource`，且服务必须已经关联当前应用。

保存角色时，仓储层在同一个事务中校验角色、API、菜单和应用边界，并更新授权集合。主体角色替换接口按 `subject + domain` 原子替换完整角色集合，调用方需要提交希望保留的全部角色。

## 5. 数据设计

核心表包括：

| 表 | 作用 |
| --- | --- |
| `applications` | 业务应用及 `policy_token` |
| `service_resources` | local 服务目录元数据 |
| `application_service_resources` | 应用与服务的关联 |
| `api_endpoints` | RBAC v1 共享 API 目录 |
| `roles` | 应用内角色 |
| `menus` | 应用菜单、按钮和目录节点 |
| `role_api_endpoint_bindings` | 角色到 API 的授权 |
| `role_menu_bindings` | 角色到菜单/按钮的授权 |
| `subject_role_bindings` | 主体、域与角色的绑定 |
| `authorization_api_endpoints` | 兼容旧目录 API 的独立数据表 |

数据库约束和仓储校验共同保证：应用内角色编码唯一，服务内接口组合唯一，角色与菜单不跨应用，角色只能授予应用已关联服务的 API，菜单父子关系不跨应用，按钮不能作为父节点。

`applications.policy_token` 是应用级缓存身份。权限表、应用服务关联、API 目录和服务资源的写操作通过语句级触发器刷新它。它不代表业务版本，也不支持回滚或历史查询。

完整字段、约束、触发器和兼容表说明见 [当前数据库结构](current-database-schema.md)。

## 6. 运行时授权与缓存

每次鉴权先读取应用启用状态、当前 `policy_token` 和有效服务目录摘要，再根据这些信息复用进程内编译索引。索引未命中时，仓储层优先读取 Redis 当前策略记录；Redis 未配置、超时、数据缺失或编译失败时回源 PostgreSQL。

编译索引包含：

- 按 `service + method` 分组的路由集合；
- 固定路径的哈希查找；
- 参数路径的 Trie；
- 应用内角色的稀疏位图；
- `subject + domain` 到角色位图的索引。

索引只服务于当前应用和当前服务目录摘要。保存提交后，新的 `policy_token` 使旧索引和 Redis 记录失效。Redis 是性能优化项，关闭 Redis 时仍由 PostgreSQL 提供完整功能。

策略缓存不按用户、角色或单条 API 建立业务 key，也不保存策略历史。Redis 连接异常时应保持鉴权正确性，系统会回源数据库；如果数据库和目录无法确认当前状态，决策应失败关闭。

详细匹配规则和基准见 [鉴权匹配与性能](authorization-matching.md) 与 [Redis 接入说明](redis-integration.md)。

## 7. 认证与安全边界

管理端登录使用 OIDC。启用 OIDC 时必须配置 HTTPS 回调地址、OIDC client secret、会话密钥持久化目录和反向代理的 cookie 转发。

运行时 `/api/v1/authorize` 使用服务到服务 Bearer 凭证，不使用管理端浏览器 cookie。当前代码保留 `authorization.service_credential` 兼容校验；默认 Compose 未配置该凭证时，运行时鉴权接口保持关闭，等待统一 Open API Token 管理接入。

业务服务必须把 `appKey` 和 `serviceResource` 作为可信部署配置，把 `subject` 从已验证的身份上下文解析出来。不能从查询参数、请求体中的角色字段或用户可控 Header 推断授权身份。SDK 的 `Require` 在身份缺失、远程错误、非法决策和 deny 时均不调用下游处理器。

管理端 API 的错误返回统一为 `success / errorCode / errorMessage / result`；运行时鉴权成功时返回原始决策对象，认证失败返回 `401`。健康检查 `/api/healthz` 只表示 HTTP 进程存活，不代表数据库、OIDC、SCIM 或远程目录均可用。

## 8. 一致性与失败语义

- PostgreSQL 事务提交前的写入不会对新请求可见；提交成功后配置立即生效。
- 事务回滚时权限数据和 `policy_token` 一起回滚。
- 已经读到旧索引的在途请求可能完成旧判断；下一次请求会按新 token 重新加载。
- 目录读取失败时不能用无法验证的旧服务目录放行请求。
- Swagger 导入逐条写入，不是整批原子事务；中途失败时已成功写入的端点会保留，重试会跳过已存在的 `method + path_template`。
- 删除服务资源、取消应用服务关联、删除有子节点菜单等操作在引用未清理时返回冲突。
- SCIM 只影响用户搜索和分配界面；SCIM 不可用不会删除数据库中已有的主体绑定。

## 9. 可扩展点

二次开发优先沿以下边界扩展：

1. 在 `internal/biz` 增加业务规则，在 `internal/data/db/repo` 增加持久化，不要在 HTTP Handler 中直接写 SQL。
2. 新增管理接口时同步更新路由、统一错误映射、Swagger 文档、前端 API 封装和接口测试。
3. 新增权限维度时同时设计数据库约束、策略编译结构、缓存失效触发器、模拟接口和 SDK 输入，避免只改管理端表单。
4. 新增外部目录时实现 `ServiceResourceCatalog` 或独立客户端，并在启动配置校验阶段失败，而不是在运行时静默降级。
5. 新增数据库字段或约束时先修改完整初始 schema，并在独立 PostgreSQL 空库上验证初始化结果。

## 10. 已知限制

- 当前默认 `/api/v1/authorize` 没有配置服务凭证，统一 Open API Token 管理接入前需要按部署环境配置服务到服务认证。
- Dashboard 的权限中心侧栏是前端内置导航；它与业务应用的菜单资源是两套数据。
- 用户资料和组织关系由 NexusAuth 管理，权限中心只保存主体字符串和域。
- 没有审计事件查询页面、策略版本、审批和回滚。
- 健康检查是存活检查，不是完整依赖探针。

配套的配置、接口和操作入口见 [文档索引](README.md)。
