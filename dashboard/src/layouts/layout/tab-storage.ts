export type LayoutTabItem = {
  value: string;
  label: string;
  removable: boolean;
};

export const DEFAULT_LAYOUT_TAB: LayoutTabItem = {
  value: '/dashboard',
  label: '配置总览',
  removable: false,
};

export const LAYOUT_TABS_STORAGE_KEY = 'nexus-authorization-layout-tabs';

export const getStoredLayoutTabs = (): LayoutTabItem[] => {
  const defaultTabs = [{ ...DEFAULT_LAYOUT_TAB }];
  try {
    const raw = window.localStorage.getItem(LAYOUT_TABS_STORAGE_KEY);
    if (!raw) {
      return defaultTabs;
    }

    const parsed = JSON.parse(raw) as LayoutTabItem[];
    if (!Array.isArray(parsed) || parsed.length === 0) {
      return defaultTabs;
    }

    const validTabs = parsed.filter((tab) => tab?.value && tab?.label && tab.value !== '/bindings');
    const hasDashboard = validTabs.some((tab) => tab.value === DEFAULT_LAYOUT_TAB.value);
    return hasDashboard ? validTabs : [...defaultTabs, ...validTabs];
  } catch {
    return defaultTabs;
  }
};

export const storeLayoutTabs = (tabs: LayoutTabItem[]) => {
  try {
    window.localStorage.setItem(LAYOUT_TABS_STORAGE_KEY, JSON.stringify(tabs));
  } catch {
    // Browsers can disable localStorage in private or restricted contexts.
  }
};

export const clearStoredLayoutTabs = () => {
  try {
    window.localStorage.removeItem(LAYOUT_TABS_STORAGE_KEY);
  } catch {
    // Browsers can disable localStorage in private or restricted contexts.
  }
};
