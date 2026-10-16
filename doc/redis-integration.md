# Redis 服务 API 授权

使用 `github.com/redis/go-redis/v9`。运行时服务 API 鉴权直接以 Redis 为授权来源：管理端保存业务表后，由服务同步重建受影响的 Redis 记录；鉴权请求只读这些记录，不读取数据库，也不回源。数据库只保存业务配置，没有 `policy_token`、角色/服务 revision 或发布历史。保存生效与撤权规则见 [权限生效设计](permission-lifecycle.md)。

菜单和全策略适配器是独立域，走自己的 current-policy 路径：每次请求在一条 MVCC 语句里读取当前规范化业务表并编译，不读取本页描述的服务 API Redis 记录，也不依赖这些记录是否 ready。因此 Redis 记录只服务服务 API 鉴权入口。

## key 标准与大小

### 授权记录

每个「应用 key + 主体 + 服务 + 归一化 domain」是一条 Redis String：

```text
PREFIX:rbac:api:app:<QUERY_ESCAPE(app_key)>:user:<QUERY_ESCAPE(subject)>:service:<QUERY_ESCAPE(service)>:domain:<QUERY_ESCAPE(normalized domain)>
```

应用 key、主体、服务和 domain 各自经 `url.QueryEscape` 编码，避免与结构分隔符冲突。空（默认）domain 保留字面空尾；旧 `global` 归一到同一空默认域，具名域各自独立。作用域选择器是应用 key，不是应用 UUID。

值是普通 `SubjectAPIPolicy` JSON，没有信封、版本号或 checksum，读取后原样反序列化。内容字段：

- `application_key`、`application_id`、`subject`、`domain`、`service_resource`、`service_active`：本次聚合的身份；读取时与请求身份逐字段校验，不一致即拒绝。
- `apis`：该用户在该服务上由启用角色授予的去重 API 并集。
- `catalog`：该服务的完整 Catalog，保留未授权与 disabled 路由，避免路由回退越权。
- `roles`：命中的角色编码。

单条记录上限 **16 MiB**，写入超过上限直接报错。记录**不设 TTL**，永不过期；新鲜度由下面每个应用的写入闸门控制，而不是靠过期时间。

### 应用辅助 key

```text
PREFIX:rbac:api:app:<QUERY_ESCAPE(app_key)>:state
PREFIX:rbac:api:app:<QUERY_ESCAPE(app_key)>:members
```

- `state` 是应用写入闸门，取值为 `ready` 或 `updating`，同样不设 TTL。应用从未发布时该 key 不存在。
- `members` 是该应用全部授权记录 key 的集合（SET），供批量替换时找出并删除被取代的旧记录。

`PREFIX` 由配置的 `key_prefix` 提供，用于环境隔离，例如 `nexusauthorization:prod`。前缀不能含空白、花括号或以冒号结尾。当前使用 standalone Client，未配置 Sentinel/Cluster 客户端。

## 读取语义

鉴权入口（`POST /api/v1/authorize` 和 `POST /api/v1/rbac-v1/simulate`）每次请求都直接从 Redis 读取该「应用 key + 主体 + 服务 + 归一化 domain」的记录，不经过数据库，也没有回源路径。一次 Lua 脚本在同一步内读取应用的 `state` 与记录：

1. `state = ready` 且记录存在：校验记录身份与请求一致、JSON 合法且不超过 16 MiB，再按内容哈希复用进程内已编译索引（上限 1024，同一内容并发加载用 30 秒 singleflight 合并），匹配 `service_resource + method + path`。
2. `state = ready` 但该身份没有记录：普通未命中，拒绝。
3. 应用从未发布（`state` 不存在）：未命中，拒绝。
4. `state = updating`：应用正在批量替换，直接拒绝，不返回可能半写的记录。
5. Redis 未配置、连接失败、超时、记录损坏或身份不匹配：报错并拒绝。

这些情形都不会读取数据库放行。Redis 未配置或不可用时，服务 API 鉴权没有可读授权，因此直接拒绝；这是设计要求。菜单路径不受影响，见下。

## 写入与重建

管理端保存业务表后，控制面在同一进程内重建受影响应用的 Redis 记录，而不是异步等待：

1. 先把受影响应用置为 `updating`（`BlockSubjectAPIApplications`），使并发鉴权在替换期间被拒绝。
2. 读取旧 `members`，删除本次不再存在的记录，再分批写入新记录（写授权记录并 `SADD members`）。每批最多 64 条，受配置的 `timeout` 限制，整个替换受 30 秒 deadline 约束。
3. 全部写完后才把 `state` 置为 `ready`。任何一步失败都保持 `updating`，使下一次重建从干净状态重试，期间鉴权持续拒绝。

删除先于写入，且闸门只在全部记录落盘后才打开，因此已离开的用户不会在闸门重开后仍被寻址。触发重建的写操作包括：保存应用、替换应用服务关联、保存/删除角色、替换主体角色、保存/删除 API 端点、创建/更新/删除服务资源。服务目录变更会重建所有关联该服务的应用。

菜单 token 不参与服务 API 记录；纯菜单保存不改变服务 API 记录。菜单与全策略适配器仍按应用读取当前规范化业务表并在进程内编译，属于独立的 current-policy 域。

## 配置

Redis 是服务 API 鉴权的运行时依赖：`redis.url` 为空时应用启动失败，避免业务配置已经保存但服务 API 授权记录没有生成。菜单 current-policy 路径仍按数据库工作，但不会绕过这个启动约束。

```yaml
redis:
  url: "redis://localhost:6379/0"
  key_prefix: "nexusauthorization:dev"
  cache_ttl: "24h"
  timeout: "200ms"
```

```sh
export NEXUSAUTHORIZATION_REDIS_URL='redis://localhost:6379/0'
export NEXUSAUTHORIZATION_REDIS_KEY_PREFIX='nexusauthorization:dev'
export NEXUSAUTHORIZATION_REDIS_CACHE_TTL='24h'
export NEXUSAUTHORIZATION_REDIS_TIMEOUT='200ms'
```

`key_prefix` 决定 key 的 `PREFIX`。`timeout` 限制单次 Redis 网络操作（默认 200 ms，至少 1 ms），同时约束重建的每一批与鉴权读取。`cache_ttl` 是配置层保留的当前记录缓存 TTL，默认 24h；本页描述的服务 API 授权记录写入时不设 TTL，不受该值影响。旧 `snapshot_ttl` / `NEXUSAUTHORIZATION_REDIS_SNAPSHOT_TTL` 已移除。支持 redis://、rediss://、用户名、密码和 DB 编号，凭据特殊字符需 URL 编码。应用启动时要求 `redis.url` 非空，但不会在启动阶段强制 PING；连接失败会在实际重建或鉴权时暴露。

## 验证

miniredis 测试覆盖服务 API 记录的读写、应用与身份隔离、闸门 `updating` 拒绝、批量替换的删除先于写入、旧记录清理、大小上限、取消和错误路径。数据库集成测试验证保存业务表后重建 Redis、撤权生效、应用禁用和服务目录变更触发重建。

```sh
cd src
go test -race ./...
# 测试库必须独立，先执行 001_init.sql；没有设置时数据库集成测试跳过。
NEXUSAUTHORIZATION_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/nexusauthorization_test?sslmode=disable' \
  go test ./internal/data/db/repo -run 'TestSubjectAPI|TestRedis' -count=1
```
