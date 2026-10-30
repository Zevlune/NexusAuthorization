# NexusAuthorization .NET SDK

本目录提供 NexusAuthorization 的 .NET 接入组件。SDK 由三个独立的
`net8.0` 类库组成，版本均为 `0.1.0`。身份认证仍由接入应用负责；SDK
只依据可信认证主体中的 `subject`、请求方法和请求路径检查业务权限。

| 项目 | 职责 |
| --- | --- |
| `NexusAuthorization.Sdk` | 核心契约、`NexusAuthorizationBuilder`、通用配置、ASP.NET Core policy/handler 和 `NexusAuthorizeAttribute`。 |
| `NexusAuthorization.Sdk.Redis` | Redis Provider、Redis 配置、策略模型和匹配器。 |
| `NexusAuthorization.Sdk.Http` | 调用授权中心 HTTP API 的 Provider 和 HTTP 配置。 |

三个项目的 namespace 分别是 `NexusAuthorization.Sdk`、
`NexusAuthorization.Sdk.Redis` 和 `NexusAuthorization.Sdk.Http`。
ASP.NET Core 集成已经合并到核心项目，namespace 仍为
`NexusAuthorization.Sdk`。旧的 `NexusAuthorization.AspNetCore`
项目和同名包已不再存在。

核心项目引用了 `Microsoft.AspNetCore.App` 共享框架。因此，即使应用只使用
核心契约，部署到控制台进程时也需要安装 ASP.NET Core Runtime，而不只是
.NET Runtime。Redis 项目只引入 Redis 实现所需的 `StackExchange.Redis`；
HTTP 项目只引入 HTTP 实现所需的 `Microsoft.Extensions.Http`。核心项目不引用
任一实现项目。

当前仓库没有把这三个包发布到 NuGet.org，不能把 `0.1.0` 当作线上包从公网源
安装；接入方式见“打包与引用”。

## 选择接入方式

接入时先注册公共配置，再通过 Builder 显式选择一个 Provider：

```csharp
using NexusAuthorization.Sdk;
using NexusAuthorization.Sdk.Redis;

var section = builder.Configuration.GetSection("NexusAuthorization");

builder.Services
    .AddNexusAuthorization(options => section.Bind(options))
    .UseRedis(options => section.GetSection("Redis").Bind(options));
```

HTTP 接入只需把实现 namespace 和扩展方法替换为：

```csharp
using NexusAuthorization.Sdk;
using NexusAuthorization.Sdk.Http;

var section = builder.Configuration.GetSection("NexusAuthorization");

builder.Services
    .AddNexusAuthorization(options => section.Bind(options))
    .UseHttp(options => section.GetSection("Http").Bind(options));
```

`AddNexusAuthorization` 接收 `Action<NexusAuthorizationOptions>`，
也可不传参数后返回 Builder，不再提供 `IConfiguration` 重载。需要绑定配置文件时，
在 Action 中调用 `section.Bind(options)`。`UseRedis` 和 `UseHttp` 各自只保留一个
接收配置 Action 的方法，返回类型为 `void`，不提供无参数或 `IConfiguration` 重载。
Redis 与 HTTP 的配置文件绑定也在 Action 中完成：

```json
{
  "NexusAuthorization": {
    "ServiceResource": "orders.api",
    "SubjectClaimType": "sub",
    "Redis": {
      "Host": "redis.example.internal",
      "Port": 6379,
      "User": "",
      "Password": "",
      "Database": 0,
      "KeyPrefix": "nexusauthorization:prod",
      "TimeoutMilliseconds": 2000,
      "Ssl": false,
      "SslHost": null
    },
    "Http": {
      "BaseUrl": "https://authorization.example.com",
      "AppKey": "orders",
      "ServiceCredential": "",
      "TimeoutMilliseconds": 3000
    }
  }
}
```

实际应用通常只保留所选 Provider 的子节。示例同时列出两者，是为了说明各自的
配置位置。SDK 没有 `Provider` 配置枚举，也不会根据配置值自动切换 Redis 和
HTTP；选择哪个 `UseRedis` 或 `UseHttp` 由接入应用的启动代码决定。

