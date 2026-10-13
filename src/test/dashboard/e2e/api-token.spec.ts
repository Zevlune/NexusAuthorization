import { createRequire } from 'node:module';
import type { Page, Route } from '../../../../dashboard/node_modules/@playwright/test';

const requireDashboardDependency = createRequire(new URL('../../../../dashboard/package.json', import.meta.url));
const { expect, test } = requireDashboardDependency('@playwright/test') as typeof import('../../../../dashboard/node_modules/@playwright/test');

const APP_KEY = 'api-token-e2e';
const APP_STORAGE_KEY = 'permission-center-application';
const endpoint = { id: 'endpoint-read', service_resource: 'orders', endpoint_key: 'orders.read', method: 'GET', path_template: '/v1/orders', module: 'orders', summary: '查询订单', source: 'manual', enabled: true };
const writeEndpoint = { id: 'endpoint-write', service_resource: 'orders', endpoint_key: 'orders.write', method: 'POST', path_template: '/v1/orders', module: 'orders', summary: '创建订单', source: 'manual', enabled: true };
const service = { id: 'service-orders', key: 'orders', name: 'orders', display_name: '订单服务', audience: 'orders.api', description: '', is_active: true, source: 'local' };

type Permission = { endpoint_id: string; service_resource: string; action: 'read' | 'write'; tenant_id: string; project_id: string; method?: string; path_template?: string; summary?: string };
type Policy = { id: string; effect: 'allow'; service_resource: string; tenant_id: string; project_id: string; permission_group_ids: string[] };
type Group = { id: string; service_resource: string; code: string; name: string; action: 'read' | 'write'; enabled: boolean; endpoint_ids: string[]; created_at: string; updated_at: string };
type Token = {
  id: string; app_key: string; name: string; owner_type: 'user' | 'service'; owner_id: string; token_prefix: string; status: 'active' | 'disabled' | 'revoked' | 'expired'; expires_at: string | null; last_used_at: string | null; created_by: string; created_at: string; updated_at: string; revoked_at: string | null; permissions: Permission[]; policies: Policy[];
};
type MockState = { tokens: Token[]; groups: Group[]; requests: Array<{ method: string; path: string; body?: Record<string, unknown> }>; counter: number };

