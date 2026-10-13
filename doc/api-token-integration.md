# API Token 使用与服务接入

## 升级现有数据库

先使用既有备份流程备份数据库，依次执行 `migrations/003_api_tokens.sql` 和 `migrations/004_api_permission_groups.sql`。这两份增量迁移都不修改初始化脚本，可以重复执行；`004` 必须在 `003` 之后执行，它创建服务作用域权限组目录并为 `api_tokens` 增加 `policies` 列。缺少应用地址字段的旧库还需要先执行 `002_application_url.sql`。

```bash
psql "$NEXUSAUTHORIZATION_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/003_api_tokens.sql
psql "$NEXUSAUTHORIZATION_DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/004_api_permission_groups.sql
```

迁移由部署人员执行，后端启动不会自动修改已有数据库。开发 Compose 的新数据库会读取 migrations 目录；已有数据卷不会重跑初始化脚本。生产 Compose 当前引用的 1.0.2 镜像不包含本次代码，应构建包含本次变更的后端和 Dashboard 镜像后再更新部署。

## 配置管理员和内部服务密钥

NexusAuthorization 的 `src/configs/app.yaml` 配置：

```yaml
api_tokens:
  service_resource: "nexusauthorization"
  admin_subjects:
    - "<NexusAuth管理员的subject>"
  internal_callers:
    - service_resource: "nexusauth"
      key: "<独立随机内部密钥，至少32字节>"
```

密钥应使用随机生成器生成，示例中的占位符不能作为真实密钥。多个接入服务各使用自己的密钥，不能共享；相同服务只配置一项。可使用现有密钥配置注入方式保存，避免提交到 Git。

环境变量对应：

```text
NEXUSAUTHORIZATION_API_TOKEN_SERVICE_RESOURCE=nexusauthorization
NEXUSAUTHORIZATION_API_TOKEN_ADMIN_SUBJECTS=<subject1>,<subject2>
NEXUSAUTHORIZATION_API_TOKEN_INTERNAL_KEY=<NexusAuth独立内部密钥>
```

`INTERNAL_KEY` 是为 nexusauth 调用者提供的快捷配置；其他服务通过 YAML internal_callers 分别配置。管理员名单在 OIDC 模式下默认空，即无人可以管理 Token；必须明确配置。

NexusAuth 启动配置需保存权限中心的私有基础地址、同一份内部密钥和固定服务键。请求权限中心时用 `X-Internal-Service-Key`；请求业务 API 时用业务 Token 的 `Authorization: Bearer`，两个位置不可互换。

配置是启动读取。修改内部密钥要更新两端并重启相应服务；业务调用方修改配置里的业务 Token 要重启调用方。中心创建／修改／撤销业务 Token 不要求 NexusAuth 重启。

## 页面操作

1. 登录 NexusAuthorization，选择业务应用。
2. 关联 NexusAuth／NexusAuthorization 服务，再打开“开放 API 权限”页面 `/open-api-permissions` 登记或导入实际接口。登记时填写接口标识、接口名称（列表展示与权限说明用的 `summary`）、HTTP 方法、路径和所属服务；导入支持填写 OpenAPI 文档 URL，或上传 JSON / YAML 文件（最大 8MB），导入写入共享服务目录，不会自动授予应用、角色或 Token 权限。使用远端服务目录时，应先在 NexusAuth 登记服务资源并在权限中心同步关联。
3. 打开“API 权限组”（`/api-permission-groups`）按服务维护命名权限组：选择 `read` 或 `write`，再从所属服务内选择启用的接口。`read` 组只能选 `GET` / `HEAD`；`write` 组可以显式选择任意受支持方法（`GET` / `HEAD` / `POST` / `PUT` / `PATCH` / `DELETE`），其中 `POST` 允许表示读形态接口。同一服务下所有应用共享这一批权限组。
4. 打开“RBAC 授权 → API Token”（`/api-tokens`）。
5. 进入创建页面 `/api-tokens/create`：填写 Token 名称，选择永久、30 天、180 天或自定义日期。页面不再提供归属主体选择器，Token 类型固定为服务类型；归属由后台用当前 `app_key` 填充。
6. 配置 Permission policies：每条策略选择一个目标服务（须已关联当前应用）、若干权限组和租户／项目范围组合。页面“全部／只读／写入”只用于过滤该服务下的权限组，不生成通配授权；权限组内的端点集合由权限组目录决定。详情编辑用 `/api-tokens/:tokenId/edit`，只改权限可用 `/api-tokens/:tokenId/permissions`。
7. 保存一次性明文，配置到业务调用方。关闭展示后无法找回，只能轮换或重新创建。
8. 停用可以恢复；撤销不可恢复。轮换可立即撤销旧 Token，或保留旧 Token 完成切换后手动撤销。

