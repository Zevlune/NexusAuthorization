# 快速开始

## 使用 Docker 镜像

本地体验：复制 `.env.dev.example` 为 `.env.dev`，使用包含 PostgreSQL 的开发版配置。默认只监听本机，禁用 SSO，使用本地服务资源。

```sh
cp .env.dev.example .env.dev
docker compose --env-file .env.dev -f docker-compose-dev.yml up -d
```

打开 `http://localhost:5274`。PostgreSQL 在空数据卷首次启动时创建 `nexus-authorization`，并且只执行挂载的 `migrations/001_init.sql` 建立完整 schema。

服务器首次上线使用外部空库，按以下顺序执行。生产 Compose 只有前后端，不会创建数据库或自动执行迁移。

```sh
psql 'postgres://postgres:ADMIN_PASSWORD@db.example.com:5432/postgres?sslmode=require' \
  -X -v ON_ERROR_STOP=1 -f migrations/000_create_database.sql

psql 'postgres://postgres:ADMIN_PASSWORD@db.example.com:5432/postgres?sslmode=require' \
  -X -v ON_ERROR_STOP=1 -c 'ALTER DATABASE "nexus-authorization" OWNER TO "nexus-authorization";'

cp .env.example .env
# 编辑 .env，将 NEXUSAUTHORIZATION_DATABASE_URL 指向空库 /nexus-authorization
set -a
. ./.env
set +a
sh src/scripts/init-db.sh

docker compose -f docker-compose.yml pull
docker compose -f docker-compose.yml up -d
```

`000_create_database.sql` 由 PostgreSQL 管理员连接维护库 `postgres` 执行一次，不能放进事务。使用独立应用用户时，管理员须先创建该用户并把 `nexus-authorization` 的所有者改为它；`nexus-authorization` 是示例用户名，可按实际环境替换。`001_init.sql` 由库拥有者连接空库执行，自带 `BEGIN` / `COMMIT`，不要再包一层事务。完整说明见 [数据库初始化](../migrations/README.md)。

完整配置见 [运维操作手册](operations-manual.md)。

## 从源代码开发

后端在 `src/`，需要 Go 1.25 和 PostgreSQL。源码开发连接默认使用 `postgres://postgres:postgres@localhost:55434/nexus-authorization?sslmode=disable`，与开发 Compose 一致。指向已存在的空库后执行 `sh src/scripts/init-db.sh` 即可，不需要手动指定 SQL 文件。

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

前端地址 `http://localhost:5274`，开发代理将 `/api` 请求转到 `http://localhost:8080`，并把 OIDC 回调 `/signin-oidc` 单独转发到后端。直接开发时登录回调地址必须对应可访问的后端 `/signin-oidc`；Cookie 所属 hostname 须与前端一致。

## 配置权限

1. 新建或选择业务应用，可填写应用地址，使用“打开应用”进入业务系统。
2. 进入权限配置，关联允许使用的服务资源。
3. 创建或导入服务 API，配置菜单及按钮。
4. 创建角色并分配 API、菜单及按钮权限。
5. 在角色行点击「分配用户」，通过 SCIM 目录选择用户；当前界面写入默认空域（`domain` 为空串），其它具名域可通过管理 API 配置。
6. 用「权限模拟」选择目标服务与 Path、HTTP 方法和用户，验证允许和拒绝结果；角色、绑定、菜单和 API 保存后立即生效。
7. 业务服务使用 SDK 或 `/api/v1/authorize`，传入应用、服务资源、用户、方法和路径进行鉴权；业务域可选，省略即使用默认空域。

当前实现没有草稿或发布版本，保存成功即参与运行时鉴权。逐页操作见 [标准使用手册](user-guide.md)，数据关系见 [系统说明](current-system-overview.md)，生效与缓存规则见 [权限生效设计](permission-lifecycle.md)，接入见 [SDK 说明](sdk-application-integration.md)。