const wrapped = (result: unknown) => ({ success: true, errorCode: null, errorMessage: null, result });
const fulfill = (route: Route, result: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(wrapped(result)) });
const readPermission = (tenant = ''): Permission => ({ endpoint_id: endpoint.id, service_resource: endpoint.service_resource, action: 'read', tenant_id: tenant, project_id: '', method: endpoint.method, path_template: endpoint.path_template, summary: endpoint.summary });
const makeGroups = (): Group[] => [
  { id: 'group-orders-read', service_resource: 'orders', code: 'orders.read', name: '订单只读', action: 'read', enabled: true, endpoint_ids: [endpoint.id], created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
  { id: 'group-orders-write', service_resource: 'orders', code: 'orders.write', name: '订单写入', action: 'write', enabled: true, endpoint_ids: [endpoint.id, writeEndpoint.id], created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' },
];
const makeToken = (id: string, name: string, options: { permissions?: Permission[]; policies?: Policy[] } = {}): Token => ({
  id, app_key: APP_KEY, name, owner_type: 'service', owner_id: '', token_prefix: 'nxs_live_', status: 'active', expires_at: null, last_used_at: null, created_by: 'admin',
  created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', revoked_at: null, permissions: options.permissions ?? [], policies: options.policies ?? [],
});

const installMocks = async (page: Page): Promise<MockState> => {
  const state: MockState = { tokens: [makeToken('token-1', '旧 Token', { permissions: [readPermission()] })], groups: makeGroups(), requests: [], counter: 1 };
  await page.route('**/api/auth/me', route => fulfill(route, { isAuthenticated: true, user: { sub: 'admin', name: '管理员' } }));
  await page.route('**/api/v1/service-resources', route => fulfill(route, { items: [service], writable: true }));
  await page.route('**/api/v1/applications/*/service-resources', route => fulfill(route, { items: [service] }));
  await page.route('**/api/v1/rbac-v1/endpoints**', route => fulfill(route, { items: [endpoint, writeEndpoint], total: 2, page: 1, page_size: 100 }));
  await page.route('**/api/v1/rbac-v1/roles**', route => fulfill(route, { items: [] }));
  await page.route('**/api/v1/rbac-v1/menus**', route => fulfill(route, { items: [] }));
  await page.route('**/api/v1/rbac-v1/subject-roles**', route => fulfill(route, { items: [] }));
  await page.route('**/api/v1/api-permission-groups**', async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (request.method() === 'GET') { await fulfill(route, { items: state.groups }); return; }
    if (request.method() === 'DELETE') {
      const id = decodeURIComponent(path.split('/').pop() || '');
      state.groups = state.groups.filter(group => group.id !== id);
      await fulfill(route, null);
      return;
    }
    if (request.method() === 'POST') {
      const body = request.postDataJSON() as Group;
      state.groups = body.id ? state.groups.map(group => group.id === body.id ? body : group) : [...state.groups, { ...body, id: `group-${state.counter += 1}` }];
      await fulfill(route, body);
      return;
    }
    await fulfill(route, null, 404);
  });
  await page.route('**/api/v1/api-tokens**', async route => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const match = path.match(/^\/api\/v1\/api-tokens\/([^/]+)(?:\/(status|revoke|rotate))?$/);
    if (request.method() === 'GET' && path === '/api/v1/api-tokens') {
      const search = url.searchParams.get('search')?.toLowerCase() || '';
      const status = url.searchParams.get('status');
      const items = state.tokens.filter(token => (!search || token.name.toLowerCase().includes(search)) && (!status || token.status === status));
      await fulfill(route, { items, total: items.length, page: 1, page_size: 25 }); return;
    }
    if (request.method() === 'POST' && path === '/api/v1/api-tokens') {
      const body = request.postDataJSON() as Record<string, unknown>;
      state.counter += 1;
      const created = makeToken(`token-${state.counter}`, String(body.name), { permissions: (body.permissions as Permission[]) || [], policies: (body.policies as Policy[]) || [] });
      created.owner_type = (body.owner_type as 'user' | 'service') || 'service'; created.owner_id = body.owner_id ? String(body.owner_id) : ''; created.expires_at = (body.expires_at as string | null) ?? null;
      state.tokens = [created, ...state.tokens]; state.requests.push({ method: 'POST', path, body });
      await fulfill(route, { token: created, raw_token: 'nxs_live_full_secret_once' }, 201); return;
    }
    if (!match) { await fulfill(route, null, 404); return; }
    const token = state.tokens.find(item => item.id === match[1]);
    if (!token) { await fulfill(route, { message: 'not found' }, 404); return; }
    if (request.method() === 'GET') { await fulfill(route, token); return; }
    if (request.method() === 'PUT') {
      const body = request.postDataJSON() as Record<string, unknown>;
      token.name = String(body.name);
      if (body.permissions !== undefined) token.permissions = body.permissions as Permission[];
      if (body.policies !== undefined) token.policies = body.policies as Policy[];
      state.requests.push({ method: 'PUT', path, body });
      await fulfill(route, token); return;
    }
    if (request.method() === 'POST' && match[2] === 'status') { const body = request.postDataJSON() as { status: 'active' | 'disabled' }; token.status = body.status; await fulfill(route, token); return; }
    if (request.method() === 'POST' && match[2] === 'revoke') { token.status = 'revoked'; token.revoked_at = '2026-01-02T00:00:00Z'; await fulfill(route, token); return; }
    if (request.method() === 'POST' && match[2] === 'rotate') {
      if (token.status !== 'active' || (token.expires_at && Date.parse(token.expires_at) <= Date.now())) { await fulfill(route, { message: '仅 active 且未过期 Token 可以轮换' }, 409); return; }
      const rotateBody = request.postDataJSON() as { revoke_old?: boolean };
      if (rotateBody.revoke_old !== false) token.status = 'revoked';
      const replacement = makeToken(`token-${++state.counter}`, `${token.name} rotated`, { permissions: token.permissions, policies: token.policies });
      state.tokens = [replacement, ...state.tokens];
      await fulfill(route, { token: replacement, raw_token: 'nxs_live_rotated_secret_once' }); return;
    }
    await fulfill(route, null, 404);
  });
  await page.addInitScript(({ key, value }) => window.sessionStorage.setItem(key, value), { key: APP_STORAGE_KEY, value: APP_KEY });
  return state;
};

