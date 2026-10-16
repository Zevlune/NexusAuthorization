# NexusAuthorization Dashboard

`dashboard/` 是 NexusAuthorization 的 React 管理前端源码目录，负责业务应用选择、服务资源、API 端点、菜单、角色与权限模拟等管理页面。没有发布版本页面：角色、绑定、菜单和 API 保存后立即生效。前端通过 `/api` 访问后端，不保存 OIDC client secret、access token 或 refresh token；OIDC 密钥只配置在后端。

详细的开发、构建、代理、路由和测试说明见 [前端开发说明](../doc/frontend-development.md)。

## 目录概览

- `src/`：React、路由、页面、API 请求和主题代码。
- `public/`：静态资源。
- `vite.config.ts`：开发服务器端口和 `/api` 代理配置。
- `nginx.conf`：生产静态文件和后端代理配置。
- `Dockerfile`：使用已经生成的 `dist/` 构建运行时镜像。

设计稿、导出图和页面验证截图位于仓库的 [`ui/`](../ui/) 目录，设计说明统一维护在 [`doc/ui-design.md`](../doc/ui-design.md)。
