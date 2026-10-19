# 应用边界 API RBAC 设计方案

Reference: [参考接口](reference-api-rbac-v1.md)

## 1. 目标与边界

业务应用（Application）是菜单、角色和用户角色绑定的边界。服务资源（ServiceResource）维护共享的 API 目录；一个应用可以显式关联多个服务资源，一个服务资源也可以被多个应用复用。

服务关联只确定应用可以使用哪些服务的 API，不会自动授予任何 API 权限。角色只能授予应用已关联服务中的 API，菜单和角色都不能跨应用引用。菜单直接属于应用，不要求创建 OAuth 客户端。

运行时鉴权必须同时指定 `appKey` 和 `serviceResource`，默认拒绝。前端菜单权限不能代替后端 API 鉴权。

```text
Subject + Domain -> Application -> Role -> API Endpoint
                                      \-> Menu
```

第一版不引入权限编码、策略文档、`allow / deny` 规则、ABAC 表达式、行级或字段级条件。local 服务资源和 API 目录由权限中心维护；NexusAuth 服务资源通过远程 `List`/`Get` 直接读取，不同步或持久化远程元数据。应用负责组合授权边界；角色、绑定、菜单和 API 保存提交后立即生效。

## 2. 核心关系

```text
Application
  ├─ ApplicationServiceResource ──(service key)─> ServiceResource ──(service key)─> ApiEndpoint
  ├─ Role ── RoleApiEndpointBinding ──> ApiEndpoint
  ├─ Role ── RoleMenuBinding ──> Menu
  └─ SubjectRoleBinding <── Subject + Domain
```

| 对象 | 职责 | 约束 |
| --- | --- | --- |
| Application | 业务应用的权限边界 | `app_key` 唯一；角色、菜单和用户角色绑定属于应用；保存提交后直接生效 |
| ServiceResource | 共享的服务/API 目录命名空间 | 可被多个应用关联；服务本身不授予权限 |
| ApplicationServiceResource | 应用与服务资源 key 的显式多对多关联 | 只允许新关联存在且启用的服务；移除有 API 授权的服务时拒绝；不可用的既有关联保留为 key-only 占位以便移除 |
| API Endpoint | 描述服务中的 method、path、摘要、来源和启停状态 | 通过 `service_resource_key` 逻辑归属；角色只能引用其应用已关联服务中的接口 |
| Role | 应用内可分配的 API 和菜单访问能力 | 角色不跨应用；用户不会直接绑定 API |
| SubjectRoleBinding | 表示 subject 在 domain 下拥有的应用角色 | 角色必须属于同一应用 |

## 3. RDS 数据模型

应用是隔离边界，服务资源是共享目录。数据库通过应用外键、同应用复合外键和触发器阻止跨应用角色、菜单、用户绑定和 API 授权；服务资源与应用关联、API 端点之间使用 key，数据库不建立指向 `service_resources` 的外键，目录校验由仓储完成。

### 3.1 应用与服务目录

```text
applications
- id UUID PK
- app_key VARCHAR(128) UNIQUE
- display_name VARCHAR(200)
- description TEXT
- url VARCHAR(2048)                  # 可选的 HTTP(S) 应用地址
- enabled BOOLEAN
- created_at / updated_at

application_service_resources
- application_id UUID FK applications
- service_resource_key VARCHAR(128)       # 目录 key；无 service_resources FK
- created_at
- PRIMARY KEY(application_id, service_resource_key)

service_resources
- id UUID PK
- resource_key VARCHAR(128) UNIQUE
- display_name VARCHAR(200)
- audience VARCHAR(200)
- source VARCHAR                 # local / nexusauth
- external_id VARCHAR(200) NULL  # local 时必须为 NULL
- enabled BOOLEAN
- created_at / updated_at
```

应用服务关联不隐式产生角色或 API 授权。新关联服务必须由当前目录确认存在且启用；远程服务不可用时，已有关联以 inactive key-only 占位返回以便移除。角色已有 API 授权时，移除对应服务关联会被拒绝。

### 3.2 服务 API、角色和菜单

```text
api_endpoints
- id UUID PK
- service_resource_key VARCHAR(128)       # 目录 key；无 service_resources FK
- endpoint_key VARCHAR
- method VARCHAR                  # GET / POST / PUT ...
- path_template VARCHAR           # /books/{id}
- module VARCHAR
- summary TEXT
- enabled BOOLEAN
- source VARCHAR                  # swagger / manual
- last_discovered_at TIMESTAMPTZ
- created_at / updated_at

UNIQUE(service_resource_key, method, path_template)
UNIQUE(service_resource_key, endpoint_key)

roles
- id UUID PK
- application_id UUID FK applications
- code VARCHAR
- name VARCHAR
- description TEXT
- enabled BOOLEAN
- created_at / updated_at

UNIQUE(application_id, code)

role_api_endpoint_bindings
- role_id UUID FK roles
- endpoint_id UUID FK api_endpoints
- created_at
- PRIMARY KEY(role_id, endpoint_id)

menus
- id UUID PK
- application_id UUID FK applications
- parent_id UUID FK menus
- menu_type VARCHAR                # directory / menu / button
- code VARCHAR
- name VARCHAR
- route VARCHAR
- component VARCHAR
- icon VARCHAR
- sort_order INTEGER
- enabled BOOLEAN
- created_at / updated_at

UNIQUE(application_id, code)

role_menu_bindings
- role_id UUID FK roles
- menu_id UUID FK menus
- created_at
- PRIMARY KEY(role_id, menu_id)
```

