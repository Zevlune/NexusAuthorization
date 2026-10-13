import { createRequire } from 'node:module';
import { Buffer } from 'node:buffer';
import type { Page, Route } from '../../../../dashboard/node_modules/@playwright/test';

const requireDashboardDependency = createRequire(new URL('../../../../dashboard/package.json', import.meta.url));
const { expect, test } = requireDashboardDependency('@playwright/test') as typeof import('../../../../dashboard/node_modules/@playwright/test');

const APP_KEY = 'open-api-e2e';
const APP_STORAGE_KEY = 'permission-center-application';
const service = { id: 'service-orders', key: 'orders', name: 'orders', display_name: '订单服务', audience: 'orders.api', description: '', is_active: true, source: 'local' };
const makeEndpoint = (overrides: Record<string, unknown> = {}) => ({
  id: 'endpoint-list',
  service_resource: 'orders',
  endpoint_key: 'orders.list',
  method: 'GET',
  path_template: '/v1/orders',
  module: 'orders',
  summary: '查询订单',
  source: 'swagger',
  enabled: true,
  ...overrides,
});

type MockState = {
  endpoints: Array<Record<string, unknown>>;
  importRequests: Array<{ body?: Record<string, unknown> }>;
  saveRequests: Array<{ body?: Record<string, unknown> }>;
  importFailuresRemaining: number;
  counter: number;
};

const wrapped = (result: unknown) => ({ success: true, errorCode: null, errorMessage: null, result });
const failed = (errorMessage: string) => ({ success: false, errorCode: 'IMPORT_FAILED', errorMessage, result: null });
const fulfill = (route: Route, result: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(wrapped(result)) });
const fulfillError = (route: Route, errorMessage: string, status = 400) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(failed(errorMessage)) });

const installMocks = async (page: Page): Promise<MockState> => {
  const state: MockState = { endpoints: [makeEndpoint()], importRequests: [], saveRequests: [], importFailuresRemaining: 0, counter: 0 };
  await page.route('**/api/auth/me', route => fulfill(route, { isAuthenticated: true, user: { sub: 'admin', name: '管理员' } }));
  await page.route('**/api/v1/applications', route => fulfill(route, { items: [{ key: APP_KEY, display_name: '开放 API 应用', enabled: true }] }));
  await page.route('**/api/v1/service-resources', route => fulfill(route, { items: [service], writable: true }));
  await page.route('**/api/v1/applications/*/service-resources', route => fulfill(route, { items: [service] }));
  await page.route('**/api/v1/rbac-v1/roles**', route => fulfill(route, { items: [] }));
  await page.route('**/api/v1/rbac-v1/menus**', route => fulfill(route, { items: [] }));
  await page.route('**/api/v1/rbac-v1/subject-roles**', route => fulfill(route, { items: [] }));
  await page.route('**/api/v1/rbac-v1/endpoints**', async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (request.method() === 'POST' && path.endsWith('/endpoints/import-swagger')) {
      const body = request.postDataJSON() as Record<string, unknown>;
      state.importRequests.push({ body });
      if (state.importFailuresRemaining > 0) {
        state.importFailuresRemaining -= 1;
        await fulfillError(route, 'OpenAPI 文档解析失败', 400);
        return;
      }
      if (body.swagger_document) {
        state.counter += 1;
        state.endpoints = [makeEndpoint({ id: `endpoint-doc-${state.counter}`, endpoint_key: `doc.${state.counter}`, path_template: '/v1/imported', summary: '导入接口' }), ...state.endpoints];
      }
      await fulfill(route, { total: 2, created: 2, skipped: 0 }, 201);
      return;
    }
    if (request.method() === 'POST' && path.endsWith('/endpoints')) {
      const body = request.postDataJSON() as Record<string, unknown>;
      state.saveRequests.push({ body });
      state.endpoints = state.endpoints.map(item => (item.id === body.id ? { ...item, ...body } : item));
      await fulfill(route, body);
      return;
    }
    if (request.method() === 'GET') {
      await fulfill(route, { items: state.endpoints, total: state.endpoints.length, page: 1, page_size: 100 });
      return;
    }
    await fulfill(route, null, 404);
  });
  await page.addInitScript(({ key, value }) => window.sessionStorage.setItem(key, value), { key: APP_STORAGE_KEY, value: APP_KEY });
  return state;
};

