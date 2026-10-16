# 前端开发说明

本文档描述 `dashboard/` 当前实际使用的入口、路由、布局与组件组织方式。页面行为与真实文案见 [UI 设计约束](ui-design.md)，面向用户的操作步骤见 [使用手册](user-guide.md)。

## 技术栈与命令

`dashboard/` 使用 React 19、TypeScript、Vite 6、TDesign React、TDesign Icons、Axios 和 Less。`dashboard/package.json` 提供以下命令：

| 命令 | 作用 |
| --- | --- |
| `npm install` | 安装前端依赖。 |
| `npm run dev` | 启动 Vite 开发服务器，默认端口 `5274`。 |
| `npm run build` | 先运行 `tsc -b`，再生成 Vite 生产构建，输出到 `dist/`。 |
| `npm run lint` | 执行 ESLint。 |
| `npm run test:e2e` | 使用 Playwright 运行 dashboard E2E 测试。 |
| `npm run preview` | 预览已生成的 `dist/`。 |

类型检查包含在 `npm run build` 中，没有单独的 typecheck 脚本。

## 本地开发

```sh
cd dashboard
npm install
npm run dev
```

浏览器打开 `http://localhost:5274/`。Vite 开发服务器按 `dashboard/vite.config.ts` 把 `^/api(?:/|$)` 代理到 `http://localhost:8080` 并保留 `/api` 前缀：浏览器请求 `/api/auth/me` 时后端收到的同样是 `/api/auth/me`。代理使用 `changeOrigin`，前端 Axios 使用 `withCredentials: true` 携带 cookie。

本地后端应监听 `:8080`，并准备 PostgreSQL、配置文件、服务资源来源和需要的 OIDC / SCIM 配置。前端不保存或注入任何 OIDC client secret、SCIM Token，认证与远端密钥只配置在后端环境。

## 生产构建和镜像

`dashboard/Dockerfile` 是运行时镜像 Dockerfile，只把现成的 `dist/` 复制到 Nginx。构建镜像前必须先生成前端产物：

```sh
cd dashboard
npm install
npm run build
docker build -t nexusauthorization-dashboard:local .
```

镜像使用 Nginx 提供静态文件，容器监听 `80`。Nginx 将 `/api` 转发到 `nexus-authorization:8080` 并保留前缀，同时把 OIDC 回调 `/signin-oidc` 用单独 location 转发到后端；其它路径回退到 `index.html`，支持 React 路由刷新。生产反向代理应使用 HTTPS，并保留 Host、转发协议、客户端地址和 cookie。

部署配置和首次启动步骤见 [运维操作手册](operations-manual.md) 与 [快速开始](quick-start.md)；不要把 OIDC secret 写进 dashboard 构建参数或前端静态资源。

## 应用入口与组件布局

应用从 `dashboard/src/main.tsx` 挂载；它在渲染前先应用主题。`dashboard/src/App.tsx` 在首屏调用一次会话检查（`checkAuth`），随后渲染 `RootRouterProvider`。

`RootRouterProvider`（`dashboard/src/router/provider.tsx`）在挂载时调用 `getMenuList()` 拉取工作台菜单记录，用 `buildWorkbenchMenus()` 构建任意层级树，再用 `createWorkbenchRouter()` 创建基于 `createBrowserRouter` 的路由，并通过 `WorkbenchNavigationContext` 把菜单树共享给布局。

布局 `PublicLayout`（`dashboard/src/layouts/layout/index.tsx`）是登录后的统一外壳：

- `layouts/layout/header`：业务应用显示与「切换应用」、主题切换、全屏。
- `layouts/layout/side/workbench-menu.tsx`：按菜单树渲染 TDesign `Menu`；分组渲染 `SubMenu`，叶子渲染 `MenuItem`，无页面组件的叶子置灰。
- 内容区：在 `layouts/layout/index.tsx` 内组装，顶部为页签（Tabs），下方为 `Outlet`。
- `layouts/layout/avatar`：当前用户与账号设置、退出登录。
- `layouts/layout/logo`、`components/brand`：品牌区。

页面组件位于 `dashboard/src/pages/`。当前工作台实际使用的页面是：

- `pages/select-service-resource/index.tsx`：选择业务应用页，路由 `/select-application`。
- `pages/login/index.tsx`：统一登录页。
- `pages/policy-workbench/index.tsx`：工作台页面分派入口，按 `pathname` 渲染下述组件。
- `pages/policy-workbench/components/services.tsx`：服务资源。
- `pages/policy-workbench/components/apis.tsx`：API 接口。
- `pages/policy-workbench/components/menus.tsx`、`menus-parent-picker.tsx`：菜单与按钮。
- `pages/policy-workbench/components/role-editor.tsx`、`role-permission-tree.ts`：角色编辑器。
- `pages/policy-workbench/components/role-users.tsx`：角色用户分配抽屉。

`pages/policy-workbench/shared.tsx` 提供 `Page`（标题 + 面包屑）、`Guard`（未选择应用 / 加载失败状态）、`Empty` 与 `fail` 等共享件；`pages/nexus-authorization/shared.tsx` 提供 `getRequestErrorMessage` 等通用工具，仍被工作台引用。

## 路由和认证

路由由 `createRoutes(menus)` 组装（`dashboard/src/router/routes.tsx`）：

- `/login`：登录页；已登录访问时按落地路径重定向。
- `/auth/callback`：后端完成 OIDC 回调后的前端过渡路由，随后进入 `/select-application`。
- `/`：按登录与所选应用重定向。
- `/select-application`：登录后的选择业务应用页。
- `/select-service-resource`：旧路径，重定向到 `/select-application`。
- 受保护布局：进入前要求登录（`RequireAuth`）并已选择业务应用（`RequireServiceResource`）。
- 工作台页面：`/dashboard`、`/service-resources`、`/api-resources`、`/menus`、`/roles`、`/roles/new`、`/roles/:roleId/edit`、`/simulate`。发布页面 `/releases` 已删除。
- `/bindings`：旧路径，重定向到 `/roles`。
- `*`：错误页。

