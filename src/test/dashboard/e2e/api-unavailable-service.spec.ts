import { createRequire } from 'node:module';
import type { Locator, Page } from '../../../../dashboard/node_modules/@playwright/test';

const requireDashboardDependency = createRequire(new URL('../../../../dashboard/package.json', import.meta.url));
const { expect, test } = requireDashboardDependency('@playwright/test') as typeof import('../../../../dashboard/node_modules/@playwright/test');

// Regression for issue #17: endpoints from a linked service must remain visible
// when the catalog marks that service inactive. They cannot be edited or newly
// imported, but manual deletion must remain available.

const APP_KEY = 'console';

const INACTIVE_SERVICE = {
  id: 'svc-orders',
  key: 'orders-service',
  name: 'orders-service',
  display_name: '订单服务',
  audience: 'orders.api',
  description: '',
  is_active: false,
  source: 'nexusauth',
};

const STALE_ENDPOINT = {
  id: 'ep-order-history',
  service_resource: 'orders-service',
  endpoint_key: 'orders.history',
  method: 'GET',
  path_template: '/v1/orders/history',
  module: '订单',
  summary: '查询历史订单',
  source: 'manual',
  enabled: true,
};

async function mockInactiveService(page: Page) {
  const state = {
    endpoints: [STALE_ENDPOINT],
    deletes: [] as string[],
  };
  await page.addInitScript(() => sessionStorage.setItem('nexus-authorization-application', 'console'));
  await page.route('**/api/**', async route => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    if (!path.startsWith('/api/')) {
      await route.fallback();
      return;
    }
    if (path === '/api/auth/me') {
      await route.fulfill({ json: { success: true, result: { isAuthenticated: true, user: { sub: 'admin', name: '管理员' } } } });
      return;
    }
    if (path === `/api/v1/applications/${APP_KEY}/service-resources`) {
      await route.fulfill({ json: { success: true, result: { items: [INACTIVE_SERVICE] } } });
      return;
    }
    if (path === '/api/v1/rbac-v1/endpoints') {
      await route.fulfill({ json: { success: true, result: { items: state.endpoints, total: state.endpoints.length, page: 1, page_size: 100 } } });
      return;
    }
    if (path === `/api/v1/rbac-v1/endpoints/${STALE_ENDPOINT.id}` && request.method() === 'DELETE') {
      state.deletes.push(path + url.search);
      state.endpoints = state.endpoints.filter(endpoint => endpoint.id !== STALE_ENDPOINT.id);
      await route.fulfill({ json: { success: true, result: null } });
      return;
    }
    await route.fulfill({ json: { success: true, result: { items: [] } } });
  });
  return state;
}

const actionButton = (page: Page, name: string): Locator =>
  page.locator('.rbac-page-header .t-button').filter({ hasText: name });

test.use({ viewport: { width: 1440, height: 900 } });

test('失效关联服务仍展示端点，仅允许手动删除', async ({ page }) => {
  const state = await mockInactiveService(page);
  await page.goto('/api-resources');

  const table = page.locator('.resource-apis-table');
  const row = page.getByRole('row').filter({ hasText: '查询历史订单' }).first();
  await expect(table).toBeVisible();
  await expect(row).toContainText('orders-service');
  await expect(row).toContainText('失效');

  await expect(actionButton(page, '新增 API')).toHaveClass(/t-is-disabled/);
  await expect(actionButton(page, '导入 Swagger')).toHaveClass(/t-is-disabled/);
  await expect(row.locator('.t-button').filter({ hasText: '编辑' })).toHaveClass(/t-is-disabled/);

  await page.locator('.page-filter-bar .t-form__item').filter({ hasText: '服务' }).locator('.t-select').click();
  await expect(page.locator('.t-select-option').filter({ hasText: '订单服务 · orders-service（失效）' })).toBeVisible();

  await row.getByRole('button', { name: '删除', exact: true }).click();
  const dialog = page.locator('.t-dialog').filter({ hasText: '删除 API 接口' });
  await expect(dialog).toBeVisible();
  await dialog.getByRole('button', { name: '确认删除', exact: true }).click();

  await expect.poll(() => state.deletes.length).toBe(1);
  expect(state.deletes[0]).toBe('/api/v1/rbac-v1/endpoints/ep-order-history?app_key=console&service_resource=orders-service');
  await expect(page.getByRole('row').filter({ hasText: '查询历史订单' })).toHaveCount(0);
});
