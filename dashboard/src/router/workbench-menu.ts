import { matchPath } from 'react-router-dom';
import type { DashboardMenu } from '../api/auth';

export type WorkbenchMenu = DashboardMenu & {
  path: string;
  parentPaths: string[];
  children: WorkbenchMenu[];
};

const resolvePath = (route: string, parentPath: string) => route.startsWith('/')
  ? route
  : `${parentPath.replace(/\/+$/, '')}/${route.replace(/^\/+/, '')}`;

export const buildWorkbenchMenus = (records: DashboardMenu[]): WorkbenchMenu[] => {
  const ids = new Set<string>();
  const children = new Map<string | null, DashboardMenu[]>();
  for (const record of records) {
    if (ids.has(record.id)) throw new Error(`导航菜单 ID 重复：${record.id}`);
    ids.add(record.id);
    children.set(record.parentId, [...(children.get(record.parentId) || []), record]);
  }
  const visited = new Set<string>();
  const build = (parentId: string | null, parentPaths: string[]): WorkbenchMenu[] => (
    (children.get(parentId) || []).map(record => {
      visited.add(record.id);
      const path = resolvePath(record.route, parentPaths[parentPaths.length - 1] || '');
      return { ...record, path, parentPaths, children: build(record.id, [...parentPaths, path]) };
    })
  );
  const tree = build(null, []);
  if (visited.size !== records.length) throw new Error('导航菜单存在无效的父级或循环引用');
  return tree;
};

export const flattenWorkbenchMenus = (menus: WorkbenchMenu[]): WorkbenchMenu[] => menus.flatMap(menu => [
  menu,
  ...flattenWorkbenchMenus(menu.children),
]);

export const visibleWorkbenchMenus = (menus: WorkbenchMenu[]): WorkbenchMenu[] => menus
  .filter(menu => menu.show)
  .map(menu => ({ ...menu, children: visibleWorkbenchMenus(menu.children) }));

export const findWorkbenchMenu = (menus: WorkbenchMenu[], pathname: string): WorkbenchMenu | undefined => (
  flattenWorkbenchMenus(menus).find(menu => matchPath({ path: menu.path, end: true }, pathname))
);

export const workbenchMenuTrail = (menus: WorkbenchMenu[], pathname: string): WorkbenchMenu[] => {
  const nodes = flattenWorkbenchMenus(menus);
  let current = findWorkbenchMenu(menus, pathname);
  const trail: WorkbenchMenu[] = [];
  while (current) {
    trail.unshift(current);
    current = nodes.find(node => node.id === current?.parentId);
  }
  return trail;
};
