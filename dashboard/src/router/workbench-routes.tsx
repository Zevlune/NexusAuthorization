import { lazy, type ComponentType } from 'react';
import type { RouteObject } from 'react-router-dom';
import ErrorPage from '../components/error';
import { flattenWorkbenchMenus, type WorkbenchMenu } from './workbench-menu';

type PageModule = { default: ComponentType };
const pageModules = import.meta.glob('../pages/**/index.tsx') as Record<string, () => Promise<PageModule>>;

export const createWorkbenchRoutes = (menus: WorkbenchMenu[]): RouteObject[] => (
  flattenWorkbenchMenus(menus).filter(menu => menu.componentPath).map(menu => {
    const componentPath = menu.componentPath?.replace(/^\/+/, '') || '';
    const loader = pageModules[`../pages/${componentPath}`];
    return {
      id: `workbench-${menu.id}`,
      path: menu.path,
      Component: loader ? lazy(loader) : ErrorPage,
      handle: { name: menu.name, menuID: menu.id, parentPaths: menu.parentPaths, show: menu.show },
    };
  })
);
