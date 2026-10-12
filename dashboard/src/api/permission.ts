import request from './request';

export const SERVICE_RESOURCE_STORAGE_KEY = 'permission-center-service-resource';
export const SERVICE_RESOURCE_CHANGE_EVENT = 'permission-center-service-resource-change';
export const MENU_CHANGE_EVENT = 'permission-center-menu-change';
export const DEFAULT_SERVICE_RESOURCE = '';

export type ServiceResourceSource = 'local' | 'nexusauth';

export type ServiceResource = {
  id: string;
  key: string;
  name: string;
  displayName: string;
  audience: string;
  description: string;
  isActive: boolean;
  source: ServiceResourceSource;
  createdAt?: string;
};

export type ServiceResourceList = {
  items: ServiceResource[];
  writable: boolean;
};

export type CreateServiceResourceRequest = {
  key: string;
  display_name: string;
  audience?: string;
  description?: string;
  is_active?: boolean;
};

export type UpdateServiceResourceRequest = {
  display_name: string;
  audience: string;
  description: string;
  is_active: boolean;
};

export type Role = {
  id: string;
  serviceResource: string;
  code: string;
  name: string;
  description: string;
  enabled: boolean;
  createdById?: string;
  createdByName?: string;
  createdAt?: string;
  updatedById?: string;
  updatedByName?: string;
  updatedAt?: string;
};

export type MenuType = 'menu' | 'button';
export type MenuHTTPMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS';

export type Menu = {
  id: string;
  serviceResource: string;
  parentId: string | null;
  code: string;
  name: string;
  description: string;
  type: MenuType;
  path: string;
  component: string;
  apiPath: string;
  httpMethod: MenuHTTPMethod | '';
  icon: string;
  sort: number;
  enabled: boolean;
  createdById?: string;
  createdByName?: string;
  createdAt?: string;
  updatedById?: string;
  updatedByName?: string;
  updatedAt?: string;
  children: Menu[];
};

export type CreateRoleRequest = {
  service_resource: string;
  code: string;
  name: string;
  description?: string;
};

export type UpdateRoleRequest = {
  code: string;
  name: string;
  description: string;
  enabled: boolean;
};

export type CreateMenuRequest = {
  service_resource: string;
  parent_id: string | null;
  code: string;
  name: string;
  description?: string;
  type: MenuType;
  path?: string;
  component?: string;
  api_path?: string;
  http_method?: MenuHTTPMethod | '';
  icon?: string;
  sort?: number;
};

export type UpdateMenuRequest = {
  code: string;
  name: string;
  description: string;
  path: string;
  component: string;
  api_path: string;
  http_method: MenuHTTPMethod | '';
  icon: string;
  sort: number;
  enabled: boolean;
};

export type APIEndpoint = {
  id: string;
  serviceResource: string;
  controller: string;
  method: string;
  pathTemplate: string;
  summary: string;
  enabled: boolean;
  createdById?: string;
  createdByName?: string;
  createdAt?: string;
  updatedById?: string;
  updatedByName?: string;
  updatedAt?: string;
};

export type CreateAPIEndpointRequest = {
  service_resource: string;
  controller: string;
  method: string;
  path_template: string;
  summary?: string;
  enabled?: boolean;
};

export type UpdateAPIEndpointRequest = {
  controller: string;
  method: string;
  path_template: string;
  summary: string;
  enabled: boolean;
};

export type ImportSwaggerAPIEndpointsRequest = {
  service_resource: string;
  swagger_url: string;
};

export type ImportSwaggerAPIEndpointsResult = {
  total: number;
  created: number;
  skipped: number;
};


type RawRecord = Record<string, unknown>;

const rawString = (record: RawRecord, ...keys: string[]): string => {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === 'string') {
      return value;
    }
  }
  return '';
};

const rawBoolean = (record: RawRecord, ...keys: string[]): boolean => {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === 'boolean') {
      return value;
    }
  }
  return false;
};

const rawNumber = (record: RawRecord, ...keys: string[]): number => {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === 'number' && Number.isFinite(value)) {
      return value;
    }
  }
  return 0;
};

const rawDate = (record: RawRecord, ...keys: string[]): string | undefined => {
  const value = rawString(record, ...keys);
  return value || undefined;
};

