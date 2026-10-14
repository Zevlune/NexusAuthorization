# 代码实现与设计对照及清理记录

整理日期：2026-10-14。对照依据为当前工作区代码：HTTP 路由和处理器、业务服务、PostgreSQL 仓储与初始化 SQL、SDK、Dashboard 入口和导航。已有工作区改动保留，当前文档描述最终实现。

## 主要差异与修订

| 原有描述或容易误解之处 | 当前代码事实 | 对应文档 |
| --- | --- | --- |
| 服务资源是角色、菜单及发布的边界 | 业务应用是边界；服务资源提供跨应用共享的 API 目录，关联服务不自动授权 | [系统概览](current-system-overview.md)、[RBAC 设计](rbac-v1-design.md) |
| 鉴权走纯内存快照并原子热更新 | 每次鉴权先查询启用应用，再从 PostgreSQL 读取当前发布并解码 JSONB；没有常驻内存快照缓存 | [接口参考](reference-api-rbac-v1.md) |
| 配置保存后可直接模拟草稿 | 配置保存为草稿；发布生成快照，模拟和业务鉴权均读取已发布版本；停用应用即时生效 | [使用手册](user-guide.md)、[RBAC 设计](rbac-v1-design.md) |
| 通过独立“用户授权”页面绑定用户 | 当前入口是角色行的“分配用户”抽屉；SCIM 只读用户目录；固定 `global`，保留用户其它角色和其它域 | [UI 操作流程](ui-design.md)、[SCIM 接入](nexusauth-scim-users.md) |
| 工作台导航来自已授权业务菜单 | 当前导航来自前端 `getMenuList()` mock；侧栏、页签、面包屑共享该树；业务菜单数据独立 | [前端开发](frontend-development.md)、[UI 说明](ui-design.md) |
| 发布与模拟没有界面入口 | 当前可见入口为 `/releases`、`/simulate`，分别使用“发布当前配置”“运行测试” | [使用手册](user-guide.md) |
| 菜单按钮自动关联 API | 菜单/按钮授权与 API 授权独立；角色编辑器分别配置 | [接口参考](reference-api-rbac-v1.md)、[使用手册](user-guide.md) |
| 所有数据沿用旧审计字段和软删除 | 应用 RBAC 表采用物理删除；兼容 API 目录表单独保留审计与软删除；应用没有删除路由 | [数据库结构](current-database-schema.md)、[系统概览](current-system-overview.md) |
| 历史设计中的审批、回滚、ABAC 已实现 | 当前没有对应接口或 UI；文档明确标注未实现 | [接口参考](reference-api-rbac-v1.md)、[UI 说明](ui-design.md) |
| 首次上线需逐个执行历史升级 SQL | 新库名为 `nexus-authorization`；管理员先建库，库拥有者执行完整 `001_init.sql`；应用地址已合并，无种子数据 | [数据库初始化](../migrations/README.md) |

当前标准操作为：登录 → 选择/创建应用 → 关联服务 → 配置共享 API 与应用菜单 → 配置角色 → 分配用户 → 发布 → 模拟并验证业务请求。逐页字段、按钮和失败处理见 [标准使用手册](user-guide.md)。

## 冗余实现清理

### 前端

删除没有运行入口且没有测试引用的旧路由、布局和资源：

- `dashboard/src/router/dynamic-routes.tsx`
- `dashboard/src/layouts/layout/content/index.tsx`
- `dashboard/src/layouts/layout/side/index.tsx`
- `dashboard/src/layouts/layout/side/menu.tsx`
- `dashboard/src/layouts/common/icon.tsx`
- `dashboard/public/brand/nexusauth-logo.svg`
- `dashboard/src/pages/policy-workbench/components/users.tsx`

其中 `users.tsx` 是清理时工作区内未跟踪的草稿，并非 Git 历史中的生产文件。

删除导航 API 中未使用的 `getVisibleMenuList`，以及未用的应用事件派发、服务查询/存储、主题辅助函数与 SCIM 类型导出。移除没有源代码引用的 `@tdesign-react/chat`，并按依赖闭包清除它独占的 128 个 lock 条目，保留其它依赖的版本和完整传递引用。当前路由及侧栏使用 `workbench-menu.ts`、`workbench-routes.tsx`、`provider.tsx` 和 `side/workbench-menu.tsx`。

### 后端

- 删除 `Application.Permissions` 及旧 `PermissionService`、角色/菜单/用户角色 SQL 仓储的启动装配。
- 删除没有注册路由且没有测试调用的旧 HTTP 处理方法：角色 CRUD、菜单 CRUD/树、角色菜单授权、用户角色查询与替换，以及仅供它们使用的 UUID 解析函数。
- 删除旧服务范围角色/菜单/用户角色仓储的 SQL 实现及其旧扫描、映射代码；当前应用范围实现由 `RBACV1Repository` 承担。
- 删除无实际引用的持久化模型：`model/menu.go`、`role.go`、`role_menu.go`、`user_role.go`、`service_resource.go`。
- 兼容 API 目录复用现有审计映射函数，移除重复字段映射。
- 删除无调用的旧服务范围端点列表业务封装与分页仓储方法；应用工作台的分页接口继续由 `ApplicationEndpoints` 实现。

