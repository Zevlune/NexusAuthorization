import request from './request';

export type ApiPermissionGroupAction = 'read' | 'write';

export type ApiPermissionGroup = {
  id: string;
  service_resource: string;
  code: string;
  name: string;
  action: ApiPermissionGroupAction;
  enabled: boolean;
  endpoint_ids: string[];
  created_at: string;
  updated_at: string;
};

export type ApiPermissionGroupPayload = {
  id?: string;
  service_resource: string;
  code: string;
  name: string;
  action: ApiPermissionGroupAction;
  enabled: boolean;
  endpoint_ids: string[];
};

const groupQuery = (appKey: string, serviceResource?: string) => {
  const params = new URLSearchParams({ app_key: appKey });
  if (serviceResource) params.set('service_resource', serviceResource);
  return `?${params.toString()}`;
};

const record = (value: unknown): Record<string, unknown> => value && typeof value === 'object' ? value as Record<string, unknown> : {};
const stringValue = (value: unknown): string => typeof value === 'string' ? value : '';
const stringList = (value: unknown): string[] => Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : [];

export const normalizeApiPermissionGroup = (value: unknown): ApiPermissionGroup => {
  const source = record(value);
  return {
    id: stringValue(source.id),
    service_resource: stringValue(source.service_resource ?? source.serviceResource),
    code: stringValue(source.code),
    name: stringValue(source.name),
    action: source.action === 'write' ? 'write' : 'read',
    enabled: source.enabled !== false,
    endpoint_ids: stringList(source.endpoint_ids ?? source.endpointIds),
    created_at: stringValue(source.created_at ?? source.createdAt),
    updated_at: stringValue(source.updated_at ?? source.updatedAt),
  };
};

export const listApiPermissionGroups = async (appKey: string, serviceResource?: string): Promise<{ items: ApiPermissionGroup[] }> => {
  const response = record(await request.get<unknown>(`/v1/api-permission-groups${groupQuery(appKey, serviceResource)}`));
  const items = Array.isArray(response.items) ? response.items.map(normalizeApiPermissionGroup) : [];
  return { items };
};

export const createApiPermissionGroup = async (appKey: string, serviceResource: string, payload: ApiPermissionGroupPayload): Promise<ApiPermissionGroup> =>
  normalizeApiPermissionGroup(await request.post<unknown>(`/v1/api-permission-groups${groupQuery(appKey, serviceResource)}`, payload));

export const deleteApiPermissionGroup = async (appKey: string, id: string, serviceResource?: string): Promise<void> => {
  await request.delete(`/v1/api-permission-groups/${encodeURIComponent(id)}${groupQuery(appKey, serviceResource)}`);
};