Redis 示例中的 `KeyPrefix` 假定中心 `LUCKENV=prod`。中心当前按
`nexusauthorization:<QueryEscape(trim(LUCKENV))>` 推导前缀，SDK 不会读取
`LUCKENV` 或 `SERVICEUK`；接入应用必须把 `KeyPrefix` 设置为完全相同的推导值，
并让 `ServiceResource` 与接入服务在中心登记的标识一致。中心不再接受
`NEXUSAUTHORIZATION_REDIS_KEY_PREFIX` 作为前缀覆盖。

## ASP.NET Core 快速接入

保留应用已有的登录、令牌校验、认证注册和授权管道。SDK 不替代
`AddAuthentication()`，并要求经过 SDK policy 的请求已经由
`UseAuthentication()` 建立认证主体。

Redis 项目的标准接入如下：

```csharp
using NexusAuthorization.Sdk;
using NexusAuthorization.Sdk.Redis;

var section = builder.Configuration.GetSection("NexusAuthorization");

builder.Services
    .AddNexusAuthorization(options => section.Bind(options))
    .UseRedis(options => section.GetSection("Redis").Bind(options));

// 保留应用现有的认证方案和授权配置。
builder.Services.AddAuthentication(/* ... */);

var app = builder.Build();

// 保留应用现有的中间件顺序。
app.UseAuthentication();
app.UseAuthorization();
```

随后直接使用核心 namespace 中的 `NexusAuthorizeAttribute`：

```csharp
using Microsoft.AspNetCore.Mvc;
using NexusAuthorization.Sdk;

[ApiController]
[NexusAuthorize]
[Route("api/orders")]
public sealed class OrdersController : ControllerBase
{
    [HttpGet("{id:int}")]
    public ActionResult<Order> Get(int id) =>
        Ok(new Order(id, $"DEMO-{id:000}"));
}

public sealed record Order(int Id, string Number);
```

`[NexusAuthorize]` 等价于
`[Authorize(Policy = NexusAuthorizationDefaults.PolicyName)]`。policy 名称为
`NexusAuthorization`。未认证请求返回 `401`；已认证但缺少配置的主体声明，或
Provider 没有给出明确 allow 决策的请求返回 `403`。

`AddNexusAuthorization` 注册命名 policy 及对应的 `NexusAuthorizationHandler`，
它与宿主的其他 handler 一起参与授权，不会替换框架 handler。SDK 不设置默认或
fallback policy；仅使用 `[Authorize]` 执行宿主默认策略，未标注授权策略的端点
不会自动启用 Nexus 检查。Controller 上的 Nexus 策略会作用于其 Action，
`[AllowAnonymous]` 可以跳过授权。

handler 使用当前 HTTP 方法、完整请求路径和认证主体中的
`NexusAuthorization:SubjectClaimType` 调用统一接口。身份认证、subject 来源和
其他业务 authorization policy 仍由应用维护。

### 使用 Action 配置

不使用配置文件绑定也可以直接配置公共选项和所选实现：

```csharp
builder.Services
    .AddNexusAuthorization(options =>
    {
        options.ServiceResource = "orders.api";
        options.SubjectClaimType = "sub";
    })
    .UseRedis(options =>
    {
        options.Host = "redis.example.internal";
        options.Port = 6379;
        options.KeyPrefix = "nexusauthorization:prod";
    });
```

公共 `NexusAuthorizationOptions` 只有：

- `ServiceResource`：必填，当前应用的服务资源标识。
- `SubjectClaimType`：认证主体中可信 subject 的声明类型，默认 `sub`。

SDK 仅支持默认域，不提供可配置域，也不对外暴露 `NormalizeDomain` API。

Redis 的 `RedisAuthorizationOptions` 和 HTTP 的
`HttpAuthorizationOptions` 与公共选项分属各自程序集。

## 普通 .NET 服务

普通服务通过依赖注入获取统一的 `INexusAuthorizationProvider`。通常引用
Redis 或 HTTP 项目，让所选实现项目和核心契约一起进入应用：

```csharp
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using NexusAuthorization.Sdk;
using NexusAuthorization.Sdk.Http;

var builder = Host.CreateApplicationBuilder(args);
builder.Services
    .AddNexusAuthorization(options =>
    {
        options.ServiceResource = "orders.api";
        options.SubjectClaimType = "sub";
    })
    .UseHttp(options =>
    {
        options.BaseUrl = "https://authorization.example.com";
        options.AppKey = "orders";
        options.ServiceCredential = builder.Configuration["AuthorizationServiceCredential"]
            ?? throw new InvalidOperationException("后台调用需要固定服务凭证。");
    });

using var host = builder.Build();
await host.RunAsync();
```

