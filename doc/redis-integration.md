# Redis 服务 API 授权

服务 API 鉴权有两种运行模式，由 `redis.enabled` 选择，默认 `true`。启用模式使用 `github.com/redis/go-redis/v9`：业务 Hash 按服务、主体和归一化 domain 共享，应用通过 field 隔离；管理端保存业务表后，由服务同步重建受影响应用的 field 与内部元数据。鉴权请求只读 Redis，不读取数据库，也不回源。显式禁用后，服务 API 鉴权走数据库 current-policy 路径，不创建 Redis 客户端，也不发布 Redis 记录。数据库只保存业务配置，没有 `policy_token`、角色/服务 revision 或发布历史。保存生效与撤权规则见 [权限生效设计](permission-lifecycle.md)。

菜单和全策略适配器是独立域，始终走自己的 current-policy 路径：每次请求在一条 MVCC 语句里读取当前规范化业务表并编译，不读取本页描述的服务 API Redis 记录，也不依赖这些记录是否 ready。Redis 开关只控制服务 API 鉴权来源和 Redis 写入，不控制菜单。

## 启用开关

- `redis.enabled` 省略时默认为 `true`；显式 YAML `false` 或 `NEXUSAUTHORIZATION_REDIS_ENABLED=false` 选择数据库 current-policy API 鉴权。环境变量覆盖 YAML。
- 启用时必须设置非空 `redis.url`。缺失或格式错误会使应用启动失败；启动阶段不会 PING Redis。
- 启用后，连接失败、超时、应用 `state` 未就绪或 Hash field 损坏都直接拒绝，不会自动回退数据库；应用已发布但该主体没有 field（未绑定角色或服务未关联）返回正常 deny 决策 `endpoint_not_granted`。
- 禁用时不创建 Redis 客户端，也不在保存应用、服务、角色或绑定时发布 Redis。数据库 current-policy 路径仍读取当前业务表。
- 开关在进程启动时生效，修改后必须重启 API。启用模式下 API 启动时会在后台把所有应用完整发布一次（见下文「自动对账」），重新启用或 Redis 被清空后无需逐个保存应用。
- 该开关不增加数据库迁移或版本表，也没有发布版本、快照或回滚记录。

## key 标准与大小

本节仅适用于启用的 Redis 服务 API 鉴权模式。

### 业务 Hash

业务授权按 `service + subject + normalized domain` 组织，应用通过 Hash field 隔离：

```text
KEY   PREFIX:<QueryEscape(trim(service))>:<QueryEscape(trim(subject))>[:<QueryEscape(normalized domain)>]
TYPE  HASH
FIELD trim(app_key)
VALUE 原始 SubjectAPIPolicy JSON
```

`prefix` 由配置的 `key_prefix` 提供，默认开发环境为 `nexusauthorization:dev`，生产环境为 `nexusauthorization:prod`。配置前缀已经带有环境，业务 key 和内部元数据 key 都不再额外拼接环境段。`service` 与 `subject` 先去除首尾空白，再按 Go `url.QueryEscape` 编码；仅当归一化 domain 非空时追加经过同样编码的 domain 段。空（默认）domain 和旧 `global` 均归一为空 domain，不追加后缀；具名域保持独立。业务 key 中没有 app 段。

应用 key 先去除首尾空白，作为 field 名使用，保留原文，不做 QueryEscape。应用 key、subject、service 和 domain 的既有非空及业务校验不变。field value 直接保存普通 `SubjectAPIPolicy` JSON，没有信封、版本号、checksum 或新 JSON 外壳，读取后原样反序列化。内容字段：

- `application_key`、`application_id`、`subject`、`domain`、`service_resource`、`service_active`：本次聚合的身份；读取时与请求身份逐字段校验，不一致即拒绝。
- `apis`：该用户在该服务上由启用角色授予的去重 API 并集。
- `catalog`：该服务的完整 Catalog，保留未授权与 disabled 路由，避免路由回退越权。
- `roles`：命中的角色编码。

每个 Hash field value 的上限仍为 **16 MiB**，超过上限直接报错。Hash **不设 TTL**，永不过期；新鲜度由下面每个应用的写入闸门控制，而不是靠过期时间。Hash 中一个 field 缺失只表示该应用对该业务身份没有授权，其他应用的 field 不受影响。

