import { createRequire } from 'node:module';
import type { Locator, Page } from '../../../../dashboard/node_modules/@playwright/test';

const requireDashboardDependency = createRequire(new URL('../../../../dashboard/package.json', import.meta.url));
const { expect, test } = requireDashboardDependency('@playwright/test') as typeof import('../../../../dashboard/node_modules/@playwright/test');

// Frozen scope: the permission simulator (/simulate) no longer accepts a hand-typed Subject or
// Domain. The user comes from the real SCIM directory (value = SCIM id / OIDC sub), the path comes
// from the selected service's API catalog, and the HTTP method follows the chosen path. The request
// still goes to /api/v1/rbac-v1/simulate with the concrete path; no service business HTTP is made.
// Everything below mocks the workbench API, so no real engine, database or SCIM directory is touched.

const APP_ORDERS = 'app-orders';
const APP_REPORTS = 'app-reports';
// An application whose last service association was removed while the shared endpoint directory
// still returns the removed service's paths.
const APP_ORPHAN = 'app-orphan';
const APPLICATION_STORAGE_KEY = 'nexus-authorization-application';
const APPLICATION_CHANGE_EVENT = 'nexus-authorization-application-change';

type ServiceSeed = { key: string; display_name: string; is_active: boolean; source: 'local' };
type EndpointSeed = {
  id: string; service_resource: string; method: string;
  path_template: string; summary: string; enabled: boolean;
};

const SERVICES: ServiceSeed[] = [
  { key: 'orders-api', display_name: '订单服务', is_active: true, source: 'local' },
  { key: 'reports-api', display_name: '报表服务', is_active: true, source: 'local' },
];

const APP_SERVICES: Record<string, string[]> = {
  [APP_ORDERS]: ['orders-api', 'reports-api'],
  [APP_REPORTS]: ['reports-api'],
  [APP_ORPHAN]: [],
};

const ORDERS_ENDPOINTS: EndpointSeed[] = [
  { id: 'ep-orders-list', service_resource: 'orders-api', method: 'GET', path_template: '/v1/orders', summary: '查询订单', enabled: true },
  { id: 'ep-orders-create', service_resource: 'orders-api', method: 'POST', path_template: '/v1/orders', summary: '创建订单', enabled: true },
  { id: 'ep-orders-detail', service_resource: 'orders-api', method: 'GET', path_template: '/v1/orders/{id}', summary: '订单详情', enabled: true },
  { id: 'ep-orders-detail-update', service_resource: 'orders-api', method: 'POST', path_template: '/v1/orders/{id}', summary: '', enabled: true },
  // `constructor` is a legal OpenAPI parameter name but a dangerous plain-object key.
  { id: 'ep-orders-ctor', service_resource: 'orders-api', method: 'GET', path_template: '/v1/orders/{constructor}', summary: '构造参数详情', enabled: true },
  { id: 'ep-reports-summary', service_resource: 'reports-api', method: 'GET', path_template: '/v1/reports/summary', summary: '报表摘要', enabled: true },
];

const ENDPOINTS: Record<string, EndpointSeed[]> = {
  [APP_ORDERS]: ORDERS_ENDPOINTS,
  [APP_REPORTS]: [
    { id: 'ep-reports-detail', service_resource: 'reports-api', method: 'GET', path_template: '/v1/reports/{reportId}', summary: '报表详情', enabled: true },
  ],
  [APP_ORPHAN]: ORDERS_ENDPOINTS,
};

const ALICE = { id: 'nexus-alice', userName: 'alice', name: { formatted: 'Alice 张' }, emails: [{ value: 'alice@example.test', primary: true }], active: true };
const ALICE_LABEL = 'Alice 张 · alice · alice@example.test';
const CAROL = { id: 'nexus-carol', userName: 'carol', name: { formatted: 'Carol 王' }, emails: [{ value: 'carol@example.test', primary: true }], active: false };

type SavedRequest = { url: string; body: Record<string, unknown> };

const userDirectory = (resources: unknown[]) => ({
  totalResults: resources.length, startIndex: 1, itemsPerPage: resources.length, Resources: resources,
});

