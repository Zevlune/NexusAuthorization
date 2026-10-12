import { createRequire } from 'node:module';
import type { Page, Route } from '../../../../dashboard/node_modules/@playwright/test';

// Resolve Playwright from the Dashboard package while the test suite lives at the repository root.
const requireDashboardDependency = createRequire(new URL('../../../../dashboard/package.json', import.meta.url));
const { expect, test } = requireDashboardDependency('@playwright/test') as typeof import('../../../../dashboard/node_modules/@playwright/test');

const SERVICE_RESOURCE_STORAGE_KEY = 'permission-center-service-resource';
const LAYOUT_TABS_STORAGE_KEY = 'permission-center-layout-tabs';
const FIRST_SERVICE_RESOURCE = {
  id: 'service-resource-content',
  key: 'content-platform',
  name: 'legacy-content-platform',
  display_name: '内容平台',
  audience: 'permission.center.api',
  description: '端到端测试服务资源一',
  is_active: true,
  source: 'local' as const,
  created_at: '2026-01-01T00:00:00Z',
};
const SECOND_SERVICE_RESOURCE = {
  id: 'service-resource-analytics',
  key: 'analytics-platform',
  name: 'legacy-analytics-platform',
  display_name: '数据平台',
  audience: 'permission.center.analytics',
  description: '端到端测试服务资源二',
  is_active: true,
  source: 'nexusauth' as const,
  created_at: '2026-01-02T00:00:00Z',
};

type ServiceResourceRecord = {
  id: string;
  key: string;
  name: string;
  display_name: string;
  audience: string;
  description: string;
  is_active: boolean;
  source: 'local' | 'nexusauth';
  created_at: string;
};

type RoleRecord = {
  id: string;
  service_resource: string;
  code: string;
  name: string;
  description: string;
  enabled: boolean;
};

type MenuRecord = {
  id: string;
  service_resource: string;
  parent_id: string | null;
  code: string;
  name: string;
  description: string;
  type: 'menu' | 'button';
  path: string;
  component: string;
  api_path: string;
  http_method: string;
  icon: string;
  sort: number;
  enabled: boolean;
  children?: MenuRecord[];
};

