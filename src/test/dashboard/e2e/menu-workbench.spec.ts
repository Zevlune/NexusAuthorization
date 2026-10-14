import { createRequire } from 'node:module';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import type { Locator, Page } from '../../../../dashboard/node_modules/@playwright/test';

// Resolve Playwright from the Dashboard package while the suite lives at the repository root.
const requireDashboardDependency = createRequire(new URL('../../../../dashboard/package.json', import.meta.url));
const { expect, test } = requireDashboardDependency('@playwright/test') as typeof import('../../../../dashboard/node_modules/@playwright/test');

// Frozen scope for the rewritten menu workbench (dashboard/src/pages/policy-workbench/components/menus.tsx
// + menus-parent-picker.tsx). The page only talks to /api/v1/rbac-v1/menus (GET list, POST save, DELETE by id);
// every route below is mocked, so no real menu or permission data is touched.
//
// New UI contract asserted by this suite:
//   - only two resource types exist: menu / button. The old 目录 is gone; the 订单中心 root is now a menu that can parent buttons.
//   - the topline merges a single primary 添加 button beside the breadcrumb inside .rbac-page-topline; it opens 新增菜单
//     and the drawer Radio switches to 按钮. There is no second 新增菜单 / 新增按钮 entry; row 添加按钮 stays as a shortcut.
//   - layout follows the API page: the standalone big title / 说明 is gone; the filter bar is its own .page-filter-bar Card
//     with an inline .workbench-filter-form (.workbench-keyword + .resource-menus-type-filter.workbench-select + 查询/重置);
//     edits are draft-only and only apply on 查询, while 重置 clears every condition.
//   - the table is a separate .resource-menus-panel Card holding .resource-menus-table; six columns 名称/类型/权限标识/路由/排序/操作.
//   - EnhancedTable renders the recursive tree; only root nodes are expanded by default, deeper levels open via the row tree icon.
//   - search / type filter keep every ancestor of a hit and auto-expand the matched path.
//   - 菜单 rows expose 编辑 + 添加按钮 + 更多操作 (Dropdown 添加子菜单/删除); 按钮 rows expose 编辑 + 删除 only.
//   - 560px TDesign Drawer; 菜单 keeps route/component; 按钮 hides route/component and clears route/component/icon in the payload.
//   - parent picker is a TDesign TreeSelect (.menus-parent-picker, readonly trigger) with a popup search Input;
//     arbitrary-depth full-path search, buttons/self/descendants disabled with a reason, selected value echoes the full path.
//   - pagination pages complete root subtrees (25/50/100); the left counter counts every node, not just roots.
//   - delete uses a TDesign Dialog; a node that really has children cannot be confirmed (guard survives filtering).

const APP_KEY = 'console';
const SCREENSHOT_DIR = fileURLToPath(new URL('../../../../ui/verification/', import.meta.url));
mkdirSync(SCREENSHOT_DIR, { recursive: true });
type ShotName = 'list' | 'parent' | 'button' | 'edit-menu' | 'edit-button' | 'delete';
const screenshotPath = (name: ShotName) => join(SCREENSHOT_DIR, `menu-workbench-implemented-${name}.png`);

type MenuSeed = {
  id: string;
  parent_id: string | null;
  code: string;
  name: string;
  menu_type: 'menu' | 'button';
  route: string;
  component: string;
  icon: string;
  sort_order: number;
  enabled: boolean;
};

type SavedMenu = Record<string, unknown>;

// Behaviour fixture: 订单中心 used to be a directory and is now a menu that owns a nested menu (订单处理) plus two
// buttons, so one edit drawer can exercise self / non-button descendant / button descendant disabling and
// 有子项不可改为按钮, while a fresh button can attach to 订单中心 or 订单管理.
const MENUS: MenuSeed[] = [
  { id: 'menu-orders-center', parent_id: null, code: 'orders-center', name: '订单中心', menu_type: 'menu', route: '', component: '', icon: '', sort_order: 1, enabled: true },
  { id: 'menu-orders', parent_id: 'menu-orders-center', code: 'orders', name: '订单管理', menu_type: 'menu', route: '/orders', component: '/pages/orders', icon: 'app', sort_order: 1, enabled: true },
  { id: 'menu-orders-process', parent_id: 'menu-orders', code: 'orders:process', name: '订单处理', menu_type: 'menu', route: '/orders/process', component: '/pages/orders/process', icon: '', sort_order: 2, enabled: true },
  { id: 'btn-orders-audit', parent_id: 'menu-orders', code: 'orders:audit', name: '审核订单', menu_type: 'button', route: '', component: '', icon: '', sort_order: 1, enabled: true },
  { id: 'btn-orders-export', parent_id: 'menu-orders', code: 'orders:export', name: '导出订单', menu_type: 'button', route: '', component: '', icon: '', sort_order: 2, enabled: true },
  { id: 'menu-reports', parent_id: null, code: 'reports', name: '报表管理', menu_type: 'menu', route: '/reports', component: '/pages/reports', icon: '', sort_order: 2, enabled: true },
  { id: 'menu-legacy', parent_id: null, code: 'legacy', name: '历史菜单', menu_type: 'menu', route: '/legacy', component: '', icon: '', sort_order: 9, enabled: false },
];

