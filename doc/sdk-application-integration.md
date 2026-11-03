# 业务服务 SDK 接入

Go SDK（`sdk/rbac`）同时提供运行时 API 鉴权和主体菜单查询。每次鉴权都必须标识受信业务应用与被访问的服务资源，`Client.Check` 和 `Client.Require` 中的 `Request.AppKey`、`Request.ServiceResource` 都是必填项。

`AppKey` 必须是接入方配置的应用标识，`ServiceResource` 必须标识受保护的 API 资源。两者都只能来自服务端固定配置，不能从请求查询参数、请求头或其他用户可控输入读取。

## 服务凭证

Compose 将 `NEXUSAUTHORIZATION_AUTHORIZATION_SERVICE_CREDENTIAL` 传入 API 服务，并映射为 `authorization.service_credential`。该值同时保护：

- `POST /api/v1/authorize`
- `GET /api/v1/subject-menus`

值非空时，调用方必须在请求头中发送完全匹配的：

```http
Authorization: Bearer <service-credential>
```

值为空或只包含空白时，两个接口都会关闭并返回 `401 UNAUTHENTICATED`。`SERVICEUK` 是当前服务的资源标识，不是这两个接口使用的 Bearer 凭证。

在权限中心部署用的 `.env` 中配置一个长随机值：

```dotenv
NEXUSAUTHORIZATION_AUTHORIZATION_SERVICE_CREDENTIAL='replace-with-a-long-random-value'
```

启动 Compose 时读取该文件：

```bash
docker compose --env-file .env -f docker-compose.yml up -d
```

业务后端必须通过自己的服务端环境变量或密钥管理系统取得同一个值。不要把它写入前端代码、浏览器存储、页面配置、URL 或普通日志。更换凭证后，权限中心和全部可信业务调用方需要同步更新。

`rbac.NewClient(baseURL, credential)` 会向 `baseURL + /api/v1/authorize` 发起鉴权请求，并向 `baseURL + /api/v1/subject-menus` 查询菜单。两个请求都自动附带上述 Bearer 凭证。SDK 默认 HTTP 超时为 3 秒。

## 中间件接入

```go
client := rbac.NewClient(
	"https://nexus-authorization.example",
	os.Getenv("NEXUSAUTHORIZATION_CREDENTIAL"),
)

middleware := client.Require(
	"books",
	"book-service",
	func(r *http.Request) (rbac.Identity, bool) {
		// 从业务系统已验签、已建立的认证上下文取得身份。
		identity, ok := auth.IdentityFromContext(r.Context())
		return rbac.Identity{
			Subject: identity.Subject,
			Domain:  identity.Domain,
		}, ok
	},
	http.HandlerFunc(listBooks),
)

http.Handle("/api/books", middleware)
```

`Require(appKey, serviceResource, resolve, next)` 的 `appKey` 和 `serviceResource` 是固定的服务端配置值。`resolve` 必须从接入服务可信的认证上下文返回身份，不能直接读取用户提交的 `subject` 参数。

`rbac.Identity.Domain` 可选。省略、空串和旧值 `global` 都表示默认空域；只有具名域隔离才填写显式值。

## 直接鉴权

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

`Check` 在凭证为空，或 `AppKey`、`ServiceResource`、`Subject`、`Method`、`Path` 任一为空时直接返回错误，不发送请求。成功时返回：

```go
type Decision struct {
	Allowed      bool
	Effect       string   // allow / deny
	Reason       string   // allowed / endpoint_not_found / ...
	MatchedRoles []string
}
```

`Allowed=true` 时，SDK 要求 `Effect=allow` 且 `Reason=allowed`，否则按非法响应处理。非 `200`、响应体解析失败或非法决策都会返回错误。

## 主体菜单

### HTTP API

可信业务后端使用独立菜单接口查询当前用户可见的菜单和按钮：

```http
GET /api/v1/subject-menus?app_key=books&subject=user%3A1&domain=tenant-a
Authorization: Bearer <service-credential>
Accept: application/json
```

参数如下：

- `app_key`：必填，业务应用的受信配置值。
- `subject`：必填，业务后端从自身认证上下文取得的已认证主体。
- `domain`：可选；省略、空串和旧值 `global` 按默认空域处理。

该接口只接受配置的服务凭证，不接受管理端 Cookie 或普通管理 Bearer 回退。原管理接口 `GET /api/v1/rbac-v1/subject-menus` 继续接受管理端认证与授权，两种调用边界彼此独立。

与管理端业务结果一致，HTTP 响应中的 `result.items` 是扁平菜单列表，并已经补齐可见菜单所需的启用祖先。例如角色授予了 `create` 按钮时，响应还会包含它启用的父菜单：