业务代码只依赖核心接口：

```csharp
using NexusAuthorization.Sdk;

public sealed class OrdersService(INexusAuthorizationProvider authorization)
{
    public async Task<bool> CanReadAsync(
        string subject,
        string path,
        CancellationToken cancellationToken)
    {
        var decision = await authorization.AuthorizeAsync(
            subject,
            "GET",
            path,
            cancellationToken);

        return decision is { Allowed: true, Effect: "allow", Reason: "allowed" };
    }
}
```

统一入口为
`AuthorizeAsync(subject, method, path, cancellationToken)`，返回
`AuthorizationDecision`。只有 `Allowed=true`、`Effect="allow"` 且
`Reason="allowed"` 才表示明确放行。`subject` 必须来自应用自己的可信认证上下文，
不能直接采用客户端未经验证的自报值。

## Builder 与 Provider 扩展

通过 Builder 注册授权实现：

- `UseRedis(...)` 选择 `RedisAuthorizationStore`。
- `UseHttp(...)` 选择 `HttpAuthorizationProvider`。
- `UseProvider<TProvider>()` 将自定义 `INexusAuthorizationProvider` 注册为所选实现。

`UseRedis` 和 `UseHttp` 返回 `void`，作为注册表达式的最后一步。需要多次调用时，先
保存 `AddNexusAuthorization(...)` 返回的 Builder，再分别调用对应方法。

业务方通过 `INexusAuthorizationProvider` 获取实现。Builder 使用
`AddScoped<INexusAuthorizationProvider, TProvider>()` 注册：同一作用域内复用实例，
不同请求使用不同实例，作用域结束时由容器释放。Provider 可以直接注入其他 Scoped
服务。只注册统一接口，避免可释放实例被重复释放。
如需使用调用方已创建的实例，可调用 `UseProvider(instance)`；该实例由调用方负责
释放，作为单例共享。

ASP.NET Core 会为每个请求创建作用域。后台任务或单例服务通过
`IServiceScopeFactory` 创建作用域，再解析 Provider：

```csharp
await using var scope = scopeFactory.CreateAsyncScope();
var authorization = scope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>();
var decision = await authorization.AuthorizeAsync(subject, "GET", path, cancellationToken);
```

每次 `UseProvider` 都移除已有的 `INexusAuthorizationProvider` 注册，再添加当前
实现；最后一次调用生效。`UseRedis` 和 `UseHttp` 通过同一方法选择 Provider。
Builder 不记录已选类型，也不额外检查重复或冲突，容器最终只保留一个统一接口注册。
已有的手动接口注册也可以被 Builder 替换。未注册 Provider 时，解析依赖它的服务会
由 DI 报告缺失依赖。

通常只调用所需的 `UseRedis` 或 `UseHttp`。如果两者都调用，最后一次选择决定授权
实现；前面已经注册的连接工厂和 HTTP 客户端仍保留在容器中。

核心项目不引用 Redis 或 HTTP 实现。选择 Redis 时不会注册 HTTP 客户端；选择
HTTP 时不会注册 Redis 连接。只引用某个实现包也不会自行选择 Provider。

`UseProvider<TProvider>()` 也为未来的客户端实现预留了统一扩展点。例如自定义
gRPC Provider 可以使用真实的 Provider 和 client 类型完成注册：

```csharp
var nexus = builder.Services
    .AddNexusAuthorization(options => options.ServiceResource = "orders.api");

nexus.UseProvider<MyGrpcAuthorizationProvider>();
nexus.Services.AddGrpcClient<MyAuthorizationClient>(options =>
{
    options.Address = new Uri("https://authorization.example.com");
});
```

SDK 当前不提供 `UseGrpc` API、服务端 proto 或内置 gRPC Provider。
`MyGrpcAuthorizationProvider` 必须由接入方实现
`INexusAuthorizationProvider`，并负责把统一授权调用映射到自己的协议。

## HTTP 授权协议

HTTP 模式的 `HttpAuthorizationOptions` 包含：

- `BaseUrl`：授权中心根地址。
- `AppKey`：调用方应用标识，作为请求 JSON 的 `appKey` 发送。
- `ServiceCredential`：可选的固定 Bearer 凭证；默认空值，转发当前请求的 Bearer Token。
- `TimeoutMilliseconds`：默认 `3000` 毫秒；非正数在调用时使用默认值。

