import { createRequire } from 'node:module';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import type { Locator, Page } from '../../../../dashboard/node_modules/@playwright/test';

// Resolve Playwright from the Dashboard package while the suite lives at the repository root.
const requireDashboardDependency = createRequire(new URL('../../../../dashboard/package.json', import.meta.url));
const { expect, test } = requireDashboardDependency('@playwright/test') as typeof import('../../../../dashboard/node_modules/@playwright/test');

// Frozen scope for the rewritten role authorization editor (dashboard/src/pages/policy-workbench/components/role-editor.tsx).
//   /roles                 -> 角色管理 list, 新增角色 leads to /roles/new, 编辑授权 leads to /roles/:roleId/edit
//   /roles/new             -> 新增角色
//   /roles/:roleId/edit    -> 编辑授权 (角色编码 disabled)
// Saving posts to POST /api/v1/rbac-v1/roles?app_key=console with the original payload
//   { id? (edit only), code, name, description, enabled, endpoint_ids, menu_ids } and never touches a real backend.
//
// UX contract asserted by this suite:
//   - compact fields carry aria-label 角色名称 / 角色编码 / 角色说明; 返回角色管理 button; role=tab 菜单与按钮 · n / API 接口 · n
//   - tree label buttons aria-label 配置{name} only locate (never grant); access switch aria-label 允许访问{name} grants the node alone
//   - button cards are <button role="checkbox" aria-label="授权{name}" aria-checked>; clicking grants every enabled ancestor
//   - API tab: service buttons use aria-pressed with {selected} / {total}; 全选当前结果 only covers the current filtered enabled rows;
//     rows carry data-endpoint-id and their checkbox aria-label is 授权{method} {path_template}
//   - 查看已选权限 opens the 已选权限 drawer; API remove buttons aria-label 移除{method} {path_template} and raise a 撤销 status
//   - dirty drafts open a useBlocker Dialog (放弃修改并离开 / 继续编辑); tab switching never confirms
//   - unavailable grants (retired menu, inactive service) must be removed before saving; disabled API grants stay removable
// Every /api/** call is mocked below, so no real authorization data, service permissions or databases are touched.

const APP_KEY = 'console';
const SCREENSHOT_DIR = fileURLToPath(new URL('../../../../ui/verification/', import.meta.url));
mkdirSync(SCREENSHOT_DIR, { recursive: true });
const screenshotPath = (name: string) => join(SCREENSHOT_DIR, `role-permission-implemented-${name}.png`);

type RoleSeed = {
  id: string;
  code: string;
  name: string;
  description: string;
  enabled: boolean;
  endpoint_ids: string[];
  menu_ids: string[];
};

type EndpointSeed = {
  id: string;
  service_resource: string;
  endpoint_key: string;
  method: string;
  path_template: string;
  module: string;
  summary: string;
  source: 'manual' | 'swagger';
  enabled: boolean;
};

type MenuSeed = {
  id: string;
  parent_id: string | null;
  code: string;
  name: string;
  menu_type: 'directory' | 'menu' | 'button';
  route: string;
  component: string;
  icon: string;
  sort_order: number;
  enabled: boolean;
};

type SavedRole = Record<string, unknown>;

// Seed role for the edit route. It intentionally authorizes a now-disabled API (ep-report-publish,
// enabled:false) so the editor keeps it while editing and the user can still remove it.
const editRoleSeed: RoleSeed = {
  id: 'role-reviewer',
  code: 'order-reviewer',
  name: '订单审核员',
  description: '审核跨服务订单与报表权限',
  enabled: true,
  endpoint_ids: ['ep-orders-list', 'ep-report-publish'],
  menu_ids: ['menu-orders', 'btn-orders-audit'],
};

// Two services use camelCase and snake_case payloads so frontend normalization is exercised; the
// legacy service is inactive and must never be freshly authorized.
const serviceResources = [
  { id: 'svc-orders', key: 'orders-service', name: 'orders-service', displayName: '订单平台', audience: 'orders.api', description: '订单服务', isActive: true, source: 'nexusauth' },
  { id: 'svc-report', key: 'report-service', name: 'report-service', display_name: '报表中心', audience: 'report.api', description: '报表服务', is_active: true, source: 'local' },
  { id: 'svc-legacy', key: 'legacy-service', name: 'legacy-service', display_name: '历史服务', audience: 'legacy.api', description: '已下线服务', is_active: false, source: 'local' },
];