// Arbitrary depth fixture: six nested menu levels (深层层级六 is the leaf at tree level 6) plus a button sibling.
const DEEP_MENUS: MenuSeed[] = [
  { id: 'deep-1', parent_id: null, code: 'deep-1', name: '深层层级一', menu_type: 'menu', route: '', component: '', icon: '', sort_order: 1, enabled: true },
  { id: 'deep-2', parent_id: 'deep-1', code: 'deep-2', name: '深层层级二', menu_type: 'menu', route: '', component: '', icon: '', sort_order: 1, enabled: true },
  { id: 'deep-3', parent_id: 'deep-2', code: 'deep-3', name: '深层层级三', menu_type: 'menu', route: '', component: '', icon: '', sort_order: 1, enabled: true },
  { id: 'deep-3-btn', parent_id: 'deep-3', code: 'deep-3-btn', name: '三级按钮', menu_type: 'button', route: '', component: '', icon: '', sort_order: 1, enabled: true },
  { id: 'deep-4', parent_id: 'deep-3', code: 'deep-4', name: '深层层级四', menu_type: 'menu', route: '/deep-4', component: '', icon: '', sort_order: 1, enabled: true },
  { id: 'deep-5', parent_id: 'deep-4', code: 'deep-5', name: '深层层级五', menu_type: 'menu', route: '/deep-5', component: '', icon: '', sort_order: 1, enabled: true },
  { id: 'deep-6', parent_id: 'deep-5', code: 'deep-6', name: '深层层级六', menu_type: 'menu', route: '/deep-6', component: '', icon: '', sort_order: 1, enabled: true },
  { id: 'deep-other', parent_id: null, code: 'deep-other', name: '无关菜单', menu_type: 'menu', route: '/deep-other', component: '', icon: '', sort_order: 2, enabled: true },
];

// Design fixture: mirrors the ui/ui.pen list so screenshots can be diffed against the pen one-to-one (4 nodes: one menu + two buttons + one menu).
const DESIGN_MENUS: MenuSeed[] = [
  { id: 'menu-orders', parent_id: null, code: 'orders', name: '订单管理', menu_type: 'menu', route: '/orders', component: '', icon: '', sort_order: 10, enabled: true },
  { id: 'btn-orders-query', parent_id: 'menu-orders', code: 'orders:query', name: '查询订单', menu_type: 'button', route: '', component: '', icon: '', sort_order: 10, enabled: true },
  { id: 'btn-orders-create', parent_id: 'menu-orders', code: 'orders:create', name: '创建订单', menu_type: 'button', route: '', component: '', icon: '', sort_order: 20, enabled: true },
  { id: 'menu-users', parent_id: null, code: 'users', name: '用户管理', menu_type: 'menu', route: '/users', component: '', icon: '', sort_order: 20, enabled: true },
];

/** 30 root menus each with one child button: enough to force a second page while keeping subtrees intact. */
const pagedMenus = (count = 30): MenuSeed[] => Array.from({ length: count }, (_, index) => {
  const seq = String(index + 1).padStart(2, '0');
  const rootId = `page-root-${seq}`;
  return [
    { id: rootId, parent_id: null, code: `page:root:${seq}`, name: `分页菜单${seq}`, menu_type: 'menu', route: `/page/${seq}`, component: '', icon: '', sort_order: index + 1, enabled: true },
    { id: `page-child-${seq}`, parent_id: rootId, code: `page:child:${seq}`, name: `分页按钮${seq}`, menu_type: 'button', route: '', component: '', icon: '', sort_order: 1, enabled: true },
  ] as MenuSeed[];
}).flat();

type MenuMock = { menus: MenuSeed[]; saved: SavedMenu[]; deletes: string[]; menuFailures: number };

// Single catch-all router. Everything under /api/** is answered from fixtures; unrelated requests fall
// back to the Vite dev server. No real backend is contacted.
async function mockMenuWorkbench(page: Page, options: { menus?: MenuSeed[]; failMenus?: number } = {}): Promise<MenuMock> {
  const state: MenuMock = {
    menus: (options.menus ?? MENUS).map(menu => ({ ...menu })),
    saved: [],
    deletes: [],
    menuFailures: options.failMenus ?? 0,
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
      await route.fulfill({ json: { success: true, result: { items: [] } } });
      return;
    }
    if (path === '/api/v1/rbac-v1/endpoints') {
      await route.fulfill({ json: { success: true, result: { items: [], total: 0, page: 1, page_size: 100 } } });
      return;
    }
    if (path === '/api/v1/rbac-v1/menus') {
      if (request.method() === 'GET' && state.menuFailures > 0) {
        state.menuFailures -= 1;
        await route.fulfill({ status: 502, json: { success: false, errorMessage: '菜单列表加载失败' } });
        return;
      }
      if (request.method() === 'POST') {
        const body = request.postDataJSON() as SavedMenu;
        state.saved.push(body);
        const next: MenuSeed = {
          id: String(body.id ?? `menu-created-${state.saved.length}`),
          parent_id: (body.parent_id as string | null) ?? null,
          code: String(body.code ?? ''),
          name: String(body.name ?? ''),
          menu_type: (body.menu_type as MenuSeed['menu_type']) ?? 'menu',
          route: String(body.route ?? ''),
          component: String(body.component ?? ''),
          icon: String(body.icon ?? ''),
          sort_order: Number(body.sort_order ?? 0),
          enabled: body.enabled !== false,
        };
        const index = state.menus.findIndex(menu => menu.id === next.id);
        if (index >= 0) state.menus[index] = next;
        else state.menus.push(next);
        await route.fulfill({ json: { success: true, result: next } });
        return;
      }
      await route.fulfill({ json: { success: true, result: { items: state.menus } } });
      return;
    }
    const menuItem = path.match(/^\/api\/v1\/rbac-v1\/menus\/([^/]+)$/);
    if (menuItem && request.method() === 'DELETE') {
      const id = decodeURIComponent(menuItem[1]);
      state.deletes.push(id);
      state.menus = state.menus.filter(menu => menu.id !== id);
      await route.fulfill({ json: { success: true, result: null } });
      return;
    }
    await route.fulfill({ json: { success: true, result: { items: [] } } });
  });

  return state;
}