SDK 将 `AppKey` 放在 HTTP 实现选项中；Redis 实现的业务 key 和策略身份不使用它。

SDK 向以下地址发起 `POST`：

```text
{Http.BaseUrl}/api/v1/authorize
```

请求 JSON 字段为：

```json
{
  "appKey": "orders",
  "serviceResource": "orders.api",
  "subject": "user-123",
  "method": "GET",
  "path": "/api/orders/1"
}
```

请求 JSON 不发送 `domain`。现有授权中心服务端会把缺失字段按默认域处理。

默认从当前业务请求读取 `Authorization`，向下游发送：

```http
Authorization: Bearer <当前请求的 Token>
```

HTTP 模式默认转发当前入站 `Authorization: Bearer`。`UseHttp` 自动注册
`IHttpContextAccessor`；ASP.NET Core 宿主已提供 Logging，无需额外注册。Provider 每次调用时读取当前请求，令牌只写入
这次出站请求，不保存在共享客户端中。Bearer 方案名不区分大小写；缺少上下文、
没有 Bearer Token、多个 Authorization 值或令牌格式错误时，返回
`bearer_token_required`，不会调用下游。Cookie 不会被自动提取或转换成 Bearer
Token；只存在登录 Cookie 时仍按缺少 Token 处理。`subject` 仍来自应用已经验证的
认证主体。

例如，ASP.NET Core 应用无需配置固定凭证：

```csharp
builder.Services
    .AddNexusAuthorization(options =>
    {
        options.ServiceResource = "orders.api";
    })
    .UseHttp(options =>
    {
        options.BaseUrl = "https://authorization.example.com";
        options.AppKey = "orders";
    });
```

固定 Bearer 凭证一旦设置就覆盖入站 Token，也支持没有 HTTP 上下文的后台任务。
当前仓库 Go 服务的 `/api/v1/authorize` 仍只认匹配的
`authorization.service_credential`，尚不支持用用户访问令牌代替服务凭证。因此，
直接调用该接口时必须显式配置与中心一致的 `ServiceCredential`，包括 Demo 使用
Cookie 登录的场景；Cookie 不会自动提取并保存为可用于调用该接口的令牌。固定
凭证应通过环境变量、user-secrets 或部署平台的 secret 机制注入。

授权中心应返回 `200 OK` 和合法决策 JSON，例如：

```json
{
  "allowed": false,
  "effect": "deny",
  "reason": "endpoint_not_granted"
}
```

SDK 只接受一致的决策：

- allow 必须是 `allowed=true`、`effect=allow`、`reason=allowed`。
- deny 必须是 `allowed=false`、`effect=deny`，且 reason 为非空、不是
  `allowed` 的值。

响应体最大为 `1 MiB`。非 `200` 状态码、声明或实际读取超过上限、非法 JSON、
重复或矛盾字段、网络错误和超时都会 fail closed。SDK 不跟随 HTTP 重定向，
也不发送 Cookie。

HTTP 配置直接使用调用方设置的值。SDK 不注册
`HttpAuthorizationOptions` validator，也不对 `BaseUrl`、`AppKey`、`ServiceCredential`
或超时添加实现层启动校验。

## Redis 读取契约

Redis 模式的业务键格式为：

```text
nexusauthorization:<QueryEscape(trim(LUCKENV))>:<QueryEscape(trim(ServiceResource))>:<QueryEscape(trim(subject))>
```

- 中心 Redis 前缀由必填的 `LUCKENV` 推导；SDK 的 `KeyPrefix` 必须精确等于
  `nexusauthorization:<QueryEscape(trim(LUCKENV))>`。消费者不会根据
  `LUCKENV` 自动推导，也不会读取中心的 `SERVICEUK`。
- SDK 的 `ServiceResource` 使用接入服务在中心登记的服务标识；服务段和
  subject 分别先 `trim`，再执行 `QueryEscape`。
- SDK 对每个受保护请求直接执行一次 Redis `GET`，对应
  `StringGetAsync`。它不缓存策略或授权结果，也不读取 Lua、readiness、锁、
  发布号或其他辅助 key。
- `QueryEscape` 与 Go `url.QueryEscape` 的编码约定一致：先对输入执行 `trim`，
  按 UTF-8 做百分号编码，并使用 `+` 表示空格。
