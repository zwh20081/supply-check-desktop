import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { isTauri } from '@tauri-apps/api/core';
import { getCurrentWindow } from '@tauri-apps/api/window';
import { LOCALES, MESSAGES, type Locale, type Messages } from './messages';

export { LOCALES, LOCALE_LABELS, type Locale } from './messages';

const LOCALE_KEY = 'supply-check-locale-v1';

/** navigator.language 自动定位；识别不出来就用英文。 */
/** 认不出系统语言时的回退。加新语言时不要动这个。 */
export const FALLBACK_LOCALE: Locale = 'en';

export function detectLocale(): Locale {
  const candidates = [
    ...(typeof navigator !== 'undefined' ? navigator.languages ?? [] : []),
    typeof navigator !== 'undefined' ? navigator.language : '',
  ].filter(Boolean);

  for (const tag of candidates) {
    const lower = tag.toLowerCase();
    if (lower.startsWith('zh')) return 'zh-CN';
    if (lower.startsWith('en')) return 'en';
  }
  return FALLBACK_LOCALE;
}

function loadLocale(): Locale {
  try {
    const saved = localStorage.getItem(LOCALE_KEY);
    // 手动选过就一直沿用，不再被系统语言覆盖
    if (saved && (LOCALES as readonly string[]).includes(saved)) return saved as Locale;
  } catch { /* fall through to detection */ }
  return detectLocale();
}

interface Ctx { locale: Locale; setLocale: (next: Locale) => void; t: Messages; }
const I18nContext = createContext<Ctx | null>(null);

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(loadLocale);

  const setLocale = useCallback((next: Locale) => {
    setLocaleState(next);
    try { localStorage.setItem(LOCALE_KEY, next); } catch { /* ignore quota errors */ }
  }, []);

  useEffect(() => {
    document.documentElement.lang = locale;
    const title = MESSAGES[locale].windowTitle;
    document.title = title;
    // 原生标题栏由 Tauri 管，跟 document.title 是两套，要单独设。
    // getCurrentWindow 在浏览器环境会同步抛错，不能只在 Promise 上 catch。
    if (isTauri()) getCurrentWindow().setTitle(title).catch(() => {});
  }, [locale]);

  const value = useMemo<Ctx>(
    () => ({ locale, setLocale, t: MESSAGES[locale] }),
    [locale, setLocale],
  );

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): Ctx {
  const value = useContext(I18nContext);
  if (!value) throw new Error('useI18n 必须在 I18nProvider 内使用');
  return value;
}