`role_api_endpoint_bindings` 只允许角色授予其应用已关联服务中的 API。`role_menu_bindings` 只允许角色和菜单属于同一应用；菜单父子关系也必须属于同一应用。

### 3.3 用户绑定

```text
subject_role_bindings
- id UUID PK
- application_id UUID FK applications
- subject VARCHAR(200)
- domain VARCHAR(200) NOT NULL DEFAULT ''   -- 空串为默认域
- role_id UUID FK roles
- created_at / updated_at

UNIQUE(application_id, subject, domain, role_id)
```

数据库只保存业务配置，没有 `policy_token`、角色/服务 revision 或发布历史。表上只保留四条业务保护触发器；Redis 启用时，一致性由控制面在保存业务表后重建 Redis 记录实现。

`redis.enabled` 默认 `true`。启用时，服务 API 鉴权读取该「应用 key＋主体＋服务＋归一化 domain」的 Redis 记录，不读取数据库、不回源；记录缺失、未 ready 或 Redis 不可用时直接拒绝。显式禁用时，服务 API 使用数据库 current-policy，不创建 Redis 客户端，也不发布记录。菜单和全策略适配器始终走独立的 current-policy 路径，在一条 MVCC 语句里读取当前可编辑表并在进程内编译。上述路径都只使用当前数据，不读取草稿、历史快照或发布版本。匹配数据结构见 [鉴权匹配与性能](authorization-matching.md)，生效规则见 [权限生效设计](permission-lifecycle.md)，缓存实现见 [Redis 接入说明](redis-integration.md)。

## 4. 管理 API

### 4.1 应用和服务关联

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/applications` | 返回 `{items: Application[]}` |
| POST | `/api/v1/applications` | 创建应用；请求字段 `key, display_name, description, url, enabled` |
| PUT | `/api/v1/applications/{key}` | 更新已有应用；`key` 由路径固定，不创建新应用 |
| GET | `/api/v1/applications/{key}/service-resources` | 返回应用当前关联的目录资源；不可用的既有关联返回 inactive key-only 占位 |
| PUT | `/api/v1/applications/{key}/service-resources` | 使用 `{service_resource_keys: string[]}` 原子替换关联 |

服务资源目录仍使用 `/api/v1/service-resources` 查询或维护；local 来源读写本地目录，NexusAuth 来源的 `List`/`Get` 直接读取远程目录，不写入本地 `service_resources`，也不使用本地元数据回退。管理关联和 API 管理使用该目录验证 key；运行时鉴权读取已保存的关联，并把目录中的有效服务集合摘要纳入缓存身份。

### 4.2 应用范围 RBAC

以下 `/api/v1/rbac-v1` 管理接口都要求 `app_key` 查询参数。旧的按 `service_resource` 隔离角色、菜单和用户绑定的入口已退役。

| 方法 | 路径 | 必要范围参数 |
| --- | --- | --- |
| GET/POST/DELETE | `/api/v1/rbac-v1/endpoints`、`/api/v1/rbac-v1/endpoints/{id}` | `app_key`；创建、删除和 Swagger 导入另需 `service_resource` |
| POST | `/api/v1/rbac-v1/endpoints/import-swagger` | `app_key`、`service_resource` |
| GET/POST/DELETE | `/api/v1/rbac-v1/roles`、`/api/v1/rbac-v1/roles/{id}` | `app_key` |
| GET/PUT | `/api/v1/rbac-v1/subject-roles` | `app_key` |
| GET/POST/DELETE | `/api/v1/rbac-v1/menus`、`/api/v1/rbac-v1/menus/{id}` | `app_key` |
| GET | `/api/v1/rbac-v1/subject-menus` | `app_key`、`subject`；`domain` 可选 |
| POST | `/api/v1/rbac-v1/simulate` | `app_key`；请求体另需 `service_resource` |

API 目录列表不带 `service_resource` 时返回该应用已关联服务中的 API；Swagger 导入必须明确指定已关联的 `service_resource`，不会因应用关联而自动导入或授权。

## 5. 配置流程

```mermaid
flowchart LR
    A[选择或创建应用] --> B[显式关联服务资源]
    B --> C[选择已关联服务并导入 Swagger]
    C --> D[创建应用角色并勾选 API]
    D --> E[配置应用菜单和角色菜单]
    E --> F[为 subject + domain 绑定角色]
    F --> G[使用 app_key + service_resource 模拟]
    G --> H[运行时鉴权]
