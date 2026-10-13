# NexusAuthorization 中文使用文档

本文面向三类读者，可以按角色选择入口：

| 读者 | 关注章节 |
| --- | --- |
| 部署人员 | 首次部署、数据库迁移、配置项与安全要求、生产检查清单 |
| 权限管理员 | 系统概念、登录与应用管理、服务资源/API、菜单按钮、角色、subject/domain 授权、发布与模拟、API Token |
| 业务服务开发者 | 运行时 RBAC 调用、API Token 调用、Go SDK 接入、故障排查 |

本文内容以仓库当前代码和 `doc/` 下既有文档为依据：`README.md`、`doc/current-system-overview.md`、`doc/rbac-v1-design.md`、`doc/api-token-integration.md`、`doc/api-token-design.md`、`doc/docker-deployment-1.0.2.md`、`doc/reference-api-rbac-v1.md`、`src/internal/server/http/routes.go`、`src/configs/app.yaml`、`migrations/` 和 `sdk/`。示例中的应用键、服务键、主体和凭证均为占位符，请替换为真实值。

## 目录

- [1. 系统概念](#1-系统概念)
- [2. 首次部署](#2-首次部署)
- [3. 数据库迁移](#3-数据库迁移)
- [4. 配置项与安全要求](#4-配置项与安全要求)
- [5. 登录与应用管理](#5-登录与应用管理)
- [6. 服务资源、API 与 Swagger](#6-服务资源api-与-swagger)
- [7. 菜单与按钮](#7-菜单与按钮)
- [8. 角色](#8-角色)
- [9. subject/domain 授权](#9-subjectdomain-授权)
- [10. 发布与模拟](#10-发布与模拟)
- [11. 运行时 RBAC 调用](#11-运行时-rbac-调用)
- [12. API Token](#12-api-token)
- [13. Go SDK 接入](#13-go-sdk-接入)
- [14. 故障排查](#14-故障排查)
- [15. 生产检查清单](#15-生产检查清单)

## 1. 系统概念

### 1.1 核心对象

| 对象 | 当前职责 | 示例 |
| --- | --- | --- |
| 业务应用（Application） | 权限配置和发布的边界，拥有菜单、按钮、角色、用户绑定和发布版本 | `business.portal`：业务管理平台 |
| 服务资源（ServiceResource） | 为服务/BFF 建立共享 API 目录 | `order.service`：订单服务 |
| API 接口（Endpoint） | 某个服务的 HTTP 方法和路径 | `GET /api/orders/{id}` |
| 菜单 | 应用内的页面入口，可组成父子层级 | 订单管理 |
| 按钮 | 菜单页面内的操作权限节点 | `orders:create`：创建订单 |
| 角色 | 组合应用菜单/按钮和已关联服务的 API | 订单管理员 |
| 主体与域（subject + domain） | 标识被授权的身份及其授权上下文 | `subject=user-001`，`domain=global` |
| API Token | 应用范围的机器主体凭证，可授予多个已关联服务的 API | `service=directory-sync` |
| 发布版本（Release） | 保存应用某一时刻的权限配置快照 | 应用发布 v1、v2 |

### 1.2 应用与服务资源的关系

业务应用是权限配置和发布边界，服务资源是共享的 API 目录。两者是多对多关系：一个应用可以关联多个服务，同一个服务也可以被多个应用复用。

```mermaid
flowchart LR
    A[业务应用] --> L[关联服务]
    L --> S[共享服务资源]
    S --> E[API 接口]
    A --> M[菜单与按钮树]
    A --> R[应用角色]
    R --> E
    R --> M
    U[身份主体 + 域] --> B[应用内角色绑定]
    B --> R
    A --> T[API Token]
    T --> E
    A --> P[发布版本与快照]
    P --> D[运行时 API 判断]
```

关键规则：

- **关联服务本身不授予权限**。把服务关联到应用只表示“这个应用可以使用该服务的 API”，用户仍然需要角色才能访问。
- API 属于服务，角色和菜单属于应用。角色只能授予该应用已关联服务中的 API。
- 应用删除服务关联前，必须先移除角色中对该服务 API 的授权。
- 业务应用与 OAuth 客户端是两个概念。这里不管理业务应用的 OAuth 客户端，也不要求新增业务应用时同步创建客户端；管理端登录使用配置中的 OIDC 客户端。

### 1.3 草稿与已发布快照的区别

所有可编辑的配置（服务关联、API 目录、角色与授权、菜单、主体角色绑定）都是**草稿**。运行时鉴权**只读取已发布快照**，默认拒绝：

| 维度 | 草稿 | 已发布快照 |
| --- | --- | --- |
| 数据 | 可编辑的管理表 | `rbac_release_snapshots` 中的 JSONB |
| 生效时机 | 保存即写入管理表 | 必须执行“发布”后生效 |
| 运行时读取 | 不读取 | 鉴权和模拟只读活动版本 |
| 变更影响 | 不改变现有快照 | 需重新发布才进入运行时判断 |

因此，普通配置修改（新增 API、调整角色授权、改菜单、改主体绑定）**都需要重新发布**才会影响运行时结果。删除接口、撤销授权、解除服务关联同样要发布对应应用才能撤销旧快照中的权限。唯一例外是停用整个应用：后端会直接取不到有效应用/活动版本，无需等待下一次发布即可阻止应用级鉴权。

历史快照不可变，修改历史版本不会回写。当前管理端**没有**历史恢复或回滚入口（也没有版本差异对比、发布审批）；发布历史只用于查看版本号和状态。要“回退”到某个历史状态，只能由管理员在管理端手工把草稿配置改回目标状态（服务关联、API、角色与授权、菜单、主体绑定），再执行一次常规发布生成新的版本。旧版本不会自动重新激活。

### 1.4 两条授权通道：RBAC 发布机制与 API Token

系统有两种访问控制机制，二者独立，不能互相替代：

| 机制 | 主体 | 生效方式 | 用途 |
| --- | --- | --- | --- |
| 应用 RBAC | 人（`subject + domain`）通过角色 | 必须先发布快照 | 用户访问业务 API、查询主体菜单 |
| API Token | 机器主体（`user` 或 `service`） | 创建/更新立即生效，无需发布 | 服务对服务、开放 API 的机器凭证 |

要点：

- RBAC 的授权来自已发布快照；API Token 的授权来自 Token 记录和权限快照，运行时实时查询本地数据库，**不依赖发布，也不需要发布权限版本**。
- API Token 有独立的内部校验接口 `/internal/v1/api-tokens/authorize`，使用独立内部密钥，不走 OIDC 登录，也不用 RBAC 发布快照。
- 一个 Token 只能属于一个应用，但可以覆盖该应用下多个已关联服务的 API，不能跨应用。

### 1.5 服务目录模式：local 与 nexusauth

服务资源来源由 `NEXUSAUTHORIZATION_SERVICE_RESOURCE_SOURCE` 控制：

| 模式 | 行为 |
| --- | --- |
| `local` | 只读取本地 `service_resources` 表；可在管理端新增、编辑、删除服务资源。 |
| `nexusauth` | `List`/`Get` 直接读取远程目录服务并规范化返回，不读取、写入或回退到本地 `service_resources` 元数据；管理端隐藏新增、编辑、删除入口，后端也拒绝资源写操作。 |

无论哪种模式，应用关联、API 端点、角色授权和发布构建都通过服务 key 校验目录。运行时鉴权只读已发布快照，**不会**在请求路径调用远程服务目录。

本地开发默认 `local`；生产 Compose 默认 `nexusauth`。远程服务资源消失或停用后，已有应用关联以 key-only 停用占位返回，便于移除；不能通过新请求添加不可用的 key。

### 1.6 页面与路由

登录后除应用选择页外，其余页面都在所选应用上下文内工作。

| 一级入口 | 二级页面 | 路由 |
| --- | --- | --- |
| 应用选择 | 选择业务应用 | `/select-application` |
| 配置总览 | — | `/dashboard` |
| 资源管理 | 服务资源 | `/service-resources` |
| 资源管理 | API 接口 | `/api-resources` |
| RBAC 授权 | API Token | `/api-tokens` |
| 资源管理 | 菜单与按钮 | `/menus` |
| RBAC 授权 | 角色管理 | `/roles` |
| RBAC 授权 | 用户授权 | `/bindings` |
| 发布与验证 | 发布版本 | `/releases` |
| 发布与验证 | 权限模拟 | `/simulate` |

旧入口 `/select-service-resource` 会重定向到 `/select-application`。工作台侧栏是权限中心自身的静态导航，与业务应用的菜单树数据不同。

## 2. 首次部署

后端默认监听 `:8080`，前端默认 `5274`。有两种 Compose 和一种源码运行方式。

### 2.1 Docker Compose 开发版（含 PostgreSQL）

用于本地体验或测试，默认禁用 SSO、使用本地服务资源，端口只绑定本机，前端 `http://localhost:5274`，数据库 `localhost:55434`。新数据卷首次启动时会执行 `migrations/` 中的 SQL。

```sh
cp .env.dev.example .env.dev
# 按需修改 .env.dev，例如 POSTGRES_PORT、DASHBOARD_PORT
docker compose --env-file .env.dev -f docker-compose-dev.yml config -q
docker compose --env-file .env.dev -f docker-compose-dev.yml up -d
```

打开 `http://localhost:5274`。数据库首次使用空数据卷时自动初始化；已有数据卷不会重跑初始化脚本，需要按第 3 节手动升级。

查看状态和日志：

```sh
docker compose --env-file .env.dev -f docker-compose-dev.yml ps
docker compose --env-file .env.dev -f docker-compose-dev.yml logs --tail=200 permission-api dashboard
```

停止时不要使用 `down -v`，它会删除数据库卷。

### 2.2 Docker Compose 生产版（外部 PostgreSQL）

只包含后端和前端，连接已有 PostgreSQL，固定启用 SSO，不创建数据库、不自动执行迁移。生产数据库须预先存在并完成迁移。

```sh
cp .env.example .env
# 填写外部数据库、OIDC、服务资源、内部密钥和业务凭证
docker compose -f docker-compose.yml config -q
docker compose -f docker-compose.yml pull
docker compose -f docker-compose.yml up -d
```

镜像为 `18790997531/nexusauthorization:1.0.2` 和 `18790997531/nexusauthorization-dashboard:1.0.2`，架构为 Linux amd64。生产环境前端 Nginx 把 `/api/` 去掉前缀后转发到后端，并把 `/signin-oidc` 转给后端回调。将 `permissions.example.com` 的 HTTPS 入口反向代理到 dashboard 的 `5274` 端口，身份平台必须精确注册相同的回调与退出地址。

### 2.3 源码运行

后端位于 `src/`，需要 Go 1.25 和 PostgreSQL；前端位于 `dashboard/`，需要 Node.js 与 npm。

后端从当前工作目录读取 `configs/app.yaml`。Go 进程不会自动加载仓库根目录的 `.env`，需先导出环境变量：

```sh
# 从仓库根目录执行；确认 .env 是自己维护的可信 shell 配置
set -a
. ./.env
set +a
cd src
go run ./cmd/api-server
```

另开终端启动前端：

```sh
cd dashboard
npm install
npm run dev
```

浏览器打开 `http://localhost:5274`。Vite 开发服务器将 `/api` 请求代理到 `http://localhost:8080`，并删除 `/api` 前缀；例如浏览器请求 `/api/auth/me`，后端收到 `/auth/me`。直接开发时登录回调地址必须对应可访问的后端 `/signin-oidc`，Cookie 所属 hostname 必须与前端一致。

`src/configs/app.yaml` 中示例数据库连接是 `localhost:55433`，而开发 Compose 发布的是 `55434`。源码运行时请通过 `NEXUSAUTHORIZATION_DATABASE_URL` 显式指定实际连接串。

### 2.4 用当前代码构建镜像并覆盖 Compose（需要 Token 等新功能时）

`docker-compose-dev.yml` 和 `docker-compose.yml` 默认引用的 `18790997531/nexusauthorization:1.0.2` 与 `18790997531/nexusauthorization-dashboard:1.0.2` 是发布镜像，**不包含当前仓库中的 API Token 等改动**（见 [API Token 使用与服务接入](api-token-integration.md)）。要在 Compose 中运行当前工作区代码，先本地构建镜像，再用 override 覆盖两个服务的 `image`。

后端镜像从源码构建（在仓库根目录执行；仅构建阶段需要 Go 1.25）：

```sh
docker build --platform linux/amd64 -t nexusauthorization:workspace .
```

前端镜像是只复制静态产物的运行时镜像，必须先产出 `dist/` 再构建：

```sh
cd dashboard
npm install
npm run build
cd ..
docker build --platform linux/amd64 -t nexusauthorization-dashboard:workspace dashboard
```

在仓库根目录新建临时 override 文件 `docker-compose.workspace.yml`，把两个服务指向本地标签。此文件仅用于本地部署，不要提交；如果保存到仓库外，请把下方命令的第二个 `-f` 参数替换为文件的实际路径：

```yaml
services:
  permission-api:
    image: nexusauthorization:workspace
  dashboard:
    image: nexusauthorization-dashboard:workspace
```

开发版和生产版都在命令中同时指定原文件和 override：

```sh
# 开发版（含本地 PostgreSQL）
docker compose --env-file .env.dev -f docker-compose-dev.yml -f docker-compose.workspace.yml up -d

# 生产版（连接外部 PostgreSQL；不执行迁移）
docker compose -f docker-compose.yml -f docker-compose.workspace.yml up -d
```

注意事项：

- 使用本地构建标签时不要执行 `docker compose ... pull`，否则 Compose 会尝试从镜像仓库拉取 `:workspace` 这类本地标签并失败。
- 本小节不修改仓库中实际的 Compose 文件，只在命令行叠加 override。
- 后端镜像会把 `src/configs/app.production.yaml` 复制为容器内 `configs/app.yaml`，运行时仍通过 `NEXUSAUTHORIZATION_` 环境变量覆盖关键配置。

## 3. 数据库迁移

迁移文件：

| 文件 | 用途 | 适用场景 |
| --- | --- | --- |
| `migrations/001_init.sql` | 完整初始化脚本，创建 11 张基础业务表 | 空数据库 |
| `migrations/002_application_url.sql` | 为 `applications` 增加可选 `url` 列 | 已有旧模型、但缺少应用地址列 |
| `migrations/003_api_tokens.sql` | 增加 API Token 相关 3 张表，可重复执行 | 已有数据库启用 API Token |

应用迁移：

```sh
# 空库：一次性初始化
psql "$NEXUSAUTHORIZATION_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/001_init.sql

# 已有当前应用模型、缺少应用地址列
psql "$NEXUSAUTHORIZATION_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/002_application_url.sql

# 启用 API Token
psql "$NEXUSAUTHORIZATION_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/003_api_tokens.sql
```

注意事项：

- 迁移由部署人员执行，后端启动**不会**自动修改已有数据库。
- 新数据库同时需要 `001`、`002`、`003`；已有数据库只执行缺失的增量迁移。
- **不要**对已有业务表的数据库执行 `001_init.sql`，也不要执行不存在的 `002_applications.sql` 或旧增量脚本。
- 执行前先按既有备份流程备份数据库。开发 Compose 的初始化 SQL 只在空数据卷首次启动时执行，已有卷需手动升级。
- 本地重建会清空现有数据。旧数据只能从备份恢复，或作为单独的数据导入任务审查后处理。

## 4. 配置项与安全要求

### 4.1 配置来源

- 后端运行参数来自 `src/configs/app.yaml` 和 `NEXUSAUTHORIZATION_` 前缀的环境变量；环境变量覆盖 YAML。
- 管理端 Cookie 名称固定为 `nexusauthorization_session`，Secure 标志按登录回调地址自动设置（HTTPS 为 true，HTTP 本地开发为 false），HttpOnly、SameSite=Lax、Path=/ 由代码管理，不通过环境变量或 YAML 配置。
- 修改 session secret 会使现有会话失效；多实例必须一致。
- 配置是启动读取。修改内部密钥要更新两端并重启相应服务。

### 4.2 后端 YAML 关键项（`src/configs/app.yaml`）

```yaml
http:
  addr: ":8080"
database:
  url: "postgres://postgres:postgres@localhost:55433/permission_center?sslmode=disable"
service_resource:
  source: "nexusauth"          # local 或 nexusauth
  nexusauth_base_url: "http://localhost:5100"
  api_key: ""
  timeout: "5s"
authorization:
  service_credential: ""       # 业务服务调用 /v1/authorize 的独立 Bearer 凭证
api_tokens:
  service_resource: "nexusauthorization"
  admin_subjects: []           # OIDC 模式下必填，否则无人可管理 Token
  internal_callers: []         # 内部校验调用方及其独立密钥
oidc:
  enabled: false
  authority: "http://localhost:5100"
  client_id: "permission.center"
  redirect_uri: "http://localhost:8080/signin-oidc"
  post_logout_redirect_uri: "http://localhost:5274/"
  session_secret: "replace-with-at-least-32-random-characters"
```

### 4.3 环境变量

变量前缀均为 `NEXUSAUTHORIZATION_`（下表省略前缀，数据库镜像变量除外）：

| 变量 | 用途 |
| --- | --- |
| `DATABASE_URL` | 外部 PostgreSQL 连接串；密码含特殊字符时须 URL 编码，也可用 pgx 支持的 keyword DSN。 |
| `OIDC_ENABLED` | 是否启用 SSO；生产 Compose 固定为 true。 |
| `OIDC_AUTHORITY` | 身份提供商公开地址。 |
| `OIDC_BACKCHANNEL_AUTHORITY` | 可选：后端访问同一身份提供商的内部地址。 |
| `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` | 身份平台注册的客户端及其密钥。 |
| `OIDC_REDIRECT_URI` | 完整登录回调地址，例如 `https://permissions.example.com/signin-oidc`。 |
| `OIDC_POST_LOGOUT_REDIRECT_URI` | 完整退出后地址，例如 `https://permissions.example.com/`。 |
| `OIDC_SCOPES` / `OIDC_AUDIENCE` | 按身份平台注册值设置；scopes 必须包含 `openid`，默认 audience 沿用 `permission.center.api`。 |
| `OIDC_SESSION_SECRET` | 稳定的会话加密密钥，至少 32 字符；多实例须一致。 |
| `SERVICE_RESOURCE_SOURCE` | `local` 或 `nexusauth`；开发默认 local，生产默认 nexusauth。 |
| `SERVICE_RESOURCE_NEXUSAUTH_BASE_URL` | nexusauth 模式下的远程目录服务地址。 |
| `SERVICE_RESOURCE_API_KEY` | nexusauth 模式下的服务目录访问凭证。 |
| `AUTHORIZATION_SERVICE_CREDENTIAL` | 业务服务调用 `/v1/authorize` 使用的独立 Bearer 凭证。 |
| `API_TOKEN_SERVICE_RESOURCE` | API Token 关联的中心服务键，默认 `nexusauthorization`。 |
| `API_TOKEN_ADMIN_SUBJECTS` | 逗号分隔的 NexusAuth 用户 subject，只有这些管理员能管理 Token；OIDC 模式下默认空。 |
| `API_TOKEN_INTERNAL_KEY` | 为 `nexusauth` 调用者提供的独立内部密钥快捷配置（至少 32 字节随机值）。 |

开发 Compose 的数据库镜像变量为 `POSTGRES_DB`、`POSTGRES_USER`、`POSTGRES_PASSWORD`、`POSTGRES_PORT`，端口映射变量为 `DASHBOARD_BIND_ADDR`、`DASHBOARD_PORT`，均不带应用前缀。

### 4.4 安全要求

- 生产必须开启 OIDC，并显式配置 `API_TOKEN_ADMIN_SUBJECTS`；否则无人可管理 Token。`authorization.service_credential` 和内部密钥必须使用独立随机值，不得复用，不得提交到 Git。
- 生成随机密钥：

  ```sh
  openssl rand -hex 32
  ```

- 页面与登录回调必须使用同一个 hostname，使浏览器能发送登录 Cookie；不同子域不能直接共享当前 host-only Cookie。
- 生产地址要求 HTTPS。内部密钥、业务 Token 不得写入 URL、日志或追踪标签。
- 管理路由由服务器配置的认证中间件保护。目前没有在上述管理路由逐操作套用应用 RBAC 管理权限矩阵，登录校验不等于已实现“权限中心管理员分权”。

## 5. 登录与应用管理

### 5.1 登录流程

管理端支持配置启用的 OIDC/SSO 登录。认证相关后端路由：

| 能力 | 方法与路径 |
| --- | --- |
| 登录配置 | `GET /auth/config` |
| 登录 | `GET /auth/login` |
| 当前身份 | `GET /auth/me` |
| 退出登录 | `POST /auth/logout` |
| OIDC 回调 | `GET /signin-oidc` |

本地开发关闭 OIDC 时遵循项目已有免登录模式；生产部署应开启 OIDC。

### 5.2 应用管理

登录后进入 `/select-application`，可以搜索、创建、编辑、启停应用并进入工作台。

应用管理 API：

| 能力 | 方法与路径 |
| --- | --- |
| 列表 | `GET /v1/applications` |
| 创建 | `POST /v1/applications` |
| 编辑 | `PUT /v1/applications/{key}` |
| 查询关联服务 | `GET /v1/applications/{key}/service-resources` |
| 替换关联服务 | `PUT /v1/applications/{key}/service-resources` |

创建应用请求示例：

```http
POST /v1/applications
Cookie: nexusauthorization_session=<浏览器登录会话 Cookie>
Content-Type: application/json

{
  "key": "business.portal",
  "display_name": "业务管理平台",
  "description": "对外业务门户",
  "url": "https://portal.example.com",
  "enabled": true
}
```

管理路由的认证有两条互不相同的路径：

- 浏览器登录：OIDC 登录成功后由后端下发 `nexusauthorization_session` Cookie，管理端页面在浏览器中使用该会话 Cookie 访问管理 API。
- 受信 OIDC access token：受信调用方可以用 `Authorization: Bearer <OIDC access token>` 访问管理 API，这是身份平台签发的令牌，不是 `/v1/authorize` 的服务凭证。

`/v1/authorize` 的专用服务凭证（`authorization.service_credential`）**不能**用于管理路由；反过来，管理端会话 Cookie 或 OIDC access token 也不是 `/v1/authorize` 的凭证。以上示例只演示浏览器会话这种形式，实际请求由前端自动携带 Cookie。

字段说明：

- `key` 为应用唯一标识，创建后不通过编辑页面修改。
- `url` 是可选的 HTTP/HTTPS Web 入口地址，只接受绝对地址且不含内嵌凭证；启用且地址有效的应用在列表中显示“打开应用”。地址可编辑或清空，不影响角色、发布和鉴权逻辑。
- 应用停用后仍可在选择页编辑和重新启用，但不能进入工作台，后端 RBAC 操作也会按启用应用查找。
- 应用选择页和后端都没有删除应用的入口。

关联服务使用应用 key，`PUT` 会原子替换整个集合：

```http
PUT /v1/applications/business.portal/service-resources
Content-Type: application/json

{
  "service_resource_keys": ["order.service", "user.service"]
}
```

## 6. 服务资源、API 与 Swagger

### 6.1 服务资源

在“资源管理 → 服务资源”页面查看名称、服务标识、Audience、启停状态，以及当前应用是否已关联。搜索框支持按名称、标识或 Audience 匹配，可切换“全部服务/已关联服务”。

服务资源 API：

| 能力 | 方法与路径 |
| --- | --- |
| 列表 | `GET /v1/service-resources` |
| 创建 | `POST /v1/service-resources` |
| 查询 | `GET /v1/service-resources/{key}` |
| 编辑 | `PUT /v1/service-resources/{key}` |
| 删除 | `DELETE /v1/service-resources/{key}` |

写操作受目录来源限制：`local` 模式允许新增、编辑和确认删除；`nexusauth` 模式隐藏写入口，后端也拒绝。已关联当前应用的资源需先取消关联；后端还会阻止删除被任意应用或 API 端点引用的资源，以及删除仍被角色授予 API 的服务。

### 6.2 API 接口

在“资源管理 → API 接口”页面搜索、筛选、手动维护接口或导入 Swagger。列表每页 25 条，可按服务和 HTTP 方法筛选。

API 字段：接口标识、摘要、目标服务、HTTP 方法、路径、模块、启用状态。接口编辑不能更换归属服务；后端按接口 ID 和服务归属限定更新。删除接口前需要确认。

API 管理位于应用范围接口下，所有请求都要带 `app_key`；创建、删除和 Swagger 导入另需 `service_resource`：

| 能力 | 方法与路径 | 必要范围参数 |
| --- | --- | --- |
| 列表 | `GET /v1/rbac-v1/endpoints` | `app_key` |
| 保存 | `POST /v1/rbac-v1/endpoints` | `app_key`、`service_resource` |
| 删除 | `DELETE /v1/rbac-v1/endpoints/{id}` | `app_key`、`service_resource` |
| Swagger 导入 | `POST /v1/rbac-v1/endpoints/import-swagger` | `app_key`、`service_resource` |

手工新增接口示例：

```http
POST /v1/rbac-v1/endpoints?app_key=business.portal&service_resource=order.service
Content-Type: application/json

{
  "endpoint_key": "orders.get",
  "method": "GET",
  "path_template": "/api/orders/{id}",
  "module": "订单",
  "summary": "查询订单详情",
  "enabled": true
}
```

归属服务由查询参数 `service_resource` 决定，不在请求体里传；`POST` 创建新接口返回 201，`POST` 更新已有接口返回 200。

### 6.3 Swagger / OpenAPI 导入

导入时先选择当前应用已关联且启用的目标服务，再填写 HTTP(S) Swagger/OpenAPI 地址：

```http
POST /v1/rbac-v1/endpoints/import-swagger?app_key=business.portal&service_resource=order.service
Content-Type: application/json

{
  "swagger_url": "https://order.example.com/openapi.json"
}
```

说明：

- 支持 Swagger 2.0、OpenAPI 3.x，以及 JSON/YAML 文档；后端提取 HTTP 方法、路径、模块与摘要。
- 同一服务内按 `method + path_template` 跳过已存在接口；返回总数、新增数、跳过数。
- 导入来源记录为 `swagger`，手工新增为 `manual`。导入**不会**自动授予任何角色权限。
- 导入目前逐条保存，不是整批原子事务；中途失败时已写入的接口可能保留，重试会通过跳过逻辑继续。
- API 是共享目录。某个应用编辑服务的 API 元数据会修改这条共享接口定义，但各应用仍需分别配置角色授权并分别发布。

## 7. 菜单与按钮

菜单和按钮直接属于业务应用，使用同一张 `menus` 表，通过 `menu_type` 区分 `menu`、`button`（`directory` 用于兼容已有目录节点）。不需要服务关联或 OAuth 客户端。

在“资源管理 → 菜单与按钮”页面：

- 菜单树按父节点和排序展示，支持搜索名称/权限标识、展开全部、收起全部。
- 可新增顶级菜单、子菜单，也可直接在某个菜单下“添加按钮”。
- 菜单字段：名称、权限标识、上级、路由、组件、图标、排序、状态。路由可空，填写时要求以 `/` 开头。
- 按钮字段：名称、权限标识、所属菜单、排序、状态。按钮不填写路由、组件、图标，保存时这些字段置空。

约束：

- 前端禁止选择自己或后代作上级，禁止将有子项的节点转为按钮，按钮必须选择菜单父节点。
- 后端校验父节点属于同一应用并检测循环层级；数据库复合外键保证父子同应用。
- 有子项的节点不能直接删除；删除叶子节点需二次确认。

菜单 API：

| 能力 | 方法与路径 |
| --- | --- |
| 列表 | `GET /v1/rbac-v1/menus?app_key=business.portal` |
| 保存 | `POST /v1/rbac-v1/menus?app_key=business.portal` |
| 删除 | `DELETE /v1/rbac-v1/menus/{id}?app_key=business.portal` |
| 主体菜单与按钮查询 | `GET /v1/rbac-v1/subject-menus?app_key=business.portal&subject=user-001&domain=global` |

保存接口每次提交一个节点。新建菜单：

```http
POST /v1/rbac-v1/menus?app_key=business.portal
Content-Type: application/json

{
  "code": "orders",
  "name": "订单管理",
  "menu_type": "menu",
  "parent_id": null,
  "route": "/orders",
  "component": "",
  "icon": "",
  "sort_order": 10,
  "enabled": true
}
```

在该菜单下新建按钮时，把 `parent_id` 设为菜单的 UUID（更新已有节点时在请求体中带上 `id`）：

```json
{
  "code": "orders:create",
  "name": "创建订单",
  "menu_type": "button",
  "parent_id": "<orders 菜单的 UUID>",
  "route": "",
  "component": "",
  "icon": "",
  "sort_order": 20,
  "enabled": true
}
```

主体菜单查询从同一活动快照返回被授予的菜单/按钮，并补齐启用的祖先节点；父节点停用、缺失或存在循环时对应分支不返回。前端可据此控制操作入口，后端 API 仍须独立鉴权。

## 8. 角色

在“RBAC 授权 → 角色管理”页面新建、编辑、停用及删除应用内角色。角色编码在同一应用内唯一。

编辑角色时可以：

- 同时选择多个已关联服务的 API。
- 选择应用内的菜单和按钮节点，界面显示资源类型。

角色 API：

| 能力 | 方法与路径 |
| --- | --- |
| 列表 | `GET /v1/rbac-v1/roles?app_key=business.portal` |
| 保存 | `POST /v1/rbac-v1/roles?app_key=business.portal` |
| 删除 | `DELETE /v1/rbac-v1/roles/{id}?app_key=business.portal` |

保存角色示例（`endpoint_ids` 与 `menu_ids` 是目录中的真实 UUID）：

```json
{
  "code": "order.viewer",
  "name": "订单查看者",
  "description": "只读访问订单接口",
  "enabled": true,
  "endpoint_ids": ["<GET /api/orders/{id} 的 UUID>"],
  "menu_ids": ["<订单管理菜单的 UUID>"]
}
```

规则与说明：

- 保存 API 和菜单授权时，数据库验证 API 对应服务已关联当前应用，菜单与角色属于同一应用。
- 角色启停、授权及删除对运行时的影响遵循发布快照机制，需重新发布才生效。
- API 授权与菜单/按钮授权是两组独立绑定，目前没有“勾选按钮自动勾选 API”的联动。
- 角色不跨应用。要访问其他应用，需在该应用中配置角色并发布。

## 9. subject/domain 授权

在“RBAC 授权 → 用户授权”页面，根据 `subject + domain` 分组查看绑定关系，为该组合分配一个或多个应用角色。这是角色绑定管理，不是账号 CRUD；身份账号由 SSO/业务身份来源提供。

授权 API：

| 能力 | 方法与路径 |
| --- | --- |
| 列出应用内全部主体/域绑定 | `GET /v1/rbac-v1/subject-roles?app_key=business.portal` |
| 替换某个主体/域的角色集合 | `PUT /v1/rbac-v1/subject-roles?app_key=business.portal` |

替换请求体：

```json
{
  "subject": "user-001",
  "domain": "global",
  "role_ids": ["<order.viewer 的 UUID>", "<order.admin 的 UUID>"]
}
```

规则与说明：

- 保存操作**替换**该主体/域在当前应用中的整组角色，不是追加单条授权。
- 前端只提供启用角色作为候选；后端限制角色必须属于当前应用。
- 当前默认域是 `global`，界面可填写其他域字符串。`domain` 是授权上下文字符串，目前没有独立域目录或租户管理表。
- 多角色取并集：只要 subject 在 domain 下的任一启用角色绑定了目标 API，即允许访问。

## 10. 发布与模拟

### 10.1 配置流程（先首次发布，再模拟）

应用配置按下面的顺序执行。**权限模拟读取的是已发布快照，未发布无法模拟**，所以首次配置必须先发布一次才能验证结果：

1. 创建或选择业务应用（`app_key`）。
2. 关联一个或多个启用的服务资源；关联本身不授予权限。
3. 在已关联服务范围内登记或导入 API。
4. 配置菜单和按钮。
5. 创建角色，勾选该应用已关联服务的 API 和菜单/按钮。
6. 为 `subject + domain` 绑定角色。
7. **首次发布**该应用，生成活动快照。
8. 用 `app_key + service_resource + subject + domain + method + path` 做权限模拟。
9. 业务服务调用 `POST /v1/authorize` 进行运行时鉴权。
10. 之后每次改动 API、角色、菜单或绑定，都要重新发布并再次模拟验证。

只有活动版本参与模拟和运行时鉴权；没有活动版本时模拟返回未找到错误。

### 10.2 发布

在“发布与验证 → 发布版本”页面填写说明并发布当前应用配置。`PublishApplication` / `buildSnapshot` 的实际行为：

1. 按 `app_key` 查找**启用**应用（应用不存在或已停用则无法发布）。
2. 读取配置目录的 active service keys：`local` 模式取本地启用的服务资源，`nexusauth` 模式取远程目录返回的可用服务资源；远程目录不可用时该步骤失败，发布中止。
3. 按 active service keys 过滤应用关联和接口，构建 JSONB 快照（`applications`、`service_resources`、`endpoints`、`roles`、`subject_roles`、`menus`）。
4. 在事务内锁定应用记录，应用内版本递增并生成 SHA-256 校验和；新版本状态为 `published`，旧活动版本改为 `superseded`。

发布 API：

| 能力 | 方法与路径 |
| --- | --- |
| 发布 | `POST /v1/rbac-v1/releases?app_key=business.portal` |
| 历史 | `GET /v1/rbac-v1/releases?app_key=business.portal` |

```http
POST /v1/rbac-v1/releases?app_key=business.portal
Content-Type: application/json

{
  "description": "订单查看者权限上线"
}
```

发布**不做**完整的“发布预检”。跨应用引用、重复编码、引用不存在/停用对象等，主要靠**保存时**的数据库约束（唯一键、复合外键、触发器）和仓储校验拦截，不要把它们当作发布步骤才检查。发布阶段实际发生的是：

- 停用的角色和接口仍会写入快照（带 `enabled=false`），由运行时代码拒绝：接口命中但停用时返回 `endpoint_disabled`；停用角色不授予其端点。
- 远程不可用或已停用的服务资源不在 active service keys 中，其服务关联和接口被过滤掉，不会进入快照，等于在下一次发布时自动撤销这些服务的授权。
- 发布失败主要出现在应用不存在/停用、读取 active service keys 失败（目录不可用）、快照构建或写库失败。

因为快照允许停用对象存在，撤销授权、删除接口或解除服务关联后**仍应重新发布**，才能在快照中移除对应条目。

### 10.3 权限模拟

在“发布与验证 → 权限模拟”页面输入主体、域、服务、方法和路径，检查**已发布**权限。模拟调用同一授权逻辑，读取当前活动发布版本，不是未发布草稿；没有活动版本时返回未找到错误。

```http
POST /v1/rbac-v1/simulate?app_key=business.portal
Content-Type: application/json

{
  "service_resource": "order.service",
  "subject": "user-001",
  "domain": "global",
  "method": "GET",
  "path": "/api/orders/1001"
}
```

当前页面没有版本差异对比、恢复历史版本、发布审批或草稿模拟入口。

### 10.4 管理员端到端验收步骤（同一 app/service/subject）

下面用一组贯穿示例验收完整链路。示例值：应用 `business.portal`（启用）、服务 `order.service`（已关联、启用）、接口 `GET /api/orders/{id}`、主体 `user-001`、域 `global`。配置与模拟在管理端页面完成，运行时请求通过业务服务或 HTTP 客户端验证。

1. 应用：确认 `business.portal` 存在且启用。
2. 关联服务：在“服务资源”确认 `order.service` 存在且启用，并已“关联”到 `business.portal`。
3. API：在“API 接口”把目标服务选为 `order.service`，新增或导入 `GET /api/orders/{id}`，确认启用。
4. 菜单/按钮：在“菜单与按钮”新建菜单 `orders`（路由 `/orders`），再在其下新建按钮 `orders:create`。
5. 角色：在“角色管理”新建 `order.viewer`，勾选接口 `GET /api/orders/{id}` 与菜单 `orders`，保存并确认启用。
6. 授权：在“用户授权”为 `subject=user-001`、`domain=global` 绑定 `order.viewer`。
7. 首次发布：在“发布版本”填写说明并发布，确认生成新版本且状态为 `published`。
8. 模拟允许：在“权限模拟”使用 `service_resource=order.service`、`subject=user-001`、`domain=global`、`method=GET`、`path=/api/orders/1001`，应得到允许。
9. 模拟拒绝：把 `path` 改成 `/api/orders`（不匹配接口），应得到拒绝，`reason=endpoint_not_found`。
10. 运行时：业务服务用 `POST /v1/authorize` 提交同样六个字段（`appKey`、`serviceResource`、`subject`、`domain`、`method`、`path`），应得到与模拟一致的允许结果；改用未绑定角色的 subject 应得到拒绝。
11. 变更验证：取消角色对接口的授权并**重新发布**，再用 `user-001` 模拟和调用，应变为拒绝。

验收标准：同一次配置下，页面模拟结果与 `POST /v1/authorize` 的运行时结果一致；未发布的草稿变更不会影响这两者。

## 11. 运行时 RBAC 调用

业务服务在验证用户身份后，用可信配置的应用标识和服务标识请求权限中心。鉴权输入始终包含：

```text
appKey + serviceResource + subject + domain + method + path -> allow / deny
```

| 字段 | 作用 |
| --- | --- |
| `appKey` | 确定业务应用及其活动发布版本。 |
| `serviceResource` | 确定请求所属的服务命名空间，同一路径在不同服务中分别判断。 |
| `subject` / `domain` | 确定主体拥有的应用角色。 |
| `method` / `path` | 匹配目标 API，再检查启用角色是否授予该接口。 |

运行时接口是 `POST /v1/authorize`，使用服务端 Bearer 凭证（`authorization.service_credential` / `NEXUSAUTHORIZATION_AUTHORIZATION_SERVICE_CREDENTIAL`），不要用管理端浏览器登录会话或用户可控参数代替服务凭证。

```http
POST /v1/authorize
Authorization: Bearer <AUTHORIZATION_SERVICE_CREDENTIAL>
Content-Type: application/json

{
  "appKey": "business.portal",
  "serviceResource": "order.service",
  "subject": "user-001",
  "domain": "global",
  "method": "GET",
  "path": "/api/orders/1001"
}
```

允许时返回：

```json
{
  "allowed": true,
  "effect": "allow",
  "reason": "allowed",
  "release_version": 1,
  "matched_roles": ["order.viewer"]
}
```

拒绝时 `allowed` 为 `false`，`reason` 取值包括：

| reason | 含义 |
| --- | --- |
| `endpoint_not_found` | 未匹配到接口。 |
| `endpoint_disabled` | 匹配到的接口已停用。 |
| `endpoint_not_granted` | 主体角色未授予该接口。 |
| `ambiguous_endpoint` | 路径匹配存在歧义。 |
| `allowed` | 允许。 |

匹配规则：`/orders/{id}` 的参数只匹配一个非空路径段；固定路径优先于参数路径；不提供通配符任意深度匹配。未匹配接口、接口停用、未授权或路径匹配歧义均拒绝。

HTTP 层行为：`/v1/authorize` 需要服务端 Bearer 凭证；请求缺少必要字段、凭证不符或应用/发布不存在时返回 HTTP 错误，不能一概当作正常的 deny 响应。业务服务应把 HTTP 错误、超时和畸形响应视为拒绝（fail closed）。

RBAC 路径与 API Token 路径的对外状态码不同，不要统一成一种：使用 `sdk/rbac` 中间件时，身份无效返回 401，鉴权错误或拒绝返回 403（SDK 的既有行为）；API Token 接入服务在中心接入异常时对外返回 503（见第 12.3 节）。

## 12. API Token

API Token 是按应用管理的机器凭证。一个 Token 属于一个 `app_key`，可选择该应用关联的多个服务及其 API；要访问其他应用需另建 Token。Token 使用随机不透明凭证，不使用 OAuth 协议。

### 12.1 前置配置

```yaml
api_tokens:
  service_resource: "nexusauthorization"
  admin_subjects:
    - "<NexusAuth 管理员的 subject>"
  internal_callers:
    - service_resource: "nexusauth"
      key: "<独立随机内部密钥，至少 32 字节>"
```

对应环境变量：

```text
NEXUSAUTHORIZATION_API_TOKEN_SERVICE_RESOURCE=nexusauthorization
NEXUSAUTHORIZATION_API_TOKEN_ADMIN_SUBJECTS=<subject1>,<subject2>
NEXUSAUTHORIZATION_API_TOKEN_INTERNAL_KEY=<nexusauth 独立内部密钥>
```

`API_TOKEN_INTERNAL_KEY` 是为 `nexusauth` 调用者提供的快捷配置，等价于把内部密钥写入一个 `service_resource: nexusauth` 的 `internal_callers` 项；其他调用服务通过 YAML `internal_callers` 分别配置，每个接入服务使用各自密钥，不得共享，相同服务只配置一项。OIDC 模式下管理员名单默认为空，必须明确配置。

### 12.2 创建、调用、轮换与撤销

管理界面在“RBAC 授权 → API Token”，对应后端 `/v1/api-tokens`。所有接口必填 `?app_key=<业务应用键>`，响应采用现有 `{success, errorCode, errorMessage, result}` 封装。

| 能力 | 方法与路径 |
| --- | --- |
| 列表 | `GET /v1/api-tokens?app_key=business.portal`（支持 search、status、page、page_size） |
| 创建 | `POST /v1/api-tokens?app_key=business.portal`（201，`result` 含 `token` 和一次性 `raw_token`） |
| 详情 | `GET /v1/api-tokens/{id}?app_key=business.portal` |
| 更新 | `PUT /v1/api-tokens/{id}?app_key=business.portal`（名称、有效期、权限；主体保持不变） |
| 启停 | `POST /v1/api-tokens/{id}/status?app_key=business.portal` |
| 撤销 | `POST /v1/api-tokens/{id}/revoke?app_key=business.portal`（不可恢复） |
| 轮换 | `POST /v1/api-tokens/{id}/rotate?app_key=business.portal`（201，一次性新明文） |

创建请求：

```http
POST /v1/api-tokens?app_key=business.portal
Content-Type: application/json

{
  "name": "目录同步",
  "owner_type": "service",
  "owner_id": "directory-sync",
  "expires_at": null,
  "permissions": [
    {
      "endpoint_id": "<GET /api/orders/{id} 的 UUID>",
      "service_resource": "order.service",
      "action": "read",
      "tenant_id": "tenant-a",
      "project_id": ""
    }
  ]
}
```

创建成功返回（`raw_token` 只在此次返回）：

```json
{
  "success": true,
  "errorCode": null,
  "errorMessage": null,
  "result": {
    "token": { "id": "<UUID>", "token_prefix": "nxa_...", "status": "active", "owner_type": "service", "owner_id": "directory-sync" },
    "raw_token": "nxa_<uuid>_<secret>"
  }
}
```

要点：

- Token 格式为 `nxa_<uuid>_<32 字节随机 secret 的 base64url>`；数据库只保存 SHA-256 哈希和前缀，完整明文只在创建与轮换成功响应返回一次，列表、详情、错误与审计不会返回明文或哈希。
- `GET / HEAD` 对应 `read`，`POST / PUT / PATCH / DELETE` 对应 `write`；本版不能给 POST 人工改成 read，也不能将 GET 标为 write，其他 HTTP 方法不能授权。
- 每个端点单独选择，可一次选择多个服务的多个 API。空 `tenant_id` 或 `project_id` 表示该维度不限；非空时必须与目标请求的真实范围完全一致。
- 服务必须已关联当前应用，API 必须启用。状态为 `active / disabled / revoked`，过期由 `expires_at` 计算，前端显示 `expired`；永久 Token 的 `expires_at` 为 NULL。
- 停用可恢复，撤销不可恢复；过期 Token 可以编辑延长有效期。轮换只接受启用且未过期的 Token，`{"revoke_old": true}` 会在同一事务内撤销旧记录。
- 应用停用、服务关联移除、端点停用都会影响后续请求，无需发布 RBAC 版本。

启停请求体：

```json
{ "status": "disabled" }
```

### 12.3 内部授权调用

业务服务收到 `Authorization: Bearer nxa_...` 后，在处理业务前调用内部接口：

```http
POST https://<权限中心私有地址>/internal/v1/api-tokens/authorize
X-Internal-Service-Key: <内部服务密钥>
Content-Type: application/json

{
  "token": "<业务请求中的 nxa_...>",
  "app_key": "business.portal",
  "service_resource": "order.service",
  "method": "GET",
  "path": "/api/orders/1001",
  "tenant_id": "tenant-a",
  "project_id": ""
}
```

内部接口不走 OIDC、不走业务 Token 校验，也不查询 NexusAuth 服务目录，避免递归依赖；内部密钥只允许检查配置中绑定的 `service_resource`。请求路径必须是 API 自身实际路径，包含前导斜杠，不含 query/fragment，不要带 Dashboard 的 `/api` 代理前缀；方法必须大写。

正常完成评估时，无论允许还是拒绝，内部接口都返回 HTTP 200 的原始 Decision JSON（不使用管理接口封装）：

```json
{
  "active": true,
  "allowed": true,
  "reason": "allowed",
  "token_id": "<UUID>",
  "app_key": "business.portal",
  "owner_type": "service",
  "owner_id": "directory-sync"
}
```

接收方处理规则。注意区分两类结果：只有内部接口返回 HTTP 200 且带业务语义的 `active` / `allowed` 时，才映射成业务 Token 的 401/403；内部接口自身的 400/401/403/503 都属于**中心接入异常**，接入服务对外一律返回 503，不能把中心返回的 401/403 直接透传成业务 Token 的 401/403。

| 内部响应 | 接收方对外行为 |
| --- | --- |
| 200，`active=false` | 业务 Token 无效，对外返回 401：格式错误、凭证错误、停用、撤销、过期等。 |
| 200，`active=true`、`allowed=false` | 业务 Token 有效但无权限，对外返回 403：没有目标 API 或范围权限。 |
| 200，两项均为 true | 继续执行，并保留返回主体用于业务审计。 |
| 400（中心接入异常） | 接入服务构造的目标参数错误，对外返回 503，不作为业务 Token 的 401/403。 |
| 401 / 403（中心接入异常） | 内部服务密钥错误或调用服务范围错误，对外返回 503。 |
| 503、超时、异常 JSON（中心接入异常） | 对外返回 503，拒绝放行。 |

```mermaid
sequenceDiagram
    participant C as 业务调用方
    participant S as 业务服务
    participant N as NexusAuthorization
    C->>S: Authorization: Bearer nxa_...
    S->>N: POST /internal/v1/api-tokens/authorize
    N-->>S: 200 active/allowed
    alt active=false
        S-->>C: 401
    else active=true 且 allowed=false
        S-->>C: 403
    else 两项均为 true
        S-->>C: 执行业务并按范围过滤数据
    else 中心接入异常（400/401/403/503、超时或畸形响应）
        S-->>C: 503
    end
```

第一版不缓存认证和授权结果，每次检查 Token 哈希及实时状态；撤销与权限修改影响事务提交后开始的请求。默认拒绝，中心不可用必须 fail closed，不能降级成免认证访问。管理员应在接口授权通过之后，仍在业务查询中按租户/项目过滤数据。

### 12.4 使用 API Token 调用 NexusAuthorization 自身开放 API

开启 OIDC 时，普通管理 API 可使用原有登录身份，支持范围内的开放 API 也接受 Bearer API Token。当前接入范围：`/v1/rbac-v1/*` 的应用权限 API、`/v1/applications/{key}` 的当前应用操作、应用列表及服务列表的只读接口。共享 API 目录的写入/导入/删除、全局服务管理、旧版全局 API 和 Token 管理不能用应用 Token 操作；`/v1/authorize` 继续使用其专用服务凭证。

调用前需把 NexusAuthorization 服务关联到目标应用，并将对应方法/路径登记为端点。

## 13. Go SDK 接入

仓库提供两个 Go SDK，模块为 `github.com/luck/permission-center-go/sdk`。

### 13.1 RBAC 客户端 `sdk/rbac`

```go
import "github.com/luck/permission-center-go/sdk/rbac"

client := rbac.NewClient("https://permission-center.example", os.Getenv("NEXUSAUTHORIZATION_CREDENTIAL"))

middleware := client.Require(
	"business.portal", // AppKey，固定可信配置
	"order.service",   // ServiceResource，固定可信配置
	func(r *http.Request) (rbac.Identity, bool) {
		// 从可信认证中间件的上下文解析身份
		identity, ok := auth.IdentityFromContext(r.Context())
		return rbac.Identity{Subject: identity.Subject, Domain: identity.Domain}, ok
	},
	http.HandlerFunc(listOrders),
)
http.Handle("/api/orders", middleware)
```

直接检查：

```go
decision, err := client.Check(ctx, rbac.Request{
	AppKey:          "business.portal",
	ServiceResource: "order.service",
	Subject:         "user-001",
	Domain:          "global",
	Method:          http.MethodGet,
	Path:            "/api/orders/1001",
})
```

说明：

- `Request.AppKey` 和 `Request.ServiceResource` 必填，必须来自配置或服务端可信解析，不能读取请求 query、header 或其他用户可控输入。
- `Client.Require(appKey, serviceResource, resolve, next)` 是 fail-closed 中间件：缺少应用/服务配置、身份未认证、授权服务错误、畸形决策和拒绝决策都会阻止下游 handler 执行；身份无效返回 401，鉴权错误或拒绝返回 403。
- SDK 默认 3 秒超时，`Check` 要求 HTTP 200，且允许决策必须满足 `effect=allow`、`reason=allowed`、`release_version>0`，否则视为错误。

### 13.2 API Token 客户端 `sdk/apitoken`

```go
import "github.com/luck/permission-center-go/sdk/apitoken"

client, err := apitoken.New(apitoken.Options{
	BaseURL:    "https://nexus-authorization.internal",
	ServiceKey: os.Getenv("NEXUSAUTH_INTERNAL_SERVICE_KEY"), // 启动加载，与业务 Token 不同
})
if err != nil {
	// 启动失败
}

decision, err := client.Authorize(ctx, incomingBearer, apitoken.Target{
	AppKey:          "business.portal",
	ServiceResource: "order.service",
	Method:          request.Method,
	Path:            request.URL.EscapedPath(),
	TenantID:        resolvedTenantID, // 服务端可信解析，不用请求体
	ProjectID:       resolvedProjectID,
})
// err != nil: 对外 503；!decision.Active: 对外 401；!decision.Allowed: 对外 403。
// 两项均为 true：执行业务，并按已验证范围过滤数据。
```

`apitoken.New` 会在启动阶段校验配置并返回错误；`apitoken.NewClient` 在首次 `Authorize` 调用时校验，两者产生相同客户端。默认 3 秒超时，禁止跨域重定向，不缓存结果，不把密钥放进错误信息。生产地址要求 HTTPS，HTTP 仅允许 localhost/loopback 开发地址。

## 14. 故障排查

| 现象 | 可能原因 | 处理 |
| --- | --- | --- |
| 运行时鉴权一直拒绝，`reason=endpoint_not_found` | 接口未在快照中、路径不匹配、服务未关联或未发布 | 检查 `service_resource`、`method`、`path`，确认接口已登记并**已发布**。 |
| `reason=endpoint_not_granted` | 主体在该应用/域下没有授予目标 API 的启用角色 | 检查 subject/domain 绑定的角色及角色 API 授权，然后重新发布。 |
| `reason=ambiguous_endpoint` | 同优先级多条路径匹配 | 规范路径，避免重叠定义。 |
| 管理端修改后运行时未变化 | 只改了草稿，没有发布 | 执行发布后再验证；模拟和运行时都只读活动快照。 |
| 发布失败 | 应用不存在或已停用、读取 active service keys 失败（服务目录不可用）、快照构建或写库失败 | 确认应用启用、服务目录可用；跨应用引用/重复编码等应在保存时已被数据库约束和仓储校验拦截，不属于发布预检。 |
| 应用无法进入工作台 | 应用被停用 | 在选择页重新启用。 |
| 服务资源无法新增/编辑 | `nexusauth` 目录模式 | 在 NexusAuth 登记服务资源；local 模式才允许本地维护。 |
| 无法删除服务资源 | 仍被应用关联、API 端点或角色授权引用 | 先移除角色 API 授权，再解除应用关联。 |
| Token 创建后找不到明文 | 明文只在创建/轮换时返回一次 | 轮换或重新创建；中心无法找回。 |
| API Token 返回 401 | 内部决策 `active=false`：格式错误、撤销、停用、过期或凭证错误 | 检查 Token 状态、有效期与格式。 |
| API Token 返回 403 | 内部决策 `active=true`、`allowed=false`：有效但无目标 API 或范围权限 | 检查 Token 权限、服务关联、端点启用状态与 tenant/project 范围。 |
| API Token 返回 503 | 中心超时、连接失败、内部 HTTP 错误或畸形响应 | 检查内部网络、密钥绑定服务与中心可用性；必须 fail closed。 |
| 内部接口返回 401/403 | 内部服务密钥错误或调用服务范围不匹配（中心接入异常） | 核对两端配置的同一密钥及绑定的 `service_resource`；接入服务对外应返回 503，不要透传成业务 Token 的 401/403。 |
| 内部接口返回 400 | 接入服务构造的目标参数错误（中心接入异常） | 检查 `app_key`、`service_resource`、method、path 与 tenant/project 是否按合同构造；接入服务对外返回 503。 |
| 登录后立即掉线或 401 | Cookie hostname 与前端不一致、session secret 变更、回调地址不匹配 | 保证前端与回调同 hostname，重设稳定 session secret，核对身份平台注册地址。 |
| NexusAuth 服务列表为空或不更新 | 远程目录不可用、`api_key` 无效、远程资源停用 | 检查 `SERVICE_RESOURCE_NEXUSAUTH_BASE_URL`、`SERVICE_RESOURCE_API_KEY`；运行时不会回退本地元数据。 |

常用排查命令：

```sh
# 后端健康检查
curl -s http://localhost:8080/healthz

# 查看容器日志
docker compose -f docker-compose.yml logs --tail=200 permission-api dashboard

# 后端测试
cd src && go test ./...

# SDK 测试
cd sdk && go test ./...
```

## 15. 生产检查清单

- [ ] 外部 PostgreSQL 已备份，并按需执行 `001`/`002`/`003`，未对已有业务表执行初始化脚本。
- [ ] `NEXUSAUTHORIZATION_OIDC_ENABLED=true`，authority、client secret、回调与退出地址已按身份平台注册值填写。
- [ ] `OIDC_SESSION_SECRET` 至少 32 字符且多实例一致；`nexusauthorization_session` Cookie 随 HTTPS 自动启用 Secure。
- [ ] `AUTHORIZATION_SERVICE_CREDENTIAL` 使用独立随机值，业务服务通过服务端 Bearer 凭证调用 `/v1/authorize`。
- [ ] `SERVICE_RESOURCE_SOURCE` 按实际设置为 `local` 或 `nexusauth`；nexusauth 模式已配置基础地址和 `API_KEY`。
- [ ] `api_tokens.admin_subjects` 已配置真实管理员 subject，Token 管理员范围最小化。
- [ ] 每个内部调用服务都有各自的 `internal_callers` 密钥（不少于 32 字节随机值），未与业务 Token 或服务凭证复用。
- [ ] 页面与登录回调使用同一 hostname；HTTPS 反向代理保留 Host、转发协议、客户端地址和 Cookie。
- [ ] 生产不通过 Dashboard Nginx 暴露 `/internal/`；内部授权走私有后端地址和 HTTPS。
- [ ] RBAC SDK 路径：身份无效返回 401，鉴权错误或拒绝返回 403；这是后端 RBAC SDK 的现有行为，不同于 Token 接入服务的 503 语义。
- [ ] API Token 接入服务：内部接口 200 且 `active=false` → 对外 401，`active=true` 且 `allowed=false` → 对外 403；中心接入异常（内部 400/401/403/503、超时、畸形响应）→ 对外 503 fail closed，授权通过后仍按 tenant/project 过滤数据。
- [ ] 已在生产部署后核对真实身份平台登录，确认 `/auth/me`、`/signin-oidc` 和退出流程可用。
- [ ] 变更 API/角色/菜单/主体绑定后已重新发布，并通过权限模拟与实际请求验证。

## 相关文档

- [快速开始](quick-start.md)
- [Docker 部署与环境变量](docker-deployment-1.0.2.md)
- [功能与数据模型](current-system-overview.md)
- [完整表结构](current-database-schema.md)
- [应用边界 RBAC 设计](rbac-v1-design.md)
- [参考接口](reference-api-rbac-v1.md)
- [开放 API Token 设计与实现](api-token-design.md)
- [API Token 使用与服务接入](api-token-integration.md)
- [Go SDK 接入](sdk-application-integration.md)
