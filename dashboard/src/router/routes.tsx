import { Navigate, RouteObject, useLocation } from 'react-router-dom';
import ErrorPage from '../components/error';
import PublicLayout from '../layouts/layout';
import Login from '../pages/login';
import SelectServiceResource from '../pages/select-service-resource';
import WorkbenchPage from '../pages/policy-workbench';
import { consumeLoginRedirect, getServiceResourceSelectionPath, getSafeRedirectPath, HomeRedirect, RedirectIfAuthenticated, RequireServiceResource, RequireAuth, setCachedAuthStatus } from './auth';

const AuthCallback = () => {
  const location = useLocation();
  const redirect = getSafeRedirectPath(new URLSearchParams(location.search).get('redirect')) || consumeLoginRedirect();
  setCachedAuthStatus(true);
  return <Navigate to={getServiceResourceSelectionPath(redirect)} replace />;
};

const workbenchRoutes = ['/dashboard', '/service-resources', '/api-resources', '/roles', '/bindings', '/menus', '/releases', '/simulate'];

export const routes: RouteObject[] = [
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
        errorElement: <ErrorPage />,
        children: [
          {
            element: <RequireServiceResource />,
            children: workbenchRoutes.map((path) => ({ path: path.slice(1), Component: WorkbenchPage })),
          },
        ],
      },
    ],
  },
  { path: '*', Component: ErrorPage },
];
