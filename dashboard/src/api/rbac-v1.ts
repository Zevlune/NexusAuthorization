import request from './request';

export type Endpoint = {
  id: string;
  service_resource: string;
  endpoint_key: string;
  method: string;
  path_template: string;
  module: string;
  summary: string;
  source: 'manual' | 'swagger';
  enabled: boolean;
};

export type Role = {
  id: string;
  application_id?: string;
  code: string;
  name: string;
  description: string;
  enabled: boolean;
  endpoint_ids: string[];
  menu_ids: string[];
};

export type SubjectRole = { id: string; subject: string; domain: string; role_id: string };
export type Menu = {
  id: string;
  parent_id: string | null;
  code: string;
  name: string;
  menu_type: 'menu' | 'button';
  route: string;
  component: string;
  icon: string;
  sort_order: number;
  enabled: boolean;
};
/** 后台 wire 兼容类型：历史记录仍可能返回 directory。 */
type MenuWire = Omit<Menu, 'menu_type'> & { menu_type: 'directory' | 'menu' | 'button' };
const normalizeMenu = (item: MenuWire): Menu => ({ ...item, menu_type: item.menu_type === 'button' ? 'button' : 'menu' });
export type PageResult<T> = { items: T[]; total: number; page: number; page_size: number };

const query = (appKey: string, serviceResource?: string) => {
  const params = new URLSearchParams({ app_key: appKey });
  if (serviceResource) params.set('service_resource', serviceResource);
  return `?${params.toString()}`;
};

export const listEndpoints = (appKey: string, page = 1, pageSize = 100) => request.get<PageResult<Endpoint>>(`/v1/rbac-v1/endpoints${query(appKey)}&page=${page}&page_size=${pageSize}`);
export const listAllEndpoints = async (appKey: string, pageSize = 100): Promise<Endpoint[]> => {
  const items: Endpoint[] = [];
  let page = 1;
  while (true) {
    const result = await listEndpoints(appKey, page, pageSize);
    items.push(...(result.items || []));
    if (!result.items?.length || result.items.length < pageSize || (result.total > 0 && items.length >= result.total)) break;
    page += 1;
  }
  return items;
};
export const saveEndpoint = (appKey: string, serviceResource: string, item: Partial<Endpoint>) => request.post<Endpoint>(`/v1/rbac-v1/endpoints${query(appKey, serviceResource)}`, item);
// Importing fetches the document server-side (up to 15s) and then saves every endpoint,
// so it needs far longer than the default request timeout.
const SWAGGER_IMPORT_TIMEOUT_MS = 2 * 60 * 1000;
export const importSwagger = (appKey: string, serviceResource: string, swaggerURL: string) => request.post<{ total: number; created: number; skipped: number }>(`/v1/rbac-v1/endpoints/import-swagger${query(appKey, serviceResource)}`, { swagger_url: swaggerURL }, { timeout: SWAGGER_IMPORT_TIMEOUT_MS });
export const deleteEndpoint = (appKey: string, serviceResource: string, id: string) => request.delete(`/v1/rbac-v1/endpoints/${id}${query(appKey, serviceResource)}`);
export const listRoles = (appKey: string) => request.get<{ items: Role[] }>(`/v1/rbac-v1/roles${query(appKey)}`);
export const saveRole = (appKey: string, item: Partial<Role>) => request.post<Role>(`/v1/rbac-v1/roles${query(appKey)}`, item);
export const deleteRole = (appKey: string, id: string) => request.delete(`/v1/rbac-v1/roles/${id}${query(appKey)}`);
export const listSubjectRoles = (appKey: string) => request.get<{ items: SubjectRole[] }>(`/v1/rbac-v1/subject-roles${query(appKey)}`);
export const replaceSubjectRoles = (appKey: string, subject: string, domain: string, roleIDs: string[]) => request.put(`/v1/rbac-v1/subject-roles${query(appKey)}`, { subject, domain, role_ids: roleIDs });
/** Grants or revokes one role for several subjects atomically, leaving their other roles untouched. */
export const updateRoleSubjects = (appKey: string, roleID: string, domain: string, add: string[], remove: string[]) => request.put(`/v1/rbac-v1/roles/${roleID}/subjects${query(appKey)}`, { domain, add, remove });
export const simulateRBAC = (appKey: string, input: { service_resource: string; subject: string; domain?: string; method: string; path: string }) => request.post(`/v1/rbac-v1/simulate${query(appKey, input.service_resource)}`, input);
export const listMenus = async (appKey: string): Promise<{ items: Menu[] }> => {
  const result = await request.get<{ items: MenuWire[] }>(`/v1/rbac-v1/menus${query(appKey)}`);
  return { items: (result.items || []).map(normalizeMenu) };
};
export const saveMenu = (appKey: string, item: Partial<Menu>) => request.post<Menu>(`/v1/rbac-v1/menus${query(appKey)}`, item);
export const deleteMenu = (appKey: string, id: string) => request.delete(`/v1/rbac-v1/menus/${id}${query(appKey)}`);
