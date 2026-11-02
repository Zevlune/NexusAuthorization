# NexusAuthorization .NET 接入 Demo

解决方案入口为 `demo/NexusAuthorization.Demo.sln`，可使用 Visual Studio 或 Rider 直接打开。在仓库根目录执行 `dotnet build demo/NexusAuthorization.Demo.sln` 构建，使用 `dotnet run --project demo/dotnet` 启动。

这是一个采用 Controllers 模式的 .NET 10 示例。Demo 使用 OpenID Connect 完成统一登录，继续通过 introspection 校验 Cookie 或 Bearer 所代表的访问令牌，并通过 [.NET SDK](../sdk/dotnet/README.md) 执行业务授权。`AuthorizationCenter:Provider` 默认选择 Redis，也可切换到 Http 调用授权中心 `/api/v1/authorize`。Redis 模式直接读取中心发布的业务 String，不访问数据库；Http 模式需要中心服务凭证。Demo 本身不访问授权中心数据库。

## 项目结构

`demo/dotnet/Controllers` 放置全部入口和业务接口，包括 Home、Health、Auth、Orders、Products、Reports 和 Public。`Program.cs` 通过 `AddControllers()` 和 `MapControllers()` 注册控制器，并配置登录、认证、令牌 introspection 和可切换的 NexusAuthorization Provider。OIDC 协议回调 `/signin-oidc` 由认证中间件处理。

`Program.cs` 先保存 `AddNexusAuthorization(settings.ConfigureAuthorization)` 返回的 Builder，再用 `switch` 显式调用 Action-only、返回 `void` 的 `UseRedis(settings.ConfigureRedis)` 或 `UseHttp(settings.ConfigureHttp)`。Provider 选择开关只存在于 Demo 启动代码，SDK 自己没有 Provider 配置。通用契约、ASP.NET Core policy/handler 位于核心项目，Redis 和 Http 分别由两个实现项目提供；核心、Redis、Http 三个项目都通过项目引用进入 Demo。

