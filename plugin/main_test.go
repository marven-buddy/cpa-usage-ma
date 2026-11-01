package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openTestDB(t *testing.T, path string) {
	t.Helper()
	if err := openDB(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cliproxyPluginShutdown)
}

func TestRegister(t *testing.T) {
	raw, err := handleMethod("plugin.register", nil)
	if err != nil {
		t.Fatal(err)
	}
	var e envelope
	if err := json.Unmarshal(raw, &e); err != nil || !e.OK {
		t.Fatalf("register: %s %v", raw, err)
	}
	var registration struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(e.Result, &registration); err != nil || registration.SchemaVersion != 6 {
		t.Fatal("management JSON must preserve raw note strings using schema 6")
	}
}

func mgmtReq(path, method string) []byte {
	req, _ := json.Marshal(map[string]interface{}{
		"result": map[string]interface{}{
			"Method":  method,
			"Path":    path,
			"Query":   map[string][]string{"start": {"2026-09-09"}, "end": {"2026-09-18"}, "bucket": {"day"}},
			"Headers": map[string][]string{},
			"Body":    nil,
		},
	})
	return req
}

func callMgmt(t *testing.T, path, method string) (int, string, []byte) {
	t.Helper()
	raw, err := handleMethod("management.handle", mgmtReq(path, method))
	if err != nil {
		t.Fatal(err)
	}
	var e envelope
	if err := json.Unmarshal(raw, &e); err != nil || !e.OK {
		t.Fatalf("handle %s: %s %v", path, raw, err)
	}
	var resp struct {
		StatusCode int
		Headers    map[string][]string
		Body       []byte
	}
	if err := json.Unmarshal(e.Result, &resp); err != nil {
		t.Fatal(err)
	}
	ct := ""
	if v := resp.Headers["content-type"]; len(v) > 0 {
		ct = v[0]
	}
	return resp.StatusCode, ct, resp.Body
}

func TestManagementReport(t *testing.T) {
	status, ct, body := callMgmt(t, "/v0/resource/plugins/usage-report/report", "GET")
	if status != 200 || len(body) < 300 {
		t.Fatalf("page: %d %s %d bytes", status, ct, len(body))
	}
	t.Logf("page ok: %d bytes, %s", len(body), ct)

	status, ct, js := callMgmt(t, "/v0/resource/plugins/usage-report/assets/index-BQjrGGWY.js", "GET")
	if status != 200 || len(js) < 100000 {
		t.Fatalf("js: %d %s %d bytes", status, ct, len(js))
	}
	t.Logf("js ok: %d bytes, %s", len(js), ct)
}

