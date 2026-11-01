export function usageQuery(range, bucket) {
  // Construct local calendar midnights separately: DST days can be 23 or 25 hours.
  const start = new Date(range.from.getFullYear(), range.from.getMonth(), range.from.getDate());
  const end = new Date(range.to.getFullYear(), range.to.getMonth(), range.to.getDate() + 1);
  if (!Number.isFinite(start.getTime()) || !Number.isFinite(end.getTime()) || start >= end) {
    throw Error("请选择有效的开始和结束日期。");
  }
  const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  if (!timezone) throw Error("无法读取浏览器时区，请检查浏览器设置。");
  return new URLSearchParams({ start_at: start.toISOString(), end_at: end.toISOString(), timezone, bucket }).toString();
}
