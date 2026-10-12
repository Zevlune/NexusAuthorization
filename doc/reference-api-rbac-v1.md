# 自研 API 级 RBAC 权限系统第一版实施方案

> 文档日期：2026-09-19
> 目标读者：使用 Codex 实现系统的 Go 开发人员
> 实施范围：只做 API 接口资源级鉴权，不依赖 Casbin 或其他权限执行内核

## 1. 项目目标

系统通过以下关系判断用户能否访问接口：

```text
用户（Subject） -> 角色（Role） -> API Endpoint
```

用户请求 `/v1/authorize` 时，输入服务资源、subject、domain、HTTP method 和 path。系统从当前发布快照中查找用户角色以及这些角色授权的 API，最终返回 ALLOW 或 DENY。

第一版是纯 Allow List：启用角色直接授予目标 API 时允许，否则拒绝。

## 2. 第一版范围

### 2.1 必须实现

1. 服务资源管理。
2. Swagger/OpenAPI 导入 API Endpoint。
3. API Endpoint 分页查询和启停管理。
4. 角色新增、编辑、启停和删除。
5. 角色与 API Endpoint 多对多绑定。
6. 用户在 Domain 下与角色绑定。
7. 未匹配默认拒绝。
8. 草稿校验、发布、版本和回滚。
9. 发布快照热加载和原子切换。
10. 权限模拟测试和鉴权解释。
11. HTTP Middleware 接入 `/v1/authorize`。
12. 角色授权、发布、回滚和敏感鉴权的审计日志。

### 2.2 明确不实现

第一版不包含：

- 独立的权限编码表；
- 接口策略及策略页面；
- `all / any` 权限组合；
- 可配置 `allow / deny` 效果；
- 用户直接绑定 API；
- 数据行级、字段级、菜单级权限；
- ABAC、脚本、SQL 条件、外部条件查询；
- 复杂规则合并算法。

这不是遗漏：当前目标仅是“用户通过角色能否访问接口”。角色直接绑定 API 可以保持配置和鉴权链路可理解、可验证。

## 3. 核心 RDS 模型

### 3.1 服务资源和 API Endpoint

```text
service_resources
- id UUID PK
- key VARCHAR UNIQUE
- display_name VARCHAR
- audience VARCHAR
- source VARCHAR
- is_active BOOLEAN

api_endpoints
- id UUID PK
- service_resource_id UUID FK service_resources
- endpoint_key VARCHAR
- method VARCHAR
- path_template VARCHAR
- module VARCHAR
- summary VARCHAR
- enabled BOOLEAN
- source VARCHAR                    # swagger / manual
- last_discovered_at TIMESTAMPTZ

UNIQUE(service_resource_id, method, path_template)
UNIQUE(service_resource_id, endpoint_key)
```

Endpoint 表只描述 API 资源，不添加 `permission`、`permission_code`、策略、`match_mode` 或 `effect` 字段。

### 3.2 角色和 API 授权

```text
roles
- id UUID PK
- service_resource_id UUID FK service_resources
- code VARCHAR
- name VARCHAR
- description TEXT
- enabled BOOLEAN
- created_at / updated_at

UNIQUE(service_resource_id, code)

role_api_endpoint_bindings
- role_id UUID FK roles
- endpoint_id UUID FK api_endpoints
- created_at

PRIMARY KEY(role_id, endpoint_id)
```

角色和 Endpoint 为多对多关系。一个角色可以拥有多个 API；同一个 API 也可分配给多个角色。API 授权只在角色管理中配置，不建立独立策略页面。

### 3.3 用户和角色

```text
subject_role_bindings
- id UUID PK
- service_resource_id UUID FK service_resources
- subject VARCHAR
- domain VARCHAR
- role_id UUID FK roles
- created_at / updated_at

UNIQUE(service_resource_id, subject, domain, role_id)
```

多角色取并集：只要用户的任一启用角色绑定了目标 API，即允许访问。用户没有角色、角色没有 API 授权或目标 API 不匹配时拒绝。

### 3.4 发布版本

```text
rbac_releases
- id UUID PK
- service_resource_id UUID FK service_resources
- version BIGINT
- status VARCHAR                    # published / superseded
- description TEXT
- checksum VARCHAR
- published_by VARCHAR
- published_at TIMESTAMPTZ

rbac_release_snapshots
- release_id UUID PK FK rbac_releases
- snapshot JSONB
```

发布对象是整个 `service_resource`，不是单个用户。快照包含该服务资源的 API、角色、角色-API 关联和用户-角色关联。

## 4. 管理配置流程

```text
1. 选择服务资源
2. 导入 Swagger/OpenAPI，确认 API Endpoint
3. 创建角色
4. 在角色的“API 授权”页签搜索并勾选 API
5. 在用户列表中绑定 subject、domain 和角色
6. 使用模拟器测试 method + path
7. 校验草稿
8. 发布整个服务资源
9. Runtime 原子切换到新快照
```

