export type ThemeMode = 'light' | 'dark';

const THEME_STORAGE_KEY = 'theme-mode';
const THEME_ATTRIBUTE = 'theme-mode';

const isThemeMode = (value: string | null): value is ThemeMode => value === 'light' || value === 'dark';

const getPreferredThemeMode = (): ThemeMode => {
  if (typeof window === 'undefined') {
    return 'light';
  }

  let storedTheme: string | null = null;
  try {
    storedTheme = window.localStorage.getItem(THEME_STORAGE_KEY);
  } catch {
    // Browsers can disable localStorage in private or restricted contexts.
  }
  if (isThemeMode(storedTheme)) {
    return storedTheme;
  }

  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
};

export const getThemeMode = (): ThemeMode => {
  if (typeof document !== 'undefined') {
    const currentTheme = document.documentElement.getAttribute(THEME_ATTRIBUTE);
    if (isThemeMode(currentTheme)) {
      return currentTheme;
    }
  }

  return getPreferredThemeMode();
};

export const applyThemeMode = (theme: ThemeMode) => {
  if (typeof document !== 'undefined') {
    document.documentElement.setAttribute(THEME_ATTRIBUTE, theme);
    const themeColor = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]');
    if (themeColor) {
      themeColor.content = theme === 'dark' ? '#101418' : '#f4f6f8';
    }
  }

  if (typeof window !== 'undefined') {
    try {
      window.localStorage.setItem(THEME_STORAGE_KEY, theme);
    } catch {
      // The theme still applies for this page when it cannot be remembered.
    }
  }
};
