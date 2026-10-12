# NexusAuthorization 1.0.2 Docker 部署

前后端镜像为 `18790997531/nexusauthorization:1.0.2` 和 `18790997531/nexusauthorization-dashboard:1.0.2`，架构为 Linux amd64。配置只使用 `NEXUSAUTHORIZATION_` 前缀；旧 `PERMISSION_CENTER_` 变量不再读取。已有服务器需要同步更新 `.env` 并拉取更新后的镜像。

## 两种 Compose

`docker-compose-dev.yml` 包含 PostgreSQL、后端和前端。默认禁用 SSO、使用本地服务资源，端口只绑定本机：前端 `http://localhost:5274`，数据库 `localhost:55434`。新数据卷首次启动时执行 `migrations/` 中的 SQL。

```sh
cp .env.dev.example .env.dev
# 修改需要调整的本地配置
docker compose --env-file .env.dev -f docker-compose-dev.yml config -q
docker compose --env-file .env.dev -f docker-compose-dev.yml up -d
```

`docker-compose.yml` 只包含后端和前端。连接已有 PostgreSQL，固定启用 SSO，不创建数据库、不自动执行迁移。

```sh
cp .env.example .env
# 填写外部数据库、SSO 和服务凭证
docker compose -f docker-compose.yml config -q
docker compose -f docker-compose.yml pull
docker compose -f docker-compose.yml up -d
```

生产数据库须预先存在：空库执行 `migrations/001_init.sql`；已有当前应用模型的 11 张表执行 `migrations/002_application_url.sql`，只增加应用地址列。不要对已有表的数据库执行 `001`。本地初始化 SQL 也只在空数据卷首次启动时执行；已有卷需手动升级。

## 保留的环境变量

下面变量前缀均为 `NEXUSAUTHORIZATION_`，表内写出后缀。

| 后缀 | 用途 |
| --- | --- |
| `DATABASE_URL` | 外部 PostgreSQL 连接串；开发版由 Compose 生成。密码含特殊字符时须 URL 编码，也可使用 pgx 支持的 keyword DSN。 |
| `OIDC_ENABLED` | 本地是否启用 SSO；生产 Compose 固定为 true。 |
| `OIDC_AUTHORITY` | 身份提供商公开地址。 |
| `OIDC_BACKCHANNEL_AUTHORITY` | 可选：后端访问同一身份提供商的内部地址。 |
| `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` | 身份平台注册的客户端及其密钥。 |
| `OIDC_REDIRECT_URI` | 完整登录回调地址，例如 `https://permissions.example.com/signin-oidc`。 |
| `OIDC_POST_LOGOUT_REDIRECT_URI` | 完整退出后地址，例如 `https://permissions.example.com/`。 |
| `OIDC_SCOPES` / `OIDC_AUDIENCE` | 可选：按身份平台注册值设置；默认沿用 `permission.center.api`，scopes 必须包含 openid。 |
| `OIDC_SESSION_SECRET` | 稳定的会话加密密钥，至少 32 字符；多实例须一致。 |
| `SERVICE_RESOURCE_SOURCE` | local 或 nexusauth；开发默认 local，生产默认 nexusauth。 |
| `SERVICE_RESOURCE_NEXUSAUTH_BASE_URL` | nexusauth 模式下的远程目录服务地址。 |
| `SERVICE_RESOURCE_API_KEY` | nexusauth 模式下的服务目录访问凭证。 |
| `AUTHORIZATION_SERVICE_CREDENTIAL` | 业务服务调用 `/v1/authorize` 使用的独立 Bearer 凭证。 |

SSO 启用时须填写 authority、客户端密钥、登录及退出地址、会话密钥。配置中的 `permission.center` 客户端标识和 `permission.center.api` audience 是身份平台注册的值，环境变量前缀改名不会修改身份平台注册信息。

Cookie 名称固定为 `nexusauthorization_session`，Secure 标志按登录回调地址自动设置：HTTPS 为 true，HTTP 本地开发为 false。HttpOnly、SameSite=Lax 和 Path=/ 由代码管理。Cookie 不再通过环境变量或 YAML 配置；升级后旧 Cookie 名称改变，需要重新登录。会话仍存于加密 Cookie 中，修改 session secret 会使现有会话失效。

HTTP 监听端口、配置路径、日志、资源目录超时和重复别名已从环境变量移除。镜像内固定读取 `configs/app.yaml`，监听 `:8080`，日志写到标准输出，目录请求默认超时 5 秒；需要调整 YAML 参数时可挂载配置文件，Cookie 的运行值仍由代码决定。

开发 PostgreSQL 使用 `POSTGRES_DB`、`POSTGRES_USER`、`POSTGRES_PASSWORD`、`POSTGRES_PORT`；这些是数据库镜像的配置，不加应用前缀。`DASHBOARD_BIND_ADDR` 和 `DASHBOARD_PORT` 控制 Compose 对宿主机的端口映射。

生产模板只含占位符；不要直接用于生产。可分别生成数据库密码、会话密钥和业务凭证：

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

本次只进行了测试、构建和配置校验，没有启动部署容器；真实身份平台账号登录仍需在服务器部署后核对。
