import axios, { AxiosError, type AxiosResponse } from 'axios';

export type ApiResult<T = unknown> = {
  success: boolean;
  errorCode?: string | null;
  errorMessage?: string | null;
  result?: T | null;
};

declare module 'axios' {
  export interface AxiosInstance {
    get<T>(url: string, config?: AxiosRequestConfig): Promise<T>;
    post<T>(url: string, data?: unknown, config?: AxiosRequestConfig): Promise<T>;
    put<T>(url: string, data?: unknown, config?: AxiosRequestConfig): Promise<T>;
    patch<T>(url: string, data?: unknown, config?: AxiosRequestConfig): Promise<T>;
    delete<T>(url: string, config?: AxiosRequestConfig): Promise<T>;
  }
}

const request = axios.create({
  baseURL: '/api',
  timeout: 10000,
  withCredentials: true,
});

const isApiResult = (value: unknown): value is ApiResult => {
  return typeof value === 'object'
    && value !== null
    && typeof (value as ApiResult).success === 'boolean';
};

const redirectToLogin = (response: AxiosResponse) => {
  if (response.status !== 401 || response.config.url?.includes('/auth/')) {
    return false;
  }

  // Return to the current page after signing in again; the login page validates the target.
  const current = `${window.location.pathname}${window.location.search}${window.location.hash}`;
  window.location.href = window.location.pathname === '/login'
    ? '/login'
    : `/login?redirect=${encodeURIComponent(current)}`;
  return true;
};

const createApiResultError = (response: AxiosResponse, apiResult: ApiResult) => {
  const message = apiResult.errorMessage || apiResult.errorCode || '请求失败';
  const errorResponse: AxiosResponse = {
    ...response,
    data: {
      ...apiResult,
      message,
    },
  };

  return new AxiosError(
    message,
    apiResult.errorCode || undefined,
    response.config,
    response.request,
    errorResponse,
  );
};

request.interceptors.response.use(
  (response: AxiosResponse) => {
    if (redirectToLogin(response)) {
      return Promise.reject(new AxiosError(
        'Unauthorized',
        AxiosError.ERR_BAD_REQUEST,
        response.config,
        response.request,
        response,
      ));
    }

    if (!isApiResult(response.data)) {
      return response.data;
    }

    if (!response.data.success) {
      return Promise.reject(createApiResultError(response, response.data));
    }

    return response.data.result;
  },
  (error: AxiosError) => {
    if (error.response && redirectToLogin(error.response)) {
      return Promise.reject(error);
    }

    if (error.response && isApiResult(error.response.data) && !error.response.data.success) {
      return Promise.reject(createApiResultError(error.response, error.response.data));
    }

    return Promise.reject(error);
  }
);

export default request;
