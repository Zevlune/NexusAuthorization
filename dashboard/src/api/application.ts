import request from './request';

export const APPLICATION_STORAGE_KEY = 'permission-center-application';
export const APPLICATION_CHANGE_EVENT = 'permission-center-application-change';

export type Application = {
  key: string;
  displayName: string;
  description: string;
  enabled: boolean;
  url?: string;
};

export type ApplicationServiceResource = {
  id: string;
  key: string;
  name: string;
  displayName: string;
  audience: string;
  description: string;
  isActive: boolean;
  source: 'local' | 'nexusauth';
};

type RawRecord = Record<string, unknown>;

const rawString = (record: RawRecord, ...keys: string[]): string => {
  for (const key of keys) {
    if (typeof record[key] === 'string') return record[key] as string;
  }
  return '';
};

const rawBoolean = (record: RawRecord, ...keys: string[]): boolean => {
  for (const key of keys) {
    if (typeof record[key] === 'boolean') return record[key] as boolean;
  }
  return false;
};

const hasBoolean = (record: RawRecord, ...keys: string[]): boolean => keys.some(key => typeof record[key] === 'boolean');

const readItems = (value: unknown): unknown[] => {
  if (Array.isArray(value)) return value;
  if (value && typeof value === 'object') {
    const record = value as RawRecord;
    const items = record.items ?? record.Items ?? record.data ?? record.Data;
    return Array.isArray(items) ? items : [];
  }
  return [];
};

const readItem = (value: unknown): unknown => {
  if (!value || typeof value !== 'object') return value;
  const record = value as RawRecord;
  return record.item ?? record.Item ?? record.data ?? record.Data ?? value;
};

const normalizeApplication = (value: unknown): Application => {
  const record = (value && typeof value === 'object' ? value : {}) as RawRecord;
  return {
    key: rawString(record, 'key', 'Key'),
    displayName: rawString(record, 'display_name', 'displayName', 'DisplayName'),
    description: rawString(record, 'description', 'Description'),
    url: rawString(record, 'url', 'URL'),
    // Some create responses only echo the application fields. New applications
    // are enabled by default; an explicit false from the server still wins.
    enabled: hasBoolean(record, 'enabled', 'Enabled') ? rawBoolean(record, 'enabled', 'Enabled') : true,
  };
};

const normalizeServiceResource = (value: unknown): ApplicationServiceResource => {
  const record = (value && typeof value === 'object' ? value : {}) as RawRecord;
  const key = rawString(record, 'key', 'Key') || rawString(record, 'name', 'Name');
  return {
    id: rawString(record, 'id', 'ID'),
    key,
    name: rawString(record, 'name', 'Name') || key,
    displayName: rawString(record, 'display_name', 'displayName', 'DisplayName') || key,
    audience: rawString(record, 'audience', 'Audience'),
    description: rawString(record, 'description', 'Description'),
    isActive: rawBoolean(record, 'is_active', 'isActive', 'IsActive'),
    source: rawString(record, 'source', 'Source') === 'local' ? 'local' : 'nexusauth',
  };
};

const dispatchApplicationChange = (key?: string) => {
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent(APPLICATION_CHANGE_EVENT, { detail: key || '' }));
  }
};

export const getStoredApplication = (): string => {
  if (typeof window === 'undefined') return '';
  try {
    return window.sessionStorage.getItem(APPLICATION_STORAGE_KEY)?.trim() || '';
  } catch {
    return '';
  }
};

export const setStoredApplication = (key: string): string => {
  const next = key.trim();
  if (typeof window !== 'undefined') {
    try {
      if (next) window.sessionStorage.setItem(APPLICATION_STORAGE_KEY, next);
      else window.sessionStorage.removeItem(APPLICATION_STORAGE_KEY);
      window.dispatchEvent(new CustomEvent(APPLICATION_CHANGE_EVENT, { detail: next }));
    } catch {
      // Browser storage can be unavailable in private or restricted contexts.
    }
  }
  return next;
};

export const listApplications = async (): Promise<Application[]> => {
  const response = await request.get<unknown>('/v1/applications');
  return readItems(response).map(normalizeApplication);
};

export const saveApplication = async (payload: Application, editingKey?: string): Promise<Application> => {
  const response = editingKey
    ? await request.put<unknown>(`/v1/applications/${encodeURIComponent(editingKey)}`, {
      display_name: payload.displayName,
      description: payload.description,
      enabled: payload.enabled,
      url: payload.url?.trim() || '',
    })
    : await request.post<unknown>('/v1/applications', {
      key: payload.key,
      display_name: payload.displayName,
      description: payload.description,
      enabled: payload.enabled,
      url: payload.url?.trim() || '',
    });
  return normalizeApplication(readItem(response));
};

export const listApplicationServiceResources = async (appKey: string): Promise<ApplicationServiceResource[]> => {
  const response = await request.get<unknown>(`/v1/applications/${encodeURIComponent(appKey)}/service-resources`);
  return readItems(response).map(normalizeServiceResource);
};

export const replaceApplicationServiceResources = async (appKey: string, serviceResourceKeys: string[]) => {
  await request.put<unknown>(`/v1/applications/${encodeURIComponent(appKey)}/service-resources`, {
    service_resource_keys: serviceResourceKeys,
  });
};

export { normalizeApplication, normalizeServiceResource, dispatchApplicationChange };