- 业务 key 不含 domain 段或 domain 后缀。SDK 只支持默认域。
- STRING 值必须是对应 `subject` 和 `service_resource` 的完整策略 JSON，最大为
  `16 MiB`。业务 key 不设 TTL。
- 为兼容中心已有的默认域 String 策略，JSON 中的 `domain` 可以缺失、为 `null`、
  为空字符串或为字面量 `global`；经 `trim` 后这些值都等价于默认域。SDK 内部
  仍会检查这份元数据，非默认具名 domain 会以 `policy_identity_mismatch` 拒绝。
- `SubjectApiPolicy` 不对外暴露 `Domain` 属性；内部仍映射该 JSON 字段，仅用于上述
  兼容校验，不表示 SDK 支持具名域。
- 中心写入默认域业务 key 的策略仍合并所有启用应用对该用户和服务的贡献；应用不是
  Redis key 或策略身份的隔离维度。某个应用没有授权时，不会覆盖其他启用应用
  已合并的授权。
- 旧应用级策略不会被当作兼容值读取。策略中出现非空
  `application_key` 或非零 `application_id` 时拒绝；缺失值和零值视为没有
  应用元数据。
- STRING 缺失、类型错误、JSON 损坏、身份不匹配、超过 `16 MiB`、超时或
  Redis 不可用都会 fail closed，不会 fallback 到旧策略、数据库或其他 key。

策略中的 `catalog` 用于定位当前请求，`apis` 保存该 subject 已被授予的 endpoint
ID。SDK 先查找方法名与请求方法相同（不区分大小写）且路径模板匹配的 endpoint；
路径模板按 `/` 分段匹配，`{name}` 段可以匹配一个非空路径段。若多个模板同时匹配，
优先使用字面量段更多的模板；相同优先级命中不同 endpoint 时按歧义拒绝。

找到 endpoint 后，仅当 `service_active=true`、endpoint 的 `enabled=true`，并且
该 endpoint ID 出现在 `apis` 中时才允许。服务未启用、endpoint 未启用、路径找不到
或没有授权都返回拒绝。

Redis 的 `KeyPrefix` 需要与写入策略的 NexusAuthorization 中心完全一致。
SDK 直接使用 `RedisAuthorizationOptions` 中的值，不对 Redis 地址、端口、数据库编号、
超时或 `KeyPrefix` 添加配置校验，因此即使值错误也会启动，并在读取时因 key 缺失而
fail closed。

已有 key 前缀发生变化时，例如中心调整 `LUCKENV` 或从旧的自选前缀迁移，完整
业务 key 已改变。中心必须按新前缀重新全量发布，所有接入应用必须同步更新
`KeyPrefix` 并重启；旧前缀记录不会被自动兼容读取。

## Fail closed

Redis 和 HTTP Provider 都遵循 fail closed：没有拿到完整、合法、明确的 allow
决策时，SDK 拒绝访问。Provider 之间没有自动回退，也不会因 Redis 或授权中心不可用
而放行请求。

## 连接生命周期与 DI

Redis 实现通过 DI 注册单例 `IConnectionMultiplexer`，不同作用域的
`RedisAuthorizationStore` 共享该连接。不要在每次请求中创建连接，也不要自行缓存
授权结果。

如果应用已经注册了 `IConnectionMultiplexer`，SDK 使用
`TryAddSingleton` 保留现有注册，不会覆盖它。外部连接的地址、ACL、TLS、网络环境
和目标策略库必须与当前 Redis 配置一致。SDK 每次通过
`GetDatabase(Redis.Database)` 选择数据库，但不会接管外部实例的释放；是否以及何时
释放外部实例由注册方决定。

HTTP 实现使用命名 `HttpClient` 和 `IHttpClientFactory`。选择 HTTP 不会创建或解析
Redis 连接；已有的 `IHttpContextAccessor` 注册会被保留。

## 配置注册

公共配置要求非空 `ServiceResource` 和非空 `SubjectClaimType`，并通过核心库的
启动校验。`UseRedis` 和 `UseHttp` 只应用各自的配置 Action，并注册 Scoped
Provider 及所需依赖；两者均不注册 `IValidateOptions<T>` 或实现层启动校验。

配置在启动时绑定，修改配置后应重启应用。

## 仓库内 Demo

