import { createRequire } from 'node:module';
import type { Locator, Page } from '../../../../dashboard/node_modules/@playwright/test';

const requireDashboardDependency = createRequire(new URL('../../../../dashboard/package.json', import.meta.url));
const { expect, test } = requireDashboardDependency('@playwright/test') as typeof import('../../../../dashboard/node_modules/@playwright/test');

// Frozen scope for this suite: the standalone 用户授权 (/bindings) page is retired and the
// role-management 角色管理 (/roles) drawer is now the only place that assigns users to a role.
// Everything below mocks the workbench API so no real SCIM directory or authorization data is touched.

const alice = { id: 'nexus-alice', userName: 'alice', name: { formatted: 'Alice 张' }, emails: [{ value: 'alice@example.test', primary: true }], active: true };
const bob = { id: 'nexus-bob', userName: 'bob', name: { formatted: 'Bob 李' }, emails: [{ value: 'bob@example.test', primary: true }], active: true };
const aliceLabel = 'Alice 张 · alice · alice@example.test';
const bobLabel = 'Bob 李 · bob · bob@example.test';

const roles = [
  { id: 'reader-role', code: 'reader', name: '读者', enabled: true, endpoint_ids: [], menu_ids: [] },
  { id: 'editor-role', code: 'editor', name: '编辑者', enabled: true, endpoint_ids: [], menu_ids: [] },
];

type Binding = { id?: string; subject: string; domain: string; role_id: string };
type PutFailure = { status: number; errorCode?: string; errorMessage: string };
// One PUT /roles/{id}/subjects call: grants or revokes a single role atomically.
type SavedAssignment = { role: string; domain: string; add: string[]; remove: string[] };

// The backend normalizes a blank/missing domain and the legacy "global" alias to the empty default.
const normalizeDomain = (domain: string) => (domain === 'global' ? '' : domain);

// Reuses the existing mockWorkbench strategy: intercept every /api/** call, answer the handful of
// endpoints the workbench needs, and keep an in-memory subject-role store for the drawer to read/save.
async function mockWorkbench(page: Page, initialBindings: Binding[] = []) {
  const saved: SavedAssignment[] = [];
  let bindings: Binding[] = initialBindings.map((binding, index) => ({ id: binding.id || `binding-${index}`, ...binding }));
  let failure: PutFailure | undefined;

  await page.addInitScript(() => sessionStorage.setItem('nexus-authorization-application', 'console'));
  await page.route('**/api/**', async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (!path.startsWith('/api/')) { await route.fallback(); return; }
    let result: unknown = { items: [] };
    if (path === '/api/auth/me') result = { isAuthenticated: true, user: { sub: 'admin', name: '管理员' } };
    if (path === '/api/v1/rbac-v1/roles') result = { items: roles };
    if (path === '/api/v1/rbac-v1/endpoints') result = { items: [], total: 0, page: 1, page_size: 100 };
    if (path === '/api/v1/rbac-v1/subject-roles') {
      if (request.method() === 'PUT') throw new Error('user assignment must not replace whole subject role sets');
      result = { items: bindings };
    }
    const roleSubjects = path.match(/^\/api\/v1\/rbac-v1\/roles\/([^/]+)\/subjects$/);
    if (roleSubjects && request.method() === 'PUT') {
      const body = request.postDataJSON() as Omit<SavedAssignment, 'role'>;
      const role = roleSubjects[1];
      saved.push({ role, ...body });
      if (failure) {
        await route.fulfill({ status: failure.status, json: { success: false, errorCode: failure.errorCode, errorMessage: failure.errorMessage } });
        return;
      }
      // Mirrors the backend: only this role changes, in the default domain (including legacy "global" rows).
      const domain = normalizeDomain(body.domain);
      bindings = bindings.filter(binding => !(binding.role_id === role && body.remove.includes(binding.subject) && normalizeDomain(binding.domain) === domain));
      for (const subject of body.add) {
        if (!bindings.some(binding => binding.role_id === role && binding.subject === subject && normalizeDomain(binding.domain) === domain)) {
          bindings.push({ id: `binding-${subject}-${role}`, subject, domain: body.domain, role_id: role });
        }
      }
      result = null;
    }
    await route.fulfill({ json: { success: true, result } });
  });

  return {
    saved,
    currentBindings: () => bindings,
    failSaves: (next: PutFailure) => { failure = next; },
  };
}

