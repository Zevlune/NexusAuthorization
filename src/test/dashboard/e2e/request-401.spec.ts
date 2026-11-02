import { createRequire } from 'node:module';
import type { Page } from '../../../../dashboard/node_modules/@playwright/test';

const requireDashboardDependency = createRequire(new URL('../../../../dashboard/package.json', import.meta.url));
const { expect, test } = requireDashboardDependency('@playwright/test') as typeof import('../../../../dashboard/node_modules/@playwright/test');

const APPLICATION_STORAGE_KEY = 'nexus-authorization-application';
const APP = { id: 'app-1', key: 'console', display_name: '控制台', description: '', url: '', enabled: true };

async function mockDashboard(page: Page, initialAuthentication = true) {
  let authenticated = initialAuthentication;

  await page.addInitScript(({ key }) => {
    sessionStorage.setItem(key, 'console');
  }, { key: APPLICATION_STORAGE_KEY });

  await page.route('**/api/**', async route => {
    const path = new URL(route.request().url()).pathname;
    if (!path.startsWith('/api/')) {
      await route.fallback();
      return;
    }

    if (path === '/api/auth/me') {
      await route.fulfill({
        json: {
          success: true,
          result: {
            isAuthenticated: authenticated,
            user: { sub: 'admin', name: '管理员' },
          },
        },
      });
      return;
    }

    if (path === '/api/v1/applications') {
      await route.fulfill({ json: { success: true, result: { items: [APP] } } });
      return;
    }

    await route.fulfill({ json: { success: true, result: { items: [], total: 0, page: 1, page_size: 100 } } });
  });

  return {
    expireSession() {
      authenticated = false;
    },
  };
}

test('ApiResult 形式的 HTTP 401 先跳转登录并保留完整的当前地址', async ({ page }) => {
  const dashboard = await mockDashboard(page);
  await page.route('**/api/v1/rbac-v1/roles**', route => {
    dashboard.expireSession();
    return route.fulfill({
      status: 401,
      json: {
        success: false,
        errorCode: 'UNAUTHENTICATED',
        errorMessage: '未登录',
        result: null,
      },
    });
  });

  await page.goto('/roles?tab=members#assignments');

  await expect.poll(() => new URL(page.url()).pathname).toBe('/login');
  const loginUrl = new URL(page.url());
  expect(loginUrl.searchParams.get('redirect')).toBe('/roles?tab=members#assignments');
});

test('/auth/ 下的 HTTP 401 不触发登录跳转，但仍保留 ApiResult 错误', async ({ page }) => {
  await mockDashboard(page, false);
  await page.route('**/api/auth/config', route => route.fulfill({
    status: 401,
    json: {
      success: false,
      errorCode: 'UNAUTHENTICATED',
      errorMessage: '未登录',
      result: null,
    },
  }));

  await page.goto('/login?redirect=%2Froles');
  await expect(page.getByRole('button', { name: '统一登录' })).toBeVisible();

  const result = await page.evaluate(async () => {
    const { default: request } = await import('/src/api/request.ts');
    try {
      await request.get('/auth/config');
      return { status: 0, message: '' };
    } catch (cause) {
      const error = cause as {
        response?: {
          status?: number;
          data?: { message?: string };
        };
      };
      return {
        status: error.response?.status ?? 0,
        message: error.response?.data?.message ?? '',
      };
    }
  });

  expect(result).toEqual({ status: 401, message: '未登录' });
  expect(new URL(page.url()).pathname).toBe('/login');
  expect(new URL(page.url()).searchParams.get('redirect')).toBe('/roles');
});

test('登录目标校验保留合法地址中的 query 和 hash', async ({ page }) => {
  await mockDashboard(page, false);
  await page.route('**/api/auth/login', route => route.fulfill({
    json: { success: true, result: { authorizeUrl: '' } },
  }));

  const redirect = '/roles?tab=members#assignments';
  await page.goto(`/login?redirect=${encodeURIComponent(redirect)}`);
  await page.getByRole('button', { name: '统一登录' }).click();

  await expect.poll(async () => page.evaluate(async () => {
    const { consumeLoginRedirect } = await import('/src/router/auth.tsx');
    return consumeLoginRedirect();
  })).toBe(redirect);
});
