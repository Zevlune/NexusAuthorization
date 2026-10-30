# 保存即生效的权限设计

应用仍是配置、角色和绑定的编辑边界。服务运行时按用户、服务和 domain 读取合并后的策略：所有已启用且关联该服务的应用贡献权限，API 与角色取并集，不会由一个应用覆盖另一个应用。具名 domain 保持隔离。默认域是空串，`global` 是同一默认域的兼容别名。

## 运行时行为

`POST /api/v1/authorize` 的请求需要 `appKey`、`serviceResource`、`subject`、`method` 和 `path`，`domain` 可选。`appKey` 仍是请求上下文，但业务策略不含应用身份，也不按应用切分 Redis 记录。身份必须来自可信登录态，客户端不传角色。

服务 API 鉴权默认使用 Redis。启用时，中心把一个用户、一个服务和一个归一化 domain 的完整策略写为 String；运行时直接 `GET` 对应业务 key，严格校验 `subject`、`service_resource`、`domain`、`service_active`、JSON 类型和大小，再按 method/path 匹配 `catalog` 与 `apis`。不读取 `state`、Lua、`rbac:api:app:*` 或其他旧辅助 key。

String 缺失、类型错误、JSON 损坏、身份不匹配、超过 16 MiB、超时或 Redis 错误都拒绝；旧应用级 JSON 不作为 fallback。显式设置 `redis.enabled: false` 或 `NEXUSAUTHORIZATION_REDIS_ENABLED=false` 时，不创建 Redis 客户端，服务 API 和权限模拟使用数据库 current-policy。

主体菜单查询直接在一个数据库快照中读取应用、菜单、角色菜单授权和主体绑定，不访问远程服务目录或服务 API Redis 记录。全策略适配器仍走独立的 current-policy 路径。

## 保存与发布

数据库是业务配置的唯一事实来源，只保存应用、服务、角色、绑定、菜单和 API，不保存 `policy_token`、发布版本、请求号或历史快照。每次影响服务 API 权限的控制面保存后全量重算；纯菜单编辑继续由独立 current-policy 路径读取，不触发 Redis 发布。

1. 在 PostgreSQL session advisory lock 下取得同一个 `REPEATABLE READ` 快照。锁先于快照取得，并保持到 Redis 发布结束，使多个中心串行使用各自完整快照发布。
2. 从该快照合并所有应发布的用户、服务和 domain 记录。
3. Redis 层先验证完整输出；全部通过后，再按专用业务前缀 `SCAN`、严格识别业务 key 形状并删除本轮快照中已不存在的旧用户记录。
4. 对仍存在的每个用户 `SET` 完整 JSON，覆盖同名旧值或旧 Hash；不设 TTL，单值上限 16 MiB。

环境级或应用批次没有整体原子性。每条用户记录的 `SET` 原子，但发布部分失败时，某些用户或应用来源可能保留旧权限，直到下一轮全量对账补发；保存返回 `503 PUBLISH_PENDING`。因此文档不承诺撤权零窗口，也不把普通 Redis 读失败描述成由闸门保证的强 fail-closed。

应用停用、移除服务关联、撤销角色或删除 API 并成功发布后，中心只从合并结果中移除该来源的贡献，其他启用应用仍提供的权限保留。具名 domain 只影响自己的业务 key。发布过程没有应用级总闸门，不能用它承诺所有请求在替换期间都强一致拒绝。

## 对账

启动时和每次 `reconcile_interval`（默认 30 秒）都会从 PostgreSQL 全量重算并替换全部业务记录。对账不再依赖 `requested` / `published` 增量进度，也不需要新数据库表或 migration、Redis 锁、索引或计数器。发布失败由下一轮全量对账修正。

## 协议切换

旧 Hash/field/state/Lua 与 `rbac:api:app:*` 元数据不是新协议的读取依赖，也无需继续写入。切换时应在维护窗口内执行：

1. 根据中心必填的 `LUCKENV` 计算前缀 `nexusauthorization:<QueryEscape(trim(LUCKENV))>`。
2. 将 Demo 或其他消费者的 `KeyPrefix` 配置为该完整前缀，并使用各自登记的服务资源标识。
3. 停止旧 Hash reader/writer 的鉴权流量。
4. 启动新版中心，让全量重建以 String 写入推导前缀下的业务 key。
5. 启动新版 Demo 或其他新 reader，验收允许、拒绝、缺失、类型/JSON 损坏和身份不匹配。
6. 确认旧 reader 全部停止后，再按旧 key 形状定向清理。

新旧 Hash/String reader 或 writer 不能混跑。旧 reader 看不到新版撤权，旧应用级 JSON 值也不能被新 reader 作为兼容值接受。本次文档同步未连接现网 Redis、未清理旧 key、未部署服务；详细命令边界见 [Redis 接入说明](redis-integration.md#协议切换)。

`LUCKENV` 变化会改变前缀和所有业务 key。已有实例切换前缀时，中心必须重新全量发布，所有消费者必须同步更新 `KeyPrefix` 并重启；旧前缀记录不会自动映射，未同步的一侧会因 key 缺失而 fail closed。中心不再接受 `NEXUSAUTHORIZATION_REDIS_KEY_PREFIX` 作为前缀覆盖。
