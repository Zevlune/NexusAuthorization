import request from './request';

// Service resource catalog API used by the workbench services page.

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

export const listServiceResources = async (): Promise<ServiceResourceList> => {
  const response = await request.get<unknown>('/v1/service-resources');
  const record = (response && typeof response === 'object' ? response : {}) as RawRecord;
  return { items: readItems(response).map(normalizeServiceResource), writable: rawBoolean(record, 'writable', 'Writable') };
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
