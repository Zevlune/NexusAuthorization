import request from './request';

export type SCIMUser = {
  id: string;
  userName: string;
  externalId?: string;
  name?: { formatted?: string; givenName?: string; familyName?: string };
  emails?: { value: string; primary?: boolean }[];
  active?: boolean;
};

type SCIMUserName = NonNullable<SCIMUser['name']>;
type SCIMUserEmail = NonNullable<SCIMUser['emails']>[number];
type RawRecord = Record<string, unknown>;

const USERS_PATH = '/v1/users';
const PAGE_SIZE = 200;

const isRecord = (value: unknown): value is RawRecord => typeof value === 'object' && value !== null;

const readString = (record: RawRecord, ...keys: string[]): string => {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === 'string') return value;
  }
  return '';
};

const readNumber = (record: RawRecord, ...keys: string[]): number | undefined => {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === 'number' && Number.isFinite(value)) return value;
  }
  return undefined;
};

const readBoolean = (record: RawRecord, ...keys: string[]): boolean | undefined => {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === 'boolean') return value;
  }
  return undefined;
};

const decodeUserName = (value: unknown): SCIMUserName | undefined => {
  if (!isRecord(value)) return undefined;
  const formatted = readString(value, 'formatted', 'Formatted');
  const givenName = readString(value, 'givenName', 'GivenName');
  const familyName = readString(value, 'familyName', 'FamilyName');
  if (!formatted && !givenName && !familyName) return undefined;
  return {
    formatted: formatted || undefined,
    givenName: givenName || undefined,
    familyName: familyName || undefined,
  };
};

const decodeUserEmails = (value: unknown): SCIMUserEmail[] | undefined => {
  if (!Array.isArray(value)) return undefined;
  const emails: SCIMUserEmail[] = [];
  for (const item of value) {
    if (!isRecord(item)) continue;
    const email = readString(item, 'value', 'Value');
    if (!email) continue;
    const primary = readBoolean(item, 'primary', 'Primary');
    emails.push(primary === undefined ? { value: email } : { value: email, primary });
  }
  return emails.length > 0 ? emails : undefined;
};

const decodeUser = (value: unknown): SCIMUser | null => {
  if (!isRecord(value)) return null;
  const id = readString(value, 'id', 'ID').trim();
  if (!id) return null;
  return {
    id,
    userName: readString(value, 'userName', 'UserName'),
    externalId: readString(value, 'externalId', 'ExternalID', 'externalID') || undefined,
    name: decodeUserName(value.name ?? value.Name),
    emails: decodeUserEmails(value.emails ?? value.Emails),
    active: readBoolean(value, 'active', 'Active'),
  };
};

const pickResources = (record: RawRecord): unknown[] => {
  // The NexusAuth provider may emit SCIM "Resources"; accept both casings.
  for (const key of ['resources', 'Resources']) {
    const value = record[key];
    if (Array.isArray(value)) return value;
  }
  return [];
};

const decodeUserList = (value: unknown): { totalResults: number; resources: unknown[] } => {
  if (!isRecord(value)) throw new Error('用户列表格式异常，请重试');
  const totalResults = readNumber(value, 'totalResults', 'TotalResults');
  if (totalResults === undefined || !Number.isInteger(totalResults) || totalResults < 0
    || (!Array.isArray(value.resources) && !Array.isArray(value.Resources))) {
    throw new Error('用户列表格式异常，请重试');
  }
  return {
    totalResults,
    resources: pickResources(value),
  };
};

export const listAllUsers = async (signal?: AbortSignal): Promise<SCIMUser[]> => {
  const users: SCIMUser[] = [];
  const seenIDs = new Set<string>();
  let startIndex = 1;

  for (;;) {
    const response = await request.get<unknown>(
      `${USERS_PATH}?startIndex=${startIndex}&count=${PAGE_SIZE}`,
      { signal },
    );
    const page = decodeUserList(response);

    if (page.resources.length === 0) {
      if (users.length >= page.totalResults) break;
      throw new Error('用户列表未完整加载，请重试');
    }

    const before = seenIDs.size;
    for (const resource of page.resources) {
      const user = decodeUser(resource);
      if (!user) throw new Error('用户列表格式异常，请重试');
      if (seenIDs.has(user.id)) continue;
      seenIDs.add(user.id);
      users.push(user);
    }

    if (users.length >= page.totalResults) break;
    if (seenIDs.size === before) {
      throw new Error('用户列表未完整加载，请重试');
    }

    // SCIM startIndex is a 1-based offset; advance by the number of served resources
    // so servers that cap the page size below count cannot be skipped over.
    startIndex += page.resources.length;
  }

  return users;
};
