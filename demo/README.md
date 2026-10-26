# NexusAuthorization .NET 接入 Demo

解决方案入口为 `demo/NexusAuthorization.Demo.sln`，可使用 Visual Studio 或 Rider 直接打开。在仓库根目录执行 `dotnet build demo/NexusAuthorization.Demo.sln` 构建，使用 `dotnet run --project demo/dotnet` 启动。

这是一个采用 Controllers 模式的 .NET 10 示例。Demo 使用 OpenID Connect 完成统一登录，继续通过 introspection 校验 Cookie 或 Bearer 所代表的访问令牌，并引用 [.NET SDK](../sdk/dotnet/README.md) 读取 NexusAuthorization 发布的业务 String 策略。默认 Redis 模式不访问数据库，也不需要权限中心服务调用凭证。

## 项目结构

`demo/dotnet/Controllers` 放置全部入口和业务接口，包括 Home、Health、Auth、Orders、Products、Reports 和 Public。`Program.cs` 通过 `AddControllers()` 和 `MapControllers()` 注册控制器，并配置登录、认证、令牌 introspection 和 Redis 授权策略。OIDC 协议回调 `/signin-oidc` 由认证中间件处理。

`Program.cs` 通过 `AddNexusAuthorization(settings.ConfigureAuthorization).UseRedis(settings.ConfigureRedis)` 注册 SDK。共享 Redis 连接、策略解析、匹配器和授权 handler 均由 `sdk/dotnet` 中的核心库和 Redis 实现库提供，Demo 不再包含这些实现。SDK 由 DI 容器创建和释放共享连接，在每次受保护请求中对业务 key 执行 `GET`，读取并严格解析完整策略 JSON。

