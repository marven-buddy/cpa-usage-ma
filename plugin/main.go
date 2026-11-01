package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
	stored_host = host;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) {
		return 1;
	}
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
		stored_host->free_buffer(ptr, len);
	}
}
*/
import "C"

import (
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unsafe"

	_ "modernc.org/sqlite"
)

//go:embed web/index.html web/assets/*
var webFS embed.FS

const abiVersion uint32 = 1

// version is injected at release time via -ldflags "-X main.version=..."
var version = "0.4.1-dev"

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

var (
	mu       sync.Mutex
	db       *sql.DB
	lastRecv time.Time
	lastErr  string
)

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	C.store_host_api(host)
	plugin.abi_version = C.uint32_t(abiVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}
	var req []byte
	if request != nil && requestLen > 0 {
		req = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, err := handleMethod(C.GoString(method), req)
	if err != nil {
		writeResponse(response, errorEnvelope("plugin_error", err.Error()))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, len C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
	_ = len
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	mu.Lock()
	defer mu.Unlock()
	if db != nil {
		_ = db.Close()
		db = nil
	}
}

func handleMethod(method string, req []byte) ([]byte, error) {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		if err := applyConfig(req); err != nil {
			return nil, err
		}
		return okEnvelopeJSON(`{"schema_version":6,"metadata":{"Name":"cpa-usage-ma","Version":"` + version + `","Author":"marven-buddy","GitHubRepository":"https://github.com/marven-buddy/cpa-usage-ma","Logo":"","ConfigFields":[]},"capabilities":{"usage_plugin":true,"management_api":true}}`)
	case "management.register":
		return okEnvelopeJSON(`{"routes":[
			{"Method":"GET","Path":"/plugins/cpa-usage-ma/api/usage","Menu":"","Description":"usage data api"},
			{"Method":"GET","Path":"/plugins/cpa-usage-ma/api/health","Menu":"","Description":"health"},
			{"Method":"POST","Path":"/plugins/cpa-usage-ma/api/collect","Menu":"","Description":"collect trigger"},
			{"Method":"POST","Path":"/plugins/cpa-usage-ma/api/key-note","Menu":"","Description":"set or clear Key note"},
			{"Method":"GET","Path":"/plugins/cpa-usage-ma/api/pricing","Menu":"","Description":"model prices"},
			{"Method":"POST","Path":"/plugins/cpa-usage-ma/api/pricing","Menu":"","Description":"custom model price"},
			{"Method":"POST","Path":"/plugins/cpa-usage-ma/api/pricing/refresh","Menu":"","Description":"refresh public prices"}
		],"resources":[
			{"Path":"/report","Menu":"cpa-usage-ma","Description":"CLIProxyAPI v8 usage dashboard"},
			{"Path":"/assets/index-Cp--UGfL.js","Menu":"","Description":"dashboard js"},
			{"Path":"/assets/index-BhRVZS53.css","Menu":"","Description":"dashboard css"}
		]}`)
	case "usage.handle":
		handleUsage(req)
		return okEnvelopeJSON(`{}`)
	case "management.handle":
		return handleManagement(req)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

// ---- config ----

func applyConfig(req []byte) error {
	var parsed struct {
		ConfigYaml json.RawMessage `json:"config_yaml"`
	}
	if len(req) == 0 {
		return nil
	}
	if err := json.Unmarshal(req, &parsed); err != nil {
		return fmt.Errorf("decode plugin configuration: %w", err)
	}
	if len(parsed.ConfigYaml) == 0 {
		// Some host versions wrap the payload in a {"result": {...}} envelope.
		var wrapped struct {
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(req, &wrapped); err == nil && wrapped.Result != nil {
			_ = json.Unmarshal(wrapped.Result, &parsed)
		}
	}
	if len(parsed.ConfigYaml) == 0 || string(parsed.ConfigYaml) == "null" {
		return nil
	}
	// Current hosts encode []byte as Base64; retain legacy plaintext support.
	var configBytes []byte
	if err := json.Unmarshal(parsed.ConfigYaml, &configBytes); err != nil {
		var plaintext string
		if err := json.Unmarshal(parsed.ConfigYaml, &plaintext); err != nil {
			return fmt.Errorf("decode config_yaml: %w", err)
		}
		configBytes = []byte(plaintext)
	}
	path := ""
	for _, line := range strings.Split(string(configBytes), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "db_path:") {
			path = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "db_path:")), `"'`)
			break
		}
	}
	if path == "" {
		return nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		if u, err := user.Current(); err == nil {
			path = filepath.Join(u.HomeDir, strings.TrimLeft(path[1:], "/\\"))
		} else {
			return fmt.Errorf("resolve database home directory: %w", err)
		}
	}
	return openDB(path)
}