### 应用辅助 key

```text
PREFIX:rbac:api:app:<QueryEscape(trim(app_key))>:state
PREFIX:rbac:api:app:<QueryEscape(trim(app_key))>:members
PREFIX:rbac:api:app:<QueryEscape(trim(app_key))>:lock
PREFIX:rbac:api:app:<QueryEscape(trim(app_key))>:requested
PREFIX:rbac:api:app:<QueryEscape(trim(app_key))>:published
```

- `state` 是应用写入闸门，取值为 `ready` 或 `updating`，同样不设 TTL。应用从未发布时该 key 不存在。
- `members` 是该应用上一次发布所包含的业务身份集合，供重建时找出需要为该应用 `HDEL` 的旧 field。
- `lock` 是重建租约，值为随机 token，带 2 分钟过期（`SET NX PX`），同一时刻只允许一个重建写该应用。
- `requested` 是计数器，每次数据库保存提交后递增；`published` 是当前应用已包含的最大请求号。

以上内部元数据 key 中的应用 key 同样先去除首尾空白，再经 `url.QueryEscape` 编码。旧的 `PREFIX:<escaped app_key>:state/members` 不再是本协议的一部分。`prefix` 不能含空白、花括号或以冒号结尾。当前使用 standalone Client，未配置 Sentinel/Cluster 客户端。**不支持 Redis Cluster**：读取和重建的 Lua 脚本同时操作应用闸门、租约和目标业务 Hash，在 Cluster 下可能跨 slot；请使用单实例或主从（含托管的单分片 Redis）。

## 读取语义

Redis 模式下，鉴权入口（`POST /api/v1/authorize` 和 `POST /api/v1/rbac-v1/simulate`）先用请求应用定位内部 readiness，再用 `service + subject + domain` 定位业务 Hash，并通过应用 field 读取该应用的策略。一次 Lua 脚本完成 `GET` 内部 `state` 与 `HGET` 目标 field，不经过数据库，也没有回源路径。脚本返回码保持为 `1`、`2`、`0`、`-1`：

1. `1`：field 存在且返回 `SubjectAPIPolicy`。校验 field 身份与请求一致、JSON 合法且不超过 16 MiB，再按内容哈希复用进程内已编译索引（上限 1024，同一内容并发加载用 30 秒 singleflight 合并），匹配 `service_resource + method + path`。
2. `2`：应用 `state = ready`，但业务 Hash 中该应用 field 不存在。该主体在这个服务上对该应用没有任何授权（未绑定角色，或服务未关联到应用），返回正常 deny 决策（HTTP 200，`reason=endpoint_not_granted`），与数据库模式一致。
3. `0`：应用 `state` 不存在，例如应用从未发布、Redis 刚被清空且尚未对账，或应用不存在。拒绝为未发布，不读取 field。
4. `-1`：应用 `state` 存在但值不是 `ready`，例如 `updating`。重建期间直接拒绝，不读取可能仍在变化的 field。

连接失败、超时、field 损坏或身份不匹配同样报错并拒绝；返回码之外的 Redis 错误由调用方失败关闭。这些情形都不会读取数据库放行。Redis 模式启动时已经要求 `redis.url` 非空；运行中不可用或未就绪时，服务 API 鉴权直接拒绝，这是设计要求。菜单路径不受影响，见下。禁用 Redis 时不会进入本节流程，服务 API 改走数据库 current-policy 路径。

应用停用时，Redis 模式按 field value 中的 `service_active=false` 返回正常 deny 决策（`reason=service_inactive`）；数据库模式找不到启用应用，返回 404。共享业务 Hash 中其他应用的 field 不参与本次读取。

## 写入与重建

Redis 模式下，管理端保存业务表并提交后，控制面在同一请求内重建受影响应用的 field 与内部元数据：

