package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func callKeyNote(t *testing.T, method string, body []byte) (int, []byte) {
	t.Helper()
	req, err := json.Marshal(map[string]any{"Method": method, "Path": "/v0/management/plugins/usage-report/api/key-note", "Body": body})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := handleMethod("management.handle", req)
	if err != nil {
		t.Fatal(err)
	}
	var reply envelope
	if err := json.Unmarshal(raw, &reply); err != nil || !reply.OK {
		t.Fatalf("reply: %s", raw)
	}
	var result struct {
		StatusCode int
		Body       []byte
	}
	if err := json.Unmarshal(reply.Result, &result); err != nil {
		t.Fatal(err)
	}
	return result.StatusCode, result.Body
}

func noteBody(t *testing.T, key, note string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]string{"key": key, "note": note})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestKeyNotesPersistWithoutChangingAttributionOrUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.sqlite")
	openTestDB(t, path)
	u := attributionRecord()
	sendAttribution(t, u)
	u.APIKey = "synthetic-client-secret-B"
	sendAttribution(t, u)
	before, _, _ := attributionResponse(t)
	var originalPayload string
	if err := db.QueryRow("SELECT payload FROM usage_events WHERE id=1").Scan(&originalPayload); err != nil {
		t.Fatal(err)
	}
	note := "开发 & <测试> \"电脑\""
	if status, body := callKeyNote(t, "POST", noteBody(t, before[0].Key, "  "+note+"  ")); status != 200 {
		t.Fatalf("save: %d %s", status, body)
	}
	events, buckets, _ := attributionResponse(t)
	if events[0].KeyNote != note || events[1].KeyNote != "" || events[0].Key != before[0].Key || events[0].KeyLabel != before[0].KeyLabel {
		t.Fatal("note changed identity or affected another client")
	}
	if len(buckets) != 1 || buckets[0].Requests != 2 || buckets[0].Input != 200 || buckets[0].Output != 40 {
		t.Fatal("note changed usage totals")
	}
	for i := range events {
		if events[i].Cost != nil && (before[i].Cost == nil || *events[i].Cost != *before[i].Cost) {
			t.Fatal("note changed costs")
		}
	}
	if status, _ := callKeyNote(t, "POST", noteBody(t, before[1].Key, note)); status != 200 {
		t.Fatal("same note on another key must be allowed")
	}
	if status, _ := callKeyNote(t, "POST", noteBody(t, before[0].Key, "办公电脑")); status != 200 {
		t.Fatal("update failed")
	}
	cliproxyPluginShutdown()
	if err := openDB(path); err != nil {
		t.Fatal(err)
	}
	events, _, _ = attributionResponse(t)
	if events[0].KeyNote != "办公电脑" || events[1].KeyNote != note {
		t.Fatal("notes did not survive database reopen")
	}
	if status, _ := callKeyNote(t, "POST", noteBody(t, before[0].Key, "  ")); status != 200 {
		t.Fatal("clear failed")
	}
	events, _, _ = attributionResponse(t)
	if events[0].KeyNote != "" || events[1].KeyNote != note {
		t.Fatal("clearing one key affected another")
	}
	var after string
	if err := db.QueryRow("SELECT payload FROM usage_events WHERE id=1").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != originalPayload {
		t.Fatal("notes must not rewrite usage event payloads")
	}
}

func TestKeyNoteValidationAndWriteFailure(t *testing.T) {
	openTestDB(t, filepath.Join(t.TempDir(), "invalid.sqlite"))
	key := clientIdentity("synthetic-key", "config-inline")
	for _, body := range [][]byte{
		[]byte("broken-json"), noteBody(t, "arbitrary-id", "x"),
		noteBody(t, key, strings.Repeat("字", 81)), noteBody(t, key, "内嵌\n换行"),
	} {
		if status, _ := callKeyNote(t, "POST", body); status != 400 {
			t.Fatalf("expected invalid request, got %d", status)
		}
	}
	if status, _ := callKeyNote(t, "GET", nil); status != 405 {
		t.Fatal("note mutation requires POST")
	}
	if status, _ := callKeyNote(t, "POST", noteBody(t, key, strings.Repeat("字", 80))); status != 200 {
		t.Fatal("80 Unicode characters must be accepted")
	}
	if _, err := db.Exec("CREATE TRIGGER reject_note BEFORE INSERT ON key_notes BEGIN SELECT RAISE(ABORT, 'injected note failure'); END"); err != nil {
		t.Fatal(err)
	}
	if status, _ := callKeyNote(t, "POST", noteBody(t, key, "replacement")); status == 200 {
		t.Fatal("failed note write was acknowledged as success")
	}
	notes, err := loadKeyNotes(db)
	if err != nil {
		t.Fatal(err)
	}
	if notes[key] != strings.Repeat("字", 80) {
		t.Fatal("failed update replaced the saved note")
	}
}

func TestLegacyAndUnknownNotesKeepTheirIdentityKinds(t *testing.T) {
	openTestDB(t, filepath.Join(t.TempDir(), "legacy-notes.sqlite"))
	if _, err := db.Exec("INSERT INTO usage_events (ts,dedup,payload) VALUES (?,?,?)", "2026-09-10 11:30:00", "legacy-note", `{"timestamp":"2026-09-10T11:30:00Z","model":"gpt-4o-mini","source":"historical-account"}`); err != nil {
		t.Fatal(err)
	}
	u := attributionRecord()
	u.APIKey = ""
	sendAttribution(t, u)
	events, _, _ := attributionResponse(t)
	for _, event := range events {
		if status, _ := callKeyNote(t, "POST", noteBody(t, event.Key, "自定义备注")); status != 200 {
			t.Fatal("displayed groups should support notes")
		}
	}
	updated, _, _ := attributionResponse(t)
	for i := range updated {
		if updated[i].KeyNote != "自定义备注" || updated[i].KeyKind != events[i].KeyKind || updated[i].Key != events[i].Key {
			t.Fatal("note reclassified an unknown or historical source as a client")
		}
	}
}