const ENDPOINTS: EndpointSeed[] = [
  { id: 'ep-orders-list', service_resource: 'orders-service', endpoint_key: 'GET:/v1/orders', method: 'GET', path_template: '/v1/orders', module: '订单', summary: '查询订单', source: 'swagger', enabled: true },
  { id: 'ep-orders-options', service_resource: 'orders-service', endpoint_key: 'OPTIONS:/v1/orders', method: 'OPTIONS', path_template: '/v1/orders', module: '订单', summary: '订单预检', source: 'swagger', enabled: true },
  { id: 'ep-orders-head', service_resource: 'orders-service', endpoint_key: 'HEAD:/v1/orders', method: 'HEAD', path_template: '/v1/orders', module: '订单巡检', summary: '订单可用性', source: 'swagger', enabled: true },
  { id: 'ep-orders-create', service_resource: 'orders-service', endpoint_key: 'POST:/v1/orders', method: 'POST', path_template: '/v1/orders', module: '订单', summary: '创建订单', source: 'swagger', enabled: true },
  { id: 'ep-orders-delete', service_resource: 'orders-service', endpoint_key: 'DELETE:/v1/orders/{id}', method: 'DELETE', path_template: '/v1/orders/{id}', module: '订单', summary: '删除订单', source: 'swagger', enabled: true },
  { id: 'ep-report-list', service_resource: 'report-service', endpoint_key: 'GET:/v1/reports', method: 'GET', path_template: '/v1/reports', module: '报表', summary: '查询报表', source: 'swagger', enabled: true },
  { id: 'ep-report-publish', service_resource: 'report-service', endpoint_key: 'POST:/v1/reports/publish', method: 'POST', path_template: '/v1/reports/publish', module: '报表', summary: '提交报表发布', source: 'swagger', enabled: false },
  { id: 'ep-legacy-list', service_resource: 'legacy-service', endpoint_key: 'GET:/v1/legacy', method: 'GET', path_template: '/v1/legacy', module: '历史', summary: '旧接口', source: 'manual', enabled: true },
];

const MENUS: MenuSeed[] = [
  { id: 'menu-dir-orders', parent_id: null, code: 'orders-center', name: '订单中心', menu_type: 'menu', route: '', component: '', icon: '', sort_order: 1, enabled: true },
  { id: 'menu-orders', parent_id: 'menu-dir-orders', code: 'orders', name: '订单管理', menu_type: 'menu', route: '/orders', component: '', icon: '', sort_order: 1, enabled: true },
  { id: 'btn-orders-audit', parent_id: 'menu-orders', code: 'order-audit', name: '审核订单', menu_type: 'button', route: '', component: '', icon: '', sort_order: 1, enabled: true },
  { id: 'btn-orders-export', parent_id: 'menu-orders', code: 'order-export', name: '导出订单', menu_type: 'button', route: '', component: '', icon: '', sort_order: 2, enabled: true },
  { id: 'menu-reports', parent_id: null, code: 'reports', name: '报表管理', menu_type: 'menu', route: '/reports', component: '', icon: '', sort_order: 2, enabled: true },
  { id: 'btn-reports-publish', parent_id: 'menu-reports', code: 'report-publish', name: '发布报表', menu_type: 'button', route: '', component: '', icon: '', sort_order: 1, enabled: true },
  { id: 'menu-legacy', parent_id: null, code: 'legacy', name: '历史菜单', menu_type: 'menu', route: '/legacy', component: '', icon: '', sort_order: 9, enabled: false },
];

// Arbitrary depth fixture: six nested levels (deep-1 .. deep-5 + deep-5-btn is aria-level 6).
const DEEP_MENUS: MenuSeed[] = [
  { id: 'deep-1', parent_id: null, code: 'deep-1', name: '深层层级一', menu_type: 'menu', route: '', component: '', icon: '', sort_order: 1, enabled: true },
  { id: 'deep-2', parent_id: 'deep-1', code: 'deep-2', name: '深层层级二', menu_type: 'menu', route: '', component: '', icon: '', sort_order: 1, enabled: true },
  { id: 'deep-3', parent_id: 'deep-2', code: 'deep-3', name: '深层层级三', menu_type: 'menu', route: '', component: '', icon: '', sort_order: 1, enabled: true },
  { id: 'deep-4', parent_id: 'deep-3', code: 'deep-4', name: '深层层级四', menu_type: 'menu', route: '/deep-4', component: '', icon: '', sort_order: 1, enabled: true },
  { id: 'deep-4-btn', parent_id: 'deep-4', code: 'deep-4-btn', name: '四级直连按钮', menu_type: 'button', route: '', component: '', icon: '', sort_order: 1, enabled: true },
  { id: 'deep-5', parent_id: 'deep-4', code: 'deep-5', name: '深层层级五', menu_type: 'menu', route: '/deep-5', component: '', icon: '', sort_order: 2, enabled: true },
  { id: 'deep-5-btn', parent_id: 'deep-5', code: 'deep-5-btn', name: '深层按钮六', menu_type: 'button', route: '', component: '', icon: '', sort_order: 1, enabled: true },
  { id: 'deep-other', parent_id: null, code: 'deep-other', name: '无关菜单', menu_type: 'menu', route: '/deep-other', component: '', icon: '', sort_order: 2, enabled: true },
];

type EditorMock = {
  roles: RoleSeed[];
  saved: SavedRole[];
  endpointsFailures: number;
  saveFailures: number;
  saveFailureMessage: string;
  saveDelayMs: number;
};

