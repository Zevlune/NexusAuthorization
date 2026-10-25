# NexusAuthorization .NET 接入 Demo

解决方案入口为 `demo/NexusAuthorization.Demo.sln`，可使用 Visual Studio 或 Rider 直接打开。在仓库根目录执行 `dotnet build demo/NexusAuthorization.Demo.sln` 构建，使用 `dotnet run --project demo/dotnet` 启动。

这是一个采用 Controllers 模式的 .NET 10 示例。Demo 使用 OpenID Connect 完成统一登录，继续通过 introspection 校验 Cookie 或 Bearer 所代表的访问令牌，并使用 StackExchange.Redis 2.13.1 从 NexusAuthorization 发布的共享业务 Hash 读取当前应用 field。Demo 不访问数据库，也不需要权限中心服务调用凭证。

## 项目结构

`demo/dotnet/Controllers` 放置全部入口和业务接口，包括 Home、Health、Auth、Orders、Products、Reports 和 Public。`Program.cs` 通过 `AddControllers()` 和 `MapControllers()` 注册控制器，并配置登录、认证、令牌 introspection 和 Redis 授权策略。OIDC 协议回调 `/signin-oidc` 由认证中间件处理。

`Program.cs` 通过 `AddSingleton<IConnectionMultiplexer>(sp => ConnectionMultiplexer.Connect(options))` 注册共享 Redis 连接，由 DI 容器负责创建和释放。`RedisAuthorizationStore` 通过构造函数注入 `IConnectionMultiplexer`，在每次受保护请求中执行 Lua，原子读取应用内部发布状态，ready 后再从共享业务 Hash 读取当前应用 field。

