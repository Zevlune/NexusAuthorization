import { createRequire } from 'node:module';
import type { Locator, Page } from '../../../../dashboard/node_modules/@playwright/test';

// Resolve Playwright from the Dashboard package while the suite lives at the repository root.
const requireDashboardDependency = createRequire(new URL('../../../../dashboard/package.json', import.meta.url));
const { expect, test } = requireDashboardDependency('@playwright/test') as typeof import('../../../../dashboard/node_modules/@playwright/test');

// Frozen scope: this suite is the only file added; no production code is touched.
//
// The NexusAuth-style dashboard builds its navigation from the async mock API module
// dashboard/src/api/auth.ts, which exports getMenuList(): Promise<DashboardMenu[]> and getVisibleMenuList.
// DashboardMenu carries { id, route, componentPath: string | null, name, show, iconName: string | null, parentId }.
// RootRouterProvider loads every record first, registers a route for each one (visible or hidden), and the
// sidebar renders only the show:true nodes through an arbitrarily deep recursion. Breadcrumb, tabs and
// document.title all read the same records. componentPath is resolved through import.meta.glob over the page
// index.tsx files, so '/policy-workbench/index.tsx' is the component every mocked entry points at.
//
// Instead of a menu HTTP endpoint the suite swaps the module itself via page.route('**/src/api/auth.ts*'),
// serving a tiny ESM module that returns the fixture. The /api/** catch-all keeps a startsWith('/api/') guard
// so it never intercepts the Vite module graph (/src/api/*.ts), and every business call is mocked.

const APP_KEY = 'console';
const WORKBENCH_PAGE = '/policy-workbench/index.tsx';

type DashboardMenu = {
  id: string;
  route: string;
  componentPath: string | null;
  name: string;
  show: boolean;
  iconName: string | null;
  parentId: string | null;
};

const pageNode = (id: string, name: string, route: string, parentId: string | null = null, show = true): DashboardMenu => ({
  id, route, componentPath: WORKBENCH_PAGE, name, show, iconName: null, parentId,
});

const groupNode = (id: string, name: string, parentId: string | null = null, route = ''): DashboardMenu => ({
  id, route, componentPath: null, name, show: true, iconName: null, parentId,
});

// Mirrors the shipped default: 3 groups + 7 visible pages + 2 hidden role routes.
const DEFAULT_MENUS: DashboardMenu[] = [
  pageNode('dashboard', '配置总览', '/dashboard'),
  groupNode('group-resources', '资源管理', null, '/resources'),
  pageNode('service-resources', '服务资源', '/service-resources', 'group-resources'),
  pageNode('api-resources', 'API 接口', '/api-resources', 'group-resources'),
  pageNode('menus', '菜单与按钮', '/menus', 'group-resources'),
  groupNode('group-rbac', 'RBAC 授权', null, '/rbac'),
  pageNode('roles', '角色管理', '/roles', 'group-rbac'),
  pageNode('role-new', '新增角色', '/roles/new', 'roles', false),
  pageNode('role-edit', '编辑角色', '/roles/:roleId/edit', 'roles', false),
  groupNode('group-delivery', '发布与验证', null, '/delivery'),
  pageNode('releases', '发布版本', '/releases', 'group-delivery'),
  pageNode('simulate', '权限模拟', '/simulate', 'group-delivery'),
];

const renameDefaultMenus = (changes: Record<string, string>): DashboardMenu[] => DEFAULT_MENUS.map((item) => (
  changes[item.id] ? { ...item, name: changes[item.id] } : item
));

// Three nested groups with relative route segments; the deepest leaf resolves to
// /deep-nav/second-layer/third-layer/leaf-page.
const DEEP_MENUS: DashboardMenu[] = [
  pageNode('dashboard', '配置总览', '/dashboard'),
  groupNode('deep-root', '深层导航', null, '/deep-nav'),
  groupNode('deep-second', '第二层分组', 'deep-root', 'second-layer'),
  groupNode('deep-third', '第三层分组', 'deep-second', 'third-layer'),
  pageNode('deep-leaf', '最深叶子页面', 'leaf-page', 'deep-third'),
];

