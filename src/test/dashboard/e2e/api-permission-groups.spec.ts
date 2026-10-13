import { createRequire } from 'node:module';
import type { Page, Route } from '../../../../dashboard/node_modules/@playwright/test';

const requireDashboardDependency = createRequire(new URL('../../../../dashboard/package.json', import.meta.url));
const { expect, test } = requireDashboardDependency('@playwright/test') as typeof import('../../../../dashboard/node_modules/@playwright/test');

const APP_KEY = 'api-permission-group-e2e';
const APP_STORAGE_KEY = 'permission-center-application';
const readEndpoint = { id: 'endpoint-read', service_resource: 'orders', endpoint_key: 'orders.read', method: 'GET', path_template: '/v1/orders', module: 'orders', summary: '查询订单', source: 'manual', enabled: true };
const writeEndpoint = { id: 'endpoint-write', service_resource: 'orders', endpoint_key: 'orders.write', method: 'POST', path_template: '/v1/orders', module: 'orders', summary: '创建订单', source: 'manual', enabled: true };
const service = { id: 'service-orders', key: 'orders', name: 'orders', display_name: '订单服务', audience: 'orders.api', description: '', is_active: true, source: 'local' };

type Group = { id: string; service_resource: string; code: string; name: string; action: 'read' | 'write'; enabled: boolean; endpoint_ids: string[]; created_at: string; updated_at: string };
type MockState = { groups: Group[]; requests: Array<{ method: string; path: string; body?: Record<string, unknown> }>; counter: number };