// Single catch-all router (kept in the scim-user-binding.spec.ts style). Everything under /api/** is
// answered from fixtures; unrelated requests fall back to the Vite dev server. No real data changes.
async function mockRoleEditor(page: Page, options: { roles?: RoleSeed[]; menus?: MenuSeed[] } = {}): Promise<EditorMock> {
  const menus = options.menus ?? MENUS;
  const state: EditorMock = {
    roles: (options.roles ?? [editRoleSeed]).map(role => ({ ...role, endpoint_ids: [...role.endpoint_ids], menu_ids: [...role.menu_ids] })),
    saved: [],
    endpointsFailures: 0,
    saveFailures: 0,
    saveFailureMessage: '保存角色失败',
    saveDelayMs: 0,
  };

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
      await route.fulfill({ json: { success: true, result: { items: serviceResources } } });
      return;
    }
    if (path === '/api/v1/rbac-v1/endpoints') {
      if (state.endpointsFailures > 0) {
        state.endpointsFailures -= 1;
        await route.fulfill({ status: 502, json: { success: false, errorMessage: '接口列表加载失败' } });
        return;
      }
      await route.fulfill({ json: { success: true, result: { items: ENDPOINTS, total: ENDPOINTS.length, page: 1, page_size: 100 } } });
      return;
    }
    if (path === '/api/v1/rbac-v1/menus') {
      await route.fulfill({ json: { success: true, result: { items: menus } } });
      return;
    }
    if (path === '/api/v1/rbac-v1/roles') {
      if (request.method() === 'POST') {
        if (state.saveFailures > 0) {
          state.saveFailures -= 1;
          await route.fulfill({ status: 502, json: { success: false, errorMessage: state.saveFailureMessage } });
          return;
        }
        const body = request.postDataJSON() as SavedRole;
        state.saved.push(body);
        if (state.saveDelayMs > 0) await new Promise(resolve => setTimeout(resolve, state.saveDelayMs));
        await route.fulfill({ json: { success: true, result: { id: body.id || 'role-created', ...body } } });
        return;
      }
      await route.fulfill({ json: { success: true, result: { items: state.roles } } });
      return;
    }
    const roleDetail = path.match(/^\/api\/v1\/rbac-v1\/roles\/([^/]+)$/);
    if (roleDetail) {
      const role = state.roles.find(item => item.id === decodeURIComponent(roleDetail[1]));
      if (!role) { await route.fulfill({ status: 404, json: { success: false, errorMessage: '角色不存在' } }); return; }
      await route.fulfill({ json: { success: true, result: role } });
      return;
    }
    if (path === '/api/v1/rbac-v1/subject-roles' || path === '/api/v1/rbac-v1/releases') {
      await route.fulfill({ json: { success: true, result: { items: [] } } });
      return;
    }
    await route.fulfill({ json: { success: true, result: { items: [] } } });
  });

  return state;
}

const editor = (page: Page) => page.locator('.role-editor');
// TDesign Input spreads aria-label onto the .t-input__wrap wrapper, so reach through it to the input.
const field = (page: Page, label: string): Locator => editor(page).locator(`.t-input__wrap[aria-label="${label}"] input`);
const tab = (page: Page, name: RegExp): Locator => editor(page).getByRole('tab', { name });
const treeLabel = (page: Page, name: string): Locator => editor(page).locator(`button[aria-label="配置${name}"]`);
const accessSwitch = (page: Page, name: string): Locator => editor(page).locator(`button[role="switch"][aria-label="允许访问${name}"]`);
const buttonCard = (page: Page, name: string): Locator => editor(page).locator(`button[role="checkbox"][aria-label="授权${name}"]`);
const checkboxLabel = (page: Page, text: string): Locator => editor(page).locator('label.t-checkbox').filter({ hasText: text });
const endpointRow = (page: Page, id: string): Locator => editor(page).locator(`tr[data-endpoint-id="${id}"]`);
const endpointCheckbox = (page: Page, id: string): Locator => endpointRow(page, id).locator('input[type="checkbox"]');
const serviceOption = (page: Page, name: string): Locator => editor(page).locator('button.role-editor-service-option').filter({ hasText: name });
const saveButton = (page: Page): Locator => editor(page).getByRole('button', { name: '保存角色', exact: true });
const backButton = (page: Page): Locator => editor(page).getByRole('button', { name: '返回角色管理', exact: true });
const reviewButton = (page: Page): Locator => editor(page).getByRole('button', { name: '查看已选权限', exact: true });
const undoStatus = (page: Page): Locator => editor(page).locator('.role-editor-undo');
const infoSection = (page: Page): Locator => editor(page).locator('section[aria-label="角色信息"]');
const alerts = (page: Page): Locator => editor(page).locator('[role="alert"]');
const tabLabel = (page: Page): Locator => editor(page).locator('h2');

async function fillDraft(page: Page, name: string, code: string, description = '') {
  await field(page, '角色名称').fill(name);
  await field(page, '角色编码').fill(code);
  if (description) await field(page, '角色说明').fill(description);
}

async function openNewRole(page: Page) {
  await page.goto('/roles/new');
  await expect(infoSection(page)).toBeVisible();
}

// Open a TDesign Select (0 = 模块筛选, 1 = 请求方法) and pick an option by its label.
async function pickSelect(page: Page, index: 0 | 1, optionText: string) {
  const trigger = editor(page).locator('.role-editor-api-filters .t-select__wrap').nth(index);
  await trigger.locator('input').first().click();
  await page.locator('.t-select-option').filter({ hasText: optionText }).first().click();
}

async function expectNoHorizontalOverflow(page: Page) {
  const metrics = await editor(page).evaluate(element => ({
    editorScroll: element.scrollWidth,
    editorClient: element.clientWidth,
    docScroll: document.documentElement.scrollWidth,
    docClient: document.documentElement.clientWidth,
  }));
  expect(metrics.editorScroll).toBeLessThanOrEqual(metrics.editorClient + 1);
  expect(metrics.docScroll).toBeLessThanOrEqual(metrics.docClient + 1);
}

