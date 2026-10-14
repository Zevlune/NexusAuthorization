import { createRequire } from 'node:module';
import type { Page } from '../../../../dashboard/node_modules/@playwright/test';

const requireDashboardDependency = createRequire(new URL('../../../../dashboard/package.json', import.meta.url));
const { expect, test } = requireDashboardDependency('@playwright/test') as typeof import('../../../../dashboard/node_modules/@playwright/test');

test('权限模拟显示默认服务、global 和 GET，并发送界面显示的值', async ({ page }: { page: Page }) => {
  let simulated: Record<string, string> | undefined;
  await page.addInitScript(() => sessionStorage.setItem('nexus-authorization-application', 'console'));
  await page.route('**/api/**', async route => {
    const pathname = new URL(route.request().url()).pathname;
    if (!pathname.startsWith('/api/')) return route.fallback();
    if (pathname === '/api/auth/me') {
      return route.fulfill({ json: { success: true, result: { isAuthenticated: true, user: { sub: 'admin' } } } });
    }
    if (pathname === '/api/v1/applications/console/service-resources') {
      return route.fulfill({ json: { success: true, result: { items: [{ key: 'orders-api', display_name: '订单服务', is_active: true, source: 'local' }] } } });
    }
    if (pathname === '/api/v1/rbac-v1/simulate') {
      simulated = route.request().postDataJSON();
      return route.fulfill({ json: { success: true, result: { allowed: true, reason: 'allowed', release_version: 1 } } });
    }
    return route.fulfill({ json: { success: true, result: { items: [] } } });
  });
  await page.goto('/simulate');
  const inputs = page.getByRole('textbox', { name: '请输入', exact: true });
  const selects = page.getByRole('textbox', { name: '请选择', exact: true });
  await expect(selects.nth(0)).toHaveValue('订单服务 · orders-api');
  await expect(inputs.nth(1)).toHaveValue('global');
  await expect(selects.nth(1)).toHaveValue('GET');
  await inputs.nth(0).fill('test-user');
  await inputs.nth(2).fill('/orders/123');
  await page.getByRole('button', { name: '运行测试', exact: true }).click();
  await expect(page.getByRole('row', { name: 'allowed true', exact: true })).toBeVisible();
  expect(simulated).toEqual({ service_resource: 'orders-api', subject: 'test-user', domain: 'global', method: 'GET', path: '/orders/123' });
});