func defaultDBPath() string {
	u, err := user.Current()
	if err != nil {
		return filepath.Join("cpa-usage-ma", "usage.sqlite")
	}
	return filepath.Join(u.HomeDir, ".cli-proxy-api", "cpa-usage-ma", "usage.sqlite")
}

func openDB(path string) error {
	mu.Lock()
	defer mu.Unlock()
	return openDBLocked(path)
}

func openDBLocked(path string) error {
	if db != nil && path != "" && lastOpened == path {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		lastErr = "sqlite directory: " + err.Error()
		return fmt.Errorf("%s", lastErr)
	}
	conn, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		lastErr = "sqlite open: " + err.Error()
		return fmt.Errorf("%s", lastErr)
	}
	_, err = conn.Exec("CREATE TABLE IF NOT EXISTS usage_events (id INTEGER PRIMARY KEY AUTOINCREMENT, ts TEXT NOT NULL, dedup TEXT UNIQUE, payload TEXT)")
	if err == nil {
		_, err = conn.Exec("CREATE TABLE IF NOT EXISTS key_notes (key_id TEXT PRIMARY KEY, note TEXT NOT NULL)")
	}
	if err == nil {
		err = initPricing(conn)
	}
	if err != nil {
		lastErr = "sqlite migrate: " + err.Error()
		_ = conn.Close()
		return fmt.Errorf("%s", lastErr)
	}
	if db != nil {
		_ = db.Close()
	}
	db = conn
	lastOpened = path
	lastErr = ""
	return nil
}

var lastOpened string

func ensureDB() {
	mu.Lock()
	defer mu.Unlock()
	if db == nil {
		_ = openDBLocked(defaultDBPath())
	}
}

// ---- usage ----

