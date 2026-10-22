# NexusAuthorization 开发与二次开发指南

整理日期：2026-10-16。本文面向需要本地运行、修改功能、扩展权限模型或维护部署产物的开发人员。

## 1. 开发环境

建议准备：

- Go 1.25；
- Node.js 与 npm；
- PostgreSQL 16；
- Docker Compose；
- Redis（服务 API 鉴权默认启用；禁用后使用数据库 current-policy，菜单始终不依赖）；
- 可选 Playwright 浏览器运行环境。

仓库主要目录：

| 目录 | 责任 |
| --- | --- |
| `src/cmd/api-server` | Go 服务入口 |
| `src/internal/app` | 依赖装配与生命周期 |
| `src/internal/auth` | OIDC 配置、会话和认证中间件 |
| `src/internal/biz` | 领域模型、授权规则、目录适配和策略编译 |
| `src/internal/data/db/repo` | PostgreSQL 仓储和事务 |
| `src/internal/data/rediscache` | Redis 服务 API 授权记录、`state` 闸门与批量替换 |
| `src/internal/server/http` | 路由、请求解析和响应映射 |
| `dashboard/src` | React 页面、路由、API 和样式 |
| `sdk/rbac` | 业务服务 Go SDK |
| `migrations` | 首次建库脚本和初始化说明 |
| `src/test/dashboard` | 前端 Playwright E2E 测试（Go 单元与集成测试与被测代码放在 `src/internal` 的同一包内） |
| `doc` | 设计、使用、部署和验证文档 |

## 2. 本地启动

### 2.1 使用开发 Compose

开发 Compose 会启动 PostgreSQL、API 和 Dashboard。空数据卷首次启动时自动执行 `001_init.sql`：

```sh
docker compose -f docker-compose-dev.yml up -d
docker compose -f docker-compose-dev.yml ps
```

默认 Dashboard 地址为 `http://127.0.0.1:5274`，PostgreSQL 地址为 `localhost:55434`。默认关闭 OIDC，适合本地页面和 API 开发；需要联调统一认证时，通过 `.env.dev` 覆盖 OIDC、服务目录和 SCIM 配置。

服务 API 的 `redis.enabled` 默认为 `true`，启用时必须提供非空 Redis URL：

```sh
NEXUSAUTHORIZATION_REDIS_ENABLED=true \
NEXUSAUTHORIZATION_REDIS_URL='redis://localhost:6379/0' \
docker compose -f docker-compose-dev.yml up -d
```

本地不需要 Redis 时，显式禁用后重启 API：

```sh
NEXUSAUTHORIZATION_REDIS_ENABLED=false docker compose -f docker-compose-dev.yml up -d
```

### 2.2 独立运行后端

指向已初始化的 PostgreSQL 后，进入 `src/` 启动：

```sh
cd src
go run ./cmd/api-server
```

默认读取 `src/configs/app.yaml`。数据库 URL、OIDC、服务目录、SCIM 和 Redis 均可以通过 `NEXUSAUTHORIZATION_*` 环境变量覆盖。`redis.enabled` 省略时仍为 `true`；启用时 `redis.url` 必须非空，禁用时不创建 Redis 客户端或发布记录。配置加载时会验证数据库、服务目录 source、远程 URL、启用模式的 Redis URL 和 SCIM 超时；配置错误会在进程启动阶段退出。

### 2.3 启动前端

```sh
cd dashboard
npm install
npm run dev
```

Vite 默认监听 `5274`，把 `/api` 请求代理到后端 `8080` 并删除 `/api` 前缀。前端通过 cookie 访问后端，不读取 OIDC client secret、SCIM token 或服务目录凭证。

## 3. 验证命令

后端：

```sh
cd src
gofmt -w $(rg --files -g '*.go' cmd internal)
go test ./...
go vet ./...
go build ./...
```

涉及并发、缓存或策略编译时再运行：

```sh
go test -race ./internal/biz/... ./internal/data/rediscache/...
```

SDK：

