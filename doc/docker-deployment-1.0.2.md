# NexusAuthorization 1.0.2 Docker 部署

前后端镜像为 `18790997531/nexusauthorization-api:1.0.2` 和 `18790997531/nexusauthorization-dashboard:1.0.2`，架构为 Linux amd64。配置只使用 `NEXUSAUTHORIZATION_` 前缀；旧版本使用过的历史前缀变量不再读取。已有服务器需要同步更新 `.env` 并拉取更新后的镜像。

## GitHub Actions 发布镜像

在本仓库的 Settings → Secrets and variables → Actions 中配置 `DOCKERHUB_USERNAME` 和 `DOCKERHUB_TOKEN`，名称及凭据与 NexusAuth 项目一致。仓库级 Secrets 需要在本仓库单独配置；组织级 Secrets 需要允许本仓库访问。

在 Actions 中分别选择 `Build and Push NexusAuthorization API Docker Image` 和 `Build and Push NexusAuthorization Dashboard Docker Image`，点击 Run workflow，为两个 workflow 填写相同的 `tag`（例如 `1.0.2` 或 `latest`）。发布架构为 `linux/amd64`，只推送填写的标签；前端 workflow 会先安装依赖并构建 `dist`。

发布成功后，将 Compose 中前后端镜像的标签改为填写的版本，再执行 `docker compose pull` 和 `docker compose up -d`。

## 部署当前工作区代码

两个 Compose 文件都只声明 `image`，不会构建源码。固定的 `1.0.2` 标签不能证明镜像包含当前修改；部署这次整理后的实现，必须先构建前后端并使用对应的新标签。

```sh
# 在仓库根运行；保留当前锁文件，先安装 dashboard 依赖
(cd dashboard && yarn install --frozen-lockfile && npm run build)
docker build --platform linux/amd64 -t YOUR_REGISTRY/nexusauthorization-api:YOUR_RELEASE .
docker build --platform linux/amd64 -t YOUR_REGISTRY/nexusauthorization-dashboard:YOUR_RELEASE dashboard

# 需要在其它服务器拉取时，再推送两个镜像
docker push YOUR_REGISTRY/nexusauthorization-api:YOUR_RELEASE
docker push YOUR_REGISTRY/nexusauthorization-dashboard:YOUR_RELEASE
```

将实际使用的 Compose 中两个 `image` 改为上述同一发布标签，再按下文初始化空库、校验配置并启动。前端 Dockerfile 复制 `dashboard/dist`，所以前端构建必须在 `docker build` 前完成。若从 Git 拉取部署，请先提交本次源码、初始化 SQL、脚本和文档；未跟踪文件不会出现在新的检出目录里。`.env` 与真实凭据不提交。

本次浏览器与部署产物复核见 [2026-10-14 验收记录](deployment-verification-2026-10-14.md)。

## 命名切换注意事项

本次命名从旧标识统一为 `nexus-authorization`，浏览器中按旧名称保存的登录回跳、业务应用状态和页签集合不再沿用。升级后需要重新登录并重新选择业务应用，旧页签状态也需重建。Compose 只固定镜像标签，不会自动包含本次改动，必须按上文构建并推送新的前后端镜像，再更新部署标签。文档和 SQL 示例中的数据库用户名只是占位示例；执行 `ALTER DATABASE "nexus-authorization" OWNER TO "nexus-authorization"` 不会创建或重命名已有的 PostgreSQL 用户，已有环境请把示例值替换为实际应用用户。

## 两种 Compose

`docker-compose-dev.yml` 包含 PostgreSQL、后端和前端。默认禁用 SSO、使用本地服务资源，端口只绑定本机：前端 `http://localhost:5274`，数据库 `localhost:55434`。空数据卷首次启动时由 PostgreSQL 创建 `nexus-authorization`，并且只执行挂载的 `migrations/001_init.sql` 建立完整 schema。

```sh
cp .env.dev.example .env.dev
# 修改需要调整的本地配置
docker compose --env-file .env.dev -f docker-compose-dev.yml config -q
docker compose --env-file .env.dev -f docker-compose-dev.yml up -d
```

