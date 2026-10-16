# Redis 当前权限缓存

使用 `github.com/redis/go-redis/v9` 缓存当前保存的权限记录。Redis 是可选缓存，PostgreSQL 的当前规范化权限表为权威数据源；没有发布版本、历史快照或按业务版本命名的 key。保存生效与事务规则见 [权限生效设计](permission-lifecycle.md)。

## key 标准与大小

```text
PREFIX:rbac:current:app:{APPLICATION_UUID}:manifest
PREFIX:rbac:current:app:{APPLICATION_UUID}:part:PART
```

`PART` 从 0 开始，UUID 外的花括号为实际 hash tag。PREFIX 用于环境隔离，例如 `nexusauthorization:prod`。前缀不能含空白、花括号或尾部冒号。当前使用 standalone Client，未配置 Sentinel/Cluster 客户端。

逻辑查询身份仍为应用＋服务＋domain＋用户，其中 `domain` 使用归一化后的值（省略、空白或旧 `global` 都是空默认域，具名域保持独立）。进程索引分别定位服务路由和身份。物理 Redis key 按应用分片缓存当前记录，不按用户或绑定逐条建 key，也不为每个用户保存一份完整接口集合。缓存中没有用户×接口矩阵。

当前记录按字节分片，每个 String 最多 **256 KiB**，包括 32 字节的内部失效标记 SHA-256 头；每片有效 JSON 字节最多 **262,112**。一整个大角色的接口列表或大成员列表同样会拆片。小清单含 `format`、应用 UUID、当前失效标记、内容 SHA-256、总字节数和片数；通常几百字节，读取时上限 4 KiB。

设 S 为当前记录 JSON 字节数，完整新写入的 key 数为：

```text
1 + ceil(S / 262112)
```

10 万成员绑定＋2 万角色接口授权的测试约 6.86 MB，使用 27 片＋1 清单，共 28 个 key。保存和重试复用这些 key，失效标记不进入 key。数据缩小时旧多余分片不再读取，按 TTL 清理，因此实际 key 数短期受最近未过期的最大缓存大小影响。单应用缓存内容上限 512 MiB，最多 2,050 个有效 key；超出限制直接使用数据库，不阻止保存或鉴权。

这些是当前数据的缓存页，不写入持久化快照表，也不保留可选择的历史权限。

## 保存与读取

1. 管理保存仅提交数据库权限及事务触发器更新的失效标记，无需等待 Redis 写入。
2. 鉴权每次读取数据库标记、启用状态和当前服务目录。先检查本地只读索引；命中直接匹配。
3. 本地未命中时读取 Redis 清单及分片。清单和每片标记必须与本次权威元数据一致，并验证片数、字节数、内容 SHA-256 和应用身份。
4. 任一验证失败、Redis 超时/不可用或缓存过期，整份回源当前数据库表，然后尝试回填。回填失败只影响缓存命中率。
5. 数据库与缓存的标记过期竞态会重读最新数据；不会把新成员与旧权限混成一次决策。

分片每批最多 16 个，通过 pipeline 读写，一批载荷最多 4 MiB。全部写完再写完成清单。相同标记的并发回填写相同内容；不同标记的并发或迟到回填可能造成缓存未命中，但不能通过最新数据库标记校验而让旧授权生效。下一次回源修复即可，不依靠 Redis 删除成功实现撤权。

清单 TTL 默认 24 小时，分片多保留 1 分钟。`timeout` 限制每批网络操作，完整读写另有 30 秒上限并受调用方 deadline 约束。缓存命中不续期。旧应用发布缓存命名空间不再读取，残留 key 按原 TTL 到期，不用全库 SCAN 或 FLUSHDB。

## 配置

不设置 URL 即仅使用数据库和本地编译索引：

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

旧 `snapshot_ttl` / `NEXUSAUTHORIZATION_REDIS_SNAPSHOT_TTL` 已移除，改用上面的缓存 TTL 配置。环境 URL 显式为空可禁用 YAML URL；支持 redis://、rediss://、用户名、密码和 DB 编号，凭据特殊字符需 URL 编码。TTL/timeout 至少 1ms。启动不强制 PING，Redis 故障可回源；数据库或服务目录不可用则返回错误并拒绝放行。

## 验证

miniredis 测试覆盖当前数据读写、应用隔离、保存失效、旧回填、混合标记、损坏/缺片、分批失败、并发重试、大小限制、TTL 和取消。数据库集成测试验证保存即生效、Redis 中断期间撤权、事务回滚、直接 SQL 修改 API 失效、用户移除、缓存修复和应用禁用。

```sh
cd src
go test -race ./...
# 测试库必须独立，先执行 001_init.sql；没有设置时数据库集成测试跳过。
NEXUSAUTHORIZATION_TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/nexusauthorization_test?sslmode=disable' \
  go test ./internal/data/db/repo -run 'TestRedisCurrentPolicyImmediateSaveIntegration|TestApplicationPermissionIntegration' -count=1
```
