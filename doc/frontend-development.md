# 前端开发说明

## 技术栈

`dashboard/` 使用 React 19、TypeScript、Vite 6、TDesign React、TDesign Icons、Axios 和 Less。`dashboard/package.json` 提供以下命令：

| 命令 | 作用 |
| --- | --- |
| `npm install` | 安装前端依赖。 |
| `npm run dev` | 启动 Vite 开发服务器。默认端口为 `5274`。 |
| `npm run build` | 先运行 `tsc -b`，再生成 Vite 生产构建。输出目录为 `dist/`。 |
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

浏览器打开 `http://localhost:5274/`。Vite 开发服务器将 `/api` 请求代理到 `http://localhost:8080`，并删除转发路径中的 `/api` 前缀。例如，浏览器请求 `/api/auth/me` 时，后端收到 `/auth/me`。代理使用 `changeOrigin`，前端 Axios 使用 `withCredentials: true` 携带 cookie。

本地后端应监听 `:8080`，并准备 PostgreSQL、配置文件、服务资源来源和需要的 OIDC 配置。前端不负责保存或注入 OIDC client secret；所有认证密钥只配置在后端环境中。

## 生产构建和镜像

当前 `dashboard/Dockerfile` 是运行时镜像 Dockerfile，只把现成的 `dist/` 复制到 Nginx。因此构建镜像前必须先生成前端产物：

```sh
cd dashboard
npm install
npm run build
docker build -t nexusauthorization-dashboard:local .
```

镜像使用 Nginx 提供静态文件，容器监听 `80`。Nginx 将 `/api` 和精确的 `/signin-oidc` 转发到 `permission-api:8080`，并对 `/api` 删除前缀；其他路径回退到 `index.html`，支持 React 路由刷新。生产反向代理应使用 HTTPS，并保留 Host、转发协议、客户端地址和 cookie。

如果使用仓库根目录的 Docker Compose 发布配置，按发布说明使用对应的镜像标签和 `.env`；不要把 OIDC secret 写进 dashboard 构建参数或前端静态资源。

## 路由和认证

当前路由结构如下：

- `/login`：登录页。前端调用 `/api/auth/me` 检查会话，未登录时调用 `/api/auth/login`。
- `/auth/callback`：后端完成 OIDC 回调后返回的前端过渡路由。
- `/select-application`：登录后选择业务应用。
- `/select-service-resource`：兼容旧路径，重定向到 `/select-application`。
- `/dashboard`、`/service-resources`、`/api-resources`、`/roles`、`/bindings`、`/menus`、`/releases`、`/simulate`：受保护的管理页面，由动态菜单和当前应用上下文控制访问。

后端回调地址是 `/signin-oidc`，不是 dashboard 的 `/auth/callback`。同域反向代理时，`/signin-oidc` 必须直接转发到后端；前端只处理后端回调完成后的页面跳转。

`src/api/request.ts` 统一创建 `/api` Axios base URL、解包成功结果，并在受保护 API 收到 401 时回到 `/login`。认证请求本身不会触发普通 401 跳转，避免登录检查循环。

## 页面开发约定

业务页面使用 React 组件、TDesign 控件和本地 Less 样式。路由页面应遵循已有的应用选择、动态菜单、当前业务应用和服务资源 session 状态；后端返回的菜单路径通过动态路由映射到前端页面。

新增 API 请求应沿用 `src/api/request.ts`，不要在页面中另建 Axios 实例或读取 OIDC 密钥。涉及登录、cookie、重定向或 `/api` 路径时，同时检查 `vite.config.ts`、`nginx.conf` 和后端反向代理约定。

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

当前 E2E 测试使用 mock API，旧测试仍有 23 个失败，主要原因是测试断言与当前应用认证及“业务应用/服务资源”路由模型不一致，例如旧断言期望 `/select-service-resource`，而当前应用使用 `/select-application`，以及测试初始化后被认证流程重定向到 `/login`。因此不能将当前 E2E 结果声称为通过，也不能把 mock API 测试当作真实后端、OIDC 或数据库验证。

## 相关文件

- [`dashboard/package.json`](../dashboard/package.json)：依赖和脚本。
- [`dashboard/vite.config.ts`](../dashboard/vite.config.ts)：端口 `5274` 和开发代理 `8080`。
- [`dashboard/nginx.conf`](../dashboard/nginx.conf)：生产静态文件、`/api` 和 `/signin-oidc` 代理。
- [`dashboard/Dockerfile`](../dashboard/Dockerfile)：要求先生成 `dist/` 的 Nginx 运行时镜像。
- [`doc/ui-design.md`](ui-design.md)：设计资源、页面范围和 UI 验证记录。
