export const fmt = (n) => Number(n || 0).toLocaleString("zh-CN");
export const compact = (n) =>
  new Intl.NumberFormat("en", {
    notation: "compact",
    maximumFractionDigits: 2,
  }).format(n || 0);
export const DEFAULT_FX_RATE = 7.2;
export const convertUSD = (n, currency = "USD", fxRate = DEFAULT_FX_RATE) =>
  Number.isFinite(n) && currency === "CNY" ? n * fxRate : n;
export const money = (n, currency = "USD", fxRate = DEFAULT_FX_RATE, maximumFractionDigits = 6) => {
  if (!Number.isFinite(n)) return "未定价";
  const value = convertUSD(n, currency, fxRate);
  const symbol = currency === "CNY" ? "¥" : "$";
  if (value > 0 && value < 0.000001) return `< ${symbol}0.000001`;
  return new Intl.NumberFormat("en-US", {
    style: "currency", currency: currency === "CNY" ? "CNY" : "USD", currencyDisplay: "narrowSymbol", minimumFractionDigits: 2, maximumFractionDigits,
  }).format(value);
};
export const dateKey = (d) =>
  `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
export const keyLabel = (event) => event.key_note || event.key_label || event.key || "未知客户端";
export const keyDescription = (event) => [event.key_note && (event.key_label || event.key), event.key_kind === "legacy_source"
  ? "客户端未知 · 历史来源"
  : event.key_kind === "unknown" ? "未提供客户端 Key" : "客户端 Key"].filter(Boolean).join(" · ");
export const matchesUsageRow = (row, query) =>
  [row.name, row.model, row.key, row.key_label, row.key_note, row.key_kind ? keyDescription(row) : ""]
    .filter(Boolean).join(" ").toLowerCase().includes(query.trim().toLowerCase());
export function summarize(events) {
  return events.reduce(
    (s, e) => ({
      n: s.n + 1,
      input: s.input + e.input,
      output: s.output + e.output,
      cache: s.cache + e.cache,
      reasoning: s.reasoning + e.reasoning,
      fail: s.fail + Number(e.failed),
      cost: s.cost + (Number.isFinite(e.cost) ? e.cost : 0),
      priced: s.priced + Number(Number.isFinite(e.cost)),
      unpriced: s.unpriced + Number(!Number.isFinite(e.cost)),
      historical: s.historical + Number(e.price_basis === "historical_estimate"),
    }),
    { n: 0, input: 0, output: 0, cache: 0, reasoning: 0, fail: 0, cost: 0, priced: 0, unpriced: 0, historical: 0 },
  );
}
export function groups(events, key) {
  const m = new Map();
  for (const e of events) {
    if (!m.has(e[key])) m.set(e[key], []);
    m.get(e[key]).push(e);
  }
  return [...m]
    .map(([name, evs]) => ({ name, ...summarize(evs), ...(key === "key" ? {
      key: name, key_label: evs[0].key_label || evs[0].key, key_note: evs[0].key_note || "", key_kind: evs[0].key_kind,
    } : {}) }))
    .sort((a, b) => b.input + b.output - (a.input + a.output));
}
export function trend(events, bucket) {
  const map = new Map();
  for (const e of events) {
    const d = new Date(e.ts * 1000),
      label =
        bucket === "month"
          ? dateKey(d).slice(0, 7)
          : bucket === "hour"
            ? `${dateKey(d)} ${String(d.getHours()).padStart(2, "0")}:00`
            : dateKey(d);
    if (!map.has(label))
      map.set(label, { label, cache: 0, net: 0, output: 0, requests: 0, cost: null, unpriced: 0 });
    const b = map.get(label);
    b.cache += e.cache;
    b.net += Math.max(0, e.input - e.cache);
    b.output += e.output;
    b.requests++;
    if (Number.isFinite(e.cost)) b.cost = (b.cost ?? 0) + e.cost;
    else b.unpriced++;
  }
  return [...map.values()].sort((a, b) => a.label.localeCompare(b.label));
}