const filterBar = (page: Page): Locator => page.locator('.page-filter-bar');
const filterForm = (page: Page): Locator => page.locator('.page-filter-bar .workbench-filter-form');
const keywordInput = (page: Page): Locator => page.getByPlaceholder('搜索菜单名称或权限标识');
const typeSelect = (page: Page): Locator => page.locator('.page-filter-bar .resource-menus-type-filter.workbench-select');
const addButton = (page: Page): Locator => page.locator('.rbac-page-topline').getByRole('button', { name: '添加', exact: true });
const toplineBreadcrumb = (page: Page): Locator => page.locator('.rbac-page-topline .rbac-breadcrumb');
const panel = (page: Page): Locator => page.locator('.resource-menus-panel');
const rows = (page: Page): Locator => page.locator('.resource-menus-table tbody tr');
const row = (page: Page, name: string): Locator => rows(page).filter({ hasText: name });
const headerCells = (page: Page): Locator => page.locator('.resource-menus-table thead th');
const treeIcon = (page: Page, name: string): Locator => row(page, name).locator('.t-table__tree-op-icon');
const moreButton = (page: Page, name: string): Locator => row(page, name).getByRole('button', { name: `更多操作：${name}` });
const drawer = (page: Page): Locator => page.locator('.resource-menus-drawer');
const field = (page: Page, name: 'name' | 'code' | 'route' | 'component' | 'sort_order' | 'parent_id'): Locator => drawer(page).locator(`.t-form-item__${name} input`).first();
const typeRadio = (page: Page, label: '菜单' | '按钮'): Locator => drawer(page).locator('.resource-menus-type-radio .t-radio').filter({ hasText: label });
const picker = (page: Page): Locator => page.locator('.menus-parent-picker');
const pickerPopup = (page: Page): Locator => page.locator('.menus-parent-picker-overlay');
const pickerSearch = (page: Page): Locator => page.locator('.menus-parent-picker-search input');
const pickerNodes = (page: Page): Locator => page.locator('.menus-parent-picker-overlay .menus-parent-picker-node');
const escapeRe = (value: string) => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
const pickerNode = (page: Page, name: string): Locator => pickerNodes(page).filter({ hasText: new RegExp(`^${escapeRe(name)}`) });
const dropdownItem = (page: Page, text: string): Locator => page.locator('.t-dropdown__item').filter({ hasText: new RegExp(`^${text}$`) });

async function settle(page: Page) {
  await page.evaluate(() => new Promise<void>(resolve => {
    requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
  }));
}

// Wait until the node's real bounding rect sits inside the viewport, so a screenshot never catches it mid-animation.
async function waitInViewport(page: Page, locator: Locator) {
  const viewport = page.viewportSize();
  await expect.poll(async () => {
    const box = await locator.boundingBox();
    if (!box || !viewport) return null;
    const insideX = box.x >= -1 && box.x + box.width <= viewport.width + 1;
    const insideY = box.y >= -1 && box.y + box.height <= viewport.height + 1;
    return insideX && insideY ? Math.round(box.width * box.height) : null;
  }, { timeout: 8000 }).not.toBeNull();
  await settle(page);
}

async function expectSameRow(page: Page, left: Locator, right: Locator) {
  const leftBox = await left.boundingBox();
  const rightBox = await right.boundingBox();
  expect(leftBox).not.toBeNull();
  expect(rightBox).not.toBeNull();
  if (!leftBox || !rightBox) return;
  const overlap = Math.min(leftBox.y + leftBox.height, rightBox.y + rightBox.height) - Math.max(leftBox.y, rightBox.y);
  expect(overlap).toBeGreaterThan(0);
}

// Filter fields are drafts: typing/selecting alone never changes the table; only 查询 commits them.
async function draftKeyword(page: Page, value: string) {
  await keywordInput(page).fill(value);
  await settle(page);
}

async function draftType(page: Page, label: '全部类型' | '菜单' | '按钮') {
  await page.locator('.page-filter-bar .resource-menus-type-filter input').first().click();
  await page.locator('.t-select-option').filter({ hasText: new RegExp(`^${label}$`) }).first().click();
  await expect(page.locator('.t-select-option').first()).toBeHidden();
}

async function applyQuery(page: Page) {
  await filterBar(page).getByRole('button', { name: '查询', exact: true }).click();
  await settle(page);
}

async function resetQuery(page: Page) {
  await filterBar(page).getByRole('button', { name: '重置', exact: true }).click();
  await settle(page);
}

async function expandRow(page: Page, name: string) {
  await treeIcon(page, name).click();
  await settle(page);
}

// The topline merges one 添加 button; it defaults to 新增菜单 and the drawer Radio can switch it to a button.
async function openCreate(page: Page, kind: 'menu' | 'button' = 'menu') {
  await addButton(page).click();
  await expect(drawer(page).locator('.resource-menus-form')).toBeVisible();
  await settle(page);
  if (kind === 'button') {
    await typeRadio(page, '按钮').click();
    await settle(page);
  }
}

async function closeDrawer(page: Page) {
  await drawer(page).getByRole('button', { name: '取消', exact: true }).click();
  // The closed Drawer keeps a pointer-events:none shell in the DOM, so close on the unmounted form.
  await expect(drawer(page).locator('.resource-menus-form')).toHaveCount(0);
  await settle(page);
}

async function openPicker(page: Page) {
  await picker(page).click();
  await expect(pickerPopup(page)).toBeVisible();
  await settle(page);
}

async function closePicker(page: Page) {
  await picker(page).click();
  await expect(pickerPopup(page)).toBeHidden();
}

async function expectNoHorizontalOverflow(page: Page) {
  const metrics = await page.evaluate(() => {
    const root = document.querySelector<HTMLElement>('.resource-menus');
    return {
      rootScroll: root?.scrollWidth ?? 0,
      rootClient: root?.clientWidth ?? 0,
      docScroll: document.documentElement.scrollWidth,
      docClient: document.documentElement.clientWidth,
    };
  });
  if (metrics.rootClient) expect(metrics.rootScroll).toBeLessThanOrEqual(metrics.rootClient + 1);
  expect(metrics.docScroll).toBeLessThanOrEqual(metrics.docClient + 1);
}

