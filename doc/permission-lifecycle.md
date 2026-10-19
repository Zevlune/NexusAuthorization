# 保存即生效的权限设计

应用是权限隔离边界。角色授权、角色启停、用户角色绑定、菜单和 API 保存成功后直接生效；删除也直接生效。没有草稿/发布状态、发布按钮、角色版本、应用版本、历史策略和持久化权限快照。数据库只保存业务配置，不保存 `policy_token`、角色/服务 revision 或发布历史。

## 运行时行为

`POST /api/v1/authorize` 的请求需要 `appKey`、`serviceResource`、`subject`、`method`、`path`；`domain` 可选，省略或传空白按默认空域处理，旧值 `global` 也归一到同一默认空域。应用/服务/用户（含域）定位权限范围，method/path 确定具体接口。身份必须来自可信登录态，客户端不传角色。多个启用角色的权限取并集。

默认域是数据库非 NULL 空串 `''`：省略 `domain` 就是使用默认域。旧字符串 `global` 是默认域的兼容别名，与空串指向同一组绑定；其它非空具名域仍按域隔离，不会被合并，也不会因为省略 `domain` 而获得具名域权限。

服务 API 鉴权默认使用 Redis，`redis.enabled` 省略时也为 `true`。启用时，管理端保存业务表后，控制面把受影响应用整体重建为新的 Redis 记录：先置 `updating` 闸门，删除被取代的旧记录、写入新记录，全部完成后再置 `ready`。鉴权请求只读 Redis，不读取数据库，也不回源；连接失败、应用未 ready、记录缺失、身份不匹配、损坏或超时都直接拒绝。启用要求启动时 `redis.url` 非空。显式配置 `redis.enabled: false` 或 `NEXUSAUTHORIZATION_REDIS_ENABLED=false` 后，服务 API 改用数据库 current-policy 鉴权，不创建 Redis 客户端，也不发布 Redis 记录。key、值、闸门和切换细节见 [Redis 接入说明](redis-integration.md)。

权限模拟与真实服务 API 鉴权走同一条当前模式对应的路径；用户菜单始终走独立的 current-policy 路径。鉴权结果包含 `allowed`、`effect`、`reason`、`matched_roles`，不返回 `release_version`。Go SDK 已适配该响应；旧 SDK 对发布版本的校验会拒绝新响应，需要同步升级。

Redis 模式下，保存事务提交后控制面才会重建 Redis，因此保存成功后纳入的新身份、授权和撤权在下一次鉴权即可见。保存失败或事务回滚时不重建，Redis 保持上一份一致记录。Redis 不可用或记录读取失败时鉴权失败关闭，SDK 拒绝访问，不使用无法验证的旧结果放行。数据库 current-policy 模式直接读取已提交的当前业务表。

模式切换需要重启 API。重新启用时，先设置 `redis.enabled=true` 或 `NEXUSAUTHORIZATION_REDIS_ENABLED=true` 并重启 API，然后在向该实例发送服务 API 流量前保存受影响的应用、服务、角色或绑定，触发对应应用整体重建。禁用期间保存的数据库编辑不会自动进入旧 Redis 记录。

## Redis 一致性

Redis 模式的服务 API 记录按「应用 key + 主体 + 服务 + 归一化 domain」组织，值为普通 JSON，不设 TTL，也不含业务版本。一致性由每个应用的写入闸门保证，而不是数据库失效标记：

1. 保存前先把该应用置为 `updating`，使并发鉴权在替换期间被拒绝。
2. 依据旧 `members` 集合删除不再存在的记录，再分批写入新记录，每批最多 64 条。
3. 全部记录落盘后才置为 `ready`。任何一步失败都保持 `updating`，下一次重建从干净状态重试。

删除先于写入且闸门最后打开，所以同一应用的新旧记录不会短暂并存，也不会暴露半写集合。Redis 启用时，`role`、`role_api_endpoint_bindings`、`subject_role_bindings`、`menus`、`role_menu_bindings`、`application_service_resources`、`api_endpoints` 和 `service_resources` 的保存都会触发受影响应用的重建：角色 API/菜单授权、主体角色绑定和应用服务关联重建本应用；接口端点和服务资源变更重建所有关联该服务的应用。纯菜单保存只影响 current-policy 路径，不改变服务 API 记录。Redis 禁用时不执行这些发布。

数据库不再用触发器维护任何缓存版本或失效标记。`applications` 没有 `policy_token` 列，也没有 `role_api_policy_tokens` / `service_api_policy_tokens` 表；唯一保留的数据库触发器是四条业务保护触发器（防止停用有关联的服务资源、解除仍有授权的服务关联、跨应用角色 API/菜单授权）。

## 菜单 current-policy 路径

菜单、全策略适配器和历史基准走 current-policy 路径，与服务 API 的模式开关和 Redis 记录是独立域。每次请求在一条 MVCC 语句里读取当前规范化业务表，并对规范 JSON 计算 SHA-256 作为内存内容 token；进程内按应用保留最多 64 个编译索引，命中时复用编译索引。元数据读取和记录读取之间发生保存提交时按 token 拒绝旧结果，最多重试 3 次。该路径不读取服务 API Redis 记录，因此无论 Redis 开关如何，菜单都按数据库工作。

菜单 token 不进入 Redis 模式的服务 API key，也不影响其记录。各条鉴权路径都只使用运行时当前数据，不写 JSONB 快照表。

## 管理界面与接口

角色编辑页提示“保存后角色授权立即生效”。用户分配保存、移除用户和删除角色直接影响鉴权。左侧保留“权限验证 → 权限模拟”，删除发布页面、发布菜单和发布 HTTP 路由；模拟按当前配置模式验证请求。API/菜单定义变更也直接生效，应复核共享服务的影响范围。

## 初始数据库结构

第一次安装使用 `migrations/000_create_database.sql` 创建数据库，再使用 `migrations/001_init.sql` 创建完整 schema。`001_init.sql` 只创建当前业务表和四条业务保护触发器，不创建 `applications.policy_token`、`role_api_policy_tokens`、`service_api_policy_tokens` 或任何缓存失效触发器，也不创建发布版本、历史快照或迁移记录表。脚本自带顶层事务，不插入业务种子数据。

数据库初始化按首次上线流程处理：先创建空数据库，再执行 `migrations/001_init.sql`。初始化完成后由管理员通过 Dashboard 或管理 API 创建应用、服务、API、角色、菜单和用户绑定。

Redis key 规范、闸门、写入顺序和可选配置单独见 [Redis 接入说明](redis-integration.md)；算法和大绑定量基准见 [鉴权匹配与性能](authorization-matching.md)。