// A latch that lets a test freeze a route handler mid-request so the pending ("running") UI is observable.
const createGate = () => {
  let paused = false;
  let waiters: (() => void)[] = [];
  return {
    pause: () => { paused = true; },
    resume: () => { paused = false; const pending = waiters; waiters = []; pending.forEach(resolve => resolve()); },
    wait: async () => { while (paused) await new Promise<void>(resolve => { waiters.push(resolve); }); },
  };
};

async function mockSimulate(page: Page, options: { users?: unknown[] } = {}) {
  const users = options.users ?? [ALICE];
  const simulations: SavedRequest[] = [];
  const endpointCatalogs: string[] = [];
  const gate = createGate();
  await page.addInitScript((key: string) => sessionStorage.setItem('nexus-authorization-application', key), APP_ORDERS);
  await page.route('**/api/**', async route => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    if (!path.startsWith('/api/')) { await route.fallback(); return; }
    const appKey = url.searchParams.get('app_key') ?? '';
    const fulfill = (result: unknown) => route.fulfill({ json: { success: true, result } });
    if (path === '/api/auth/me') return fulfill({ isAuthenticated: true, user: { sub: 'admin' } });
    if (path === '/api/v1/rbac-v1/endpoints') {
      const items = ENDPOINTS[appKey] ?? [];
      endpointCatalogs.push(appKey);
      return fulfill({ items, total: items.length, page: 1, page_size: 100 });
    }
    if (path === '/api/v1/rbac-v1/simulate' && request.method() === 'POST') {
      await gate.wait();
      const body = request.postDataJSON() as Record<string, unknown>;
      simulations.push({ url: path + url.search, body });
      // The verdict depends on the method so a test can tell an old result apart from a new one.
      const allowed = body.method === 'GET';
      return fulfill({ allowed, effect: allowed ? 'allow' : 'deny', reason: allowed ? 'role_match' : 'no_role_match', matched_roles: allowed ? ['reader-role'] : [] });
    }
    if (path === '/api/v1/users') return fulfill(userDirectory(users));
    const applicationServices = path.match(/^\/api\/v1\/applications\/([^/]+)\/service-resources$/);
    if (applicationServices) {
      const associated = APP_SERVICES[decodeURIComponent(applicationServices[1])] ?? [];
      return fulfill({ items: SERVICES.filter(item => associated.includes(item.key)) });
    }
    return fulfill({ items: [] });
  });
  return {
    simulations,
    endpointCatalogs,
    hold: gate.pause,
    release: gate.resume,
  };
}

const field = (page: Page, name: string): Locator => page.locator(`[data-field="${name}"]`);
const fieldInput = (page: Page, name: string): Locator => field(page, name).locator('input').first();
const runButton = (page: Page): Locator => page.getByRole('button', { name: '运行测试', exact: true });
const openSelect = async (page: Page, name: string) => { await field(page, name).locator('.t-select').first().click(); };
// Only the open dropdown is visible; closed selects keep their options in the DOM.
const chooseOption = async (page: Page, text: string) => { await page.locator('.t-select-option:visible').filter({ hasText: text }).first().click(); };
const visibleOptions = (page: Page): Locator => page.locator('.t-select-option:visible');
const selectValue = async (page: Page, name: string, text: string) => { await openSelect(page, name); await chooseOption(page, text); };
const finalPath = (page: Page): Locator => page.locator('[data-testid="simulate-final-path"] code');

test('权限模拟：默认服务、SCIM 用户与具体路径提交，且不展示 Domain、不提交 domain', async ({ page }) => {
  const state = await mockSimulate(page);
  await page.goto('/simulate');

  await expect(fieldInput(page, 'service')).toHaveValue('订单服务 · orders-api');
  await expect(page.getByText('Domain', { exact: true })).toHaveCount(0);
  await expect(runButton(page)).toBeDisabled();

  await selectValue(page, 'path', '/v1/orders · 查询订单');
  await selectValue(page, 'method', 'GET');
  await selectValue(page, 'user', ALICE_LABEL);
  await expect(runButton(page)).toBeEnabled();

  await runButton(page).click();
  await expect.poll(() => state.simulations.length).toBe(1);
  expect(state.simulations[0].url).toContain('/api/v1/rbac-v1/simulate');
  expect(state.simulations[0].url).toContain('app_key=app-orders');
  expect(state.simulations[0].url).toContain('service_resource=orders-api');
  // The SCIM directory id is submitted as the subject, never the display label.
  expect(state.simulations[0].body).toEqual({ service_resource: 'orders-api', subject: 'nexus-alice', method: 'GET', path: '/v1/orders' });
  expect(state.simulations[0].body.domain).toBeUndefined();
  await expect(page.getByText('allow', { exact: true })).toBeVisible();
});