### 保留范围与原因

所有已有测试文件保留，不通过删测试或修改断言来规避失败。部分旧实现仍被回归测试直接或间接引用，因此保留：

- `biz/permission.go` 及其测试需要的业务类型、接口。
- `nullableMenuHTTPMethod`、`normalizeUserRoleScope`、`normalizeRoleIDs` 测试辅助函数。
- `role_repo.go` 仅保留包声明和说明，作为既有测试软链接的目标；旧 SQL 实现已删除。
- `src/test/` 内原有软链接及其必要目标文件。
- 旧 `pages/nexus-authorization/` 页面仍被历史 E2E mock 的组件路径引用，保留为测试支撑；共享样式/工具还被当前工作台使用。

`/v1/authorization/api-endpoints*` 是仍注册并受测试覆盖的兼容接口，不能仅因新工作台不调用就视为无用代码。其独立表 `authorization_api_endpoints` 已纳入首次初始化，解决新库下缺表的问题；该目录不参与应用 RBAC 发布，新的操作流程使用 `/v1/rbac-v1/*`。

## 验证记录

后续按部署前验收重新启动真实后端、PostgreSQL 和前端，并通过实际 NexusAuth 登录操作。发现并修复了停用应用编辑入口与受控表单回显问题；新增 4 个回归用例，当前流程共 52 项通过，前后端最终 Docker 镜像与 Nginx 代理验证通过。真实浏览器范围与正式上线条件见 [2026-10-14 验收记录](deployment-verification-2026-10-14.md)。下述 48 项为清理完成时的原始记录。

- 保留测试基线：58 个既有路径；实际测试文件内容未变，28 个测试软链接保持有效。
- Go 后端 `go test ./...`、SDK `go test ./...` 通过；在独立 PostgreSQL 16 空库执行建库及完整初始化后，5 个数据库集成测试（含兼容 API 目录、跨应用隔离、菜单按钮规则、并发写入和远程目录）全部通过。
- 全量前端 E2E 共 71 项：当前工作台相关的 48 项通过，旧 `nexus-authorization.spec.ts` 中 23 项失败。旧用例仍预期服务资源选择、后端动态菜单和旧 PDP 策略页面；当前实现采用应用选择、前端工作台导航和应用 RBAC 发布，断言与实现不一致。用例原样保留，结果不记为全量通过。“原样保留”属于清理阶段的原则，指不删测试、不改断言以规避失败；本轮命名同步沿用同一原则，未删除用例或改变断言的业务逻辑。最终代码与依赖清理后，当前工作台 48 项再次通过（22.2 秒）。
- 初始化实际创建 12 张表、4 个枚举、4 个触发器和 7 个显式索引；无业务种子数据，无 `schema_migrations`。前端生产构建和类型检查通过。文档本地链接、脚本语法与 `git diff --check` 均通过。

复核命令（均在对应目录运行）：

```sh
# src/：不设置数据库 URL 时，数据库集成测试会跳过
go test ./...
# 指向只用 001_init.sql 初始化的临时数据库后，真实执行数据库用例
NEXUSAUTHORIZATION_TEST_DATABASE_URL='<disposable-postgres-dsn>' go test ./internal/data/db/repo -run Integration -count=1 -v

# sdk/
go test ./...

# dashboard/
npm run build
# 全量：保留的历史用例仍会失败
npm run test:e2e -- --workers=4 --timeout=15000
# 当前工作台复核；不修改或删除历史用例
npm run test:e2e -- --grep-invert '权限中心关键操作流程' --workers=4
```

## 本轮命名同步

本轮把文档、示例与代码中的旧品牌标识统一为 `nexus-authorization`：后端 Go 模块为 `github.com/luck/nexus-authorization`，SDK 模块为 `github.com/luck/nexus-authorization/sdk`，Swagger 与管理端会话 Cookie 使用实际默认名 `nexusauthorization_session`。会话密钥派生中的域分隔名同步更新为 `nexus-authorization/session-key/`，已有浏览器会话失效，需要重新登录并重新选择业务应用。

本轮只同步名称、import 与测试 fixtures，不删除测试用例，也不改变断言的业务逻辑。上文“用例原样保留”属于清理阶段的记录；本轮历史 23 项用例仅改文件名和 fixture 路径，断言未变，仍沿用清理阶段记录的失败事实，不计入本轮通过结论。

本轮前端 `npm run build` 通过；当前工作台分组 52 项在 `--timeout=60000` 下全量重跑通过，冷启动首次出现的 4 项超时已复跑通过；后端 `go test`、`go vet` 与 SDK 测试通过。本轮按仓库 Dockerfile 构建出本地 linux/amd64 镜像 `nexus-authorization-api:rename-20261014` 和 `nexus-authorization-dashboard:rename-20261014`，未推送、未启动真实部署，因此不记录部署验证结论。
