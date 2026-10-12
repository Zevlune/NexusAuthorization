# 应用选择与新建页面设计

沿用 dashboard/src/main.css 现有主题：蓝色主操作 #2563eb，页面 #f4f6f8，白色容器，原有文字与边框颜色。前端使用主题变量支持深色模式。

实现顺序：
1. 在 [`../ui/ui.pen`](../ui/ui.pen) 中绘制应用列表和新建抽屉。
2. 还原布局，修复独立样式加载和卡片按钮嵌套。
3. 补齐搜索、停用应用编辑、必填校验、保存状态和失败重试。
4. 构建并验证桌面、移动端和深色显示。

设计文件：[`../ui/ui.pen`](../ui/ui.pen)；导出预览：[`../ui/exports/bi8Au.png`](../ui/exports/bi8Au.png)（应用列表）、[`../ui/exports/M0pzmt.png`](../ui/exports/M0pzmt.png)（新建抽屉）。设计中的应用为示例数据。

交互：先选择应用，再进入；编辑与选择为独立操作。新建抽屉按名称、唯一标识、描述、启用状态填写，保存按钮固定在底部。停用应用继续展示，便于编辑和重新启用。窄屏单列，抽屉占满屏幕。

验证结果：TypeScript/Vite 构建与页面 ESLint 通过；Playwright 使用模拟 API 验证选择、搜索、停用项编辑、必填校验、Enter 提交、保存失败后重试、创建成功选择、重新启用、加载失败重试及空态。检查 1440px 桌面、390px 移动端和深色模式；验证截图位于 [`../ui/verification/`](../ui/verification/)，示例数据未写入数据库。

## 资源管理与二级导航

导航分为配置总览、资源管理（服务资源/API接口/菜单与按钮）、RBAC授权（角色管理/用户授权）、发布与验证（发布版本/权限模拟）。既有URL保持可访问。

新增设计：服务目录列表与新建/关联抽屉、API列表与Swagger导入、菜单按钮树与两种新增表单。导入仅选择应用已关联服务；菜单是页面入口，按钮是所属菜单的操作权限。统一沿用既有主题。

新增设计图对应表：

| 页面 / 操作 | 预览 |
| --- | --- |
| 服务资源管理 | [`../ui/exports/S3I98.png`](../ui/exports/S3I98.png) |
| 新建服务资源 | [`../ui/exports/Luj0G.png`](../ui/exports/Luj0G.png) |
| 关联已有服务（旧方案，当前使用表格行内操作） | [`../ui/exports/ch45n.png`](../ui/exports/ch45n.png) |
| API 接口管理 | [`../ui/exports/GiEDz.png`](../ui/exports/GiEDz.png) |
| Swagger 导入 | [`../ui/exports/o4xj7l.png`](../ui/exports/o4xj7l.png) |
| 菜单与按钮树 | [`../ui/exports/YL7Be.png`](../ui/exports/YL7Be.png) |
| 新增菜单 | [`../ui/exports/QgXRf.png`](../ui/exports/QgXRf.png) |
| 新增按钮 | [`../ui/exports/H8bGNL.png`](../ui/exports/H8bGNL.png) |

实现：原URL保留，侧栏改为SubMenu两级导航并按当前路由展开分组；资源页面拆分为独立组件。服务目录支持新建/编辑/确认删除（本地可写目录）、关联/取消关联和查看API；NexusAuth目录隐藏资源新增/编辑/删除入口，显示只读说明，后端拒绝写入。Swagger仅选择当前应用已关联且启用的服务，并显示新增/跳过统计。菜单按父子层级展示，按钮必须归属菜单；父级排除自己及后代，有子项的节点删除前须先处理子项。角色授权区标识菜单/按钮类型。

新增验证：TypeScript/Vite构建和变更页面ESLint通过，git diff --check通过。模拟API浏览器验证服务新增/编辑/删除（取消、失败重试）、远程只读入口隐藏、服务关联、服务到API筛选跳转、Swagger必填/失败重试/结果、按钮创建与父菜单关联、父节点删除保护、二级导航、浅/深色和390px三个资源抽屉完整显示。截图位于 [`../ui/verification/`](../ui/verification/)。验证未操作真实数据库或SSO配置。
