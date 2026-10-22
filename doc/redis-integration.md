# Redis 服务 API 授权

服务 API 鉴权有两种运行模式，由 `redis.enabled` 选择，默认 `true`。启用模式使用 `github.com/redis/go-redis/v9`：管理端保存业务表后，由服务同步重建受影响的 Redis 记录；鉴权请求只读这些记录，不读取数据库，也不回源。显式禁用后，服务 API 鉴权走数据库 current-policy 路径，不创建 Redis 客户端，也不发布 Redis 记录。数据库只保存业务配置，没有 `policy_token`、角色/服务 revision 或发布历史。保存生效与撤权规则见 [权限生效设计](permission-lifecycle.md)。

菜单和全策略适配器是独立域，始终走自己的 current-policy 路径：每次请求在一条 MVCC 语句里读取当前规范化业务表并编译，不读取本页描述的服务 API Redis 记录，也不依赖这些记录是否 ready。Redis 开关只控制服务 API 鉴权来源和 Redis 写入，不控制菜单。

## 启用开关

- `redis.enabled` 省略时默认为 `true`；显式 YAML `false` 或 `NEXUSAUTHORIZATION_REDIS_ENABLED=false` 选择数据库 current-policy API 鉴权。环境变量覆盖 YAML。
- 启用时必须设置非空 `redis.url`。缺失或格式错误会使应用启动失败；启动阶段不会 PING Redis。
- 启用后，连接失败、超时、`state` 未就绪或记录损坏都直接拒绝，不会自动回退数据库；应用已发布但该主体没有记录（未绑定角色或服务未关联）返回正常 deny 决策 `endpoint_not_granted`。
- 禁用时不创建 Redis 客户端，也不在保存应用、服务、角色或绑定时发布 Redis。数据库 current-policy 路径仍读取当前业务表。
- 开关在进程启动时生效，修改后必须重启 API。启用模式下 API 启动时会在后台把所有应用完整发布一次（见下文「自动对账」），重新启用或 Redis 被清空后无需逐个保存应用。
- 该开关不增加数据库迁移或版本表，也没有发布版本、快照或回滚记录。

## key 标准与大小

本节仅适用于启用的 Redis 服务 API 鉴权模式。

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
- `lock` 是重建租约，值为随机 token，带 2 分钟过期（`SET NX PX`），同一时刻只允许一个重建写该应用。
- `requested` 是计数器，每次数据库保存提交后递增；`published` 是当前记录已包含的最大请求号。

```text
PREFIX:rbac:api:app:<QUERY_ESCAPE(app_key)>:lock
PREFIX:rbac:api:app:<QUERY_ESCAPE(app_key)>:requested
PREFIX:rbac:api:app:<QUERY_ESCAPE(app_key)>:published
```

`PREFIX` 由配置的 `key_prefix` 提供，用于环境隔离，例如 `nexusauthorization:prod`。前缀不能含空白、花括号或以冒号结尾。当前使用 standalone Client，未配置 Sentinel/Cluster 客户端。**不支持 Redis Cluster**：读取和重建的 Lua 脚本同时操作闸门、租约与记录等多个 key，在 Cluster 下会跨 slot；请使用单实例或主从（含托管的单分片 Redis）。

## 读取语义

Redis 模式下，鉴权入口（`POST /api/v1/authorize` 和 `POST /api/v1/rbac-v1/simulate`）每次请求都直接从 Redis 读取该「应用 key + 主体 + 服务 + 归一化 domain」的记录，不经过数据库，也没有回源路径。一次 Lua 脚本在同一步内读取应用的 `state` 与记录：

1. `state = ready` 且记录存在：校验记录身份与请求一致、JSON 合法且不超过 16 MiB，再按内容哈希复用进程内已编译索引（上限 1024，同一内容并发加载用 30 秒 singleflight 合并），匹配 `service_resource + method + path`。
2. `state = ready` 但该身份没有记录：该主体在这个服务上没有任何授权（未绑定角色，或服务未关联到应用），返回正常 deny 决策（HTTP 200，`reason=endpoint_not_granted`），与数据库模式一致。
3. 应用从未发布（`state` 不存在，例如应用不存在、Redis 刚被清空且尚未对账）：返回 404 错误并拒绝。
4. `state = updating`：应用正在批量替换，直接拒绝，不返回可能半写的记录。
5. 连接失败、超时、记录损坏或身份不匹配：报错并拒绝。

这些情形都不会读取数据库放行。Redis 模式启动时已经要求 `redis.url` 非空；运行中不可用或未就绪时，服务 API 鉴权直接拒绝，这是设计要求。菜单路径不受影响，见下。禁用 Redis 时不会进入本节流程，服务 API 改走数据库 current-policy 路径。

应用停用时，Redis 模式按记录中的 `service_active=false` 返回正常 deny 决策（`reason=service_inactive`）；数据库模式找不到启用应用，返回 404。

## 写入与重建

Redis 模式下，管理端保存业务表并提交后，控制面在同一请求内重建受影响应用的 Redis 记录：

1. 递增应用的 `requested` 计数，得到本次保存的请求号。
2. 获取应用的重建租约（`lock`，`SET NX PX`）。另一个重建正在进行时等待，最长约 90 秒。
3. 拿到租约后读取 `requested` 作为本次重建的「视界」；若 `published` 已不小于本次请求号且闸门 `ready`，说明别的重建已经包含了这次保存，直接返回。
4. 否则在 `REPEATABLE READ` 只读事务中读取数据库快照，服务目录只读取一次；目录读取失败则中止本次重建、保留旧记录，不会把服务误写成停用。
5. 置 `updating`、删除不再存在的记录、分批写入新记录（每批最多 64 条，受 `timeout` 限制，整个替换受 30 秒 deadline 约束），最后把 `state` 置为 `ready` 并把 `published` 设为视界。
6. 释放租约。