func TestManagementAPI(t *testing.T) {
	openTestDB(t, filepath.Join(t.TempDir(), "test.sqlite"))
	seed := []byte(`{"result":{"Provider":"codex","Model":"gpt-ci-test","Source":"sk-ci","RequestedAt":"2026-09-10T11:30:00+08:00","Detail":{"InputTokens":100,"OutputTokens":20,"CacheReadTokens":80,"TotalTokens":120},"Failed":false}}`)
	if _, err := handleMethod("usage.handle", seed); err != nil {
		t.Fatal(err)
	}
	status, _, body := callMgmt(t, "/v0/management/plugins/usage-report/api/usage", "GET")
	if status != 200 {
		t.Fatalf("usage api: %d %s", status, body)
	}
	var data struct {
		Events  []map[string]interface{} `json:"events"`
		Buckets []map[string]interface{} `json:"buckets"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatalf("decode: %v body=%s", err, body[:200])
	}
	if len(data.Events) != 1 {
		t.Fatalf("expect 1 event, got %d", len(data.Events))
	}
	if got := data.Events[0]["model"]; got != "gpt-ci-test" {
		t.Fatalf("unexpected model: %v", got)
	}
	t.Logf("usage api ok: %d events, %d buckets", len(data.Events), len(data.Buckets))
}

func TestUsageHandle(t *testing.T) {
	openTestDB(t, filepath.Join(t.TempDir(), "test.sqlite"))
	req := []byte(`{"result":{"Provider":"codex","Model":"gpt-6-astra","Source":"sk-test","RequestedAt":"2026-09-18T11:30:00+08:00","Latency":1500000000,"TTFT":250000000,"Detail":{"InputTokens":100,"OutputTokens":20,"ReasoningTokens":5,"CachedTokens":80,"CacheReadTokens":80,"TotalTokens":120},"Failed":false}}`)
	raw, err := handleMethod("usage.handle", req)
	if err != nil {
		t.Fatal(err)
	}
	var e envelope
	if err := json.Unmarshal(raw, &e); err != nil || !e.OK {
		t.Fatalf("usage.handle: %s %v", raw, err)
	}
	var n int
	mu.Lock()
	_ = db.QueryRow("SELECT COUNT(*) FROM usage_events").Scan(&n)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("expect 1 row, got %d", n)
	}
	t.Logf("usage.handle wrote %d row", n)
}

func TestApplyConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Cleanup(cliproxyPluginShutdown)
	directPath := filepath.Join(tmp, "direct.sqlite")
	direct, _ := json.Marshal(map[string]any{"config_yaml": "enabled: true\ndb_path: " + directPath + "\n"})
	if err := applyConfig(direct); err != nil {
		t.Fatal(err)
	}
	if lastOpened != directPath {
		t.Fatalf("direct shape: got %q", lastOpened)
	}
	wrappedPath := filepath.Join(tmp, "wrapped.sqlite")
	wrapped, _ := json.Marshal(map[string]any{"result": map[string]any{"config_yaml": "db_path: " + wrappedPath}})
	if err := applyConfig(wrapped); err != nil {
		t.Fatal(err)
	}
	if lastOpened != wrappedPath {
		t.Fatalf("wrapped shape: got %q", lastOpened)
	}
}

func TestV8ConfigAndFailedReconfigure(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(cliproxyPluginShutdown)
	path := filepath.Join(dir, "new", "nested", "usage.sqlite")
	// Match the v8 host's []byte JSON encoding, including Base64 config_yaml.
	raw, err := json.Marshal(struct {
		ConfigYAML    []byte `json:"config_yaml"`
		SchemaVersion int    `json:"schema_version"`
	}{[]byte("enabled: true\ndb_path: " + path + "\n"), 9})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handleMethod("plugin.register", raw); err != nil {
		t.Fatal(err)
	}
	if lastOpened != path {
		t.Fatalf("v8 configuration ignored: got %q, want %q", lastOpened, path)
	}
	blocker := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	badConfig, _ := json.Marshal(map[string]any{"config_yaml": []byte("db_path: " + filepath.Join(blocker, "usage.sqlite"))})
	if _, err := handleMethod("plugin.reconfigure", badConfig); err == nil {
		t.Fatal("invalid database path should fail reconfiguration")
	}
	if lastOpened != path {
		t.Fatal("failed reconfiguration replaced the working database")
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("working database was closed: %v", err)
	}
}

func TestManagementRegisterRoutes(t *testing.T) {
	raw, err := handleMethod("management.register", nil)
	if err != nil {
		t.Fatal(err)
	}
	var e envelope
	if err := json.Unmarshal(raw, &e); err != nil || !e.OK {
		t.Fatalf("register: %s %v", raw, err)
	}
	var reg struct {
		Routes []struct {
			Method string
			Path   string
		}
		Resources []struct {
			Path string
		}
	}
	if err := json.Unmarshal(e.Result, &reg); err != nil {
		t.Fatal(err)
	}
	if len(reg.Routes) != 7 {
		t.Fatalf("expect 7 authenticated routes, got %d", len(reg.Routes))
	}
	for _, r := range reg.Routes {
		if r.Method != "GET" && r.Method != "POST" {
			t.Fatalf("route %s missing explicit method", r.Path)
		}
		if !strings.HasPrefix(r.Path, "/plugins/usage-report/api/") {
			t.Fatalf("route outside plugin namespace: %s", r.Path)
		}
	}
	for _, r := range reg.Resources {
		if strings.Contains(r.Path, "/api/") {
			t.Fatalf("dynamic api must not be registered as resource: %s", r.Path)
		}
	}
}
