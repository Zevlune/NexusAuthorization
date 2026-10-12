# 应用边界 API RBAC 设计方案

Reference: [参考接口](reference-api-rbac-v1.md)

## 1. 目标与边界

业务应用（Application）是菜单、角色、用户角色绑定和权限发布的边界。服务资源（ServiceResource）维护共享的 API 目录；一个应用可以显式关联多个服务资源，一个服务资源也可以被多个应用复用。

服务关联只确定应用可以使用哪些服务的 API，不会自动授予任何 API 权限。角色只能授予应用已关联服务中的 API，菜单和角色都不能跨应用引用。菜单直接属于应用，不要求创建 OAuth 客户端。

运行时鉴权必须同时指定 `appKey` 和 `serviceResource`，默认拒绝。前端菜单权限不能代替后端 API 鉴权。

```text
Subject + Domain -> Application -> Role -> API Endpoint
                                      \-> Menu
```

第一版不引入权限编码、策略文档、`allow / deny` 规则、ABAC 表达式、行级或字段级条件。local 服务资源和 API 目录由权限中心维护；NexusAuth 服务资源通过远程 `List`/`Get` 直接读取，不同步或持久化远程元数据。应用负责组合和发布授权边界。

## 2. 核心关系

```text
Application
  ├─ ApplicationServiceResource ──(service key)─> ServiceResource ──(service key)─> ApiEndpoint
  ├─ Role ── RoleApiEndpointBinding ──> ApiEndpoint
  ├─ Role ── RoleMenuBinding ──> Menu
  ├─ SubjectRoleBinding <── Subject + Domain
  └─ RBACRelease ──> immutable snapshot
```

| 对象 | 职责 | 约束 |
| --- | --- | --- |
| Application | 业务应用的权限边界和发布范围 | `app_key` 唯一；角色、菜单、用户角色绑定和发布属于应用 |
| ServiceResource | 共享的服务/API 目录命名空间 | 可被多个应用关联；服务本身不授予权限 |
| ApplicationServiceResource | 应用与服务资源 key 的显式多对多关联 | 只允许新关联存在且启用的服务；移除有 API 授权的服务时拒绝；不可用的既有关联保留为 key-only 占位以便移除 |
| API Endpoint | 描述服务中的 method、path、摘要、来源和启停状态 | 通过 `service_resource_key` 逻辑归属；不能跨应用角色授权 |
| Role | 应用内可分配的 API 和菜单访问能力 | 角色不跨应用；用户不会直接绑定 API |
| SubjectRoleBinding | 表示 subject 在 domain 下拥有的应用角色 | 角色必须属于同一应用 |
| RBAC Release | 应用范围的不可变授权快照 | 运行时只读取已发布快照 |

## 3. RDS 数据模型

应用是隔离边界，服务资源是共享目录。数据库通过应用外键、同应用复合外键和触发器阻止跨应用角色、菜单、用户绑定和 API 授权；服务资源与应用关联、API 端点之间使用 key，数据库不建立指向 `service_resources` 的外键，目录校验由仓储完成。

### 3.1 应用与服务目录

```text
applications
- id UUID PK
- app_key VARCHAR(128) UNIQUE
- display_name VARCHAR(200)
- description TEXT
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

### 3.3 用户绑定、发布和快照

```text
subject_role_bindings
- id UUID PK
- application_id UUID FK applications
- subject VARCHAR(200)
- domain VARCHAR(200)
- role_id UUID FK roles
- created_at / updated_at

UNIQUE(application_id, subject, domain, role_id)

rbac_releases
- id UUID PK
- application_id UUID FK applications
- version BIGINT
- status VARCHAR                    # published / superseded
- description TEXT
- checksum VARCHAR
- published_by VARCHAR
- published_at TIMESTAMPTZ

rbac_release_snapshots
- release_id UUID PK FK rbac_releases
- snapshot JSONB
```

发布快照包含应用标识、配置目录中当前 active service keys 对应的服务标识、服务 API、应用角色、角色 API/菜单授权、菜单和用户角色绑定。运行时不直接读取可编辑草稿表，也不调用远程服务目录；远程目录调用只发生在管理读取和发布快照构建阶段。

## 4. 管理 API

### 4.1 应用和服务关联

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/v1/applications` | 返回 `{items: Application[]}` |
| POST | `/v1/applications` | 创建应用；请求字段 `key, display_name, description, enabled` |
| PUT | `/v1/applications/{key}` | 更新已有应用；`key` 由路径固定，不创建新应用 |
| GET | `/v1/applications/{key}/service-resources` | 返回应用当前关联的目录资源；不可用的既有关联返回 inactive key-only 占位 |
| PUT | `/v1/applications/{key}/service-resources` | 使用 `{service_resource_keys: string[]}` 原子替换关联 |

