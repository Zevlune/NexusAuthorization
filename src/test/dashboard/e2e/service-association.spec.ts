import { createRequire } from 'node:module';
import type { Page } from '../../../../dashboard/node_modules/@playwright/test';

const requireDashboardDependency = createRequire(new URL('../../../../dashboard/package.json', import.meta.url));
const { expect, test } = requireDashboardDependency('@playwright/test') as typeof import('../../../../dashboard/node_modules/@playwright/test');

// Regression for the service association buttons in policy-workbench/components/services.tsx.
// PUT /applications/{key}/service-resources replaces the whole set, so the page must never build that
// set from stale or empty page state: it waits for the workbench data and merges into the latest links.

const APP_KEY = 'console';
const service = (key: string, name: string) => ({ id: key, key, name, display_name: name, audience: key, description: '', is_active: true, source: 'local' as const });
const CATALOG = [service('orders', '订单服务'), service('billing', '账单服务'), service('inventory', '库存服务')];

type Options = { failRoles?: boolean; links: string[] };

async function mockWorkbench(page: Page, options: Options) {
  const puts: string[][] = [];
  const state = { links: [...options.links] };
  await page.addInitScript(() => sessionStorage.setItem('nexus-authorization-application', 'console'));
  await page.route('**/api/**', async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (!path.startsWith('/api/')) { await route.fallback(); return; }
    if (path === '/api/auth/me') {
      await route.fulfill({ json: { success: true, result: { isAuthenticated: true, user: { sub: 'admin', name: '管理员' } } } });
      return;
    }
    if (path === '/api/v1/service-resources') {
      await route.fulfill({ json: { success: true, result: { items: CATALOG, writable: true } } });
      return;
    }
    if (path === `/api/v1/applications/${APP_KEY}/service-resources`) {
      if (request.method() === 'PUT') {
        const keys = (request.postDataJSON() as { service_resource_keys: string[] }).service_resource_keys;
        puts.push(keys);
        state.links = keys;
        await route.fulfill({ json: { success: true, result: null } });
        return;
      }
      await route.fulfill({ json: { success: true, result: { items: CATALOG.filter(item => state.links.includes(item.key)) } } });
      return;
    }
    if (path === '/api/v1/rbac-v1/roles' && options.failRoles) {
      await route.fulfill({ status: 500, json: { success: false, errorCode: 'INTERNAL_ERROR', errorMessage: 'internal server error' } });
      return;
    }
    await route.fulfill({ json: { success: true, result: { items: [], total: 0, page: 1, page_size: 100 } } });
  });
  return { puts, state };
}

const rowFor = (page: Page, name: string) => page.locator('.resource-services-table tr').filter({ hasText: name }).first();

test.use({ viewport: { width: 1440, height: 900 } });

test('工作台数据加载失败时禁用关联操作，避免整组替换删除其他关联', async ({ page }) => {
  const mock = await mockWorkbench(page, { failRoles: true, links: ['orders'] });
  await page.goto('/service-resources');
  await expect(page.locator('.resource-services-table')).toContainText('账单服务');
  await expect(page.locator('.rbac-load-error')).toBeVisible();
  // Orders is linked but reads as unlinked here: a click would have replaced the set with [billing].
  await expect(rowFor(page, '订单服务')).toContainText('未关联');
  // TDesign renders a disabled Button as a <div class="t-is-disabled"> without a click handler.
  const link = rowFor(page, '账单服务').locator('.t-button').filter({ hasText: /^关联$/ });
  await expect(link).toHaveClass(/t-is-disabled/);
  await link.click({ force: true });
  expect(mock.puts).toEqual([]);
});

test('关联时合并最新已保存关联，保留他人在加载后新增的服务', async ({ page }) => {
  const mock = await mockWorkbench(page, { links: ['orders'] });
  await page.goto('/service-resources');
  const billing = rowFor(page, '账单服务').getByRole('button', { name: '关联', exact: true });
  await expect(billing).toBeEnabled();

  // Another administrator links inventory after this page loaded.
  mock.state.links = ['orders', 'inventory'];
  await billing.click();

  await expect.poll(() => mock.puts.length).toBe(1);
  expect([...mock.puts[0]].sort()).toEqual(['billing', 'inventory', 'orders']);
});
