# Redis 服务 API 授权

服务 API 鉴权由 `redis.enabled` 选择来源，默认 `true`。启用时，运行时直接读取 Redis String，不访问数据库、旧应用级 JSON、Lua 或其他 RBAC 辅助 key；显式禁用时使用数据库 current-policy。菜单与全策略适配器始终走独立的 current-policy 路径。

## Key 与值

中心的 Redis 前缀不是独立配置项，而是启动时由必填的 `LUCKENV` 推导：

```text
nexusauthorization:<QueryEscape(trim(LUCKENV))>
```

`LUCKENV` 先 `trim`，再按 Go `url.QueryEscape` 编码。中心为各个服务发布记录；管理接口使用自身 `SERVICEUK` 读取对应服务策略。中心不再接受 `NEXUSAUTHORIZATION_REDIS_KEY_PREFIX`；SDK、Demo 等消费者仍使用各自的 `KeyPrefix` 配置显式指定读取前缀，该值必须与中心推导结果完全相同。

```text
nexusauthorization:<QueryEscape(trim(LUCKENV))>:<QueryEscape(trim(service))>:<QueryEscape(trim(subject))>[:<QueryEscape(normalizedDomain)>]
```

- 类型为 Redis **STRING**，值为一个用户、一个服务和归一化 domain 的完整策略 JSON。
- `service` 和 `subject` 先 `trim`，再按 Go `url.QueryEscape` 编码；消费者使用自身服务资源标识。
- domain 先 `trim`；空串和兼容值 `global` 归一为空域且不追加后缀。具名 domain 经 `QueryEscape` 后追加，各具名 domain 使用不同 key，不能互相取得权限。
- JSON 字段为 `subject`、`domain`、`service_resource`、`service_active`、`catalog`、`apis` 和 `roles`。
- JSON 不含 `application_key` 或 `application_id`。权限由所有关联该服务、已启用应用的授权合并得出；多个应用对同一用户、服务和 domain 的 API 与角色取并集，不会由某个应用覆盖其他应用。
- `catalog` 是该服务的完整接口目录；`apis` 是该用户由启用角色获得的去重 API 并集；`roles` 是命中的角色编码并集。
- 单值上限为 **16 MiB**，key 不设 TTL。

这是共享服务维度的业务记录，不是应用级记录。应用停用、移除服务关联或撤销权限后，中心重新合并所有剩余来源；其他仍在提供权限的应用不受影响。

## 读取语义

`POST /api/v1/authorize` 和 `POST /api/v1/rbac-v1/simulate` 在启用模式下直接 `GET` 上述业务 key，并严格解析完整 JSON：

- `subject`、归一化后的 `domain` 和 `service_resource` 必须与请求身份完全一致。
- `service_active=false`、接口不存在、接口停用或未被 `apis` 授予时拒绝。
- String 缺失、类型错误、JSON 损坏、身份不匹配、超过 16 MiB、超时或其他 Redis 错误都拒绝请求。
- 不读取 `rbac:api:app:*` 等旧辅助 key、`state`、members、锁、请求号或发布号，也没有 Lua、旧应用级 JSON fallback 或数据库回源。

`appKey` 仍是运行时请求的必填业务上下文，但 Redis 策略中没有应用身份，也不按应用切分权限记录。

## 保存与全量重建

每次影响服务 API 权限的控制面保存后执行全量重建。纯菜单编辑不影响服务 API 策略，仍走独立的 current-policy 路径，不触发 Redis 发布。

1. 获取 PostgreSQL session advisory lock，串行化多个中心的发布。
2. 保持该锁，取得同一个 `REPEATABLE READ` 数据库快照，计算并合并所有应发布的用户、服务和 domain 记录。
3. Redis 层对完整输出再做一次预验证，包括业务 key 形状、JSON 和单值大小；全部通过后才开始修改 Redis。
4. 按 `LUCKENV` 推导出的专用业务 key 前缀 `SCAN`，严格识别本协议的 key 形状，删除本轮快照中已不存在的旧用户记录。
5. 对每条记录 `SET` 完整 JSON。`SET` 会覆盖同名旧值或旧 Hash，成为 String；不设 TTL。
6. 发布结束后释放 advisory lock。

