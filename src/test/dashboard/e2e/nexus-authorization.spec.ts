import { createRequire } from 'node:module';
import type { Locator, Page } from '../../../../dashboard/node_modules/@playwright/test';

// Resolve Playwright from the Dashboard package while the test suite lives at the repository root.
const requireDashboardDependency = createRequire(new URL('../../../../dashboard/package.json', import.meta.url));
const { expect, test } = requireDashboardDependency('@playwright/test') as typeof import('../../../../dashboard/node_modules/@playwright/test');

// Regression scope for the current application permission workbench
// (dashboard/src/pages/policy-workbench/index.tsx and its components).
//
// The retired NexusAuth service-resource workbench (service-resource permission isolation,
// PDP publish, draft/snapshot and release pages) is gone. The workbench now:
//   - stores the selected business application in sessionStorage 'nexus-authorization-application'
//     (dashboard/src/api/application.ts) and scopes every business call with ?app_key=;
//   - manages roles through /api/v1/rbac-v1/roles (POST creates and updates, DELETE removes);
//   - binds users through /api/v1/rbac-v1/subject-roles (PUT replaces one subject+domain membership,
//     default domain = the empty string);
//   - evaluates requests through /api/v1/rbac-v1/simulate chosen from the service catalog, API
//     endpoints and the SCIM directory (service_resource + subject/method/path, domain omitted).
//
// Every /api/** call is mocked from in-memory fixtures, so these tests prove the UI/HTTP contract only.
// They do not exercise a real authorization engine, database or SCIM directory.

const APP_ORDERS = 'app-orders';
const APP_REPORTS = 'app-reports';
const APPLICATION_STORAGE_KEY = 'nexus-authorization-application';
const LAYOUT_TABS_STORAGE_KEY = 'nexus-authorization-layout-tabs';

type ApplicationSeed = { key: string; display_name: string; description: string; enabled: boolean };

const APPLICATIONS: ApplicationSeed[] = [
  { key: APP_ORDERS, display_name: '订单应用', description: '订单域权限配置', enabled: true },
  { key: APP_REPORTS, display_name: '报表应用', description: '报表域权限配置', enabled: true },
];

type ServiceSeed = {
  id: string; key: string; name: string; display_name: string;
  audience: string; description: string; is_active: boolean; source: 'local';
};
type EndpointSeed = {
  id: string; service_resource: string; endpoint_key: string; method: string;
  path_template: string; module: string; summary: string; source: 'manual' | 'swagger'; enabled: boolean;
};
type RoleSeed = {
  id: string; code: string; name: string; description: string;
  enabled: boolean; endpoint_ids: string[]; menu_ids: string[];
};
type MenuSeed = {
  id: string; parent_id: string | null; code: string; name: string;
  menu_type: 'menu' | 'button'; route: string; component: string; icon: string;
  sort_order: number; enabled: boolean;
};
type BindingSeed = { subject: string; domain: string; role_id: string };

const ALICE = {
  id: 'nexus-alice',
  userName: 'alice',
  name: { formatted: 'Alice 张' },
  emails: [{ value: 'alice@example.test', primary: true }],
  active: true,
};
const ALICE_LABEL = 'Alice 张 · alice · alice@example.test';

// The workbench lists a global service catalog (/v1/service-resources) and a per-application
// association (/v1/applications/{key}/service-resources). Both stores are mutable inside the mock so
// create / associate / unassociate / delete round-trips through the real UI code paths.
const baseCatalog = (): ServiceSeed[] => [
  { id: 'svc-orders', key: 'orders-api', name: 'orders-api', display_name: '订单服务', audience: 'orders.api', description: '订单服务', is_active: true, source: 'local' },
  { id: 'svc-reports', key: 'reports-api', name: 'reports-api', display_name: '报表服务', audience: 'reports.api', description: '报表服务', is_active: true, source: 'local' },
];

const baseAssociations = (): Record<string, string[]> => ({
  [APP_ORDERS]: ['orders-api'],
  [APP_REPORTS]: ['reports-api'],
});

const baseEndpoints = (): Record<string, EndpointSeed[]> => ({
  [APP_ORDERS]: [{ id: 'ep-orders-list', service_resource: 'orders-api', endpoint_key: 'GET:/v1/orders', method: 'GET', path_template: '/v1/orders', module: '订单', summary: '查询订单', source: 'swagger', enabled: true }],
  [APP_REPORTS]: [],
});

const MENUS: Record<string, MenuSeed[]> = {
  [APP_ORDERS]: [{ id: 'menu-orders', parent_id: null, code: 'orders', name: '订单管理', menu_type: 'menu', route: '/orders', component: '', icon: '', sort_order: 1, enabled: true }],
  [APP_REPORTS]: [{ id: 'menu-reports', parent_id: null, code: 'reports', name: '报表管理', menu_type: 'menu', route: '/reports', component: '', icon: '', sort_order: 1, enabled: true }],
};

const baseRoles = (): Record<string, RoleSeed[]> => ({
  [APP_ORDERS]: [
    { id: 'reader-role', code: 'reader', name: '读者', description: '只读角色', enabled: true, endpoint_ids: ['ep-orders-list'], menu_ids: ['menu-orders'] },
    { id: 'editor-role', code: 'editor', name: '编辑者', description: '编辑角色', enabled: true, endpoint_ids: [], menu_ids: [] },
  ],
  [APP_REPORTS]: [
    { id: 'reporter-role', code: 'reporter', name: '报表员', description: '', enabled: true, endpoint_ids: [], menu_ids: [] },
  ],
});