1. 递增应用的 `requested` 计数，得到本次保存的请求号。
2. 获取应用的重建租约（`lock`，`SET NX PX`）。另一个重建正在进行时等待，最长约 90 秒。
3. 拿到租约后读取 `requested` 作为本次重建的「视界」；若 `published` 已不小于本次请求号且闸门 `ready`，说明别的重建已经包含了这次保存，直接返回。
4. 否则在 `REPEATABLE READ` 只读事务中读取数据库快照，服务目录只读取一次；目录读取失败则中止本次重建、保留旧记录，不会把服务误写成停用。
5. 置 `updating`，依据旧 `members` 对业务 Hash 分批 `HDEL` 该应用中已被移除或不再属于新快照的 field，再分批 `HSET` 该应用的新 field（每批最多 64 条，受 `timeout` 限制，整个替换受 30 秒 deadline 约束），最后把内部 `state` 置为 `ready` 并把 `published` 设为视界。
6. 释放租约。

每一步写入（闸门、删除、写入、开闸）都在 Lua 脚本中先校验租约 token：持有者暂停过久、租约已过期并被他人接手时，旧持有者的任何写入都会被拒绝，因此**基于旧快照的重建不可能覆盖较新的发布**（例如让刚撤销的授权重新生效）。数据库快照在获得租约之后才读取，所以租约之前提交的保存一定包含在快照里。

重建与 HTTP 请求的取消解耦：管理员关闭页面或连接中断不会打断已经开始的重建。重建失败时数据库仍已提交，接口返回 `503 PUBLISH_PENDING`（「配置已保存，但授权记录发布失败，后台会自动重试」），由下文的自动对账补发布。

发布只按当前应用操作其 field：`HDEL` 撤权或不再属于当前应用的 field，`HSET` 新增或更新的 field，不会删除其他应用在同一 Hash 中的 field。删除先于写入，且闸门只在全部 field 落盘后才打开，因此已离开的用户不会在闸门重开后仍被该应用寻址。触发重建的写操作包括：保存应用、替换应用服务关联、保存/删除角色、替换主体角色、按角色增删主体、保存/删除 API 端点（Swagger 导入整批只重建一次）、创建/更新/删除服务资源。服务目录变更会重建所有关联该服务的应用。

## 自动对账

启用模式下，API 进程在后台运行对账器：

- **启动时**把所有应用完整发布一次，覆盖首次部署、Redis 清空、以及停机期间远程服务目录发生的变化；失败会在下一个周期继续重试完整发布。
- **之后每隔 `reconcile_interval`（默认 30 秒）**只处理需要的应用：闸门缺失或停在 `updating`、`published` 落后于 `requested`（某次保存的重建失败）、本进程记录的发布失败（包括 Redis 当时连计数都写不进去的情况），以及服务目录状态（存在与启用）相对上一轮发生变化的服务所关联的应用。远程 NexusAuth 目录中服务被停用或删除，无需任何本地保存即可在一个周期内生效。

多实例部署时每个实例都运行对账器；租约与请求号保证同一应用不会被并发重建，已被其他实例覆盖的请求会直接跳过。

菜单 token 不参与服务 API 记录；纯菜单保存不改变服务 API 记录。菜单与全策略适配器仍按应用读取当前规范化业务表并在进程内编译，属于独立的 current-policy 域。Redis 禁用时，上述保存流程不执行任何 Redis 发布。

## 协议切换与部署

新版本只读业务 Hash 与 `PREFIX:rbac:api:app:*` 元数据，旧 String 协议不自动迁移，也不提供自动回退。本次代码修改未访问在线 Redis；部署后由新版中心按既有启动发布与后台对账机制，从 PostgreSQL 自动重建应用元数据、新业务 Hash field，并生成新的 `state`。

已有环境必须把旧的自然语言前缀值从 `nexusauthorization-dev` / `nexusauthorization-prod` 改为带冒号的 `nexusauthorization:dev` / `nexusauthorization:prod`。这包括中心 YAML 的 `redis.key_prefix`、环境变量 `NEXUSAUTHORIZATION_REDIS_KEY_PREFIX`、Compose 注入值和所有部署环境覆盖。Demo 的 `Redis:KeyPrefix` 与容器环境变量 `Redis__KeyPrefix` 也必须使用同一个新值。配置值是完整的 `PREFIX`，不要写成 `nexusauthorization:prod:prod` 或 `nexusauthorization:prod:dev`。