const normalizeServiceResource = (value: unknown): ServiceResource => {
  const record = (value && typeof value === 'object' ? value : {}) as RawRecord;
  const key = rawString(record, 'key', 'Key') || rawString(record, 'name', 'Name');
  const source = rawString(record, 'source', 'Source') === 'local' ? 'local' : 'nexusauth';
  return {
    id: rawString(record, 'id', 'ID'),
    key,
    name: rawString(record, 'name', 'Name') || key,
    displayName: rawString(record, 'display_name', 'displayName', 'DisplayName'),
    audience: rawString(record, 'audience', 'Audience'),
    description: rawString(record, 'description', 'Description'),
    isActive: rawBoolean(record, 'is_active', 'isActive', 'IsActive'),
    source,
    createdAt: rawDate(record, 'created_at', 'createdAt', 'CreatedAt'),
  };
};

const normalizeRole = (value: unknown): Role => {
  const record = (value && typeof value === 'object' ? value : {}) as RawRecord;
  return {
    id: rawString(record, 'id', 'ID'),
    serviceResource: rawString(record, 'service_resource', 'serviceResource', 'ServiceResource'),
    code: rawString(record, 'code', 'Code'),
    name: rawString(record, 'name', 'Name'),
    description: rawString(record, 'description', 'Description'),
    enabled: rawBoolean(record, 'enabled', 'Enabled'),
    createdById: rawString(record, 'created_by_id', 'createdById', 'CreatedByID') || undefined,
    createdByName: rawString(record, 'created_by_name', 'createdByName', 'CreatedByName') || undefined,
    createdAt: rawDate(record, 'created_at', 'createdAt', 'CreatedAt'),
    updatedById: rawString(record, 'updated_by_id', 'updatedById', 'UpdatedByID') || undefined,
    updatedByName: rawString(record, 'updated_by_name', 'updatedByName', 'UpdatedByName') || undefined,
    updatedAt: rawDate(record, 'updated_at', 'updatedAt', 'UpdatedAt'),
  };
};

const normalizeMenu = (value: unknown): Menu => {
  const record = (value && typeof value === 'object' ? value : {}) as RawRecord;
  const type = rawString(record, 'type', 'Type') === 'button' ? 'button' : 'menu';
  const childrenValue = record.children ?? record.Children;
  const children = Array.isArray(childrenValue) ? childrenValue.map(normalizeMenu) : [];
  const parentID = rawString(record, 'parent_id', 'parentId', 'ParentID');

  return {
    id: rawString(record, 'id', 'ID'),
    serviceResource: rawString(record, 'service_resource', 'serviceResource', 'ServiceResource'),
    parentId: parentID || null,
    code: rawString(record, 'code', 'Code'),
    name: rawString(record, 'name', 'Name'),
    description: rawString(record, 'description', 'Description'),
    type,
    path: rawString(record, 'path', 'Path', 'route', 'Route'),
    component: rawString(record, 'component', 'Component'),
    apiPath: rawString(record, 'api_path', 'apiPath', 'APIPath', 'action', 'Action'),
    httpMethod: normalizeMenuHTTPMethod(rawString(record, 'http_method', 'httpMethod', 'HTTPMethod')),
    icon: rawString(record, 'icon', 'Icon'),
    sort: rawNumber(record, 'sort', 'Sort', 'sort_order', 'sortOrder', 'SortOrder'),
    enabled: rawBoolean(record, 'enabled', 'Enabled'),
    createdById: rawString(record, 'created_by_id', 'createdById', 'CreatedByID') || undefined,
    createdByName: rawString(record, 'created_by_name', 'createdByName', 'CreatedByName') || undefined,
    createdAt: rawDate(record, 'created_at', 'createdAt', 'CreatedAt'),
    updatedById: rawString(record, 'updated_by_id', 'updatedById', 'UpdatedByID') || undefined,
    updatedByName: rawString(record, 'updated_by_name', 'updatedByName', 'UpdatedByName') || undefined,
    updatedAt: rawDate(record, 'updated_at', 'updatedAt', 'UpdatedAt'),
    children,
  };
};

const normalizeMenuHTTPMethod = (value: string): MenuHTTPMethod | '' => {
  const method = value.trim().toUpperCase();
  switch (method) {
    case 'GET':
    case 'POST':
    case 'PUT':
    case 'PATCH':
    case 'DELETE':
    case 'HEAD':
    case 'OPTIONS':
      return method;
    default:
      return '';
  }
};