推荐 UI 页面：

| 页面 | 操作 |
| --- | --- |
| 选择服务资源 | 新增、编辑、切换服务资源，使用 Drawer |
| API 资源 | Swagger 导入、手动维护、分页查看接口目录 |
| 角色管理 | 创建角色，在同一 Drawer 的“API 授权”页签中按模块、方法和路径筛选、批量勾选 API |
| 用户列表 | 按 subject + domain 多选角色绑定 |
| 发布与版本 | 发布服务资源、查看历史版本、回滚 |
| 权限测试 | 输入 subject/domain/method/path，查看鉴权解释 |

主要配置使用 Drawer，不使用 Modal 承载复杂表单。API 资源页面仅维护接口目录；API 授权配置仅在角色管理中维护。

## 5. 发布校验

错误（阻止发布）：

- API 或角色编码重复；
- method + path 重复；
- 关联记录引用不存在、停用或其他服务资源的对象；
- 用户角色绑定指向不存在或停用的角色；
- 快照构建失败。

警告（可确认后发布）：

- API 没有分配给任何角色；
- 角色暂未分配任何 API；
- 角色暂未分配给用户；
- 停用 API 仍存在历史角色关联。

## 6. 运行时鉴权

### 6.1 请求

```json
{
  "serviceResource": "book-service",
  "subject": "user:1001",
  "domain": "tenant-a",
  "method": "GET",
  "path": "/books"
}
```

### 6.2 算法

```text
1. 读取 serviceResource 当前 active release。
2. 用 method + path 查找启用的 API Endpoint。
3. 查找 subject + domain 的启用角色。
4. 展开角色绑定的启用 API Endpoint。
5. 存在目标 Endpoint -> ALLOW。
6. 其他情况 -> DENY。
```

返回结果：

```go
type Decision struct {
    Allowed       bool
    Reason        string
    PolicyVersion int64
    MatchedRoles  []string
    Endpoint      string
}
```

`reason` 至少区分：`allowed`、`no_active_release`、`endpoint_not_found`、`subject_has_no_role`、`no_matching_role_endpoint_binding`。

### 6.3 HTTP Middleware

业务服务或 API Gateway 只需将认证后的身份和当前路由转给授权服务：

```text
认证 Token -> 得到 subject
当前请求 -> 得到 domain、method、path
调用 /v1/authorize
Allowed=true -> 进入业务 Handler
Allowed=false -> 返回 403
未认证 -> 返回 401
```

授权服务不要求业务服务传角色；角色及 API 授权始终由已发布快照解析。

## 7. 发布、测试与回滚

发布流程：

```text
编辑草稿 -> 校验引用关系 -> 模拟测试 -> 编译不可变快照
-> 写入新版本 -> 原子切换 active snapshot -> 审计
```

模拟测试不改变数据库，默认使用当前 active 快照，并明确显示测试使用的版本。若产品需要测试未发布草稿，可另设“草稿模拟”接口，但草稿不能直接参与线上鉴权。

回滚通过读取历史版本并重新发布为新版本完成，不能覆盖原历史记录。

## 8. Go 领域模型

```go
type Endpoint struct {
    ID      string
    Method  string
    Path    string
    Enabled bool
}

type Role struct {
    ID        string
    Code      string
    Name      string
    Endpoints []string
}

type SubjectRoleBinding struct {
    Subject string
    Domain  string
    Roles   []string
}

type AccessRequest struct {
    ServiceResource string
    Subject         string
    Domain          string
    Method          string
    Path            string
}
```

## 9. Runtime Snapshot

运行时快照只需高效索引 Endpoint、角色及主体绑定：

```go
type Snapshot struct {
    Version        int64
    Checksum       string
    Endpoints      map[EndpointKey]EndpointRef
    RoleEndpoints  map[string]map[string]struct{}
    SubjectRoles   map[DomainSubjectKey][]string
}

type EndpointKey struct {
    Method string
    Path   string
}

type DomainSubjectKey struct {
    Domain  string
    Subject string
}
```

鉴权热路径只读内存快照，不读数据库、不解析动态脚本。发布成功后通过原子指针一次性替换当前快照；旧请求继续使用旧快照，新请求使用新快照。

## 10. 未来扩展

第一版完成并稳定后，再考虑：

- 显式 `deny` 和 deny 优先级；
- 面向 API 集合的可复用策略；
- 菜单、按钮和前端可见性绑定；
- 数据行级、字段级授权；
- 组织、用户组和直接授权；
- 审批流、差异比较和更完整的审计。

这些能力属于后续版本，不应改变第一版“用户 -> 角色 -> API”的基本理解。
