import React, { useEffect, useMemo, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  Activity,
  ArrowDownUp,
  CalendarDays,
  Check,
  ChevronLeft,
  ChevronRight,
  CircleHelp,
  Database,
  KeyRound,
  Layers,
  Moon,
  Pencil,
  RefreshCw,
  Search,
  Sun,
  WifiOff,
} from "lucide-react";
import { format, subDays } from "date-fns";
import { zhCN } from "date-fns/locale";
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { SegmentedControl } from "@/components/segmented-control";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { Calendar } from "@/components/ui/calendar";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import { Separator } from "@/components/ui/separator";
import { PriceDialog } from "@/components/price-dialog";
import { CurrencyControl } from "@/components/currency-control";
import { AuthDialog } from "@/components/auth-dialog";
import { KeyNoteDialog } from "@/components/key-note-dialog";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart";
import { compact, convertUSD, dateKey, fmt, groups, keyDescription, keyLabel, matchesUsageRow, money, summarize, trend } from "./analytics";
import { readCurrencySettings, saveCurrencySettings } from "./currency";
import { AuthError, createManagementClient } from "./auth";
import { usageQuery } from "./time-range";
import "./index.css";

const chartConfig = {
  cache: { label: "缓存输入", color: "var(--chart-1)" },
  net: { label: "净输入", color: "var(--chart-2)" },
  output: { label: "输出", color: "var(--chart-3)" },
};
const costChartConfig = { cost: { label: "预估费用", color: "var(--chart-3)" } };
const initialRange = () => {
  const today = new Date();
  return { from: today, to: today };
};
function Help({ children }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className="size-5 text-muted-foreground"
          aria-label="指标说明"
        >
          <CircleHelp className="size-3.5" />
        </Button>
      </TooltipTrigger>
      <TooltipContent className="max-w-72">{children}</TooltipContent>
    </Tooltip>
  );
}
function useCountUp(target, duration = 550) {
  const [value, setValue] = useState(target);
  const prev = useRef(target);
  useEffect(() => {
    const from = prev.current;
    prev.current = target;
    if (from === target) {
      setValue(target);
      return;
    }
    if (window.matchMedia?.("(prefers-reduced-motion: reduce)").matches) {
      setValue(target);
      return;
    }
    let raf;
    const start = performance.now();
    const tick = (now) => {
      const progress = Math.min(1, (now - start) / duration);
      const eased = 1 - Math.pow(1 - progress, 3);
      setValue(from + (target - from) * eased);
      if (progress < 1) raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [target, duration]);
  return value;
}

function Metric({ label, value, raw, format, detail, help, loading, className = "" }) {
  const animated = useCountUp(typeof raw === "number" ? raw : 0);
  const display =
    typeof raw === "number" && format ? format(animated) : value;
  return (
    <Card className={`gap-3 shadow-none ${className}`}>
      <CardHeader className="flex flex-row items-center justify-between pb-0">
        <CardDescription>{label}</CardDescription>
        <Help>{help}</Help>
      </CardHeader>
      <CardContent>
        {loading ? (
          <Skeleton className="h-9 w-28" />
        ) : (
          <div
            className="text-2xl sm:text-3xl font-semibold tracking-tight tabular-nums"
            title={String(value)}
          >
            {display}
          </div>
        )}
        <p className="mt-2 text-xs text-muted-foreground">{detail}</p>
      </CardContent>
    </Card>
  );
}
// Accessing browser storage itself can fail when storage is disabled.
const browserStorage = (name) => { try { return window[name]; } catch { return undefined; } };
const authClient = createManagementClient({
  localStorage: browserStorage("localStorage"),
  sessionStorage: browserStorage("sessionStorage"),
  host: location.host,
  userAgent: navigator.userAgent,
  fetch: (...args) => fetch(...args),
  devKey: import.meta.env.DEV && ["127.0.0.1", "localhost", "[::1]"].includes(location.hostname)
    ? import.meta.env.VITE_CPA_DEV_KEY || "" : "",
});
const credentialSource = () => { try { return authClient.credential().source; } catch { return ""; } };

function App() {
  const [noteTarget, setNoteTarget] = useState(null);
  const [authOpen, setAuthOpen] = useState(false);
  const [authIssue, setAuthIssue] = useState("");
  const [authSource, setAuthSource] = useState(credentialSource);
  const [authNotice, setAuthNotice] = useState("");
  const [range, setRange] = useState(initialRange),
    [draft, setDraft] = useState(initialRange),
    [calendarOpen, setCalendarOpen] = useState(false),
    [bucket, setBucket] = useState("hour"),
    [chartMetric, setChartMetric] = useState("tokens"),
    [currencySettings, setCurrencySettings] = useState(readCurrencySettings),
    [model, setModel] = useState("all"),
    [data, setData] = useState(null),
    [error, setError] = useState(""),
    [loading, setLoading] = useState(true),
    [busy, setBusy] = useState(false),
    [query, setQuery] = useState(""),
    [tab, setTab] = useState("model"),
    [page, setPage] = useState(0),
    [sort, setSort] = useState("tokens"),
    [dark, setDark] = useState(
      () => { try { return browserStorage("localStorage")?.getItem("cpa-usage-theme") === "dark"; } catch { return false; } },
    );
  const generation = useRef(0);
  const authRevision = useRef(0);
  const { currency, fxRate } = currencySettings;
  const displayMoney = (n) => money(n, currency, fxRate);
  useEffect(() => saveCurrencySettings(currencySettings), [currencySettings]);
  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark);
    try { browserStorage("localStorage")?.setItem("cpa-usage-theme", dark ? "dark" : "light"); } catch { /* Theme remains page-local. */ }
  }, [dark]);
  async function request(path, options = {}) {
    const revision = authRevision.current;
    try {
      const result = await authClient.request(path, options);
      if (revision === authRevision.current) { setAuthSource(credentialSource()); setAuthIssue(""); }
      return result;
    } catch (e) {
      if (revision === authRevision.current && e instanceof AuthError) { setAuthIssue(e.message); setAuthOpen(true); }
      throw e;
    }
  }
  async function load() {
    const id = ++generation.current;
    setBusy(true);
    try {
      const d = await request(`/usage?${usageQuery(range, bucket)}`);
      if (id === generation.current) {
        setData(d);
        setError("");
      }
    } catch (e) {
      if (id === generation.current) setError(e.message);
    } finally {
      if (id === generation.current) {
        setBusy(false);
        setLoading(false);
      }
    }
  }
  useEffect(() => {
    load();
    const timer = setInterval(() => {
      if (!document.hidden) load();
    }, 60000);
    return () => {
      clearInterval(timer);
      generation.current++;
    };
  }, [range, bucket]);
  const events = useMemo(
    () =>
      (data?.events || []).filter((e) => model === "all" || e.model === model),
    [data, model],
  );
  const stats = useMemo(() => summarize(events), [events]),
    buckets = useMemo(() => trend(events, bucket), [events, bucket]);
  const legacyRequests = events.filter((event) => event.key_kind === "legacy_source").length;
  const costBuckets = useMemo(() => buckets.map((b) => ({
    ...b, cost: convertUSD(b.cost, currency, fxRate),
  })), [buckets, currency, fxRate]);
  const modelNames = useMemo(
    () => [...new Set((data?.events || []).map((e) => e.model))].sort(),
    [data],
  );
  useEffect(() => {
    if (model !== "all" && !modelNames.includes(model)) setModel("all");
  }, [modelNames]);
  useEffect(() => setPage(0), [tab, query, model, range, sort]);
  const rows = useMemo(() => {
    let result =
      tab === "requests"
        ? [...events].reverse()
        : groups(events, tab === "model" ? "model" : "key");
    result = result.filter((r) => matchesUsageRow(r, query));
    if (sort === "cost")
      result.sort((a, b) => {
        const aCost = tab === "requests" ? a.cost : a.priced ? a.cost : null;
        const bCost = tab === "requests" ? b.cost : b.priced ? b.cost : null;
        return (bCost ?? -1) - (aCost ?? -1);
      });
    else if (tab !== "requests")
      result.sort((a, b) =>
        sort === "requests"
          ? b.n - a.n
          : sort === "name"
            ? a.name.localeCompare(b.name)
            : b.input + b.output - (a.input + a.output),
      );
    return result;
  }, [events, tab, query, sort]);
  const maxPage = Math.max(0, Math.ceil(rows.length / 8) - 1),
    shown = rows.slice(
      Math.min(page, maxPage) * 8,
      (Math.min(page, maxPage) + 1) * 8,
    );
  const total = stats.input + stats.output,
    rate = stats.n
      ? (((stats.n - stats.fail) / stats.n) * 100).toFixed(1)
      : null;
  const healthy =
    data?.configured && data?.collected_at && !data?.collect_err && !error;
  const quick = (days) =>
    setRange({ from: subDays(new Date(), days - 1), to: new Date() });
  const todayKey = dateKey(new Date());
  const fromKey = dateKey(range.from);
  const toKey = dateKey(range.to);
  const spansToday = toKey === todayKey;
  const activeQuick =
    spansToday && fromKey === todayKey
      ? 1
      : spansToday && fromKey === dateKey(subDays(new Date(), 6))
        ? 7
        : spansToday && fromKey === dateKey(subDays(new Date(), 29))
          ? 30
          : 0;
  return (
    <TooltipProvider>
      <KeyNoteDialog row={noteTarget} onClose={() => setNoteTarget(null)} onSave={async (key, note) => {
        await request("/key-note", { method: "POST", body: JSON.stringify({ key, note }) });
        setData((current) => current ? { ...current, events: current.events.map((event) => event.key === key ? { ...event, key_note: note } : event) } : current);
      }} />
      <AuthDialog open={authOpen} onOpenChange={setAuthOpen} issue={authIssue} source={authSource}
        onConnect={async (key) => {
          const { persisted } = await authClient.connect(key);
          authRevision.current++;
          setAuthSource(credentialSource()); setAuthIssue(""); setAuthOpen(false);
          setAuthNotice(persisted ? "" : "浏览器未允许会话存储，管理密钥仅在当前页面有效，刷新后需重新输入。");
          await load();
        }}
        onClear={() => {
          authClient.clearManualKey(); authRevision.current++;
          setAuthSource(credentialSource()); setAuthIssue(""); setAuthNotice("");
          load();
        }} />
      <div className="min-h-dvh">
        <header className="border-b bg-card">
          <div className="mx-auto flex h-16 max-w-7xl items-center justify-between px-4 sm:px-8">
            <a
              href="/"
              className="flex items-center gap-3 font-semibold tracking-tight"
            >
              <span className="flex size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
                <Activity className="size-4" />
              </span>
              CPA Usage
              <Badge
                variant="outline"
                className="hidden sm:inline-flex font-normal text-muted-foreground"
              >
                LOCAL
              </Badge>
            </a>
            <div className="flex items-center gap-2">
              <Button variant="outline" size="sm" onClick={() => setAuthOpen(true)}>
                <KeyRound className="size-4" /><span className="hidden sm:inline">管理密钥</span><span className="sr-only sm:hidden">管理密钥</span>
              </Button>
              <PriceDialog request={request} onSaved={load} currency={currency} fxRate={fxRate} />
              <Badge
                variant="outline"
                className="hidden sm:flex gap-1.5 font-normal"
              >
                {healthy ? (
                  <Check className="size-3 text-primary" />
                ) : (
                  <WifiOff className="size-3" />
                )}
                {healthy ? "采集正常" : "等待连接"}
              </Badge>
              <Button
                variant="ghost"
                size="icon"
                aria-label={dark ? "切换浅色" : "切换深色"}
                onClick={() => setDark(!dark)}
              >
                {dark ? <Sun /> : <Moon />}
              </Button>
            </div>
          </div>
        </header>
        <main className="mx-auto max-w-7xl space-y-6 px-4 py-8 sm:px-8 sm:py-10">
          <div className="flex flex-wrap items-end justify-between gap-4">
            <div>
              <p className="mb-2 text-xs font-medium text-muted-foreground">
                工作台 / 用量分析
              </p>
              <h1 className="text-3xl font-semibold tracking-tight">
                本地优先，轻量运行。
              </h1>
              <p className="mt-2 text-sm text-muted-foreground">
                数据留在本机，一个轻量服务看清请求、Token 与模型用量。
              </p>
            </div>
            <Button
              variant="outline"
              onClick={() => load()}
              disabled={busy}
            >
              <RefreshCw className={busy ? "animate-spin" : ""} />
              {busy ? "刷新中" : "刷新"}
            </Button>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <SegmentedControl
              ariaLabel="时间范围"
              value={activeQuick}
              onChange={quick}
              items={[
                { label: "今天", value: 1 },
                { label: "近 7 天", value: 7 },
                { label: "近 30 天", value: 30 },
              ]}
            />
            <Popover
              open={calendarOpen}
              onOpenChange={(o) => {
                setCalendarOpen(o);
                if (o) setDraft(range);
              }}
            >
              <PopoverTrigger asChild>
                <Button variant="outline" size="sm">
                  <CalendarDays />
                  {format(range.from, "yyyy/MM/dd")} –{" "}
                  {format(range.to, "MM/dd")}
                </Button>
              </PopoverTrigger>
              <PopoverContent align="start" className="w-auto p-0">
                <Calendar
                  mode="range"
                  locale={zhCN}
                  selected={draft}
                  onSelect={(v) => setDraft(v || {})}
                  defaultMonth={range.from}
                  numberOfMonths={1}
                  disabled={{ after: new Date() }}
                />
                <Separator />
                <div className="flex items-center justify-between gap-4 p-3">
                  <span className="text-xs text-muted-foreground">
                    选择开始和结束日期
                  </span>
                  <Button
                    size="sm"
                    disabled={!draft.from || !draft.to || draft.from > draft.to}
                    onClick={() => {
                      setRange(draft);
                      setCalendarOpen(false);
                    }}
                  >
                    应用
                  </Button>
                </div>
              </PopoverContent>
            </Popover>
            <Select value={model} onValueChange={setModel}>
              <SelectTrigger
                className="w-44 bg-card"
                size="sm"
                aria-label="筛选模型"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部模型</SelectItem>
                {modelNames.map((m) => (
                  <SelectItem key={m} value={m}>
                    {m}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <CurrencyControl value={currencySettings} onChange={setCurrencySettings} />
            <span className="ml-auto text-xs text-muted-foreground">
              {data?.collected_at
                ? "更新于 " + format(new Date(data.collected_at), "HH:mm:ss")
                : "每 60 秒自动刷新"}
            </span>
          </div>
          {authNotice && <p role="status" className="text-sm text-muted-foreground">{authNotice}</p>}
          {error && (
            <Alert variant="destructive">
              <WifiOff />
              <AlertTitle>暂时无法获取用量</AlertTitle>
              <AlertDescription>
                {error}。
                {data
                  ? "下方保留上次结果，请重试。"
                  : "请确认 CLIProxyAPI 与插件运行正常。"}
              </AlertDescription>
            </Alert>
          )}
          {data?.configured && data.collect_err && (
            <Alert variant="destructive">
              <WifiOff />
              <AlertTitle>采集需要关注</AlertTitle>
              <AlertDescription>
                {data.collect_err}。历史记录仍可查看。
              </AlertDescription>
            </Alert>
          )}
          <section
            aria-label="核心指标"
            className="animate-in fade-in slide-in-from-bottom-2 duration-500 grid grid-cols-2 gap-3 lg:grid-cols-5"
          >
            <Metric
              className="col-span-2 lg:col-span-1"
              label={stats.priced && stats.unpriced ? "预估费用 · 部分费用" : "预估费用"}
              value={!stats.n ? "暂无" : stats.priced ? displayMoney(stats.cost) : "未定价"}
              raw={stats.priced ? stats.cost : undefined}
              format={(n) => money(n, currency, fxRate, convertUSD(n, currency, fxRate) >= 100 ? 2 : 6)}
              detail={!stats.n ? `跟随日期与模型筛选 · ${currency}` : `${currency} · ${fmt(stats.priced)} / ${fmt(stats.n)} 请求已计价${stats.unpriced ? ` · ${fmt(stats.unpriced)} 未定价` : ""}${stats.historical ? ` · ${fmt(stats.historical)} 历史估算` : ""}`}
              help={`按请求对应的价格版本估算，普通输入、缓存读取、缓存写入、输出分别计价；推理不重复累加。历史缺价采用首个可用价格。${currency === "CNY" ? `按 1 USD = ${fxRate} CNY 换算。` : ""}预估费用不代表实际账单。`}
              loading={loading}
            />
            <Metric
              label="总 Token"
              value={compact(total)}
              raw={total}
              format={compact}
              detail={`输入 ${compact(stats.input)} / 输出 ${compact(stats.output)}`}
              help={`精确值 ${fmt(total)}。输入包含缓存，输出包含推理，不重复累加。`}
              loading={loading}
            />
            <Metric
              label="请求次数"
              value={fmt(stats.n)}
              raw={stats.n}
              format={(v) => fmt(Math.round(v))}
              detail={`${fmt(groups(events, "model").length)} 个模型参与调用`}
              help="包含成功、失败和零 Token 请求。"
              loading={loading}
            />
            <Metric
              label="请求成功率"
              value={rate === null ? "暂无" : rate + "%"}
              raw={rate ?? undefined}
              format={(v) => v.toFixed(1) + "%"}
              detail={`${fmt(stats.fail)} 次失败 / ${fmt(stats.n - stats.fail)} 次成功`}
              help="按 CPA 返回的 failed 标记统计，不推断错误原因。"
              loading={loading}
            />
            <Metric
              label="缓存命中率"
              value={
                stats.input
                  ? ((stats.cache / stats.input) * 100).toFixed(1) + "%"
                  : "暂无"
              }
              raw={stats.input ? (stats.cache / stats.input) * 100 : undefined}
              format={(v) => v.toFixed(1) + "%"}
              detail={`复用 ${compact(stats.cache)} 输入 Token`}
              help="缓存读取 Token / 输入 Token。缓存是输入的一部分。"
              loading={loading}
            />
          </section>
          <Card className="animate-in fade-in slide-in-from-bottom-2 duration-500 [animation-delay:80ms] shadow-none">
            <CardHeader className="flex flex-wrap flex-row items-start justify-between gap-4">
              <div>
                <CardTitle className="text-base">{chartMetric === "cost" ? "费用趋势" : "Token 趋势"}</CardTitle>
                <CardDescription className="mt-1.5">
                  {chartMetric === "cost" ? `预估费用（${currency}）${stats.unpriced ? ` · ${fmt(stats.unpriced)} 次请求未定价，金额仅包含已计价部分` : ""}${stats.historical ? ` · ${fmt(stats.historical)} 次历史估算` : ""}` : "缓存输入、净输入和输出的堆叠分布"}
                </CardDescription>
              </div>
              <div className="flex flex-wrap gap-2">
              <SegmentedControl
                ariaLabel="趋势指标"
                value={chartMetric}
                onChange={setChartMetric}
                items={[{ label: "Token", value: "tokens" }, { label: "费用", value: "cost" }]}
              />
              <SegmentedControl
                ariaLabel="聚合粒度"
                value={bucket}
                onChange={setBucket}
                items={[
                  { label: "小时", value: "hour" },
                  { label: "天", value: "day" },
                  { label: "月", value: "month" },
                ]}
              />
              </div>
            </CardHeader>
            <CardContent>
              {loading ? (
                <Skeleton className="h-72 w-full" />
              ) : buckets.length && (chartMetric !== "cost" || stats.priced) ? (
                <>
                  <ChartContainer
                    className="h-[280px] w-full sm:h-[330px]"
                    config={chartMetric === "cost" ? costChartConfig : chartConfig}
                  >
                    <AreaChart
                      accessibilityLayer
                      data={chartMetric === "cost" ? costBuckets : buckets}
                      margin={{ top: 12, left: 0, right: 12, bottom: 0 }}
                    >
                      <CartesianGrid vertical={false} strokeDasharray="3 4" />
                      <XAxis
                        dataKey="label"
                        axisLine={false}
                        tickLine={false}
                        minTickGap={35}
                        tickMargin={12}
                        tickFormatter={(v) =>
                          bucket === "hour"
                            ? v.slice(5)
                            : bucket === "day"
                              ? v.slice(5)
                              : v
                        }
                      />
                      <YAxis
                        tickFormatter={chartMetric === "cost" ? (n) => `${currency === "CNY" ? "¥" : "$"}${compact(n)}` : compact}
                        axisLine={false}
                        tickLine={false}
                        width={chartMetric === "cost" ? 64 : 48}
                      />
                      <ChartTooltip
                        content={chartMetric === "cost" ? <ChartTooltipContent formatter={(value, name, item) => (
                          <div className="space-y-1"><p className="font-medium tabular-nums">预估费用 {money(value, currency, 1)}</p>{item.payload.unpriced > 0 && <p className="text-muted-foreground">部分费用 · {fmt(item.payload.unpriced)} 次请求未定价</p>}</div>
                        )} /> : <ChartTooltipContent valueFormatter={fmt} />}
                      />
                      {(chartMetric === "cost" ? ["cost"] : ["cache", "net", "output"]).map((k) => (
                        <Area
                          key={k}
                          dataKey={k}
                          type="linear"
                          connectNulls={false}
                          stackId="total"
                          stroke={`var(--color-${k})`}
                          fill={`var(--color-${k})`}
                          fillOpacity={k === "output" ? 0.8 : 0.6}
                          animationDuration={450}
                          animationEasing="ease-out"
                        />
                      ))}
                    </AreaChart>
                  </ChartContainer>
                  <div className="mt-4 flex flex-wrap justify-center gap-6 text-xs text-muted-foreground">
                    {Object.entries(chartMetric === "cost" ? costChartConfig : chartConfig).map(([k, v]) => (
                      <span key={k} className="flex items-center gap-2">
                        <span
                          className="size-2 rounded-sm"
                          style={{ background: v.color }}
                        />
                        {v.label}
                      </span>
                    ))}
                  </div>
                </>
              ) : (
                <div className="flex h-72 flex-col items-center justify-center gap-3 text-muted-foreground">
                  <Activity className="size-8 opacity-40" />
                  <p className="text-sm">{stats.n && chartMetric === "cost" ? "当前请求未定价，请在价格表中设置单价。" : "这个时间范围内，还没有用量。"}</p>
                  <Button variant="outline" size="sm" onClick={() => quick(30)}>
                    查看近 30 天
                  </Button>
                </div>
              )}
            </CardContent>
          </Card>
          <Card className="animate-in fade-in slide-in-from-bottom-2 duration-500 [animation-delay:160ms] gap-0 overflow-hidden shadow-none">
            <CardHeader className="pb-5">
              <div className="flex flex-wrap items-center justify-between gap-4">
                <div>
                  <CardTitle className="text-base">用量明细</CardTitle>
                  <CardDescription className="mt-1.5">
                    {tab === "key" ? "按客户端 Key 汇总，升级前历史来源单独列出" : "从模型到每次请求，逐层查看"}
                  </CardDescription>
                </div>
                <div className="relative w-full sm:w-60">
                  <Search className="absolute left-3 top-2.5 size-4 text-muted-foreground" />
                  <Input
                    className="pl-9"
                    placeholder="搜索模型、Key 或备注"
                    aria-label="搜索明细"
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                  />
                </div>
              </div>
            </CardHeader>
            <div>
              <div className="flex flex-wrap items-center justify-between gap-3 px-6 pb-4">
                <SegmentedControl
                  ariaLabel="明细视图"
                  value={tab}
                  onChange={(value) => { setTab(value); setSort(value === "requests" ? "time" : "tokens"); }}
                  items={[
                    {
                      label: (
                        <>
                          <Layers className="size-3.5" />
                          按模型
                        </>
                      ),
                      value: "model",
                    },
                    {
                      label: (
                        <>
                          <KeyRound className="size-3.5" />
                          按 Key
                        </>
                      ),
                      value: "key",
                    },
                    {
                      label: (
                        <>
                          <Activity className="size-3.5" />
                          请求
                        </>
                      ),
                      value: "requests",
                    },
                  ]}
                />
                  <Select value={tab === "requests" && sort !== "cost" ? "time" : sort} onValueChange={setSort}>
                    <SelectTrigger
                      className="w-36"
                      size="sm"
                      aria-label="明细排序"
                    >
                      <ArrowDownUp className="size-3" />
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="cost">费用降序</SelectItem>
                      {tab === "requests" ? <SelectItem value="time">时间倒序</SelectItem> : <>
                        <SelectItem value="tokens">Token 降序</SelectItem>
                        <SelectItem value="requests">请求数降序</SelectItem>
                        <SelectItem value="name">名称排序</SelectItem>
                      </>}
                    </SelectContent>
                  </Select>
              </div>
              {tab === "key" && legacyRequests > 0 && <p className="px-6 pb-4 text-xs leading-relaxed text-muted-foreground">
                当前范围内有 {fmt(legacyRequests)} 条历史记录无法确定客户端，仅按历史来源列出，其用量仍计入总览。
              </p>}
              <Separator />
              <Table>
                <TableHeader>
                  <TableRow>
                    {(tab === "requests"
                      ? ["时间 / 模型", "客户端 Key", "状态", "输入", "缓存", "输出", "预估费用"]
                      : [
                          tab === "key" ? "客户端 Key / 历史来源" : "模型",
                          "请求",
                          "成功率",
                          "输入",
                          "缓存",
                          "输出",
                          "总 Token",
                          "预估费用",
                        ]
                    ).map((s, i) => (
                      <TableHead key={s} className={i ? "text-right" : "pl-6"}>
                        {s}
                      </TableHead>
                    ))}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {shown.map((r, i) => (
                    <TableRow
                      key={tab === "requests" ? `${r.ts}-${i}` : r.name}
                      className="animate-in fade-in duration-500"
                      style={{ animationDelay: `${Math.min(i, 12) * 40}ms` }}
                    >
                      {tab === "requests" ? (
                        <>
                          <TableCell className="pl-6">
                            <div className="text-xs text-muted-foreground">
                              {format(new Date(r.ts * 1000), "MM-dd HH:mm:ss")}
                            </div>
                            <div
                              className="max-w-56 truncate font-medium"
                              title={r.model}
                            >
                              {r.model}
                            </div>
                          </TableCell>
                          <TableCell className="max-w-64 text-right text-xs" title={`${keyLabel(r)}\n${r.key}`}>
                            <div className="font-mono">{keyLabel(r)}</div>
                            <p className="mt-1 text-[10px] text-muted-foreground">{keyDescription(r)}</p>
                          </TableCell>
                          <TableCell className="text-right">
                            <Badge
                              variant={r.failed ? "destructive" : "secondary"}
                            >
                              {r.failed ? "失败" : "成功"}
                            </Badge>
                          </TableCell>
                        </>
                      ) : (
                        <>
                          <TableCell
                            className="max-w-64 truncate pl-6 font-medium"
                            title={tab === "key" ? `${keyLabel(r)}\n${r.name}` : r.name}
                          >
                            <div className="flex items-center gap-2">
                              <span className="truncate">{tab === "key" ? keyLabel(r) : r.name}</span>
                              {tab === "key" && <Button variant="ghost" size="icon" className="size-6 shrink-0" aria-label={`编辑备注 ${r.key_label || r.name}`} onClick={() => setNoteTarget(r)}><Pencil className="size-3" /></Button>}
                            </div>
                            {tab === "key" && <p className="mt-1 text-[10px] font-normal text-muted-foreground">{keyDescription(r)}</p>}
                          </TableCell>
                          <TableCell className="text-right tabular-nums">
                            {fmt(r.n)}
                          </TableCell>
                          <TableCell className="text-right tabular-nums">
                            {(((r.n - r.fail) / r.n) * 100).toFixed(1)}%
                          </TableCell>
                        </>
                      )}
                      {["input", "cache", "output"].map((k) => (
                        <TableCell
                          key={k}
                          className="text-right tabular-nums"
                          title={fmt(r[k])}
                        >
                          {compact(r[k])}
                        </TableCell>
                      ))}
                      {tab !== "requests" && (
                        <TableCell
                          className="text-right font-medium tabular-nums"
                          title={fmt(r.input + r.output)}
                        >
                          {compact(r.input + r.output)}
                        </TableCell>
                      )}
                      <TableCell className="pr-6 text-right tabular-nums whitespace-nowrap" title={r.price_version ? `价格版本 ${r.price_version}` : undefined}>
                        <div className="font-medium">{tab === "requests" ? displayMoney(r.cost) : r.priced ? displayMoney(r.cost) : "未定价"}</div>
                        {r.priced > 0 && r.unpriced > 0 && <p className="mt-1 text-[10px] text-muted-foreground">部分费用 · {fmt(r.unpriced)} 未定价</p>}
                        {(r.historical > 0 || r.price_basis === "historical_estimate") && <p className="mt-1 text-[10px] text-muted-foreground">历史估算</p>}
                      </TableCell>
                    </TableRow>
                  ))}
                  {!shown.length && (
                    <TableRow>
                      <TableCell
                        colSpan={tab === "requests" ? 7 : 8}
                        className="h-32 text-center text-muted-foreground"
                      >
                        {loading
                          ? "正在加载明细…"
                          : query
                            ? "没有匹配结果，换个关键词试试。"
                            : "暂无明细"}
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </div>
            <Separator />
            <div className="flex items-center justify-between px-6 py-3 text-xs text-muted-foreground">
              <span>{fmt(rows.length)} 条记录 · 客户端 Key 已匿名化</span>
              <div className="flex items-center gap-2">
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-7"
                  aria-label="上一页"
                  disabled={page === 0}
                  onClick={() => setPage((p) => p - 1)}
                >
                  <ChevronLeft />
                </Button>
                <span>
                  {Math.min(page, maxPage) + 1} / {maxPage + 1}
                </span>
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-7"
                  aria-label="下一页"
                  disabled={page >= maxPage}
                  onClick={() => setPage((p) => p + 1)}
                >
                  <ChevronRight />
                </Button>
              </div>
            </div>
          </Card>
          <footer className="flex flex-wrap items-center justify-between gap-2 pb-3 text-xs text-muted-foreground">
            <span className="flex items-center gap-1.5">
              <Database className="size-3" />
              本地 SQLite · 统计时区：{data?.timezone || "浏览器时区"} · v
              {data?.version || "0.1.1"}
            </span>
            <span>
              输入包含缓存，输出包含推理
              {data?.skipped ? ` · ${data.skipped} 条无效记录未统计` : ""}
            </span>
          </footer>
        </main>
      </div>
    </TooltipProvider>
  );
}
createRoot(document.getElementById("root")).render(<App />);
