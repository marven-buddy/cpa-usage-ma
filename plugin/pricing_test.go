package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func rate(n float64) *float64 { return &n }

func assertCost(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 1e-9 {
		t.Fatalf("cost=%v, want %.9f", got, want)
	}
}

func TestCostCategories(t *testing.T) {
	p := modelPrice{tokenPrice: tokenPrice{Input: rate(2), Output: rate(8), CacheRead: rate(.2), CacheWrite: rate(2.5)}}
	e := outEvent{Input: 1000000, Cache: 600000, CacheCreation: 100000, Output: 100000, Reasoning: 50000}
	// 300k ordinary + 600k read + 100k write + 100k output. Reasoning is an output subset.
	assertCost(t, calculateCost(e, p), 1.77)
	p.CacheWrite = nil
	if calculateCost(e, p) != nil {
		t.Fatal("missing write rate must not become zero or ordinary input")
	}
	e.CacheCreation = 0
	assertCost(t, calculateCost(e, p), 1.72)
	p.CacheRead = rate(0)
	assertCost(t, calculateCost(e, p), 1.6)
	e.Cache = e.Input + 1
	if calculateCost(e, p) != nil {
		t.Fatal("inconsistent counters must not produce a bill")
	}
}

func TestCatalogAndContextTiers(t *testing.T) {
	prices, err := parseCatalog([]byte(`{"test-model":{"mode":"chat","input_cost_per_token":0.000002,"output_cost_per_token":0.000008,"cache_read_input_token_cost":0,"input_cost_per_token_above_200k_tokens":0.000004,"output_cost_per_token_above_200k_tokens":0.000016},"image-model":{"mode":"image_generation","input_cost_per_token":1,"output_cost_per_token":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(prices) != 1 {
		t.Fatal("non-text models should not be priced")
	}
	p := prices["test-model"]
	assertCost(t, calculateCost(outEvent{Input: 200000, Output: 1000}, p), .408)
	assertCost(t, calculateCost(outEvent{Input: 200001, Output: 1000}, p), .816004)
	if p.CacheRead == nil || *p.CacheRead != 0 || p.CacheWrite != nil {
		t.Fatal("unknown and free prices got conflated")
	}
	if _, err := parseCatalog([]byte(`{"garbage":{}}`)); err == nil {
		t.Fatal("empty feed accepted")
	}
}

func addVersion(t *testing.T, at float64, prices, overrides map[string]modelPrice) {
	t.Helper()
	p, _ := json.Marshal(prices)
	o, _ := json.Marshal(overrides)
	if _, err := db.Exec("INSERT INTO price_versions (effective_at,prices,overrides) VALUES(?,?,?)", at, string(p), string(o)); err != nil {
		t.Fatal(err)
	}
}

func TestHistoricalPriceVersions(t *testing.T) {
	openTestDB(t, filepath.Join(t.TempDir(), "history.sqlite"))
	if _, err := db.Exec("DELETE FROM price_versions"); err != nil {
		t.Fatal(err)
	}
	p := modelPrice{tokenPrice: tokenPrice{Input: rate(2), Output: rate(8)}}
	addVersion(t, 1000, map[string]modelPrice{"test": p}, map[string]modelPrice{})
	p.Input = rate(4)
	addVersion(t, 2000, map[string]modelPrice{"test": p}, map[string]modelPrice{priceKey("reseller", "alias"): p})
	p.Input = rate(9)
	addVersion(t, 3000, map[string]modelPrice{"test": p}, map[string]modelPrice{priceKey("reseller", "alias"): p})
	book, err := loadPriceBook(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		model, provider string
		ts, want        float64
		basis           string
	}{
		{"test", "codex", 1500, 2, "catalog"},
		{"test", "codex", 2500, 4, "catalog"},
		{"test", "codex", 500, 2, "historical_estimate"},
		{"alias", "reseller", 1500, 4, "historical_estimate"},
		{"alias", "reseller", 2500, 4, "custom"},
		{"alias", "reseller", 3500, 9, "custom"},
	} {
		q, err := book.quote(outEvent{Model: tc.model, Provider: tc.provider, Ts: tc.ts, Input: 1000000})
		if err != nil {
			t.Fatal(err)
		}
		assertCost(t, q.Cost, tc.want)
		if q.PriceBasis != tc.basis {
			t.Fatalf("%+v: basis %s", tc, q.PriceBasis)
		}
	}
	for _, e := range []outEvent{{Model: "test-custom-suffix", Input: 1, Ts: 1500}, {Model: "alias", Provider: "other", Input: 1, Ts: 3500}} {
		q, _ := book.quote(e)
		if q.Cost != nil || q.CostStatus != "unpriced" {
			t.Fatal("unknown model/channel got another model's price")
		}
	}
	q, _ := book.quote(outEvent{Model: "unknown", Failed: true})
	assertCost(t, q.Cost, 0)
}

func TestCustomPriceValidationAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.sqlite")
	openTestDB(t, path)
	for _, body := range []string{`{}`, `{"model":"test","price":{"input":-1,"output":2}}`, `{"model":"test","price":{"input":2}}`, `{"model":"test","price":{"input":"2","output":2}}`} {
		if setCustomPrice([]byte(body)) == nil {
			t.Fatalf("invalid price accepted: %s", body)
		}
	}
	body := []byte(`{"model":"local-alias","provider":"channel-a","price":{"input":2,"output":8,"cache_read":0}}`)
	if err := setCustomPrice(body); err != nil {
		t.Fatal(err)
	}
	book, _ := loadPriceBook(db)
	v, err := book.load(len(book.versions) - 1)
	if err != nil {
		t.Fatal(err)
	}
	p, custom, ok := v.find("channel-a", "local-alias")
	if !ok || !custom || p.CacheRead == nil || *p.CacheRead != 0 {
		t.Fatal("custom zero rate was lost")
	}
	// Reopen the real SQLite file, rather than relying on an in-memory settings map.
	openTestDB(t, filepath.Join(t.TempDir(), "other.sqlite"))
	openTestDB(t, path)
	data, err := pricingResponse()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "local-alias") {
		t.Fatal("custom price not persisted")
	}
	if err := setCustomPrice([]byte(`{"model":"local-alias","provider":"channel-a","price":null}`)); err != nil {
		t.Fatal(err)
	}
	book, _ = loadPriceBook(db)
	v, _ = book.load(len(book.versions) - 1)
	if _, _, ok := v.find("channel-a", "local-alias"); ok {
		t.Fatal("reset left the custom rate active")
	}
}

type rewriteTransport struct{ url string }

type failedPriceTransport struct{}

func (failedPriceTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("read tcp 192.0.2.10:52494->198.51.100.20:443: read: connection reset by peer")
}

func TestPriceUpdateFailureIsFriendly(t *testing.T) {
	openTestDB(t, filepath.Join(t.TempDir(), "friendly-error.sqlite"))
	oldClient := priceHTTPClient
	priceHTTPClient = &http.Client{Transport: failedPriceTransport{}}
	t.Cleanup(func() { priceHTTPClient = oldClient })
	const want = "暂时无法更新价格，已继续使用本地价格，请稍后重试。"
	book, _ := loadPriceBook(db)
	count := len(book.versions)

	t.Run("failed update", func(t *testing.T) {
		status, _, body := callMgmt(t, "/v0/management/plugins/cpa-usage-ma/api/pricing/refresh", "POST")
		var response struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatal(err)
		}
		if status != 502 || response.Error != want {
			t.Fatalf("HTTP %d, error %q; want friendly warning", status, response.Error)
		}
		book, _ = loadPriceBook(db)
		if len(book.versions) != count {
			t.Fatal("failed update changed local prices")
		}
	})
	t.Run("previously saved technical error", func(t *testing.T) {
		if _, err := db.Exec("UPDATE price_state SET error=? WHERE id=1", "价格更新失败：read tcp 192.0.2.10:443: connection reset by peer"); err != nil {
			t.Fatal(err)
		}
		body, err := pricingResponse()
		if err != nil {
			t.Fatal(err)
		}
		var response struct {
			Warning string `json:"update_error"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatal(err)
		}
		if response.Warning != want {
			t.Fatalf("saved error %q; want friendly warning", response.Warning)
		}
	})
}

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	replacement, err := http.NewRequest(req.Method, r.url, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultTransport.RoundTrip(replacement)
}

