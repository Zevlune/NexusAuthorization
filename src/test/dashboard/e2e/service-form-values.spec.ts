import { createRequire } from 'node:module';
import type { Locator, Page } from '../../../../dashboard/node_modules/@playwright/test';

// Resolve Playwright from the Dashboard package while the suite lives at the repository root.
const requireDashboardDependency = createRequire(new URL('../../../../dashboard/package.json', import.meta.url));
const { expect, test } = requireDashboardDependency('@playwright/test') as typeof import('../../../../dashboard/node_modules/@playwright/test');

// Regression for the local-service edit drawer in dashboard/src/pages/policy-workbench/components/services.tsx.
// TDesign 1.16.9 FormItem clones its direct child and overwrites the controlled value with its own empty
// formValue (even without a name), so every externally controlled Input/Select/Switch/Textarea is wrapped in a
// React Fragment. This suite proves the drawer echoes original values, keeps the switch controlled, and that
// saving only the description preserves the other fields.

const APP_KEY = 'console';

const LOCAL_SERVICE = {
  id: 'svc-orders',
  key: 'orders-service',
  name: 'orders-service',
  display_name: '订单平台',
  audience: 'orders.api',
  description: '订单服务',
  is_active: false,
  source: 'local' as const,
};

type SavePayload = { display_name: string; audience: string; description: string; is_active: boolean };

async function mockWorkbench(page: Page): Promise<{ saves: SavePayload[] }> {
  const saves: SavePayload[] = [];
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
      await route.fulfill({ json: { success: true, result: { items: [LOCAL_SERVICE], writable: true } } });
      return;
    }
    if (path === `/api/v1/service-resources/${LOCAL_SERVICE.key}` && request.method() === 'PUT') {
      saves.push(JSON.parse(request.postData() || '{}'));
      await route.fulfill({ json: { success: true, result: LOCAL_SERVICE } });
      return;
    }
    if (path === `/api/v1/applications/${APP_KEY}/service-resources`) {
      await route.fulfill({ json: { success: true, result: { items: [] } } });
      return;
    }
    await route.fulfill({ json: { success: true, result: { items: [], total: 0, page: 1, page_size: 100 } } });
  });
  return { saves };
}

const drawer = (page: Page): Locator => page.locator('.resource-services-drawer');
const textFields = (page: Page): Locator => drawer(page).locator('.t-input__inner');
const descriptionField = (page: Page): Locator => drawer(page).locator('.t-textarea__inner');
const activeSwitch = (page: Page): Locator => drawer(page).locator('.t-switch');

test.use({ viewport: { width: 1440, height: 900 } });

test('本地服务编辑抽屉回显原值与开关，只改描述保存时保留其它字段', async ({ page }) => {
  const state = await mockWorkbench(page);
  await page.goto('/service-resources');

  const table = page.locator('.resource-services-table');
  await expect(table).toBeVisible();
  await expect(table).toContainText('订单平台');

  await table.getByRole('button', { name: '编辑', exact: true }).first().click();

  const panel = drawer(page);
  await expect(panel).toBeVisible();

  // 原值回显：名称 / 标识 / Audience / 描述，未被 FormItem 覆盖为空。
  await expect(textFields(page).nth(0)).toHaveValue('订单平台');
  await expect(textFields(page).nth(1)).toHaveValue('orders-service');
  await expect(textFields(page).nth(2)).toHaveValue('orders.api');
  await expect(descriptionField(page)).toHaveValue('订单服务');

  // 开关与原值一致（is_active=false => 未选中），且点击后受控切换。
  const toggle = activeSwitch(page);
  await expect(toggle).not.toHaveClass(/t-is-checked/);
  await toggle.click();
  await expect(toggle).toHaveClass(/t-is-checked/);
  await toggle.click();
  await expect(toggle).not.toHaveClass(/t-is-checked/);

  // 只改描述，其它字段保持原值。
  await descriptionField(page).fill('订单服务（更新描述）');
  await panel.getByRole('button', { name: '保存修改', exact: true }).click();

  await expect.poll(() => state.saves.length).toBe(1);
  expect(state.saves[0]).toEqual({
    display_name: '订单平台',
    audience: 'orders.api',
    description: '订单服务（更新描述）',
    is_active: false,
  });
  // TDesign keeps the drawer node in the DOM after closing; the open modifier is the reliable signal.
  await expect(panel).not.toHaveClass(/t-drawer--open/);
});