const normalizeAPIEndpoint = (value: unknown): APIEndpoint => {
  const record = (value && typeof value === 'object' ? value : {}) as RawRecord;
  return {
    id: rawString(record, 'id', 'ID'),
    serviceResource: rawString(record, 'service_resource', 'serviceResource', 'ServiceResource'),
    controller: rawString(record, 'controller', 'Controller'),
    method: rawString(record, 'method', 'Method').toUpperCase(),
    pathTemplate: rawString(record, 'path_template', 'pathTemplate', 'PathTemplate'),
    summary: rawString(record, 'summary', 'Summary'),
    enabled: rawBoolean(record, 'enabled', 'Enabled'),
    createdById: rawString(record, 'created_by_id', 'createdById', 'CreatedByID') || undefined,
    createdByName: rawString(record, 'created_by_name', 'createdByName', 'CreatedByName') || undefined,
    createdAt: rawDate(record, 'created_at', 'createdAt', 'CreatedAt'),
    updatedById: rawString(record, 'updated_by_id', 'updatedById', 'UpdatedByID') || undefined,
    updatedByName: rawString(record, 'updated_by_name', 'updatedByName', 'UpdatedByName') || undefined,
    updatedAt: rawDate(record, 'updated_at', 'updatedAt', 'UpdatedAt'),
  };
};


const readItems = (value: unknown): unknown[] => {
  if (Array.isArray(value)) {
    return value;
  }
  if (value && typeof value === 'object') {
    const record = value as RawRecord;
    const items = record.items ?? record.Items ?? record.data ?? record.Data;
    return Array.isArray(items) ? items : [];
  }
  return [];
};

const readItem = (value: unknown): unknown => {
  if (!value || typeof value !== 'object') {
    return value;
  }
  const record = value as RawRecord;
  return record.item ?? record.Item ?? record.data ?? record.Data ?? value;
};

const readIDs = (value: unknown, key: 'role_ids'): string[] => {
  if (!value || typeof value !== 'object') {
    return [];
  }
  const values = (value as RawRecord)[key];
  return Array.isArray(values) ? values.filter((item): item is string => typeof item === 'string') : [];
};

const dispatchMenuChange = (serviceResource?: string) => {
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent(MENU_CHANGE_EVENT, { detail: serviceResource }));
  }
};

export const getStoredServiceResource = (): string => {
  if (typeof window === 'undefined') {
    return DEFAULT_SERVICE_RESOURCE;
  }
  try {
    return window.sessionStorage.getItem(SERVICE_RESOURCE_STORAGE_KEY)?.trim() || DEFAULT_SERVICE_RESOURCE;
  } catch {
    return DEFAULT_SERVICE_RESOURCE;
  }
};

export const setStoredServiceResource = (serviceResource: string): string => {
  const next = serviceResource.trim();
  if (typeof window !== 'undefined') {
    try {
      if (next) {
        window.sessionStorage.setItem(SERVICE_RESOURCE_STORAGE_KEY, next);
      } else {
        window.sessionStorage.removeItem(SERVICE_RESOURCE_STORAGE_KEY);
      }
      // 旧版本使用 localStorage；清理旧值，避免其他页签覆盖当前会话选择。
      window.localStorage.removeItem(SERVICE_RESOURCE_STORAGE_KEY);
      window.dispatchEvent(new CustomEvent(SERVICE_RESOURCE_CHANGE_EVENT, { detail: next }));
    } catch {
      // Browsers can disable localStorage in private or restricted contexts.
    }
  }
  return next;
};

export const listServiceResources = async (): Promise<ServiceResourceList> => {
  const response = await request.get<unknown>('/v1/service-resources');
  const record = (response && typeof response === 'object' ? response : {}) as RawRecord;
  return { items: readItems(response).map(normalizeServiceResource), writable: rawBoolean(record, 'writable', 'Writable') };
};

export const getServiceResource = async (key: string): Promise<ServiceResource> => {
  const resourceKey = key.trim();
  const response = await request.get<unknown>(`/v1/service-resources/${encodeURIComponent(resourceKey)}`);
  return normalizeServiceResource(readItem(response));
};

export const createServiceResource = async (payload: CreateServiceResourceRequest): Promise<ServiceResource> => {
  const response = await request.post<unknown>('/v1/service-resources', payload);
  return normalizeServiceResource(readItem(response));
};

export const updateServiceResource = async (key: string, payload: UpdateServiceResourceRequest): Promise<ServiceResource> => {
  const response = await request.put<unknown>(`/v1/service-resources/${encodeURIComponent(key.trim())}`, payload);
  return normalizeServiceResource(readItem(response));
};