async function rowNames(page: Page): Promise<string[]> {
  return rows(page).evaluateAll(list => list.map(item => item.querySelector('td')?.textContent?.trim() ?? ''));
}

async function expectRowNames(page: Page, expected: string[]) {
  await expect.poll(() => rowNames(page), { timeout: 8000 }).toEqual(expected);
}

async function shoot(page: Page, name: ShotName, fullPage = false) {
  await settle(page);
  await page.screenshot({ path: screenshotPath(name), fullPage, animations: 'disabled' });
}

const sortedKeys = (value: unknown): string[] => Object.keys((value as Record<string, unknown>) ?? {}).sort();
const CREATE_KEYS = ['code', 'component', 'enabled', 'icon', 'menu_type', 'name', 'parent_id', 'route', 'sort_order'];
const EDIT_KEYS = ['code', 'component', 'enabled', 'icon', 'id', 'menu_type', 'name', 'parent_id', 'route', 'sort_order'];

test.use({ viewport: { width: 1440, height: 1040 } });

test('列表：顶栏添加与面包屑同行、筛选卡片与表格卡片分离、行操作按类型分派', async ({ page }) => {
  await mockMenuWorkbench(page);
  await page.goto('/menus');

  // 独立大标题/说明已移除，顶栏只剩面包屑 + 单个主按钮 添加。
  await expect(page.getByRole('heading', { name: '菜单与按钮' })).toHaveCount(0);
  await expect(page.locator('.resource-menus-heading')).toHaveCount(0);
  await expect(toplineBreadcrumb(page)).toContainText('菜单与按钮');
  await expect(addButton(page)).toBeVisible();
  await expectSameRow(page, toplineBreadcrumb(page), addButton(page));
  await expect(page.getByRole('button', { name: '新增菜单', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: '新增按钮', exact: true })).toHaveCount(0);

  // 筛选栏独立卡片 + inline Form；类型选择保留 .resource-menus-type-filter 并带 .workbench-select。
  await expect(filterBar(page)).toBeVisible();
  await expect(filterForm(page)).toBeVisible();
  await expect(page.locator('.page-filter-bar .workbench-keyword.resource-menus-search')).toHaveCount(1);
  await expect(typeSelect(page)).toHaveCount(1);
  await expect(filterBar(page).getByRole('button', { name: '查询', exact: true })).toBeVisible();
  await expect(filterBar(page).getByRole('button', { name: '重置', exact: true })).toBeVisible();

  // 表格独立卡片，筛选栏不在表格卡片内部。
  await expect(panel(page)).toBeVisible();
  await expect(panel(page).locator('.resource-menus-table')).toBeVisible();
  await expect(panel(page).locator('.page-filter-bar')).toHaveCount(0);
  await expect(page.locator('.page-filter-bar .resource-menus-table')).toHaveCount(0);

  // Six fixed columns; the legacy 启用 column is gone.
  expect(await headerCells(page).allTextContents()).toEqual(['名称', '类型', '权限标识', '路由', '排序', '操作']);
  await expect(page.getByRole('button', { name: '展开全部', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: '收起全部', exact: true })).toHaveCount(0);

  // Default expansion: only root nodes are open, so 订单管理's children stay hidden until the tree icon is used.
  await expectRowNames(page, ['订单中心', '订单管理', '报表管理', '历史菜单']);
  await expect(row(page, '订单处理')).toHaveCount(0);
  await expect(page.locator('.resource-menus-footer')).toContainText('共 7 条');

  // Button rows are leaves: 编辑 + 删除 only.
  await expandRow(page, '订单管理');
  await expectRowNames(page, ['订单中心', '订单管理', '审核订单', '导出订单', '订单处理', '报表管理', '历史菜单']);
  const buttonRow = row(page, '审核订单');
  await expect(buttonRow.getByRole('button', { name: '编辑', exact: true })).toHaveCount(1);
  await expect(buttonRow.getByRole('button', { name: '删除', exact: true })).toHaveCount(1);
  await expect(buttonRow.getByRole('button', { name: '添加按钮', exact: true })).toHaveCount(0);
  await expect(buttonRow.getByRole('button', { name: /更多操作/ })).toHaveCount(0);

  // 菜单 rows own children: 编辑 + 添加按钮 + 更多操作 (row 快捷入口保留).
  const menuRow = row(page, '订单管理');
  await expect(menuRow.getByRole('button', { name: '编辑', exact: true })).toHaveCount(1);
  await expect(menuRow.getByRole('button', { name: '添加按钮', exact: true })).toHaveCount(1);
  await expect(moreButton(page, '订单管理')).toHaveCount(1);

  // 订单中心 was a directory and is now a menu, so it keeps the menu row actions (no 目录 type exists anymore).
  const formerDirectoryRow = row(page, '订单中心');
  await expect(formerDirectoryRow.locator('td').nth(1)).toHaveText('菜单');
  await expect(formerDirectoryRow.getByRole('button', { name: '编辑', exact: true })).toHaveCount(1);
  await expect(formerDirectoryRow.getByRole('button', { name: '添加按钮', exact: true })).toHaveCount(1);
  await expect(moreButton(page, '订单中心')).toHaveCount(1);

  // 更多操作 opens the child/delete Dropdown for non-button rows.
  await moreButton(page, '订单中心').click();
  await expect(dropdownItem(page, '添加子菜单')).toBeVisible();
  await expect(dropdownItem(page, '删除')).toBeVisible();
  await page.keyboard.press('Escape');
  await settle(page);

  await expectNoHorizontalOverflow(page);
});