每一步写入（闸门、删除、写入、开闸）都在 Lua 脚本中先校验租约 token：持有者暂停过久、租约已过期并被他人接手时，旧持有者的任何写入都会被拒绝，因此**基于旧快照的重建不可能覆盖较新的发布**（例如让刚撤销的授权重新生效）。数据库快照在获得租约之后才读取，所以租约之前提交的保存一定包含在快照里。

重建与 HTTP 请求的取消解耦：管理员关闭页面或连接中断不会打断已经开始的重建。重建失败时数据库仍已提交，接口返回 `503 PUBLISH_PENDING`（「配置已保存，但授权记录发布失败，后台会自动重试」），由下文的自动对账补发布。

删除先于写入，且闸门只在全部记录落盘后才打开，因此已离开的用户不会在闸门重开后仍被寻址。触发重建的写操作包括：保存应用、替换应用服务关联、保存/删除角色、替换主体角色、按角色增删主体、保存/删除 API 端点（Swagger 导入整批只重建一次）、创建/更新/删除服务资源。服务目录变更会重建所有关联该服务的应用。

## 自动对账

启用模式下，API 进程在后台运行对账器：

- **启动时**把所有应用完整发布一次，覆盖首次部署、Redis 清空、以及停机期间远程服务目录发生的变化；失败会在下一个周期继续重试完整发布。
- **之后每隔 `reconcile_interval`（默认 30 秒）**只处理需要的应用：闸门缺失或停在 `updating`、`published` 落后于 `requested`（某次保存的重建失败）、本进程记录的发布失败（包括 Redis 当时连计数都写不进去的情况），以及服务目录状态（存在与启用）相对上一轮发生变化的服务所关联的应用。远程 NexusAuth 目录中服务被停用或删除，无需任何本地保存即可在一个周期内生效。

多实例部署时每个实例都运行对账器；租约与请求号保证同一应用不会被并发重建，已被其他实例覆盖的请求会直接跳过。

菜单 token 不参与服务 API 记录；纯菜单保存不改变服务 API 记录。菜单与全策略适配器仍按应用读取当前规范化业务表并在进程内编译，属于独立的 current-policy 域。Redis 禁用时，上述保存流程不执行任何 Redis 发布。

## 配置

默认的 Redis 模式如下：

```yaml
redis:
  enabled: true
  url: "redis://localhost:6379/0"
  key_prefix: "nexusauthorization:dev"
  timeout: "200ms"
  reconcile_interval: "30s"
```

```sh
export NEXUSAUTHORIZATION_REDIS_ENABLED='true'
export NEXUSAUTHORIZATION_REDIS_URL='redis://localhost:6379/0'
export NEXUSAUTHORIZATION_REDIS_KEY_PREFIX='nexusauthorization:dev'
export NEXUSAUTHORIZATION_REDIS_TIMEOUT='200ms'
export NEXUSAUTHORIZATION_REDIS_RECONCILE_INTERVAL='30s'
```

数据库 current-policy API 模式可设置为：

```yaml
redis:
  enabled: false
```

或：

```sh
export NEXUSAUTHORIZATION_REDIS_ENABLED='false'
```

禁用时 `redis.url`、`key_prefix`、`timeout` 和 `reconcile_interval` 不参与 Redis 连接或发布，也不需要配置为有效值。`key_prefix` 决定启用模式的 key `PREFIX`。`timeout` 限制单次 Redis 网络操作（默认 200 ms，至少 1 ms），同时约束重建的每一批与鉴权读取。`reconcile_interval` 是后台对账周期（默认 30 秒，至少 1 秒）。`cache_ttl` / `NEXUSAUTHORIZATION_REDIS_CACHE_TTL` 仅为兼容旧配置而保留，已不再使用：服务 API 授权记录不设 TTL。旧 `snapshot_ttl` / `NEXUSAUTHORIZATION_REDIS_SNAPSHOT_TTL` 已移除。支持 redis://、rediss://、用户名、密码和 DB 编号，凭据特殊字符需 URL 编码。应用启动时要求启用的 `redis.url` 非空，但不会在启动阶段强制 PING；连接失败会在实际重建或鉴权时暴露。

## 验证

miniredis 测试覆盖服务 API 记录的读写、应用与身份隔离、闸门 `updating` 拒绝、批量替换的删除先于写入、旧记录清理、大小上限、租约互斥、租约丢失后写入被拒、请求号与发布号、取消和错误路径。数据库集成测试验证保存业务表后重建 Redis、撤权生效、应用禁用、服务目录变更（含远程目录变化）触发重建、并发保存最终收敛、基于旧快照的重建不能覆盖撤权，以及对账修复发布失败、Redis 清空和卡在 `updating` 的闸门。

```sh
cd src
go test -race ./...
# 测试库必须独立，先执行 001_init.sql；没有设置时数据库集成测试跳过。
NEXUSAUTHORIZATION_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/nexusauthorization_test?sslmode=disable' \
  go test ./internal/data/db/repo -run 'TestSubjectAPI|TestRedis|TestRemote' -count=1
```