仓库 Demo 保留 `AuthorizationCenter` 和顶层 `Redis` 的既有配置路径及 user-secrets，
并新增顶层 `Http`。`AuthorizationCenter:Provider` 默认 `Redis`，比较时忽略大小写
和首尾空白，只接受 `Redis`、`Http`；未知值在启动时直接报错。这个开关属于 Demo，
SDK 本身没有 Provider 配置枚举或自动切换逻辑。

`demo/dotnet/Program.cs` 先保存 `AddNexusAuthorization(...)` 返回的 Builder，
再用 `switch` 显式调用 Action-only、`void` 返回的 `UseRedis` 或 `UseHttp`：

```csharp
using NexusAuthorization.Sdk;
using NexusAuthorization.Sdk.Redis;
using NexusAuthorization.Sdk.Http;

var settings = builder.Configuration.Get<DemoSettings>()
    ?? throw new InvalidOperationException("缺少配置。");

var nexus = builder.Services
    .AddNexusAuthorization(settings.ConfigureAuthorization);

switch (settings.AuthorizationCenter.Provider.Trim().ToUpperInvariant())
{
    case "REDIS":
        nexus.UseRedis(settings.ConfigureRedis);
        break;
    case "HTTP":
        nexus.UseHttp(settings.ConfigureHttp);
        break;
    default:
        throw new InvalidOperationException(
            "AuthorizationCenter:Provider 必须为 Redis 或 Http。");
}
```

`DemoSettings.ConfigureAuthorization` 把 `AuthorizationCenter.ServiceResource`
和 `SubjectClaimType` 映射到公共 `NexusAuthorizationOptions`；
`ConfigureRedis` 映射顶层 `Redis`；`ConfigureHttp` 映射顶层 `Http` 的连接选项，
并把 `AuthorizationCenter.AppKey` 赋给 `HttpAuthorizationOptions.AppKey`。
Demo 沿用已有的 `AuthorizationCenter:AppKey` 配置路径，因此 Demo 的 `Http:AppKey`
不会生效；其他项目直接绑定 SDK HTTP 选项时可以在该选项所在配置节设置 `AppKey`。

Demo 同时项目引用 `NexusAuthorization.Sdk.Redis` 和
`NexusAuthorization.Sdk.Http`。两个实现项目都传递引用
`NexusAuthorization.Sdk` 核心项目，因此 Demo 不需要再单独引用核心项目或旧
ASP.NET Core 包。

在 `demo/dotnet/appsettings.json` 设置 `AuthorizationCenter:Provider` 为 `Redis`
或 `Http`，并填写所选实现的连接配置；也可在启动时覆盖：

```sh
AuthorizationCenter__Provider=Http dotnet run --project demo/dotnet
```

完整配置及 Cookie / 服务凭证说明见 [Demo README](../../demo/README.md)。

## 打包与引用

在仓库根目录分别打包三个 csproj：

```sh
dotnet pack sdk/dotnet/NexusAuthorization.Sdk/NexusAuthorization.Sdk.csproj \
  -c Release -o artifacts/packages

dotnet pack sdk/dotnet/NexusAuthorization.Sdk.Redis/NexusAuthorization.Sdk.Redis.csproj \
  -c Release -o artifacts/packages

dotnet pack sdk/dotnet/NexusAuthorization.Sdk.Http/NexusAuthorization.Sdk.Http.csproj \
  -c Release -o artifacts/packages
```

命令会生成三个 `0.1.0` 包：

```text
NexusAuthorization.Sdk.0.1.0.nupkg
NexusAuthorization.Sdk.Redis.0.1.0.nupkg
NexusAuthorization.Sdk.Http.0.1.0.nupkg
```

Redis 和 HTTP 包都以同版本核心包为依赖。接入方引用
`NexusAuthorization.Sdk.Redis` 或 `NexusAuthorization.Sdk.Http` 即可传递获得
核心契约。仅在实现自定义 Provider 或直接使用核心契约时才需要单独引用核心包。

仓库或本地开发可使用 `ProjectReference`：

```xml
<!-- Redis 接入，同时传递引用核心项目 -->
<ProjectReference
  Include="../NexusAuthorization/sdk/dotnet/NexusAuthorization.Sdk.Redis/NexusAuthorization.Sdk.Redis.csproj" />

<!-- HTTP 接入，同时传递引用核心项目 -->
<ProjectReference
  Include="../NexusAuthorization/sdk/dotnet/NexusAuthorization.Sdk.Http/NexusAuthorization.Sdk.Http.csproj" />

<!-- 自定义 Provider 或只使用核心契约 -->
<ProjectReference
  Include="../NexusAuthorization/sdk/dotnet/NexusAuthorization.Sdk/NexusAuthorization.Sdk.csproj" />
```