test('权限模拟：同路径多方法去重联动，切换服务清除旧选择且不泄漏其他服务路径', async ({ page }) => {
  await mockSimulate(page);
  await page.goto('/simulate');

  await selectValue(page, 'path', '/v1/orders · 查询订单');
  await openSelect(page, 'method');
  await expect(visibleOptions(page)).toHaveCount(2);
  await expect(visibleOptions(page).filter({ hasText: 'GET' })).toHaveCount(1);
  await expect(visibleOptions(page).filter({ hasText: 'POST' })).toHaveCount(1);
  await chooseOption(page, 'POST');
  await expect(fieldInput(page, 'method')).toHaveValue('POST');

  await selectValue(page, 'service', '报表服务 · reports-api');
  await expect(fieldInput(page, 'path')).toHaveValue('');
  await expect(fieldInput(page, 'method')).toHaveValue('');

  await openSelect(page, 'path');
  const texts = await visibleOptions(page).allInnerTexts();
  expect(texts.some(text => text.includes('/v1/orders'))).toBe(false);
  expect(texts.some(text => text.includes('/v1/reports/summary'))).toBe(true);
});

test('权限模拟：动态路径参数按单段编码构造实际请求路径', async ({ page }) => {
  const state = await mockSimulate(page);
  await page.goto('/simulate');

  await selectValue(page, 'path', '/v1/orders/{id} · 订单详情');
  await expect(finalPath(page)).toHaveText('/v1/orders/{id}');
  const param = page.getByRole('textbox', { name: '请输入id' });
  await expect(param).toBeVisible();

  await selectValue(page, 'method', 'GET');
  await selectValue(page, 'user', ALICE_LABEL);
  await expect(runButton(page)).toBeDisabled();

  await param.fill('a/b c');
  await expect(finalPath(page)).toHaveText('/v1/orders/a%2Fb%20c');
  await expect(runButton(page)).toBeEnabled();

  await runButton(page).click();
  await expect.poll(() => state.simulations.length).toBe(1);
  expect(state.simulations[0].body.path).toBe('/v1/orders/a%2Fb%20c');
  expect(String(state.simulations[0].body.path)).not.toContain('{id}');
});

test('权限模拟：任意输入修改后清空上一次结果', async ({ page }) => {
  await mockSimulate(page);
  await page.goto('/simulate');

  await selectValue(page, 'path', '/v1/orders · 查询订单');
  await selectValue(page, 'method', 'GET');
  await selectValue(page, 'user', ALICE_LABEL);
  await runButton(page).click();
  await expect(page.getByText('allow', { exact: true })).toBeVisible();

  await selectValue(page, 'method', 'POST');
  await expect(page.getByText('allow', { exact: true })).toHaveCount(0);
  await expect(page.getByText('选择目标服务、接口路径、HTTP 方法与用户后运行测试')).toBeVisible();
});

