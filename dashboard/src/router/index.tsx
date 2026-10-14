import { createBrowserRouter } from 'react-router-dom';
import { createRoutes } from './routes';
import type { WorkbenchMenu } from './workbench-menu';

export const createWorkbenchRouter = (menus: WorkbenchMenu[]) => createBrowserRouter(createRoutes(menus));
