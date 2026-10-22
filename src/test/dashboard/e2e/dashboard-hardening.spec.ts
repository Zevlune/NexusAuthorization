import { createRequire } from 'node:module';
import type { Page } from '../../../../dashboard/node_modules/@playwright/test';

const requireDashboardDependency = createRequire(new URL('../../../../dashboard/package.json', import.meta.url));
const { expect, test } = requireDashboardDependency('@playwright/test') as typeof import('../../../../dashboard/node_modules/@playwright/test');

// Regressions for dashboard hardening: post-login redirects stay on this site, an expired session
// returns to the current page, route load failures are not reported as 404, page switches keep the
// loaded data, and destructive confirmations ignore repeated clicks.

const APP = { id: 'app-1', key: 'console', display_name: '控制台', description: '', url: '', enabled: true };
const ROLE = { id: 'reader-role', code: 'reader', name: '读者', description: '', enabled: true, endpoint_ids: [], menu_ids: [] };

async function mockAPI(page: Page, options: { selectApplication?: boolean } = {}) {
  const deletes: string[] = [];
  if (options.selectApplication !== false) {
    await page.addInitScript(() => sessionStorage.setItem('nexus-authorization-application', 'console'));
  }
  await page.route('**/api/**', async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (!path.startsWith('/api/')) { await route.fallback(); return; }
    if (path === '/api/auth/me') {
      await route.fulfill({ json: { success: true, result: { isAuthenticated: true, user: { sub: 'admin', name: '管理员' } } } });
      return;
    }
    if (path === '/api/v1/applications') {
      await route.fulfill({ json: { success: true, result: { items: [APP] } } });
      return;
    }
    if (path === '/api/v1/rbac-v1/roles') {
      await route.fulfill({ json: { success: true, result: { items: [ROLE] } } });
      return;
    }
    if (path === `/api/v1/rbac-v1/roles/${ROLE.id}` && request.method() === 'DELETE') {
      deletes.push(path);
      // Slow enough for a second click to land while the first delete is in flight.
      await new Promise(resolve => setTimeout(resolve, 800));
      await route.fulfill({ json: { success: true, result: null } });
      return;
    }
    await route.fulfill({ json: { success: true, result: { items: [], total: 0, page: 1, page_size: 100 } } });
  });
  return { deletes };
}

test('选择应用后不会跳转到其他站点：反斜杠与控制字符形式的回跳地址被拒绝', async ({ page }) => {
  await mockAPI(page, { selectApplication: false });
  // Browsers treat "\" as "/" and strip tabs, so both would otherwise leave for evil.example.
  for (const redirect of ['/\\evil.example', '/\t/evil.example']) {
    await page.goto(`/select-application?redirect=${encodeURIComponent(redirect)}`);
    await page.locator('.application-card').filter({ hasText: '控制台' }).first().click();
    await page.getByRole('button', { name: '进入权限配置' }).click();
    await expect(page).toHaveURL(/^http:\/\/localhost:5275\/dashboard$/);
  }
  // A percent-encoded backslash is an ordinary path on this site, never another origin.
  await page.goto(`/select-application?redirect=${encodeURIComponent('/%5Cevil.example')}`);
  await page.locator('.application-card').filter({ hasText: '控制台' }).first().click();
  await page.getByRole('button', { name: '进入权限配置' }).click();
  await expect(page).toHaveURL(/^http:\/\/localhost:5275\//);
  // A path on this site is still honoured.
  await page.goto(`/select-application?redirect=${encodeURIComponent('/roles')}`);
  await page.locator('.application-card').filter({ hasText: '控制台' }).first().click();
  await page.getByRole('button', { name: '进入权限配置' }).click();
  await expect(page).toHaveURL(/\/roles$/);
});

test('会话过期（401）时跳转登录并带上当前页面作为回跳地址', async ({ page }) => {
  await mockAPI(page);
  await page.route('**/api/v1/rbac-v1/roles**', route => route.fulfill({ status: 401, json: { error: 'authentication required' } }));
  await page.goto('/roles');
  await expect(page).toHaveURL(/\/login\?redirect=%2Froles$/);
});

test('页面模块加载失败显示「页面加载失败」而不是 404', async ({ page }) => {
  await mockAPI(page);
  await page.route('**/src/pages/policy-workbench/index.tsx*', route => route.abort());
  await page.goto('/roles');
  await expect(page.getByText('页面加载失败')).toBeVisible();
  await expect(page.getByRole('button', { name: '刷新页面' })).toBeVisible();
  await expect(page.getByText('对不起，你访问的页面不存在。')).toHaveCount(0);
});

// Guard: every workbench route resolves to the same page component, which React keeps mounted
// across route changes, so switching pages must not reload the whole permission data set.
test('在工作台页面之间切换不会重新加载全部权限数据', async ({ page }) => {
  await mockAPI(page);
  let serviceLoads = 0;
  page.on('request', request => {
    if (request.method() === 'GET' && new URL(request.url()).pathname === '/api/v1/applications/console/service-resources') serviceLoads += 1;
  });
  await page.goto('/dashboard');
  await expect.poll(() => serviceLoads).toBe(1);
  for (const name of ['角色管理', '菜单与按钮', '配置总览']) {
    await page.locator('.t-menu__item').filter({ hasText: name }).first().click();
    await expect(page.locator('.t-menu__item.t-is-active').filter({ hasText: name })).toHaveCount(1);
  }
  expect(serviceLoads).toBe(1);
});

test('删除角色时重复点击确认只发送一次删除请求', async ({ page }) => {
  const mock = await mockAPI(page);
  await page.goto('/roles');
  await page.getByRole('row').filter({ hasText: '读者' }).first().getByRole('button', { name: '删除' }).click();
  const confirm = page.locator('.t-dialog').getByRole('button', { name: '确认删除' });
  await confirm.click();
  await confirm.click({ force: true }).catch(() => undefined);
  await confirm.click({ force: true }).catch(() => undefined);
  await expect(page.locator('.t-dialog')).toHaveCount(0, { timeout: 5000 });
  expect(mock.deletes).toHaveLength(1);
});