const sortedIDs = (value: unknown): string[] => [...((value as string[]) ?? [])].sort();

test.use({ viewport: { width: 1440, height: 1040 } });

test('新增角色入口与紧凑表单：返回角色管理、aria 字段、编码可编辑、role=tab 计数', async ({ page }) => {
  await mockRoleEditor(page);

  await page.goto('/roles');
  await page.getByRole('button', { name: '新增角色', exact: true }).click();
  await expect(page).toHaveURL(/\/roles\/new$/);

  await expect(backButton(page)).toBeVisible();
  await expect(field(page, '角色名称')).toBeVisible();
  await expect(field(page, '角色编码')).toBeEnabled();
  await expect(field(page, '角色说明')).toBeVisible();
  await expect(tab(page, /菜单与按钮/)).toHaveText('菜单与按钮 · 0');
  await expect(tab(page, /API 接口/)).toHaveText('API 接口 · 0');
  await expect(tab(page, /菜单与按钮/)).toHaveAttribute('aria-selected', 'true');

  // 返回角色管理 works while the draft is untouched (no blocker).
  await backButton(page).click();
  await expect(page).toHaveURL(/\/roles$/);
});

test('菜单与按钮：定位不授权、开关只选本节点、按钮补齐有效祖先，保存 payload 的 data shape 不变', async ({ page }) => {
  const state = await mockRoleEditor(page);

  await openNewRole(page);
  await fillDraft(page, '订单审核员', 'order-reviewer', '跨服务审核角色');

  // Locating a button node points at its parent menu with the button highlighted, without granting anything.
  await treeLabel(page, '审核订单').click();
  await expect(tabLabel(page)).toHaveText('订单管理');
  await expect(accessSwitch(page, '订单管理')).not.toHaveClass(/is-checked/);
  await expect(buttonCard(page, '审核订单')).toHaveAttribute('aria-checked', 'false');
  await expect(tab(page, /菜单与按钮/)).toHaveText('菜单与按钮 · 0');

  // The access switch grants only the node it describes.
  await accessSwitch(page, '订单管理').click();
  await expect(accessSwitch(page, '订单管理')).toHaveClass(/is-checked/);
  await expect(buttonCard(page, '审核订单')).toHaveAttribute('aria-checked', 'false');
  await expect(tab(page, /菜单与按钮/)).toHaveText('菜单与按钮 · 1');

  // A button card grants every enabled ancestor and itself.
  await buttonCard(page, '审核订单').click();
  await expect(buttonCard(page, '审核订单')).toHaveAttribute('aria-checked', 'true');
  await expect(tab(page, /菜单与按钮/)).toHaveText('菜单与按钮 · 3');
  await treeLabel(page, '订单中心').click();
  await expect(accessSwitch(page, '订单中心')).toHaveClass(/is-checked/);

  // Capture a menu with its direct buttons.
  await treeLabel(page, '订单管理').click();
  await expect(tabLabel(page)).toHaveText('订单管理');
  await expect(buttonCard(page, '审核订单')).toHaveAttribute('aria-checked', 'true');
  await expect(buttonCard(page, '导出订单')).toBeVisible();
  await page.screenshot({ path: screenshotPath('menus'), fullPage: true, animations: 'disabled' });

  await saveButton(page).click();
  await expect(page).toHaveURL(/\/roles$/);
  expect(state.saved).toHaveLength(1);

  const payload = state.saved[0];
  expect(payload.id).toBeUndefined();
  expect(payload.code).toBe('order-reviewer');
  expect(payload.name).toBe('订单审核员');
  expect(payload.description).toBe('跨服务审核角色');
  expect(payload.enabled).toBe(true);
  expect(Object.keys(payload).sort()).toEqual(['code', 'description', 'enabled', 'endpoint_ids', 'menu_ids', 'name']);
  expect(sortedIDs(payload.menu_ids)).toEqual(['btn-orders-audit', 'menu-dir-orders', 'menu-orders']);
  expect(payload.endpoint_ids).toEqual([]);
});

test('listMenus 兼容旧 wire directory 记录：映射为菜单、保留层级并按菜单计数', async ({ page }) => {
  const legacyMenus: MenuSeed[] = [
    { id: 'legacy-center', parent_id: null, code: 'legacy-center', name: '旧目录节点', menu_type: 'directory', route: '', component: '', icon: '', sort_order: 1, enabled: true },
    { id: 'legacy-orders', parent_id: 'legacy-center', code: 'legacy-orders', name: '旧目录下的菜单', menu_type: 'menu', route: '/legacy-orders', component: '', icon: '', sort_order: 1, enabled: true },
  ];
  const state = await mockRoleEditor(page, { menus: legacyMenus });

  await openNewRole(page);
  await fillDraft(page, '兼容角色', 'legacy-role');

  // directory 记录在 API 边界即映射为菜单：保留 id/parent 层级，展示访问开关且不再有目录图例。
  await expect(treeLabel(page, '旧目录下的菜单')).toBeVisible();
  await expect(editor(page).locator('.role-tree-legend')).not.toContainText('目录');

  await treeLabel(page, '旧目录节点').click();
  await expect(accessSwitch(page, '旧目录节点')).toBeVisible();
  await accessSwitch(page, '旧目录节点').click();
  await expect(tab(page, /菜单与按钮/)).toHaveText('菜单与按钮 · 1');

  await saveButton(page).click();
  await expect(page).toHaveURL(/\/roles$/);
  expect(sortedIDs(state.saved[0].menu_ids)).toEqual(['legacy-center']);
});