func handleUsage(req []byte) {
	var parsed struct {
		Result json.RawMessage `json:"result"`
	}
	if len(req) > 0 {
		_ = json.Unmarshal(req, &parsed)
	}
	rec := parsed.Result
	if rec == nil {
		rec = req
	}
	var u usageRecord
	if err := json.Unmarshal(rec, &u); err != nil {
		return
	}
	lastRecv = time.Now()

	model := u.Model
	if model == "" {
		model = u.Alias
	}
	if model == "" {
		model = "unknown"
	}
	ts := u.RequestedAt
	if ts.IsZero() {
		ts = time.Now()
	}
	tokens := map[string]int64{
		"input_tokens":          u.Detail.InputTokens,
		"output_tokens":         u.Detail.OutputTokens,
		"reasoning_tokens":      u.Detail.ReasoningTokens,
		"cached_tokens":         u.Detail.CachedTokens,
		"cache_read_tokens":     u.Detail.CacheReadTokens,
		"cache_creation_tokens": u.Detail.CacheCreationTokens,
		"total_tokens":          u.Detail.TotalTokens,
	}
	clientID := clientIdentity(u.APIKey, u.AccessProvider)
	upstreamID := upstreamIdentity(u)
	event := map[string]interface{}{
		"event_version":   2,
		"timestamp":       ts.Format(time.RFC3339Nano),
		"model":           model,
		"client_id":       clientID,
		"access_provider": strings.TrimSpace(u.AccessProvider),
		"upstream_id":     upstreamID,
		"provider":        u.Provider,
		"executor_type":   u.ExecutorType,
		"request_id":      u.RequestID,
		"selection_id":    u.SelectionID,
		"failed":          u.Failed,
		"latency_ms":      float64(u.Latency) / float64(time.Millisecond),
		"ttft_ms":         float64(u.TTFT) / float64(time.Millisecond),
		"tokens":          tokens,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	// Versioned identities keep clients, providers, and side-model events distinct.
	// Existing rows retain their original dedup values; no historical rewrite is needed.
	digest := sha256.Sum256(payload)

	ensureDB()
	mu.Lock()
	defer mu.Unlock()
	if db == nil {
		return
	}
	_, _ = db.Exec("INSERT OR IGNORE INTO usage_events (ts, dedup, payload) VALUES (?,?,?)",
		ts.Format("2006-01-02 15:04:05"), "v2:"+hex.EncodeToString(digest[:]), string(payload))
}

type usageRecord struct {
	RequestID       string
	SelectionID     string
	Provider        string
	ExecutorType    string
	Model           string
	Alias           string
	APIKey          string
	AccessProvider  string
	AuthID          string
	Source          string
	ReasoningEffort string
	RequestedAt     time.Time
	Latency         time.Duration
	TTFT            time.Duration
	Failed          bool
	Detail          struct {
		InputTokens         int64
		OutputTokens        int64
		ReasoningTokens     int64
		CachedTokens        int64
		CacheReadTokens     int64
		CacheCreationTokens int64
		TotalTokens         int64
	}
}

// ---- management ----

func handleManagement(req []byte) ([]byte, error) {
	var parsed struct {
		Result json.RawMessage `json:"result"`
	}
	if len(req) > 0 {
		_ = json.Unmarshal(req, &parsed)
	}
	rec := parsed.Result
	if rec == nil {
		rec = req
	}
	var m struct {
		Method string
		Path   string
		Query  map[string][]string
		Body   []byte
	}
	if err := json.Unmarshal(rec, &m); err != nil {
		return errorEnvelope("bad_request", err.Error()), nil
	}

	if strings.HasSuffix(m.Path, "/report") || m.Path == "" {
		html, err := webFS.ReadFile("web/index.html")
		if err != nil {
			return errorEnvelope("asset_missing", err.Error()), nil
		}
		return managementResponse(200, "text/html; charset=utf-8", html)
	}
	if strings.HasSuffix(m.Path, "/assets/index-Cp--UGfL.js") {
		js, err := webFS.ReadFile("web/assets/index-Cp--UGfL.js")
		if err != nil {
			return errorEnvelope("asset_missing", err.Error()), nil
		}
		return managementResponse(200, "application/javascript; charset=utf-8", js)
	}
	if strings.HasSuffix(m.Path, "/assets/index-BhRVZS53.css") {
		css, err := webFS.ReadFile("web/assets/index-BhRVZS53.css")
		if err != nil {
			return errorEnvelope("asset_missing", err.Error()), nil
		}
		return managementResponse(200, "text/css; charset=utf-8", css)
	}
	if strings.HasSuffix(m.Path, "/api/health") {
		body, _ := json.Marshal(map[string]interface{}{"service": "cpa-usage-ma", "version": version, "pid": 0})
		return managementResponse(200, "application/json; charset=utf-8", body)
	}
	if strings.HasSuffix(m.Path, "/api/collect") {
		collectOnce()
		body, _ := json.Marshal(map[string]interface{}{"collected_at": collectedAt(), "collect_err": collectErr()})
		return managementResponse(200, "application/json; charset=utf-8", body)
	}
	if strings.HasSuffix(m.Path, "/api/usage") {
		body, err := usageResponse(m.Query)
		if err != nil {
			b, _ := json.Marshal(map[string]interface{}{"error": err.Error()})
			return managementResponse(400, "application/json; charset=utf-8", b)
		}
		return managementResponse(200, "application/json; charset=utf-8", body)
	}
	if strings.HasSuffix(m.Path, "/api/key-note") {
		if m.Method != "POST" {
			return managementResponse(405, "application/json", []byte(`{"error":"请求方法不支持"}`))
		}
		if err := setKeyNote(m.Body); err != nil {
			body, _ := json.Marshal(map[string]string{"error": err.Error()})
			return managementResponse(400, "application/json; charset=utf-8", body)
		}
		return managementResponse(200, "application/json", []byte(`{"ok":true}`))
	}
	if strings.HasSuffix(m.Path, "/api/pricing") || strings.HasSuffix(m.Path, "/api/pricing/refresh") {
		ensureDB()
		var err error
		status := 400
		switch {
		case strings.HasSuffix(m.Path, "/pricing/refresh") && m.Method == "POST":
			err = refreshPrices()
			status = 502
		case strings.HasSuffix(m.Path, "/api/pricing") && m.Method == "POST":
			err = setCustomPrice(m.Body)
		case strings.HasSuffix(m.Path, "/api/pricing") && m.Method == "GET":
			maybeRefreshPrices()
		default:
			return managementResponse(405, "application/json", []byte(`{"error":"请求方法不支持"}`))
		}
		if err != nil {
			body, _ := json.Marshal(map[string]string{"error": err.Error()})
			return managementResponse(status, "application/json", body)
		}
		body, err := pricingResponse()
		if err != nil {
			return nil, err
		}
		return managementResponse(200, "application/json; charset=utf-8", body)
	}
	return managementResponse(404, "text/plain; charset=utf-8", []byte("not found"))
}

func managementResponse(status int, contentType string, body []byte) ([]byte, error) {
	obj, err := json.Marshal(map[string]interface{}{
		"StatusCode": status,
		"Headers":    map[string][]string{"content-type": {contentType}},
		"Body":       body,
	})
	if err != nil {
		return errorEnvelope("encode_error", err.Error()), nil
	}
	return json.Marshal(envelope{OK: true, Result: obj})
}

// ---- usage api (compatible with cpa-usage frontend contract) ----

type dbEvent struct {
	EventVersion   int            `json:"event_version"`
	ClientID       string         `json:"client_id"`
	AccessProvider string         `json:"access_provider"`
	UpstreamID     string         `json:"upstream_id"`
	Timestamp      string         `json:"timestamp"`
	Model          string         `json:"model"`
	Provider       string         `json:"provider"`
	ExecutorType   string         `json:"executor_type"`
	Alias          string         `json:"alias"`
	Source         string         `json:"source"`
	APIKey         string         `json:"api_key"`
	Failed         bool           `json:"failed"`
	LatencyMs      float64        `json:"latency_ms"`
	TTFTMs         float64        `json:"ttft_ms"`
	Tokens         map[string]any `json:"tokens"`
}

type outEvent struct {
	costQuote
	Ts             float64 `json:"ts"`
	Model          string  `json:"model"`
	Provider       string  `json:"provider"`
	Key            string  `json:"key"`
	KeyLabel       string  `json:"key_label"`
	KeyNote        string  `json:"key_note,omitempty"`
	KeyKind        string  `json:"key_kind"`
	AccessProvider string  `json:"access_provider,omitempty"`
	UpstreamID     string  `json:"upstream_id,omitempty"`
	Input          int64   `json:"input"`
	Output         int64   `json:"output"`
	Cache          int64   `json:"cache"`
	CacheCreation  int64   `json:"cache_creation"`
	Reasoning      int64   `json:"reasoning"`
	Failed         bool    `json:"failed"`
	LatencyMs      float64 `json:"latency_ms"`
	TTFTMs         float64 `json:"ttft_ms"`
}

type outBucket struct {
	Label    string   `json:"label"`
	Input    int64    `json:"input"`
	Output   int64    `json:"output"`
	Cache    int64    `json:"cache"`
	Requests int64    `json:"requests"`
	Cost     *float64 `json:"cost"`
	Unpriced int64    `json:"unpriced"`
}

func anonKey(source string) string {
	if source == "" {
		return "未知 Key"
	}
	sum := sha256.Sum256([]byte(source))
	return "key-" + hex.EncodeToString(sum[:])[:10]
}

func identityHash(namespace string, values ...string) string {
	// JSON framing avoids ambiguity when opaque values contain separators.
	raw, _ := json.Marshal(append([]string{namespace}, values...))
	sum := sha256.Sum256(raw)
	return namespace + "-" + hex.EncodeToString(sum[:])
}

func clientIdentity(apiKey, accessProvider string) string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return ""
	}
	return identityHash("client", strings.TrimSpace(accessProvider), apiKey)
}