StackExchange.Redis 的 multiplexer 线程安全，适合在整个应用内共享复用；`GetDatabase()` 只返回轻量数据库句柄，不必另行缓存。可参考[官方基础用法](https://stackexchange.github.io/StackExchange.Redis/Basics.html)。

`SubjectApiPolicyMatcher` 根据请求方法和完整请求路径，从策略中的 `Catalog` 选择接口并检查授权。Demo 不缓存授权结果，也不回源数据库或权限中心 HTTP API。

`demo/dotnet/Demo.Api.http` 提供匿名访问、Bearer 调用、商品增删改查、参数校验和 CSV 导出的请求示例，可在支持 `.http` 文件的 IDE 中运行，也可在 Swagger 中测试。

`demo/tests/Demo.Api.IntegrationTests` 是解决方案中的正式集成测试项目，使用真实 Controller、授权 handler、JSON 解析和 StackExchange.Redis。测试边界见下文“集成测试”。

默认地址：

- Demo：`http://localhost:5055`
- NexusAuth：以 `Auth:Authority` 的实际配置为准

## 配置

`demo/dotnet/appsettings.json` 中的非敏感配置应与下面一致：

```json
{
  "AuthorizationCenter": {
    "AppKey": "bugmanager.admin",
    "ServiceResource": "nexusauthorization.demo.api",
    "Domain": ""
  },
  "Redis": {
    "Host": "192.168.31.200",
    "Port": 8433,
    "User": "",
    "Password": "",
    "Database": 0,
    "KeyPrefix": "nexusauthorization:prod",
    "TimeoutMilliseconds": 2000
  }
}
```

`AuthorizationCenter` 使用 `AppKey`、`ServiceResource` 和 `Domain`。`AppKey` 仍是运行时请求上下文，但不再出现在 Redis 业务 key 或策略 JSON 中，也不是授权隔离维度；中心会合并所有启用应用对该服务的用户权限。Demo 当前配置为应用 `bugmanager.admin`、服务资源 `nexusauthorization.demo.api`、空域策略。配置在启动时读取，修改后需重启 Demo。

Demo 保留 `AuthorizationCenter` 和 `Redis` 两个配置节，以兼容已有 user-secrets。Demo.Api 只引用 `NexusAuthorization.Sdk.Redis`，并传递引用核心库；Redis 读取、路径匹配和 ASP.NET Core 授权 handler 均由 SDK 提供。通用上下文使用 `NexusAuthorizationOptions`，Redis 连接使用独立的 `RedisAuthorizationOptions`。其他项目可通过 Builder 的 `UseRedis(configure)` 或 `UseHttp(configure)` 选择实现；这两个方法仅接收配置 Action，返回 `void`。HTTP 实现位于独立的 `NexusAuthorization.Sdk.Http` 类库；接入方式见 [SDK 文档](../sdk/dotnet/README.md)。

通过 Demo 项目的 .NET user-secrets 或环境变量提供 `Redis:Password`、`Auth:ClientSecret` 等敏感值。文档不记录实际密钥：

```sh
dotnet user-secrets set --project demo/dotnet "Auth:ClientSecret" "替换为 NexusAuth 客户端密钥"
dotnet user-secrets set --project demo/dotnet "Redis:Password" "替换为 Redis 密码"
```

Redis 若需要 ACL 用户名，可在本机配置 `Redis:User`；当前默认值为空。

默认启动配置使用 Development 环境，会自动加载 user-secrets；也可使用 `Redis__Password`、`Auth__ClientSecret` 环境变量提供密钥。

## 登录与鉴权流程

登录、OIDC 和 introspection 行为保持不变：

1. 浏览器通过 `/api/auth/login` 进入 ASP.NET Core OpenID Connect Authorization Code + PKCE。
2. Cookie 请求由 OIDC 验证身份；Bearer 请求校验 JWT 的签名、issuer、audience 和有效期。
3. 两种身份都会通过 `/connect/introspect` 检查 `sub`、client、issuer、scope 和撤销状态。
4. 受保护业务接口再把 token 中的 `sub` 与配置的 service、domain 组合成业务 key，直接读取并严格解析 NexusAuthorization Redis String 策略。

令牌与 introspection 请求的客户端认证方式由 `Auth:TokenEndpointAuthMethod` 控制。默认 `client_secret_basic` 必须与 NexusAuth 中登记的方式一致；也可以把两端同步改为 `client_secret_post`，无需修改代码。

Demo 不刷新 token。access token 到期后需要重新登录并取得新 token。

`GET /api/auth/login` 会直接返回 `302` 跳转到登录中心。登录成功后回到 `/swagger/index.html`。请在新标签页直接访问 `http://localhost:5055/api/auth/login` 完成登录，再回到 Swagger 使用 Cookie。不要从 Swagger Try it out 发起登录，跨站 `302` 跳转可能被浏览器 fetch 流程拦截。

受保护接口也可以使用原始 access token：

```http
Authorization: Bearer <access token>
```

Swagger 的 Authorize 输入框可直接填入不带 `Bearer ` 前缀的 access token。

## Redis 读取契约

记配置中的 `KeyPrefix` 为 `prefix`。业务 key 中不包含应用段：

```text
prefix:<QueryEscape(trim(ServiceResource))>:<QueryEscape(trim(sub))>[:<QueryEscape(normalizedDomain)>]
```

- 类型为 Redis STRING，值为一个用户、一个服务和归一化 domain 的完整策略 JSON。
- JSON 字段为 `subject`、`domain`、`service_resource`、`service_active`、`catalog`、`apis` 和 `roles`，不含 `application_key` 或 `application_id`。
- JSON 由中心合并所有启用应用对该服务的授权；app 不覆盖其他 app，也不参与业务 key。
- `ServiceResource` 和 `sub` 都先 `trim`，再按 Go `url.QueryEscape` 编码。Go `QueryEscape` 使用 `+` 表示空格。
- `Domain` 也先 `trim`；空字符串和字面量 `global` 都归一化为空域，且不追加后缀。具名 domain 经 `QueryEscape` 后追加。
- 每个 String value 上限 16 MiB，key 不设 TTL。

当前 Demo 配置下，`sub = 51b21204-54ab-4da6-ad83-d14f72ced46f`、服务 `nexusauthorization.demo.api`、空域的业务 key 为：

```text
nexusauthorization:prod:nexusauthorization.demo.api:51b21204-54ab-4da6-ad83-d14f72ced46f
```

Demo 对该 key 直接执行 `GET`。读取后要求 JSON 合法，`subject`、`service_resource` 和归一化 `domain` 与请求身份完全一致，并检查 `service_active`。String 缺失、类型错误、JSON 损坏、身份不匹配、超过 16 MiB、Redis 超时或不可用时拒绝请求。Demo 不读取 `rbac:api:app:*`、`state`、members、锁或发布号，不执行 Lua，也不接受旧应用级 JSON 的 fallback。

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
- 服务资源为 `nexusauthorization.demo.api`，domain 为空，因此使用无域业务 key。
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
| `GET` | `/api/demo/orders` | 已认证，并通过 Redis `Catalog` 与 `apis` 授权 |
| `GET` | `/api/demo/orders/{id:int}` | 已认证，并通过 Redis `Catalog` 与 `apis` 授权；路径参数参与匹配 |
| `GET` | `/api/demo/products` | 已认证，并通过 Redis `Catalog` 与 `apis` 授权 |
| `GET` | `/api/demo/products/{id:int}` | 已认证，并通过 Redis `Catalog` 与 `apis` 授权；路径参数参与匹配 |
| `POST` | `/api/demo/products` | 已认证，并通过 Redis `Catalog` 与 `apis` 授权 |
| `PUT` | `/api/demo/products/{id:int}` | 已认证，并通过 Redis `Catalog` 与 `apis` 授权；路径参数参与匹配 |
| `DELETE` | `/api/demo/products/{id:int}` | 已认证，并通过 Redis `Catalog` 与 `apis` 授权；路径参数参与匹配 |
| `GET` | `/api/demo/reports/summary` | 已认证，并通过 Redis `Catalog` 与 `apis` 授权 |
| `POST` | `/api/demo/reports/export` | 已认证，并通过 Redis `Catalog` 与 `apis` 授权 |
| `GET` | `/api/public/ping` | 匿名 |
| `GET` | `/api/public/info` | 匿名 |

控制器迁移后，健康检查、认证、订单等原有端点路径保持不变。

商品创建和更新的请求体格式如下：

```json
{
  "name": "演示商品",
  "price": 49.90
}
```

`name` 必填且最长 100 个字符；`price` 必须在 `0.01` 到 `1000000` 之间。请求先通过认证和 Redis 授权，再由数据注解和 `[ApiController]` 自动校验；不符合约束时返回 `400`。

`POST /api/demo/products` 返回 `201 Created`，`Location` 指向 `/api/demo/products/3`，响应体包含 id 为 `3` 的回显数据及 `persisted=false`。Demo 只回显请求，不保存数据，因此随后访问该详情仍会返回 `404`。更新和删除响应同样包含 `persisted=false`，删除只返回“演示删除，不保存数据”。

`POST /api/demo/reports/export` 下载文件名 `demo-orders.csv` 的 UTF-8 CSV，媒体类型为 `text/csv`。文件包含 `id,number,amount` 表头和 `DEMO-001`、`DEMO-002` 两条固定订单数据。

匿名接口不要求 Redis；`/api/auth/me` 只要求认证；订单、商品和报表接口使用 `DemoAuthenticationDefaults.PermissionPolicy`，未登录返回 `401`，已登录但没有可放行的 Redis 策略返回 `403`。

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

Demo 不依赖数据库直连，也不需要配置权限中心的 `BaseUrl`、`ServiceCredential` 或服务调用凭证。登录完成后，可通过 `GET /api/auth/me` 确认当前 token 的 `sub`。

## 推荐启动与切换顺序

1. 在 NexusAuth 确认 Demo 客户端、回调地址、scope 和 audience 已登记，并让令牌认证方式与 Demo 配置一致。
2. 将中心 `NEXUSAUTHORIZATION_REDIS_KEY_PREFIX` 与 Demo `Redis:KeyPrefix` / `Redis__KeyPrefix` 配成同一个值。默认生产值为 `nexusauthorization:prod`，默认开发值为 `nexusauthorization:dev`；前缀可按部署配置。
3. 进入维护窗口，停止旧 Hash reader/writer 的鉴权流量，包括旧中心、旧 Demo、旧 SDK 和其他读写进程；新旧协议不能混跑。
4. 启动新版中心并执行全量重建，确认新 String 写入专用业务 key 且没有发布失败。不要读取旧 `rbac:api:app` 辅助 key 作为 readiness。
5. 中心发布成功后启动新版 Demo：`dotnet run --project demo/dotnet`，默认监听 `http://localhost:5055`。启动前在本机设置 `Auth:ClientSecret` 和 `Redis:Password`，并确认 `AuthorizationCenter` 与 `Redis` 的非敏感配置正确。
6. 打开 `http://localhost:5055/swagger`；使用 Cookie 时，在新标签页直接访问 `http://localhost:5055/api/auth/login`。不使用 Cookie 时，在 Swagger Authorize 或 `.http` 文件中填入原始 access token。
7. 先验收允许、拒绝、String 缺失、类型/JSON 损坏和身份不匹配。确认旧 reader 已全部停止后，才可按旧 key 形状定向清理旧 Hash/state key；本次文档同步不连接现网、不清理、不部署。

## 集成测试

测试用户固定为 `51b21204-54ab-4da6-ad83-d14f72ced46f`。本地测试在 testhost 中注入该 `sub`，覆盖真实 Controller、身份 handler、JSON 解析和 StackExchange.Redis 路径。

SDK 三类库验证（2026-10-26）：144 项 SDK 单元测试、30 项真实 HTTP 集成测试及 49 项临时 Redis 集成测试通过，共 223 项，0 失败；4 项现网 Redis 测试未启用。新增覆盖规则匹配边界、宿主启动校验、Provider 释放与外部连接所有权、MVC 过滤器上下文，以及完整 HTTP 授权链路中的拒绝/撤销、超时、重定向、响应限制和用户隔离。测试发现并修复空模板可能匹配根路径、可释放 Provider 被重复释放的问题。TRX 报告位于 `demo/TestResults/sdk-expanded-20261026/release-final`。此次验证没有访问现网 Redis。

本轮验证（2026-10-25）：Go `go test -race -count=1 ./...` 使用隔离临时 PostgreSQL 与 miniredis 全部包通过，覆盖多应用 union、服务移除、跨实例锁和单连接池；`go vet ./...` 通过。Demo 构建成功，0 个警告、0 个错误；修正一个测试预期后，45 项本地 String 协议集成测试全部通过。本轮没有访问现网 Redis，新版中心与 Demo 尚未部署，因此旧 12 项现网记录不能作为新协议通过结论。

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

`Category=LiveRedis` 只运行只读现网测试，不启动临时 Docker Redis，也不发布、清理或改写现网授权数据。新协议切换后，应以 String 业务 key 和严格身份解析路径重新执行并单独记录结果。`demo/tests/Demo.Api.IntegrationTests/live-authorization.json` 记录业务期望，仍应验证：

- `GET /api/demo/orders` 返回 `200`。
- `GET /api/demo/orders/1` 返回 `403`，鉴权决策原因为 `endpoint_not_granted`。
- 其余七个受保护端点返回 `403`，鉴权决策原因为 `endpoint_not_found`。

这些期望用于避免“所有请求都被拒绝”被误判为测试成功。调整用户、应用、服务、domain、角色或授权后，需要根据目标状态手工修改 `live-authorization.json`；不得按测试实际结果自动改写期望。

## 状态码与限制

- 未登录调用 `/api/auth/me`、订单、商品或报表接口返回 `401`；Public、Health 和首页路由保持匿名。
- 已登录但业务 String 缺失、Redis 类型错误、JSON 损坏、身份不匹配、超过 16 MiB、Redis 超时或不可用时，业务接口返回 `403`，不回退授权。
- 已登录但 `Catalog` 无匹配项、目录歧义、目录项禁用或获胜 API ID 未授权时返回 `403`。
- 商品请求体缺少 `name`、`name` 超过 100 个字符或 `price` 超出范围时，在已获授权后返回 `400`。
- 未设置 `Auth:ClientSecret` 时，登录入口返回 `503`；令牌 introspection 失败或身份不满足要求时，受保护请求返回 `401`。
- Demo 不保存数据库状态，也不缓存 Redis 授权结果；每个受保护请求都会直接读取完整业务 String，不访问 readiness、Lua 或其他 RBAC 辅助 key。
- access token 到期后需重新执行登录流程；SSO 登出需要浏览器继续打开 `/api/auth/logout` 返回的 `logoutUrl`。

## invalid_client 排查

如果 `Auth:TokenEndpointAuthMethod` 与 NexusAuth 客户端登记的令牌认证方式不一致，`/connect/token` 和 `/connect/introspect` 都会拒绝 Basic 与表单凭据错配的请求。

NexusAuth 的 `401` 响应只返回 `{ "error": "invalid_client" }`，没有 `error_description`。这表示客户端认证失败，不表示网络请求未到达服务端。
