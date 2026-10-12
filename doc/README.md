# NexusAuthorization 文档

- [快速开始与使用流程](quick-start.md)
- [Docker 部署与环境变量](docker-deployment-1.0.2.md)
- [前端开发](frontend-development.md)
- [UI 设计资源](ui-design.md)
- [功能与数据模型](current-system-overview.md)
- [完整表结构](current-database-schema.md)
- [应用边界 RBAC 设计](rbac-v1-design.md)
- [接口参考](reference-api-rbac-v1.md)
- [Go SDK 接入](sdk-application-integration.md)

以下为后端功能和接口概述。


当前实现的中文说明：[功能与数据模型](current-system-overview.md) · [完整表结构](current-database-schema.md)。

Permission Center uses a business application as the permission boundary for roles, menus, subject-role bindings, and published authorization snapshots. Service resources remain a shared API catalog: an application must explicitly associate a service resource before its API endpoints can be selected.

The runtime authorization context is always both the application key and the service resource:

```text
appKey + serviceResource + subject + domain + method + path -> allow / deny
```

Management APIs:

- `GET/POST /v1/applications`
- `PUT /v1/applications/{key}`
- `GET/PUT /v1/applications/{key}/service-resources`
- Application-scoped RBAC APIs under `/v1/rbac-v1/*`, all requiring `app_key`; endpoint import and endpoint writes also require `service_resource`.
- `POST /v1/authorize` with `appKey`, `serviceResource`, `subject`, `domain`, `method`, and `path`.

The configuration flow is application first: associate enabled service resources, choose an associated service for Swagger/OpenAPI import, configure roles and menus, bind roles to subjects, simulate with both scope values, and publish the application snapshot. Association does not grant API access by itself.

Service resources can continue to come from the local catalog or NexusAuth, and existing deployment and authentication configuration remains applicable. The retired service-scoped RBAC routes must not be used for roles, menus, user bindings, releases, or simulation.

Initialization uses a single schema script, `migrations/001_init.sql`. For a local rebuild, back up the existing database, recreate the `permission_center` database, and execute that script. The rebuild clears existing data; there is no `002_applications.sql` upgrade, legacy incremental migration, or old data seed to run. Restore or import old data only as an explicit, separately reviewed operation.

Run the backend and dashboard as direct processes in separate terminals during local development:

```bash
cd src
go run ./cmd/api-server
```

```bash
cd dashboard
npm run dev
```

The retired service-scoped RBAC routes are not part of the supported API surface.

The Go runtime SDK change is breaking: `Client.Check` requires `Request.AppKey` and `Request.ServiceResource`, and `Client.Require` requires both fixed trusted configuration values. See [SDK application integration](sdk-application-integration.md).

See [应用边界 API RBAC 设计](rbac-v1-design.md) for the data model and concrete endpoint contract, and [参考接口](reference-api-rbac-v1.md) for the lower-level API reference.

Application Web entry addresses are optional `url` fields on application create/update/list APIs. Only absolute HTTP(S) URLs without embedded credentials are accepted. Enabled applications with a valid address offer an “打开应用” link in a new browser tab. Apply `migrations/002_application_url.sql` to existing databases before running the updated backend; it adds an empty-default column without changing existing applications. New databases include the field in `001_init.sql`.