func upstreamIdentity(u usageRecord) string {
	provider := strings.TrimSpace(u.Provider)
	if authID := strings.TrimSpace(u.AuthID); authID != "" {
		return identityHash("upstream-auth", provider, authID)
	}
	source := strings.TrimSpace(u.Source)
	// The host can fall back to the client key when no upstream source exists.
	if source == "" || source == strings.TrimSpace(u.APIKey) {
		return ""
	}
	return identityHash("upstream-source", provider, source)
}

func eventKey(e dbEvent) (id, label, kind string) {
	if e.EventVersion >= 2 {
		if e.ClientID == "" {
			return "client:unknown", "未知客户端", "unknown"
		}
		// Use the full identity for grouping; shortening is only for display.
		digest := strings.TrimPrefix(e.ClientID, "client-")
		if len(digest) > 10 {
			digest = digest[:10]
		}
		return e.ClientID, "key-" + digest, "client"
	}
	// Old sources may be upstream accounts or client credentials. Do not guess.
	source := e.Source
	if source == "" {
		source = e.APIKey
	}
	if source == "" {
		return "legacy:unknown", "历史来源未知", "legacy_source"
	}
	return identityHash("legacy", source), "历史来源 · " + anonKey(source), "legacy_source"
}

func usageResponse(q map[string][]string) ([]byte, error) {
	get := func(k string) string {
		if v, ok := q[k]; ok && len(v) > 0 {
			return v[0]
		}
		return ""
	}
	bucket := get("bucket")
	if bucket == "" {
		bucket = "day"
	}
	if bucket != "hour" && bucket != "day" && bucket != "month" {
		return nil, fmt.Errorf("无效时间粒度")
	}
	window, rangeErr := parseUsageTimeRange(q)
	if rangeErr != nil {
		return nil, rangeErr
	}

	events := []outEvent{}
	bins := map[string]*outBucket{}
	order := []string{}
	skipped := 0

	ensureDB()
	maybeRefreshPrices()
	mu.Lock()
	var rows *sql.Rows
	var err error
	var prices *priceBook
	var keyNotes map[string]string
	if db != nil {
		prices, err = loadPriceBook(db)
		if err == nil {
			keyNotes, err = loadKeyNotes(db)
		}
		if err == nil {
			rows, err = db.Query("SELECT ts, payload FROM usage_events ORDER BY id")
		}
	}
	mu.Unlock()
	if err != nil {
		return nil, err
	}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var storedTs, payload string
			if err := rows.Scan(&storedTs, &payload); err != nil {
				continue
			}
			var e dbEvent
			if err := json.Unmarshal([]byte(payload), &e); err != nil {
				skipped++
				continue
			}
			tsStr := e.Timestamp
			if tsStr == "" {
				tsStr = storedTs
			}
			eventTime, err := parseAnyTime(tsStr)
			if err != nil {
				skipped++
				continue
			}
			if eventTime.Before(window.start) || !eventTime.Before(window.end) {
				continue
			}
			ts := float64(eventTime.UnixNano()) / 1e9
			model := e.Model
			if model == "" {
				model = e.Alias
			}
			if model == "" {
				model = "unknown"
			}
			key, keyLabel, keyKind := eventKey(e)
			var input, output, cache, creation, reasoning int64
			if t := e.Tokens; t != nil {
				input = toI64(t["input_tokens"])
				output = toI64(t["output_tokens"])
				read := toI64(t["cache_read_tokens"])
				cached := toI64(t["cached_tokens"])
				creation = toI64(t["cache_creation_tokens"])
				cache = read
				if cache == 0 && cached > 0 && creation == 0 {
					cache = cached
				}
				reasoning = toI64(t["reasoning_tokens"])
				if addInputCache(e, input, output, cache, creation, reasoning) {
					input += cache + creation
				}
			}
			event := outEvent{
				Ts: ts, Model: model, Key: key, KeyLabel: keyLabel, KeyKind: keyKind,
				KeyNote:        keyNotes[key],
				AccessProvider: e.AccessProvider, UpstreamID: e.UpstreamID,
				Input: input, Output: output, Cache: cache, Reasoning: reasoning,
				Provider: e.Provider, CacheCreation: creation,
				Failed: e.Failed, LatencyMs: e.LatencyMs, TTFTMs: e.TTFTMs,
			}
			event.costQuote, err = prices.quote(event)
			if err != nil {
				return nil, err
			}
			events = append(events, event)
			t := eventTime.In(window.location)
			var label string
			switch bucket {
			case "hour":
				label = t.Format("2006-01-02 15") + ":00"
			case "month":
				label = t.Format("2006-01")
			default:
				label = t.Format("2006-01-02")
			}
			bk := bins[label]
			if bk == nil {
				bk = &outBucket{Label: label}
				bins[label] = bk
				order = append(order, label)
			}
			bk.Input += input
			bk.Output += output
			bk.Cache += cache
			bk.Requests++
			if event.Cost != nil {
				if bk.Cost == nil {
					zero := 0.0
					bk.Cost = &zero
				}
				*bk.Cost += *event.Cost
			} else {
				bk.Unpriced++
			}
		}
	}
	sort.Strings(order)
	buckets := make([]*outBucket, 0, len(order))
	for _, l := range order {
		buckets = append(buckets, bins[l])
	}

	resp := map[string]interface{}{
		"events":       events,
		"buckets":      buckets,
		"collected_at": collectedAt(),
		"collect_err":  collectErr(),
		"skipped":      skipped,
		"version":      version,
		"configured":   true,
		"timezone":     window.timezone,
	}
	return json.Marshal(resp)
}