```

1. 创建或选择应用。
2. 关联一个或多个启用的服务资源；关联动作本身不授予权限。
3. 在已关联服务范围内导入 Swagger/OpenAPI，或维护 API 目录。
4. 创建应用角色，在角色中选择来自已关联服务的 API，并配置菜单授权。
5. 为 `subject + domain` 绑定一个或多个应用角色；不指定具名域时使用默认空域。
6. 保存后即生效：API、角色、菜单或用户角色变化在保存事务提交后直接进入运行时判断。
7. 使用 `app_key`、`service_resource`、subject、method 和 path 模拟当前配置（domain 可选），再验证实际业务请求。

## 6. 写入校验与生效语义

下列约束在写入阶段由仓储检查与数据库唯一约束/触发器保证，写入失败返回 `400` 或 `409`：

- 应用 key、服务 key、应用内角色/菜单编码、服务内 Endpoint 标识与 method+path 的唯一性；
- 角色、菜单、主体绑定、角色菜单、角色 API 关联不跨应用；
- 角色授予的 API 所在服务已关联当前应用且启用；
- 菜单父子关系属于同一应用（数据库复合外键）；
- 按钮不能作为父节点、不能有子节点，菜单树不存在循环。

写入成功后不生成任何版本或快照：事务提交业务表；Redis 启用时随后由控制面重建受影响应用的 Redis 记录，Redis 禁用时直接使用数据库 current-policy。提交前已开始读取的在途请求可以完成其已读到的判断。应用不存在、已停用或写入约束不满足时整个事务回滚，不执行 Redis 重建。当前没有历史版本、快照表或回滚入口。

## 7. 运行时鉴权

业务服务调用 `POST /api/v1/authorize` 时提交：

```json
{
  "appKey": "books",
  "serviceResource": "book-service",
  "subject": "user:1",
  "domain": "",
  "method": "GET",
  "path": "/api/books"
}
```

Redis 启用时，鉴权用 `appKey`、`serviceResource`、subject 和归一化 domain 直接读取该身份的 `SubjectAPIPolicy` 记录，再按 method 和 path 匹配 API；记录已聚合该用户在该服务上的启用角色授权。应用记录缺失、未 ready 或 Redis 读取失败时返回错误并拒绝。显式禁用 Redis 时，服务 API 鉴权改用数据库 current-policy，不连接或发布 Redis。`domain` 可省略，省略、空白和旧值 `global` 都归一为空默认域，具名域保持独立。Redis 记录中 `service_active=false`、API 不存在或未被授予时返回正常 deny 决策（`allowed=false`）。

Redis 启用时，应用停用会触发重建并把受影响记录标记为 `service_active=false`；保存 API 角色授权、角色启停和主体角色绑定会触发受影响应用重建。保存角色时附带的菜单授权也会因同一角色保存路径触发整体重建，但不改变 Service API 记录内容；单独保存或删除菜单节点不触发重建，由菜单 current-policy 路径读取。Redis 禁用时，这些修改由数据库 current-policy 在后续请求中读取。响应不包含发布版本号。

运行时 SDK 已要求调用方同时提供应用和服务上下文，这是 breaking change。请参阅 [SDK application integration](sdk-application-integration.md)；`Client.Check` 的 `Request` 需要 `AppKey` 和 `ServiceResource`，`Client.Require` 需要同时传入两个固定的受信配置值，不能从用户输入读取。

## 8. 初始化与兼容策略

首次上线使用空库 `nexus-authorization`。PostgreSQL 管理员连接维护库 `postgres` 执行一次 `migrations/000_create_database.sql`；该脚本是独立执行的 `CREATE DATABASE "nexus-authorization" WITH TEMPLATE template0 ENCODING 'UTF8';`，不能放进事务。使用独立应用用户时，管理员须先创建该用户，再把数据库所有者改给它：

```sql
ALTER DATABASE "nexus-authorization" OWNER TO "nexus-authorization";
```

将 `NEXUSAUTHORIZATION_DATABASE_URL` 指向这个已存在的空库，并由库拥有者执行：

```bash
sh src/scripts/init-db.sh
```

`init-db.sh` 只执行 `migrations/001_init.sql`。该脚本自带 `BEGIN` / `COMMIT`，创建 `nexusauthorization` schema、完整初始结构和 `applications.url`，不插入业务种子数据，也不创建 `schema_migrations`；不要再包一层事务。数据库初始化按首次上线流程处理：删除旧数据库后，在空库执行 `migrations/001_init.sql`。完整说明见 [数据库初始化](../migrations/README.md)。

按服务资源隔离的旧角色、菜单、用户绑定和模拟路由已退役，避免并存两套不一致的授权入口。共享服务资源目录、API 目录维护和 OIDC/NexusAuth 认证配置继续保留。第一次安装的完整结构和保存即生效语义都由 `migrations/001_init.sql` 直接创建。

## 9. 未来方向（未实现）

以下能力属于尚未设计的工作，当前代码没有任何对应接口、表或运行时语义：显式 deny 与 deny 优先级、策略文档与策略审批、审计差异对比、发布版本与历史回滚、用户组与组织架构、数据行级/字段级权限。若要实现，必须新增独立的数据结构与运行时语义，不应把跨应用或隐式服务授权塞进现有角色 API 关联表。
