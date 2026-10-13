import request from './request';

export type ApiTokenStatus = 'active' | 'disabled' | 'revoked' | 'expired';
export type ApiTokenOwnerType = 'user' | 'service';
export type ApiTokenAction = 'read' | 'write';

export type ApiTokenPermission = {
  endpoint_id: string;
  service_resource: string;
  action: ApiTokenAction;
  tenant_id: string;
  project_id: string;
  method?: string;
  path_template?: string;
  summary?: string;
};

export type ApiTokenPolicy = {
  id: string;
  effect: 'allow';
  service_resource: string;
  tenant_id: string;
  project_id: string;
  permission_group_ids: string[];
};

export type ApiToken = {
  id: string;
  app_key: string;
  name: string;
  owner_type: ApiTokenOwnerType;
  owner_id: string;
  token_prefix: string;
  status: ApiTokenStatus;
  expires_at: string | null;
  last_used_at: string | null;
  created_by: string;
  created_at: string;
  updated_at: string;
  revoked_at: string | null;
  permissions: ApiTokenPermission[];
  policies: ApiTokenPolicy[];
};

export type ApiTokenPayload = {
  name: string;
  expires_at: string | null;
  owner_type?: ApiTokenOwnerType;
  owner_id?: string;
  permissions?: ApiTokenPermission[];
  policies?: ApiTokenPolicy[];
};

export type ApiTokenList = { items: ApiToken[]; total: number; page: number; page_size: number };
export type ApiTokenSecret = { token: ApiToken; raw_token: string };

const appQuery = (appKey: string, extra: Record<string, string | number | undefined> = {}) => {
  const params = new URLSearchParams({ app_key: appKey });
  Object.entries(extra).forEach(([key, value]) => {
    if (value !== undefined && value !== '') params.set(key, String(value));
  });
  return `?${params.toString()}`;
};

const record = (value: unknown): Record<string, unknown> => value && typeof value === 'object' ? value as Record<string, unknown> : {};
const stringValue = (value: unknown): string => typeof value === 'string' ? value : '';
const nullableString = (value: unknown): string | null => stringValue(value) || null;
const stringList = (value: unknown): string[] => Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : [];

export const normalizeApiToken = (value: unknown): ApiToken => {
  const source = record(value);
  const permissions = Array.isArray(source.permissions) ? source.permissions : [];
  const policies = Array.isArray(source.policies) ? source.policies : [];
  return {
    id: stringValue(source.id),
    app_key: stringValue(source.app_key ?? source.appKey),
    name: stringValue(source.name),
    owner_type: source.owner_type === 'service' ? 'service' : 'user',
    owner_id: stringValue(source.owner_id ?? source.ownerId),
    token_prefix: stringValue(source.token_prefix ?? source.tokenPrefix),
    status: ['active', 'disabled', 'revoked', 'expired'].includes(String(source.status)) ? source.status as ApiTokenStatus : 'disabled',
    expires_at: nullableString(source.expires_at ?? source.expiresAt),
    last_used_at: nullableString(source.last_used_at ?? source.lastUsedAt),
    created_by: stringValue(source.created_by ?? source.createdBy),
    created_at: stringValue(source.created_at ?? source.createdAt),
    updated_at: stringValue(source.updated_at ?? source.updatedAt),
    revoked_at: nullableString(source.revoked_at ?? source.revokedAt),
    permissions: permissions.map(item => {
      const permission = record(item);
      return {
        endpoint_id: stringValue(permission.endpoint_id ?? permission.endpointId),
        service_resource: stringValue(permission.service_resource ?? permission.serviceResource),
        action: permission.action === 'read' ? 'read' : 'write',
        tenant_id: stringValue(permission.tenant_id ?? permission.tenantId),
        project_id: stringValue(permission.project_id ?? permission.projectId),
        method: stringValue(permission.method) || undefined,
        path_template: stringValue(permission.path_template ?? permission.pathTemplate) || undefined,
        summary: stringValue(permission.summary) || undefined,
      };
    }),
    policies: policies.map(item => {
      const policy = record(item);
      return {
        id: stringValue(policy.id),
        effect: 'allow' as const,
        service_resource: stringValue(policy.service_resource ?? policy.serviceResource),
        tenant_id: stringValue(policy.tenant_id ?? policy.tenantId),
        project_id: stringValue(policy.project_id ?? policy.projectId),
        permission_group_ids: stringList(policy.permission_group_ids ?? policy.permissionGroupIds),
      };
    }),
  };
};

const normalizeList = (value: unknown): ApiTokenList => {
  const source = record(value);
  const items = Array.isArray(source.items) ? source.items.map(normalizeApiToken) : [];
  return {
    items,
    total: typeof source.total === 'number' ? source.total : items.length,
    page: typeof source.page === 'number' ? source.page : 1,
    page_size: typeof source.page_size === 'number' ? source.page_size : 25,
  };
};

export const listApiTokens = async (appKey: string, page: number, pageSize: number, search: string, status?: ApiTokenStatus | 'all'): Promise<ApiTokenList> => {
  const response = await request.get<unknown>(`/v1/api-tokens${appQuery(appKey, { page, page_size: pageSize, search: search.trim() || undefined, status: status && status !== 'all' ? status : undefined })}`);
  return normalizeList(response);
};

export const createApiToken = async (appKey: string, payload: ApiTokenPayload): Promise<ApiTokenSecret> => {
  const response = record(await request.post<unknown>(`/v1/api-tokens${appQuery(appKey)}`, payload));
  return { token: normalizeApiToken(response.token), raw_token: stringValue(response.raw_token) };
};

export const getApiToken = async (appKey: string, id: string): Promise<ApiToken> => normalizeApiToken(await request.get<unknown>(`/v1/api-tokens/${encodeURIComponent(id)}${appQuery(appKey)}`));

export const updateApiToken = async (appKey: string, id: string, payload: ApiTokenPayload): Promise<ApiToken> => normalizeApiToken(await request.put<unknown>(`/v1/api-tokens/${encodeURIComponent(id)}${appQuery(appKey)}`, payload));

export const updateApiTokenStatus = async (appKey: string, id: string, status: 'active' | 'disabled'): Promise<ApiToken> => normalizeApiToken(await request.post<unknown>(`/v1/api-tokens/${encodeURIComponent(id)}/status${appQuery(appKey)}`, { status }));

export const revokeApiToken = async (appKey: string, id: string): Promise<ApiToken> => normalizeApiToken(await request.post<unknown>(`/v1/api-tokens/${encodeURIComponent(id)}/revoke${appQuery(appKey)}`, {}));

export const rotateApiToken = async (appKey: string, id: string, revokeOld: boolean): Promise<ApiTokenSecret> => {
  const response = record(await request.post<unknown>(`/v1/api-tokens/${encodeURIComponent(id)}/rotate${appQuery(appKey)}`, { revoke_old: revokeOld }));
  return { token: normalizeApiToken(response.token), raw_token: stringValue(response.raw_token) };
};