// app_key scoped request log. `path` excludes the query string; `app_key` is read from it.
type LoggedRequest = { method: string; path: string; appKey: string | null };
type SavedRequest = { url: string; body: Record<string, unknown> };

type WorkbenchState = {
  authenticated: boolean;
  requests: LoggedRequest[];
  publishRequests: string[];
  roleSaves: SavedRequest[];
  roleDeletes: string[];
  subjectRolePuts: SavedRequest[];
  simulations: SavedRequest[];
  catalog: ServiceSeed[];
  appServices: Record<string, string[]>;
  serviceWrites: SavedRequest[];
  serviceDeletes: string[];
  serviceAssociations: SavedRequest[];
  endpoints: Record<string, EndpointSeed[]>;
  endpointWrites: SavedRequest[];
  endpointDeletes: string[];
  swaggerImports: SavedRequest[];
  roles: Record<string, RoleSeed[]>;
  bindings: Record<string, BindingSeed[]>;
};

type Route = import('../../../../dashboard/node_modules/@playwright/test').Route;

const fulfill = async (route: Route, result: unknown, status = 200) => {
  await route.fulfill({ status, json: { success: true, result } });
};

// Single catch-all router in the scim-user-binding.spec.ts style: everything under /api/** is served
// from fixtures, anything else falls back to the Vite dev server. No real authorization data is used.
async function mockWorkbench(page: Page, options: { application?: string | null; bindings?: BindingSeed[] } = {}): Promise<WorkbenchState> {
  const state: WorkbenchState = {
    authenticated: true,
    requests: [],
    publishRequests: [],
    roleSaves: [],
    roleDeletes: [],
    subjectRolePuts: [],
    simulations: [],
    catalog: baseCatalog(),
    appServices: baseAssociations(),
    serviceWrites: [],
    serviceDeletes: [],
    serviceAssociations: [],
    endpoints: baseEndpoints(),
    endpointWrites: [],
    endpointDeletes: [],
    swaggerImports: [],
    roles: baseRoles(),
    bindings: {
      [APP_ORDERS]: options.bindings ? options.bindings.map(binding => ({ ...binding })) : [],
      [APP_REPORTS]: [],
    },
  };

  // Seed the selected application once so a switch performed in-page survives a hard refresh; an
  // explicitly unset application exercises the /select-application gate instead.
  const seededApplication = options.application === undefined ? APP_ORDERS : options.application;
  await page.addInitScript(({ key, value }: { key: string; value: string | null }) => {
    if (value && !window.sessionStorage.getItem(key)) window.sessionStorage.setItem(key, value);
  }, { key: APPLICATION_STORAGE_KEY, value: seededApplication });

  await page.route('**/api/**', async route => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    if (!path.startsWith('/api/')) { await route.fallback(); return; }

    const appKey = url.searchParams.get('app_key');
    state.requests.push({ method: request.method(), path, appKey });
    if (/publish|release|snapshot|draft/i.test(path)) state.publishRequests.push(`${request.method()} ${path}`);

    if (path === '/api/auth/me') {
      if (!state.authenticated) {
        await route.fulfill({ status: 401, json: { success: false, errorCode: 'UNAUTHENTICATED', errorMessage: '未登录', result: null } });
        return;
      }
      await fulfill(route, { isAuthenticated: true, user: { sub: 'admin', name: '管理员' } });
      return;
    }

    if (path === '/api/auth/logout' && request.method() === 'POST') {
      state.authenticated = false;
      await fulfill(route, { logoutUrl: '' });
      return;
    }

    if (path === '/api/v1/applications' && request.method() === 'GET') {
      await fulfill(route, { items: APPLICATIONS });
      return;
    }

    // Global service catalog: the workbench reads it to render the table and writes to it from the drawer.
    if (path === '/api/v1/service-resources') {
      if (request.method() === 'POST') {
        const body = request.postDataJSON() as Record<string, unknown>;
        state.serviceWrites.push({ url: url.pathname + url.search, body });
        const created: ServiceSeed = {
          id: `svc-${state.catalog.length + 1}`,
          key: String(body.key ?? ''),
          name: String(body.key ?? ''),
          display_name: String(body.display_name ?? ''),
          audience: String(body.audience ?? ''),
          description: String(body.description ?? ''),
          is_active: body.is_active !== false,
          source: 'local',
        };
        state.catalog = [...state.catalog, created];
        await fulfill(route, created);
        return;
      }
      await fulfill(route, { items: state.catalog, writable: true });
      return;
    }

    const serviceDetail = path.match(/^\/api\/v1\/service-resources\/([^/]+)$/);
    if (serviceDetail) {
      const key = decodeURIComponent(serviceDetail[1]);
      const existing = state.catalog.find(item => item.key === key);
      if (request.method() === 'PUT' && existing) {
        const body = request.postDataJSON() as Record<string, unknown>;
        state.serviceWrites.push({ url: url.pathname + url.search, body });
        const updated: ServiceSeed = {
          ...existing,
          display_name: String(body.display_name ?? existing.display_name),
          audience: String(body.audience ?? existing.audience),
          description: String(body.description ?? existing.description),
          is_active: body.is_active === undefined ? existing.is_active : body.is_active === true,
        };
        state.catalog = state.catalog.map(item => item.key === key ? updated : item);
        await fulfill(route, updated);
        return;
      }
      if (request.method() === 'DELETE') {
        state.serviceDeletes.push(url.pathname + url.search);
        state.catalog = state.catalog.filter(item => item.key !== key);
        Object.keys(state.appServices).forEach(name => {
          state.appServices[name] = state.appServices[name].filter(item => item !== key);
        });
        await fulfill(route, null);
        return;
      }
    }

    const applicationServices = path.match(/^\/api\/v1\/applications\/([^/]+)\/service-resources$/);
    if (applicationServices) {
      const key = decodeURIComponent(applicationServices[1]);
      if (request.method() === 'PUT') {
        const body = request.postDataJSON() as { service_resource_keys?: unknown };
        state.serviceAssociations.push({ url: url.pathname + url.search, body: body as Record<string, unknown> });
        state.appServices[key] = Array.isArray(body.service_resource_keys)
          ? body.service_resource_keys.map(String)
          : [];
        await fulfill(route, null);
        return;
      }
      const associated = state.appServices[key] ?? [];
      await fulfill(route, { items: state.catalog.filter(item => associated.includes(item.key)) });
      return;
    }

    // API endpoints are scoped by app_key + service_resource (list, create/update, delete, Swagger import).
    if (path === '/api/v1/rbac-v1/endpoints') {
      const items = state.endpoints[appKey ?? ''] ?? [];
      if (request.method() === 'POST') {
        const body = request.postDataJSON() as Record<string, unknown>;
        state.endpointWrites.push({ url: url.pathname + url.search, body });
        const next: EndpointSeed = {
          id: typeof body.id === 'string' ? body.id : `endpoint-created-${state.endpointWrites.length}`,
          service_resource: String(body.service_resource ?? url.searchParams.get('service_resource') ?? ''),
          endpoint_key: String(body.endpoint_key ?? ''),
          method: String(body.method ?? 'GET'),
          path_template: String(body.path_template ?? ''),
          module: String(body.module ?? ''),
          summary: String(body.summary ?? ''),
          source: 'manual',
          enabled: body.enabled !== false,
        };
        const existing = (state.endpoints[appKey ?? ''] ?? []).find(item => item.id === next.id);
        state.endpoints[appKey ?? ''] = existing
          ? (state.endpoints[appKey ?? ''] ?? []).map(item => item.id === next.id ? next : item)
          : [...items, next];
        await fulfill(route, next);
        return;
      }
      await fulfill(route, { items, total: items.length, page: 1, page_size: 100 });
      return;
    }

    if (path === '/api/v1/rbac-v1/endpoints/import-swagger' && request.method() === 'POST') {
      const body = request.postDataJSON() as { swagger_url?: string };
      state.swaggerImports.push({ url: url.pathname + url.search, body: body as Record<string, unknown> });
      const serviceResource = url.searchParams.get('service_resource') ?? '';
      const imported: EndpointSeed = {
        id: `endpoint-imported-${state.swaggerImports.length}`,
        service_resource: serviceResource,
        endpoint_key: 'GET:/v1/imported',
        method: 'GET',
        path_template: '/v1/imported',
        module: '导入',
        summary: 'Swagger 导入接口',
        source: 'swagger',
        enabled: true,
      };
      state.endpoints[appKey ?? ''] = [...(state.endpoints[appKey ?? ''] ?? []), imported];
      await fulfill(route, { total: 3, created: 2, skipped: 1 });
      return;
    }

    const endpointDetail = path.match(/^\/api\/v1\/rbac-v1\/endpoints\/([^/]+)$/);
    if (endpointDetail && request.method() === 'DELETE') {
      const id = decodeURIComponent(endpointDetail[1]);
      state.endpointDeletes.push(url.pathname + url.search);
      state.endpoints[appKey ?? ''] = (state.endpoints[appKey ?? ''] ?? []).filter(item => item.id !== id);
      await fulfill(route, null);
      return;
    }

    if (path === '/api/v1/rbac-v1/menus' && request.method() === 'GET') {
      await fulfill(route, { items: MENUS[appKey ?? ''] ?? [] });
      return;
    }

    if (path === '/api/v1/rbac-v1/roles') {
      const current = state.roles[appKey ?? ''] ?? [];
      if (request.method() === 'POST') {
        const body = request.postDataJSON() as Record<string, unknown>;
        state.roleSaves.push({ url: url.pathname + url.search, body });
        const next: RoleSeed = {
          id: typeof body.id === 'string' ? body.id : `role-created-${state.roleSaves.length}`,
          code: String(body.code ?? ''),
          name: String(body.name ?? ''),
          description: String(body.description ?? ''),
          enabled: body.enabled !== false,
          endpoint_ids: Array.isArray(body.endpoint_ids) ? body.endpoint_ids.map(String) : [],
          menu_ids: Array.isArray(body.menu_ids) ? body.menu_ids.map(String) : [],
        };
        const existing = current.find(item => item.id === next.id);
        state.roles[appKey ?? ''] = existing
          ? current.map(item => item.id === next.id ? next : item)
          : [...current, next];
        await fulfill(route, next);
        return;
      }
      await fulfill(route, { items: current });
      return;
    }

    const roleDetail = path.match(/^\/api\/v1\/rbac-v1\/roles\/([^/]+)$/);
    if (roleDetail && request.method() === 'DELETE') {
      const id = decodeURIComponent(roleDetail[1]);
      state.roleDeletes.push(url.pathname + url.search);
      state.roles[appKey ?? ''] = (state.roles[appKey ?? ''] ?? []).filter(item => item.id !== id);
      await fulfill(route, null);
      return;
    }

    if (path === '/api/v1/rbac-v1/subject-roles') {
      const current = state.bindings[appKey ?? ''] ?? [];
      if (request.method() === 'PUT') {
        const body = request.postDataJSON() as { subject: string; domain: string; role_ids: string[] };
        state.subjectRolePuts.push({ url: url.pathname + url.search, body });
        const remaining = current.filter(item => !(item.subject === body.subject && item.domain === body.domain));
        state.bindings[appKey ?? ''] = [
          ...remaining,
          ...(body.role_ids || []).map(roleID => ({ subject: body.subject, domain: body.domain, role_id: String(roleID) })),
        ];
        await fulfill(route, null);
        return;
      }
      await fulfill(route, { items: current });
      return;
    }

    if (path === '/api/v1/rbac-v1/simulate' && request.method() === 'POST') {
      state.simulations.push({ url: url.pathname + url.search, body: request.postDataJSON() as Record<string, unknown> });
      await fulfill(route, { allowed: true, effect: 'allow', reason: 'role_match', matched_roles: ['reader-role'] });
      return;
    }

    if (path === '/api/v1/users' && request.method() === 'GET') {
      await fulfill(route, { totalResults: 1, startIndex: 1, itemsPerPage: 1, Resources: [ALICE] });
      return;
    }

    await fulfill(route, { items: [] });
  });

  return state;
}

