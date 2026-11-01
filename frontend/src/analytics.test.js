import test from "node:test";
import assert from "node:assert/strict";
import { summarize, groups, trend, dateKey, keyDescription, keyLabel, matchesUsageRow, money } from "./analytics.js";
const fixture = [
  {
    ts: new Date(2026, 0, 1, 12).getTime() / 1000,
    model: "a",
    key: "k",
    input: 100,
    cache: 60,
    output: 20,
    reasoning: 5,
    failed: false,
  },
  {
    ts: new Date(2026, 0, 1, 13).getTime() / 1000,
    model: "a",
    key: "k",
    input: 0,
    cache: 0,
    output: 0,
    reasoning: 0,
    failed: true,
  },
];
test("tokens are not double counted and zero-token failures are included", () => {
  const s = summarize(fixture);
  assert.equal(s.input + s.output, 120);
  assert.equal(s.n, 2);
  assert.equal(s.fail, 1);
});
test("grouping and trend use same total", () => {
  assert.equal(groups(fixture, "model")[0].n, 2);
  const b = trend(fixture, "day")[0];
  assert.equal(b.cache + b.net + b.output, 120);
});
test("local date does not use UTC slicing", () =>
  assert.equal(dateKey(new Date(2026, 0, 1)), "2026-01-01"));
test("fees and missing price coverage follow the same filters and grouping", () => {
  const events = [
    { ...fixture[0], cost: 1.77, price_basis: "historical_estimate" },
    { ...fixture[1], cost: 0 },
    { ...fixture[0], model: "unpriced", cost: null },
  ];
  const total = summarize(events);
  assert.equal(total.cost, 1.77);
  assert.equal(total.priced, 2);
  assert.equal(total.unpriced, 1);
  assert.equal(total.historical, 1);
  const known = summarize(events.filter((e) => e.model === "a"));
  assert.equal(known.unpriced, 0);
  assert.equal(groups(events, "model").find((g) => g.name === "unpriced").priced, 0);
  assert.equal(trend(events, "day")[0].cost, total.cost);
  assert.equal(trend(events, "day")[0].unpriced, 1);
});
test("unpriced trend buckets remain gaps and free buckets remain zero", () => {
  const buckets = trend([{ ...fixture[0], cost: null }, { ...fixture[1], cost: 0 }], "hour");
  assert.equal(buckets[0].cost, null);
  assert.equal(buckets[1].cost, 0);
  assert.equal(money(null), "未定价");
  assert.equal(money(0), "$0.00");
  assert.equal(money(0.0000001), "< $0.000001");
});

test("client grouping uses full IDs even when display labels collide, and keeps history separate", () => {
  const events = [
    { ...fixture[0], key: "client-abcdefghij111", key_label: "key-abcdefghij", key_kind: "client", cost: 1 },
    { ...fixture[0], model: "another-model", key: "client-abcdefghij111", key_label: "key-abcdefghij", key_kind: "client", cost: 2 },
    { ...fixture[0], key: "client-abcdefghij222", key_label: "key-abcdefghij", key_kind: "client", cost: 3 },
    { ...fixture[0], key: "legacy-same-source", key_label: "历史来源 · key-abcdefghij", key_kind: "legacy_source", cost: 4 },
    { ...fixture[0], key: "client:unknown", key_label: "未知客户端", key_kind: "unknown", cost: 5 },
  ];
  const grouped = groups(events, "key");
  assert.equal(grouped.length, 4);
  assert.equal(grouped.find((row) => row.name === events[0].key).n, 2);
  assert.equal(grouped.reduce((sum, row) => sum + row.cost, 0), summarize(events).cost);
  assert.equal(grouped.reduce((sum, row) => sum + row.input + row.output, 0), summarize(events).input + summarize(events).output);
  assert.equal(groups(events, "model").find((row) => row.name === "a").n, 4);
  const legacy = grouped.find((row) => row.key_kind === "legacy_source");
  assert.equal(keyLabel(legacy), "历史来源 · key-abcdefghij");
  assert.equal(keyDescription(legacy), "客户端未知 · 历史来源");
  assert.equal(keyDescription(events[4]), "未提供客户端 Key");
});

test("usage searches match visible labels, full identities, and legacy/unknown descriptions", () => {
  const row = { name: "client-full-id-12345", key_label: "key-short-id", key_kind: "client" };
  assert.equal(matchesUsageRow(row, " SHORT-ID "), true);
  assert.equal(matchesUsageRow(row, "full-id"), true);
  assert.equal(matchesUsageRow(row, "客户端"), true);
  assert.equal(matchesUsageRow({ key: "legacy-id", key_kind: "legacy_source" }, "历史来源"), true);
  assert.equal(matchesUsageRow({ key_label: "未知客户端", key_kind: "unknown" }, "未知客户端"), true);
  assert.equal(matchesUsageRow({ model: "my-model", key: "client-id" }, "my-model"), true);
  assert.equal(matchesUsageRow(row, "missing-label"), false);
});

test("notes change labels and search without merging clients or losing their original labels", () => {
  const events = [
    { ...fixture[0], key: "client-first", key_label: "key-first", key_kind: "client", key_note: "办公 & <电脑>" },
    { ...fixture[0], key: "client-second", key_label: "key-second", key_kind: "client", key_note: "办公 & <电脑>" },
  ];
  const rows = groups(events, "key");
  assert.equal(rows.length, 2);
  assert.equal(rows[0].n, 1);
  assert.equal(keyLabel(rows[0]), "办公 & <电脑>");
  assert.equal(rows[0].key_label, "key-first");
  assert.equal(keyDescription(rows[0]), "key-first · 客户端 Key");
  assert.equal(matchesUsageRow(rows[0], "办公"), true);
  assert.equal(matchesUsageRow(rows[0], "key-first"), true);
  assert.equal(keyLabel({ ...rows[0], key_note: "" }), "key-first");
  assert.equal(summarize(events).n, rows.reduce((total, row) => total + row.n, 0));
});