const escapeRegex = (value: string) => value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
const exactText = (value: string) => new RegExp(`^${escapeRegex(value)}$`);

// A minimal stand-in for dashboard/src/api/auth.ts. It only needs the two named exports the app imports.
function menuModuleSource(menus: DashboardMenu[], options: { failUntilAllowed?: boolean }): string {
  const data = JSON.stringify(menus);
  const fail = options.failUntilAllowed ? 'true' : 'false';
  return [
    `const DATA = ${data};`,
    `const FAIL = ${fail};`,
    'export const getMenuList = async () => {',
    "  if (FAIL && !(typeof window !== 'undefined' && window.__wfAllowMenu)) {",
    "    throw new Error('菜单加载失败，请重试');",
    '  }',
    '  return DATA.map((item) => ({ ...item }));',
    '};',
    'export const getVisibleMenuList = async () => (await getMenuList()).filter((item) => item.show);',
    'export default { getMenuList, getVisibleMenuList };',
    '',
  ].join('\n');
}

type WorkbenchState = { businessMenuRequests: number };

async function installWorkbench(
  page: Page,
  menus: DashboardMenu[],
  options: { failUntilAllowed?: boolean; businessMenus?: unknown[]; selectedApplication?: boolean } = {},
): Promise<WorkbenchState> {
  const state: WorkbenchState = { businessMenuRequests: 0 };

  // Single init script (run order between separate addInitScript calls is not guaranteed): either seed the
  // selected business application or leave it unset so the app routes through /select-application.
  await page.addInitScript((selected: boolean) => {
    if (selected) window.sessionStorage.setItem('nexus-authorization-application', 'console');
    else window.sessionStorage.removeItem('nexus-authorization-application');
  }, options.selectedApplication ?? true);

  // Every business /api call is mocked. The guard is required: without it the glob also matches the
  // Vite module URL /src/api/auth.ts and would swallow the auth module request.
  await page.route('**/api/**', async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    if (!pathname.startsWith('/api/')) {
      await route.fallback();
      return;
    }
    if (pathname === '/api/auth/me') {
      await route.fulfill({ json: { success: true, result: { isAuthenticated: true, user: { sub: 'admin', name: '管理员' } } } });
      return;
    }
    if (pathname === `/api/v1/applications/${APP_KEY}/service-resources`) {
      await route.fulfill({ json: { success: true, result: { items: [] } } });
      return;
    }
    if (pathname === '/api/v1/rbac-v1/menus') {
      state.businessMenuRequests += 1;
      await route.fulfill({ json: { success: true, result: { items: options.businessMenus ?? [] } } });
      return;
    }
    await route.fulfill({ json: { success: true, result: { items: [], total: 0, page: 1, page_size: 100 } } });
  });

  // Replace the menu API module itself; there is no HTTP menu endpoint backing navigation.
  await page.route('**/src/api/auth.ts*', async (route) => {
    await route.fulfill({ status: 200, contentType: 'text/javascript', body: menuModuleSource(menus, options) });
  });

  return state;
}

const siderMenu = (page: Page): Locator => page.locator('.layout-sider-menu');
const menuItem = (page: Page, name: string): Locator => siderMenu(page).locator('.t-menu__item').filter({ hasText: exactText(name) });
const activeMenuItem = (page: Page, name: string): Locator => siderMenu(page).locator('.t-menu__item.t-is-active').filter({ hasText: exactText(name) });
const openedMenuItem = (page: Page, name: string): Locator => siderMenu(page).locator('.t-menu__item.t-is-opened').filter({ hasText: exactText(name) });
const breadcrumb = (page: Page): Locator => page.locator('.rbac-page-topline .rbac-breadcrumb');
const tabItem = (page: Page, name: string): Locator => page.locator('.layout-content-tabs .t-tabs__nav-item').filter({ hasText: new RegExp(escapeRegex(name)) });
const retryButton = (page: Page): Locator => page.getByRole('button', { name: '重试' }).first();

