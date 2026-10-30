# Issues 核验与本次修复

核验日期：2026-10-30。主 agent 读取 GitHub 全部 issues，并对照最新 `origin/main`（`eedd23b`）核验；列表共 21 个 issue，GitHub 状态为 15 个 closed、6 个 open。以下区分 GitHub 状态与实际修复程度，不自动关闭或改写 issue。

修复分支：`fix/open-issues-and-management-auth`。按用户要求，原工作目录中 27 个未提交文件一并带入并验证，保留原目录内容。

## 逐项核验

| Issue | GitHub 状态 | 代码核验与本次处理 |
| --- | --- | --- |
| #2 并发发布导致旧授权回写 | closed | 已有 PostgreSQL session advisory lock，取得锁后在 repeatable-read 事务中构建并串行发布，已有并发回归。当前 String 协议仅保证逐用户原子写入，不保证批次整体原子，见权限生效文档。 |
| #3 发布失败与缺少自愈 | closed | 已有独立发布超时、`WithoutCancel`、启动与周期全量对账；目录读取失败中止发布，返回 `PUBLISH_PENDING`。 |
| #4 明文口令与个人部署配置 | open | 生产 Compose 已从环境变量读取数据库连接及部署设置；历史公开口令的轮换、访问日志检查和是否改写历史属于维护者操作，本次不据此认定整个 issue 完结。 |
| #5 管理分权与审计 | open | 带入的修改已增加 OIDC 认证后的管理服务 API 授权；本次补管理写操作审计日志。管理权限仍按服务合并，不按应用隔离；配置前后值、审计表、保留与防篡改策略、查询页面仍未实现，暂不视为完整解决。 |
| #6 Compose 与镜像启动 | closed | 已有开发 Redis、上游别名、环境变量配置及非 root 服务进程；本次补 `.env.dev.example` 和 CI 容器所需的 `LUCKENV`、`SERVICEUK`，修复带入配置变更造成的启动回归。服务鉴权凭证为空时接口继续关闭。 |
| #7 无记录或停用的鉴权响应 | closed | 已有缺少用户服务记录时的正常 deny 和 `service_inactive` 文档。当前合并协议已取代 issue 描述的旧闸门协议，数据库模式的应用边界仍按该模式校验。 |
| #8 性能与扩展性 | open | 每次构建只列一次目录、批量角色成员更新只发布一次、持行锁前读取远程目录均已有实现；本次菜单查询不再读取服务目录和 API 端点。全量发布、每用户内嵌目录、数据库 API 鉴权聚合和远程目录缓存等整体优化仍待设计。 |
| #9 同形路由歧义 | closed | 已按 `RouteShape` 判重，保存有 advisory transaction lock，Swagger 导入按同形路由去重，已有回归。 |
| #10 自动接口 key 与长度 | closed | 已有截断加哈希、更新接口 key 和 160 字符限制；复核发现 PostgreSQL `22001` 尚未映射，本次补为 `INVALID_ARGUMENT`，避免字段超长返回 500。 |
| #11 整组替换丢失更新 | closed | 已有服务加载保护和提交前重新读取；角色成员已改为按角色批量增删，事务保存并只发布一次，已有并发回归。服务关联的完整替换接口仍保留其 PUT 语义。 |
| #12 Swagger SSRF | closed | 已有连接时 IP 校验、回环开关、主机/网段白名单及元数据地址限制，已有回归。 |
| #13 HTTP 加固 | closed | 已有服务超时、优雅退出、请求体上限、JSON Content-Type 校验及统一内部错误日志，已有回归。 |
| #14 会话刷新与 introspection | open | 401 登录跳转已携带原页面。会话仍受 ID Token 到期时间约束，未增加 refresh token 保存或 introspection 缓存；需先确定会话存储、最长时长与撤销时效。 |
| #15 登录回跳校验 | closed | 已拒绝反斜杠、控制字符和跨源地址，已有浏览器回归。 |
| #16 业务菜单访问 | open | 本次移除主体菜单读取对远程服务目录的依赖，并纠正文档。业务调用方认证、是否仅允许查询本人，以及 Go SDK 菜单接口仍待确定；继续保留管理接口认证边界。 |
| #17 失效服务端点清理 | open | 本次修复：保留端点并提供手动清理，列表和删除仅校验本地应用关联；界面显示失效状态和删除入口，新增/编辑/导入仍要求有效服务。目录故障时已提交的删除仍遵守 `PUBLISH_PENDING` 语义。 |
| #18 Swagger 超时与部分导入 | closed | 导入请求已有独立两分钟超时，失败时也刷新列表。 |
| #19 符号链接与死代码 | closed | Go 测试已移到真实包，无 `src/test/internal` 源码符号链接；旧页面只剩仍在引用的共享工具。 |
| #20 CI 检查缺失 | closed | 已有后端 vet、race 和 PostgreSQL 集成测试、SDK 检查、前端 lint/build、Compose 与镜像验证；本次补启动所需环境变量。 |
| #21 前端健壮性 | closed | 已有删除防重、路由错误页、存储异常保护、共享目录删除提示，以及 Nginx 安全头和缓存策略。 |
| #22 接口边界与一致性 | closed | 已有省略状态保护、ID 去重、路径 key 校验、响应规范、触发器 search_path 与引用索引等修复；本次补字段超长错误映射。旧跨 slot Lua 限制已随 String 协议移除。 |

## 本次实现的边界

- 保留失效服务端点，允许管理员显式删除，不做自动数据清理；删除仍影响所有共享该服务目录的应用。
- 管理授权使用必填 `SERVICEUK`；Redis 前缀从必填 `LUCKENV` 推导。启用 OIDC 前需按运维手册先准备管理员服务权限，防止首次启用后无权访问。
- 管理写操作记录 `ManagementAudit`，包含认证主体、方法、路径、查询中的应用/服务范围、HTTP 状态、结果与请求 ID；不保存请求体、凭证或配置差异。日志沿用现有输出和文件配置。
- 主体菜单 SQL 在一个快照中读取菜单与授权关系，仍检查应用启用、角色启用、主体/域边界和完整祖先链；未开放新的业务调用认证方式。
- PostgreSQL 字符串超长返回参数错误；无效记录不落库。

## 验证结果

- 后端：在专用 PostgreSQL 16 空库初始化 `001_init.sql` 后，`go test -race -count=1 -timeout=180s ./...`、`go vet ./...`、`go build ./...` 全部通过，包含本次菜单目录故障、字段超长、失效端点清理和管理审计回归。
- Go SDK：`go vet ./...` 与 `go test -race -count=1 ./...` 通过。
- Dashboard：`npm run lint` 通过，0 error、16 个既有 warning；`npm run build` 通过，仍有既有 chunk 大小及静态/动态导入提示。
- 浏览器：Playwright 全部 88 个用例通过，包括新增失效服务端点清理用例。
- 两套 Compose：分别使用 `.env.example`、`.env.dev.example` 执行 `docker compose config -q` 通过；CI YAML 可解析。
- `git diff --check` 通过。浏览器测试生成的既有截图变更和临时依赖链接不纳入提交。

本次没有连接生产环境、轮换历史口令、执行历史改写或关闭 GitHub issues。真实 OIDC 登录与生产镜像部署需在对应环境按运维手册验收。