登录与回跳逻辑集中在 `dashboard/src/router/auth.tsx`：会话检查用 `checkAuth`（`GET /api/auth/me`），未登录跳转 `/login?redirect=...`，已登录但未选应用跳转 `/select-application?redirect=...`。登录回跳地址读写 `localStorage` 的 `nexus-authorization-login-redirect`；当前业务应用读写 `sessionStorage` 的 `nexus-authorization-application`，并通过 `nexus-authorization-application-change` 事件在组件间同步（`dashboard/src/api/application.ts`）。

后端回调地址是 `/signin-oidc`，不是 dashboard 的 `/auth/callback`；它位于 `/api` 前缀之外，由 `/signin-oidc` 代理规则单独转发到后端。前端只处理后端回调完成后的页面跳转。

退出登录从头像下拉发起：清空页签和回跳地址、清除本地登录缓存，再调用 `POST /api/auth/logout` 并跳转后端返回的 `logoutUrl`，失败回退 `/login`（`dashboard/src/layouts/layout/avatar/index.tsx`）。

`dashboard/src/api/request.ts` 统一创建 `/api` Axios base URL、解包统一响应结构（`success`/`result`），并在受保护 API 收到 401 时回到 `/login`；认证请求本身不触发普通 401 跳转，避免登录检查循环。

## 导航模型（工作台 mock）

工作台侧栏来自 `dashboard/src/api/auth.ts` 的 `getMenuList()`，当前是前端函数，不发送 HTTP 请求。它返回扁平记录，字段为 `id`、`parentId`、`route`、`componentPath`、`name`、`show`、`iconName`。`buildWorkbenchMenus()` 用 `parentId` 生成任意层级树并计算完整路径，`createWorkbenchRoutes()` 用 `componentPath` 解析到已打包的 `pages/**/index.tsx` 模块并注册路由。

导航数据约定：

- `show: false` 的页面仍注册路由，但不进入侧栏（例如「新增角色」「编辑角色」）。
- `componentPath: null` 且 `show: true` 的节点只作为分组。
- 新增导航或修改标题时改 `getMenuList()` 返回的数据，不要改页面里的硬编码。
- `componentPath` 采用 `/页面目录/index.tsx` 格式，只能加载 `pages/` 内已打包的页面模块。

dashboard 自身导航与「菜单与按钮」页维护的业务应用菜单是两套数据。业务菜单 CRUD 调用 `/api/v1/rbac-v1/menus`，不会改动 dashboard 的导航；mock 导航也不承担后端权限校验，运行时鉴权由后端按当前已保存配置判断，保存提交后立即生效。

## 页面开发约定

业务页面使用 React 函数组件、TDesign 控件和本地 Less 样式，页面级样式与组件同目录。路由页面应遵循已有的应用选择、工作台 mock 导航与当前业务应用（session 级）状态模型。

新增 API 请求应沿用 `dashboard/src/api/request.ts`，不要在页面里另建 Axios 实例或读取认证密钥。涉及登录、cookie、重定向或 `/api` 路径时，同时检查 `vite.config.ts`、`nginx.conf` 与后端反向代理约定。

## 历史代码（不再作为当前页面入口）

旧的按后端菜单动态注册权限中心页面的实现，以及旧的侧栏菜单与内容容器模块，已不在当前入口中使用并已从工作区移除；它们的当前替代实现是 `router/workbench-menu.ts`、`router/workbench-routes.tsx`、`router/provider.tsx` 与 `layouts/layout/side/workbench-menu.tsx`。修改导航相关代码时应基于这些当前模块，不要恢复旧文件。

历史 `dashboard/src/pages/nexus-authorization/**/index.tsx` 页面被旧 E2E mock 的组件路径引用，按测试保留原则继续保留；当前工作台对应能力在 `pages/policy-workbench/`。旧的独立用户角色绑定组件 `users.tsx` 没有运行入口或测试引用，已删除；当前用户分配在角色管理的「分配用户」抽屉中。

`pages/nexus-authorization/shared.tsx` 与 `pages/nexus-authorization/style.less` 仍被工作台页面引用，不能按“整目录废弃”处理。

## 验证

构建和静态检查：

```sh
cd dashboard
npm run build
npm run lint
```

E2E 测试：

```sh
npm run test:e2e
```

E2E 用例位于 `src/test/dashboard/e2e/`（如 `workbench-navigation.spec.ts`、`workbench-list-layout.spec.ts`、`menu-workbench.spec.ts`、`role-editor.spec.ts`、`scim-user-binding.spec.ts`）。这些用例使用 mock API，主要验证页面交互和 HTTP 契约；真实后端、OIDC、SCIM 和数据库链路需要按 [运维操作手册](operations-manual.md) 的首次初始化流程单独验收。

## 相关文件

- [`dashboard/package.json`](../dashboard/package.json)：依赖和脚本。
- [`dashboard/vite.config.ts`](../dashboard/vite.config.ts)：端口 `5274` 和开发代理 `8080`。
- [`dashboard/nginx.conf`](../dashboard/nginx.conf)：生产静态文件、`/api` 代理，以及 OIDC 回调 `/signin-oidc` 代理。
- [`dashboard/Dockerfile`](../dashboard/Dockerfile)：要求先生成 `dist/` 的 Nginx 运行时镜像。
- [`doc/ui-design.md`](ui-design.md)：当前 UI 约束、页面行为与代码证据。
- [`doc/user-guide.md`](user-guide.md)：面向用户的操作手册。
