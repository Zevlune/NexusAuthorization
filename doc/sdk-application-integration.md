# 业务服务 SDK 接入

Go SDK（`sdk/rbac`）要求每次鉴权同时标识受信业务应用和被访问的服务资源。这是 `Client.Check` 与 `Client.Require` 的破坏性变更：`Request.AppKey` 和 `Request.ServiceResource` 都是必填项。`AppKey` 必须是调用方配置的应用标识，`ServiceResource` 必须标识受保护的 API 资源；两者都不能从请求查询参数、请求头或其他用户可控输入读取。

## 认证

默认 Compose 不再配置独立的服务凭证，当前 `/api/v1/authorize` 拒绝业务服务调用。统一 Open API Token 管理与校验尚未接入；下面的 SDK 示例适用于旧服务凭证兼容机制，默认部署需等待统一 Token 校验完成后再接入。

`rbac.NewClient(baseURL, credential)` 创建的客户端向 `baseURL + /api/v1/authorize` 发起 `POST`，并自动附带 `Authorization: Bearer <credential>`。该凭证是权限中心配置的服务到服务凭证（`authorization.service_credential`），不是管理端浏览器会话。HTTP 客户端默认 3 秒超时。

## 中间件接入

```go
client := rbac.NewClient("https://nexus-authorization.example", os.Getenv("NEXUSAUTHORIZATION_CREDENTIAL"))

middleware := client.Require(
	"books",
	"book-service",
	func(r *http.Request) (rbac.Identity, bool) {
		// 从可信认证中间件解析身份。
		identity, ok := auth.IdentityFromContext(r.Context())
		return rbac.Identity{Subject: identity.Subject, Domain: identity.Domain}, ok
	},
	http.HandlerFunc(listBooks),
)
http.Handle("/api/books", middleware)
```

`Require(appKey, serviceResource, resolve, next)` 的 `appKey` 和 `serviceResource` 是固定的受信配置值。身份由接入服务负责解析；SDK 不接受用户传入的角色或授权参数。

`rbac.Identity.Domain` 可选：省略、空串和旧值 `global` 都表示默认空域，只有需要按具名域隔离时才填显式值。SDK 不再要求 `Domain` 非空。

## 直接调用

```go
decision, err := client.Check(ctx, rbac.Request{
	AppKey:          "books",
	ServiceResource: "book-service",
	Subject:         "user:1",
	Domain:          "tenant-a", // 可选；省略或留空使用默认域
	Method:          http.MethodGet,
	Path:            "/api/books",
})
```

`Check` 在凭证为空或必需字段（`AppKey`、`ServiceResource`、`Subject`、`Method`、`Path`）为空时直接返回错误，不发送请求；`Domain` 可省略，省略或空串按默认域处理。成功时返回 `Decision`：

```go
type Decision struct {
	Allowed        bool
	Effect         string   // allow / deny
	Reason         string   // allowed / endpoint_not_found / ...
	MatchedRoles   []string
}
```

SDK 校验返回的决策：`Allowed=true` 时 `Effect` 必须为 `allow`、`Reason` 必须为 `allowed`，否则返回错误。服务端不再返回发布版本号，SDK 也不再校验 `ReleaseVersion` 字段；升级旧 SDK 时请同步移除该字段和对应校验。非 `200` 响应、响应体解析失败都会返回错误。

## 失败关闭

中间件按失败关闭处理：应用或服务配置缺失、身份未认证、鉴权服务调用出错、决策响应非法或决策为拒绝时，都不会执行下游处理器。身份无效返回 `401`；鉴权错误或拒绝返回 `403`。

## 相关文档

- [RBAC v1 接口参考](reference-api-rbac-v1.md)
- [应用边界 API RBAC 设计方案](rbac-v1-design.md)
