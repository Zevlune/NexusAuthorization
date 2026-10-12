import * as React from 'react';
import { lazy, type ComponentType } from 'react';
import type { RouteObject } from 'react-router-dom';

import ErrorPage from '../components/error';
import {
  getMenuTree,
  getStoredServiceResource,
  MENU_CHANGE_EVENT,
  SERVICE_RESOURCE_CHANGE_EVENT,
  type Menu,
} from '../api/permission';

type PageModule = { default?: ComponentType };
type PageLoader = () => Promise<PageModule>;

const pageModules = import.meta.glob('../pages/permission-center/**/index.tsx') as Record<string, PageLoader>;

export type NavigationMenu = Omit<Menu, 'children' | 'path'> & {
  path: string;
  children: NavigationMenu[];
};

export const DASHBOARD_NAVIGATION_MENU: NavigationMenu = {
  id: 'frontend-dashboard',
  serviceResource: '',
  parentId: null,
  code: 'dashboard',
  name: '仪表盘',
  description: '权限中心概览',
  type: 'menu',
  path: '/dashboard',
  component: '/permission-center/dashboard/index.tsx',
  apiPath: '',
  httpMethod: '',
  icon: 'dashboard',
  sort: -1,
  enabled: true,
  children: [],
};

const routePath = (value: string, parentPath: string): string => {
  const path = value.trim();
  if (!path) {
    return parentPath || '/';
  }
  if (path.startsWith('/')) {
    return path;
  }

  const parent = parentPath === '/' ? '' : parentPath.replace(/\/+$/, '');
  return `${parent}/${path.replace(/^\/+/, '')}`;
};

export const buildNavigationMenus = (nodes: Menu[], parentPath = ''): NavigationMenu[] => nodes
  .filter((node) => (
    node.enabled
    && node.type === 'menu'
    && node.code !== 'dashboard'
    && routePath(node.path, parentPath) !== '/dashboard'
  ))
  .map((node) => {
    const path = routePath(node.path, parentPath);
    return {
      ...node,
      path,
      children: buildNavigationMenus(node.children, path),
    };
  });

const normalizedComponentCandidates = (value: string): string[] => {
  const raw = value.trim().replace(/^\/+/, '').replace(/^\.\/+/, '');
  if (!raw || raw.includes('\\')) {
    return [];
  }

  const pagePath = raw.startsWith('pages/') ? raw.slice('pages/'.length) : raw;
  if (pagePath.split('/').some((segment) => segment === '..')) {
    return [];
  }

  const withoutIndex = pagePath.replace(/\/index\.tsx$/i, '');
  const candidates = new Set<string>();
  if (/\/index\.tsx$/i.test(pagePath)) {
    candidates.add(`../pages/${pagePath}`);
  }
  candidates.add(`../pages/${withoutIndex}/index.tsx`);
  return [...candidates];
};

const resolvePageLoader = (componentPath: string): PageLoader | undefined => {
  for (const candidate of normalizedComponentCandidates(componentPath)) {
    const loader = pageModules[candidate];
    if (loader) {
      return loader;
    }
  }

  return undefined;
};

export const resolveMenuComponent = (componentPath: string): ComponentType => {
  const loader = resolvePageLoader(componentPath);
  if (!loader) {
    return ErrorPage;
  }

  return lazy(async () => {
    const module = await loader();
    return { default: module.default || ErrorPage };
  });
};

const flattenNavigationMenus = (nodes: NavigationMenu[]): NavigationMenu[] => nodes.flatMap((node) => [
  node,
  ...flattenNavigationMenus(node.children),
]);

export const createDynamicRoutes = (menus: NavigationMenu[]): RouteObject[] => (
  flattenNavigationMenus(menus).map((menu) => ({
    id: `menu-${menu.id || menu.path}`,
    path: menu.path,
    Component: resolveMenuComponent(menu.component),
    handle: {
      name: menu.name,
      menuID: menu.id,
      icon: menu.icon,
    },
  }))
);

export const findNavigationMenu = (
  menus: NavigationMenu[],
  pathname: string,
): NavigationMenu | undefined => flattenNavigationMenus(menus).find((menu) => menu.path === pathname);

export type DynamicNavigationState = {
  menus: NavigationMenu[];
  loading: boolean;
  error: unknown;
  scope: string;
  version: number;
};

export const useDynamicNavigation = (): DynamicNavigationState => {
  const [menus, setMenus] = React.useState<NavigationMenu[]>([DASHBOARD_NAVIGATION_MENU]);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<unknown>(null);
  const [scope, setScope] = React.useState(getStoredServiceResource);
  const [version, setVersion] = React.useState(0);
  const requestVersion = React.useRef(0);

  const reload = React.useCallback(async () => {
    const requestID = requestVersion.current + 1;
    requestVersion.current = requestID;
    const nextScope = getStoredServiceResource();

    setScope(nextScope);
    setMenus([DASHBOARD_NAVIGATION_MENU]);
    setError(null);
    setLoading(true);

    if (!nextScope) {
      if (requestVersion.current === requestID) {
        setLoading(false);
        setVersion((value) => value + 1);
      }
      return;
    }

    try {
      const tree = await getMenuTree(nextScope);
      if (requestVersion.current !== requestID) {
        return;
      }

      const nextMenus = [DASHBOARD_NAVIGATION_MENU, ...buildNavigationMenus(tree)];
      setMenus(nextMenus);
      setVersion((value) => value + 1);
      setLoading(false);
    } catch (nextError) {
      if (requestVersion.current !== requestID) {
        return;
      }

      setMenus([DASHBOARD_NAVIGATION_MENU]);
      setError(nextError);
      setVersion((value) => value + 1);
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void reload();

    const handleNavigationChange = () => {
      void reload();
    };
    window.addEventListener(SERVICE_RESOURCE_CHANGE_EVENT, handleNavigationChange);
    window.addEventListener(MENU_CHANGE_EVENT, handleNavigationChange);
    return () => {
      window.removeEventListener(SERVICE_RESOURCE_CHANGE_EVENT, handleNavigationChange);
      window.removeEventListener(MENU_CHANGE_EVENT, handleNavigationChange);
    };
  }, [reload]);

  return { menus, loading, error, scope, version };
};