也可以把 `artifacts/packages` 添加为本地包源：

```sh
dotnet add MyService.csproj package NexusAuthorization.Sdk.Redis \
  --version 0.1.0 --source ./artifacts/packages

# 或者
dotnet add MyService.csproj package NexusAuthorization.Sdk.Http \
  --version 0.1.0 --source ./artifacts/packages
```

对应的 `PackageReference` 为：

```xml
<PackageReference Include="NexusAuthorization.Sdk.Redis" Version="0.1.0" />
<!-- 或 -->
<PackageReference Include="NexusAuthorization.Sdk.Http" Version="0.1.0" />
```

使用本地 `PackageReference` 时，需要让 `artifacts/packages` 出现在 NuGet 配置的
包源中，并确保 Redis/HTTP 包依赖的 `NexusAuthorization.Sdk.0.1.0.nupkg` 也能从
该本地源解析。本地包源只在包含已打包文件的机器或 CI 工作区内有效。

旧 `NexusAuthorization.AspNetCore` 包不再生成，也不应再作为依赖添加。

## 测试

SDK 单元测试项目位于：

```text
sdk/dotnet/tests/NexusAuthorization.Sdk.Tests
```

可执行：

```sh
dotnet test sdk/dotnet/tests/NexusAuthorization.Sdk.Tests/NexusAuthorization.Sdk.Tests.csproj
```

HTTP 集成测试项目使用入站 TestServer 和临时本机 Kestrel 授权服务，验证真实
HTTP 请求、Controller 属性、拒绝/撤销、超时、重定向、响应限制和用户隔离：

```sh
dotnet test sdk/dotnet/tests/NexusAuthorization.Sdk.IntegrationTests/NexusAuthorization.Sdk.IntegrationTests.csproj
```

Demo 的 Redis / Http 集成测试项目位于：

```text
demo/tests/Demo.Api.IntegrationTests
```

可执行：

```sh
dotnet test demo/tests/Demo.Api.IntegrationTests/Demo.Api.IntegrationTests.csproj
```

这三个测试项目以 `net10.0` 为目标，需要本机安装 .NET 10 SDK；SDK 类库仍以
`net8.0` 为目标。Demo 的 Redis fixture 会启动临时 Docker Redis，运行前应确认
Docker 可用；HTTP 集成测试只监听本机随机端口，不访问外部授权中心。

完整运行全部测试：

```sh
dotnet test demo/NexusAuthorization.Demo.sln
```

2026-10-27 验证结果：145 项 SDK 单元测试、30 项 SDK 真实 HTTP 集成测试、
76 项 Demo 集成测试通过，共 251 项通过、0 项失败。Demo 包含 58 项临时 Redis
测试及 18 项 HTTP / Provider 配置测试；4 项现网 Redis 测试未启用。
报告位于 `demo/TestResults/sdk-remove-domain-20261027/verified`，本次未访问现网服务。
本次补充验证公开契约不再提供 Domain、原始 HTTP JSON 不含 domain、Redis 默认域
元数据兼容、具名域策略拒绝及非法域字段类型拒绝；保留服务和用户隔离检查。
新增 Demo 测试通过实际 `Program` 的配置分支和真实 Controller 验证 HTTP 请求，
覆盖九个业务端点、Bearer 转发、固定凭证、缺少令牌、撤权、匿名和仅认证端点，
并检查 HTTP 模式不注册 Redis 连接、未知 Provider 启动失败。
此前测试已修复空路径模板被当作根路径匹配、可释放 Provider 重复释放的问题。
HTTP 测试覆盖默认 Bearer 转发、固定凭证覆盖、缺少或非法 Token 拒绝、并发用户令牌
隔离，以及后续请求不会复用上一次 Token。
生命周期测试验证 Scoped Provider 的实例隔离、Scoped 依赖注入、作用域结束时只释放
一次，以及不同作用域继续共享同一个 Redis 连接。
注册测试验证最后一次 Provider 选择生效、替换外部注册和调用方实例所有权。
API 契约测试验证 `UseRedis`、`UseHttp` 各自仅有接收配置 Action 的 `void` 扩展方法。
