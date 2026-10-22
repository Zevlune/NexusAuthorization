import { Navigate, Outlet, useLocation } from 'react-router-dom';
import { checkAuth } from '../api/login';
import { getStoredApplication } from '../api/application';

let cachedAuthStatus: boolean | null = null;

export const LOGIN_REDIRECT_STORAGE_KEY = 'nexus-authorization-login-redirect';

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

// Browsers read "\" as "/" and drop tabs and newlines in URLs, so "/\evil.example" or
// "/<tab>/evil.example" would act like "//evil.example".
const hasUnsafeRedirectCharacter = (value: string) => [...value].some(char => {
  const code = char.charCodeAt(0);
  return char === '\\' || code < 0x20 || code === 0x7f;
});

/** Returns the redirect only when it is a path on this site, never another origin. */
export const getSafeRedirectPath = (redirect: string | null | undefined) => {
  if (!redirect || !redirect.startsWith('/') || redirect.startsWith('//') || hasUnsafeRedirectCharacter(redirect)) {
    return null;
  }
  if (typeof window !== 'undefined') {
    try {
      if (new URL(redirect, window.location.origin).origin !== window.location.origin) {
        return null;
      }
    } catch {
      return null;
    }
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