test.describe('开放 API 权限目录维护', () => {
  test('以开放 API 视角展示并保存接口名称 summary', async ({ page }) => {
    const state = await installMocks(page);
    await page.goto('/open-api-permissions');

    await expect(page.getByLabel('当前位置').getByText('开放 API 权限', { exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: '导入 OpenAPI', exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: '新增开放 API', exact: true })).toBeVisible();
    await expect(page.locator('th').filter({ hasText: '接口名称 / 路径' })).toBeVisible();
    await expect(page.getByRole('row').filter({ hasText: '查询订单' })).toContainText('/v1/orders');

    await page.getByRole('row').filter({ hasText: '查询订单' }).getByRole('button', { name: '编辑', exact: true }).click();
    const drawer = page.locator('.t-drawer').filter({ hasText: '编辑开放 API' });
    await expect(drawer).toBeVisible();
    const nameInput = drawer.locator('.t-form__item').filter({ hasText: '接口名称' }).locator('input');
    await nameInput.fill('订单列表接口');
    await drawer.getByRole('button', { name: '保存', exact: true }).click();

    await expect.poll(() => state.saveRequests.length).toBe(1);
    expect(state.saveRequests[0]?.body).toMatchObject({ id: 'endpoint-list', summary: '订单列表接口' });
    await expect(page.getByRole('row').filter({ hasText: '订单列表接口' })).toBeVisible();
  });

  test('导入本地 JSON 与 YAML OpenAPI 文档并展示导入结果', async ({ page }) => {
    const state = await installMocks(page);
    await page.goto('/open-api-permissions');

    const jsonDocument = JSON.stringify({ openapi: '3.0.3', info: { title: 'Orders API' }, paths: { '/v1/orders': { get: { summary: '查询订单' } } } });
    await page.getByRole('button', { name: '导入 OpenAPI', exact: true }).click();
    const drawer = page.locator('.t-drawer').filter({ hasText: '导入 OpenAPI' });
    await expect(drawer).toBeVisible();
    await drawer.locator('.t-select').first().click();
    await page.locator('.t-select-option').filter({ hasText: '订单服务 · orders' }).click();

    // URL 模式保留英文旧字段，兼容既有 Swagger 用例。
    await expect(drawer.locator('.t-form__label').filter({ hasText: 'Swagger URL' })).toBeVisible();
    await drawer.getByText('本地文件', { exact: true }).click();
    await drawer.locator('input[type=file]').setInputFiles({ name: 'openapi.json', mimeType: 'application/json', buffer: Buffer.from(jsonDocument) });
    await expect(drawer).toContainText('openapi.json');
    await drawer.getByRole('button', { name: '开始导入', exact: true }).click();

    await expect.poll(() => state.importRequests.length).toBe(1);
    expect(state.importRequests[0]?.body).toEqual({ swagger_document: jsonDocument });
    await expect(drawer.getByText('导入完成', { exact: true })).toBeVisible();

    const yamlDocument = 'openapi: 3.0.3\ninfo:\n  title: Orders API\npaths:\n  "/v1/orders":\n    get:\n      summary: 查询订单\n';
    await drawer.getByRole('button', { name: '再导入', exact: true }).click();
    await drawer.locator('input[type=file]').setInputFiles({ name: 'openapi.yaml', mimeType: 'application/x-yaml', buffer: Buffer.from(yamlDocument) });
    await expect(drawer).toContainText('openapi.yaml');
    await drawer.getByRole('button', { name: '开始导入', exact: true }).click();

    await expect.poll(() => state.importRequests.length).toBe(2);
    expect(state.importRequests[1]?.body).toEqual({ swagger_document: yamlDocument });
    await expect(drawer.getByText('导入完成', { exact: true })).toBeVisible();
  });

  test('URL 模式导入失败后可以重试成功', async ({ page }) => {
    const state = await installMocks(page);
    state.importFailuresRemaining = 1;
    await page.goto('/open-api-permissions');

    await page.getByRole('button', { name: '导入 OpenAPI', exact: true }).click();
    const drawer = page.locator('.t-drawer').filter({ hasText: '导入 OpenAPI' });
    await drawer.locator('.t-select').first().click();
    await page.locator('.t-select-option').filter({ hasText: '订单服务 · orders' }).click();
    await drawer.getByPlaceholder('https://example.com/openapi.json').fill('https://example.com/openapi.json');
    await drawer.getByRole('button', { name: '开始导入', exact: true }).click();

    await expect(drawer.locator('.t-alert').filter({ hasText: 'OpenAPI 文档解析失败' })).toBeVisible();
    await expect(drawer.getByRole('button', { name: '重试导入', exact: true })).toBeVisible();
    await drawer.getByRole('button', { name: '重试导入', exact: true }).click();

    await expect.poll(() => state.importRequests.length).toBe(2);
    expect(state.importRequests[0]?.body).toEqual({ swagger_url: 'https://example.com/openapi.json' });
    expect(state.importRequests[1]?.body).toEqual({ swagger_url: 'https://example.com/openapi.json' });
    await expect(drawer.getByText('导入完成', { exact: true })).toBeVisible();
  });

  test('选择无效文档会清空之前成功的文件选择', async ({ page }) => {
    const state = await installMocks(page);
    await page.goto('/open-api-permissions');

    await page.getByRole('button', { name: '导入 OpenAPI', exact: true }).click();
    const drawer = page.locator('.t-drawer').filter({ hasText: '导入 OpenAPI' });
    await drawer.locator('.t-select').first().click();
    await page.locator('.t-select-option').filter({ hasText: '订单服务 · orders' }).click();
    await drawer.getByText('本地文件', { exact: true }).click();

    const validDocument = JSON.stringify({ openapi: '3.0.3', paths: { '/v1/orders': { get: {} } } });
    await drawer.locator('input[type=file]').setInputFiles({ name: 'openapi.json', mimeType: 'application/json', buffer: Buffer.from(validDocument) });
    await expect(drawer).toContainText('openapi.json');

    await drawer.locator('input[type=file]').setInputFiles({ name: 'broken.json', mimeType: 'application/json', buffer: Buffer.from('{"hello":"world"}') });
    await expect(drawer).toContainText('文件内容不是有效的 OpenAPI/Swagger 文档');
    await expect(drawer).not.toContainText('openapi.json');
    await expect(drawer).toContainText('支持 JSON / YAML，最大 8MB');

    await drawer.getByRole('button', { name: '开始导入', exact: true }).click();
    await expect(drawer).toContainText('请选择要导入的 OpenAPI 文档');
    expect(state.importRequests).toHaveLength(0);
  });
});