const userDirectory = (resources: unknown[], totalResults = resources.length) => ({
  success: true,
  result: { totalResults, startIndex: 1, itemsPerPage: resources.length, Resources: resources },
});

const roleRow = (page: Page, roleName: string) => page.getByRole('row').filter({ hasText: roleName }).first();
const drawerSelect = (drawer: Locator) => drawer.locator('.t-select').first();

async function openAssignDrawer(page: Page, roleName = '读者') {
  await page.goto('/roles');
  await roleRow(page, roleName).getByRole('button', { name: '分配用户', exact: true }).click();
  const drawer = page.locator('.t-drawer').filter({ hasText: `分配用户 · ${roleName}` });
  await expect(drawer).toBeVisible();
  return drawer;
}

async function pickUser(page: Page, drawer: Locator, search: string, optionLabel: string) {
  const select = drawerSelect(drawer);
  await select.click();
  if (search) await page.keyboard.type(search);
  await page.locator('.t-select-option').filter({ hasText: optionLabel }).first().click();
}

async function removeUserTag(drawer: Locator, label: string) {
  await drawerSelect(drawer).locator('.t-tag').filter({ hasText: label }).first().locator('.t-tag__icon-close').click();
}

const saveAssignment = (drawer: Locator) => drawer.getByRole('button', { name: '保存分配', exact: true }).click();
const expectSameIDs = (actual: string[], expected: string[]) => expect([...actual].sort()).toEqual([...expected].sort());

test('角色分配用户按 200/页 自动读取完整 SCIM 目录，并显示用户名、姓名与邮箱', async ({ page }) => {
  const workbench = await mockWorkbench(page);
  const requests: number[] = [];
  const firstPage = Array.from({ length: 200 }, (_, index) => ({ id: `first-${index}`, userName: `员工${index}`, active: true }));
  await page.route('**/api/v1/users?**', async route => {
    const url = new URL(route.request().url());
    const start = Number(url.searchParams.get('startIndex'));
    requests.push(start);
    expect(url.searchParams.get('count')).toBe('200');
    expect(route.request().headers().authorization).toBeUndefined();
    const resources = start === 1 ? firstPage : [alice];
    await route.fulfill({ json: { success: true, result: { totalResults: 201, startIndex: start, itemsPerPage: resources.length, Resources: resources } } });
  });

  const drawer = await openAssignDrawer(page);
  await expect(drawer.getByPlaceholder('搜索并选择用户')).toBeVisible();
  await expect.poll(() => requests).toEqual([1, 201]);

  await pickUser(page, drawer, 'alice', aliceLabel);
  await expect(drawerSelect(drawer).locator('.t-tag').filter({ hasText: aliceLabel })).toBeVisible();

  await saveAssignment(drawer);
  await expect.poll(() => workbench.saved.length).toBe(1);
  expect(workbench.saved[0]).toEqual({ role: 'reader-role', domain: '', add: ['nexus-alice'], remove: [] });
  await expect(page.getByText('用户分配已保存')).toBeVisible();
  await expect(drawer).toBeHidden();
});

test('移除用户只改默认域的本角色 membership，保留其他角色与其他 domain', async ({ page }) => {
  const workbench = await mockWorkbench(page, [
    { subject: alice.id, domain: 'global', role_id: 'reader-role' },
    { subject: alice.id, domain: 'global', role_id: 'editor-role' },
    { subject: alice.id, domain: 'tenant-a', role_id: 'reader-role' },
  ]);
  await page.route('**/api/v1/users?**', route => route.fulfill({ json: userDirectory([alice]) }));

  const drawer = await openAssignDrawer(page);
  await expect(drawerSelect(drawer).locator('.t-tag').filter({ hasText: aliceLabel })).toBeVisible();

  await removeUserTag(drawer, aliceLabel);
  await saveAssignment(drawer);

  await expect.poll(() => workbench.saved.length).toBe(1);
  // Only the reader membership in the default domain is dropped: editor stays, and tenant-a is untouched.
  // The legacy "global" rows read as the default domain.
  expect(workbench.saved[0]).toEqual({ role: 'reader-role', domain: '', add: [], remove: [alice.id] });
  expect(workbench.currentBindings().some(binding => binding.domain === 'tenant-a' && binding.role_id === 'reader-role')).toBe(true);
  expect(workbench.currentBindings().some(binding => binding.subject === alice.id && binding.role_id === 'editor-role')).toBe(true);
  await expect(drawer).toBeHidden();
});

