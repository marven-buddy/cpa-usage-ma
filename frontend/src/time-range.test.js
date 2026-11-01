import test from "node:test";
import assert from "node:assert/strict";
import { usageQuery } from "./time-range.js";
import { trend } from "./analytics.js";

function inTimezone(zone, run) {
  const previous = process.env.TZ;
  process.env.TZ = zone;
  try { run(); } finally {
    if (previous === undefined) delete process.env.TZ;
    else process.env.TZ = previous;
  }
}
const queryForDay = (year, month, day) => new URLSearchParams(usageQuery({
  from: new Date(year, month - 1, day, 12), to: new Date(year, month - 1, day, 18),
}, "day"));

test("Shanghai calendar dates send UTC bounds and an explicit IANA timezone", () => {
  inTimezone("Asia/Shanghai", () => {
    const query = queryForDay(2026, 9, 10);
    assert.equal(query.get("start_at"), "2026-09-09T16:00:00.000Z");
    assert.equal(query.get("end_at"), "2026-09-10T16:00:00.000Z");
    assert.equal(query.get("timezone"), "Asia/Shanghai");
    assert.equal(query.get("bucket"), "day");
    assert.equal(query.has("start"), false);
    const events = ["2026-09-09T16:00:00Z", "2026-09-10T15:59:59.999Z"].map((ts) => ({ ts: Date.parse(ts) / 1000, input: 10, cache: 0, output: 2, cost: 0 }));
    assert.deepEqual(trend(events, "day").map((b) => [b.label, b.requests]), [["2026-09-10", 2]]);
    assert.deepEqual(trend(events, "hour").map((b) => b.label), ["2026-09-10 00:00", "2026-09-10 23:00"]);
  });
});

test("DST days use calendar midnights, keeping adjacent ranges continuous", () => {
  inTimezone("America/New_York", () => {
    for (const [month, day, hours, start, end] of [
      [3, 8, 23, "2026-03-08T05:00:00.000Z", "2026-03-09T04:00:00.000Z"],
      [11, 1, 25, "2026-11-01T04:00:00.000Z", "2026-11-02T05:00:00.000Z"],
    ]) {
      const query = queryForDay(2026, month, day);
      assert.equal(query.get("start_at"), start);
      assert.equal(query.get("end_at"), end);
      assert.equal((Date.parse(end) - Date.parse(start)) / 3600000, hours);
      assert.equal(queryForDay(2026, month, day - 1).get("end_at"), start);
      assert.equal(queryForDay(2026, month, day + 1).get("start_at"), end);
      const events = Array.from({ length: hours }, (_, hour) => ({ ts: Date.parse(start) / 1000 + hour * 3600, input: 1, cache: 0, output: 0, cost: 0 }));
      const bins = trend(events, "day");
      assert.equal(bins.length, 1);
      assert.equal(bins[0].requests, hours);
    }
  });
});

test("multi-day and leap-day ranges include the entire final local date", () => {
  inTimezone("Asia/Shanghai", () => {
    const query = new URLSearchParams(usageQuery({ from: new Date(2028, 1, 28), to: new Date(2028, 1, 29) }, "month"));
    assert.equal(query.get("start_at"), "2028-02-27T16:00:00.000Z");
    assert.equal(query.get("end_at"), "2028-02-29T16:00:00.000Z");
    assert.equal(query.get("bucket"), "month");
    assert.throws(() => usageQuery({ from: new Date(NaN), to: new Date() }, "day"), /有效/);
    assert.throws(() => usageQuery({ from: new Date(2026, 9, 2), to: new Date(2026, 9, 1) }, "day"), /有效/);
  });
});