func addInputCache(e dbEvent, input, output, cache, creation, reasoning int64) bool {
	cachedInput := cache + creation
	if cachedInput <= 0 {
		return false
	}
	total := toI64(e.Tokens["total_tokens"])
	// An inclusive total must never receive cache a second time.
	if total > 0 && total == input+output {
		return false
	}
	provider := strings.ToLower(strings.TrimSpace(e.Provider))
	executor := strings.ToLower(strings.TrimSpace(e.ExecutorType))
	if executor == "openaicompatexecutor" || provider == "openai-compatibility" || strings.HasPrefix(provider, "openai-compatible-") {
		return false
	}
	protocol := provider + " " + executor
	if strings.Contains(protocol, "claude") || strings.Contains(protocol, "anthropic") {
		return total == 0 || total == input+output+cachedInput
	}
	if provider != "" || executor != "" {
		return false
	}
	// Old rows have no protocol identity. Only repair counts that cannot be
	// an input subset; total equality alone also matches Gemini reasoning.
	if cachedInput <= input {
		return false
	}
	return (total == 0 && input == 0) ||
		(total == input+output+cachedInput && total != input+output+reasoning)
}

func parseAnyTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unsupported time %q", s)
}

func collectedAt() interface{} {
	// Report the data freshness timestamp as of this response: with real-time
	// collection this means "the dashboard data is current as of now".
	return time.Now().Format(time.RFC3339Nano)
}

func collectErr() interface{} {
	mu.Lock()
	defer mu.Unlock()
	if lastErr == "" {
		return nil
	}
	return lastErr
}

func collectOnce() {
	ensureDB()
	mu.Lock()
	lastRecv = time.Now()
	mu.Unlock()
}

func toI64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}

// ---- envelope helpers ----

func okEnvelopeJSON(result string) ([]byte, error) {
	return json.Marshal(envelope{OK: true, Result: json.RawMessage(result)})
}

func errorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return raw
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}

func callHost(method string, payload []byte) {
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	var response C.cliproxy_buffer
	var req *C.uint8_t
	if len(payload) > 0 {
		req = (*C.uint8_t)(C.CBytes(payload))
		defer C.free(unsafe.Pointer(req))
	}
	if C.call_host_api(cMethod, req, C.size_t(len(payload)), &response) == 0 && response.ptr != nil {
		C.free_host_buffer(response.ptr, response.len)
	}
}

var _ = os.Getenv
