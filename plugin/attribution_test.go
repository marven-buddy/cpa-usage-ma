package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func attributionRecord() usageRecord {
	u := usageRecord{
		RequestID: "request-1", SelectionID: "selection-1", Provider: "codex", Model: "gpt-4o-mini",
		APIKey: "synthetic-client-secret-A", AccessProvider: "config-inline",
		Source: "account@example.test", AuthID: "upstream-account-1",
		RequestedAt: time.Date(2026, 9, 10, 11, 30, 0, 0, time.UTC),
	}
	u.Detail.InputTokens, u.Detail.OutputTokens, u.Detail.TotalTokens = 100, 20, 120
	return u
}

func sendAttribution(t *testing.T, u usageRecord) {
	t.Helper()
	raw, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handleMethod("usage.handle", raw); err != nil {
		t.Fatal(err)
	}
}

func attributionResponse(t *testing.T) ([]outEvent, []outBucket, []byte) {
	t.Helper()
	status, _, body := callMgmt(t, "/v0/management/plugins/usage-report/api/usage", "GET")
	if status != 200 {
		t.Fatalf("HTTP %d: %s", status, body)
	}
	var result struct {
		Events  []outEvent  `json:"events"`
		Buckets []outBucket `json:"buckets"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	return result.Events, result.Buckets, body
}

func TestClientAttributionSeparatesClientsAndUpstreams(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attribution.sqlite")
	openTestDB(t, path)
	base := attributionRecord()
	variants := []usageRecord{base, base, base, base, base, base}
	// Deliberately identical times, counters and request IDs expose client-based dedup bugs.
	variants[1].APIKey = "synthetic-client-secret-B"
	variants[2].Provider, variants[2].Model = "claude", "another-model"
	variants[2].AuthID, variants[2].Source = "upstream-account-2", "other-account@example.test"
	variants[3].AccessProvider = "plugin-auth"
	variants[4].APIKey = ""
	variants[5].APIKey, variants[5].AuthID = "", "upstream-account-3"
	for _, u := range variants {
		sendAttribution(t, u)
	}
	events, buckets, body := attributionResponse(t)
	if len(events) != len(variants) {
		t.Fatalf("got %d events, want %d", len(events), len(variants))
	}
	if events[0].Key == events[1].Key {
		t.Fatal("different client keys sharing one upstream were merged")
	}
	if events[0].Key != events[2].Key {
		t.Fatal("one client was split by provider, model or upstream rotation")
	}
	if events[0].UpstreamID == events[2].UpstreamID || events[0].UpstreamID != events[1].UpstreamID {
		t.Fatal("upstream identity was not tracked independently")
	}
	if events[0].Key == events[3].Key {
		t.Fatal("distinct access providers were merged")
	}
	if events[4].KeyKind != "unknown" || events[4].Key != events[5].Key || events[4].KeyLabel != "未知客户端" {
		t.Fatal("missing client keys fell back to upstream identities")
	}
	if events[0].KeyKind != "client" || len(events[0].Key) != len("client-")+64 {
		t.Fatal("grouping must use the full client identity")
	}
	if len(buckets) != 1 || buckets[0].Requests != int64(len(variants)) || buckets[0].Input != 600 || buckets[0].Output != 120 {
		t.Fatalf("overall usage changed: %+v", buckets)
	}
	rows, err := db.Query("SELECT payload FROM usage_events ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		var stored dbEvent
		if err := json.Unmarshal([]byte(payload), &stored); err != nil {
			t.Fatal(err)
		}
		if stored.EventVersion != 2 || stored.Source != "" || stored.APIKey != "" {
			t.Fatal("new rows must store versioned, anonymized identity fields")
		}
		for _, u := range variants {
			for _, secret := range []string{u.APIKey, u.Source, u.AuthID} {
				if secret != "" && (strings.Contains(payload, secret) || strings.Contains(string(body), secret)) {
					t.Fatal("raw client or upstream identity leaked")
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	cliproxyPluginShutdown()
	if err := openDB(path); err != nil {
		t.Fatal(err)
	}
	after, _, _ := attributionResponse(t)
	if len(after) != len(events) {
		t.Fatal("restart changed event count")
	}
	for i := range after {
		if after[i].Key != events[i].Key || after[i].UpstreamID != events[i].UpstreamID {
			t.Fatal("identity changed across database reopen")
		}
	}
}

func TestSourceFallbackNeverPersistsClientSecret(t *testing.T) {
	openTestDB(t, filepath.Join(t.TempDir(), "fallback.sqlite"))
	u := attributionRecord()
	u.AuthID, u.Source = "", u.APIKey
	sendAttribution(t, u)
	var payload string
	if err := db.QueryRow("SELECT payload FROM usage_events").Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, u.APIKey) {
		t.Fatal("host source fallback persisted the client secret")
	}
	events, _, _ := attributionResponse(t)
	if len(events) != 1 || events[0].KeyKind != "client" || events[0].UpstreamID != "" {
		t.Fatal("client fallback was misclassified as an upstream")
	}
	// Without AuthID, a genuine upstream Source still has its own opaque identity.
	u.Source = "upstream-only@example.test"
	if upstreamIdentity(u) == "" {
		t.Fatal("upstream source was lost")
	}
	if clientIdentity("  ", "config-inline") != "" {
		t.Fatal("blank APIKey must remain unknown")
	}
	if clientIdentity(" secret ", " config-inline ") != clientIdentity("secret", "config-inline") {
		t.Fatal("host whitespace normalization is inconsistent")
	}
	if clientIdentity("b\nc", "a") == clientIdentity("c", "a\nb") {
		t.Fatal("identity framing is ambiguous")
	}
}

func TestLegacySourcesRemainDistinctAndUnmodified(t *testing.T) {
	openTestDB(t, filepath.Join(t.TempDir(), "legacy.sqlite"))
	legacy := []string{
		`{"timestamp":"2026-09-10T11:30:00Z","model":"gpt-4o-mini","source":"synthetic-client-secret-A","tokens":{"input_tokens":100,"output_tokens":20,"total_tokens":120}}`,
		`{"timestamp":"2026-09-10T11:30:00Z","model":"gpt-4o-mini","api_key":"legacy-explicit-key","tokens":{"input_tokens":100,"output_tokens":20,"total_tokens":120}}`,
		`{"timestamp":"2026-09-10T11:30:00Z","model":"gpt-4o-mini","tokens":{"input_tokens":100,"output_tokens":20,"total_tokens":120}}`,
	}
	for i, payload := range legacy {
		if _, err := db.Exec("INSERT INTO usage_events (ts,dedup,payload) VALUES (?,?,?)", "2026-09-10 11:30:00", []string{"old-a", "old-b", "old-c"}[i], payload); err != nil {
			t.Fatal(err)
		}
	}
	sendAttribution(t, attributionRecord())
	unknown := attributionRecord()
	unknown.APIKey = ""
	sendAttribution(t, unknown)
	events, buckets, body := attributionResponse(t)
	if len(events) != 5 || len(buckets) != 1 || buckets[0].Requests != 5 || buckets[0].Input != 500 || buckets[0].Output != 100 {
		t.Fatal("history was dropped from totals")
	}
	for i := range legacy {
		if events[i].KeyKind != "legacy_source" || !strings.Contains(events[i].KeyLabel, "历史来源") {
			t.Fatal("legacy row was guessed to be a client")
		}
		var stored, dedup string
		if err := db.QueryRow("SELECT payload,dedup FROM usage_events WHERE id=?", i+1).Scan(&stored, &dedup); err != nil {
			t.Fatal(err)
		}
		if stored != legacy[i] || dedup != []string{"old-a", "old-b", "old-c"}[i] {
			t.Fatal("query rewrote historical data or dedup")
		}
	}
	if events[0].Key == events[3].Key || events[2].Key == events[4].Key {
		t.Fatal("legacy and current client namespaces were merged")
	}
	for _, raw := range []string{"synthetic-client-secret-A", "legacy-explicit-key"} {
		if strings.Contains(string(body), raw) {
			t.Fatal("API exposed a raw historical credential")
		}
	}
}

func TestAttributionDedupPreservesDistinctExecutions(t *testing.T) {
	openTestDB(t, filepath.Join(t.TempDir(), "dedup.sqlite"))
	base := attributionRecord()
	sendAttribution(t, base)
	sendAttribution(t, base)
	variants := []usageRecord{base, base, base, base, base}
	variants[0].APIKey = "synthetic-client-secret-B"
	variants[1].Model = "side-model"
	variants[2].Provider = "another-provider"
	variants[3].SelectionID = "selection-2"
	variants[4].RequestID = "request-2"
	for _, u := range variants {
		sendAttribution(t, u)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM usage_events").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 6 {
		t.Fatalf("exact replay should dedup but distinct client/model/provider/selection/request must remain: rows=%d", count)
	}
}