test('筛选：草稿输入点查询才生效、命中子节点保留祖先、重置清空所有条件', async ({ page }) => {
  await mockMenuWorkbench(page);
  await page.goto('/menus');

  // Draft only: typing filters nothing until 查询.
  await draftKeyword(page, '订单处理');
  await expectRowNames(page, ['订单中心', '订单管理', '报表管理', '历史菜单']);
  await applyQuery(page);
  await expectRowNames(page, ['订单中心', '订单管理', '订单处理']);

  // Keyword hits name/权限标识, keeps ancestors and auto-expands the path.
  await draftKeyword(page, 'orders:audit');
  await applyQuery(page);
  await expectRowNames(page, ['订单中心', '订单管理', '审核订单']);

  await draftKeyword(page, '报表');
  await applyQuery(page);
  await expectRowNames(page, ['报表管理']);
  await expect(page.locator('.resource-menus-footer')).toContainText('共 1 条');

  await draftKeyword(page, 'zzz');
  await applyQuery(page);
  await expect(rows(page)).toHaveCount(1);
  await expect(page.locator('.rbac-empty')).toContainText('没有匹配的菜单或按钮');

  // 重置 clears the keyword filter and every other condition.
  await resetQuery(page);
  await expect(keywordInput(page)).toHaveValue('');
  await expectRowNames(page, ['订单中心', '订单管理', '报表管理', '历史菜单']);
  await expect(page.locator('.resource-menus-footer')).toContainText('共 7 条');

  // Type filter is a draft too: picking a type changes nothing until 查询, and keeps matching nodes plus ancestors.
  await draftType(page, '按钮');
  await expectRowNames(page, ['订单中心', '订单管理', '报表管理', '历史菜单']);
  await applyQuery(page);
  await expectRowNames(page, ['订单中心', '订单管理', '审核订单', '导出订单']);
  await expect(row(page, '订单处理')).toHaveCount(0);

  await draftType(page, '菜单');
  await applyQuery(page);
  await expectRowNames(page, ['订单中心', '订单管理', '订单处理', '报表管理', '历史菜单']);
  await expect(page.locator('.resource-menus-footer')).toContainText('共 5 条');

  // 重置 also restores the type select to 全部类型.
  await resetQuery(page);
  await expect(page.locator('.page-filter-bar .resource-menus-type-filter input')).toHaveValue('全部类型');
  await expectRowNames(page, ['订单中心', '订单管理', '报表管理', '历史菜单']);
});

test('六级树：默认仅根展开并逐层展开到第六层', async ({ page }) => {
  await mockMenuWorkbench(page, { menus: DEEP_MENUS });
  await page.goto('/menus');

  await expectRowNames(page, ['深层层级一', '深层层级二', '无关菜单']);
  await expect(row(page, '深层层级一')).toHaveClass(/t-table-tr--level-0/);
  await expect(row(page, '深层层级二')).toHaveClass(/t-table-tr--level-1/);
  await expect(row(page, '深层层级六')).toHaveCount(0);

  await expandRow(page, '深层层级二');
  await expect(row(page, '深层层级三')).toHaveClass(/t-table-tr--level-2/);

  await expandRow(page, '深层层级三');
  await expect(row(page, '三级按钮')).toHaveClass(/t-table-tr--level-3/);
  await expect(row(page, '深层层级四')).toHaveClass(/t-table-tr--level-3/);

  await expandRow(page, '深层层级四');
  await expect(row(page, '深层层级五')).toHaveClass(/t-table-tr--level-4/);

  await expandRow(page, '深层层级五');
  await expect(row(page, '深层层级六')).toHaveClass(/t-table-tr--level-5/);
  await expect(page.locator('.resource-menus-footer')).toContainText('共 8 条');
});

test('分页：25/50/100 顶层分页保留完整子树，左侧统计所有节点', async ({ page }) => {
  await mockMenuWorkbench(page, { menus: pagedMenus(30) });
  await page.goto('/menus');

  // 30 roots + 30 children = 60 nodes; every root is expanded by default.
  await expect(page.locator('.resource-menus-footer')).toContainText('共 60 条');
  await expect(rows(page)).toHaveCount(50);
  await expect(row(page, '分页菜单01')).toBeVisible();
  await expect(row(page, '分页按钮01')).toBeVisible();

  await page.locator('.t-pagination__number').filter({ hasText: /^2$/ }).click();
  // Page 2 holds roots 26-30 with their children directly beneath them (no detached subtree).
  await expectRowNames(page, [
    '分页菜单26', '分页按钮26', '分页菜单27', '分页按钮27', '分页菜单28', '分页按钮28',
    '分页菜单29', '分页按钮29', '分页菜单30', '分页按钮30',
  ]);

  await page.locator('.resource-menus-footer .t-pagination__select input').click();
  await page.locator('.t-select-option').filter({ hasText: /^50 条\/页$/ }).click();
  await expect(rows(page)).toHaveCount(60);
  await expect(row(page, '分页菜单30')).toBeVisible();
});