一个 Token 的权限可以覆盖所属应用关联的多个服务，不能跨应用。归属不再由页面选择：后台忽略请求体里的 `owner_id`，用 `app_key` 作为 `owner_id`，`owner_type` 固定为 `service`，显式用户类型会被拒绝。历史 `user` 归属的 Token 仍可读取，编辑名称／有效期／策略并可轮换，但归属不可改，也不能通过后台新建用户类型归属。

权限组是服务作用域的共享目录：同一服务下所有应用共用同一批组，编辑或停用某个组会立即影响所有引用它的 Token。组、接口、服务停用或服务关联移除，以及 Token 策略修改，都会影响后续请求，无需发布 RBAC 版本。带策略的 Token 在运行时按“当前组映射 + 当前 API 目录”实时求值，不会回退到旧快照；旧版逐条端点授权的 Token 继续按原语义工作。

命名权限组 + Token policy 的授权结构参考了 Cloudflare API Token 的模型以方便对照（[List permission groups](https://developers.cloudflare.com/api/resources/user/subresources/tokens/subresources/permission_groups/methods/list/)、[Create Token](https://developers.cloudflare.com/api/resources/user/subresources/tokens/methods/create/)），字段与作用域不同，不是 Cloudflare 的等价实现。

## 内部授权调用

服务端从实际业务请求获取 Token，构造可信的应用、服务、方法、实际路径和业务范围，调用：

```http
POST https://<权限中心私有地址>/internal/v1/api-tokens/authorize
X-Internal-Service-Key: <内部服务密钥>
Content-Type: application/json

{
  "token": "<业务请求中的nxa_...>",
  "app_key": "identity-console",
  "service_resource": "nexusauth",
  "method": "GET",
  "path": "/scim/v2/Users",
  "tenant_id": "tenant-a",
  "project_id": ""
}
```

外部调用方不能通过请求 body 中随意传 tenant_id 等字段来冒充目标范围。按 ID 访问资源时，服务应查明资源真实归属后授权；列表 API 在授权后仍需按允许范围过滤结果。路由 path 不带 query 字符串，方法必须大写。

正常评估返回 HTTP 200，解析 `active` 和 `allowed`：两项为 true 才放行；active=false 对外返回 401；active=true、allowed=false 返回 403。超时、连接失败、内部 HTTP 错误、畸形响应均拒绝并对外返回 503，不能降级成免认证访问。

默认无缓存，每次校验真实哈希和数据库状态；不要仅凭 token_id 缓存有效性，否则替换 secret 仍可能被误放行。带 policy 的 Token 在每次校验时连接权限组映射与当前 API 目录，停用的组、端点或服务立即不再授权；policy 非空时不会回退到任何旧快照。

## Go 服务

使用 `sdk/apitoken`：

```go
client, err := apitoken.New(apitoken.Options{
    BaseURL: "https://authorization.internal.example",
    ServiceKey: configuredInternalKey,
})
if err != nil { /* 启动失败 */ }

decision, err := client.Authorize(ctx, incomingBearer, apitoken.Target{
    AppKey: "identity-console",
    ServiceResource: "nexusauth",
    Method: request.Method,
    Path: request.URL.EscapedPath(),
    TenantID: resolvedTenantID,
    ProjectID: resolvedProjectID,
})
// err != nil: 503；!decision.Active: 401；!decision.Allowed: 403。
// 两项均 true：执行业务，并按已验证范围过滤业务数据。
```

默认超时 3 秒、阻止跨域重定向、不缓存结果、不把密钥放进错误信息。生产地址要求 HTTPS，HTTP 仅允许 localhost 回环开发地址。具体接口见 [SDK README](../sdk/apitoken/README.md)。

## NexusAuth / ASP.NET Core 接入参考

此仓库没有修改 NexusAuth。下面客户端合同可用于另一个项目接入，注册时配置固定服务键和私有 HTTPS 地址；HTTP client 禁止重定向，不使用重试延长用户请求。

```csharp
using System.Net.Http.Json;
using System.Text.Json.Serialization;

public sealed record TokenTarget(
    [property: JsonPropertyName("app_key")] string AppKey,
    [property: JsonPropertyName("service_resource")] string ServiceResource,
    [property: JsonPropertyName("method")] string Method,
    [property: JsonPropertyName("path")] string Path,
    [property: JsonPropertyName("tenant_id")] string TenantId,
    [property: JsonPropertyName("project_id")] string ProjectId);

public sealed record TokenDecision(
    [property: JsonPropertyName("active")] bool Active,
    [property: JsonPropertyName("allowed")] bool Allowed,
    [property: JsonPropertyName("reason")] string Reason,
    [property: JsonPropertyName("owner_type")] string? OwnerType,
    [property: JsonPropertyName("owner_id")] string? OwnerId);

public sealed class ApiTokenAuthorizationClient(HttpClient http)
{
    public async Task<TokenDecision> AuthorizeAsync(
        string token, TokenTarget target, CancellationToken cancellationToken)
    {
        // 内部key在 HttpClient 注册时设置，token是业务请求中收到的明文。
        using var response = await http.PostAsJsonAsync(
            "internal/v1/api-tokens/authorize", new {
                token,
                app_key = target.AppKey,
                service_resource = target.ServiceResource,
                method = target.Method,
                path = target.Path,
                tenant_id = target.TenantId,
                project_id = target.ProjectId
            }, cancellationToken);
        if (!response.IsSuccessStatusCode)
            throw new InvalidOperationException("API token authorization unavailable");
        var decision = await response.Content.ReadFromJsonAsync<TokenDecision>(
            cancellationToken: cancellationToken);
        if (decision is null || string.IsNullOrEmpty(decision.Reason)
            || (decision.Allowed && !decision.Active))
            throw new InvalidOperationException("Invalid authorization response");
        return decision;
    }
}
```

注册客户端（.NET 8 示例）：

```csharp
services.AddHttpClient<ApiTokenAuthorizationClient>(http => {
    // base URL 以 / 结尾；生产服务启动时校验为受信 HTTPS 地址。
    http.BaseAddress = new Uri(configuredAuthorizationBaseUrl);
    http.Timeout = TimeSpan.FromSeconds(3);
    http.DefaultRequestHeaders.Add("X-Internal-Service-Key", configuredInternalKey);
}).ConfigurePrimaryHttpMessageHandler(() => new HttpClientHandler {
    AllowAutoRedirect = false
});
```

在需保护的 SCIM／开放 API 认证入口调用客户端，使用启动配置或服务端可信解析的 app_key 与 tenant/project；捕获超时／HTTP／JSON 异常返回 503，只记录通用错误码，不记录请求头、Token、内部密钥或请求 body。缺少 Bearer Token 直接返回 401。既有 SCIM 响应格式和数据过滤由 NexusAuth 的 SCIM 实现负责。

如果是 NexusAuthorization 主动调用 NexusAuth，业务 Token 可以放进调用方现有配置，发出时放在 Authorization 头；NexusAuth 使用上述回调再次确认。现有服务目录客户端仍保留旧 OpenApiCredential 合同，NexusAuth 接入中心 Token 认证后再切换，不自动替换现有凭证。

## 验证

后端与 SDK：

```bash
cd src
go test ./...
# 指向已执行001/002/003/004的独立测试数据库，启用真实DB集成测试：
NEXUSAUTHORIZATION_TEST_DATABASE_URL="$TEST_DATABASE_URL" go test ./internal/data/db/repo ./internal/server/http
```

```bash
cd sdk
go test ./...
```

前端：

```bash
cd dashboard
npm run build
npm run test:e2e -- api-token.spec.ts
```

集成测试为随机命名的测试应用创建数据并清理；应使用专用测试数据库。
