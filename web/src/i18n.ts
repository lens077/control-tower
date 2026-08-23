import i18next from "i18next";
import { initReactI18next, useTranslation } from "react-i18next";

export { i18next, useTranslation };
export type Locale = "zh-CN" | "en";

export async function initI18n(options: {
  ns: string;
  resources: Record<Locale, Record<string, unknown>>;
  titleKey?: string;
  locale?: Locale;
}): Promise<void> {
  const saved = localStorage.getItem("config-center-locale");
  const locale: Locale = options.locale ?? (saved === "en" ? "en" : "zh-CN");
  if (!i18next.isInitialized) {
    await i18next.use(initReactI18next).init({
      lng: locale,
      fallbackLng: "zh-CN",
      ns: [options.ns],
      defaultNS: options.ns,
      interpolation: { escapeValue: false },
      resources: {},
    });
  }
  for (const [language, resource] of Object.entries(options.resources)) {
    i18next.addResourceBundle(language, options.ns, resource, true, true);
  }
  if (options.titleKey) document.title = i18next.t(options.titleKey);
}

export function useLocale(): { locale: Locale; setLocale: (locale: Locale) => Promise<void> } {
  const { i18n } = useTranslation();
  const locale: Locale = i18n.resolvedLanguage === "en" ? "en" : "zh-CN";
  return {
    locale,
    setLocale: async (next) => {
      localStorage.setItem("config-center-locale", next);
      await i18n.changeLanguage(next);
    },
  };
}

export function formatDate(value: Date | number | string | null | undefined): string {
  if (!value) return "";
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return new Intl.DateTimeFormat(i18next.resolvedLanguage === "en" ? "en" : "zh-CN", {
    dateStyle: "medium",
    timeStyle: "medium",
  }).format(date);
}