test('新增用户到角色时保留其已有的其他默认域角色', async ({ page }) => {
  const workbench = await mockWorkbench(page, [{ subject: alice.id, domain: 'global', role_id: 'editor-role' }]);
  await page.route('**/api/v1/users?**', route => route.fulfill({ json: userDirectory([alice]) }));

  const drawer = await openAssignDrawer(page);
  await expect(drawerSelect(drawer).locator('.t-tag')).toHaveCount(0);

  await pickUser(page, drawer, 'alice', aliceLabel);
  await saveAssignment(drawer);

  await expect.poll(() => workbench.saved.length).toBe(1);
  // The request only grants this role; the editor role is never sent and therefore never overwritten.
  expect(workbench.saved[0]).toEqual({ role: 'reader-role', domain: '', add: [alice.id], remove: [] });
  expectSameIDs(workbench.currentBindings().filter(binding => binding.subject === alice.id).map(binding => binding.role_id), ['editor-role', 'reader-role']);
  await expect(drawer).toBeHidden();
});

test('目录中已不存在的原绑定用户显示为 原绑定用户（ID）并可移除', async ({ page }) => {
  const workbench = await mockWorkbench(page, [{ subject: 'former-user', domain: 'global', role_id: 'reader-role' }]);
  await page.route('**/api/v1/users?**', route => route.fulfill({ json: userDirectory([]) }));

  const drawer = await openAssignDrawer(page);
  await expect(drawer.getByText('原绑定用户（former-user）')).toBeVisible();

  await removeUserTag(drawer, '原绑定用户（former-user）');
  await saveAssignment(drawer);

  await expect.poll(() => workbench.saved.length).toBe(1);
  expect(workbench.saved[0]).toEqual({ role: 'reader-role', domain: '', add: [], remove: ['former-user'] });
  await expect(drawer).toBeHidden();
});

test('目录加载失败可重试，失败时禁用保存且不发送保存请求', async ({ page }) => {
  const workbench = await mockWorkbench(page);
  let attempts = 0;
  await page.route('**/api/v1/users?**', async route => {
    attempts += 1;
    if (attempts === 1) await route.fulfill({ status: 502, json: { success: false, errorMessage: '用户目录暂时不可用' } });
    else await route.fulfill({ json: userDirectory([alice]) });
  });

  const drawer = await openAssignDrawer(page);
  await expect(drawer.getByRole('alert')).toContainText('用户目录暂时不可用');
  await expect(drawer.getByRole('button', { name: '保存分配', exact: true })).toBeDisabled();

  await drawer.getByRole('button', { name: '重试', exact: true }).click();
  await expect(drawer.getByPlaceholder('搜索并选择用户')).toBeVisible();
  await expect(drawer.getByRole('button', { name: '保存分配', exact: true })).toBeEnabled();
  expect(workbench.saved).toEqual([]);
});

test('停用用户不能新增，但原停用绑定用户可以取消', async ({ page }) => {
  const inactiveBound = { id: 'nexus-inactive-bound', userName: '停用已绑定', active: false };
  const inactiveNew = { id: 'nexus-inactive-new', userName: '停用未绑定', active: false };
  const workbench = await mockWorkbench(page, [{ subject: inactiveBound.id, domain: 'global', role_id: 'reader-role' }]);
  await page.route('**/api/v1/users?**', route => route.fulfill({ json: userDirectory([alice, inactiveBound, inactiveNew]) }));

  const drawer = await openAssignDrawer(page);
  const select = drawerSelect(drawer);
  await expect(select.locator('.t-tag').filter({ hasText: '停用已绑定' })).toBeVisible();

  await select.click();
  await expect(page.locator('.t-select-option').filter({ hasText: '停用未绑定' })).toHaveClass(/t-is-disabled/);

  await removeUserTag(drawer, '停用已绑定');
  await saveAssignment(drawer);

  await expect.poll(() => workbench.saved.length).toBe(1);
  expect(workbench.saved[0]).toEqual({ role: 'reader-role', domain: '', add: [], remove: [inactiveBound.id] });
  expect(workbench.saved.some(item => item.add.includes(inactiveNew.id))).toBe(false);
  await expect(drawer).toBeHidden();
});