test('父级选择：TreeSelect 全路径搜索、禁用原因、选中回显并保存', async ({ page }) => {
  const state = await mockMenuWorkbench(page, { menus: DEEP_MENUS });
  await page.goto('/menus');

  await openCreate(page, 'menu');
  await expect(typeRadio(page, '菜单')).toHaveClass(/t-is-checked/);
  await field(page, 'name').fill('深层新菜单');
  await field(page, 'code').fill('deep:new');

  await openPicker(page);
  // 根节点 + the eight fixture nodes.
  await expect(pickerNodes(page)).toHaveCount(9);
  await expect(pickerNode(page, '根节点')).toHaveClass(/is-selected/);

  // Full-path search narrows the six-level tree to the matching leaf and its ancestor chain.
  const fullPath = '深层层级一 / 深层层级二 / 深层层级三 / 深层层级四 / 深层层级五 / 深层层级六';
  await pickerSearch(page).fill(fullPath);
  await settle(page);
  await expect(pickerNode(page, '深层层级六')).toBeVisible();
  await expect(pickerNode(page, '无关菜单')).toHaveCount(0);

  await pickerSearch(page).fill('');
  await settle(page);
  await expect(pickerNode(page, '三级按钮')).toHaveClass(/is-disabled/);
  await expect(pickerNode(page, '三级按钮').locator('.menus-parent-picker-node-reason')).toHaveText('按钮 · 不可作为上级');

  // A disabled node refuses selection and keeps the popup open.
  await pickerNode(page, '三级按钮').locator('.menus-parent-picker-node-name').click();
  await settle(page);
  await expect(pickerPopup(page)).toBeVisible();
  await expect(picker(page).locator('input')).toHaveValue('根节点');

  await pickerNode(page, '深层层级五').locator('.menus-parent-picker-node-name').click();
  await expect(pickerPopup(page)).toBeHidden();
  const levelFivePath = '深层层级一 / 深层层级二 / 深层层级三 / 深层层级四 / 深层层级五';
  await expect(picker(page).locator('input')).toHaveValue(levelFivePath);

  await openPicker(page);
  await expect(pickerNode(page, '深层层级五')).toHaveClass(/is-selected/);
  await expect(page.locator('.menus-parent-picker-note')).toContainText('自身和后代不可选择');
  await closePicker(page);

  const saveRequest = page.waitForRequest(request => request.method() === 'POST' && request.url().includes('/api/v1/rbac-v1/menus'));
  await drawer(page).getByRole('button', { name: '保存菜单', exact: true }).click();
  await saveRequest;
  await expect(drawer(page).locator('.resource-menus-form')).toHaveCount(0);

  expect(state.saved).toHaveLength(1);
  expect(state.saved[0]).toMatchObject({ name: '深层新菜单', code: 'deep:new', menu_type: 'menu', parent_id: 'deep-5' });
  await expect(row(page, '深层新菜单')).toHaveClass(/t-table-tr--level-5/);
});

test('编辑：自身/后代/按钮禁用并给出原因，有子项不可改为按钮', async ({ page }) => {
  await mockMenuWorkbench(page);
  await page.goto('/menus');

  await row(page, '订单管理').getByRole('button', { name: '编辑', exact: true }).click();
  await expect(drawer(page)).toBeVisible();
  await expect(drawer(page).locator('.t-drawer__header')).toContainText('编辑菜单');
  const drawerBody = await drawer(page).locator('.t-drawer__body').boundingBox();
  expect(Math.round(drawerBody?.width ?? 0)).toBe(560);

  // Two resource types only (菜单/按钮), and 订单管理 owns children so 按钮 is locked.
  await expect(drawer(page).locator('.resource-menus-type-radio .t-radio')).toHaveCount(2);
  await expect(typeRadio(page, '按钮').locator('input')).toBeDisabled();
  await expect(drawer(page).locator('.t-form-item__code input')).toBeDisabled();

  await openPicker(page);
  await expect(pickerNode(page, '订单管理')).toHaveClass(/is-disabled/);
  await expect(pickerNode(page, '订单处理')).toHaveClass(/is-disabled/);
  await expect(pickerNode(page, '审核订单')).toHaveClass(/is-disabled/);
  await expect(pickerNode(page, '订单中心')).not.toHaveClass(/is-disabled/);
  await expect(pickerNode(page, '报表管理')).not.toHaveClass(/is-disabled/);

  await expect(pickerNode(page, '订单管理').locator('.menus-parent-picker-node-reason')).toHaveText('当前编辑节点');
  await expect(pickerNode(page, '订单处理').locator('.menus-parent-picker-node-reason')).toHaveText('当前节点的子级');
  await expect(pickerNode(page, '审核订单').locator('.menus-parent-picker-node-reason')).toHaveText('按钮 · 不可作为上级');
  await expect(pickerNode(page, '订单中心').locator('.menus-parent-picker-node-reason')).toHaveCount(0);
  await expect(pickerNode(page, '报表管理').locator('.menus-parent-picker-node-reason')).toHaveCount(0);
  await expect(page.locator('.menus-parent-picker-note')).toContainText('自身和后代不可选择');
});

test('保存：新建菜单 payload 与 编辑保留原 icon', async ({ page }) => {
  const state = await mockMenuWorkbench(page);
  await page.goto('/menus');

  // Create a menu at the root: exactly the original nine keys, 根节点 mapped back to parent_id: null.
  await openCreate(page, 'menu');
  await field(page, 'name').fill('新建菜单');
  await field(page, 'code').fill('new-menu');
  await field(page, 'route').fill('/new-menu');
  await field(page, 'component').fill('/pages/new-menu');
  await expect(picker(page).locator('input')).toHaveValue('根节点');
  const createMenuRequest = page.waitForRequest(request => request.method() === 'POST' && request.url().includes('/api/v1/rbac-v1/menus'));
  await drawer(page).getByRole('button', { name: '保存菜单', exact: true }).click();
  await createMenuRequest;
  await expect(drawer(page).locator('.resource-menus-form')).toHaveCount(0);

  expect(state.saved).toHaveLength(1);
  expect(sortedKeys(state.saved[0])).toEqual(CREATE_KEYS);
  expect(state.saved[0]).toEqual({
    code: 'new-menu',
    name: '新建菜单',
    menu_type: 'menu',
    parent_id: null,
    route: '/new-menu',
    component: '/pages/new-menu',
    icon: '',
    sort_order: 0,
    enabled: true,
  });

  // Edit 订单管理: no icon UI, but the original icon value survives the round-trip.
  await row(page, '订单管理').getByRole('button', { name: '编辑', exact: true }).click();
  await expect(drawer(page).locator('.resource-menus-form')).toBeVisible();
  await expect(drawer(page).locator('.t-form-item__icon')).toHaveCount(0);
  await expect(field(page, 'code')).toBeDisabled();
  await expect(field(page, 'code')).toHaveValue('orders');
  await field(page, 'name').fill('订单中心管理');
  const editRequest = page.waitForRequest(request => request.method() === 'POST' && request.url().includes('/api/v1/rbac-v1/menus'));
  await drawer(page).getByRole('button', { name: '保存菜单', exact: true }).click();
  await editRequest;
  await expect(drawer(page).locator('.resource-menus-form')).toHaveCount(0);

  const edited = state.saved[1];
  expect(sortedKeys(edited)).toEqual(EDIT_KEYS);
  expect(edited).toMatchObject({ id: 'menu-orders', code: 'orders', name: '订单中心管理', menu_type: 'menu', icon: 'app', parent_id: 'menu-orders-center' });
});