Redis 模式下，SDK 通过 DI 创建或复用共享连接，在每次受保护请求中对业务 key 执行 `GET`。StackExchange.Redis 的 multiplexer 线程安全，适合在整个应用内共享；`GetDatabase()` 只返回轻量数据库句柄，不必另行缓存。可参考[官方基础用法](https://stackexchange.github.io/StackExchange.Redis/Basics.html)。

Redis 模式下，`SubjectApiPolicyMatcher` 根据请求方法和完整请求路径，从策略中的 `Catalog` 选择接口并检查授权。Http 模式把主体、方法、路径、应用和服务发送给授权中心；请求不包含 domain。两种模式都不由 Demo 缓存授权结果；Demo 始终不直接访问数据库。

`demo/dotnet/Demo.Api.http` 提供匿名访问、Bearer 调用、商品增删改查、参数校验和 CSV 导出的请求示例，可在支持 `.http` 文件的 IDE 中运行，也可在 Swagger 中测试。

`demo/tests/Demo.Api.IntegrationTests` 是解决方案中的正式集成测试项目，使用真实 Controller、授权 handler、JSON 解析和 StackExchange.Redis。测试边界见下文“集成测试”。

默认地址：

- Demo：`http://localhost:5055`
- Http Provider 示例 BaseUrl：`http://localhost:8080`
- NexusAuth：以 `Auth:Authority` 的实际配置为准

## 配置

配置结构与下面一致，具体主机和密钥按本地环境填写：

```json
{
  "AuthorizationCenter": {
    "Provider": "Redis",
    "AppKey": "bugmanager.admin",
    "ServiceResource": "nexusauthorization.demo.api",
    "SubjectClaimType": "sub"
  },
  "Redis": {
    "Host": "redis.example.internal",
    "Port": 6379,
    "User": "",
    "Password": "",
    "Database": 0,
    "KeyPrefix": "nexusauthorization:prod",
    "TimeoutMilliseconds": 2000
  },
  "Http": {
    "BaseUrl": "http://localhost:8080",
    "ServiceCredential": "",
    "TimeoutMilliseconds": 3000
  }
}
```

`AuthorizationCenter:Provider` 默认 `Redis`，比较时忽略大小写及首尾空白，只接受 `Redis`、`Http`；其他值会让 Demo 在启动时直接报错。配置在启动时读取，修改后需重启 Demo。

示例中的 `Redis:KeyPrefix` 是 Demo 作为消费者的显式配置；假定中心使用 `LUCKENV=prod`，因此值为 `nexusauthorization:prod`。Demo 不读取 `LUCKENV` 或 `SERVICEUK`：部署时必须把 `Redis:KeyPrefix` 设置为中心实际推导的 `nexusauthorization:<QueryEscape(trim(LUCKENV))>`，并让 `AuthorizationCenter:ServiceResource` 与 Demo 在中心登记的服务标识一致。中心不再接受 `NEXUSAUTHORIZATION_REDIS_KEY_PREFIX` 作为前缀配置。

例如在配置好 `Http:BaseUrl` 和调用凭证后，通过环境变量切换并启动：

```sh
AuthorizationCenter__Provider=Http dotnet run --project demo/dotnet
```

`AuthorizationCenter` 保留 `AppKey`、`ServiceResource` 和 `SubjectClaimType`。`ConfigureAuthorization` 只把服务和主体声明映射到公共 `NexusAuthorizationOptions`；核心选项和 Demo 配置都不再有 Domain。`ConfigureHttp` 把 `AuthorizationCenter:AppKey` 映射到 `HttpAuthorizationOptions.AppKey`。因此 Demo 的 `Http:AppKey` 不会生效，应用标识沿用原配置路径。

顶层 `Redis` 保持既有路径，映射到 `RedisAuthorizationOptions`。新增顶层 `Http` 映射到 `HttpAuthorizationOptions`，其中 `BaseUrl` 默认示例为 `http://localhost:8080`，`ServiceCredential` 默认为空，`TimeoutMilliseconds` 默认 `3000`。Demo.Api 同时项目引用 `NexusAuthorization.Sdk.Redis` 和 `NexusAuthorization.Sdk.Http`，两者都会传递引用核心库。

通过 Demo 项目的 .NET user-secrets 或环境变量提供 `Redis:Password`、`Auth:ClientSecret` 等敏感值。文档不记录实际密钥：

```sh
dotnet user-secrets set --project demo/dotnet "Auth:ClientSecret" "替换为 NexusAuth 客户端密钥"
dotnet user-secrets set --project demo/dotnet "Redis:Password" "替换为 Redis 密码"
dotnet user-secrets set --project demo/dotnet "Http:ServiceCredential" "替换为授权中心服务凭证"
```

Redis 若需要 ACL 用户名，可在本机配置 `Redis:User`；当前默认值为空。

默认启动配置使用 Development 环境，会自动加载 user-secrets；也可使用 `Redis__Password`、`Http__ServiceCredential`、`Auth__ClientSecret` 环境变量提供密钥。使用当前仓库 Go 服务的 `/api/v1/authorize` 时必须设置与中心一致的 `Http:ServiceCredential`，因为该接口只接受 `authorization.service_credential`。Cookie 不会被 SDK 自动提取或保存为服务令牌，不能替代这个固定凭证。

## 登录与鉴权流程

登录、OIDC 和 introspection 行为保持不变：

1. 浏览器通过 `/api/auth/login` 进入 ASP.NET Core OpenID Connect Authorization Code + PKCE。
2. Cookie 请求由 OIDC 验证身份；Bearer 请求校验 JWT 的签名、issuer、audience 和有效期。
3. 两种身份都会通过 `/connect/introspect` 检查 `sub`、client、issuer、scope 和撤销状态。
4. 受保护业务接口通过启动时选定的 Provider 执行 NexusAuthorization 检查。Redis 模式用 token 中的 `sub` 和 service 组成业务 key 并读取完整策略；Http 模式向授权中心发送不含 domain 的授权请求。

令牌与 introspection 请求的客户端认证方式由 `Auth:TokenEndpointAuthMethod` 控制。默认 `client_secret_basic` 必须与 NexusAuth 中登记的方式一致；也可以把两端同步改为 `client_secret_post`，无需修改代码。

Demo 不刷新 token。access token 到期后需要重新登录并取得新 token。

`GET /api/auth/login` 会直接返回 `302` 跳转到登录中心。登录成功后回到 `/swagger/index.html`。请在新标签页直接访问 `http://localhost:5055/api/auth/login` 完成登录，再回到 Swagger 使用 Cookie。不要从 Swagger Try it out 发起登录，跨站 `302` 跳转可能被浏览器 fetch 流程拦截。

受保护接口也可以使用原始 access token：

```http
Authorization: Bearer <access token>
```

Swagger 的 Authorize 输入框可直接填入不带 `Bearer ` 前缀的 access token。

## Redis 读取契约

Demo 配置中的 `KeyPrefix` 必须与中心由 `LUCKENV` 推导的前缀完全相同。业务 key 中不包含应用段：

```text
nexusauthorization:<QueryEscape(trim(LUCKENV))>:<QueryEscape(trim(ServiceResource))>:<QueryEscape(trim(sub))>
```

- 类型为 Redis STRING，值为一个用户和一个服务的完整策略 JSON。
- 中心为各服务发布策略；Demo 的 `ServiceResource` 必须与其自身登记的服务标识一致。Demo 本身只读取配置好的 `KeyPrefix`，不会读取 `LUCKENV` 或 `SERVICEUK`。
- JSON 字段为 `subject`、`domain`、`service_resource`、`service_active`、`catalog`、`apis` 和 `roles`，不含 `application_key` 或 `application_id`。
- JSON 由中心合并所有启用应用对该服务的授权；app 不覆盖其他 app，也不参与业务 key。
- `ServiceResource` 和 `sub` 都先 `trim`，再按 Go `url.QueryEscape` 编码。Go `QueryEscape` 使用 `+` 表示空格。
- 业务 key 不含 domain 段或 domain 后缀；.NET SDK/Demo 仅支持默认域，不暴露 `NormalizeDomain` API，也没有可配置域。
- 为兼容中心已有的默认域 String 策略，JSON 的 `domain` 可以缺失、为 `null`、为空字符串或为字面量 `global`；经 `trim` 后都等价于默认域。非默认具名 domain 会以 `policy_identity_mismatch` 拒绝。
- `SubjectApiPolicy` 不对外暴露 `Domain` 属性；内部仍映射该 JSON 字段，只用于默认域兼容校验。
- 每个 String value 上限 16 MiB，key 不设 TTL。

假定中心 `LUCKENV=prod` 且当前 Demo 配置下，`sub = 51b21204-54ab-4da6-ad83-d14f72ced46f`、服务 `nexusauthorization.demo.api`、默认域的业务 key 为：

```text
nexusauthorization:prod:nexusauthorization.demo.api:51b21204-54ab-4da6-ad83-d14f72ced46f
```

Provider 为 Redis 时，Demo 对该 key 直接执行 `GET`。读取后要求 JSON 合法，`subject` 和 `service_resource` 与请求身份完全一致，内部 domain 元数据属于默认域，并检查 `service_active`。String 缺失、类型错误、JSON 损坏、身份不匹配、具名 domain、超过 16 MiB、Redis 超时或不可用时拒绝请求。Demo 不读取 `rbac:api:app:*`、`state`、members、锁或发布号，不执行 Lua，也不接受旧应用级 JSON 的 fallback。

## Catalog API 匹配

策略中的 `Catalog` 是完整 API 目录，匹配顺序如下：

1. 只保留 HTTP 方法与请求方法一致、且 `path_template` 与完整请求路径匹配的目录项。方法比较忽略大小写。
2. 路径按 `/` 分段；字面 segment 必须完全一致，`{参数}` 只匹配一个非空 segment。中心从 Swagger 导入的 `/api/demo/orders/{id}` 模板可匹配实际请求 `/api/demo/orders/1`。MVC 的 `:int` 约束仍由控制器路由处理。
3. 在所有匹配项中选择字面 segment 数最多的一项。字面路径通常优先于参数路径。
4. 最高分出现不同 ID 的并列项时按歧义拒绝。
5. 获胜目录项为 `disabled` 时拒绝；否则仅当获胜项的 ID 出现在策略 `apis` 中时放行。

没有目录匹配项、目录项禁用、目录歧义或获胜 ID 未授权都返回 `403`。

## 目标 Redis 授权记录

按当前配置，新 String 协议下的目标记录为：

- 用户 `51b21204-54ab-4da6-ad83-d14f72ced46f` 拥有角色 `order-review`。
- 服务资源为 `nexusauthorization.demo.api`；策略的 domain 元数据为默认域，业务 key 不含 domain 后缀。
- 完整 JSON 位于 `nexusauthorization:prod:nexusauthorization.demo.api:51b21204-54ab-4da6-ad83-d14f72ced46f`，类型为 String；JSON 不含应用标识。
- 该策略合并所有启用应用对该用户的授权，`service_active=true`。
- Catalog 只有 `GET /api/demo/orders` 和 `GET /api/demo/orders/{id}` 两项。
- `GET /api/demo/orders` 已授予；`GET /api/demo/orders/{id}` 未授予。

这些是验收目标，不代表本次文档同步已写入或验证现网 Redis。该用户访问订单列表时应通过授权；访问订单详情时命中 Catalog 但没有 API 授予，应返回 `403`，鉴权日志中的原因为 `endpoint_not_granted`。其他七个受保护业务端点不在当前 Catalog 中，应返回 `403`，原因为 `endpoint_not_found`。原因记录在服务端日志中，不包含在 HTTP 响应体中。

## 接口

| 方法 | 路径 | 要求 |
| --- | --- | --- |
| `GET` | `/` | 匿名，跳转到 `/swagger` |
| `GET` | `/health` | 匿名 |
| `GET` | `/api/auth/login` | 浏览器登录；未登录时直接 `302` 到 NexusAuth |
| `GET` | `/api/auth/me` | 仅要求已认证 |
| `POST` | `/api/auth/logout` | 清除 Demo Cookie，返回 `{ "logoutUrl": "..." }`；浏览器需继续打开该地址完成 SSO 登出 |
| `GET` | `/api/demo/orders` | 已认证，并通过所选 Provider 的 NexusAuthorization 检查 |
| `GET` | `/api/demo/orders/{id:int}` | 已认证，并通过所选 Provider 的 NexusAuthorization 检查；路径参数参与匹配 |
| `GET` | `/api/demo/products` | 已认证，并通过所选 Provider 的 NexusAuthorization 检查 |
| `GET` | `/api/demo/products/{id:int}` | 已认证，并通过所选 Provider 的 NexusAuthorization 检查；路径参数参与匹配 |
| `POST` | `/api/demo/products` | 已认证，并通过所选 Provider 的 NexusAuthorization 检查 |
| `PUT` | `/api/demo/products/{id:int}` | 已认证，并通过所选 Provider 的 NexusAuthorization 检查；路径参数参与匹配 |
| `DELETE` | `/api/demo/products/{id:int}` | 已认证，并通过所选 Provider 的 NexusAuthorization 检查；路径参数参与匹配 |
| `GET` | `/api/demo/reports/summary` | 已认证，并通过所选 Provider 的 NexusAuthorization 检查 |
| `POST` | `/api/demo/reports/export` | 已认证，并通过所选 Provider 的 NexusAuthorization 检查 |
| `GET` | `/api/public/ping` | 匿名 |
| `GET` | `/api/public/info` | 匿名 |

业务 Controller 使用核心库的 `[NexusAuthorize]` 展示 SDK 接入方式。它等价于原生 `[Authorize(Policy = NexusAuthorizationDefaults.PolicyName)]`。只写 `[Authorize]` 不会执行 NexusAuthorization 检查；标记 `[AllowAnonymous]` 的接口也不会检查 Nexus。健康检查、认证、订单等原有端点路径保持不变。

商品创建和更新的请求体格式如下：

```json
{
  "name": "演示商品",
  "price": 49.90
}
```

`name` 必填且最长 100 个字符；`price` 必须在 `0.01` 到 `1000000` 之间。请求先通过认证和所选 Provider 的 NexusAuthorization 检查，再由数据注解和 `[ApiController]` 自动校验；不符合约束时返回 `400`。

`POST /api/demo/products` 返回 `201 Created`，`Location` 指向 `/api/demo/products/3`，响应体包含 id 为 `3` 的回显数据及 `persisted=false`。Demo 只回显请求，不保存数据，因此随后访问该详情仍会返回 `404`。更新和删除响应同样包含 `persisted=false`，删除只返回“演示删除，不保存数据”。

`POST /api/demo/reports/export` 下载文件名 `demo-orders.csv` 的 UTF-8 CSV，媒体类型为 `text/csv`。文件包含 `id,number,amount` 表头和 `DEMO-001`、`DEMO-002` 两条固定订单数据。

匿名接口不要求 NexusAuthorization；`/api/auth/me` 只要求认证；订单、商品和报表接口使用 `[NexusAuthorize]`，未登录返回 `401`，已登录但没有明确 allow 决策返回 `403`。

`[NexusAuthorize]` 等价于 `[Authorize(Policy = NexusAuthorizationDefaults.PolicyName)]`。
Demo 未设置全局或 fallback 授权策略；`[AllowAnonymous]` 跳过授权，裸 `[Authorize]`
执行默认认证策略，不会调用 Nexus Provider。Controller 上的策略也适用于其 Action。

Swagger UI 位于 `/swagger`，OpenAPI 文档位于 `/swagger/v1/swagger.json`。

## 登录中心登记

Demo 客户端的登记项保持不变：

1. 创建机密客户端 `nexusauthorization.demo`。
2. 允许 `authorization_code` grant，并启用 `S256` PKCE；本 Demo 不要求 `refresh_token`。
3. 令牌认证方式与 Demo 的 `Auth:TokenEndpointAuthMethod` 保持一致。
4. 登录回调地址登记为 `http://localhost:5055/signin-oidc`。
5. 登出后回调地址登记为 `http://localhost:5055/swagger/index.html`。
6. 服务资源、scope 和 audience 使用 `nexusauthorization.demo.api`。
7. 允许 Demo 客户端申请 `openid`、`profile` 和 `nexusauthorization.demo.api` scope。

## NexusAuthorization 授权准备

业务接口成功需要以下登记保持一致：

1. 应用 key 使用 `bugmanager.admin`，并关联服务资源 `nexusauthorization.demo.api`。
2. 导入 Demo Swagger URL `http://localhost:5055/swagger/v1/swagger.json` 时，只把确需暴露的请求方法和路径模板加入 `Catalog`。
3. 角色和 API 授予必须同时满足；当前用户角色为 `order-review`，订单列表已授予，订单详情未授予。
4. 新版中心完成全量重建且发布没有返回 `PUBLISH_PENDING` 后，确认业务 key 是可严格解析的 String，JSON 身份、`service_active`、`Catalog`、`apis` 和 `roles` 符合上述目标。

Demo 不依赖数据库直连。Redis Provider 需要使用顶层 `Redis`；Http Provider 需要使用顶层 `Http:BaseUrl` 和与中心一致的 `Http:ServiceCredential`。当前 Go 服务的 `/api/v1/authorize` 只认服务凭证，不接受用户访问令牌；Cookie 登录后也不会由 SDK 自动提取并保存令牌。登录完成后，可通过 `GET /api/auth/me` 确认当前 token 的 `sub`。

## 推荐启动与切换顺序

1. 在 NexusAuth 确认 Demo 客户端、回调地址、scope 和 audience 已登记，并让令牌认证方式与 Demo 配置一致。
2. 选择 `AuthorizationCenter:Provider`。Redis 模式先根据中心 `LUCKENV` 计算 `nexusauthorization:<QueryEscape(trim(LUCKENV))>`，将 Demo `Redis:KeyPrefix` / `Redis__KeyPrefix` 配成完全相同的值，并确认 `AuthorizationCenter:ServiceResource` 与 Demo 登记的服务标识一致；Http 模式需设置 `Http:BaseUrl` 和匹配的 `Http:ServiceCredential`。
3. Redis 模式按 String 协议准备中心发布；Http 模式确认授权中心 `/api/v1/authorize` 可访问且服务凭证有效。Http 请求使用 `AuthorizationCenter:AppKey` 且不发送 domain，Redis 策略按服务和主体隔离。
4. 从旧 Hash 协议升级的环境，先按 [Redis 接入说明](../doc/redis-integration.md) 完成迁移。
5. Redis 前缀因 `LUCKENV` 调整或旧前缀配置迁移而变化时，先让中心按新前缀完成全量发布，再更新所有消费者并重启；Demo 的 `KeyPrefix` 未同步会读取缺失 key 并 fail closed。
6. 启动 Demo：`dotnet run --project demo/dotnet`，默认监听 `http://localhost:5055`。启动前设置 `Auth:ClientSecret`，并按所选 Provider 设置 Redis 或 Http 配置；未知 Provider 值会阻止启动。
7. 打开 `http://localhost:5055/swagger`；使用 Cookie 时，在新标签页直接访问 `http://localhost:5055/api/auth/login`。不使用 Cookie 时，在 Swagger Authorize 或 `.http` 文件中填入原始 access token。
8. 验收允许、拒绝和 Provider 故障时的拒绝行为。Redis 模式还应验证 String 缺失、类型/JSON 损坏和身份不匹配。

## 集成测试

测试用户固定为 `51b21204-54ab-4da6-ad83-d14f72ced46f`。本地测试在 testhost 中注入该 `sub`，覆盖真实 Controller、身份 handler、JSON 解析和 StackExchange.Redis 路径。

2026-10-27 验证：145 项 SDK 单元测试、30 项 SDK 真实 HTTP 集成测试及 76 项 Demo 集成测试通过，共 251 项通过、0 项失败；4 项现网 Redis 测试未启用。Demo 包含 58 项临时 Redis 测试和 18 项 HTTP / Provider 配置测试，覆盖九个实际业务接口、默认 Bearer 转发、固定凭证、缺少令牌、即时撤权、匿名与仅认证端点、HTTP 模式不注册 Redis 连接及未知 Provider 启动失败。本次还验证 HTTP JSON 不含 domain、Redis 默认域元数据兼容、具名域策略拒绝和非法域字段类型拒绝。测试配置在 Program 选择 Provider 前注入，测试不另行注册 SDK。TRX 报告位于 `demo/TestResults/sdk-remove-domain-20261027/verified`。此次验证没有访问现网 Redis 或授权中心。

历史验证（2026-10-25）：Go `go test -race -count=1 ./...` 使用隔离临时 PostgreSQL 与 miniredis 全部包通过，覆盖多应用 union、服务移除、跨实例锁和单连接池；`go vet ./...` 通过。Demo 构建成功，0 个警告、0 个错误；修正一个测试预期后，45 项本地 String 协议集成测试全部通过。本轮没有访问现网 Redis，新版中心与 Demo 尚未部署，因此旧 12 项现网记录不能作为新协议通过结论。

历史记录仅用于说明上轮工作，均属于旧 Hash/field/Lua 契约，不是新 String 协议结果：

- 2026-10-24：Demo 构建成功；Go 服务端 `go test ./...` 通过；38 项本地集成测试通过。测试使用临时 Redis，未连接现网。此前 49 项测试同样是历史结果。
- 2026-10-25：上轮 Hash/Lua 记录包含 38 项本地集成测试和 12 项只读现网集成测试，共 50 项通过；该结果不代表 String/union、严格身份解析、全量替换或 `PUBLISH_PENDING` 语义通过。
- 上轮只读现网运行曾出现一次 `UnableToConnect`，复测后恢复；结果文件位于 `demo/TestResults/redis-protocol-20261025/local-redis.trx` 和 `live-redis.trx`（已忽略，不提交）。该记录不证明本轮新协议可用。

默认执行：

```sh
dotnet test demo/NexusAuthorization.Demo.sln
```

该命令会启动测试自己的临时 `redis:7-alpine` Docker 容器，因此需要本机 Docker 正在运行。容器只接收测试 fixture 生成的数据，测试完成后自动清理。执行成功与否应以本轮新协议的实际输出另行记录。

测试身份注入只服务于集成测试，不能证明真实 OIDC 登录、令牌签名/issuer/audience 或 introspection 校验正确；这些边界仍需通过 Demo 登录流程验证。

只读现网授权测试使用真实 Demo 配置、本机 user-secrets、环境变量、实际 Redis 和上述用户：

```sh
NEXUS_DEMO_RUN_LIVE_TESTS=1 dotnet test demo/NexusAuthorization.Demo.sln --filter 'Category=LiveRedis'
```

`Category=LiveRedis` 固定选择 Redis Provider，只运行只读现网测试，不启动临时 Docker Redis，也不发布、清理或改写现网授权数据。新协议切换后，应以 String 业务 key 和严格身份解析路径重新执行并单独记录结果。`demo/tests/Demo.Api.IntegrationTests/live-authorization.json` 记录业务期望，仍应验证：

- `GET /api/demo/orders` 返回 `200`。
- `GET /api/demo/orders/1` 返回 `403`，鉴权决策原因为 `endpoint_not_granted`。
- 其余七个受保护端点返回 `403`，鉴权决策原因为 `endpoint_not_found`。

这些期望用于避免“所有请求都被拒绝”被误判为测试成功。调整用户、应用、服务、角色或授权后，需要根据目标状态手工修改 `live-authorization.json`；不得按测试实际结果自动改写期望。

## 状态码与限制

- 未登录调用 `/api/auth/me`、订单、商品或报表接口返回 `401`；Public、Health 和首页路由保持匿名。
- 已登录但所选 Provider 没有返回明确 allow 决策、请求超时或不可用时，业务接口返回 `403`，不自动回退到另一 Provider。
- Redis 模式下，业务 String 缺失、类型错误、JSON 损坏、身份不匹配、超过 16 MiB 时返回 `403`；`Catalog` 无匹配项、目录歧义、目录项禁用或获胜 API ID 未授权时也返回 `403`。
- 商品请求体缺少 `name`、`name` 超过 100 个字符或 `price` 超出范围时，在已获授权后返回 `400`。
- 未设置 `Auth:ClientSecret` 时，登录入口返回 `503`；令牌 introspection 失败或身份不满足要求时，受保护请求返回 `401`。
- Demo 不保存数据库状态，也不缓存授权结果。Redis 模式每个受保护请求直接读取完整业务 String；Http 模式每个受保护请求调用一次授权中心 API。
- access token 到期后需重新执行登录流程；SSO 登出需要浏览器继续打开 `/api/auth/logout` 返回的 `logoutUrl`。

## invalid_client 排查

如果 `Auth:TokenEndpointAuthMethod` 与 NexusAuth 客户端登记的令牌认证方式不一致，`/connect/token` 和 `/connect/introspect` 都会拒绝 Basic 与表单凭据错配的请求。

NexusAuth 的 `401` 响应只返回 `{ "error": "invalid_client" }`，没有 `error_description`。这表示客户端认证失败，不表示网络请求未到达服务端。