const settle = async (page: Page) => {
  await page.evaluate(() => new Promise<void>((resolve) => {
    requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
  }));
};

// The shipped sidebar expands every top-level group but leaves deeper groups collapsed. Walk the
// ancestor chain, only clicking a submenu that is actually closed so an open group is never toggled shut.
async function revealMenu(page: Page, name: string, ancestors: string[] = []) {
  for (const ancestor of ancestors) {
    if (await openedMenuItem(page, ancestor).count() === 0) {
      await menuItem(page, ancestor).click();
      await settle(page);
    }
  }
  await expect(menuItem(page, name)).toBeVisible();
}

test.use({ viewport: { width: 1440, height: 900 } });

test('mock 名称与分组改写后，侧栏、角色列表面包屑、tabs 与 document.title 同步', async ({ page }) => {
  await installWorkbench(page, renameDefaultMenus({ 'group-rbac': '自定义授权分组', roles: '自定义角色管理' }));
  await page.goto('/dashboard');

  await expect(siderMenu(page)).toBeVisible();
  await expect(menuItem(page, '自定义授权分组')).toBeVisible();
  await expect(menuItem(page, '自定义角色管理')).toBeVisible();
  await expect(menuItem(page, '角色管理')).toHaveCount(0);

  await revealMenu(page, '自定义角色管理', ['自定义授权分组']);
  await menuItem(page, '自定义角色管理').click();

  await expect(page).toHaveURL(/\/roles$/);
  await expect(breadcrumb(page)).toContainText('自定义授权分组');
  await expect(breadcrumb(page)).toContainText('自定义角色管理');
  await expect(tabItem(page, '自定义角色管理')).toBeVisible();
  await expect(page).toHaveTitle(/自定义角色管理/);
});

test('三层嵌套分组 + 相对路由：点击最深叶子页可渲染，刷新后路由仍有效', async ({ page }) => {
  await installWorkbench(page, DEEP_MENUS);
  await page.goto('/dashboard');
  await expect(siderMenu(page)).toBeVisible();

  await revealMenu(page, '最深叶子页面', ['深层导航', '第二层分组', '第三层分组']);
  await menuItem(page, '最深叶子页面').click();

  await expect(page).toHaveURL(/\/deep-nav\/second-layer\/third-layer\/leaf-page$/);
  await expect(breadcrumb(page)).toContainText('最深叶子页面');
  await expect(page).toHaveTitle(/最深叶子页面/);

  await page.reload();
  await expect(page).toHaveURL(/\/deep-nav\/second-layer\/third-layer\/leaf-page$/);
  await expect(breadcrumb(page)).toContainText('最深叶子页面');
  await expect(page).toHaveTitle(/最深叶子页面/);
  await expect(page.getByText('对不起，你访问的页面不存在')).toHaveCount(0);
});

test('隐藏角色页不进入侧栏，但新增/编辑路由可直接访问且角色管理保持选中', async ({ page }) => {
  await installWorkbench(page, DEFAULT_MENUS);
  await page.goto('/dashboard');
  await expect(siderMenu(page)).toBeVisible();

  await expect(siderMenu(page)).not.toContainText('新增角色');
  await expect(siderMenu(page)).not.toContainText('编辑角色');
  await expect(menuItem(page, '新增角色')).toHaveCount(0);
  await expect(menuItem(page, '编辑角色')).toHaveCount(0);

  // The hidden new-role route stays registered and renders the editor form (no workbench breadcrumb there).
  await page.goto('/roles/new');
  await expect(page).toHaveURL(/\/roles\/new$/);
  await expect(page.locator('.role-editor-topline')).toContainText('新增角色');
  await expect(page.getByRole('button', { name: '返回角色管理' })).toBeVisible();
  await expect(page.getByRole('textbox', { name: /角色名称/ })).toBeVisible();
  await expect(tabItem(page, '新增角色')).toBeVisible();
  await expect(page).toHaveTitle(/新增角色/);
  await expect(activeMenuItem(page, '角色管理')).toBeVisible();

  // The hidden edit route loads too; an unknown role id shows the 角色不存在 state.
  await page.goto('/roles/role-admin/edit');
  await expect(page).toHaveURL(/\/roles\/role-admin\/edit$/);
  await expect(page.locator('.role-editor-topline')).toContainText('编辑授权');
  await expect(page.getByRole('heading', { name: '角色不存在' })).toBeVisible();
  await expect(tabItem(page, '编辑角色')).toBeVisible();
  await expect(page).toHaveTitle(/编辑角色/);
  await expect(activeMenuItem(page, '角色管理')).toBeVisible();
});

