# 快速开始

## 使用 Docker 镜像

本地体验：复制 `.env.dev.example` 为 `.env.dev`，使用包含 PostgreSQL 的开发版配置。默认只监听本机，禁用 SSO，使用本地服务资源。

```sh
cp .env.dev.example .env.dev
docker compose --env-file .env.dev -f docker-compose-dev.yml up -d
```

打开 `http://localhost:5274`。数据库首次使用空数据卷时自动初始化；已有数据卷需按数据库说明手动升级。

服务器部署：复制 `.env.example` 为 `.env`，填入已有数据库、身份平台与服务凭证。生产版只有前后端，不会创建或升级外部数据库。

```sh
cp .env.example .env
docker compose -f docker-compose.yml pull
docker compose -f docker-compose.yml up -d
```

完整配置见 [Docker 部署](docker-deployment-1.0.2.md)。镜像架构为 Linux amd64，版本为 1.0.2。

## 从源代码开发

后端在 `src/`，需要 Go 1.25 和 PostgreSQL。先初始化空库 `migrations/001_init.sql`，已有当前应用模型的数据库仅执行 `002_application_url.sql`。不要对已有业务表执行初始化脚本。

后端从当前工作目录读取 `configs/app.yaml`；环境变量名和含义见部署文档。Go 进程不会自动加载根目录 `.env`，需先导出：

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

前端地址 `http://localhost:5274`，开发代理将 `/api` 请求转到 `http://localhost:8080`。直接开发时登录回调地址必须对应可访问的后端 `/signin-oidc`；Cookie 所属 hostname 须与前端一致。

## 配置权限

1. 新建或选择业务应用，可填写应用地址，使用“打开应用”进入业务系统。
2. 进入权限配置，关联允许使用的服务资源。
3. 创建或导入服务 API，配置菜单及按钮。
4. 创建角色并分配 API、菜单及按钮权限。
5. 给用户标识及业务域绑定角色。
6. 发布该应用的权限版本，再模拟允许和拒绝结果。
7. 业务服务使用 SDK 或 `/v1/authorize`，传入应用、服务资源、用户、业务域、方法和路径进行鉴权。

未发布的草稿不进入运行时鉴权快照。具体操作与数据关系见 [系统说明](current-system-overview.md)，接入见 [SDK 说明](sdk-application-integration.md)。