test('按钮：表单隐藏路由字段、payload 清空、必需所属菜单', async ({ page }) => {
  const state = await mockMenuWorkbench(page);
  await page.goto('/menus');

  // The topline 添加 opens 新增菜单; switching the Radio to 按钮 drops route/component and clears them on save.
  await openCreate(page, 'menu');
  await expect(drawer(page).locator('.resource-menus-type-radio .t-radio')).toHaveCount(2);
  await expect(field(page, 'route')).toHaveCount(1);
  await field(page, 'name').fill('新建按钮');
  await field(page, 'code').fill('new-button');
  await field(page, 'route').fill('/ignored');
  await field(page, 'component').fill('/pages/ignored');
  await typeRadio(page, '按钮').click();
  await settle(page);
  await expect(field(page, 'route')).toHaveCount(0);
  await expect(field(page, 'component')).toHaveCount(0);
  await expect(drawer(page).locator('.t-form-item__name label')).toHaveText('按钮名称');
  await expect(drawer(page).getByRole('switch', { name: '启用按钮' })).toBeVisible();

  // A button without 所属菜单 is rejected by the required rule before any request is sent.
  await drawer(page).getByRole('button', { name: '保存按钮', exact: true }).click();
  await expect(drawer(page).locator('.t-form-item__parent_id .t-input__extra')).toHaveText('请填写所属菜单');
  expect(state.saved).toHaveLength(0);

  await openPicker(page);
  await expect(pickerNode(page, '根节点')).toHaveCount(0);
  // 订单中心 is a menu now (the old directory is gone), so it is a valid button parent; only buttons stay disabled.
  await expect(pickerNode(page, '订单中心')).not.toHaveClass(/is-disabled/);
  await expect(pickerNode(page, '订单中心').locator('.menus-parent-picker-node-reason')).toHaveCount(0);
  await expect(pickerNode(page, '审核订单')).toHaveClass(/is-disabled/);
  await expect(pickerNode(page, '审核订单').locator('.menus-parent-picker-node-reason')).toHaveText('按钮 · 不可作为所属');
  await expect(pickerNode(page, '订单管理')).not.toHaveClass(/is-disabled/);
  await expect(page.locator('.menus-parent-picker-note')).toContainText('按钮只能挂在菜单下');
  await pickerNode(page, '订单管理').locator('.menus-parent-picker-node-name').click();
  await expect(picker(page).locator('input')).toHaveValue('订单中心 / 订单管理');

  const createButtonRequest = page.waitForRequest(request => request.method() === 'POST' && request.url().includes('/api/v1/rbac-v1/menus'));
  await drawer(page).getByRole('button', { name: '保存按钮', exact: true }).click();
  await createButtonRequest;
  await expect(drawer(page).locator('.resource-menus-form')).toHaveCount(0);

  expect(state.saved).toHaveLength(1);
  expect(sortedKeys(state.saved[0])).toEqual(CREATE_KEYS);
  expect(state.saved[0]).toMatchObject({
    code: 'new-button',
    name: '新建按钮',
    menu_type: 'button',
    parent_id: 'menu-orders',
    route: '',
    component: '',
    icon: '',
    sort_order: 0,
    enabled: true,
  });
});

test('删除：叶子可确认删除，有子项即使被过滤也不可确认', async ({ page }) => {
  const state = await mockMenuWorkbench(page);
  await page.goto('/menus');

  // A type filter + keyword parts the 订单中心 subtree so it renders without its children; the delete guard still sees them.
  await draftType(page, '菜单');
  await draftKeyword(page, '订单中');
  await applyQuery(page);
  await expectRowNames(page, ['订单中心']);
  await moreButton(page, '订单中心').click();
  await dropdownItem(page, '删除').click();
  const guardedDialog = page.locator('.t-dialog');
  await expect(guardedDialog).toBeVisible();
  await expect(guardedDialog.locator('.t-dialog__header')).toContainText('删除菜单');
  await expect(guardedDialog.locator('.t-dialog__confirm')).toHaveClass(/t-is-disabled/);
  await expect(guardedDialog.locator('.t-alert')).toContainText('该节点仍有子项，请先处理子项后再删除。');
  await guardedDialog.locator('.t-dialog__cancel').click();
  await expect(guardedDialog).toBeHidden();

  // A leaf button deletes normally and refreshes the list.
  await resetQuery(page);
  await expandRow(page, '订单管理');
  await row(page, '审核订单').getByRole('button', { name: '删除', exact: true }).click();
  const dialog = page.locator('.t-dialog');
  await expect(dialog).toBeVisible();
  await expect(dialog.locator('.t-dialog__header')).toContainText('删除按钮');
  await expect(dialog.locator('.t-alert')).toHaveCount(0);
  await expect(dialog.locator('.t-dialog__confirm')).not.toHaveClass(/t-is-disabled/);

  const deleteRequest = page.waitForRequest(request => request.method() === 'DELETE' && new URL(request.url()).pathname === '/api/v1/rbac-v1/menus/btn-orders-audit');
  await dialog.locator('.t-dialog__confirm').click();
  await deleteRequest;

  expect(state.deletes).toEqual(['btn-orders-audit']);
  await expect(row(page, '审核订单')).toHaveCount(0);
  await expect(page.locator('.resource-menus-footer')).toContainText('共 6 条');
});

