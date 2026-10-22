import { Navigate, RouteObject, useLocation } from 'react-router-dom';
import ErrorPage from '../components/error';
import RouteError from '../components/error/route-error';
import PublicLayout from '../layouts/layout';
import Login from '../pages/login';
import SelectServiceResource from '../pages/select-service-resource';
import { createWorkbenchRoutes } from './workbench-routes';
import type { WorkbenchMenu } from './workbench-menu';
import { consumeLoginRedirect, getServiceResourceSelectionPath, getSafeRedirectPath, HomeRedirect, RedirectIfAuthenticated, RequireServiceResource, RequireAuth, setCachedAuthStatus } from './auth';

const AuthCallback = () => {
  const location = useLocation();
  const redirect = getSafeRedirectPath(new URLSearchParams(location.search).get('redirect')) || consumeLoginRedirect();
  setCachedAuthStatus(true);
  return <Navigate to={getServiceResourceSelectionPath(redirect)} replace />;
};

export const createRoutes = (menus: WorkbenchMenu[]): RouteObject[] => [
  { element: <RedirectIfAuthenticated />, children: [{ path: '/login', Component: Login }] },
  { path: '/auth/callback', Component: AuthCallback },
  { path: '/', element: <HomeRedirect /> },
  {
    element: <RequireAuth />,
    children: [
      { path: '/select-application', Component: SelectServiceResource, handle: { name: '选择业务应用' } },
      { path: '/select-service-resource', element: <Navigate to="/select-application" replace /> },
      {
        id: 'authenticated-layout',
        path: '/',
        Component: PublicLayout,
        errorElement: <RouteError />,
        children: [
          {
            element: <RequireServiceResource />,
            children: [
              ...createWorkbenchRoutes(menus),
              { path: 'bindings', element: <Navigate to="/roles" replace /> },
              { path: '*', Component: ErrorPage },
            ],
          },
        ],
      },
    ],
  },
  { path: '*', Component: ErrorPage },
];