test('权限模拟：运行中禁用全部输入，请求开始清空旧结果且只展示新结果', async ({ page }) => {
  const state = await mockSimulate(page);
  await page.goto('/simulate');

  await selectValue(page, 'path', '/v1/orders/{id} · 订单详情');
  await page.getByRole('textbox', { name: '请输入id' }).fill('42');
  await selectValue(page, 'method', 'GET');
  await selectValue(page, 'user', ALICE_LABEL);
  await runButton(page).click();
  await expect(page.getByText('allow', { exact: true })).toBeVisible();
  expect(state.simulations).toHaveLength(1);

  // Pick a method whose mocked verdict differs, then hold the response open to freeze the running UI.
  await selectValue(page, 'method', 'POST');
  await expect(page.getByText('allow', { exact: true })).toHaveCount(0);

  state.hold();
  await runButton(page).click();

  // While pending every selector is inert, the run button is disabled, and no stale verdict sticks.
  await expect(fieldInput(page, 'service')).toBeDisabled();
  await expect(fieldInput(page, 'path')).toBeDisabled();
  await expect(fieldInput(page, 'method')).toBeDisabled();
  await expect(fieldInput(page, 'user')).toBeDisabled();
  await expect(page.getByRole('textbox', { name: '请输入id' })).toBeDisabled();
  await expect(runButton(page)).toBeDisabled();
  await expect(page.getByText('allow', { exact: true })).toHaveCount(0);
  await expect(page.getByText('deny', { exact: true })).toHaveCount(0);
  await expect(page.getByText('选择目标服务、接口路径、HTTP 方法与用户后运行测试')).toBeVisible();

  state.release();
  await expect(page.getByText('deny', { exact: true })).toBeVisible();
  expect(state.simulations).toHaveLength(2);
  await expect(fieldInput(page, 'path')).toBeEnabled();
  await expect(runButton(page)).toBeEnabled();
});

test('权限模拟：constructor 等原型参数名可安全构造实际路径', async ({ page }) => {
  const state = await mockSimulate(page);
  await page.goto('/simulate');

  await selectValue(page, 'path', '/v1/orders/{constructor} · 构造参数详情');
  await expect(finalPath(page)).toHaveText('/v1/orders/{constructor}');
  const param = page.getByRole('textbox', { name: '请输入constructor' });
  await expect(param).toBeVisible();

  await selectValue(page, 'method', 'GET');
  await selectValue(page, 'user', ALICE_LABEL);
  await expect(runButton(page)).toBeDisabled();

  await param.fill('proto');
  await expect(finalPath(page)).toHaveText('/v1/orders/proto');
  await expect(runButton(page)).toBeEnabled();

  await runButton(page).click();
  await expect.poll(() => state.simulations.length).toBe(1);
  expect(state.simulations[0].body.path).toBe('/v1/orders/proto');
  expect(String(state.simulations[0].body.path)).not.toContain('{constructor}');
});

test('权限模拟：用户目录加载失败可重试，不再发送鉴权请求', async ({ page }) => {
  const state = await mockSimulate(page);
  let attempts = 0;
  await page.route('**/api/v1/users?**', async route => {
    attempts += 1;
    if (attempts === 1) return route.fulfill({ status: 502, json: { success: false, errorMessage: '用户目录暂时不可用' } });
    return route.fulfill({ json: { success: true, result: userDirectory([ALICE]) } });
  });

  await page.goto('/simulate');
  await expect(page.getByRole('alert')).toContainText('用户目录暂时不可用');
  await expect(fieldInput(page, 'user')).toBeDisabled();
  await expect(runButton(page)).toBeDisabled();

  await page.getByRole('button', { name: '重试', exact: true }).click();
  await expect(fieldInput(page, 'user')).toBeEnabled();
  await selectValue(page, 'path', '/v1/orders · 查询订单');
  await selectValue(page, 'method', 'GET');
  await selectValue(page, 'user', ALICE_LABEL);
  await expect(runButton(page)).toBeEnabled();
  expect(state.simulations).toEqual([]);
});

