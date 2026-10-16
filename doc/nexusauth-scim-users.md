# NexusAuth SCIM 用户列表

NexusAuthorization 后端使用独立的 SCIM Bearer Token 调用 NexusAuth Provider 的 `GET /scim/v2/Users`，只查询用户，不存储用户资料。

在本地 `.env` 配置以下变量，真实 Token 不要填写到 `.env.example`、YAML、前端配置或提交记录中：

```dotenv
NEXUSAUTHORIZATION_SCIM_NEXUSAUTH_BASE_URL='https://authlogin.kawhiwei.com:8800'
NEXUSAUTHORIZATION_SCIM_API_KEY='REPLACE_WITH_SCIM_READ_TOKEN'
NEXUSAUTHORIZATION_SCIM_TIMEOUT='5s'
```

基址是 Provider 地址，客户端会追加 `/scim/v2/Users`。当前 Provider 地址使用 HTTPS：`http://authlogin.kawhiwei.com:8800` 会返回“需要 HTTPS”。凭据需要 `scim:read` 权限，独立于服务资源目录的 Open API 凭据。未配置 SCIM 时，其余功能保持可用，查询用户返回 503。

Docker Compose 已传递这些变量；需要使用包含本次代码的后端镜像。直接运行 Go 时，需要先将 `.env` 变量导出到进程环境。

后端调用入口：

```http
GET /api/v1/users?startIndex=1&count=100
GET /api/v1/users?filter=userName%20eq%20%22alice%22&startIndex=1&count=20
```

浏览器经 Dashboard 代理访问时路径同样是 `/api/v1/users`，与后端 canonical 路径一致。接口使用现有登录认证中间件，SCIM Token 仅在后端发送给 NexusAuth。

`startIndex` 从 1 开始，默认 1；`count` 默认 100，范围 0–200，0 只查询总数；`filter` 遵循 NexusAuth 支持的 SCIM 语法（如 `userName eq "alice"`）。响应沿用统一包裹格式，`result` 包含 SCIM `schemas`、`totalResults`、`startIndex`、`itemsPerPage` 和 `resources` 用户数组。用户 `id` 对应后续角色绑定使用的 NexusAuth subject。

前端在“角色管理”列表中点击“分配用户”，自动分页读取用户目录，并回显该角色已有的用户。选择框可以按姓名、用户名或邮箱搜索，提交时仍使用用户的 `id` 作为 `subject`，默认写入空域 `domain`。保存只增加或移除当前角色，保留用户的其他角色和具名域绑定。停用用户不能新增分配；停用用户或已从目录删除的原绑定用户仍可取消分配。加载失败显示重试入口并禁用保存。部分用户保存失败时抽屉保持打开并显示失败数量，再次保存只重试未完成的变化。

单独的“用户授权”菜单与标签页已移除，旧 `/bindings` 地址会重定向到 `/roles`。角色分配入口写入默认空域（`domain` 为空串），无需填写 Domain；旧值 `global` 视作同一默认域。后端仍保留 Domain 字段用于按租户或组织隔离授权，具名域保持独立；此入口不修改其他 Domain 的已有绑定。

参数无效返回 400，远端请求失败返回 502，未配置返回 503。错误响应不回显远端正文或密钥。
