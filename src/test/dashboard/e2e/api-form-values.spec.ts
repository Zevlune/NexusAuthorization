import { createRequire } from 'node:module';
import type { Locator, Page } from '../../../../dashboard/node_modules/@playwright/test';

// Resolve Playwright from the Dashboard package while the suite lives at the repository root.
const requireDashboardDependency = createRequire(new URL('../../../../dashboard/package.json', import.meta.url));
const { expect, test } = requireDashboardDependency('@playwright/test') as typeof import('../../../../dashboard/node_modules/@playwright/test');

// Regression scope for dashboard/src/pages/policy-workbench/components/apis.tsx.
//
// tdesign 1.16.9 FormItem clones every custom child and injects `value: formValue`
// (es/form/FormItem.js), which resets an externally controlled Input/Select/Switch to empty
// when the FormItem has no `name`. Wrapping the child in a React Fragment makes FormItem return
// it untouched, so the edit drawer must now echo the stored record and submit it back intact.
//
// Every /api/** call is mocked below, so no real authorization data or database is touched.

const APP_KEY = 'console';

const SERVICE_RESOURCES = [
  { id: 'svc-orders', key: 'orders-service', name: 'orders-service', displayName: '订单平台', audience: 'orders.api', description: '订单服务', isActive: true, source: 'nexusauth' },
];

const EXISTING_ENDPOINT = {
  id: 'ep-orders-detail',
  service_resource: 'orders-service',
  endpoint_key: 'orders.detail',
  method: 'PUT',
  path_template: '/v1/orders/{id}',
  module: '订单中心',
  summary: '更新订单',
  source: 'swagger',
  enabled: true,
};

type SavedEndpoint = Record<string, unknown>;

async function mockAPIs(page: Page): Promise<{ saved: SavedEndpoint[] }> {
  const state = { saved: [] as SavedEndpoint[] };
  await page.addInitScript(() => sessionStorage.setItem('nexus-authorization-application', 'console'));
  await page.route('**/api/**', async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (!path.startsWith('/api/')) { await route.fallback(); return; }

    if (path === '/api/auth/me') {
      await route.fulfill({ json: { success: true, result: { isAuthenticated: true, user: { sub: 'admin', name: '管理员' } } } });
      return;
    }
    if (path === `/api/v1/applications/${APP_KEY}/service-resources`) {
      await route.fulfill({ json: { success: true, result: { items: SERVICE_RESOURCES } } });
      return;
    }
    if (path === '/api/v1/rbac-v1/endpoints') {
      if (request.method() === 'POST') {
        state.saved.push(request.postDataJSON() as SavedEndpoint);
        await route.fulfill({ json: { success: true, result: EXISTING_ENDPOINT } });
        return;
      }
      await route.fulfill({ json: { success: true, result: { items: [EXISTING_ENDPOINT], total: 1, page: 1, page_size: 100 } } });
      return;
    }
    await route.fulfill({ json: { success: true, result: { items: [] } } });
  });
  return state;
}

const editDrawer = (page: Page): Locator => page.locator('.resource-apis-drawer:visible');
// The Select renders the chosen option label into its inner input; scope by the FormItem label.
const selectInput = (page: Page, label: string): Locator =>
  editDrawer(page).locator('.t-form__item').filter({ hasText: label }).locator('input');

test.use({ viewport: { width: 1440, height: 900 } });

test('API 编辑回显已有记录且保存仅改动摘要', async ({ page }) => {
  const state = await mockAPIs(page);
  await page.goto('/api-resources');

  const row = page.locator('.resource-apis-table tbody tr').filter({ hasText: '/v1/orders/{id}' });
  await expect(row).toBeVisible();
  await row.getByRole('button', { name: '编辑', exact: true }).click();

  // 已有记录逐字段回显：修复前 FormItem 会把 value 覆盖为空，这里会全部拿到空值。
  await expect(page.getByPlaceholder('例如 orders.list')).toHaveValue('orders.detail');
  await expect(page.getByPlaceholder('用于列表展示和权限说明')).toHaveValue('更新订单');
  await expect(page.getByPlaceholder('例如 /v1/users/{id}')).toHaveValue('/v1/orders/{id}');
  await expect(page.getByPlaceholder('例如用户中心')).toHaveValue('订单中心');
  await expect(selectInput(page, '目标服务')).toHaveValue('订单平台 · orders-service');
  await expect(selectInput(page, 'HTTP 方法')).toHaveValue('PUT');
  await expect(page.getByRole('switch', { name: '启用接口' })).toHaveClass(/t-is-checked/);

  // 仅修改摘要后保存，其余字段必须原样提交。
  await page.getByPlaceholder('用于列表展示和权限说明').fill('更新订单说明');
  await page.getByRole('button', { name: '保存', exact: true }).click();

  await expect(editDrawer(page)).toBeHidden();
  await expect.poll(() => state.saved.length).toBe(1);
  expect(state.saved[0]).toEqual({
    id: 'ep-orders-detail',
    endpoint_key: 'orders.detail',
    method: 'PUT',
    path_template: '/v1/orders/{id}',
    module: '订单中心',
    summary: '更新订单说明',
    enabled: true,
  });
});
