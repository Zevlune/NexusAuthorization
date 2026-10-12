import { Navigate, Outlet, useLocation } from 'react-router-dom';
import { checkAuth } from '../api/login';
import { getStoredApplication } from '../api/application';

let cachedAuthStatus: boolean | null = null;

export const LOGIN_REDIRECT_STORAGE_KEY = 'permission-center-login-redirect';

export const getCachedAuthStatus = () => cachedAuthStatus;

export const setCachedAuthStatus = (status: boolean) => {
  cachedAuthStatus = status;
};

export const checkAuthenticated = async () => {
  try {
    const isAuth = await checkAuth();
    setCachedAuthStatus(isAuth);
    return isAuth;
  } catch {
    setCachedAuthStatus(false);
    return false;
  }
};

export const getSafeRedirectPath = (redirect: string | null | undefined) => {
  if (!redirect || !redirect.startsWith('/') || redirect.startsWith('//')) {
    return null;
  }

  return redirect;
};

const isReservedRedirect = (path: string) => (
  path === '/'
  || path === '/login'
  || path === '/auth/callback'
  || path === '/select-service-resource'
  || path === '/select-application'
);

export const getSafePostLoginRedirect = (redirect: string | null | undefined) => {
  const safeRedirect = getSafeRedirectPath(redirect);
  if (!safeRedirect || isReservedRedirect(safeRedirect.split(/[?#]/, 1)[0])) {
    return null;
  }

  return safeRedirect;
};

export const getServiceResourceSelectionPath = (redirect: string | null | undefined) => {
  const safeRedirect = getSafePostLoginRedirect(redirect);
  return safeRedirect
    ? `/select-application?redirect=${encodeURIComponent(safeRedirect)}`
    : '/select-application';
};

export const getPostServiceResourceSelectionPath = (redirect: string | null | undefined) => (
  getSafePostLoginRedirect(redirect) || '/dashboard'
);

export const rememberLoginRedirect = (redirect: string | null | undefined) => {
  if (typeof window === 'undefined') {
    return;
  }

  try {
    const safeRedirect = getSafePostLoginRedirect(redirect);
    if (safeRedirect) {
      window.localStorage.setItem(LOGIN_REDIRECT_STORAGE_KEY, safeRedirect);
    } else {
      window.localStorage.removeItem(LOGIN_REDIRECT_STORAGE_KEY);
    }
  } catch {
    // Browsers can disable localStorage in private or restricted contexts.
  }
};

export const consumeLoginRedirect = () => {
  if (typeof window === 'undefined') {
    return null;
  }

  try {
    const redirect = window.localStorage.getItem(LOGIN_REDIRECT_STORAGE_KEY);
    window.localStorage.removeItem(LOGIN_REDIRECT_STORAGE_KEY);
    return getSafePostLoginRedirect(redirect);
  } catch {
    return null;
  }
};

export const clearLoginRedirect = () => {
  if (typeof window === 'undefined') {
    return;
  }

  try {
    window.localStorage.removeItem(LOGIN_REDIRECT_STORAGE_KEY);
  } catch {
    // Browsers can disable localStorage in private or restricted contexts.
  }
};

export const getAuthenticatedLandingPath = (redirect: string | null | undefined) => {
  const safeRedirect = getSafePostLoginRedirect(redirect);
  return getStoredApplication()
    ? safeRedirect || '/dashboard'
    : getServiceResourceSelectionPath(safeRedirect);
};

const getRedirectPath = (location: { pathname: string; search?: string; hash?: string }) => {
  return `${location.pathname}${location.search || ''}${location.hash || ''}`;
};

const getLoginPath = (location: { pathname: string; search?: string; hash?: string }) => {
  const params = new URLSearchParams({ redirect: getRedirectPath(location) });
  return `/login?${params.toString()}`;
};

export const RequireAuth = () => {
  const location = useLocation();

  if (!getCachedAuthStatus()) {
    return <Navigate to={getLoginPath(location)} replace state={{ from: location }} />;
  }

  return <Outlet />;
};

export const RequireServiceResource = () => {
  const location = useLocation();

  if (!getStoredApplication()) {
    return (
      <Navigate
        to={getServiceResourceSelectionPath(getRedirectPath(location))}
        replace
        state={{ from: location }}
      />
    );
  }

  return <Outlet />;
};

export const RedirectIfAuthenticated = () => {
  const location = useLocation();

  if (getCachedAuthStatus()) {
    return (
      <Navigate
        to={getAuthenticatedLandingPath(new URLSearchParams(location.search).get('redirect'))}
        replace
      />
    );
  }

  return <Outlet />;
};

export const HomeRedirect = () => {
  return <Navigate to={getCachedAuthStatus() ? getAuthenticatedLandingPath(null) : '/login'} replace />;
};