test('多个用户在一次请求中原子保存；失败时停留在抽屉、提示失败且不显示成功', async ({ page }) => {
  const workbench = await mockWorkbench(page, [
    { subject: alice.id, domain: 'global', role_id: 'reader-role' },
    { subject: bob.id, domain: 'global', role_id: 'reader-role' },
  ]);
  workbench.failSaves({ status: 502, errorMessage: '用户分配保存失败' });
  await page.route('**/api/v1/users?**', route => route.fulfill({ json: userDirectory([alice, bob]) }));

  const drawer = await openAssignDrawer(page);
  await removeUserTag(drawer, aliceLabel);
  await removeUserTag(drawer, bobLabel);
  await saveAssignment(drawer);

  await expect.poll(() => workbench.saved.length).toBe(1);
  expect(workbench.saved[0].role).toBe('reader-role');
  expectSameIDs(workbench.saved[0].remove, [alice.id, bob.id]);
  // Nothing was committed: both users keep the role.
  expect(workbench.currentBindings().filter(binding => binding.role_id === 'reader-role')).toHaveLength(2);
  await expect(drawer).toBeVisible();
  await expect(page.getByText('用户分配已保存')).toHaveCount(0);
  await expect(page.getByText('用户分配保存失败')).toBeVisible();
});

test('保存已提交但发布待重试（PUBLISH_PENDING）时提示原因，再次保存不会重复提交', async ({ page }) => {
  const workbench = await mockWorkbench(page);
  workbench.failSaves({ status: 503, errorCode: 'PUBLISH_PENDING', errorMessage: '配置已保存，但授权记录发布失败，后台会自动重试；如持续失败请检查 Redis' });
  await page.route('**/api/v1/users?**', route => route.fulfill({ json: userDirectory([alice]) }));

  const drawer = await openAssignDrawer(page);
  await pickUser(page, drawer, 'alice', aliceLabel);
  await saveAssignment(drawer);

  await expect.poll(() => workbench.saved.length).toBe(1);
  await expect(drawer.getByRole('alert')).toContainText('配置已保存');
  // The change counts as saved, so saving again closes without another request.
  await saveAssignment(drawer);
  await expect(drawer).toBeHidden();
  expect(workbench.saved).toHaveLength(1);
});

test('保存成功关闭抽屉，未修改绑定时不发送保存请求', async ({ page }) => {
  const workbench = await mockWorkbench(page, [{ subject: alice.id, domain: 'global', role_id: 'reader-role' }]);
  await page.route('**/api/v1/users?**', route => route.fulfill({ json: userDirectory([alice]) }));

  const drawer = await openAssignDrawer(page);
  await expect(drawerSelect(drawer).locator('.t-tag').filter({ hasText: aliceLabel })).toBeVisible();
  await saveAssignment(drawer);

  expect(workbench.saved).toEqual([]);
  await expect(drawer).toBeHidden();
});

test('旧 /bindings 重定向到 /roles，导航与标签不再保留用户授权入口', async ({ page }) => {
  await mockWorkbench(page, [{ subject: alice.id, domain: 'global', role_id: 'reader-role' }]);
  await page.route('**/api/v1/users?**', route => route.fulfill({ json: userDirectory([alice]) }));
  await page.addInitScript(() => {
    window.localStorage.setItem('nexus-authorization-layout-tabs', JSON.stringify([
      { value: '/bindings', label: '用户授权', removable: true },
      { value: '/dashboard', label: '配置总览', removable: false },
    ]));
  });

  await page.goto('/bindings');
  await expect(page).toHaveURL(/\/roles$/);
  await expect(roleRow(page, '读者')).toHaveCount(1);
  await expect(page.locator('.t-menu__item').filter({ hasText: '用户授权' })).toHaveCount(0);
  await expect(page.locator('.t-tabs__nav-item').filter({ hasText: '用户授权' })).toHaveCount(0);

  const storedTabs = await page.evaluate(() => JSON.parse(window.localStorage.getItem('nexus-authorization-layout-tabs') || '[]') as { value: string }[]);
  expect(storedTabs.some(tab => tab.value === '/bindings')).toBe(false);
});