type EndpointRecord = {
  id: string;
  service_resource: string;
  controller: string;
  method: string;
  path_template: string;
  summary: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

type PolicyRecord = {
  id: string;
  service_resource: string;
  code: string;
  name: string;
  description: string;
  effect: 'allow' | 'deny';
  status: 'draft' | 'published' | 'disabled' | 'archived';
  authorization_type: 'api' | 'data';
  priority: number;
  condition: Record<string, unknown> | null;
  current_version: number;
  role_ids: string[];
  endpoint_ids: string[];
};

type RoleRequest = {
  method: string;
  path: string;
  payload?: Record<string, unknown>;
};

type PermissionMockState = {
  authenticated: boolean;
  serviceResourcesWritable: boolean;
  serviceResources: ServiceResourceRecord[];
  rolesByServiceResource: Record<string, RoleRecord[]>;
  menusByServiceResource: Record<string, MenuRecord[]>;
  endpointsByServiceResource: Record<string, EndpointRecord[]>;
  policiesByServiceResource: Record<string, PolicyRecord[]>;
  userRoleIDs: string[];
  roleRequests: string[];
  roleMutationRequests: RoleRequest[];
  menuRequests: RoleRequest[];
  serviceResourceMutationRequests: RoleRequest[];
  endpointRequests: RoleRequest[];
  swaggerImportRequests: RoleRequest[];
  userRoleRequests: string[];
  userRoleReads: number;
  serviceResourceRequests: number;
};

const makeRole = (
  serviceResource: string,
  id: string,
  code: string,
  name: string,
  description = '',
): RoleRecord => ({
  id,
  service_resource: serviceResource,
  code,
  name,
  description,
  enabled: true,
});

const makeMenu = (
  serviceResource: string,
  id: string,
  code: string,
  name: string,
  type: 'menu' | 'button' = 'menu',
  parentID: string | null = null,
  fields: Partial<MenuRecord> = {},
): MenuRecord => ({
  id,
  service_resource: serviceResource,
  parent_id: parentID,
  code,
  name,
  description: '',
  type,
  path: type === 'menu' ? `/${code}` : '',
  component: type === 'menu' ? `/pages/${code}` : '',
  api_path: type === 'button' ? `/v1/${code}` : '',
  http_method: type === 'button' ? 'POST' : '',
  icon: type === 'menu' ? 'menu' : '',
  sort: 0,
  enabled: true,
  ...fields,
});

const makeNavigationMenus = (serviceResource: string): MenuRecord[] => [
  makeMenu(serviceResource, `${serviceResource}-permission-center`, 'permission-center', '权限中心', 'menu', null, {
    path: '/permission-center', component: '', icon: 'lock', sort: 10,
  }),
  makeMenu(serviceResource, `${serviceResource}-roles`, 'role-management', '角色管理', 'menu', `${serviceResource}-permission-center`, {
    path: '/roles', component: '/permission-center/role-management/index.tsx', icon: 'usergroup', sort: 10,
  }),
  makeMenu(serviceResource, `${serviceResource}-policies`, 'policy-management', 'PDP 授权策略', 'menu', `${serviceResource}-permission-center`, {
    path: '/policies', component: '/permission-center/policy-management/index.tsx', icon: 'lock', sort: 40,
  }),
  makeMenu(serviceResource, `${serviceResource}-developer-center`, 'developer-center', '开发者中心', 'menu', null, {
    path: '/developer-center', component: '', icon: 'tools', sort: 30,
  }),
  makeMenu(serviceResource, `${serviceResource}-service-resources`, 'service-resource-management', '服务资源管理', 'menu', `${serviceResource}-developer-center`, {
    path: '/service-resources', component: '/permission-center/service-resource-management/index.tsx', icon: 'cloud', sort: 10,
  }),
  makeMenu(serviceResource, `${serviceResource}-api-endpoints`, 'api-endpoint-management', 'API 端点管理', 'menu', `${serviceResource}-developer-center`, {
    path: '/api-endpoints', component: '/permission-center/api-endpoint-management/index.tsx', icon: 'api', sort: 20,
  }),
  makeMenu(serviceResource, `${serviceResource}-menus`, 'menu-management', '菜单管理', 'menu', `${serviceResource}-developer-center`, {
    path: '/menus', component: '/permission-center/menu-management/index.tsx', icon: 'menu', sort: 30,
  }),
];

const makeEndpoint = (
  serviceResource: string,
  id: string,
  controller: string,
  method: string,
  pathTemplate: string,
  summary: string,
  fields: Partial<EndpointRecord> = {},
): EndpointRecord => ({
  id,
  service_resource: serviceResource,
  controller,
  method,
  path_template: pathTemplate,
  summary,
  enabled: true,
  created_at: '2026-01-03T00:00:00Z',
  updated_at: '2026-01-03T00:00:00Z',
  ...fields,
});

const buildMenuTree = (items: MenuRecord[]): MenuRecord[] => {
  const nodes = new Map<string, MenuRecord>(items.map((item): [string, MenuRecord] => [item.id, { ...item, children: [] }]));
  const roots: MenuRecord[] = [];
  nodes.forEach((node) => {
    if (node.parent_id) {
      const parent = nodes.get(node.parent_id);
      if (parent) {
        parent.children = [...(parent.children || []), node];
        return;
      }
    }
    roots.push(node);
  });
  return roots;
};

const jsonResult = (result: unknown) => ({
  success: true,
  errorCode: null,
  errorMessage: null,
  result,
});

const fulfillJSON = async (route: Route, body: unknown, status = 200) => {
  await route.fulfill({
    status,
    contentType: 'application/json',
    body: JSON.stringify(body),
  });
};

const getServiceResourceRecords = (): ServiceResourceRecord[] => [
  { ...FIRST_SERVICE_RESOURCE },
  { ...SECOND_SERVICE_RESOURCE },
];

const installAPIMocks = async (page: Page): Promise<PermissionMockState> => {
  const state: PermissionMockState = {
    authenticated: true,
    serviceResourcesWritable: true,
    serviceResources: getServiceResourceRecords(),
    rolesByServiceResource: {
      [FIRST_SERVICE_RESOURCE.key]: [
        makeRole(FIRST_SERVICE_RESOURCE.key, 'role-editor', 'content-editor', '内容编辑'),
        makeRole(FIRST_SERVICE_RESOURCE.key, 'role-reviewer', 'content-reviewer', '内容审核'),
      ],
      [SECOND_SERVICE_RESOURCE.key]: [
        makeRole(SECOND_SERVICE_RESOURCE.key, 'role-analyst', 'data-analyst', '数据分析'),
      ],
    },
    menusByServiceResource: {
      [FIRST_SERVICE_RESOURCE.key]: [
        ...makeNavigationMenus(FIRST_SERVICE_RESOURCE.key),
        makeMenu(FIRST_SERVICE_RESOURCE.key, 'menu-posts', 'posts', '帖子管理', 'menu', null, {
          description: '内容菜单',
          path: '/posts',
          component: '/pages/posts',
          sort: 1,
        }),
        makeMenu(FIRST_SERVICE_RESOURCE.key, 'menu-post-create', 'post-create', '创建帖子', 'button', 'menu-posts', {
          description: '创建帖子按钮',
          api_path: '/v1/posts',
          http_method: 'POST',
          sort: 2,
        }),
      ],
      [SECOND_SERVICE_RESOURCE.key]: makeNavigationMenus(SECOND_SERVICE_RESOURCE.key),
    },
    endpointsByServiceResource: {
      [FIRST_SERVICE_RESOURCE.key]: [
        makeEndpoint(FIRST_SERVICE_RESOURCE.key, 'endpoint-post-list', 'PostController', 'GET', '/v1/posts', '查询帖子'),
        makeEndpoint(FIRST_SERVICE_RESOURCE.key, 'endpoint-post-import', 'PostController', 'POST', '/v1/posts/import', '导入帖子', { enabled: false }),
      ],
      [SECOND_SERVICE_RESOURCE.key]: [],
    },
    policiesByServiceResource: {
      [FIRST_SERVICE_RESOURCE.key]: [],
      [SECOND_SERVICE_RESOURCE.key]: [],
    },
    userRoleIDs: [],
    roleRequests: [],
    roleMutationRequests: [],
    menuRequests: [],
    serviceResourceMutationRequests: [],
    endpointRequests: [],
    swaggerImportRequests: [],
    userRoleRequests: [],
    userRoleReads: 0,
    serviceResourceRequests: 0,
  };

  await page.route('**/api/auth/me', async (route) => {
    if (!state.authenticated) {
      await fulfillJSON(route, {
        success: false,
        errorCode: 'UNAUTHENTICATED',
        errorMessage: '未登录',
        result: null,
      }, 401);
      return;
    }
    await fulfillJSON(route, jsonResult({
      isAuthenticated: true,
      user: { sub: 'e2e-user', name: 'Playwright 用户', email: 'e2e@example.test' },
    }));
  });

  await page.route('**/api/auth/logout', async (route) => {
    state.authenticated = false;
    await fulfillJSON(route, jsonResult({ logoutUrl: '' }));
  });

  await page.route('**/api/v1/service-resources**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() === 'GET' && url.pathname === '/api/v1/service-resources') {
      state.serviceResourceRequests += 1;
      await fulfillJSON(route, jsonResult({ items: state.serviceResources, writable: state.serviceResourcesWritable }));
      return;
    }

    if (request.method() === 'POST' && url.pathname === '/api/v1/service-resources') {
      const payload = request.postDataJSON() as Record<string, unknown>;
      const key = String(payload.key || '');
      const resource: ServiceResourceRecord = {
        id: `service-resource-${state.serviceResources.length + 1}`,
        key,
        name: key,
        display_name: String(payload.display_name || ''),
        audience: String(payload.audience || ''),
        description: String(payload.description || ''),
        is_active: payload.is_active !== false,
        source: 'local',
        created_at: '2026-01-04T00:00:00Z',
      };
      state.serviceResources = [...state.serviceResources, resource];
      state.serviceResourceMutationRequests.push({ method: 'POST', path: url.pathname, payload });
      await fulfillJSON(route, jsonResult(resource), 201);
      return;
    }

    const resourceMatch = url.pathname.match(/^\/api\/v1\/service-resources\/([^/]+)$/);
    if (resourceMatch && (request.method() === 'PUT' || request.method() === 'DELETE')) {
      const resourceKey = decodeURIComponent(resourceMatch[1]);
      const resource = state.serviceResources.find((item) => item.key === resourceKey);
      const payload = request.method() === 'PUT' ? request.postDataJSON() as Record<string, unknown> : undefined;
      state.serviceResourceMutationRequests.push({ method: request.method(), path: url.pathname, payload });
      if (!resource || resource.source !== 'local') {
        await fulfillJSON(route, jsonResult(null), 403);
        return;
      }
      if (request.method() === 'DELETE') {
        state.serviceResources = state.serviceResources.filter((item) => item.key !== resourceKey);
        await fulfillJSON(route, jsonResult(null));
        return;
      }
      const nextResource: ServiceResourceRecord = {
        ...resource,
        display_name: String(payload?.display_name ?? resource.display_name),
        audience: String(payload?.audience ?? resource.audience),
        description: String(payload?.description ?? resource.description),
        is_active: payload?.is_active !== undefined ? payload.is_active === true : resource.is_active,
      };
      state.serviceResources = state.serviceResources.map((item) => item.key === resourceKey ? nextResource : item);
      await fulfillJSON(route, jsonResult(nextResource));
      return;
    }

    await fulfillJSON(route, jsonResult(null), 404);
  });

  await page.route('**/api/v1/roles**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const serviceResource = url.searchParams.get('service_resource') || '';

    if (request.method() === 'GET' && path === '/api/v1/roles') {
      state.roleRequests.push(serviceResource);
      await fulfillJSON(route, jsonResult({ items: state.rolesByServiceResource[serviceResource] || [] }));
      return;
    }

    if (request.method() === 'POST' && path === '/api/v1/roles') {
      const payload = request.postDataJSON() as {
        service_resource?: string;
        code?: string;
        name?: string;
        description?: string;
      };
      const serviceResourceRoles = state.rolesByServiceResource[payload.service_resource || ''] || [];
      const role = makeRole(
        payload.service_resource || '',
        `role-${serviceResourceRoles.length + 1}`,
        payload.code || '',
        payload.name || '',
        payload.description || '',
      );
      state.rolesByServiceResource[role.service_resource] = [...serviceResourceRoles, role];
      state.roleMutationRequests.push({ method: 'POST', path, payload });
      await fulfillJSON(route, jsonResult(role));
      return;
    }

    const roleMatch = path.match(/^\/api\/v1\/roles\/([^/]+)$/);
    if (roleMatch && (request.method() === 'PUT' || request.method() === 'DELETE')) {
      const roleID = roleMatch[1];
      const resourceName = Object.keys(state.rolesByServiceResource).find((name) => (
        state.rolesByServiceResource[name].some((role) => role.id === roleID)
      ));
      const currentRoles = resourceName ? state.rolesByServiceResource[resourceName] : [];
      const payload = request.method() === 'PUT' ? request.postDataJSON() as Record<string, unknown> : undefined;
      state.roleMutationRequests.push({ method: request.method(), path, payload });
      if (request.method() === 'DELETE') {
        if (resourceName) {
          state.rolesByServiceResource[resourceName] = currentRoles.filter((role) => role.id !== roleID);
        }
        await fulfillJSON(route, jsonResult(null));
        return;
      }
      const nextRoles = currentRoles.map((role) => role.id === roleID ? {
        ...role,
        code: String(payload?.code ?? role.code),
        name: String(payload?.name ?? role.name),
        description: String(payload?.description ?? role.description),
        enabled: payload?.enabled !== undefined ? payload.enabled === true : role.enabled,
      } : role);
      if (resourceName) {
        state.rolesByServiceResource[resourceName] = nextRoles;
      }
      await fulfillJSON(route, jsonResult(nextRoles.find((role) => role.id === roleID) || null));
      return;
    }

    await fulfillJSON(route, jsonResult(null), 404);
  });

  await page.route('**/api/v1/users/*/roles**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    state.userRoleRequests.push(`${request.method()} ${url.pathname}${url.search}`);
    const userPath = url.pathname.match(/^\/api\/v1\/users\/([^/]+)\/roles$/);
    if (!userPath) {
      await fulfillJSON(route, jsonResult(null), 404);
      return;
    }

    if (request.method() === 'GET') {
      state.userRoleReads += 1;
      await fulfillJSON(route, jsonResult({ role_ids: state.userRoleIDs }));
      return;
    }

    if (request.method() === 'PUT') {
      const payload = request.postDataJSON() as { role_ids?: unknown };
      state.userRoleIDs = Array.isArray(payload.role_ids)
        ? payload.role_ids.filter((roleID): roleID is string => typeof roleID === 'string')
        : [];
      await fulfillJSON(route, jsonResult(null));
      return;
    }

    await fulfillJSON(route, jsonResult(null), 404);
  });

  await page.route('**/api/v1/authorization/api-endpoints**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const serviceResource = url.searchParams.get('service_resource') || FIRST_SERVICE_RESOURCE.key;

    if (request.method() === 'GET' && path === '/api/v1/authorization/api-endpoints') {
      await fulfillJSON(route, jsonResult({ items: state.endpointsByServiceResource[serviceResource] || [] }));
      return;
    }

    if (request.method() === 'POST' && path === '/api/v1/authorization/api-endpoints/import-swagger') {
      const payload = request.postDataJSON() as Record<string, unknown>;
      state.swaggerImportRequests.push({ method: 'POST', path, payload });
      const imported: EndpointRecord = makeEndpoint(
        String(payload.service_resource || serviceResource),
        `endpoint-imported-${state.swaggerImportRequests.length}`,
        'SwaggerController',
        'GET',
        '/v1/imported',
        'Swagger 导入接口',
      );
      const importedServiceResource = imported.service_resource;
      state.endpointsByServiceResource[importedServiceResource] = [
        ...(state.endpointsByServiceResource[importedServiceResource] || []),
        imported,
      ];
      await fulfillJSON(route, jsonResult({ total: 1, created: 1, skipped: 0 }));
      return;
    }

    if (request.method() === 'POST' && path === '/api/v1/authorization/api-endpoints') {
      const payload = request.postDataJSON() as Record<string, unknown>;
      const resourceName = String(payload.service_resource || serviceResource);
      const endpoint: EndpointRecord = makeEndpoint(
        resourceName,
        `endpoint-created-${(state.endpointRequests.filter((item) => item.method === 'POST').length + 1)}`,
        String(payload.controller || ''),
        String(payload.method || 'GET'),
        String(payload.path_template || ''),
        String(payload.summary || ''),
        { enabled: payload.enabled !== false },
      );
      state.endpointRequests.push({ method: 'POST', path, payload });
      state.endpointsByServiceResource[resourceName] = [...(state.endpointsByServiceResource[resourceName] || []), endpoint];
      await fulfillJSON(route, jsonResult(endpoint), 201);
      return;
    }

    const endpointMatch = path.match(/^\/api\/v1\/authorization\/api-endpoints\/([^/]+)$/);
    if (endpointMatch && request.method() === 'GET') {
      const endpointID = decodeURIComponent(endpointMatch[1]);
      const endpoint = Object.values(state.endpointsByServiceResource)
        .flat()
        .find((item) => item.id === endpointID);
      await fulfillJSON(route, jsonResult(endpoint || null), endpoint ? 200 : 404);
      return;
    }

    if (endpointMatch && (request.method() === 'PUT' || request.method() === 'DELETE')) {
      const endpointID = decodeURIComponent(endpointMatch[1]);
      const resourceName = Object.keys(state.endpointsByServiceResource).find((name) => (
        state.endpointsByServiceResource[name].some((item) => item.id === endpointID)
      ));
      const currentEndpoints = resourceName ? state.endpointsByServiceResource[resourceName] : [];
      const payload = request.method() === 'PUT' ? request.postDataJSON() as Record<string, unknown> : undefined;
      state.endpointRequests.push({ method: request.method(), path, payload });
      if (!resourceName) {
        await fulfillJSON(route, jsonResult(null), 404);
        return;
      }
      if (request.method() === 'DELETE') {
        state.endpointsByServiceResource[resourceName] = currentEndpoints.filter((item) => item.id !== endpointID);
        await fulfillJSON(route, jsonResult(null));
        return;
      }
      const nextEndpoints = currentEndpoints.map((item) => item.id === endpointID ? {
        ...item,
        controller: String(payload?.controller ?? item.controller),
        method: String(payload?.method ?? item.method),
        path_template: String(payload?.path_template ?? item.path_template),
        summary: String(payload?.summary ?? item.summary),
        enabled: payload?.enabled !== undefined ? payload.enabled === true : item.enabled,
        updated_at: '2026-01-05T00:00:00Z',
      } : item);
      state.endpointsByServiceResource[resourceName] = nextEndpoints;
      await fulfillJSON(route, jsonResult(nextEndpoints.find((item) => item.id === endpointID) || null));
      return;
    }

    await fulfillJSON(route, jsonResult(null), 404);
  });

  await page.route('**/api/v1/authorization/policies**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const serviceResource = url.searchParams.get('service_resource') || FIRST_SERVICE_RESOURCE.key;
    if (request.method() === 'POST' && path === '/api/v1/authorization/policies/simulate') {
      const payload = request.postDataJSON() as Record<string, unknown>;
      await fulfillJSON(route, jsonResult({ decision: 'allow', reason_code: 'allowed_by_policy', matched_policy_ids: ['policy-created-1'], snapshot_version: 1, request_id: payload.request_id || '' }));
      return;
    }
    if (request.method() === 'GET' && path === '/api/v1/authorization/policies') {
      await fulfillJSON(route, jsonResult({ items: state.policiesByServiceResource[serviceResource] || [] }));
      return;
    }
    if (request.method() === 'POST' && path === '/api/v1/authorization/policies') {
      const payload = request.postDataJSON() as Record<string, unknown>;
      const resourceName = String(payload.service_resource || serviceResource);
      const policy: PolicyRecord = { id: `policy-created-${(state.policiesByServiceResource[resourceName] || []).length + 1}`, service_resource: resourceName, code: String(payload.code || ''), name: String(payload.name || ''), description: String(payload.description || ''), effect: payload.effect === 'deny' ? 'deny' : 'allow', status: 'draft', authorization_type: payload.authorization_type === 'data' ? 'data' : 'api', priority: typeof payload.priority === 'number' ? payload.priority : 0, condition: payload.condition && typeof payload.condition === 'object' ? payload.condition as Record<string, unknown> : null, current_version: 0, role_ids: Array.isArray(payload.role_ids) ? payload.role_ids.map(String) : [], endpoint_ids: Array.isArray(payload.endpoint_ids) ? payload.endpoint_ids.map(String) : [] };
      state.policiesByServiceResource[resourceName] = [...(state.policiesByServiceResource[resourceName] || []), policy];
      await fulfillJSON(route, jsonResult(policy), 201);
      return;
    }
    const policyMatch = path.match(/^\/api\/v1\/authorization\/policies\/([^/]+)(?:\/(publish))?$/);
    if (policyMatch) {
      const policyID = decodeURIComponent(policyMatch[1]);
      const resourceName = Object.keys(state.policiesByServiceResource).find((name) => state.policiesByServiceResource[name].some((item) => item.id === policyID));
      const current = resourceName ? state.policiesByServiceResource[resourceName] : [];
      const policy = current.find((item) => item.id === policyID);
      if (!policy) { await fulfillJSON(route, jsonResult(null), 404); return; }
      if (request.method() === 'POST' && policyMatch[2] === 'publish') {
        const published = { ...policy, status: 'published' as const, current_version: policy.current_version + 1 };
        state.policiesByServiceResource[resourceName!] = current.map((item) => item.id === policyID ? published : item);
        await fulfillJSON(route, jsonResult(published));
        return;
      }
      if (request.method() === 'PUT') {
        const payload = request.postDataJSON() as Record<string, unknown>;
        const updated: PolicyRecord = {
          ...policy,
          code: String(payload.code ?? policy.code),
          name: String(payload.name ?? policy.name),
          description: String(payload.description ?? policy.description),
          effect: payload.effect === 'deny' ? 'deny' : 'allow',
          authorization_type: payload.authorization_type === 'data' ? 'data' : 'api',
          priority: typeof payload.priority === 'number' ? payload.priority : policy.priority,
          condition: payload.condition && typeof payload.condition === 'object' ? payload.condition as Record<string, unknown> : null,
          role_ids: Array.isArray(payload.role_ids) ? payload.role_ids.map(String) : policy.role_ids,
          endpoint_ids: Array.isArray(payload.endpoint_ids) ? payload.endpoint_ids.map(String) : policy.endpoint_ids,
        };
        state.policiesByServiceResource[resourceName!] = current.map((item) => item.id === policyID ? updated : item);
        await fulfillJSON(route, jsonResult(updated));
        return;
      }
      if (request.method() === 'DELETE') { state.policiesByServiceResource[resourceName!] = current.filter((item) => item.id !== policyID); await fulfillJSON(route, jsonResult(null)); return; }
    }
    await fulfillJSON(route, jsonResult(null), 404);
  });

  await page.route('**/api/v1/menus**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const serviceResource = url.searchParams.get('service_resource') || FIRST_SERVICE_RESOURCE.key;

    if (request.method() === 'GET' && path === '/api/v1/menus/tree') {
      await fulfillJSON(route, jsonResult({ items: buildMenuTree(state.menusByServiceResource[serviceResource] || []) }));
      return;
    }

    if (request.method() === 'POST' && path === '/api/v1/menus') {
      const payload = request.postDataJSON() as Record<string, unknown>;
      const resourceName = String(payload.service_resource || serviceResource);
      const items = state.menusByServiceResource[resourceName] || [];
      const id = `menu-created-${state.menuRequests.filter((item) => item.method === 'POST').length + 1}`;
      const menu = makeMenu(
        resourceName,
        id,
        String(payload.code || ''),
        String(payload.name || ''),
        payload.type === 'button' ? 'button' : 'menu',
        typeof payload.parent_id === 'string' ? payload.parent_id : null,
        {
          description: String(payload.description || ''),
          path: String(payload.path || ''),
          component: String(payload.component || ''),
          api_path: String(payload.api_path || ''),
          http_method: String(payload.http_method || ''),
          icon: String(payload.icon || ''),
          sort: typeof payload.sort === 'number' ? payload.sort : 0,
        },
      );
      state.menuRequests.push({ method: 'POST', path, payload });
      state.menusByServiceResource[resourceName] = [...items, menu];
      await fulfillJSON(route, jsonResult(menu), 201);
      return;
    }

    const itemMatch = path.match(/^\/api\/v1\/menus\/([^/]+)$/);
    if (itemMatch && (request.method() === 'PUT' || request.method() === 'DELETE')) {
      const menuID = itemMatch[1];
      const resourceName = Object.keys(state.menusByServiceResource).find((name) => (
        state.menusByServiceResource[name].some((item) => item.id === menuID)
      ));
      const currentItems = resourceName ? state.menusByServiceResource[resourceName] : [];
      const payload = request.method() === 'PUT' ? request.postDataJSON() as Record<string, unknown> : undefined;
      state.menuRequests.push({ method: request.method(), path, payload });
      if (request.method() === 'DELETE') {
        if (resourceName) {
          state.menusByServiceResource[resourceName] = currentItems.filter((item) => item.id !== menuID);
        }
        await fulfillJSON(route, jsonResult(null));
        return;
      }
      const nextItems = currentItems.map((item) => item.id === menuID ? {
        ...item,
        code: String(payload?.code ?? item.code),
        name: String(payload?.name ?? item.name),
        description: String(payload?.description ?? item.description),
        path: String(payload?.path ?? item.path),
        component: String(payload?.component ?? item.component),
        api_path: String(payload?.api_path ?? item.api_path),
        http_method: String(payload?.http_method ?? item.http_method),
        icon: String(payload?.icon ?? item.icon),
        sort: typeof payload?.sort === 'number' ? payload.sort : item.sort,
        enabled: payload?.enabled !== undefined ? payload.enabled === true : item.enabled,
      } : item);
      if (resourceName) {
        state.menusByServiceResource[resourceName] = nextItems;
      }
      await fulfillJSON(route, jsonResult(nextItems.find((item) => item.id === menuID) || null));
      return;
    }

    await fulfillJSON(route, jsonResult(null), 404);
  });

  return state;
};