export const deleteServiceResource = async (key: string): Promise<void> => {
  await request.delete<unknown>(`/v1/service-resources/${encodeURIComponent(key.trim())}`);
};

export const listRoles = async (serviceResource: string): Promise<Role[]> => {
  const response = await request.get<unknown>(`/v1/roles?service_resource=${encodeURIComponent(serviceResource.trim())}`);
  return readItems(response).map(normalizeRole);
};

export const createRole = async (payload: CreateRoleRequest): Promise<Role> => {
  const response = await request.post<unknown>('/v1/roles', payload);
  return normalizeRole(readItem(response));
};

export const updateRole = async (roleID: string, payload: UpdateRoleRequest): Promise<Role> => {
  const response = await request.put<unknown>(`/v1/roles/${encodeURIComponent(roleID)}`, payload);
  return normalizeRole(readItem(response));
};

export const deleteRole = async (roleID: string): Promise<void> => {
  await request.delete<unknown>(`/v1/roles/${encodeURIComponent(roleID)}`);
};

export const getMenuTree = async (serviceResource: string): Promise<Menu[]> => {
  const response = await request.get<unknown>(`/v1/menus/tree?service_resource=${encodeURIComponent(serviceResource.trim())}`);
  return readItems(response).map(normalizeMenu);
};

export const createMenu = async (payload: CreateMenuRequest): Promise<Menu> => {
  const response = await request.post<unknown>('/v1/menus', payload);
  const menu = normalizeMenu(readItem(response));
  dispatchMenuChange(payload.service_resource);
  return menu;
};

export const updateMenu = async (menuID: string, payload: UpdateMenuRequest): Promise<Menu> => {
  const response = await request.put<unknown>(`/v1/menus/${encodeURIComponent(menuID)}`, payload);
  const menu = normalizeMenu(readItem(response));
  dispatchMenuChange();
  return menu;
};

export const deleteMenu = async (menuID: string): Promise<void> => {
  await request.delete<unknown>(`/v1/menus/${encodeURIComponent(menuID)}`);
  dispatchMenuChange();
};

export const listAPIEndpoints = async (serviceResource: string): Promise<APIEndpoint[]> => {
  const response = await request.get<unknown>(
    `/v1/authorization/api-endpoints?service_resource=${encodeURIComponent(serviceResource.trim())}`,
  );
  return readItems(response).map(normalizeAPIEndpoint);
};

export const createAPIEndpoint = async (payload: CreateAPIEndpointRequest): Promise<APIEndpoint> => {
  const response = await request.post<unknown>('/v1/authorization/api-endpoints', payload);
  return normalizeAPIEndpoint(readItem(response));
};

export const updateAPIEndpoint = async (endpointID: string, payload: UpdateAPIEndpointRequest): Promise<APIEndpoint> => {
  const response = await request.put<unknown>(
    `/v1/authorization/api-endpoints/${encodeURIComponent(endpointID)}`,
    payload,
  );
  return normalizeAPIEndpoint(readItem(response));
};

export const deleteAPIEndpoint = async (endpointID: string): Promise<void> => {
  await request.delete<unknown>(`/v1/authorization/api-endpoints/${encodeURIComponent(endpointID)}`);
};

export const importSwaggerAPIEndpoints = async (
  payload: ImportSwaggerAPIEndpointsRequest,
): Promise<ImportSwaggerAPIEndpointsResult> => {
  const response = await request.post<unknown>('/v1/authorization/api-endpoints/import-swagger', payload);
  const item = readItem(response);
  const record = (item && typeof item === 'object' ? item : {}) as RawRecord;
  return {
    total: rawNumber(record, 'total', 'Total'),
    created: rawNumber(record, 'created', 'Created'),
    skipped: rawNumber(record, 'skipped', 'Skipped'),
  };
};


export const getUserRoleIDs = async (userID: string, serviceResource: string): Promise<string[]> => {
  const response = await request.get<unknown>(
    `/v1/users/${encodeURIComponent(userID)}/roles?service_resource=${encodeURIComponent(serviceResource.trim())}`,
  );
  return readIDs(response, 'role_ids');
};

export const replaceUserRoles = async (userID: string, serviceResource: string, roleIDs: string[]): Promise<void> => {
  await request.put<unknown>(
    `/v1/users/${encodeURIComponent(userID)}/roles?service_resource=${encodeURIComponent(serviceResource.trim())}`,
    { role_ids: roleIDs },
  );
};