func TestRefreshOfflineAndOverride(t *testing.T) {
	openTestDB(t, filepath.Join(t.TempDir(), "refresh.sqlite"))
	if err := setCustomPrice([]byte(`{"model":"test","provider":"proxy","price":{"input":1,"output":2}}`)); err != nil {
		t.Fatal(err)
	}
	status := 200
	feed := `{"test":{"mode":"chat","input_cost_per_token":0.000003,"output_cost_per_token":0.000004}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); fmt.Fprint(w, feed) }))
	defer server.Close()
	oldClient := priceHTTPClient
	priceHTTPClient = &http.Client{Transport: rewriteTransport{server.URL}}
	defer func() { priceHTTPClient = oldClient }()
	if err := refreshPrices(); err != nil {
		t.Fatal(err)
	}
	book, _ := loadPriceBook(db)
	v, _ := book.load(len(book.versions) - 1)
	p, custom, _ := v.find("proxy", "test")
	if !custom || *p.Input != 1 {
		t.Fatal("refresh overwrote custom rate")
	}
	p, _, _ = v.find("codex", "test")
	if *p.Input != 3 {
		t.Fatal("catalog did not refresh")
	}
	count := len(book.versions)
	if err := refreshPrices(); err != nil {
		t.Fatal(err)
	}
	book, _ = loadPriceBook(db)
	if len(book.versions) != count {
		t.Fatal("identical feeds created extra history")
	}
	for _, bad := range []struct {
		status int
		feed   string
	}{{503, "unavailable"}, {200, "{broken"}, {200, "{}"}} {
		status = bad.status
		feed = bad.feed
		if err := refreshPrices(); err == nil {
			t.Fatal("bad feed accepted")
		}
		book, _ = loadPriceBook(db)
		if len(book.versions) != count {
			t.Fatal("bad feed changed prices")
		}
		data, _ := pricingResponse()
		if !strings.Contains(string(data), priceUpdateWarning) {
			t.Fatal("offline state hidden")
		}
	}
	status = 200
	feed = `{"test":{"mode":"chat","input_cost_per_token":0.000003,"output_cost_per_token":0.000004}}`
	if err := refreshPrices(); err != nil {
		t.Fatal(err)
	}
	data, _ := pricingResponse()
	var recovered struct {
		Warning string `json:"update_error"`
	}
	if err := json.Unmarshal(data, &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.Warning != "" {
		t.Fatal("successful retry left the warning visible")
	}
}

func TestCostAPIFromRawUsage(t *testing.T) {
	openTestDB(t, filepath.Join(t.TempDir(), "api.sqlite"))
	if err := setCustomPrice([]byte(`{"model":"alias","provider":"claude","price":{"input":2,"output":8,"cache_read":0.2,"cache_write":2.5}}`)); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"alias", "unknown-alias"} {
		handleUsage([]byte(fmt.Sprintf(`{"result":{"Provider":"claude","Model":%q,"Source":"secret-key","RequestedAt":"2026-09-10T11:30:00+08:00","Detail":{"InputTokens":300000,"OutputTokens":100000,"CacheReadTokens":600000,"CacheCreationTokens":100000,"TotalTokens":1100000}}}`, model)))
	}
	status, _, body := callMgmt(t, "/v0/management/plugins/cpa-usage-ma/api/usage", "GET")
	if status != 200 {
		t.Fatalf("HTTP %d: %s", status, body)
	}
	if strings.Contains(string(body), "secret-key") {
		t.Fatal("API exposed client key")
	}
	var response struct {
		Events  []outEvent
		Buckets []outBucket
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Events) != 2 || response.Events[0].Input != 1000000 {
		t.Fatalf("bad normalized usage: %s", body)
	}
	assertCost(t, response.Events[0].Cost, 1.77)
	if response.Events[1].Cost != nil || response.Buckets[0].Unpriced != 1 {
		t.Fatal("missing prices disappeared from API")
	}
	assertCost(t, response.Buckets[0].Cost, 1.77)
}

func TestPricingManagementWrite(t *testing.T) {
	openTestDB(t, filepath.Join(t.TempDir(), "management.sqlite"))
	for _, tc := range []struct {
		method, body string
		want         int
	}{
		{"POST", `{"model":"management-alias","provider":"local","price":{"input":2,"output":8}}`, 200},
		{"POST", `{"model":"management-alias","price":{"input":-1,"output":8}}`, 400},
		{"DELETE", `{}`, 405},
	} {
		request, _ := json.Marshal(map[string]any{"result": map[string]any{"Method": tc.method, "Path": "/v0/management/plugins/cpa-usage-ma/api/pricing", "Body": []byte(tc.body)}})
		raw, err := handleMethod("management.handle", request)
		if err != nil {
			t.Fatal(err)
		}
		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil || !env.OK {
			t.Fatalf("envelope %s: %v", raw, err)
		}
		var response struct {
			StatusCode int
			Body       []byte
		}
		if err := json.Unmarshal(env.Result, &response); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != tc.want {
			t.Fatalf("%s returned %d: %s", tc.method, response.StatusCode, response.Body)
		}
		if tc.want == 200 && !strings.Contains(string(response.Body), "management-alias") {
			t.Fatal("write not reflected in response")
		}
	}
}