test('取消授权移除后代并提供 role=status 撤销', async ({ page }) => {
  const state = await mockRoleEditor(page);

  await openNewRole(page);
  await fillDraft(page, '菜单角色', 'menu-role');

  await treeLabel(page, '审核订单').click();
  await buttonCard(page, '审核订单').click();
  await expect(tab(page, /菜单与按钮/)).toHaveText('菜单与按钮 · 3');

  await treeLabel(page, '订单中心').click();
  await expect(accessSwitch(page, '订单中心')).toHaveClass(/is-checked/);

  // Turning the parent menu off clears its whole subtree.
  await accessSwitch(page, '订单中心').click();
  await expect(accessSwitch(page, '订单中心')).not.toHaveClass(/is-checked/);
  await expect(tab(page, /菜单与按钮/)).toHaveText('菜单与按钮 · 0');
  await expect(undoStatus(page)).toBeVisible();
  await expect(undoStatus(page)).toContainText('已移除 3 项权限');

  await undoStatus(page).getByRole('button', { name: '撤销', exact: true }).click();
  await expect(undoStatus(page)).toHaveCount(0);
  await expect(accessSwitch(page, '订单中心')).toHaveClass(/is-checked/);
  await expect(tab(page, /菜单与按钮/)).toHaveText('菜单与按钮 · 3');

  await saveButton(page).click();
  await expect(page).toHaveURL(/\/roles$/);
  expect(sortedIDs(state.saved[0].menu_ids)).toEqual(['btn-orders-audit', 'menu-dir-orders', 'menu-orders']);
});

test('编辑角色：编码锁定、回显授权、停用 API 授权保留且可移除', async ({ page }) => {
  const state = await mockRoleEditor(page, { roles: [editRoleSeed] });

  await page.goto('/roles/role-reviewer/edit');
  await expect(infoSection(page)).toBeVisible();
  await expect(field(page, '角色名称')).toHaveValue('订单审核员');
  await expect(field(page, '角色编码')).toHaveValue('order-reviewer');
  await expect(field(page, '角色编码')).toBeDisabled();
  // 首层节点现在也是菜单，默认详情落在 订单中心；定位到 订单管理 校验回显授权。
  await treeLabel(page, '订单管理').click();
  await expect(tabLabel(page)).toHaveText('订单管理');
  await expect(accessSwitch(page, '订单管理')).toHaveClass(/is-checked/);
  await expect(buttonCard(page, '审核订单')).toHaveAttribute('aria-checked', 'true');

  await tab(page, /API 接口/).click();
  await expect(tab(page, /API 接口/)).toHaveText('API 接口 · 2');
  await serviceOption(page, '报表中心').click();
  await expect(endpointCheckbox(page, 'ep-report-publish')).toBeChecked();
  await expect(endpointCheckbox(page, 'ep-report-publish')).toBeEnabled();
  await expect(endpointRow(page, 'ep-report-publish')).toHaveClass(/is-selected/);

  // The retained but disabled API can still be removed.
  await endpointRow(page, 'ep-report-publish').locator('label.t-checkbox').click();
  await expect(endpointCheckbox(page, 'ep-report-publish')).not.toBeChecked();
  await expect(tab(page, /API 接口/)).toHaveText('API 接口 · 1');

  await saveButton(page).click();
  await expect(page).toHaveURL(/\/roles$/);
  expect(state.saved).toHaveLength(1);
  expect(state.saved[0].id).toBe('role-reviewer');
  expect(Object.keys(state.saved[0]).sort()).toEqual(['code', 'description', 'enabled', 'endpoint_ids', 'id', 'menu_ids', 'name']);
  expect(state.saved[0].endpoint_ids).toEqual(['ep-orders-list']);
  expect(sortedIDs(state.saved[0].menu_ids)).toEqual(['btn-orders-audit', 'menu-orders']);
});