服务资源目录仍使用 `/v1/service-resources` 查询或维护；local 来源读写本地目录，NexusAuth 来源的 `List`/`Get` 直接读取远程目录，不写入本地 `service_resources`，也不使用本地元数据回退。管理关联、API 管理和发布构建使用该目录验证 key；运行时鉴权只读取已发布快照。

### 4.2 应用范围 RBAC

以下 `/v1/rbac-v1` 管理接口都要求 `app_key` 查询参数。旧的按 `service_resource` 隔离角色、菜单、用户绑定和发布的入口已退役。

| 方法 | 路径 | 必要范围参数 |
| --- | --- | --- |
| GET/POST/DELETE | `/v1/rbac-v1/endpoints`、`/v1/rbac-v1/endpoints/{id}` | `app_key`；创建、删除和 Swagger 导入另需 `service_resource` |
| POST | `/v1/rbac-v1/endpoints/import-swagger` | `app_key`、`service_resource` |
| GET/POST/DELETE | `/v1/rbac-v1/roles`、`/v1/rbac-v1/roles/{id}` | `app_key` |
| GET/PUT | `/v1/rbac-v1/subject-roles` | `app_key` |
| GET/POST/DELETE | `/v1/rbac-v1/menus`、`/v1/rbac-v1/menus/{id}` | `app_key` |
| GET | `/v1/rbac-v1/subject-menus` | `app_key`、`subject`、`domain` |
| POST/GET | `/v1/rbac-v1/releases` | `app_key` |
| POST | `/v1/rbac-v1/simulate` | `app_key`；请求体另需 `service_resource` |

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
    G --> H[发布应用快照]
    H --> I[运行时鉴权]
```

1. 创建或选择应用。
2. 关联一个或多个启用的服务资源；关联动作本身不授予权限。
3. 在已关联服务范围内导入 Swagger/OpenAPI，或维护 API 目录。
4. 创建应用角色，在角色中选择来自已关联服务的 API，并配置菜单授权。
5. 为 `subject + domain` 绑定一个或多个应用角色。
6. 使用 `app_key`、`service_resource`、subject、domain、method 和 path 模拟请求。
7. 发布应用；任何 API、角色、菜单或用户角色变化都要重新发布才对运行时生效。

## 6. 发布校验

发布必须阻止：

- 应用、服务资源、角色、菜单或 Endpoint 编码重复；
- 角色、菜单、用户绑定、角色菜单或角色 API 关联跨应用；
- 角色授予未关联服务中的 API；
- 关联记录引用不存在或已停用对象；
- 菜单父子关系跨应用；
- API 的 service resource + method + path 重复；
- 快照无法完整构建。

应用关联服务变化、API 目录变化、角色变化或用户角色变化不会修改历史快照。回滚读取目标版本并作为新应用版本重新发布。

## 7. 运行时鉴权

业务服务调用 `POST /v1/authorize` 时提交：

```json
{
  "appKey": "books",
  "serviceResource": "book-service",
  "subject": "user:1",
  "domain": "global",
  "method": "GET",
  "path": "/api/books"
}
```

鉴权先按 `appKey` 读取当前应用发布快照，再按 `serviceResource`、method 和 path 匹配 API，最后检查该应用中 subject 在 domain 下的启用角色是否授予了目标 API。应用不存在、服务未关联、没有可用快照、API 不存在或没有角色授权时默认拒绝。

运行时 SDK 已要求调用方同时提供应用和服务上下文，这是 breaking change。请参阅 [SDK application integration](sdk-application-integration.md)；`Client.Check` 的 `Request` 需要 `AppKey` 和 `ServiceResource`，`Client.Require` 需要同时传入两个固定的受信配置值，不能从用户输入读取。

## 8. 初始化与兼容策略

当前版本使用单一初始化脚本 `migrations/001_init.sql`。本地重建前必须备份数据库，然后重建 `permission_center` 并清空旧数据，再执行：

```bash
psql "$NEXUSAUTHORIZATION_DATABASE_URL" -v ON_ERROR_STOP=1 --single-transaction -f migrations/001_init.sql
```

不要执行 `migrations/002_applications.sql`、旧的增量升级脚本或旧数据种子。该流程没有在线旧库升级路径；旧数据只能从备份恢复，或作为单独的数据导入任务审查后处理。

按服务资源隔离的旧角色、菜单、用户绑定、发布和模拟路由已退役，避免并存两套不一致的授权入口。共享服务资源目录、API 目录维护和既有部署、OIDC/NexusAuth 认证配置继续保留。

## 9. 后续扩展

后续可以增加显式 deny、策略审批、审计差异、用户组和数据级权限，但必须定义新的数据结构与运行时语义，不应把跨应用或隐式服务授权塞进现有角色 API 关联表。