const seedServiceResource = async (page: Page, serviceResource = FIRST_SERVICE_RESOURCE.key) => {
  await page.addInitScript(({ key, value }) => {
    window.sessionStorage.setItem(key, value);
  }, { key: SERVICE_RESOURCE_STORAGE_KEY, value: serviceResource });
};

test.describe('权限中心关键操作流程', () => {
  test('Header 主题和全屏图标在明暗状态下正确显示', async ({ page }) => {
    await installAPIMocks(page);
    await seedServiceResource(page);
    await page.addInitScript(() => {
      let fullscreen = false;
      Object.defineProperty(Document.prototype, 'fullscreenElement', {
        configurable: true,
        get: () => fullscreen ? document.documentElement : null,
      });
      Object.defineProperty(Document.prototype, 'fullscreenEnabled', { configurable: true, value: true });
      Object.defineProperty(HTMLElement.prototype, 'requestFullscreen', {
        configurable: true,
        value: async () => {
          fullscreen = true;
          document.dispatchEvent(new Event('fullscreenchange'));
        },
      });
      Object.defineProperty(Document.prototype, 'exitFullscreen', {
        configurable: true,
        value: async () => {
          fullscreen = false;
          document.dispatchEvent(new Event('fullscreenchange'));
        },
      });
      window.localStorage.setItem('theme-mode', 'light');
    });
    await page.goto('/dashboard');

    const themeButton = page.getByRole('button', { name: '切换到暗色主题' });
    const fullscreenButton = page.getByRole('button', { name: '进入全屏' });
    await expect(themeButton.locator('.t-icon-moon')).toBeVisible();
    await expect(fullscreenButton.locator('.t-icon-fullscreen-1')).toBeVisible();
    await expect(themeButton).toHaveCSS('color', 'rgb(83, 98, 115)');
    await expect(fullscreenButton).toHaveCSS('color', 'rgb(83, 98, 115)');

    await themeButton.click();
    await expect(page.locator('html')).toHaveAttribute('theme-mode', 'dark');
    await expect(page.getByRole('button', { name: '切换到浅色主题' }).locator('.t-icon-sunny')).toBeVisible();

    await fullscreenButton.click();
    await expect(page.getByRole('button', { name: '退出全屏' }).locator('.t-icon-fullscreen-exit-1')).toBeVisible();
  });

  test('未选择服务资源时直接访问角色管理会重定向到服务资源选择页', async ({ page }) => {
    await installAPIMocks(page);
    await page.goto('/roles');

    await expect(page).toHaveURL(/\/select-service-resource\?redirect=%2Froles$/);
    await expect(page.getByRole('heading', { name: '选择允许访问的服务资源' })).toBeVisible();
    await expect(page.getByText(FIRST_SERVICE_RESOURCE.display_name, { exact: true })).toBeVisible();
  });

  test('选择的服务资源会在刷新后保留，并显示在 Header 中', async ({ page }) => {
    await installAPIMocks(page);
    await seedServiceResource(page);
    await page.goto('/dashboard');

    const currentServiceResource = page.locator('.layout-current-service-resource-value');
    await expect(currentServiceResource).toHaveText(FIRST_SERVICE_RESOURCE.key);

    await page.reload();

    await expect(page).toHaveURL(/\/dashboard$/);
    await expect(currentServiceResource).toHaveText(FIRST_SERVICE_RESOURCE.key);
  });

  test('后台动态路由在 API 端点页面硬刷新后仍可访问', async ({ page }) => {
    await installAPIMocks(page);
    await seedServiceResource(page);
    await page.goto('/api-endpoints');

    await expect(page.getByRole('heading', { name: 'API 端点管理', exact: true })).toBeVisible();
    await expect(page.getByRole('complementary').getByText('API 端点管理', { exact: true })).toBeVisible();
    await expect(page.getByRole('complementary').getByText('用户角色绑定', { exact: true })).toHaveCount(0);

    await page.reload();

    await expect(page).toHaveURL(/\/api-endpoints$/);
    await expect(page.getByRole('heading', { name: 'API 端点管理', exact: true })).toBeVisible();
    await expect(page.getByRole('complementary').getByText('API 端点管理', { exact: true })).toBeVisible();
    await expect(page.getByRole('complementary').getByText('用户角色绑定', { exact: true })).toHaveCount(0);
    await expect(page.getByRole('heading', { name: '404', exact: true })).toHaveCount(0);
  });

  test('服务资源选择页展示可用目录并保持只读', async ({ page }) => {
    const state = await installAPIMocks(page);
    await page.goto('/select-service-resource');

    await expect(page.getByRole('heading', { name: '选择允许访问的服务资源' })).toBeVisible();
    await expect(page.getByText(FIRST_SERVICE_RESOURCE.display_name, { exact: true })).toBeVisible();
    await expect(page.getByText('本地', { exact: true })).toBeVisible();
    await expect(page.getByText('NexusAuth', { exact: true })).toBeVisible();
    await expect.poll(() => state.serviceResourceRequests).toBeGreaterThan(0);
    await expect(page.getByRole('button', { name: '新建服务资源', exact: true })).toHaveCount(0);
  });

  test('侧栏固定展示仪表盘，并只渲染后台返回的启用菜单', async ({ page }) => {
    const state = await installAPIMocks(page);
    state.menusByServiceResource[FIRST_SERVICE_RESOURCE.key] = [
      makeMenu(FIRST_SERVICE_RESOURCE.key, 'legacy-dashboard', 'dashboard', '后台仪表盘', 'menu', null, {
        path: '/dashboard', component: '/permission-center/dashboard/index.tsx', enabled: true,
      }),
      makeMenu(FIRST_SERVICE_RESOURCE.key, 'disabled-menu', 'disabled', '停用菜单', 'menu', null, { enabled: false }),
      makeMenu(FIRST_SERVICE_RESOURCE.key, 'button-node', 'create-item', '新增按钮', 'button'),
    ];
    await seedServiceResource(page);
    await page.goto('/dashboard');

    await expect(page.getByRole('heading', { name: '仪表盘', exact: true })).toBeVisible();
    await expect(page.getByRole('complementary').getByText('仪表盘', { exact: true })).toHaveCount(1);
    await expect(page.getByText('后台仪表盘', { exact: true })).toHaveCount(0);
    await expect(page.getByText('角色管理', { exact: true })).toHaveCount(0);
    await expect(page.getByText('停用菜单', { exact: true })).toHaveCount(0);
    await expect(page.getByText('新增按钮', { exact: true })).toHaveCount(0);
  });

  test('侧栏按照后台菜单的 icon 字段渲染图标', async ({ page }) => {
    await installAPIMocks(page);
    await seedServiceResource(page);
    await page.goto('/dashboard');

    const sidebar = page.getByRole('complementary');
    await expect(sidebar.getByText('开发者中心', { exact: true })).toBeVisible();
    await expect(sidebar.locator('.t-icon-tools')).toBeVisible();
  });

  test('退出登录会清空历史页签，重新登录后只保留仪表盘', async ({ page }) => {
    const state = await installAPIMocks(page);
    await seedServiceResource(page);
    await page.goto('/roles');
    await page.goto('/menus');

    await expect.poll(async () => page.evaluate((key) => {
      const tabs = JSON.parse(window.localStorage.getItem(key) || '[]') as unknown[];
      return tabs.length;
    }, LAYOUT_TABS_STORAGE_KEY)).toBeGreaterThan(1);

    await page.locator('.layout-avatar-trigger').click();
    await page.getByText('退出登录', { exact: true }).click();
    await expect(page).toHaveURL(/\/login/);
    await expect.poll(() => page.evaluate(
      (key) => window.localStorage.getItem(key),
      LAYOUT_TABS_STORAGE_KEY,
    )).toBeNull();

    state.authenticated = true;
    await page.goto('/dashboard');
    const layoutTabs = page.locator('.layout-content-tabs .t-tabs__nav-item');
    await expect(layoutTabs).toHaveCount(1);
    await expect(layoutTabs.first()).toContainText('仪表盘');
    await expect(page.evaluate(
      (key) => JSON.parse(window.localStorage.getItem(key) || '[]'),
      LAYOUT_TABS_STORAGE_KEY,
    )).resolves.toEqual([{ value: '/dashboard', label: '仪表盘', removable: false }]);
  });

  test('Header 切换服务资源后返回原页面，并按新服务资源刷新角色列表', async ({ page }) => {
    const state = await installAPIMocks(page);
    await seedServiceResource(page);
    await page.goto('/roles');

    await expect(page.getByText('内容编辑', { exact: true })).toBeVisible();
    await page.getByRole('button', { name: '切换服务资源', exact: true }).click();
    await expect(page).toHaveURL(/\/select-service-resource\?redirect=%2Froles$/);

    await page.locator('.service-resource-card').filter({ hasText: SECOND_SERVICE_RESOURCE.display_name }).click();
    await page.getByRole('button', { name: '继续并进入控制台', exact: true }).click();

    await expect(page).toHaveURL(/\/roles$/);
    await expect(page.locator('.layout-current-service-resource-value')).toHaveText(SECOND_SERVICE_RESOURCE.key);
    await expect(page.getByText('数据分析', { exact: true })).toBeVisible();
    await expect(page.getByText('内容编辑', { exact: true })).not.toBeVisible();
    expect(state.roleRequests).toContain(SECOND_SERVICE_RESOURCE.key);
  });

  test('创建角色成功后关闭弹窗、重置表单并刷新列表', async ({ page }) => {
    await installAPIMocks(page);
    await seedServiceResource(page);
    await page.goto('/roles');

    await page.getByRole('button', { name: '新建角色', exact: true }).click();
    const dialog = page.locator('.t-dialog').filter({ hasText: '新建角色' });
    await expect(dialog).toBeVisible();
    await dialog.getByPlaceholder('例如 content-editor').fill('new-role');
    await dialog.getByPlaceholder('例如 内容编辑').fill('新建角色');
    await dialog.getByPlaceholder('可选').fill('创建后应刷新');
    await dialog.getByRole('button', { name: '创建', exact: true }).click();

    await expect(dialog).toBeHidden();
    await expect(page.getByRole('table').getByText('新建角色', { exact: true })).toBeVisible();

    await page.getByRole('button', { name: '新建角色', exact: true }).click();
    const freshDialog = page.locator('.t-dialog').filter({ hasText: '新建角色' });
    await expect(freshDialog.getByPlaceholder('例如 content-editor')).toHaveValue('');
    await expect(freshDialog.getByPlaceholder('例如 内容编辑')).toHaveValue('');
    await expect(freshDialog.getByPlaceholder('可选')).toHaveValue('');
  });

  test('编辑角色提交冻结字段并刷新列表', async ({ page }) => {
    const state = await installAPIMocks(page);
    await seedServiceResource(page);
    await page.goto('/roles');

    const roleRow = page.getByRole('row').filter({ hasText: '内容编辑' }).first();
    await roleRow.getByRole('button', { name: '编辑', exact: true }).click();
    const dialog = page.locator('.t-dialog').filter({ hasText: '编辑角色' });
    await expect(dialog).toBeVisible();
    await dialog.getByPlaceholder('例如 content-editor').fill('content-editor-updated');
    await dialog.getByPlaceholder('例如 内容编辑').fill('内容编辑（更新）');
    await dialog.getByPlaceholder('可选').fill('编辑后描述');
    await dialog.locator('.t-select').click();
    await page.locator('.t-select-option').filter({ hasText: '停用' }).click();

    const updateRequest = page.waitForRequest((request) => (
      request.method() === 'PUT'
      && request.url().endsWith('/api/v1/roles/role-editor')
    ));
    await dialog.getByRole('button', { name: '保存', exact: true }).click();
    await updateRequest;

    const payload = state.roleMutationRequests.find((item) => item.method === 'PUT')?.payload;
    expect(Object.keys(payload || {}).sort()).toEqual(['code', 'description', 'enabled', 'name']);
    expect(payload).toEqual({
      code: 'content-editor-updated',
      name: '内容编辑（更新）',
      description: '编辑后描述',
      enabled: false,
    });
    await expect(page.getByText('内容编辑（更新）', { exact: true })).toBeVisible();
    await expect(page.getByText('停用', { exact: true })).toBeVisible();
  });

  test('删除角色需要确认并刷新列表', async ({ page }) => {
    const state = await installAPIMocks(page);
    await seedServiceResource(page);
    await page.goto('/roles');

    const roleRow = page.getByRole('row').filter({ hasText: '内容审核' }).first();
    await roleRow.getByRole('button', { name: '删除', exact: true }).click();
    const dialog = page.locator('.t-dialog').filter({ hasText: '删除角色' });
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText('内容审核');

    const deleteRequest = page.waitForRequest((request) => (
      request.method() === 'DELETE'
      && request.url().endsWith('/api/v1/roles/role-reviewer')
    ));
    await dialog.getByRole('button', { name: '确认删除', exact: true }).click();
    await deleteRequest;

    expect(state.roleMutationRequests.some((item) => item.method === 'DELETE')).toBe(true);
    await expect(page.getByText('内容审核', { exact: true })).toHaveCount(0);
  });

  test('创建菜单提交服务资源和节点字段并刷新树', async ({ page }) => {
    const state = await installAPIMocks(page);
    await seedServiceResource(page);
    await page.goto('/menus');

    await expect(page.locator('.permission-tree-header').getByText('操作', { exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: '新建菜单', exact: true })).toHaveCount(0);
    await expect(page.getByRole('button', { name: '新建按钮', exact: true })).toHaveCount(0);
    await page.getByRole('button', { name: '新建权限节点', exact: true }).click();
    const drawer = page.locator('.t-drawer').filter({ hasText: '新建菜单' });
    await expect(drawer).toBeVisible();
    await expect(drawer.getByText('类型', { exact: true })).toBeVisible();
    await drawer.getByPlaceholder('例如 article-management').fill('article-management');
    await drawer.getByPlaceholder('例如 文章管理').fill('文章管理');
    await drawer.getByPlaceholder('例如 /articles').fill('/articles');
    await drawer.getByPlaceholder('例如 /pages/articles').fill('/pages/articles');
    const createRequest = page.waitForRequest((request) => (
      request.method() === 'POST'
      && request.url().endsWith('/api/v1/menus')
    ));
    await drawer.getByRole('button', { name: '创建', exact: true }).click();
    await createRequest;

    await expect(drawer).toBeHidden();
    await expect(page.getByText('文章管理', { exact: true })).toBeVisible();
    await expect(page.getByRole('complementary').getByText('文章管理', { exact: true })).toBeVisible();
    const payload = state.menuRequests.find((item) => item.method === 'POST')?.payload;
    expect(payload).toMatchObject({
      service_resource: FIRST_SERVICE_RESOURCE.key,
      code: 'article-management',
      name: '文章管理',
      type: 'menu',
      parent_id: null,
      path: '/articles',
      component: '/pages/articles',
    });

    await page.getByRole('button', { name: '新建权限节点', exact: true }).click();
    const freshDrawer = page.locator('.t-drawer').filter({ hasText: '新建菜单' });
    await expect(freshDrawer).toBeVisible();
    await expect(freshDrawer.getByPlaceholder('例如 article-management')).toHaveValue('');
    await expect(freshDrawer.getByPlaceholder('例如 文章管理')).toHaveValue('');
    await expect(freshDrawer.getByPlaceholder('例如 /articles')).toHaveValue('');
    await expect(freshDrawer.getByPlaceholder('例如 /pages/articles')).toHaveValue('');
    await expect(freshDrawer.getByPlaceholder('0')).toHaveValue('0');

    await freshDrawer.locator('.t-select').nth(0).click();
    await page.locator('.t-select-option').filter({ hasText: '按钮' }).click();
    await expect(page.locator('.t-drawer').getByText('API 路径', { exact: true })).toBeVisible();
    const buttonDrawer = page.locator('.t-drawer');
    await buttonDrawer.locator('.t-select').nth(1).click();
    await page.locator('.t-select-option').filter({ hasText: '文章管理' }).click();
    await buttonDrawer.getByPlaceholder('例如 article-management').fill('article-create');
    await buttonDrawer.getByPlaceholder('例如 文章管理').fill('新建文章');
    await buttonDrawer.getByPlaceholder('例如 /v1/articles').fill('/v1/articles');
    await buttonDrawer.locator('.t-select').nth(2).click();
    await page.locator('.t-select-option').filter({ hasText: 'POST' }).click();
    const createButtonRequest = page.waitForRequest((request) => (
      request.method() === 'POST'
      && request.url().endsWith('/api/v1/menus')
    ));
    await buttonDrawer.getByRole('button', { name: '创建', exact: true }).click();
    await createButtonRequest;

    expect(state.menuRequests.find((item) => item.method === 'POST' && item.payload.code === 'article-create')?.payload).toMatchObject({
      service_resource: FIRST_SERVICE_RESOURCE.key,
      parent_id: expect.any(String),
      code: 'article-create',
      name: '新建文章',
      type: 'button',
      api_path: '/v1/articles',
      http_method: 'POST',
    });
  });

  test('编辑菜单提交冻结字段并刷新树', async ({ page }) => {
    const state = await installAPIMocks(page);
    await seedServiceResource(page);
    await page.goto('/menus');

    const menuRow = page.locator('.permission-tree-row').filter({ has: page.getByText('帖子管理', { exact: true }) });
    await menuRow.getByRole('button', { name: '编辑', exact: true }).click();
    const drawer = page.locator('.t-drawer').filter({ hasText: '编辑菜单' });
    await expect(drawer).toBeVisible();
    await expect(drawer.getByPlaceholder('例如 article-management')).toHaveValue('posts');
    await expect(drawer.getByPlaceholder('例如 文章管理')).toHaveValue('帖子管理');
    await expect(drawer.getByPlaceholder('可选').first()).toHaveValue('内容菜单');
    await expect(drawer.getByPlaceholder('例如 /articles')).toHaveValue('/posts');
    await expect(drawer.getByPlaceholder('例如 /pages/articles')).toHaveValue('/pages/posts');
    await expect(drawer.getByPlaceholder('0')).toHaveValue('1');
    await drawer.getByPlaceholder('例如 article-management').fill('posts-updated');
    await drawer.getByPlaceholder('例如 文章管理').fill('帖子中心');
    await drawer.getByPlaceholder('例如 /articles').fill('/post-center');
    await drawer.getByPlaceholder('例如 /pages/articles').fill('/pages/post-center');
    await drawer.locator('.t-select').last().click();
    await page.locator('.t-select-option').filter({ hasText: '停用' }).click();

    const updateRequest = page.waitForRequest((request) => (
      request.method() === 'PUT'
      && request.url().endsWith('/api/v1/menus/menu-posts')
    ));
    await drawer.getByRole('button', { name: '保存', exact: true }).click();
    await updateRequest;

    const payload = state.menuRequests.find((item) => item.method === 'PUT')?.payload;
    expect(Object.keys(payload || {}).sort()).toEqual([
      'api_path',
      'code',
      'component',
      'description',
      'enabled',
      'http_method',
      'icon',
      'name',
      'path',
      'sort',
    ]);
    expect(payload).toMatchObject({
      code: 'posts-updated',
      name: '帖子中心',
      path: '/post-center',
      component: '/pages/post-center',
      enabled: false,
    });
    await expect(page.getByText('帖子中心', { exact: true })).toBeVisible();
    await expect(page.getByText('停用', { exact: true })).toBeVisible();

    await page.reload();
    const reloadedRow = page.locator('.permission-tree-row').filter({ has: page.getByText('帖子中心', { exact: true }) });
    await expect(reloadedRow).toBeVisible();
    await expect(reloadedRow.getByText('停用', { exact: true })).toBeVisible();
  });

  test('删除菜单按钮需要确认并刷新树', async ({ page }) => {
    const state = await installAPIMocks(page);
    await seedServiceResource(page);
    await page.goto('/menus');

    const menuRow = page.locator('.permission-tree-row').filter({ has: page.getByText('创建帖子', { exact: true }) });
    await menuRow.getByRole('button', { name: '删除', exact: true }).click();
    const dialog = page.locator('.t-dialog').filter({ hasText: '删除权限节点' });
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText('创建帖子');

    const deleteRequest = page.waitForRequest((request) => (
      request.method() === 'DELETE'
      && request.url().endsWith('/api/v1/menus/menu-post-create')
    ));
    await dialog.getByRole('button', { name: '确认删除', exact: true }).click();
    await deleteRequest;

    expect(state.menuRequests.some((item) => item.method === 'DELETE')).toBe(true);
    await expect(page.getByText('创建帖子', { exact: true })).toHaveCount(0);
  });

  test('角色页内分配用户会查询、保存并重新读取服务端角色数据', async ({ page }) => {
    const state = await installAPIMocks(page);
    await seedServiceResource(page);
    await page.goto('/roles');

    const roleRow = page.getByRole('row').filter({ hasText: '内容编辑' }).first();
    await roleRow.getByRole('button', { name: '分配用户', exact: true }).click();
    const drawer = page.locator('.t-drawer').filter({ hasText: '分配用户' });
    await expect(drawer).toBeVisible();
    await page.getByPlaceholder('输入 NexusAuth subject').fill('e2e-user');
    await drawer.getByRole('button', { name: '查询', exact: true }).click();

    const assignmentCheckbox = drawer.locator('.t-checkbox').filter({ hasText: '分配当前角色' });
    await expect(assignmentCheckbox).toBeVisible();
    await expect(assignmentCheckbox.locator('.t-checkbox__input')).not.toBeChecked();
    const replaceRequest = page.waitForRequest((request) => (
      request.method() === 'PUT'
      && request.url().endsWith(`/api/v1/users/e2e-user/roles?service_resource=${FIRST_SERVICE_RESOURCE.key}`)
    ));
    await assignmentCheckbox.locator('.t-checkbox__input').click();
    await drawer.getByRole('button', { name: '保存分配', exact: true }).click();
    const request = await replaceRequest;

    expect(request.postDataJSON()).toEqual({ role_ids: ['role-editor'] });
    await expect.poll(
      () => state.userRoleReads,
      { message: `用户角色请求：${state.userRoleRequests.join(', ')}` },
    ).toBeGreaterThan(1);
    expect(state.userRoleIDs).toEqual(['role-editor']);
    await expect(drawer).toBeHidden();
  });

  test('角色页内分配用户会保留用户已有的其他角色', async ({ page }) => {
    const state = await installAPIMocks(page);
    state.userRoleIDs = ['role-reviewer'];
    await seedServiceResource(page);
    await page.goto('/roles');

    const roleRow = page.getByRole('row').filter({ hasText: '内容编辑' }).first();
    await roleRow.getByRole('button', { name: '分配用户', exact: true }).click();
    const drawer = page.locator('.t-drawer').filter({ hasText: '分配用户' });
    await expect(drawer).toBeVisible();
    await drawer.getByPlaceholder('输入 NexusAuth subject').fill('e2e-user');
    await drawer.getByRole('button', { name: '查询', exact: true }).click();

    const assignmentCheckbox = drawer.locator('.t-checkbox').filter({ hasText: '分配当前角色' });
    await expect(assignmentCheckbox).toBeVisible();
    await expect(assignmentCheckbox.locator('.t-checkbox__input')).not.toBeChecked();
    const replaceRequest = page.waitForRequest((request) => (
      request.method() === 'PUT'
      && request.url().endsWith(`/api/v1/users/e2e-user/roles?service_resource=${FIRST_SERVICE_RESOURCE.key}`)
    ));
    await assignmentCheckbox.locator('.t-checkbox__input').click();
    await drawer.getByRole('button', { name: '保存分配', exact: true }).click();
    const request = await replaceRequest;

    expect(request.postDataJSON()).toEqual({ role_ids: ['role-reviewer', 'role-editor'] });
    await expect.poll(
      () => state.userRoleReads,
      { message: `用户角色请求：${state.userRoleRequests.join(', ')}` },
    ).toBeGreaterThan(1);
    expect(state.userRoleIDs).toEqual(['role-reviewer', 'role-editor']);
    await expect(drawer).toBeHidden();
  });

  test('本地服务资源支持新增、编辑和删除', async ({ page }) => {
    const state = await installAPIMocks(page);
    await seedServiceResource(page);
    await page.goto('/service-resources');

    await expect(page.getByRole('heading', { name: '服务资源管理' })).toBeVisible();
    await expect(page.getByRole('columnheader', { name: '唯一 Key', exact: true })).toBeVisible();
    await page.getByRole('button', { name: '新建服务资源', exact: true }).click();
    const createDialog = page.locator('.t-dialog').filter({ hasText: '新建服务资源' });
    await expect(createDialog).toBeVisible();
    await createDialog.getByPlaceholder('例如 content-platform').fill('notification-platform');
    await createDialog.getByPlaceholder('例如 内容平台').fill('通知平台');
    await createDialog.getByPlaceholder('例如 permission.center.api').fill('permission.center.notification');
    await createDialog.getByPlaceholder('可选').fill('本地通知服务');

    const createRequest = page.waitForRequest((request) => (
      request.method() === 'POST' && request.url().endsWith('/api/v1/service-resources')
    ));
    await createDialog.getByRole('button', { name: '创建', exact: true }).click();
    await createRequest;

    const createdRow = page.getByRole('row').filter({ hasText: '通知平台' }).first();
    await expect(createdRow).toBeVisible();
    await expect(createdRow).toContainText('notification-platform');
    expect(state.serviceResourceMutationRequests.find((item) => item.method === 'POST')?.payload).toEqual({
      key: 'notification-platform',
      display_name: '通知平台',
      audience: 'permission.center.notification',
      description: '本地通知服务',
      is_active: true,
    });

    await createdRow.getByRole('button', { name: '编辑', exact: true }).click();
    const editDialog = page.locator('.t-dialog').filter({ hasText: '编辑服务资源' });
    await editDialog.getByPlaceholder('例如 内容平台').fill('通知中心');
    const updateRequest = page.waitForRequest((request) => (
      request.method() === 'PUT' && request.url().endsWith('/api/v1/service-resources/notification-platform')
    ));
    await editDialog.getByRole('button', { name: '保存', exact: true }).click();
    await updateRequest;
    await expect(page.getByText('通知中心', { exact: true })).toBeVisible();
    expect(state.serviceResourceMutationRequests.find((item) => item.method === 'PUT')?.payload).toMatchObject({
      display_name: '通知中心',
      audience: 'permission.center.notification',
      is_active: true,
    });

    const updatedRow = page.getByRole('row').filter({ hasText: '通知中心' }).first();
    await updatedRow.getByRole('button', { name: '删除', exact: true }).click();
    const deleteDialog = page.locator('.t-dialog').filter({ hasText: '删除服务资源' });
    const deleteRequest = page.waitForRequest((request) => (
      request.method() === 'DELETE' && request.url().endsWith('/api/v1/service-resources/notification-platform')
    ));
    await deleteDialog.getByRole('button', { name: '确认删除', exact: true }).click();
    await deleteRequest;
    await expect(page.getByText('通知中心', { exact: true })).toHaveCount(0);
    expect(state.serviceResourceMutationRequests.some((item) => item.method === 'DELETE')).toBe(true);
  });

  test('NexusAuth 服务资源条目仅供查看', async ({ page }) => {
    const state = await installAPIMocks(page);
    state.serviceResourcesWritable = false;
    state.serviceResources = [{ ...SECOND_SERVICE_RESOURCE }];
    await seedServiceResource(page);
    await page.goto('/service-resources');

    await expect(page.getByText('维护模式：只读', { exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: '新建服务资源', exact: true })).toHaveCount(0);
    const nexusRow = page.getByRole('row').filter({ hasText: SECOND_SERVICE_RESOURCE.display_name }).first();
    await expect(nexusRow).toContainText(SECOND_SERVICE_RESOURCE.key);
    await expect(nexusRow).toContainText('NexusAuth');
    await expect(nexusRow).toContainText('只读');
    await expect(nexusRow.getByRole('button', { name: '编辑', exact: true })).toHaveCount(0);
    await expect(nexusRow.getByRole('button', { name: '删除', exact: true })).toHaveCount(0);
  });

  test('Swagger 导入使用当前已选服务资源', async ({ page }) => {
    const state = await installAPIMocks(page);
    await page.goto('/select-service-resource');
    await page.locator('.service-resource-card').filter({ hasText: SECOND_SERVICE_RESOURCE.display_name }).click();
    await page.getByRole('button', { name: '继续并进入控制台', exact: true }).click();
    await page.goto('/api-endpoints');

    await expect(page.getByRole('heading', { name: 'API 端点管理' })).toBeVisible();
    await page.getByRole('button', { name: '导入 Swagger', exact: true }).click();
    const dialog = page.locator('.t-dialog').filter({ hasText: '导入 Swagger API 端点' });
    await expect(dialog).toContainText(SECOND_SERVICE_RESOURCE.key);
    await expect(dialog.getByText('服务资源', { exact: true })).toBeVisible();
    await dialog.getByPlaceholder('例如 https://example.com/swagger/openapi.json').fill('https://example.com/openapi.json');

    const importRequest = page.waitForRequest((request) => (
      request.method() === 'POST'
      && request.url().endsWith('/api/v1/authorization/api-endpoints/import-swagger')
    ));
    await dialog.getByRole('button', { name: '开始导入', exact: true }).click();
    await importRequest;

    expect(state.swaggerImportRequests).toHaveLength(1);
    expect(state.swaggerImportRequests[0]?.payload).toEqual({
      service_resource: SECOND_SERVICE_RESOURCE.key,
      swagger_url: 'https://example.com/openapi.json',
    });
    await expect(page.getByText('SwaggerController', { exact: true })).toBeVisible();
  });

  test('API 端点支持手工新增、编辑和删除', async ({ page }) => {
    const state = await installAPIMocks(page);
    await seedServiceResource(page);
    await page.goto('/api-endpoints');

    await page.getByRole('button', { name: '新建端点', exact: true }).click();
    const createDialog = page.locator('.t-dialog').filter({ hasText: '新建 API 端点' });
    await createDialog.getByPlaceholder('例如 UserController').fill('OrderController');
    await createDialog.getByPlaceholder('例如 /v1/users/{id}').fill('/v1/orders/{id}');
    await createDialog.getByPlaceholder('例如 查询用户详情').fill('查询订单详情');

    const createRequest = page.waitForRequest((request) => (
      request.method() === 'POST' && request.url().endsWith('/api/v1/authorization/api-endpoints')
    ));
    await createDialog.getByRole('button', { name: '创建', exact: true }).click();
    await createRequest;
    const createdRow = page.getByRole('row').filter({ hasText: '查询订单详情' }).first();
    await expect(createdRow).toBeVisible();
    expect(state.endpointRequests.find((item) => item.method === 'POST')?.payload).toMatchObject({
      service_resource: FIRST_SERVICE_RESOURCE.key,
      controller: 'OrderController',
      method: 'GET',
      path_template: '/v1/orders/{id}',
      summary: '查询订单详情',
      enabled: true,
    });

    await createdRow.getByRole('button', { name: '编辑', exact: true }).click();
    const editDialog = page.locator('.t-dialog').filter({ hasText: '编辑 API 端点' });
    await editDialog.getByPlaceholder('例如 UserController').fill('OrderControllerV2');
    await editDialog.getByPlaceholder('例如 查询用户详情').fill('更新订单详情');
    const updateRequest = page.waitForRequest((request) => (
      request.method() === 'PUT' && request.url().endsWith('/api/v1/authorization/api-endpoints/endpoint-created-1')
    ));
    await editDialog.getByRole('button', { name: '保存', exact: true }).click();
    await updateRequest;
    await expect(page.getByText('更新订单详情', { exact: true })).toBeVisible();
    expect(state.endpointRequests.find((item) => item.method === 'PUT')?.payload).toMatchObject({
      controller: 'OrderControllerV2',
      method: 'GET',
      path_template: '/v1/orders/{id}',
      summary: '更新订单详情',
      enabled: true,
    });

    const updatedRow = page.getByRole('row').filter({ hasText: '更新订单详情' }).first();
    await updatedRow.getByRole('button', { name: '删除', exact: true }).click();
    const deleteDialog = page.locator('.t-dialog').filter({ hasText: '删除 API 端点' });
    const deleteRequest = page.waitForRequest((request) => (
      request.method() === 'DELETE' && request.url().endsWith('/api/v1/authorization/api-endpoints/endpoint-created-1')
    ));
    await deleteDialog.getByRole('button', { name: '确认删除', exact: true }).click();
    await deleteRequest;
    await expect(page.getByText('更新订单详情', { exact: true })).toHaveCount(0);
    expect(state.endpointRequests.some((item) => item.method === 'DELETE')).toBe(true);
  });

  test('PDP 策略将角色和 API 端点绑定，并支持发布与决策模拟', async ({ page }) => {
    const state = await installAPIMocks(page);
    await seedServiceResource(page);
    await page.goto('/policies');

    await expect(page.getByRole('heading', { name: 'PDP 授权策略' })).toBeVisible();
    await page.getByRole('button', { name: '新建策略', exact: true }).click();
    const drawer = page.locator('.t-drawer').filter({ hasText: '新建策略草稿' });
    await drawer.getByPlaceholder('例如 post-read').fill('post-read');
    await drawer.getByPlaceholder('例如 阅读帖子').fill('阅读帖子');
    await drawer.locator('.t-form__item').filter({ hasText: '角色' }).locator('.t-select').click();
    await page.locator('.t-select-option').filter({ hasText: '内容编辑' }).click();
    await drawer.locator('.t-form__item').filter({ hasText: 'API 端点' }).locator('.t-select').click();
    const enabledEndpointOption = page.locator('.t-select-option').filter({ hasText: 'GET /v1/posts' });
    const disabledEndpointOption = page.locator('.t-select-option').filter({ hasText: 'POST /v1/posts/import（停用）' });
    await expect(disabledEndpointOption).toBeVisible();
    await enabledEndpointOption.click();
    await disabledEndpointOption.click();
    await expect(enabledEndpointOption).toHaveClass(/t-is-selected/);
    await expect(disabledEndpointOption).toHaveClass(/t-is-selected/);
    await enabledEndpointOption.click();
    await expect(enabledEndpointOption).not.toHaveClass(/t-is-selected/);
    await drawer.getByText('Subject 属性路径', { exact: true }).click();
    await expect(disabledEndpointOption).not.toBeVisible();
    await drawer.locator('.t-form__item').filter({ hasText: 'API 端点' }).locator('.t-select').click();
    await expect(enabledEndpointOption).not.toHaveClass(/t-is-selected/);
    await expect(disabledEndpointOption).toHaveClass(/t-is-selected/);
    await drawer.getByRole('button', { name: '创建', exact: true }).click();
    expect(state.policiesByServiceResource[FIRST_SERVICE_RESOURCE.key][0]?.endpoint_ids).toEqual(['endpoint-post-import']);
    const row = page.getByRole('row').filter({ hasText: '阅读帖子' });
    await expect(row).toContainText('draft');
    await row.getByRole('button', { name: '发布', exact: true }).click();
    await expect(row).toContainText('published');

    await page.getByPlaceholder('NexusAuth subject').fill('e2e-user');
    await page.getByPlaceholder('tenant_id').fill('tenant-a');
    await page.locator('.permission-policy-simulator .t-select').nth(1).click();
    await page.locator('.t-select-option').filter({ hasText: 'GET /v1/posts' }).last().click();
    await page.getByRole('button', { name: '执行决策', exact: true }).click();
    await expect(page.getByText('ALLOW', { exact: true })).toBeVisible();
  });

  test('PDP 草稿编辑抽屉回填现值并保存到服务端', async ({ page }) => {
    const state = await installAPIMocks(page);
    state.policiesByServiceResource[FIRST_SERVICE_RESOURCE.key] = [{
      id: 'policy-draft-edit',
      service_resource: FIRST_SERVICE_RESOURCE.key,
      code: 'department-read',
      name: '部门读取策略',
      description: '仅限工程部门',
      effect: 'deny',
      status: 'draft',
      authorization_type: 'data',
      priority: 42,
      condition: {
        comparison: {
          left: { source: 'subject', path: 'department', type: 'string' },
          op: 'neq',
          right: { source: 'literal', type: 'string', value: 'engineering' },
        },
      },
      current_version: 0,
      role_ids: ['role-reviewer'],
      endpoint_ids: ['endpoint-post-import'],
    }];
    await seedServiceResource(page);
    await page.goto('/policies');

    const row = page.getByRole('row').filter({ hasText: 'department-read' });
    await expect(row).toContainText('数据级');
    await row.getByRole('button', { name: '编辑', exact: true }).click();
    const drawer = page.locator('.t-drawer').filter({ hasText: '编辑策略草稿' });
    await expect(drawer.getByPlaceholder('例如 post-read')).toHaveValue('department-read');
    await expect(drawer.getByPlaceholder('例如 阅读帖子')).toHaveValue('部门读取策略');
    await expect(drawer.getByRole('spinbutton')).toHaveValue('42');
    await expect(drawer.locator('.t-form-item__authorizationType').getByRole('textbox')).toHaveValue('业务数据级');
    await expect(drawer.getByText('内容审核 (content-reviewer)', { exact: true })).toBeVisible();
    await expect(drawer.getByText('POST /v1/posts/import（停用）', { exact: true })).toBeVisible();
    await drawer.locator('.t-form__item').filter({ hasText: '角色' }).locator('.t-select').click();
    const selectedRoleOption = page.locator('.t-select-option').filter({ hasText: '内容审核 (content-reviewer)' });
    await expect(selectedRoleOption.locator('.t-checkbox')).toHaveClass(/t-is-checked/);
    await drawer.locator('.t-form__item').filter({ hasText: '角色' }).locator('.t-select').click();
    await drawer.locator('.t-form__item').filter({ hasText: 'API 端点' }).locator('.t-select').click();
    const selectedEndpointOption = page.locator('.t-select-option').filter({ hasText: 'POST /v1/posts/import（停用）' });
    await expect(selectedEndpointOption.locator('.t-checkbox')).toHaveClass(/t-is-checked/);
    await selectedEndpointOption.click();
    await expect(selectedEndpointOption.locator('.t-checkbox')).not.toHaveClass(/t-is-checked/);
    await selectedEndpointOption.click();
    await expect(selectedEndpointOption.locator('.t-checkbox')).toHaveClass(/t-is-checked/);
    await drawer.getByText('Subject 属性路径', { exact: true }).click();
    await expect(drawer.getByPlaceholder('可选，例如 department')).toHaveValue('department');
    await expect(drawer.getByPlaceholder('字符串值')).toHaveValue('engineering');
    await expect(drawer.locator('textarea')).toHaveValue('仅限工程部门');

    await drawer.getByPlaceholder('例如 阅读帖子').fill('部门读取策略（已编辑）');
    await drawer.locator('textarea').fill('保存后的策略说明');
    await drawer.getByRole('button', { name: '保存', exact: true }).click();

    await expect(page.getByRole('row').filter({ hasText: '部门读取策略（已编辑）' })).toBeVisible();
    await expect.poll(() => state.policiesByServiceResource[FIRST_SERVICE_RESOURCE.key][0]?.name).toBe('部门读取策略（已编辑）');
    expect(state.policiesByServiceResource[FIRST_SERVICE_RESOURCE.key][0]).toMatchObject({
      description: '保存后的策略说明',
      authorization_type: 'data',
      role_ids: ['role-reviewer'],
      endpoint_ids: ['endpoint-post-import'],
    });

    await page.reload();
    await expect(page.getByRole('row').filter({ hasText: '部门读取策略（已编辑）' })).toBeVisible();
  });

});