`docker-compose.yml` 只包含后端和前端。连接已有 PostgreSQL，固定启用 SSO，不创建数据库、不自动执行迁移。首次上线使用空库，先由 PostgreSQL 管理员执行建库脚本，再由库拥有者初始化 schema：

```sh
# PostgreSQL 管理员连接 postgres 维护库执行，建库脚本不能放进事务
psql 'postgres://postgres:ADMIN_PASSWORD@db.example.com:5432/postgres?sslmode=require' \
  -X -v ON_ERROR_STOP=1 -f migrations/000_create_database.sql

# 仅在应用使用独立数据库用户时执行；先由管理员创建该用户
psql 'postgres://postgres:ADMIN_PASSWORD@db.example.com:5432/postgres?sslmode=require' \
  -X -v ON_ERROR_STOP=1 -c 'ALTER DATABASE "nexus-authorization" OWNER TO "nexus-authorization";'

cp .env.example .env
# 将 NEXUSAUTHORIZATION_DATABASE_URL 指向空库 /nexus-authorization，并填写 SSO、服务目录与 SCIM 凭证
set -a
. ./.env
set +a
sh src/scripts/init-db.sh

docker compose -f docker-compose.yml config -q
docker compose -f docker-compose.yml pull
docker compose -f docker-compose.yml up -d
```

建库脚本默认把数据库归执行建库的管理员所有。生产使用独立应用用户时，管理员须先把 `nexus-authorization` 的 owner 改为该用户；`nexus-authorization` 是示例用户名，可按实际环境替换。初始化必须由库拥有者连接空库执行，否则创建扩展、表、触发器和索引时可能权限不足。`src/scripts/init-db.sh` 只执行 `migrations/001_init.sql`；该脚本自带 `BEGIN` / `COMMIT`，不要再包一层事务，并且不创建 `schema_migrations`、不插入业务种子数据。`src/scripts/migrate.sh` 仅作为转调 `init-db.sh` 的兼容入口，首次上线只使用 `init-db.sh`。完整说明见 [数据库初始化](../migrations/README.md)。

## 保留的环境变量

下面变量前缀均为 `NEXUSAUTHORIZATION_`，表内写出后缀。

| 后缀 | 用途 |
| --- | --- |
| `DATABASE_URL` | 外部 PostgreSQL 连接串；开发版由 Compose 生成。密码含特殊字符时须 URL 编码，也可使用 pgx 支持的 keyword DSN。 |
| `OIDC_ENABLED` | 本地是否启用 SSO；生产 Compose 固定为 true。 |
| `OIDC_AUTHORITY` | 身份提供商公开地址。 |
| `OIDC_BACKCHANNEL_AUTHORITY` | 仅开发 Compose 可选：后端访问同一身份提供商的内部地址；生产统一使用公开认证域名。 |
| `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` | 身份平台注册的客户端及其密钥。 |
| `OIDC_REDIRECT_URI` | 完整登录回调地址，例如 `https://permissions.example.com/signin-oidc`。 |
| `OIDC_POST_LOGOUT_REDIRECT_URI` | 完整退出后地址，例如 `https://permissions.example.com/`。 |
| `OIDC_SCOPES` / `OIDC_AUDIENCE` | 可选：按身份平台注册值设置；默认沿用 `permission.center.api`，scopes 必须包含 openid。 |
| `OIDC_SESSION_SECRET` | 可选的自管会话密钥，至少 32 字符；默认无需配置，服务自动生成并保存。默认 Compose 不传入此变量。 |
| `SERVICE_RESOURCE_SOURCE` | local 或 nexusauth；开发默认 local，生产默认 nexusauth。 |
| `SERVICE_RESOURCE_NEXUSAUTH_BASE_URL` | nexusauth 模式下的远程目录服务地址。 |
| `SERVICE_RESOURCE_API_KEY` | nexusauth 模式下的服务目录访问凭证。 |
| `SCIM_NEXUSAUTH_BASE_URL` | 生产必填的 NexusAuth Provider SCIM 基址；客户端追加 `/scim/v2/Users`。 |
| `SCIM_API_KEY` | 生产必填的 NexusAuth SCIM 专用 Bearer Token，只需授予 `scim:read`；不要提交到仓库。 |
| `SCIM_TIMEOUT` | SCIM 用户列表请求超时，默认 `5s`。 |