```json
{
  "success": true,
  "errorCode": null,
  "errorMessage": null,
  "result": {
    "items": [
      {
        "id": "5dd40f52-2c27-4d6a-9f14-bcdb19f73288",
        "application_id": "b94885db-c2da-4c10-8711-26079e115503",
        "parent_id": null,
        "code": "orders",
        "name": "Orders",
        "menu_type": "menu",
        "route": "/orders",
        "component": "orders/index",
        "icon": "shopping-cart",
        "sort_order": 10,
        "enabled": true
      },
      {
        "id": "39da7377-c3f2-4ad1-b37c-b727f4f5a32f",
        "application_id": "b94885db-c2da-4c10-8711-26079e115503",
        "parent_id": "5dd40f52-2c27-4d6a-9f14-bcdb19f73288",
        "code": "create",
        "name": "Create order",
        "menu_type": "button",
        "route": "",
        "component": "",
        "icon": "",
        "sort_order": 20,
        "enabled": true
      }
    ]
  }
}
```

父菜单停用、缺失或层级存在循环时，对应分支不会返回。缺少 `app_key` 或 `subject` 返回 `400`；应用不存在或已停用返回 `404`；凭证缺失或不匹配返回 `401`。

### Go SDK 嵌套树

`SubjectMenus` 的真实签名是：

```go
func (c *Client) SubjectMenus(
	ctx context.Context,
	appKey string,
	subject string,
	domain string,
) ([]rbac.SubjectMenu, error)
```

```go
menus, err := client.SubjectMenus(
	ctx,
	"books",
	identity.Subject,
	identity.Domain,
)
if err != nil {
	return err
}
```

SDK 读取 HTTP 响应中的扁平 `result.items`，再按照 `ParentID` 构建嵌套树。返回值只包含根节点，后代位于 `Children`，同级顺序与服务端返回顺序一致：

```go
type SubjectMenu struct {
	ID            string
	ApplicationID string
	ParentID      *string
	Code          string
	Name          string
	MenuType      string
	Route         string
	Component     string
	Icon          string
	SortOrder     int
	Enabled       bool
	Children      []SubjectMenu
}
```

遍历完整树：

```go
func walkMenus(items []rbac.SubjectMenu, visit func(rbac.SubjectMenu)) {
	for _, item := range items {
		visit(item)
		walkMenus(item.Children, visit)
	}
}

walkMenus(menus, func(menu rbac.SubjectMenu) {
	fmt.Printf("%s (%s), visible=%t\n", menu.Name, menu.MenuType, menu.Enabled)
})
```

SDK 会校验成功包装、`result.items`、菜单 ID 和父子关系。非 `200`、缺失凭证、重复 ID、缺失父节点、循环层级以及畸形 JSON 都会返回错误；调用方应停止渲染菜单或返回业务错误，不能把失败结果当成空权限继续放行。

### Trusted subject 与 domain

权限中心只验证服务凭证，不验证终端用户。对拥有服务凭证的可信调用方，它可以返回任意 `subject + domain` 的菜单，因此业务后端必须自行建立以下信任链：

```go
func subjectMenusHandler(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.IdentityFromContext(r.Context())
	if !ok || identity.Subject == "" {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}

	menus, err := client.SubjectMenus(
		r.Context(),
		"books",
		identity.Subject,
		identity.Domain,
	)
	if err != nil {
		http.Error(w, "menu lookup failed", http.StatusServiceUnavailable)
		return
	}

	writeMenusForBrowser(w, menus)
}
```

这里的 `identity.Subject` 和 `identity.Domain` 必须来自业务后端已验签的会话或令牌。不要从浏览器的 query、body、Cookie 或普通请求头直接取得 `subject` 后转传给权限中心，也不要让业务浏览器直接持有服务凭证。

### 按钮只控制 UI

`menu_type=button` 表示角色已授予该按钮的菜单投影，可用于决定是否渲染“新建”“删除”等入口。它不是 API 鉴权的替代品，也不能阻止用户直接调用后端接口。

下面的辅助函数只负责界面显示：

```go
func hasButton(items []rbac.SubjectMenu, code string) bool {
	for _, item := range items {
		if !item.Enabled {
			continue
		}
		if item.MenuType == "button" && item.Code == code {
			return true
		}
		if hasButton(item.Children, code) {
			return true
		}
	}
	return false
}

showCreateButton := hasButton(menus, "create")
```

所有敏感操作仍必须在业务 API 上使用 `Client.Require` 或 `Client.Check` 独立鉴权：

```go
http.Handle(
	http.MethodPost + " /api/books",
	client.Require(
		"books",
		"book-service",
		resolveTrustedIdentity,
		http.HandlerFunc(createBook),
	),
)
```

菜单结果可能因缓存、配置变更或客户端篡改而过期。即使 `showCreateButton=false`，也必须保留服务端 API 鉴权；即使按钮可见，服务端拒绝时仍应返回 `403`。

## 失败关闭

中间件按失败关闭处理：应用或服务配置缺失、身份未认证、鉴权服务调用出错、决策响应非法或决策为拒绝时，都不会执行下游处理器。身份无效返回 `401`；鉴权错误或拒绝返回 `403`。

菜单查询失败时，业务后端应返回错误或隐藏相关入口，同时继续保护目标 API。不能因为菜单不可用而跳过 API 鉴权。

## 相关文档

- [RBAC v1 接口参考](reference-api-rbac-v1.md)
- [应用边界 API RBAC 设计方案](rbac-v1-design.md)