如果新业务 Hash key 与旧同名 String key 冲突，Redis 返回 `WRONGTYPE`，新读端应保持失败关闭并拒绝。先核对精确 key、旧读端状态和影响范围，再定向处理冲突；应用不会自动删除旧 key，此时也可以选择新的环境前缀重新部署。

切换前先统一配置环境前缀，并在维护窗口停止旧中心与旧消费者的鉴权流量，再依次执行：

1. 先部署新版中心，确认目标应用的新 `state` 已为 `ready`，并确认共享业务 Hash 中已有对应应用 field。重建完成前，新读协议会以 `0` 或 `-1` 拒绝，这是预期失败关闭窗口。
2. 再部署 Demo 或其他新协议消费者，并在对外提供鉴权前执行验收。旧中心、旧 Demo 与旧 SDK 读端不能与新版中心同时对外承担鉴权：旧读端仍读旧 String key，新版发布不会再更新这些 key，旧授权或已撤销权限可能继续生效。
3. 若无法安排 consumer 同步升级，应先停止旧消费者或进入维护窗口，再完成中心与新读端的切换。不要依赖带外手工同步旧 key 来维持混合版本。
4. 回退同样需要停止流量并恢复整套匹配版本；不能只回退发布逻辑，因为新协议变更会在新 field 与 `state` 进入 `ready` 前短暂拒绝。旧 String 协议不会自动恢复或迁移。
5. 只有在新 Hash/state 已生成、所有旧读端确认停止后，才可按精确匹配定向清理该环境的旧 String key。不得使用 `FLUSHDB`、整库删除、全库 scan-and-delete 或清空共享前缀来代替协议迁移。

旧 key 清理不属于应用启动或对账流程；本仓库的部署步骤不会自动删除在线 Redis 中的任何旧 key。定向清理前应先在非生产环境核对环境前缀、旧 key 形状和影响范围，并保留可审计的执行记录。

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

默认前缀已经包含环境，因此 `key_prefix` 直接使用 `nexusauthorization:dev` 或 `nexusauthorization:prod`，不要再追加第二段 `dev`/`prod`。数据库 current-policy API 模式可设置为：

```yaml
redis:
  enabled: false
```

或：

```sh
export NEXUSAUTHORIZATION_REDIS_ENABLED='false'
```

禁用时 `redis.url`、`key_prefix`、`timeout` 和 `reconcile_interval` 不参与 Redis 连接或发布，也不需要配置为有效值。`key_prefix` 决定启用模式的 `PREFIX`，代码默认值为 `nexusauthorization:dev`；Compose 的开发与 production 默认值分别为 `nexusauthorization:dev` 与 `nexusauthorization:prod`。`timeout` 限制单次 Redis 网络操作（默认 200 ms，至少 1 ms），同时约束重建的每一批与鉴权读取。`reconcile_interval` 是后台对账周期（默认 30 秒，至少 1 秒）。`cache_ttl` / `NEXUSAUTHORIZATION_REDIS_CACHE_TTL` 仅为兼容旧配置而保留，已不再使用：服务 API Hash 不设 TTL。旧 `snapshot_ttl` / `NEXUSAUTHORIZATION_REDIS_SNAPSHOT_TTL` 已移除。支持 redis://、rediss://、用户名、密码和 DB 编号，凭据特殊字符需 URL 编码。应用启动时要求启用的 `redis.url` 非空，但不会在启动阶段强制 PING；连接失败会在实际重建或鉴权时暴露。

## 验证

本节列出协议目标用例。旧 String 协议时期的测试结果不能作为新 Hash 协议在现网已经通过的证明；实际验证范围应以包含新 key、field、readiness、跨应用隔离和迁移失败语义的测试记录为准。

```sh
cd src
go test -race ./...
# 测试库必须独立，先执行 001_init.sql；没有设置时数据库集成测试跳过。
NEXUSAUTHORIZATION_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/nexusauthorization_test?sslmode=disable' \
  go test ./internal/data/db/repo -run 'TestSubjectAPI|TestRedis|TestRemote' -count=1
```