test('权限模拟：空用户目录提示且不能运行', async ({ page }) => {
  await mockSimulate(page, { users: [] });
  await page.goto('/simulate');

  await expect(page.getByRole('alert')).toContainText('用户目录为空');
  await openSelect(page, 'user');
  await expect(page.getByText('目录中没有用户', { exact: true })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(runButton(page)).toBeDisabled();
});

test('权限模拟：切换应用清空选择与结果', async ({ page }) => {
  await mockSimulate(page);
  await page.goto('/simulate');

  await selectValue(page, 'path', '/v1/orders · 查询订单');
  await selectValue(page, 'method', 'GET');
  await selectValue(page, 'user', ALICE_LABEL);
  await runButton(page).click();
  await expect(page.getByText('allow', { exact: true })).toBeVisible();

  await page.evaluate(({ key, event }) => {
    window.sessionStorage.setItem(key, 'app-reports');
    window.dispatchEvent(new CustomEvent(event, { detail: 'app-reports' }));
  }, { key: APPLICATION_STORAGE_KEY, event: APPLICATION_CHANGE_EVENT });

  await expect(fieldInput(page, 'service')).toHaveValue('报表服务 · reports-api');
  await expect(fieldInput(page, 'path')).toHaveValue('');
  await expect(fieldInput(page, 'method')).toHaveValue('');
  await expect(fieldInput(page, 'user')).toHaveValue('');
  await expect(page.getByText('allow', { exact: true })).toHaveCount(0);
});

test('权限模拟：应用最后一个服务被移除或停用且目录接口仍存在时清空旧判定与依赖选择且不能运行', async ({ page }) => {
  const state = await mockSimulate(page);
  await page.goto('/simulate');

  // A completed allow for an application that still owns orders-api.
  await selectValue(page, 'path', '/v1/orders · 查询订单');
  await selectValue(page, 'method', 'GET');
  await selectValue(page, 'user', ALICE_LABEL);
  await runButton(page).click();
  await expect(page.getByText('allow', { exact: true })).toBeVisible();
  expect(state.simulations).toHaveLength(1);

  // The application loses its last service association, but the shared endpoint directory still
  // serves the removed service's paths for the new app key.
  await page.evaluate(({ key, event }) => {
    window.sessionStorage.setItem(key, 'app-orphan');
    window.dispatchEvent(new CustomEvent(event, { detail: 'app-orphan' }));
  }, { key: APPLICATION_STORAGE_KEY, event: APPLICATION_CHANGE_EVENT });
  expect(state.endpointCatalogs).toContain(APP_ORDERS);
  await expect.poll(() => state.endpointCatalogs.at(-1)).toBe(APP_ORPHAN);

  // The stale service, its path/method and the previous allow are all gone, and nothing can run.
  await expect(fieldInput(page, 'service')).toHaveValue('');
  await expect(fieldInput(page, 'path')).toBeDisabled();
  await expect(fieldInput(page, 'method')).toBeDisabled();
  await expect(fieldInput(page, 'user')).toHaveValue('');
  await expect(page.getByText('allow', { exact: true })).toHaveCount(0);
  await expect(page.getByText('deny', { exact: true })).toHaveCount(0);
  await expect(runButton(page)).toBeDisabled();

  // The removed service is not offered again even though its endpoints are still in the directory.
  await openSelect(page, 'service');
  await expect(visibleOptions(page).filter({ hasText: 'orders-api' })).toHaveCount(0);
  await page.keyboard.press('Escape');
  await expect(runButton(page)).toBeDisabled();
  expect(state.simulations).toHaveLength(1);
});

test('权限模拟：停用用户标记为已停用且不可选择', async ({ page }) => {
  await mockSimulate(page, { users: [ALICE, CAROL] });
  await page.goto('/simulate');

  await openSelect(page, 'user');
  const carol = page.locator('.t-select-option').filter({ hasText: 'Carol 王' });
  await expect(carol).toContainText('已停用');
  await expect(carol).toHaveClass(/t-is-disabled/);
});

test('权限模拟：目录全部停用时给出提示且不能运行', async ({ page }) => {
  await mockSimulate(page, { users: [CAROL] });
  await page.goto('/simulate');

  await expect(page.getByRole('alert')).toContainText('没有启用的用户');
  await expect(runButton(page)).toBeDisabled();
});

test('权限模拟：951px 视口保持两列网格且字段顺序为 目标服务/Path/方法/用户', async ({ page }) => {
  await page.setViewportSize({ width: 951, height: 900 });
  await mockSimulate(page);
  await page.goto('/simulate');

  await expect(page.locator('[data-testid="simulate-grid"]')).toBeVisible();
  const order = await page.locator('[data-testid="simulate-grid"] [data-field]')
    .evaluateAll(nodes => nodes.map(node => node.getAttribute('data-field')));
  expect(order).toEqual(['service', 'path', 'method', 'user']);

  const columns = await page.locator('[data-testid="simulate-grid"]')
    .evaluate(element => getComputedStyle(element).gridTemplateColumns);
  expect(columns.trim().split(/\s+/).length).toBe(2);
});
