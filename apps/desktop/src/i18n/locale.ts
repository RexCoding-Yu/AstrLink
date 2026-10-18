export const LOCALES = ["en", "zh-CN", "ja"] as const;

export type Locale = (typeof LOCALES)[number];

export const DEFAULT_LOCALE: Locale = "en";

/** Each language named in itself, so a reader can find their own. */
export const LOCALE_NAMES: Readonly<Record<Locale, string>> = {
  en: "English",
  "zh-CN": "简体中文",
  ja: "日本語",
};

export function isLocale(value: unknown): value is Locale {
  return (LOCALES as readonly unknown[]).includes(value);
}
