# NexusAuthorization 首次部署与运维操作手册

整理日期：2026-10-16。本文面向首次部署、测试环境准备和日常维护人员。所有目标环境的密钥、数据库地址和反向代理配置都应使用真实值，示例中的域名和凭证均为占位符。

## 1. 上线前检查

上线前确认：

- PostgreSQL 16 或兼容版本已准备，部署账号拥有目标库的 schema、扩展、表、触发器和索引创建权限；
- 目标主机可以访问镜像仓库、OIDC Provider、NexusAuth 服务目录和 SCIM 接口；
- 已准备 HTTPS 域名和反向代理；
- 已创建 OIDC 客户端，回调地址精确指向 `/signin-oidc`（该端点位于 `/api` 前缀之外，需要单独反向代理）；
- 已申请服务目录读取凭证和 SCIM 只读凭证；
- 目标环境 `.env` 文件存放在受保护路径，不提交 Git；
- 计划内备份已验证可恢复；
- 业务服务明确使用的 `app_key`、`service_resource` 和身份 subject 格式。

## 2. 首次部署外部 PostgreSQL

Compose 不创建外部数据库。首次部署空库时，先由 PostgreSQL 管理员创建数据库：

```sh
psql 'postgres://postgres:ADMIN_PASSWORD@db.example.com:5432/postgres?sslmode=require' \
  -X -v ON_ERROR_STOP=1 -f migrations/000_create_database.sql
```

如果使用独立应用账号，先创建账号并把数据库 owner 改为该账号：

```sql
ALTER DATABASE "nexus-authorization" OWNER TO "nexus-authorization";
```

准备 `.env.deploy`，至少填写：

```sh
NEXUSAUTHORIZATION_DATABASE_URL='postgres://nexus-authorization:CHANGE_ME@db.example.com:5432/nexus-authorization?sslmode=require'
NEXUSAUTHORIZATION_OIDC_ENABLED='true'
NEXUSAUTHORIZATION_OIDC_AUTHORITY='https://auth.example.com'
NEXUSAUTHORIZATION_OIDC_CLIENT_ID='permission.center'
NEXUSAUTHORIZATION_OIDC_CLIENT_SECRET='CHANGE_ME'
NEXUSAUTHORIZATION_OIDC_REDIRECT_URI='https://permissions.example.com/signin-oidc'
NEXUSAUTHORIZATION_OIDC_POST_LOGOUT_REDIRECT_URI='https://permissions.example.com/'
NEXUSAUTHORIZATION_SERVICE_RESOURCE_SOURCE='nexusauth'
NEXUSAUTHORIZATION_SERVICE_RESOURCE_NEXUSAUTH_BASE_URL='https://auth.example.com'
NEXUSAUTHORIZATION_SERVICE_RESOURCE_API_KEY='CHANGE_ME'
NEXUSAUTHORIZATION_SCIM_NEXUSAUTH_BASE_URL='https://auth.example.com'
NEXUSAUTHORIZATION_SCIM_API_KEY='CHANGE_ME'
```

以数据库 owner 身份执行完整初始化：

```sh
set -a
. ./.env.deploy
set +a
sh src/scripts/init-db.sh
```

`001_init.sql` 自带事务。空库只执行一次；不要把 `000_create_database.sql` 和 `001_init.sql` 放进同一事务，也不要对已经存在业务数据的库重复执行完整初始化。

## 3. Docker Compose 首次部署

当前 Compose 使用外部 `nexusauth` 网络、API 容器端口 `8080`、Dashboard 容器端口 `80`，默认映射到宿主机 `9101` 和 `5274`。先确认网络存在：

```sh
docker network inspect nexusauth >/dev/null 2>&1 || docker network create nexusauth
docker compose --env-file .env.deploy -f docker-compose.yml config -q
docker compose --env-file .env.deploy -f docker-compose.yml pull
docker compose --env-file .env.deploy -f docker-compose.yml up -d
```

检查服务状态和健康检查：

```sh
docker compose -f docker-compose.yml ps
curl -fsS http://127.0.0.1:9101/api/healthz
curl -I http://127.0.0.1:5274/
docker compose -f docker-compose.yml logs --tail=200 nexusauthorization-api dashboard
```

输出 `{"status":"ok"}` 只说明 API 进程可响应。登录、服务目录、SCIM 和运行时鉴权应在部署验收中单独验证。

## 4. 关键环境变量

| 变量 | 必填场景 | 说明 |
| --- | --- | --- |
| `NEXUSAUTHORIZATION_DATABASE_URL` | 始终 | PostgreSQL 连接串，密码中的特殊字符必须 URL 编码 |
| `NEXUSAUTHORIZATION_OIDC_ENABLED` | 需要登录时 | 开启管理端 OIDC |
| `NEXUSAUTHORIZATION_OIDC_*` | 开启 OIDC | Provider、客户端、回调、scope、audience 和 secret |
| `NEXUSAUTHORIZATION_SERVICE_RESOURCE_SOURCE` | 始终 | `local` 或 `nexusauth` |
| `NEXUSAUTHORIZATION_SERVICE_RESOURCE_NEXUSAUTH_BASE_URL` | nexusauth 模式 | 远程服务目录地址 |
| `NEXUSAUTHORIZATION_SERVICE_RESOURCE_API_KEY` | nexusauth 模式 | 服务目录读取凭证 |
| `NEXUSAUTHORIZATION_SCIM_*` | 需要分配用户 | NexusAuth SCIM 地址、只读凭证和超时 |
| `NEXUSAUTHORIZATION_REDIS_URL` | 可选 | 为空时使用 PostgreSQL-only 模式 |
| `NEXUSAUTHORIZATION_REDIS_KEY_PREFIX` | 使用 Redis | 多环境必须使用不同前缀 |
| `NEXUSAUTHORIZATION_REDIS_CACHE_TTL` | 使用 Redis | 当前策略记录 TTL，默认 24h |
| `NEXUSAUTHORIZATION_REDIS_TIMEOUT` | 使用 Redis | Redis 操作超时，默认 200ms |

