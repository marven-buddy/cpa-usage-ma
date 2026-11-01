import { convertUSD, DEFAULT_FX_RATE } from "./analytics.js";

export const CURRENCY_STORAGE_KEY = "cpa-usage-ma-currency";
export const validFxRate = (n) => Number.isFinite(n) && n > 0;
export function readCurrencySettings(storage) {
  try {
    const saved = JSON.parse((storage ?? localStorage).getItem(CURRENCY_STORAGE_KEY));
    return {
      currency: saved?.currency === "CNY" ? "CNY" : "USD",
      fxRate: validFxRate(saved?.fxRate) ? saved.fxRate : DEFAULT_FX_RATE,
    };
  } catch {
    return { currency: "USD", fxRate: DEFAULT_FX_RATE };
  }
}
export function saveCurrencySettings(settings, storage) {
  try { (storage ?? localStorage).setItem(CURRENCY_STORAGE_KEY, JSON.stringify(settings)); } catch {}
}

export function priceInUSD(value, original, currency, fxRate) {
  if (value === "") return null;
  const n = Number(value);
  // Preserve exact USD rates when a converted field was left unchanged.
  if (n === convertUSD(original, currency, fxRate)) return original;
  return currency === "CNY" ? n / fxRate : n;
}