advisory lock 在取得数据库快照之前获得，并覆盖整个 Redis 发布过程，因此多个中心不会用不同快照交错发布。环境级或应用批次没有整体原子性；每条用户记录的 `SET` 是原子的。部分写入失败时，可能暂时保留旧权限，直到下一轮全量重建补发，保存接口返回 `503 PUBLISH_PENDING`。该流程不承诺撤权零窗口，也不提供在所有故障下阻止旧策略继续生效的强 fail-closed 闸门。

启动时和每次 `reconcile_interval` 对账都会全量重建，不复用旧的 `requested` / `published` 增量状态。该设计不需要新数据库表或 migration，也不需要 Redis 锁、索引或计数器。

## 协议切换

本次文档同步没有连接现网 Redis、没有清理旧 Hash/state key，也不部署服务。未来切换必须使用维护窗口，并按同一 `LUCKENV` 推导出的完整前缀保持中心与消费者一致：

1. 为中心和消费者记录目标前缀 `nexusauthorization:<QueryEscape(trim(LUCKENV))>`，并确认消费者使用自身在中心登记的服务资源。
2. 停止旧 Hash reader/writer 的鉴权流量。
3. 启动新版中心，让其按目标前缀全量重建以 String 写入业务 key。
4. 将 Demo 或其他新消费者的 `KeyPrefix` 更新为目标前缀并启动，完成允许、拒绝、缺失、损坏和身份不匹配验收。
5. 确认旧 reader 已全部停止后，再按旧 key 形状做定向清理。

旧 `rbac:api:app:*` 辅助 key 不需要被新 reader 或 writer 依赖。旧 Hash reader、旧 writer 与新 String 中心不能混跑；旧读端不会看到新版撤权。旧应用级 JSON 值不能作为新协议的兼容 fallback。

已有实例调整 `LUCKENV`、或从旧的自选前缀迁移到推导前缀时，完整 key 已经改变。中心必须按新前缀重新发布全部业务记录，所有消费者也必须同步更新 `KeyPrefix`；只改一侧会读到缺失 key 并 fail closed，旧前缀下的记录不会自动迁移或兼容读取。

## 配置

```yaml
redis:
  enabled: true
  url: "redis://localhost:6379/0"
  timeout: "200ms"
  reconcile_interval: "30s"
```

中心启动时必须通过环境变量提供非空 `LUCKENV` 和 `SERVICEUK`。其余对应环境变量为 `NEXUSAUTHORIZATION_REDIS_ENABLED`、`NEXUSAUTHORIZATION_REDIS_URL`、`NEXUSAUTHORIZATION_REDIS_TIMEOUT` 和 `NEXUSAUTHORIZATION_REDIS_RECONCILE_INTERVAL`。中心不提供 Redis 前缀环境变量；消费者根据相同 `LUCKENV` 计算并配置 `KeyPrefix`。启用时必须配置非空 `redis.url`；修改环境或消费者前缀后需重启相应进程。禁用时不创建 Redis 客户端，服务 API 使用数据库 current-policy。

## 本轮验证

2026-10-25 的本地验证覆盖新 String 协议：`go test -race -count=1 ./...` 全部包通过，使用隔离临时 PostgreSQL 与 miniredis，覆盖多应用 union、服务移除、跨实例锁和 `MaxConns=1`；`go vet ./...` 通过。Demo 构建 0 警告、0 错误，45 项本地集成测试通过。本轮未访问现网 Redis，也未部署新版中心与 Demo，旧 12 项现网记录不能作为新协议通过结论。

保存生效与失败语义见 [权限生效设计](permission-lifecycle.md)，运行时接口见 [API 参考](reference-api-rbac-v1.md#8-运行时鉴权)。