const wrapped = (result: unknown) => ({ success: true, errorCode: null, errorMessage: null, result });
const fulfill = (route: Route, result: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(wrapped(result)) });
const makeGroup = (overrides: Partial<Group> = {}): Group => ({ id: 'group-orders-read', service_resource: 'orders', code: 'orders.read', name: '订单只读', action: 'read', enabled: true, endpoint_ids: [readEndpoint.id], created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', ...overrides });

const installMocks = async (page: Page): Promise<MockState> => {
  const state: MockState = { groups: [makeGroup(), makeGroup({ id: 'group-orders-write', code: 'orders.write', name: '订单写入', action: 'write', endpoint_ids: [readEndpoint.id, writeEndpoint.id] })], requests: [], counter: 0 };
  await page.route('**/api/auth/me', route => fulfill(route, { isAuthenticated: true, user: { sub: 'admin', name: '管理员' } }));
  await page.route('**/api/v1/service-resources', route => fulfill(route, { items: [service], writable: true }));
  await page.route('**/api/v1/applications/*/service-resources', route => fulfill(route, { items: [service] }));
  await page.route('**/api/v1/rbac-v1/endpoints**', route => fulfill(route, { items: [readEndpoint, writeEndpoint], total: 2, page: 1, page_size: 100 }));
  await page.route('**/api/v1/rbac-v1/roles**', route => fulfill(route, { items: [] }));
  await page.route('**/api/v1/rbac-v1/menus**', route => fulfill(route, { items: [] }));
  await page.route('**/api/v1/rbac-v1/subject-roles**', route => fulfill(route, { items: [] }));
  await page.route('**/api/v1/api-permission-groups**', async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (request.method() === 'GET') { await fulfill(route, { items: state.groups }); return; }
    if (request.method() === 'DELETE') {
      const id = decodeURIComponent(path.split('/').pop() || '');
      state.groups = state.groups.filter(group => group.id !== id);
      await fulfill(route, null);
      return;
    }
    if (request.method() === 'POST') {
      const body = request.postDataJSON() as Group;
      state.requests.push({ method: 'POST', path, body });
      state.groups = body.id ? state.groups.map(group => group.id === body.id ? body : group) : [...state.groups, { ...body, id: `group-${state.counter += 1}` }];
      await fulfill(route, body);
      return;
    }
    await fulfill(route, null, 404);
  });
  await page.addInitScript(({ key, value }) => window.sessionStorage.setItem(key, value), { key: APP_STORAGE_KEY, value: APP_KEY });
  return state;
};

test.describe('API 权限组', () => {
  test('列表展示编码、服务、类型、接口数与状态并提供创建/导入入口', async ({ page }) => {
    await installMocks(page);
    await page.goto('/api-permission-groups');
    await expect(page.getByRole('button', { name: '创建权限组', exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: '导入开放 API', exact: true })).toBeVisible();
    const row = page.getByRole('row').filter({ hasText: 'orders.read' });
    await expect(row).toContainText('订单只读');
    await expect(row).toContainText('订单服务 · orders');
    await expect(row).toContainText('只读');
    await expect(row).toContainText('启用');
    await page.getByRole('button', { name: '导入开放 API', exact: true }).click();
    await expect(page).toHaveURL(/\/open-api-permissions$/);
  });

  test('创建前可选服务与编码，编辑时锁定服务与编码', async ({ page }) => {
    const state = await installMocks(page);
    await page.goto('/api-permission-groups/create');
    await expect(page.getByPlaceholder('例如：orders.read')).toBeEnabled();
    await page.getByPlaceholder('例如：orders.read').fill('orders.export');
    await page.getByPlaceholder('例如：订单只读').fill('订单导出');
    await page.locator('.api-permission-group-endpoints .t-checkbox').filter({ hasText: '查询订单' }).click();
    await page.getByRole('button', { name: '创建权限组', exact: true }).click();
    await expect(page).toHaveURL(/\/api-permission-groups$/);
    const body = state.requests.find(request => request.method === 'POST')?.body as Record<string, unknown>;
    expect(body).toMatchObject({ service_resource: 'orders', code: 'orders.export', name: '订单导出', action: 'read', enabled: true, endpoint_ids: [readEndpoint.id] });
    expect(body.id).toBeUndefined();

    await page.goto('/api-permission-groups/group-orders-read/edit');
    await expect(page.getByPlaceholder('例如：orders.read')).toBeDisabled();
    await expect(page.getByPlaceholder('例如：orders.read')).toHaveValue('orders.read');
    await expect(page.getByPlaceholder('例如：订单只读')).toHaveValue('订单只读');
    await expect(page.getByPlaceholder('请选择服务')).toBeDisabled();
  });

  test('只读仅限 GET/HEAD，写入可选 GET 且保留被搜索隐藏的选择', async ({ page }) => {
    const state = await installMocks(page);
    await page.goto('/api-permission-groups/create');
    await page.getByPlaceholder('例如：orders.read').fill('orders.mixed');
    await page.getByPlaceholder('例如：订单只读').fill('订单混合');
    await expect(page.locator('.api-permission-group-endpoints .t-checkbox').filter({ hasText: '创建订单' })).toHaveCount(0);
    await page.locator('.api-permission-group-endpoints .t-checkbox').filter({ hasText: '查询订单' }).click();
    await page.locator('.api-permission-group-grid .t-radio-button').filter({ hasText: '写入' }).click();
    await expect(page.locator('.api-permission-group-endpoints .t-checkbox').filter({ hasText: '创建订单' })).toBeVisible();
    await page.locator('.api-permission-group-endpoints .t-checkbox').filter({ hasText: '创建订单' }).click();
    await page.getByPlaceholder('搜索接口名称、模块或路径').fill('创建订单');
    await expect(page.locator('.api-permission-group-endpoints .t-checkbox').filter({ hasText: '查询订单' })).toHaveCount(0);
    await page.getByPlaceholder('搜索接口名称、模块或路径').fill('');
    await expect(page.locator('.api-permission-group-endpoints .t-checkbox').filter({ hasText: '查询订单' }).locator('input')).toBeChecked();
    await page.getByRole('button', { name: '创建权限组', exact: true }).click();
    await expect(page).toHaveURL(/\/api-permission-groups$/);
    const body = state.requests.find(request => request.method === 'POST')?.body as { action: string; endpoint_ids: string[] };
    expect(body.action).toBe('write');
    expect(body.endpoint_ids).toEqual(expect.arrayContaining([readEndpoint.id, writeEndpoint.id]));
  });

  test('编辑时展示不可用的已选接口并阻止保存直到移除', async ({ page }) => {
    const state = await installMocks(page);
    state.groups = state.groups.map(group => group.id === 'group-orders-read' ? { ...group, endpoint_ids: [readEndpoint.id, writeEndpoint.id, 'endpoint-missing'] } : group);
    await page.goto('/api-permission-groups/group-orders-read/edit');
    await expect(page.locator('.api-permission-group-unavailable > div:not(.t-alert)')).toHaveCount(2);
    await expect(page.locator('.api-permission-group-unavailable')).toContainText('endpoint-missing');
    await page.getByRole('button', { name: '保存修改', exact: true }).click();
    await expect(page.getByText('已选接口包含停用、跨服务或不兼容当前权限类型的接口，请先移除后再保存')).toBeVisible();
    expect(state.requests.filter(request => request.method === 'POST')).toHaveLength(0);
    await page.locator('.api-permission-group-unavailable').getByRole('button', { name: '移除接口' }).first().click();
    await page.locator('.api-permission-group-unavailable').getByRole('button', { name: '移除接口' }).first().click();
    await expect(page.locator('.api-permission-group-unavailable > div:not(.t-alert)')).toHaveCount(0);
    await page.getByRole('button', { name: '保存修改', exact: true }).click();
    await expect(page).toHaveURL(/\/api-permission-groups$/);
    const body = state.requests.find(request => request.method === 'POST')?.body as { endpoint_ids: string[] };
    expect(body.endpoint_ids).toEqual([readEndpoint.id]);
  });
});