test('API 接口：服务 aria-pressed selected/total、搜索、模块/方法筛选、全选当前结果只作用于筛选内启用接口', async ({ page }) => {
  const state = await mockRoleEditor(page);

  await openNewRole(page);
  await fillDraft(page, '接口角色', 'api-role');
  await tab(page, /API 接口/).click();

  // Default service = 订单平台; buttons expose aria-pressed and {selected} / {total}.
  await expect(serviceOption(page, '订单平台')).toHaveAttribute('aria-pressed', 'true');
  await expect(serviceOption(page, '订单平台')).toHaveText(/0 \/ 5/);
  await expect(serviceOption(page, '报表中心')).toHaveAttribute('aria-pressed', 'false');
  await expect(checkboxLabel(page, '全选当前结果')).toContainText('全选当前结果（5）');

  await endpointRow(page, 'ep-orders-list').locator('label.t-checkbox').click();
  await expect(endpointCheckbox(page, 'ep-orders-list')).toBeChecked();
  await expect(endpointRow(page, 'ep-orders-list').locator('label.t-checkbox')).toHaveAttribute('aria-label', '授权GET /v1/orders');
  await expect(serviceOption(page, '订单平台')).toHaveText(/1 \/ 5/);
  await expect(tab(page, /API 接口/)).toHaveText('API 接口 · 1');

  // Search matches 接口说明 / 路径; selections survive filtering.
  await editor(page).getByPlaceholder('搜索接口名称或路径').fill('reports');
  await expect(endpointRow(page, 'ep-orders-list')).toHaveCount(0);
  await expect(editor(page).locator('.role-editor-empty')).toContainText('没有匹配的接口');
  await editor(page).getByPlaceholder('搜索接口名称或路径').fill('');
  await expect(endpointCheckbox(page, 'ep-orders-list')).toBeChecked();

  // Method filter narrows the bulk scope; hidden selections are preserved.
  await pickSelect(page, 1, 'DELETE');
  await expect(endpointRow(page, 'ep-orders-list')).toHaveCount(0);
  await expect(endpointRow(page, 'ep-orders-delete')).toBeVisible();
  await expect(checkboxLabel(page, '全选当前结果')).toContainText('全选当前结果（1）');
  await checkboxLabel(page, '全选当前结果').click();
  await expect(endpointCheckbox(page, 'ep-orders-delete')).toBeChecked();
  await expect(editor(page).locator('.role-editor-api-bulk')).toContainText('另有 1 个已选被筛选隐藏');

  await editor(page).locator('.role-editor-filter-chips button').filter({ hasText: 'DELETE' }).click();
  await expect(endpointCheckbox(page, 'ep-orders-list')).toBeChecked();
  await expect(endpointCheckbox(page, 'ep-orders-create')).not.toBeChecked();
  await expect(tab(page, /API 接口/)).toHaveText('API 接口 · 2');

  // Module filter shares the same Select contract.
  await pickSelect(page, 0, '订单巡检');
  await expect(endpointRow(page, 'ep-orders-head')).toBeVisible();
  await expect(endpointRow(page, 'ep-orders-list')).toHaveCount(0);
  await editor(page).locator('.role-editor-filter-chips button').filter({ hasText: '订单巡检' }).click();

  // Cross-service: switching services keeps selections, and bulk never selects disabled rows.
  await serviceOption(page, '报表中心').click();
  await expect(serviceOption(page, '报表中心')).toHaveAttribute('aria-pressed', 'true');
  await expect(endpointRow(page, 'ep-orders-list')).toHaveCount(0);
  await expect(checkboxLabel(page, '全选当前结果')).toContainText('全选当前结果（1）');
  await checkboxLabel(page, '全选当前结果').click();
  await expect(endpointCheckbox(page, 'ep-report-list')).toBeChecked();
  await expect(endpointCheckbox(page, 'ep-report-publish')).not.toBeChecked();
  await expect(tab(page, /API 接口/)).toHaveText('API 接口 · 3');

  await serviceOption(page, '订单平台').click();
  await expect(endpointCheckbox(page, 'ep-orders-list')).toBeChecked();
  await expect(endpointCheckbox(page, 'ep-orders-delete')).toBeChecked();
  await expect(endpointCheckbox(page, 'ep-orders-create')).not.toBeChecked();

  // Capture active module/method chips and the selected API hidden by those filters.
  await pickSelect(page, 0, '订单');
  await pickSelect(page, 1, 'GET');
  await expect(editor(page).locator('.role-editor-filter-chips button')).toHaveText(['订单 ×', 'GET ×']);
  await expect(endpointCheckbox(page, 'ep-orders-list')).toBeChecked();
  await expect(endpointRow(page, 'ep-orders-delete')).toHaveCount(0);
  await expect(editor(page).locator('.role-editor-api-bulk')).toContainText('当前已选 1 / 1 · 另有 1 个已选被筛选隐藏');
  await expect(serviceOption(page, '报表中心')).toHaveText(/1 \/ 2/);
  await page.screenshot({ path: screenshotPath('apis'), fullPage: true, animations: 'disabled' });

  await saveButton(page).click();
  await expect(page).toHaveURL(/\/roles$/);
  expect(sortedIDs(state.saved[0].endpoint_ids)).toEqual(['ep-orders-delete', 'ep-orders-list', 'ep-report-list']);
});