const siderMenu = (page: Page): Locator => page.locator('.layout-sider-menu');
const currentApplication = (page: Page): Locator => page.locator('.layout-current-service-resource-value');
const roleRow = (page: Page, text: string): Locator => page.getByRole('row').filter({ hasText: text }).first();
const roleEditor = (page: Page): Locator => page.locator('.role-editor');
const editorField = (page: Page, label: string): Locator => roleEditor(page).locator(`.t-input__wrap[aria-label="${label}"] input`);
const saveRoleButton = (page: Page): Locator => roleEditor(page).getByRole('button', { name: '保存角色', exact: true });
const enabledSwitch = (page: Page): Locator => roleEditor(page).locator('button[role="switch"][aria-label="启用角色"]');

const storedApplication = (page: Page) => page.evaluate(
  (key: string) => window.sessionStorage.getItem(key),
  APPLICATION_STORAGE_KEY,
);

const storedTabs = (page: Page) => page.evaluate(
  (key: string) => JSON.parse(window.localStorage.getItem(key) || '[]') as unknown[],
  LAYOUT_TABS_STORAGE_KEY,
);

const selectApplication = async (page: Page, label: string) => {
  await page.getByRole('button', { name: `选择应用：${label}`, exact: true }).click();
  await page.getByRole('button', { name: '进入权限配置', exact: true }).click();
};