StackExchange.Redis 的 multiplexer 线程安全，适合在整个应用内共享复用；`GetDatabase()` 只返回轻量数据库句柄，不必另行缓存。可参考[官方基础用法](https://stackexchange.github.io/StackExchange.Redis/Basics.html)。

`SubjectApiPolicyMatcher` 根据请求方法和完整请求路径，从策略中的 `Catalog` 选择接口并检查授权。Demo 不缓存授权结果，也不回源数据库或权限中心 HTTP API。

`demo/dotnet/Demo.Api.http` 提供匿名访问、Bearer 调用、商品增删改查、参数校验和 CSV 导出的请求示例，可在支持 `.http` 文件的 IDE 中运行，也可在 Swagger 中测试。

`demo/tests/Demo.Api.IntegrationTests` 是解决方案中的正式集成测试项目，使用真实 Controller、授权 handler、Lua 和 StackExchange.Redis。测试边界见下文“集成测试”。

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

`AuthorizationCenter` 使用 `AppKey`、`ServiceResource` 和 `Domain`。`AppKey` 继续保留：它不再出现在业务 key 中，而是作为共享业务 Hash 的 field，用于隔离同一服务、主体和 domain 下不同应用的权限。Demo 当前配置为应用 `bugmanager.admin`、服务资源 `nexusauthorization.demo.api`、空域策略。配置在启动时读取，修改后需重启 Demo。

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
4. 受保护业务接口再以 token 中的 `sub` 读取 NexusAuthorization Redis 策略。

令牌与 introspection 请求的客户端认证方式由 `Auth:TokenEndpointAuthMethod` 控制。默认 `client_secret_basic` 必须与 NexusAuth 中登记的方式一致；也可以把两端同步改为 `client_secret_post`，无需修改代码。

Demo 不刷新 token。access token 到期后需要重新登录并取得新 token。

`GET /api/auth/login` 会直接返回 `302` 跳转到登录中心。登录成功后回到 `/swagger/index.html`。请在新标签页直接访问 `http://localhost:5055/api/auth/login` 完成登录，再回到 Swagger 使用 Cookie。不要从 Swagger Try it out 发起登录，跨站 `302` 跳转可能被浏览器 fetch 流程拦截。

受保护接口也可以使用原始 access token：

```http
Authorization: Bearer <access token>
```

Swagger 的 Authorize 输入框可直接填入不带 `Bearer ` 前缀的 access token。

## Redis 读取契约

记配置中的 `KeyPrefix` 为 `prefix`。业务 Hash key 中不包含 app 段：

```text
prefix:<QueryEscape(trim(ServiceResource))>:<QueryEscape(trim(sub))>[:<QueryEscape(normalizedDomain)>]
```

- 类型为 Redis HASH。
- field 是去除首尾空白后的 `AppKey` 原文；field 名不做 `QueryEscape`。
- field value 是原始 `SubjectAPIPolicy` JSON，没有新 JSON 外壳。
- `ServiceResource` 和 `sub` 都先 `trim`，再按 Go `url.QueryEscape` 编码。Go `QueryEscape` 使用 `+` 表示空格。
- `Domain` 也先 `trim`；空字符串和字面量 `global` 都归一化为空域，且不追加后缀。具名 domain 经 `QueryEscape` 后追加。
- 每个 field value 上限 16 MiB，Hash 不设 TTL。

应用内部元数据不使用业务 Hash，统一位于：

```text
prefix:rbac:api:app:<QueryEscape(trim(AppKey))>:state
prefix:rbac:api:app:<QueryEscape(trim(AppKey))>:members
prefix:rbac:api:app:<QueryEscape(trim(AppKey))>:lock
prefix:rbac:api:app:<QueryEscape(trim(AppKey))>:requested
prefix:rbac:api:app:<QueryEscape(trim(AppKey))>:published
```

当前 Demo 配置下，`sub = 51b21204-54ab-4da6-ad83-d14f72ced46f`、服务 `nexusauthorization.demo.api`、空域的业务 key 为：

```text
nexusauthorization:prod:nexusauthorization.demo.api:51b21204-54ab-4da6-ad83-d14f72ced46f
```

应用 `bugmanager.admin` 的策略保存在该 Hash 的 `bugmanager.admin` field。应用内部 readiness key 为：

```text
nexusauthorization:prod:rbac:api:app:bugmanager.admin:state
```

Lua 先用同一个原子操作执行 `GET` 应用内部 `state`；仅当值为精确的 `ready` 时，才对业务 Hash 执行 `HGET`，field 为 `bugmanager.admin`。脚本返回码保持不变：

- `1`：field 存在，继续校验和匹配策略。
- `2`：应用已 ready，但当前应用 field 不存在，正常拒绝。
- `0`：应用内部 `state` 不存在，按未发布拒绝。
- `-1`：应用 `state` 存在但不是 `ready`，按未就绪拒绝。

应用尚未发布、正在更新、状态缺失、field 缺失或 Redis 不可用时，Demo 都失败关闭，不回退数据库或调用 NexusAuthorization HTTP API。

读取到策略后，Demo 还要求其中的 `application_key`、`subject`、`service_resource` 和归一化 `domain` 与本次请求完全一致；身份不匹配、JSON 损坏、field 超过 16 MiB、超时或其他 Redis 读取失败同样返回 `403`。

该协议在 standalone Redis 上使用跨 key 的原子 Lua 读取，**不支持 Redis Cluster**。

## Catalog API 匹配

策略中的 `Catalog` 是完整 API 目录，匹配顺序如下：

1. 只保留 HTTP 方法与请求方法一致、且 `path_template` 与完整请求路径匹配的目录项。方法比较忽略大小写。
2. 路径按 `/` 分段；字面 segment 必须完全一致，`{参数}` 只匹配一个非空 segment。中心从 Swagger 导入的 `/api/demo/orders/{id}` 模板可匹配实际请求 `/api/demo/orders/1`。MVC 的 `:int` 约束仍由控制器路由处理。
3. 在所有匹配项中选择字面 segment 数最多的一项。字面路径通常优先于参数路径。
4. 最高分出现不同 ID 的并列项时按歧义拒绝。
5. 获胜目录项为 `disabled` 时拒绝；否则仅当获胜项的 ID 出现在策略 `apis` 中时放行。

没有目录匹配项、目录项禁用、目录歧义或获胜 ID 未授权都返回 `403`。

## 目标 Redis 授权记录

按当前已确认配置，新协议下预期读取：

- 应用 `bugmanager.admin` 的内部 gate 状态为 `ready`，位于 `rbac:api:app` 元数据 namespace。
- 用户 `51b21204-54ab-4da6-ad83-d14f72ced46f` 拥有角色 `order-review`。
- 服务资源为 `nexusauthorization.demo.api`，domain 为空，因此使用无域业务 Hash key。
- 应用策略在 `nexusauthorization:prod:nexusauthorization.demo.api:51b21204-54ab-4da6-ad83-d14f72ced46f` Hash 的 `bugmanager.admin` field。
- Catalog 只有 `GET /api/demo/orders` 和 `GET /api/demo/orders/{id}` 两项。
- `GET /api/demo/orders` 已授予；`GET /api/demo/orders/{id}` 未授予。

这些是切换后的验收目标，不代表本次文档修改已验证现网 Redis。该用户访问订单列表时应通过授权；访问订单详情时命中 Catalog 但没有 API 授予，应返回 `403`，鉴权日志中的原因为 `endpoint_not_granted`。其他七个受保护业务端点不在当前 Catalog 中，应返回 `403`，原因为 `endpoint_not_found`。原因记录在服务端日志中，不包含在 HTTP 响应体中。

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
4. 新版中心重建后确认 `rbac:api:app` 内部 `state` 为 `ready`，且共享业务 Hash 的 `bugmanager.admin` field 可读。

Demo 不依赖数据库直连，也不需要配置权限中心的 `BaseUrl`、`ServiceCredential` 或服务调用凭证。登录完成后，可通过 `GET /api/auth/me` 确认当前 token 的 `sub`。

## 推荐启动与切换顺序

1. 在 NexusAuth 确认 Demo 客户端、回调地址、scope 和 audience 已登记，并让令牌认证方式与 Demo 配置一致。
2. 从中心 `NEXUSAUTHORIZATION_REDIS_KEY_PREFIX=nexusauthorization-prod`、Demo `Redis:KeyPrefix=nexusauthorization-prod` 或对应 `Redis__KeyPrefix` 迁移的环境，先将 YAML、容器与部署环境变量统一改为 `nexusauthorization:prod`；开发环境统一为 `nexusauthorization:dev`。配置前缀已包含环境，不要再拼接第二段 `prod`/`dev`。
3. 进入维护窗口，停止旧中心、旧 Demo 和其他旧 String 协议读端的鉴权流量；旧读端不会看到新版撤权。
4. 升级 NexusAuthorization 中心，等待应用在 `rbac:api:app` namespace 下的新 `state` 为 `ready`，确认共享业务 Hash 中有 `bugmanager.admin` field。不要把旧 String key 当作新协议 readiness。
5. 在新版中心 ready 后启动新版 Demo：`dotnet run --project demo/dotnet`，默认监听 `http://localhost:5055`。启动前在本机设置 `Auth:ClientSecret` 和 `Redis:Password`，并确认 `AuthorizationCenter` 与 `Redis` 的非敏感配置正确。
6. 打开 `http://localhost:5055/swagger`；使用 Cookie 时，在新标签页直接访问 `http://localhost:5055/api/auth/login`。不使用 Cookie 时，在 Swagger Authorize 或 `.http` 文件中填入原始 access token。
7. 只有新中心 ready、所有新读端完成切换且旧读端确认停止后，才按精确环境前缀定向清理旧 String key。不得使用 `FLUSHDB`、整库删除、全库 scan-and-delete 或清空共享前缀。

## 集成测试

测试用户固定为 `51b21204-54ab-4da6-ad83-d14f72ced46f`。本地测试在 testhost 中注入该 `sub`，未替换权限判断；应用的真实 Controller、授权 handler、Lua 和 StackExchange.Redis 仍参与验证。

2026-10-24 验证：Demo 解决方案构建成功，0 个警告、0 个错误；Go 服务端 `go test ./...` 全部通过；`dotnet test demo/NexusAuthorization.Demo.sln --no-build --no-restore --filter 'Category!=LiveRedis'` 的 38 项本地集成测试全部通过。测试使用独立临时 Redis 容器，未连接现网 Redis。此前旧 String 协议记录的 49 项测试属于历史结果，不作为新协议或现网验证结论。

默认执行：

```sh
dotnet test demo/NexusAuthorization.Demo.sln
```

该命令会启动测试自己的临时 `redis:7-alpine` Docker 容器，因此需要本机 Docker 正在运行。容器只接收测试 fixture 生成的数据，测试完成后自动清理。新协议测试覆盖九个受保护端点的允许与未认证 `401`、角色缺权 `403`、撤权只删除当前应用 field、gate 未就绪、field 缺失/损坏、路径目录匹配和业务状态码等场景；本次本地集成测试全部通过。

测试身份注入只服务于集成测试，不能证明真实 OIDC 登录、令牌签名/issuer/audience 或 introspection 校验正确；这些边界仍需通过 Demo 登录流程验证。

只读现网授权测试使用真实 Demo 配置、本机 user-secrets、环境变量、实际 Redis 和上述用户：

```sh
NEXUS_DEMO_RUN_LIVE_TESTS=1 dotnet test demo/NexusAuthorization.Demo.sln --filter 'Category=LiveRedis'
```

`Category=LiveRedis` 只运行现网测试，不启动临时 Docker Redis，也不发布、清理或改写现网授权数据。`demo/tests/Demo.Api.IntegrationTests/live-authorization.json` 明确记录期望，默认必须同时验证：

- `GET /api/demo/orders` 返回 `200`。
- `GET /api/demo/orders/1` 返回 `403`，鉴权决策原因为 `endpoint_not_granted`。
- 其余七个受保护端点返回 `403`，鉴权决策原因为 `endpoint_not_found`。

这些期望用于避免“所有请求都被拒绝”被误判为测试成功。调整用户、应用、服务、domain、角色或授权后，需要根据目标状态手工修改 `live-authorization.json`；不得按测试实际结果自动改写期望。

## 状态码与限制

- 未登录调用 `/api/auth/me`、订单、商品或报表接口返回 `401`；Public、Health 和首页路由保持匿名。
- 已登录但应用未发布、正在更新、当前应用 field 缺失、Redis 超时或不可用、field 损坏或身份不匹配时，业务接口返回 `403`，不回退授权。
- 已登录但 `Catalog` 无匹配项、目录歧义、目录项禁用或获胜 API ID 未授权时返回 `403`。
- 商品请求体缺少 `name`、`name` 超过 100 个字符或 `price` 超出范围时，在已获授权后返回 `400`。
- 未设置 `Auth:ClientSecret` 时，登录入口返回 `503`；令牌 introspection 失败或身份不满足要求时，受保护请求返回 `401`。
- Demo 不保存数据库状态，也不缓存 Redis 授权结果；每个受保护请求都会重新读取应用内部状态和共享业务 Hash 的当前应用 field。
- access token 到期后需重新执行登录流程；SSO 登出需要浏览器继续打开 `/api/auth/logout` 返回的 `logoutUrl`。

## invalid_client 排查

如果 `Auth:TokenEndpointAuthMethod` 与 NexusAuth 客户端登记的令牌认证方式不一致，`/connect/token` 和 `/connect/introspect` 都会拒绝 Basic 与表单凭据错配的请求。

NexusAuth 的 `401` 响应只返回 `{ "error": "invalid_client" }`，没有 `error_description`。这表示客户端认证失败，不表示网络请求未到达服务端。
