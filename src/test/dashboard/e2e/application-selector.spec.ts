import { createRequire } from 'node:module';
import type { Page } from '../../../../dashboard/node_modules/@playwright/test';

const requireDashboardDependency = createRequire(new URL('../../../../dashboard/package.json', import.meta.url));
const { expect, test } = requireDashboardDependency('@playwright/test') as typeof import('../../../../dashboard/node_modules/@playwright/test');

test('停用应用不能进入，但仍可编辑并重新启用', async ({ page }: { page: Page }) => {
  const application = { key: 'disabled-app', display_name: '停用验收应用', description: '保留应用说明', url: 'https://example.com', enabled: false };
  let updates = 0;
  await page.route('**/api/**', async route => {
    const pathname = new URL(route.request().url()).pathname;
    if (!pathname.startsWith('/api/')) return route.fallback();
    if (pathname === '/api/auth/me') {
      return route.fulfill({ json: { success: true, result: { isAuthenticated: true, user: { sub: 'admin', name: '管理员' } } } });
    }
    if (pathname === '/api/v1/applications') {
      return route.fulfill({ json: { success: true, result: { items: [application] } } });
    }
    if (pathname === '/api/v1/applications/disabled-app' && route.request().method() === 'PUT') {
      Object.assign(application, route.request().postDataJSON());
      updates += 1;
      return route.fulfill({ json: { success: true, result: application } });
    }
    return route.fulfill({ json: { success: true, result: { items: [] } } });
  });
  await page.goto('/select-application');
  const card = page.getByRole('group', { name: '选择应用：停用验收应用', exact: true });
  await expect(card.getByRole('radio')).toBeDisabled();
  await expect(page.locator('.application-selection-bar .t-button')).toHaveClass(/t-is-disabled/);
  await expect(card.getByRole('button', { name: '编辑', exact: true })).toBeEnabled();
  await card.getByRole('button', { name: '编辑', exact: true }).click();
  await expect(page.getByPlaceholder('例如：业务管理平台')).toHaveValue('停用验收应用');
  await expect(page.getByPlaceholder('例如：business.portal')).toBeDisabled();
  await expect(page.getByPlaceholder('例如：business.portal')).toHaveValue('disabled-app');
  await expect(page.getByPlaceholder('例如：https://portal.example.com')).toHaveValue('https://example.com');
  await expect(page.getByPlaceholder('简要说明应用用途（选填）')).toHaveValue('保留应用说明');
  await expect(page.getByRole('switch', { name: '启用应用', exact: true })).not.toHaveClass(/t-is-checked/);
  await page.getByRole('switch', { name: '启用应用', exact: true }).click();
  await page.getByRole('button', { name: '保存修改', exact: true }).click();
  await expect(page.getByRole('switch', { name: '启用应用', exact: true })).toBeHidden();
  await expect.poll(() => updates).toBe(1);
  const enabledCard = page.getByRole('button', { name: '选择应用：停用验收应用', exact: true });
  await enabledCard.click();
  await expect(page.getByRole('button', { name: '进入权限配置', exact: true })).toBeEnabled();
});
