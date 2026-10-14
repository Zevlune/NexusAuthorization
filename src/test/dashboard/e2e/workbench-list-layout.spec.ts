import { createRequire } from 'node:module';
import type { Locator, Page } from '../../../../dashboard/node_modules/@playwright/test';

// Resolve Playwright from the Dashboard package while the suite lives at the repository root.
const requireDashboardDependency = createRequire(new URL('../../../../dashboard/package.json', import.meta.url));
const { expect, test } = requireDashboardDependency('@playwright/test') as typeof import('../../../../dashboard/node_modules/@playwright/test');

// Shared list layout for the three Workbench list pages (菜单与按钮 / 角色管理 / API 接口), which follow the API page:
//   - the primary create action lives in .rbac-page-topline on the same row as the breadcrumb (Page actions)
//   - the search filters are their own .page-filter-bar Card / block, separate from the table Card
// Every /api/** call is mocked below, so no real backend is contacted.

const APP_KEY = 'console';

const MENUS = [
  { id: 'menu-orders', parent_id: null, code: 'orders', name: '订单管理', menu_type: 'menu', route: '/orders', component: '', icon: '', sort_order: 1, enabled: true },
  { id: 'btn-orders-query', parent_id: 'menu-orders', code: 'orders:query', name: '查询订单', menu_type: 'button', route: '', component: '', icon: '', sort_order: 1, enabled: true },
];

const ROLES = [
  { id: 'role-admin', code: 'admin', name: '管理员', description: '默认角色', enabled: true, endpoint_ids: [], menu_ids: [] },
];

const SERVICE_RESOURCES = [
  { id: 'svc-orders', key: 'orders-service', name: 'orders-service', displayName: '订单平台', audience: 'orders.api', description: '订单服务', isActive: true, source: 'nexusauth' as const },
];

const ENDPOINTS = [
  { id: 'ep-orders-list', service_resource: 'orders-service', endpoint_key: 'GET:/v1/orders', method: 'GET', path_template: '/v1/orders', module: '订单', summary: '查询订单', source: 'swagger' as const, enabled: true },
];

async function mockWorkbench(page: Page) {
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
      await route.fulfill({ json: { success: true, result: { items: ENDPOINTS, total: ENDPOINTS.length, page: 1, page_size: 100 } } });
      return;
    }
    if (path === '/api/v1/rbac-v1/menus') {
      await route.fulfill({ json: { success: true, result: { items: MENUS } } });
      return;
    }
    if (path === '/api/v1/rbac-v1/roles') {
      await route.fulfill({ json: { success: true, result: { items: ROLES } } });
      return;
    }
    await route.fulfill({ json: { success: true, result: { items: [] } } });
  });
}

const topline = (page: Page): Locator => page.locator('.rbac-page-topline');
const breadcrumb = (page: Page): Locator => topline(page).locator('.rbac-breadcrumb');
const filterBar = (page: Page): Locator => page.locator('.page-filter-bar');

async function expectSameRow(page: Page, left: Locator, right: Locator) {
  const leftBox = await left.boundingBox();
  const rightBox = await right.boundingBox();
  expect(leftBox).not.toBeNull();
  expect(rightBox).not.toBeNull();
  if (!leftBox || !rightBox) return;
  const overlap = Math.min(leftBox.y + leftBox.height, rightBox.y + rightBox.height) - Math.max(leftBox.y, rightBox.y);
  expect(overlap).toBeGreaterThan(0);
}

// The create action sits in the topline on the breadcrumb row, and the filter bar is a sibling of the table card.
async function expectListLayout(page: Page, addLabel: string, tableCard: Locator, table: Locator) {
  await expect(breadcrumb(page)).toBeVisible();
  const add = topline(page).getByRole('button', { name: addLabel, exact: true });
  await expect(add).toBeVisible();
  await expectSameRow(page, breadcrumb(page), add);

  await expect(filterBar(page)).toBeVisible();
  await expect(tableCard).toBeVisible();
  await expect(table).toBeVisible();
  await expect(tableCard.locator('.page-filter-bar')).toHaveCount(0);
  await expect(filterBar(page).locator('.t-table').first()).toHaveCount(0);
}

test.use({ viewport: { width: 1440, height: 900 } });

test('菜单与按钮：顶部添加与面包屑同行，搜索卡片与表格卡片分离', async ({ page }) => {
  await mockWorkbench(page);
  await page.goto('/menus');

  await expect(breadcrumb(page)).toContainText('菜单与按钮');
  await expectListLayout(page, '添加', page.locator('.resource-menus-panel'), page.locator('.resource-menus-table'));
  // The type select keeps the resource-menus-type-filter selector and adds the shared workbench-select width.
  await expect(filterBar(page).locator('.resource-menus-type-filter.workbench-select')).toHaveCount(1);
  await expect(filterBar(page).locator('.workbench-keyword')).toHaveCount(1);
});

test('角色管理：顶部新增角色与面包屑同行，搜索卡片与表格卡片分离', async ({ page }) => {
  await mockWorkbench(page);
  await page.goto('/roles');

  await expect(breadcrumb(page)).toContainText('角色管理');
  await expectListLayout(page, '新增角色', page.locator('.role-management-panel'), page.locator('.role-management-panel .t-table'));
  await expect(page.locator('.role-management-footer')).toContainText('共 1 个角色');
});

test('API 接口：顶部新增 API 与面包屑同行，搜索卡片与表格卡片分离', async ({ page }) => {
  await mockWorkbench(page);
  await page.goto('/api-resources');

  await expect(breadcrumb(page)).toContainText('API 接口');
  const tableCard = page.locator('.resource-apis-card').filter({ has: page.locator('.resource-apis-table') });
  await expectListLayout(page, '新增 API', tableCard, page.locator('.resource-apis-table'));
});