```sh
cd sdk
go test ./...
```

前端：

```sh
cd dashboard
npm run lint
npm run build
npm run test:e2e
```

E2E 测试会启动 Vite 开发服务器并模拟全部后端接口，不需要后端。本机没有 Playwright 自带浏览器时，可以指向已安装的 Chrome：`PLAYWRIGHT_EXECUTABLE_PATH='C:/Program Files/Google/Chrome/Application/chrome.exe' npx playwright test`。

仓储集成测试需要独立 PostgreSQL，使用一次性数据库连接串：

```sh
cd src
NEXUSAUTHORIZATION_TEST_DATABASE_URL='postgres://.../nexus_test?sslmode=disable' \
  go test -race -count=1 -timeout=120s ./internal/data/db/repo -v
```

不要把个人数据库、OIDC secret 或真实 SCIM token 写进测试文件、`.env` 或提交记录。

## 4. 代码修改边界

新增或修改功能时遵循现有分层：

1. HTTP Handler 只负责读取路径/查询/JSON、调用业务服务和映射错误；
2. `internal/biz` 负责校验、领域规则、授权决策和外部目录接口；
3. Repository 负责 SQL、事务、行映射和数据库约束协作；
4. Redis 启用时，Redis 层保存服务 API 授权记录，控制面在保存业务表后同步重建，不保存业务版本；禁用时业务入口走数据库 current-policy，不访问 Redis；
5. Dashboard API 统一走 `dashboard/src/api/request.ts`，页面不自行创建 Axios 实例；
6. SDK 保持失败关闭，并且禁止把用户可控输入当作 app/service/subject 的可信来源。

导出的 Go 类型和函数使用 GoDoc；复杂算法旁边解释“为什么这样做”和失效条件，避免只重复代码语句。修改代码时不要用注释掩盖未处理的错误，也不要为没有业务语义的简单赋值增加噪声注释。

## 5. 扩展权限模型

如果要新增租户、资源动作、显式 deny 或其它权限维度，不能只修改前端表单。至少需要同步设计：

- 数据库字段、索引、唯一约束和跨边界约束；
- Repository 查询和事务；
- `CurrentPolicy` 数据装载；
- `compiledRBACPolicy` 的编译和匹配；
- Redis 启用时的保存后服务 API 记录重建，以及禁用时的数据库 current-policy 读取；
- `/api/v1/rbac-v1/simulate` 输入和结果；
- `/api/v1/authorize` 请求字段及 SDK；
- 管理端 API、表单、回显和错误处理；
- 单元测试、集成测试和文档。

策略匹配的关键代码在 `src/internal/biz/rbac_policy.go`，业务操作在 `src/internal/biz/rbac_v1.go`，服务 API 聚合与编译在 `src/internal/biz/subject_api_authorization.go`，数据库读取在 `src/internal/data/db/repo/current_policy_repo.go` 和 `src/internal/data/db/repo/subject_api_repo.go`。先阅读 [系统设计](system-design.md) 和 [鉴权匹配与性能](authorization-matching.md)，再修改实现。

## 6. 新增管理能力的步骤

以新增一个应用配置字段为例：

1. 修改 `migrations/001_init.sql` 中的完整初始结构；
2. 更新 Go model、Repository 的 `SELECT/INSERT/UPDATE` 和校验；
3. 更新 HTTP request/response 结构、Swagger 文档和错误映射；
4. 更新 Dashboard API 类型、表单、列表和加载状态；
5. 增加后端单元或集成测试，以及前端 mock/E2E 用例；
6. 更新使用手册、接口参考、设计说明和部署变量说明；
7. 使用全新的空 PostgreSQL 数据库执行 `001_init.sql` 验证结构；
8. 执行 Go、SDK、前端构建和 lint。

初始化 SQL 是当前系统的完整初始结构。修改表结构时直接更新这份脚本，并在独立空库上完整执行验证；不要把临时 SQL 直接用于目标环境后结束，必须回写仓库和文档。数据库结构变更按首次上线流程处理：删除旧数据库后，在独立空库中重新执行完整的 `migrations/001_init.sql`。