test('菜单首次加载失败展示错误与重试，重试成功后恢复导航', async ({ page }) => {
  await installWorkbench(page, DEFAULT_MENUS, { failUntilAllowed: true });
  await page.goto('/dashboard');

  await expect(page.getByText('加载导航菜单失败')).toBeVisible();
  await expect(page.getByText('菜单加载失败，请重试')).toBeVisible();
  await expect(retryButton(page)).toBeVisible();
  await expect(menuItem(page, '角色管理')).toHaveCount(0);

  await page.evaluate(() => { (window as unknown as Record<string, unknown>).__wfAllowMenu = true; });
  await retryButton(page).click();

  await expect(siderMenu(page)).toBeVisible();
  await expect(page.getByText('加载导航菜单失败')).toHaveCount(0);
  await revealMenu(page, '角色管理', ['RBAC 授权']);
  await expect(menuItem(page, '角色管理')).toBeVisible();
});

test('默认导航保留工作台 7 个可见页面，业务 listMenus 数据不影响侧栏', async ({ page }) => {
  const state = await installWorkbench(page, DEFAULT_MENUS, {
    businessMenus: [
      { id: 'm-user-grant', parent_id: null, code: 'user-grant', name: '用户授权', menu_type: 'menu', route: '/user-grant', component: '', icon: '', sort_order: 1, enabled: true },
    ],
  });
  await page.goto('/dashboard');
  await expect(siderMenu(page)).toBeVisible();

  await expect(menuItem(page, '配置总览')).toBeVisible();
  await revealMenu(page, '服务资源', ['资源管理']);
  await expect(menuItem(page, 'API 接口')).toBeVisible();
  await expect(menuItem(page, '菜单与按钮')).toBeVisible();
  await revealMenu(page, '角色管理', ['RBAC 授权']);
  await revealMenu(page, '发布版本', ['发布与验证']);
  await expect(menuItem(page, '权限模拟')).toBeVisible();

  for (const name of ['配置总览', '服务资源', 'API 接口', '菜单与按钮', '角色管理', '发布版本', '权限模拟']) {
    await expect(menuItem(page, name)).toBeVisible();
  }

  await expect(menuItem(page, '用户授权')).toHaveCount(0);
  await expect(siderMenu(page)).not.toContainText('用户授权');
  await expect.poll(() => state.businessMenuRequests).toBeGreaterThan(0);
});

test('未登录直接访问 /roles 跳转登录并携带回跳地址', async ({ page }) => {
  await installWorkbench(page, DEFAULT_MENUS);
  // installWorkbench 的 /api/** mock 默认是已登录；后注册的处理器优先生效，覆盖为未认证。
  await page.route('**/api/auth/me', route => route.fulfill({ json: { success: true, result: { isAuthenticated: false } } }));

  await page.goto('/roles');
  await expect(page).toHaveURL(/\/login\?redirect=%2Froles$/);
  await expect(siderMenu(page)).toHaveCount(0);
  await expect(page.locator('.role-management')).toHaveCount(0);
});

test('已登录但未选择业务应用直接访问 /roles 跳转选择应用', async ({ page }) => {
  await installWorkbench(page, DEFAULT_MENUS, { selectedApplication: false });

  await page.goto('/roles');
  await expect(page).toHaveURL(/\/select-application\?redirect=%2Froles$/);
  await expect(siderMenu(page)).toHaveCount(0);
  await expect(page.locator('.role-management')).toHaveCount(0);
});
