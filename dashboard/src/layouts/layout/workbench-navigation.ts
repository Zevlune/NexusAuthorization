import { createContext, useContext } from 'react';
import { findWorkbenchMenu, workbenchMenuTrail, type WorkbenchMenu } from '../../router/workbench-menu';

export const WorkbenchNavigationContext = createContext<WorkbenchMenu[]>([]);
export const useWorkbenchNavigation = () => useContext(WorkbenchNavigationContext);

export const getWorkbenchNavItem = (menus: WorkbenchMenu[], pathname: string) => (
  workbenchMenuTrail(menus, pathname).filter(menu => menu.show && menu.componentPath).slice(-1)[0]
);

export const getWorkbenchPageLabel = (menus: WorkbenchMenu[], pathname: string) => (
  findWorkbenchMenu(menus, pathname)?.name || pathname
);