test('查看已选权限抽屉：API 移除与撤销', async ({ page }) => {
  await mockRoleEditor(page);

  await openNewRole(page);
  await fillDraft(page, '审核角色', 'review-role');
  await tab(page, /API 接口/).click();
  await endpointRow(page, 'ep-orders-list').locator('label.t-checkbox').click();
  await tab(page, /菜单与按钮/).click();
  await treeLabel(page, '订单管理').click();
  await accessSwitch(page, '订单管理').click();

  await reviewButton(page).click();
  const drawer = page.locator('.role-permission-review');
  await expect(drawer).toBeVisible();
  await expect(drawer.getByText('已选权限')).toBeVisible();
  await expect(drawer).toContainText('订单平台 · 1');
  await expect(drawer.locator('.role-review-api code')).toHaveText('GET /v1/orders');

  // Visibility alone can pass while the drawer panel is still sliding in offscreen.
  const drawerPanel = drawer.locator('.t-drawer__content-wrapper');
  await expect.poll(async () => {
    const box = await drawerPanel.boundingBox();
    const viewport = page.viewportSize();
    if (!box || !viewport) return false;
    const settled = await drawerPanel.evaluate(element => element.getAnimations().every(animation =>
      animation.playState !== 'running' && !animation.pending));
    return settled && box.width > 0 && box.height > 0 && box.x >= -1 && box.y >= -1
      && box.x + box.width <= viewport.width + 1 && box.y + box.height <= viewport.height + 1;
  }, { message: 'Review drawer animation finishes with the panel fully inside the viewport' }).toBe(true);
  await page.screenshot({ path: screenshotPath('review'), animations: 'disabled' });

  await drawer.locator('button[aria-label="移除GET /v1/orders"]').click();
  await expect(drawer.locator('.role-review-api')).toHaveCount(0);
  await expect(undoStatus(page)).toBeVisible();
  await expect(undoStatus(page)).toContainText('已移除 1 个 API');

  await undoStatus(page).getByRole('button', { name: '撤销', exact: true }).click();
  await expect(undoStatus(page)).toHaveCount(0);
  await expect(drawer.locator('.role-review-api code')).toHaveText('GET /v1/orders');
});

test('任意深度(>=6)树：focus 本分支/返回完整树/上一级，搜索保留祖先，详情只列本节点直接按钮', async ({ page }) => {
  const state = await mockRoleEditor(page, { menus: DEEP_MENUS });

  await openNewRole(page);
  await fillDraft(page, '深层角色', 'deep-role');

  await expect(editor(page).locator('div[role="treeitem"]:has(> button[aria-label="配置深层按钮六"])')).toHaveAttribute('aria-level', '6');
  await expect(treeLabel(page, '深层按钮六')).toBeVisible();

  // Own-button scope: 深层层级四 lists only its direct button, not the grandchild button.
  await treeLabel(page, '深层层级四').click();
  await expect(tabLabel(page)).toHaveText('深层层级四');
  await expect(buttonCard(page, '四级直连按钮')).toBeVisible();
  await expect(buttonCard(page, '深层按钮六')).toHaveCount(0);
  await expect(editor(page).locator('.role-editor-child-link').filter({ hasText: '深层层级五' })).toBeVisible();

  // Focus this branch + 上一级 + 返回完整树.
  await editor(page).getByRole('button', { name: /聚焦本分支/ }).click();
  await expect(editor(page).locator('.role-tree-root')).toHaveText('当前树根：深层层级四');
  await expect(editor(page).getByRole('button', { name: /返回完整树/ })).toBeVisible();
  await expect(treeLabel(page, '无关菜单')).toHaveCount(0);
  await expect(treeLabel(page, '深层按钮六')).toBeVisible();

  await editor(page).getByRole('button', { name: '上一级' }).click();
  await expect(editor(page).locator('.role-tree-root')).toHaveText('当前树根：深层层级三');

  await editor(page).getByRole('button', { name: /返回完整树/ }).click();
  await expect(treeLabel(page, '无关菜单')).toBeVisible();

  // Searching a descendant keeps its ancestors visible and hides unrelated branches/siblings.
  const menuSearch = editor(page).getByPlaceholder('搜索菜单或按钮');
  await menuSearch.fill('深层按钮六');
  await expect(treeLabel(page, '深层层级一')).toBeVisible();
  await expect(treeLabel(page, '深层层级四')).toBeVisible();
  await expect(treeLabel(page, '深层按钮六')).toBeVisible();
  await expect(treeLabel(page, '四级直连按钮')).toHaveCount(0);
  await expect(treeLabel(page, '无关菜单')).toHaveCount(0);
  await menuSearch.fill('');

  // Clicking a button card grants the whole enabled ancestor chain.
  await treeLabel(page, '深层按钮六').click();
  await buttonCard(page, '深层按钮六').click();
  await expect(tab(page, /菜单与按钮/)).toHaveText('菜单与按钮 · 6');

  // Keep all six levels visible under a focused root, with the actual ancestor path in the detail.
  await treeLabel(page, '深层层级一').click();
  await editor(page).getByRole('button', { name: /聚焦本分支/ }).click();
  await treeLabel(page, '深层按钮六').click();
  await expect(editor(page).locator('.role-tree-root')).toHaveText('当前树根：深层层级一');
  await expect(editor(page).getByRole('button', { name: /返回完整树/ })).toBeVisible();
  await expect(treeLabel(page, '无关菜单')).toHaveCount(0);
  await expect(editor(page).locator('div[role="treeitem"]:has(> button[aria-label="配置深层按钮六"])')).toHaveAttribute('aria-level', '6');
  await expect(editor(page).locator('.role-editor-path')).toHaveText('深层层级一 / 深层层级二 / 深层层级三 / 深层层级四 / 深层层级五');
  await expect(buttonCard(page, '深层按钮六')).toHaveAttribute('aria-checked', 'true');
  await page.screenshot({ path: screenshotPath('deep'), fullPage: true, animations: 'disabled' });

  await saveButton(page).click();
  await expect(page).toHaveURL(/\/roles$/);
  expect(sortedIDs(state.saved[0].menu_ids)).toEqual(['deep-1', 'deep-2', 'deep-3', 'deep-4', 'deep-5', 'deep-5-btn']);
});