## 7. SDK 二次开发

SDK 位于 `sdk/rbac`，当前入口为：

```go
client := rbac.NewClient(baseURL, credential)
decision, err := client.Check(ctx, rbac.Request{
	AppKey: "order-console", ServiceResource: "order-api",
	Subject: "user:123", Method: http.MethodGet, Path: "/orders/1",
})
```

中间件使用 `Require(appKey, serviceResource, resolver, next)`。`resolver` 必须从业务服务已经验证过的上下文返回 subject 和可选 domain。SDK 会校验必填字段、Bearer 凭证和服务端决策格式；远程错误、deny、非法响应都会阻止下游执行。

SDK 改动后至少验证：缺少凭证、缺少 app/service/subject、HTTP 非 200、非法 JSON、允许响应、拒绝响应和 resolver 未认证等分支。协议字段改变时同步更新 `doc/sdk-application-integration.md` 和 `doc/reference-api-rbac-v1.md`。

## 8. 前端二次开发

当前 Dashboard 的工作台导航来自 `dashboard/src/api/auth.ts` 的内置 mock 结构，业务菜单资源来自 `/api/v1/rbac-v1/menus`，两者不能混用。

新增页面通常需要：

1. 在 `dashboard/src/pages/` 创建页面和同目录 Less；
2. 在工作台导航定义中增加 `route`、`componentPath` 和显示状态；
3. 在 `dashboard/src/api` 增加请求函数和类型；
4. 在请求失败、401、空数据和重复提交时提供页面状态；
5. 增加对应 Playwright mock/E2E；
6. 更新 UI 设计和用户手册。

Dashboard 部署镜像只复制 `dist/`，构建前必须执行 `npm run build`。Nginx 保留 `/api` 前缀转发，OIDC 回调 `/signin-oidc` 由单独 location 转发（该路径不在 `/api` 前缀内），一旦修改 `/api`、`/signin-oidc` 或 React fallback 规则，应同时做容器级 smoke test。

## 9. 发布构建

构建 API 镜像：

```sh
docker build -t nexusauthorization-api:local .
```

构建 Dashboard 镜像：

```sh
cd dashboard
npm install
npm run build
docker build -t nexusauthorization-dashboard:local .
```

发布前记录 API 和 Dashboard 镜像标签、数据库脚本版本、环境变量变更和验收结果。不要把真实 `.env.deploy`、OIDC secret、数据库密码或数据库导出文件加入镜像和 Git。

## 10. 调试顺序

遇到“页面显示正常但权限不对”时，按顺序检查：

1. 浏览器 Network 中请求的 `app_key`、`service_resource`、HTTP 状态和响应体；
2. API 服务日志中的 subject、路由和数据库错误；
3. 应用是否启用、服务是否关联且启用、API 是否存在且启用；
4. 角色是否启用、是否授权该 API、主体是否绑定该角色；
5. domain 是否和绑定记录一致；
6. 实际 path 是否符合固定/参数路径规则；
7. 确认 `redis.enabled`；启用时检查 Redis 服务 API 记录是否重建成功、应用 `state` 是否为 `ready` 及前缀，禁用时检查数据库 current-policy；
8. 用 `/api/v1/rbac-v1/simulate` 复现同一组输入。

若页面无法登录，先用 `/api/healthz` 和 `/api/auth/config` 区分进程问题、反向代理问题与 OIDC 配置问题。若服务目录或 SCIM 失败，直接检查外部 URL、凭证和超时，不要修改权限表来绕过依赖故障。

## 11. 相关文档

- [系统设计说明](system-design.md)
- [运维操作手册](operations-manual.md)
- [用户使用手册](user-guide.md)
- [接口参考](reference-api-rbac-v1.md)
- [Go SDK 接入](sdk-application-integration.md)
- [数据库初始化](../migrations/README.md)
- [前端开发](frontend-development.md)
