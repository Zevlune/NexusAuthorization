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
  menu_type: 'directory' | 'menu' | 'button';
  route: string;
  component: string;
  icon: string;
  sort_order: number;
  enabled: boolean;
};
export type PageResult<T> = { items: T[]; total: number; page: number; page_size: number };
export type Release = { id: string; version: number; status: string; description: string; published_by: string; published_at: string };

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
export type SwaggerImportInput = string | { swagger_url?: string; swagger_document?: string };
// 兼容旧的 URL 字符串调用，同时支持直接提交 JSON/YAML 文档文本。
export const importSwagger = (appKey: string, serviceResource: string, input: SwaggerImportInput) => request.post<{ total: number; created: number; skipped: number }>(
  `/v1/rbac-v1/endpoints/import-swagger${query(appKey, serviceResource)}`,
  typeof input === 'string' ? { swagger_url: input } : input,
);
export const deleteEndpoint = (appKey: string, serviceResource: string, id: string) => request.delete(`/v1/rbac-v1/endpoints/${id}${query(appKey, serviceResource)}`);
export const listRoles = (appKey: string) => request.get<{ items: Role[] }>(`/v1/rbac-v1/roles${query(appKey)}`);
export const saveRole = (appKey: string, item: Partial<Role>) => request.post<Role>(`/v1/rbac-v1/roles${query(appKey)}`, item);
export const deleteRole = (appKey: string, id: string) => request.delete(`/v1/rbac-v1/roles/${id}${query(appKey)}`);
export const listSubjectRoles = (appKey: string) => request.get<{ items: SubjectRole[] }>(`/v1/rbac-v1/subject-roles${query(appKey)}`);
export const replaceSubjectRoles = (appKey: string, subject: string, domain: string, roleIDs: string[]) => request.put(`/v1/rbac-v1/subject-roles${query(appKey)}`, { subject, domain, role_ids: roleIDs });
export const publishRBAC = (appKey: string, description: string) => request.post<Release>(`/v1/rbac-v1/releases${query(appKey)}`, { description });
export const listRBACReleases = (appKey: string) => request.get<{ items: Release[] }>(`/v1/rbac-v1/releases${query(appKey)}`);
export const simulateRBAC = (appKey: string, input: { service_resource: string; subject: string; domain: string; method: string; path: string }) => request.post(`/v1/rbac-v1/simulate${query(appKey, input.service_resource)}`, input);
export const listMenus = (appKey: string) => request.get<{ items: Menu[] }>(`/v1/rbac-v1/menus${query(appKey)}`);
export const saveMenu = (appKey: string, item: Partial<Menu>) => request.post<Menu>(`/v1/rbac-v1/menus${query(appKey)}`, item);
export const deleteMenu = (appKey: string, id: string) => request.delete(`/v1/rbac-v1/menus/${id}${query(appKey)}`);