配置加载顺序是镜像中的 YAML 默认值，再由环境变量覆盖。不要把 secret 写入 Dashboard 构建参数或前端静态文件。

## 5. 初始化完成后的核对

首次初始化完成后，先确认 API 能连接数据库并完成健康检查，再打开 Dashboard 做最小业务链路验证：

1. 创建一个测试业务应用；
2. 关联一个启用服务资源；
3. 新增一个 API 或导入一个 Swagger 文档；
4. 创建角色并授予 API；
5. 分配一个测试用户；
6. 用权限模拟验证一次允许和一次拒绝；
7. 删除测试角色和测试应用，确认清理规则符合预期。

数据库只由 PostgreSQL 作为权限事实来源。Redis 是可选缓存，首次部署可以先保持 `NEXUSAUTHORIZATION_REDIS_URL` 为空，待基础链路稳定后再启用。

## 6. 日常操作

启动、停止和重启：

```sh
docker compose -f docker-compose.yml start
docker compose -f docker-compose.yml stop
docker compose -f docker-compose.yml restart nexusauthorization-api
```

滚动发布单个镜像：

```sh
docker compose --env-file .env.deploy -f docker-compose.yml pull nexusauthorization-api
docker compose --env-file .env.deploy -f docker-compose.yml up -d nexusauthorization-api
docker compose -f docker-compose.yml ps
```

发布 Dashboard 时，应先确认新产物和 Nginx 配置，再更新容器。发布后验证登录回调、React 路由刷新、`/api/auth/me` 和 `/api/v1/applications`。

## 7. 备份与恢复

建议至少备份：

- PostgreSQL 全库，包括 schema、触发器、函数和权限数据；
- `/app/data` 中的 OIDC 会话密钥和运行时数据；
- Compose 文件、环境变量模板和实际镜像版本；
- Redis 不作为主备份来源，它可以清空并由 PostgreSQL 重建。

示例：

```sh
pg_dump "$NEXUSAUTHORIZATION_DATABASE_URL" \
  --format=custom --file=nexus-authorization-$(date +%Y%m%d).dump
```

恢复前停止 API，恢复到隔离数据库并核对表、触发器和应用数量；确认可用后再切换连接串。恢复后首次请求会按 `policy_token` 重新编译策略，Redis 中的旧记录可以删除。

## 8. 故障排查

| 现象 | 优先检查 |
| --- | --- |
| 容器反复重启 | `docker compose logs`；数据库 URL、服务目录 source、OIDC 必填项和配置校验错误 |
| `/api/healthz` 正常但页面打不开 | Dashboard 容器、宿主端口、Nginx 配置、反向代理和浏览器控制台 |
| 登录回调失败 | OIDC redirect URI 是否逐字符匹配、HTTPS/cookie、authority 是否可达、client secret |
| 服务资源列表失败 | `source`、远程 URL、API key、远程 TLS 和超时 |
| 分配用户失败 | SCIM URL、只读凭证、上游分页响应和 `NEXUSAUTHORIZATION_SCIM_TIMEOUT` |
| 权限保存成功但鉴权旧结果 | 检查事务是否提交、`policy_token` 是否变化、Redis key 前缀、数据库日志；清理 Redis 后重试 |
| 取消服务关联冲突 | 先从所有角色移除该服务的 API 授权，再取消关联 |
| 运行时接口返回 401 | 当前版本是否配置了 `authorization.service_credential`；管理端 OIDC cookie 不能代替服务凭证 |
| 运行时接口返回拒绝 | 核对 app、service、subject、domain、method、实际 path 和角色绑定，先用权限模拟复现 |

排障时优先保留第一条错误日志、请求的 app/service 标识、数据库迁移版本和镜像标签，避免只记录最后一条连锁错误。

## 9. 发布验收清单

```text
[ ] docker compose config -q 通过
[ ] API /api/healthz 返回 ok
[ ] Dashboard 首页和 React 路由刷新正常
[ ] OIDC 登录、回调、退出登录正常
[ ] 应用创建/选择正常
[ ] 服务目录读取正常
[ ] SCIM 用户列表和角色分配正常（若启用）
[ ] API、角色、菜单、用户绑定保存后立即生效
[ ] 权限模拟允许和拒绝各验证一次
[ ] 业务服务实际调用鉴权接口或 SDK 验证一次
[ ] 数据库备份和恢复演练记录完整
[ ] 日志无配置、连接池、迁移或远程目录错误
```

管理端的详细配置步骤见 [使用手册](user-guide.md)，数据库细节见 [数据库初始化](../migrations/README.md)。