SSO 启用时须填写 authority、客户端密钥、登录及退出地址。会话密钥无需手工设置。配置中的 `permission.center` 客户端标识和 `permission.center.api` audience 是身份平台注册的值，环境变量前缀改名不会修改身份平台注册信息。

Cookie 名称固定为 `nexusauthorization_session`，Secure 标志按登录回调地址自动设置：HTTPS 为 true，HTTP 本地开发为 false。HttpOnly、SameSite=Lax 和 Path=/ 由代码管理。Cookie 不再通过环境变量或 YAML 配置；升级后旧 Cookie 名称改变，需要重新登录。

未显式配置会话密钥时，服务首次启动自动生成 256 位随机密钥，保存在 `data/oidc-session.key`（容器中为 `/app/data/oidc-session.key`），文件权限为 `0600`。生产和开发 Compose 都挂载 `session-data:/app/data`，重启、容器重建时复用密钥。读取或保存失败、密钥文件损坏时启动失败，不会静默更换密钥。会话仍存于加密 Cookie 中；从原来的手工密钥切换到自动密钥，用户需要重新登录一次。删除该卷（例如 `down -v`）会丢失密钥并使已有会话失效。

多个实例需要共享同一个持久化密钥文件；独立服务器的同名 Docker 卷不会自动共享。自管多实例也可显式提供相同的 `NEXUSAUTHORIZATION_OIDC_SESSION_SECRET`，需自行在 Compose 的 `environment` 中传入。自动生成行为涉及后端源码与 Dockerfile 修改，必须重新构建并发布后端镜像，再更新 Compose 的镜像标签；现有远程 `1.0.2` 镜像不保证包含此行为。

HTTP 监听端口、配置路径、日志、资源目录超时和重复别名已从环境变量移除。镜像内固定读取 `configs/app.yaml`，监听 `:8080`，日志写到标准输出，目录请求默认超时 5 秒；需要调整 YAML 参数时可挂载配置文件，Cookie 的运行值仍由代码决定。

开发 PostgreSQL 使用 `POSTGRES_DB`、`POSTGRES_USER`、`POSTGRES_PASSWORD`、`POSTGRES_PORT`；这些是数据库镜像的配置，不加应用前缀。`DASHBOARD_BIND_ADDR` 和 `DASHBOARD_PORT` 控制 Compose 对宿主机的端口映射。

生产和开发 Compose 不再配置独立的 `AUTHORIZATION_SERVICE_CREDENTIAL`。当前 `/v1/authorize` 在服务凭证为空时拒绝所有调用（返回 `401`）；管理后台、角色配置、权限发布和管理端模拟接口照常使用各自的认证流程。业务服务 SDK 接入需等统一 Open API Token 管理及校验完成；仅在外部创建 Token 并不会自动使当前接口接受它。后端保留旧服务凭证校验作为兼容实现。

生产模板只含占位符；不要直接用于生产。可生成数据库密码：

```sh
openssl rand -hex 32
```

## 登录与反向代理

后端不直接暴露宿主端口。前端 Nginx 把 `/api/` 去掉前缀后转发至后端，并把 `/signin-oidc` 转至后端回调。生产可将 `permissions.example.com` 的 HTTPS 入口反向代理到 dashboard 的 5274 端口，身份平台必须精确注册相同的回调和退出地址。

页面与登录回调必须使用同一个 hostname，使浏览器能发送登录 Cookie；不同子域不能直接共享当前 host-only Cookie。HTTPS 时浏览器只通过 HTTPS 发送 Secure Cookie，外层反向代理到容器的 HTTP 链路不影响此判断。

## 查看状态

```sh
docker compose -f docker-compose.yml ps
docker compose -f docker-compose.yml logs --tail=200 permission-api dashboard
```

开发版在命令中增加 `--env-file .env.dev -f docker-compose-dev.yml`。不要通过 `down -v` 重启开发环境，这会删除数据库卷。

本地真实身份平台登录结果、当前源码构建与验证范围见 [验收记录](deployment-verification-2026-10-14.md)。正式部署使用的新域名、HTTPS、回调和退出地址仍须在目标服务器核对。