test.describe('API Token management', () => {
  test('legacy token keeps explicit interface permissions and owner fields', async ({ page }) => {
    const state = await installMocks(page);
    const original = state.tokens[0];
    original.permissions = ['tenant-a', 'tenant-b'].map(tenant => ({ ...readPermission(tenant), project_id: 'project-1' }));
    await page.goto('/api-tokens/token-1/permissions');
    await expect(page.getByRole('heading', { name: '维护权限策略' })).toBeVisible();
    await expect(page.getByText('该 Token 使用历史接口权限（permissions）', { exact: false })).toBeVisible();
    await expect(page.getByPlaceholder('例如：订单服务生产 Token')).toBeDisabled();
    await expect(page.getByPlaceholder('例如：订单服务生产 Token')).toHaveValue(original.name);
    await expect(page.getByPlaceholder('请选择归属服务')).toHaveCount(0);
    await expect(page.locator('.token-policy')).toHaveCount(2);
    await page.getByRole('button', { name: '编辑策略 1', exact: true }).click();
    await page.getByLabel('搜索 API 权限').locator('input').fill('不存在的接口');
    await expect(page.getByText('没有匹配的 API 权限')).toBeVisible();
    await page.getByRole('button', { name: '保存权限策略' }).click();
    await expect(page).toHaveURL(/\/api-tokens$/);
    const updated = state.requests.find(request => request.method === 'PUT')?.body;
    expect(updated).toMatchObject({ name: original.name, owner_type: 'service', owner_id: '', expires_at: null });
    expect(updated?.permissions).toHaveLength(2);
    expect(updated?.policies).toBeUndefined();
    expect(updated?.permissions).toEqual(expect.arrayContaining([
      expect.objectContaining({ endpoint_id: endpoint.id, tenant_id: 'tenant-a', project_id: 'project-1' }),
      expect.objectContaining({ endpoint_id: endpoint.id, tenant_id: 'tenant-b', project_id: 'project-1' }),
    ]));
  });

  test('creates group-scoped token without ownership selection and preserves filtered selections', async ({ page }) => {
    const state = await installMocks(page);
    await page.setViewportSize({ width: 1440, height: 1200 });
    await page.goto('/api-tokens/create');
    await expect(page.getByText('服务类型', { exact: true })).toBeVisible();
    await expect(page.getByPlaceholder('请选择归属服务')).toHaveCount(0);
    await expect(page.getByPlaceholder('用户 ID 或服务 ID')).toHaveCount(0);
    await page.getByPlaceholder('例如：订单服务生产 Token').fill('分组 Token');
    await page.getByRole('button', { name: '从零配置' }).click();
    await page.getByLabel('策略 1 租户范围').locator('input').fill('tenant-a');
    await page.getByLabel('策略 1 项目范围').locator('input').fill('project-a');
    await page.locator('.token-policy-groups .t-checkbox').filter({ hasText: '订单只读' }).click();
    await page.locator('.token-policy-groups .t-checkbox').filter({ hasText: '订单写入' }).click();
    await page.locator('.token-policy-toolbar .t-radio-button').filter({ hasText: '只读' }).click();
    await expect(page.locator('.token-policy-groups .t-checkbox').filter({ hasText: '订单写入' })).toHaveCount(0);
    await page.locator('.token-policy-toolbar .t-radio-button').filter({ hasText: '全部' }).click();
    await expect(page.locator('.token-policy-groups .t-checkbox').filter({ hasText: '订单写入' }).locator('input')).toBeChecked();
    await expect(page.locator('.api-token-error')).toHaveCount(0);
    if (process.env.TOKEN_UI_PREVIEW_PATH) await page.screenshot({ path: process.env.TOKEN_UI_PREVIEW_PATH, fullPage: true });
    await page.getByRole('button', { name: '创建 Token', exact: true }).click();
    await expect(page.getByText('nxs_live_full_secret_once', { exact: true })).toBeVisible();
    const body = state.requests.find(request => request.method === 'POST')?.body as Record<string, unknown>;
    expect(body).toMatchObject({ name: '分组 Token' });
    expect(body.owner_type).toBeUndefined();
    expect(body.permissions).toBeUndefined();
    const policies = body.policies as Policy[];
    expect(policies).toHaveLength(1);
    expect(policies[0]).toMatchObject({ effect: 'allow', service_resource: 'orders', tenant_id: 'tenant-a', project_id: 'project-a', permission_group_ids: ['group-orders-read', 'group-orders-write'] });
    expect(policies[0].id).toMatch(/^[0-9a-f-]{36}$/);
  });

  test('surfaces unavailable selected groups and blocks save until removed', async ({ page }) => {
    const state = await installMocks(page);
    state.groups.push({ id: 'group-orders-disabled', service_resource: 'orders', code: 'orders.disabled', name: '订单停用组', action: 'read', enabled: false, endpoint_ids: [endpoint.id], created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' });
    const original = state.tokens[0];
    original.permissions = [];
    original.policies = [{ id: 'policy-1', effect: 'allow', service_resource: 'orders', tenant_id: 'tenant-a', project_id: '', permission_group_ids: ['group-orders-read', 'group-orders-disabled', 'group-missing'] }];
    await page.goto('/api-tokens/token-1/edit');
    await page.getByRole('button', { name: '编辑策略 1', exact: true }).click();
    await expect(page.locator('.token-policy-unavailable > div:not(.t-alert)')).toHaveCount(2);
    await expect(page.locator('.token-policy-unavailable')).toContainText('group-missing');
    await expect(page.locator('.token-policy-groups .t-checkbox').filter({ hasText: '订单停用组' })).toHaveCount(0);
    await page.locator('.token-policy-groups .t-checkbox').filter({ hasText: '订单写入' }).click();
    await page.locator('.token-policy-toolbar .t-radio-button').filter({ hasText: '只读' }).click();
    await expect(page.locator('.token-policy-groups .t-checkbox').filter({ hasText: '订单写入' })).toHaveCount(0);
    await page.locator('.token-policy-toolbar .t-radio-button').filter({ hasText: '全部' }).click();
    await expect(page.locator('.token-policy-groups .t-checkbox').filter({ hasText: '订单写入' }).locator('input')).toBeChecked();
    await page.getByRole('button', { name: '保存修改', exact: true }).click();
    await expect(page.getByText('权限策略包含已停用、跨服务或不存在的权限组，请先移除后再保存')).toBeVisible();
    expect(state.requests.filter(request => request.method === 'PUT')).toHaveLength(0);
    await page.locator('.token-policy-unavailable').getByRole('button', { name: '移除权限组' }).first().click();
    await page.locator('.token-policy-unavailable').getByRole('button', { name: '移除权限组' }).first().click();
    await expect(page.locator('.token-policy-unavailable > div:not(.t-alert)')).toHaveCount(0);
    await page.getByRole('button', { name: '保存修改', exact: true }).click();
    await expect(page).toHaveURL(/\/api-tokens$/);
    const policies = state.requests.filter(request => request.method === 'PUT').at(-1)?.body.policies as Policy[];
    expect(policies).toHaveLength(1);
    expect(policies[0].permission_group_ids).toEqual(expect.arrayContaining(['group-orders-read', 'group-orders-write']));
    expect(policies[0].permission_group_ids).not.toContain('group-missing');
    expect(policies[0].permission_group_ids).not.toContain('group-orders-disabled');
  });

  test('new token without groups links straight to group creation', async ({ page }) => {
    const state = await installMocks(page);
    state.groups = [];
    await page.goto('/api-tokens/create');
    await expect(page.getByText('当前应用尚无权限组', { exact: false })).toBeVisible();
    await page.getByRole('button', { name: '创建权限组', exact: true }).click();
    await expect(page).toHaveURL(/\/api-permission-groups\/create$/);
  });

  test('creates group token, shows secret once, edits policies, changes status, rotates and revokes', async ({ page }) => {
    const state = await installMocks(page);
    await page.goto('/api-tokens');
    await expect(page.getByLabel('当前位置').getByText('API Token', { exact: true })).toBeVisible();

    await page.getByRole('button', { name: '创建 Token', exact: true }).click();
    await expect(page).toHaveURL(/\/api-tokens\/create$/);
    await page.getByPlaceholder('例如：订单服务生产 Token').fill('生产订单 Token');
    await page.getByRole('button', { name: '从零配置' }).click();
    await page.getByLabel('策略 1 租户范围').locator('input').fill('tenant-a');
    await page.locator('.token-policy-groups .t-checkbox').filter({ hasText: '订单只读' }).click();
    await page.getByRole('button', { name: '创建 Token', exact: true }).click();
    await expect(page.getByText('nxs_live_full_secret_once', { exact: true })).toBeVisible();
    expect(state.requests.find(request => request.method === 'POST')?.body).toMatchObject({ name: '生产订单 Token', policies: [expect.objectContaining({ service_resource: 'orders', permission_group_ids: ['group-orders-read'] })] });

    const secretDialog = page.locator('.t-dialog').filter({ hasText: '新 Token 仅显示一次' });
    await secretDialog.getByRole('button', { name: '关闭', exact: true }).click();
    await page.locator('.t-dialog').filter({ hasText: '确认关闭 Token 展示' }).getByRole('button', { name: '确认已保存', exact: true }).click();
    await expect(page.getByText('nxs_live_full_secret_once', { exact: true })).not.toBeVisible();

    const row = page.getByRole('row').filter({ hasText: '生产订单 Token' });
    await row.getByRole('button', { name: '编辑', exact: true }).click();
    await expect(page).toHaveURL(/\/api-tokens\/token-2\/edit$/);
    await expect(page.getByPlaceholder('例如：订单服务生产 Token')).toHaveValue('生产订单 Token');
    await page.getByRole('button', { name: '编辑策略 1', exact: true }).click();
    await expect(page.locator('.token-policy-groups .t-checkbox').filter({ hasText: '订单只读' }).locator('input')).toBeChecked();
    await expect(page.getByPlaceholder('请选择归属服务')).toHaveCount(0);
    await page.getByPlaceholder('例如：订单服务生产 Token').fill('编辑后的 Token');
    await page.getByRole('button', { name: '保存修改', exact: true }).click();
    await expect(page.getByText('编辑后的 Token', { exact: true })).toBeVisible();
    expect(state.requests.filter(request => request.method === 'PUT').at(-1)?.body).toMatchObject({ name: '编辑后的 Token', policies: [expect.objectContaining({ permission_group_ids: ['group-orders-read'] })] });

    const editedRow = page.getByRole('row').filter({ hasText: '编辑后的 Token' });
    await editedRow.getByRole('button', { name: '停用', exact: true }).click();
    await page.getByRole('button', { name: '确认', exact: true }).click();
    await expect(editedRow.getByText('停用', { exact: true })).toBeVisible();
    await editedRow.getByRole('button', { name: '启用', exact: true }).click();
    await page.getByRole('button', { name: '确认', exact: true }).click();
    await expect(editedRow.getByText('启用', { exact: true })).toBeVisible();
    await editedRow.getByRole('button', { name: '轮换', exact: true }).click();
    await page.getByRole('button', { name: '确认', exact: true }).click();
    await expect(page.getByText('nxs_live_rotated_secret_once', { exact: true })).toBeVisible();
    await page.locator('.t-dialog').filter({ hasText: '新 Token 仅显示一次' }).getByRole('button', { name: '关闭', exact: true }).click();
    await page.locator('.t-dialog').filter({ hasText: '确认关闭 Token 展示' }).getByRole('button', { name: '确认已保存', exact: true }).click();

    const rotatedRow = page.getByRole('row').filter({ hasText: '编辑后的 Token rotated' });
    await rotatedRow.getByRole('button', { name: '撤销', exact: true }).click();
    await page.getByRole('button', { name: '确认', exact: true }).click();
    await expect(rotatedRow.getByText('已撤销', { exact: true })).toBeVisible();
  });
});