test('未保存修改：菜单导航与浏览器返回弹确认，tabs 切换不确认', async ({ page }) => {
  const state = await mockRoleEditor(page);

  await page.goto('/roles');
  await page.getByRole('button', { name: '新增角色', exact: true }).click();
  await expect(page).toHaveURL(/\/roles\/new$/);
  await fillDraft(page, '未保存角色', 'dirty-role');
  await expect(editor(page).locator('.role-editor-topline')).toContainText('有未保存的修改');

  // Tab switching is local state and never confirms.
  await tab(page, /API 接口/).click();
  await expect(page).toHaveURL(/\/roles\/new$/);
  await expect(page.locator('.t-dialog')).toBeHidden();
  await tab(page, /菜单与按钮/).click();

  // Sidebar navigation is blocked while dirty (a different nav item so the menu value actually changes).
  await page.locator('.t-menu__item').filter({ hasText: '服务资源' }).click();
  const dialog = page.locator('.t-dialog');
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText('还有未保存的修改');
  await expect(page).toHaveURL(/\/roles\/new$/);
  await dialog.getByRole('button', { name: '继续编辑' }).click();
  await expect(page).toHaveURL(/\/roles\/new$/);
  await expect(dialog).toBeHidden();
  await expect(page.locator('.t-loading__fullscreen')).toBeHidden();
  await expect(field(page, '角色名称')).toHaveValue('未保存角色');

  // Browser back is blocked too; 放弃修改并离开 proceeds back to the previous route.
  await page.goBack();
  await expect(dialog).toBeVisible();
  await dialog.getByRole('button', { name: '放弃修改并离开' }).click();
  await expect(page).toHaveURL(/\/roles$/);
  expect(state.saved).toHaveLength(0);
});

test('加载失败可重试、保存失败保留草稿、保存中防重复提交', async ({ page }) => {
  const state = await mockRoleEditor(page);
  state.endpointsFailures = 1;

  await page.goto('/roles/new');
  await expect(alerts(page)).toContainText('接口列表加载失败');
  await expect(saveButton(page)).toHaveCount(0);

  await editor(page).getByRole('button', { name: '重试' }).click();
  await expect(infoSection(page)).toBeVisible();
  await expect(alerts(page)).toHaveCount(0);

  await fillDraft(page, '失败角色', 'fail-role', '草稿保留');
  state.saveFailures = 1;
  await saveButton(page).click();
  await expect(alerts(page)).toContainText('保存角色失败');
  await expect(page).toHaveURL(/\/roles\/new$/);
  await expect(field(page, '角色名称')).toHaveValue('失败角色');
  await expect(field(page, '角色编码')).toHaveValue('fail-role');
  await expect(field(page, '角色说明')).toHaveValue('草稿保留');
  expect(state.saved).toHaveLength(0);

  await saveButton(page).click();
  await expect(page).toHaveURL(/\/roles$/);
  expect(state.saved).toHaveLength(1);

  // Saving in-flight disables the button and a second submit is ignored.
  await page.getByRole('button', { name: '新增角色', exact: true }).click();
  await fillDraft(page, '双提交角色', 'double-submit');
  state.saveDelayMs = 1000;
  await saveButton(page).click();
  await expect(saveButton(page)).toBeDisabled();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/\/roles$/);
  expect(state.saved).toHaveLength(2);
});

test('不活动服务与缺失菜单：须先移除不可用授权才能保存', async ({ page }) => {
  const staleRole: RoleSeed = { id: 'role-stale', code: 'stale-role', name: '历史角色', description: '', enabled: true, endpoint_ids: ['ep-orders-list', 'ep-legacy-list'], menu_ids: ['menu-orders', 'menu-vanished'] };
  const state = await mockRoleEditor(page, { roles: [staleRole] });

  await page.goto('/roles/role-stale/edit');
  await expect(infoSection(page)).toBeVisible();
  await expect(alerts(page)).toContainText('移除不可用授权后才能保存');
  await expect(saveButton(page)).toBeDisabled();

  await editor(page).getByRole('button', { name: '移除不可用授权' }).click();
  await expect(alerts(page)).toHaveCount(0);
  await expect(saveButton(page)).toBeEnabled();

  await saveButton(page).click();
  await expect(page).toHaveURL(/\/roles$/);
  expect(state.saved[0].endpoint_ids).toEqual(['ep-orders-list']);
  expect(state.saved[0].menu_ids).toEqual(['menu-orders']);
});

test('编辑不存在的角色显示角色不存在且不可误创建', async ({ page }) => {
  const state = await mockRoleEditor(page, { roles: [editRoleSeed] });

  await page.goto('/roles/missing-role/edit');
  await expect(editor(page).getByText('角色不存在')).toBeVisible();
  await expect(saveButton(page)).toHaveCount(0);
  expect(state.saved).toHaveLength(0);
});

test('移动端无横向溢出（无截图）', async ({ page }) => {
  await mockRoleEditor(page);
  await page.setViewportSize({ width: 390, height: 844 });

  await openNewRole(page);
  await fillDraft(page, '移动角色', 'mobile-role');
  await expectNoHorizontalOverflow(page);

  await tab(page, /API 接口/).click();
  await expectNoHorizontalOverflow(page);

  await tab(page, /菜单与按钮/).click();
  await expectNoHorizontalOverflow(page);
});
