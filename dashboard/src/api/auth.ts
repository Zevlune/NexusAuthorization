/** Dashboard 导航 mock，与 NexusAuth Workbench 的菜单返回结构保持一致。 */
export type DashboardMenu = {
  id: string;
  route: string;
  componentPath: string | null;
  name: string;
  show: boolean;
  iconName: string | null;
  parentId: string | null;
};

// componentPath 为空的节点仅用于导航分组；show:false 的页面仍参与路由注册。
const mockMenus: DashboardMenu[] = [
  { id: 'dashboard', route: '/dashboard', componentPath: '/policy-workbench/index.tsx', name: '配置总览', show: true, iconName: 'dashboard', parentId: null },
  { id: 'resources', route: '/resources', componentPath: null, name: '资源管理', show: true, iconName: 'api', parentId: null },
  { id: 'services', route: '/service-resources', componentPath: '/policy-workbench/index.tsx', name: '服务资源', show: true, iconName: null, parentId: 'resources' },
  { id: 'apis', route: '/api-resources', componentPath: '/policy-workbench/index.tsx', name: 'API 接口', show: true, iconName: null, parentId: 'resources' },
  { id: 'menus', route: '/menus', componentPath: '/policy-workbench/index.tsx', name: '菜单与按钮', show: true, iconName: null, parentId: 'resources' },
  { id: 'rbac', route: '/rbac', componentPath: null, name: 'RBAC 授权', show: true, iconName: 'usergroup', parentId: null },
  { id: 'roles', route: '/roles', componentPath: '/policy-workbench/index.tsx', name: '角色管理', show: true, iconName: null, parentId: 'rbac' },
  { id: 'role-new', route: '/roles/new', componentPath: '/policy-workbench/index.tsx', name: '新增角色', show: false, iconName: null, parentId: 'roles' },
  { id: 'role-edit', route: '/roles/:roleId/edit', componentPath: '/policy-workbench/index.tsx', name: '编辑角色', show: false, iconName: null, parentId: 'roles' },
  { id: 'delivery', route: '/delivery', componentPath: null, name: '权限验证', show: true, iconName: 'history', parentId: null },
  { id: 'simulate', route: '/simulate', componentPath: '/policy-workbench/index.tsx', name: '权限模拟', show: true, iconName: null, parentId: 'delivery' },
];

export const getMenuList = (): Promise<DashboardMenu[]> => new Promise(resolve => {
  window.setTimeout(() => resolve(mockMenus.map(menu => ({ ...menu }))), 0);
});