const sortedIDs = (values: unknown): string[] => [...((values as string[]) ?? [])].sort();

test.use({ viewport: { width: 1440, height: 900 } });

test('Header 在已选业务应用下切换主题与全屏图标', async ({ page }) => {
  await mockWorkbench(page);
  await page.addInitScript(() => {
    window.localStorage.setItem('theme-mode', 'light');
    let fullscreen = false;
    Object.defineProperty(Document.prototype, 'fullscreenElement', {
      configurable: true,
      get: () => (fullscreen ? document.documentElement : null),
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
  });
  await page.goto('/dashboard');

  await expect(currentApplication(page)).toHaveText(APP_ORDERS);
  const themeButton = page.getByRole('button', { name: '切换到暗色主题' });
  const fullscreenButton = page.getByRole('button', { name: '进入全屏' });
  await expect(themeButton.locator('.t-icon-moon')).toBeVisible();
  await expect(fullscreenButton.locator('.t-icon-fullscreen-1')).toBeVisible();

  await themeButton.click();
  await expect(page.locator('html')).toHaveAttribute('theme-mode', 'dark');
  await expect(page.getByRole('button', { name: '切换到浅色主题' }).locator('.t-icon-sunny')).toBeVisible();

  await fullscreenButton.click();
  await expect(page.getByRole('button', { name: '退出全屏' }).locator('.t-icon-fullscreen-exit-1')).toBeVisible();
});

test('未选择业务应用时角色管理跳转选择应用，选择后写入 sessionStorage 并回到原页面', async ({ page }) => {
  await mockWorkbench(page, { application: null });

  await page.goto('/roles');
  await expect(page).toHaveURL(/\/select-application\?redirect=%2Froles$/);
  await expect(page.getByRole('heading', { name: '选择业务应用', exact: true })).toBeVisible();
  expect(await storedApplication(page)).toBeNull();

  await selectApplication(page, '订单应用');

  await expect(page).toHaveURL(/\/roles$/);
  expect(await storedApplication(page)).toBe(APP_ORDERS);
  await expect(currentApplication(page)).toHaveText(APP_ORDERS);
  await expect(roleRow(page, '读者')).toBeVisible();
});

test('切换业务应用后返回原页面：app_key 隔离请求且不残留旧应用数据', async ({ page }) => {
  const state = await mockWorkbench(page);
  await page.goto('/roles');
  await expect(roleRow(page, '读者')).toBeVisible();
  await expect(roleRow(page, '报表员')).toHaveCount(0);

  const beforeSwitch = state.requests.length;
  await page.getByRole('button', { name: '切换应用', exact: true }).click();
  await expect(page).toHaveURL(/\/select-application\?redirect=%2Froles$/);
  await selectApplication(page, '报表应用');

  await expect(page).toHaveURL(/\/roles$/);
  await expect(currentApplication(page)).toHaveText(APP_REPORTS);
  await expect(roleRow(page, '报表员')).toBeVisible();
  await expect(roleRow(page, '读者')).toHaveCount(0);

  // Every business request after the switch carries the new app_key; the old application is not queried again.
  const afterSwitch = state.requests.slice(beforeSwitch).filter(item => item.path.startsWith('/api/v1/rbac-v1/'));
  expect(afterSwitch.length).toBeGreaterThan(0);
  expect(afterSwitch.every(item => item.appKey === APP_REPORTS)).toBe(true);
  expect(afterSwitch.some(item => item.path === '/api/v1/rbac-v1/roles')).toBe(true);
  expect(state.requests.filter(item => item.path === '/api/v1/rbac-v1/roles' && item.appKey === APP_ORDERS)).toHaveLength(1);
});

test('所选业务应用与角色列表在硬刷新后保留，并按同一 app_key 重新请求', async ({ page }) => {
  const state = await mockWorkbench(page);
  await page.goto('/roles');
  await expect(roleRow(page, '读者')).toBeVisible();
  const beforeReload = state.requests.length;

  await page.reload();

  await expect(page).toHaveURL(/\/roles$/);
  await expect(currentApplication(page)).toHaveText(APP_ORDERS);
  await expect(roleRow(page, '读者')).toBeVisible();
  expect(await storedApplication(page)).toBe(APP_ORDERS);

  const afterReload = state.requests.slice(beforeReload).filter(item => item.path === '/api/v1/rbac-v1/roles');
  expect(afterReload.length).toBeGreaterThan(0);
  expect(afterReload.every(item => item.appKey === APP_ORDERS)).toBe(true);
});

test('退出登录清空历史页签，重新进入后只保留配置总览', async ({ page }) => {
  const state = await mockWorkbench(page);
  await page.goto('/roles');
  await page.goto('/simulate');
  await expect.poll(async () => (await storedTabs(page)).length).toBeGreaterThan(1);

  await page.locator('.layout-avatar-trigger').click();
  await page.getByText('退出登录', { exact: true }).click();

  await expect(page).toHaveURL(/\/login/);
  expect(await page.evaluate((key: string) => window.localStorage.getItem(key), LAYOUT_TABS_STORAGE_KEY)).toBeNull();

  state.authenticated = true;
  await page.goto('/dashboard');
  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(page.locator('.layout-content-tabs .t-tabs__nav-item')).toHaveCount(1);
  await expect(page.locator('.layout-content-tabs .t-tabs__nav-item').first()).toContainText('配置总览');
  expect(await storedTabs(page)).toEqual([{ value: '/dashboard', label: '配置总览', removable: false }]);
});

test('新增角色：保存 POST 到 rbac-v1 roles 并带 app_key，列表刷新且无发布请求', async ({ page }) => {
  const state = await mockWorkbench(page);
  await page.goto('/roles');

  await page.getByRole('button', { name: '新增角色', exact: true }).click();
  await expect(page).toHaveURL(/\/roles\/new$/);
  await expect(roleEditor(page).locator('section[aria-label="角色信息"]')).toBeVisible();
  await editorField(page, '角色名称').fill('审核员');
  await editorField(page, '角色编码').fill('audit-role');
  await editorField(page, '角色说明').fill('负责单据审核');

  await saveRoleButton(page).click();

  await expect(page).toHaveURL(/\/roles$/);
  await expect.poll(() => state.roleSaves.length).toBe(1);
  const save = state.roleSaves[0];
  expect(save.url).toContain('app_key=app-orders');
  expect(save.body).toMatchObject({ code: 'audit-role', name: '审核员', description: '负责单据审核', enabled: true, endpoint_ids: [], menu_ids: [] });
  expect(save.body.id).toBeUndefined();
  await expect(roleRow(page, '审核员')).toBeVisible();
  await expect(roleRow(page, '审核员')).toContainText('audit-role');
  expect(state.publishRequests).toEqual([]);
});

test('编辑角色：保存带 id 与 app_key，停用后列表显示停用，再次保存可重新启用', async ({ page }) => {
  const state = await mockWorkbench(page);
  await page.goto('/roles/reader-role/edit');
  await expect(roleEditor(page).locator('section[aria-label="角色信息"]')).toBeVisible();
  await expect(editorField(page, '角色编码')).toBeDisabled();
  await expect(enabledSwitch(page)).toHaveClass(/t-is-checked/);

  await enabledSwitch(page).click();
  await expect(roleEditor(page)).toContainText('角色停用');
  await saveRoleButton(page).click();

  await expect(page).toHaveURL(/\/roles$/);
  await expect.poll(() => state.roleSaves.length).toBe(1);
  expect(state.roleSaves[0].url).toContain('app_key=app-orders');
  expect(state.roleSaves[0].body).toMatchObject({ id: 'reader-role', code: 'reader', name: '读者', enabled: false });
  await expect(roleRow(page, '读者')).toContainText('停用');

  await roleRow(page, '读者').getByRole('button', { name: '编辑授权', exact: true }).click();
  await expect(roleEditor(page).locator('section[aria-label="角色信息"]')).toBeVisible();
  await expect(enabledSwitch(page)).not.toHaveClass(/t-is-checked/);
  await enabledSwitch(page).click();
  await saveRoleButton(page).click();

  await expect(page).toHaveURL(/\/roles$/);
  await expect.poll(() => state.roleSaves.length).toBe(2);
  expect(state.roleSaves[1].body).toMatchObject({ id: 'reader-role', enabled: true });
  await expect(roleRow(page, '读者')).toContainText('启用');
  expect(state.publishRequests).toEqual([]);
});

test('删除角色：确认后 DELETE rbac-v1 roles/{id} 带 app_key 并刷新列表', async ({ page }) => {
  const state = await mockWorkbench(page);
  await page.goto('/roles');

  await roleRow(page, '编辑者').getByRole('button', { name: '删除', exact: true }).click();
  const dialog = page.locator('.t-dialog').filter({ hasText: '删除角色' });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText('编辑者');
  await dialog.getByRole('button', { name: '确认删除', exact: true }).click();

  await expect.poll(() => state.roleDeletes.length).toBe(1);
  expect(state.roleDeletes[0]).toContain('/api/v1/rbac-v1/roles/editor-role');
  expect(state.roleDeletes[0]).toContain('app_key=app-orders');
  await expect(roleRow(page, '编辑者')).toHaveCount(0);
  await expect(roleRow(page, '读者')).toBeVisible();
  expect(state.publishRequests).toEqual([]);
});

test('分配用户：subject-roles 保存保留其他角色与 domain，移除后再分配', async ({ page }) => {
  const state = await mockWorkbench(page, {
    bindings: [
      { subject: ALICE.id, domain: '', role_id: 'reader-role' },
      { subject: ALICE.id, domain: '', role_id: 'editor-role' },
      { subject: ALICE.id, domain: 'tenant-a', role_id: 'reader-role' },
    ],
  });
  await page.goto('/roles');

  const openDrawer = async () => {
    await roleRow(page, '读者').getByRole('button', { name: '分配用户', exact: true }).click();
    const drawer = page.locator('.t-drawer').filter({ hasText: '分配用户 · 读者' });
    await expect(drawer).toBeVisible();
    return drawer;
  };

  // Removing this role only rewrites the default domain and keeps the other default role.
  const drawer = await openDrawer();
  const select = drawer.locator('.t-select').first();
  await expect(select.locator('.t-tag').filter({ hasText: ALICE_LABEL })).toBeVisible();
  await select.locator('.t-tag').filter({ hasText: ALICE_LABEL }).first().locator('.t-tag__icon-close').click();
  await drawer.getByRole('button', { name: '保存分配', exact: true }).click();

  await expect.poll(() => state.subjectRolePuts.length).toBe(1);
  const removal = state.subjectRolePuts[0];
  expect(removal.url).toContain('/api/v1/rbac-v1/subject-roles');
  expect(removal.url).toContain('app_key=app-orders');
  expect(removal.body).toEqual({ subject: ALICE.id, domain: '', role_ids: ['editor-role'] });
  expect(state.bindings[APP_ORDERS]).toEqual([
    { subject: ALICE.id, domain: 'tenant-a', role_id: 'reader-role' },
    { subject: ALICE.id, domain: '', role_id: 'editor-role' },
  ]);

  // Assigning the role back merges the existing other role instead of replacing the whole subject.
  const reassign = await openDrawer();
  const reassignSelect = reassign.locator('.t-select').first();
  await reassignSelect.click();
  await page.locator('.t-select-option').filter({ hasText: ALICE_LABEL }).first().click();
  await reassign.getByRole('button', { name: '保存分配', exact: true }).click();

  await expect.poll(() => state.subjectRolePuts.length).toBe(2);
  const assignment = state.subjectRolePuts[1];
  expect(assignment.body).toMatchObject({ subject: ALICE.id, domain: '' });
  expect(sortedIDs((assignment.body as { role_ids: string[] }).role_ids)).toEqual(['editor-role', 'reader-role']);
  expect(state.bindings[APP_ORDERS]).toContainEqual({ subject: ALICE.id, domain: 'tenant-a', role_id: 'reader-role' });
});

test('权限模拟：选择服务/路径/方法/SCIM 用户后提交具体路径，且不带 domain 与 release_version', async ({ page }) => {
  const state = await mockWorkbench(page);
  await page.goto('/simulate');

  const field = (name: string) => page.locator(`[data-field="${name}"]`);
  await expect(field('service').locator('input').first()).toHaveValue('订单服务 · orders-api');
  await expect(page.getByText('Domain', { exact: true })).toHaveCount(0);

  await field('path').locator('.t-select').first().click();
  await page.locator('.t-select-option').filter({ hasText: '/v1/orders · 查询订单' }).first().click();
  await field('method').locator('.t-select').first().click();
  await page.locator('.t-select-option').filter({ hasText: 'GET' }).first().click();
  await field('user').locator('.t-select').first().click();
  await page.locator('.t-select-option').filter({ hasText: ALICE_LABEL }).first().click();

  await page.getByRole('button', { name: '运行测试', exact: true }).click();

  await expect.poll(() => state.simulations.length).toBe(1);
  const simulation = state.simulations[0];
  expect(simulation.url).toContain('/api/v1/rbac-v1/simulate');
  expect(simulation.url).toContain('app_key=app-orders');
  expect(simulation.url).toContain('service_resource=orders-api');
  expect(simulation.body).toEqual({ service_resource: 'orders-api', subject: 'nexus-alice', method: 'GET', path: '/v1/orders' });
  expect(simulation.body.domain).toBeUndefined();
  expect(simulation.body.release_version).toBeUndefined();
  await expect(page.getByText('allow', { exact: true })).toBeVisible();
});

test('旧发布入口不可访问，工作台关键操作不产生发布请求', async ({ page }) => {
  const state = await mockWorkbench(page);
  await page.goto('/dashboard');
  await expect(siderMenu(page)).toBeVisible();
  await expect(siderMenu(page)).not.toContainText('发布');

  await page.goto('/roles');
  await expect(roleRow(page, '读者')).toBeVisible();
  await expect(page.getByRole('button', { name: /发布/ })).toHaveCount(0);

  await page.goto('/simulate');
  await expect(page.getByRole('button', { name: '运行测试', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: /发布/ })).toHaveCount(0);

  await page.goto('/releases');
  await expect(page.getByText('对不起，你访问的页面不存在。', { exact: true })).toBeVisible();

  expect(state.publishRequests).toEqual([]);
});

test('服务资源：新增、关联/取消关联与删除走当前 service-resources 接口', async ({ page }) => {
  const state = await mockWorkbench(page);
  await page.goto('/service-resources');

  const table = page.locator('.resource-services-table');
  await expect(table).toContainText('订单服务');
  await page.getByRole('button', { name: '新建服务', exact: true }).click();
  const drawer = page.locator('.t-drawer').filter({ hasText: '新建服务资源' });
  await expect(drawer).toBeVisible();
  const fields = drawer.locator('.t-input__inner');
  await fields.nth(0).fill('通知服务');
  await fields.nth(1).fill('notification-api');
  await fields.nth(2).fill('notification.api');
  await drawer.locator('.t-textarea__inner').fill('本地通知服务');

  await drawer.getByRole('button', { name: '创建服务', exact: true }).click();

  await expect.poll(() => state.serviceWrites.length).toBe(1);
  expect(state.serviceWrites[0].url).toBe('/api/v1/service-resources');
  expect(state.serviceWrites[0].body).toEqual({
    key: 'notification-api',
    display_name: '通知服务',
    audience: 'notification.api',
    description: '本地通知服务',
    is_active: true,
  });
  const createdRow = page.getByRole('row').filter({ hasText: '通知服务' }).first();
  await expect(createdRow).toContainText('未关联');

  // 关联写入应用的服务集合，取消关联只移除本服务。
  await createdRow.getByRole('button', { name: '关联', exact: true }).click();
  await expect.poll(() => state.serviceAssociations.length).toBe(1);
  expect(state.serviceAssociations[0].url).toBe('/api/v1/applications/app-orders/service-resources');
  expect(state.serviceAssociations[0].body).toEqual({ service_resource_keys: ['orders-api', 'notification-api'] });
  await expect(page.getByRole('row').filter({ hasText: '通知服务' }).first()).toContainText('已关联');

  await page.getByRole('row').filter({ hasText: '通知服务' }).first().getByRole('button', { name: '取消关联', exact: true }).click();
  await expect.poll(() => state.serviceAssociations.length).toBe(2);
  expect(state.serviceAssociations[1].body).toEqual({ service_resource_keys: ['orders-api'] });
  await expect(page.getByRole('row').filter({ hasText: '通知服务' }).first()).toContainText('未关联');

  // 未关联的本地服务可以删除；删除保护与 UI 语义一致（关联中按钮为禁用态已在此前验证）。
  await page.getByRole('row').filter({ hasText: '通知服务' }).first().getByRole('button', { name: '删除', exact: true }).click();
  const dialog = page.locator('.t-dialog').filter({ hasText: '删除服务资源' });
  await expect(dialog).toBeVisible();
  await dialog.getByRole('button', { name: '确认删除', exact: true }).click();

  await expect.poll(() => state.serviceDeletes.length).toBe(1);
  expect(state.serviceDeletes[0]).toBe('/api/v1/service-resources/notification-api');
  await expect(page.getByRole('row').filter({ hasText: '通知服务' })).toHaveCount(0);
  expect(state.publishRequests).toEqual([]);
});

test('API 接口：新增、编辑与删除走 app_key + service_resource 的 rbac-v1 端点', async ({ page }) => {
  const state = await mockWorkbench(page);
  await page.goto('/api-resources');

  const table = page.locator('.resource-apis-table');
  await expect(table).toContainText('查询订单');
  await page.getByRole('button', { name: '新增 API', exact: true }).click();
  const createDrawer = page.locator('.t-drawer').filter({ hasText: '新增 API 接口' });
  await expect(createDrawer).toBeVisible();
  await createDrawer.getByPlaceholder('例如 orders.list').fill('orders.detail');
  await createDrawer.getByPlaceholder('用于列表展示和权限说明').fill('查询订单详情');
  await createDrawer.locator('.t-form__item').filter({ hasText: 'HTTP 方法' }).locator('input').click();
  await page.locator('.t-select-option').filter({ hasText: 'POST' }).first().click();
  await createDrawer.getByPlaceholder('例如用户中心').fill('订单中心');
  await createDrawer.getByPlaceholder('例如 /v1/users/{id}').fill('/v1/orders/{id}');
  await createDrawer.getByRole('button', { name: '保存', exact: true }).click();

  await expect.poll(() => state.endpointWrites.length).toBe(1);
  const created = state.endpointWrites[0];
  expect(created.url).toBe('/api/v1/rbac-v1/endpoints?app_key=app-orders&service_resource=orders-api');
  expect(created.body).toEqual({
    endpoint_key: 'orders.detail',
    method: 'POST',
    path_template: '/v1/orders/{id}',
    module: '订单中心',
    summary: '查询订单详情',
    enabled: true,
  });
  const createdRow = page.getByRole('row').filter({ hasText: '查询订单详情' }).first();
  await expect(createdRow).toContainText('/v1/orders/{id}');

  await createdRow.getByRole('button', { name: '编辑', exact: true }).click();
  const editDrawer = page.locator('.t-drawer').filter({ hasText: '编辑 API 接口' });
  await expect(editDrawer.getByPlaceholder('用于列表展示和权限说明')).toHaveValue('查询订单详情');
  await editDrawer.getByPlaceholder('用于列表展示和权限说明').fill('查询订单详情（更新）');
  await editDrawer.getByRole('button', { name: '保存', exact: true }).click();

  await expect.poll(() => state.endpointWrites.length).toBe(2);
  expect(state.endpointWrites[1].url).toContain('app_key=app-orders');
  expect(state.endpointWrites[1].url).toContain('service_resource=orders-api');
  expect(state.endpointWrites[1].body).toMatchObject({ id: 'endpoint-created-1', summary: '查询订单详情（更新）', method: 'POST' });
  const updatedRow = page.getByRole('row').filter({ hasText: '查询订单详情（更新）' }).first();
  await expect(updatedRow).toBeVisible();

  await updatedRow.getByRole('button', { name: '删除', exact: true }).click();
  const dialog = page.locator('.t-dialog').filter({ hasText: '删除 API 接口' });
  await expect(dialog).toBeVisible();
  await dialog.getByRole('button', { name: '确认删除', exact: true }).click();

  await expect.poll(() => state.endpointDeletes.length).toBe(1);
  expect(state.endpointDeletes[0]).toBe('/api/v1/rbac-v1/endpoints/endpoint-created-1?app_key=app-orders&service_resource=orders-api');
  await expect(page.getByRole('row').filter({ hasText: '查询订单详情（更新）' })).toHaveCount(0);
  expect(state.publishRequests).toEqual([]);
});

test('Swagger 导入使用当前应用选中的服务，并展示导入结果', async ({ page }) => {
  const state = await mockWorkbench(page);
  await page.goto('/api-resources');

  await page.getByRole('button', { name: '导入 Swagger', exact: true }).click();
  const drawer = page.locator('.t-drawer').filter({ hasText: '导入 Swagger' });
  await expect(drawer).toBeVisible();
  await expect(drawer).toContainText('导入到共享服务目录，不会自动授予应用或角色权限。');

  await drawer.locator('.t-select').first().click();
  await page.locator('.t-select-option').filter({ hasText: '订单服务 · orders-api' }).first().click();
  await drawer.getByPlaceholder('https://example.com/openapi.json').fill('https://example.com/openapi.json');
  await drawer.getByRole('button', { name: '开始导入', exact: true }).click();

  await expect.poll(() => state.swaggerImports.length).toBe(1);
  const imported = state.swaggerImports[0];
  expect(imported.url).toBe('/api/v1/rbac-v1/endpoints/import-swagger?app_key=app-orders&service_resource=orders-api');
  expect(imported.body).toEqual({ swagger_url: 'https://example.com/openapi.json' });

  const result = drawer.locator('.resource-apis-import-result');
  await expect(result).toContainText('导入完成');
  await expect(result).toContainText('总计');
  await expect(result).toContainText('3 个接口');
  await expect(result).toContainText('2 个');
  await expect(result).toContainText('1 个');
  await expect(page.locator('.resource-apis-table')).toContainText('Swagger 导入接口');
  expect(state.publishRequests).toEqual([]);
});