test('修改按钮与删除叶子菜单：回显、提交、确认删除与刷新', async ({ page }) => {
  const state = await mockMenuWorkbench(page);
  await page.goto('/menus');
  await expandRow(page, '订单管理');
  await row(page, '审核订单').getByRole('button', { name: '编辑', exact: true }).click();
  await expect(field(page, 'name')).toHaveValue('审核订单');
  await expect(field(page, 'code')).toHaveValue('orders:audit');
  await expect(field(page, 'code')).toBeDisabled();
  await expect(picker(page).locator('input')).toHaveValue('订单中心 / 订单管理');
  await field(page, 'name').fill('订单审核');
  await field(page, 'sort_order').fill('42');
  await drawer(page).getByRole('switch', { name: '启用按钮' }).click();
  await drawer(page).getByRole('button', { name: '保存按钮', exact: true }).click();
  await expect(drawer(page).locator('.resource-menus-form')).toHaveCount(0);
  expect(state.saved).toHaveLength(1);
  expect(sortedKeys(state.saved[0])).toEqual(EDIT_KEYS);
  expect(state.saved[0]).toMatchObject({ id: 'btn-orders-audit', code: 'orders:audit', name: '订单审核', menu_type: 'button', parent_id: 'menu-orders', sort_order: 42, enabled: false, route: '', component: '', icon: '' });
  await expect(row(page, '订单审核')).toBeVisible();

  await moreButton(page, '报表管理').click();
  await dropdownItem(page, '删除').click();
  const dialog = page.locator('.t-dialog');
  await expect(dialog.locator('.t-dialog__header')).toContainText('删除菜单');
  await expect(dialog.locator('.t-dialog__confirm')).not.toHaveClass(/t-is-disabled/);
  await dialog.locator('.t-dialog__confirm').click();
  await expect(dialog).toBeHidden();
  expect(state.deletes).toEqual(['menu-reports']);
  await expect(row(page, '报表管理')).toHaveCount(0);
  await expect(page.locator('.resource-menus-footer')).toContainText('共 6 条');
});

test('错误状态与重试：首次加载失败展示告警，重试后恢复', async ({ page }) => {
  await mockMenuWorkbench(page, { failMenus: 1 });
  await page.goto('/menus');

  const alert = page.locator('.rbac-load-error');
  await expect(alert).toBeVisible();
  await expect(alert).toContainText('菜单列表加载失败');

  await alert.getByRole('button', { name: '重试', exact: true }).click();
  await expect(alert).toHaveCount(0);
  await expectRowNames(page, ['订单中心', '订单管理', '报表管理', '历史菜单']);
});

test('空状态：没有菜单时展示空态与零计数', async ({ page }) => {
  await mockMenuWorkbench(page, { menus: [] });
  await page.goto('/menus');

  await expect(page.locator('.rbac-empty')).toBeVisible();
  await expect(page.locator('.rbac-empty')).toContainText('尚无菜单或按钮');
  await expect(page.locator('.resource-menus-footer')).toContainText('共 0 条');
});

test('移动端无横向溢出（含抽屉与父选择弹层）', async ({ page }) => {
  await mockMenuWorkbench(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/menus');

  await expect(rows(page)).toHaveCount(4);
  await expectNoHorizontalOverflow(page);

  await openCreate(page, 'menu');
  await expect(drawer(page)).toBeVisible();
  await expectNoHorizontalOverflow(page);

  await openPicker(page);
  await expect(pickerPopup(page)).toBeVisible();
  await expectNoHorizontalOverflow(page);
});

test.describe('设计稿截图', () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test('截图：列表 list 与 父级选择 parent', async ({ page }) => {
    await mockMenuWorkbench(page, { menus: DESIGN_MENUS });
    await page.goto('/menus');
    await expect(addButton(page)).toBeVisible();
    await expectRowNames(page, ['订单管理', '查询订单', '创建订单', '用户管理']);
    await expect(page.locator('.resource-menus-footer')).toContainText('共 4 条');
    await waitInViewport(page, panel(page));
    await shoot(page, 'list', true);

    // 新增菜单 drawer + parent TreeSelect popup (根节点 selected, buttons disabled).
    await openCreate(page, 'menu');
    await openPicker(page);
    await expect(pickerNode(page, '根节点')).toHaveClass(/is-selected/);
    await expect(pickerNode(page, '查询订单')).toHaveClass(/is-disabled/);
    await shoot(page, 'parent');
  });

  test('截图：新增按钮 button、编辑菜单 edit-menu、编辑按钮 edit-button', async ({ page }) => {
    await mockMenuWorkbench(page, { menus: DESIGN_MENUS });
    await page.goto('/menus');

    await openCreate(page, 'button');
    await expect(field(page, 'route')).toHaveCount(0);
    await openPicker(page);
    await expect(pickerNode(page, '查询订单')).toHaveClass(/is-disabled/);
    await shoot(page, 'button');
    await closeDrawer(page);

    await row(page, '订单管理').getByRole('button', { name: '编辑', exact: true }).click();
    await expect(drawer(page)).toBeVisible();
    await expect(drawer(page).locator('.t-drawer__header')).toContainText('编辑菜单');
    await expect(typeRadio(page, '按钮').locator('input')).toBeDisabled();
    await shoot(page, 'edit-menu');
    await closeDrawer(page);

    await row(page, '查询订单').getByRole('button', { name: '编辑', exact: true }).click();
    await expect(drawer(page)).toBeVisible();
    await expect(drawer(page).locator('.t-drawer__header')).toContainText('编辑按钮');
    await expect(field(page, 'route')).toHaveCount(0);
    await shoot(page, 'edit-button');
    await closeDrawer(page);
  });

  test('截图：删除 delete 确认框', async ({ page }) => {
    await mockMenuWorkbench(page, { menus: DESIGN_MENUS });
    await page.goto('/menus');

    await row(page, '查询订单').getByRole('button', { name: '删除', exact: true }).click();
    const dialog = page.locator('.t-dialog');
    await expect(dialog).toBeVisible();
    await expect(dialog.locator('.t-dialog__confirm')).not.toHaveClass(/t-is-disabled/);
    await shoot(page, 'delete');
  });
});
