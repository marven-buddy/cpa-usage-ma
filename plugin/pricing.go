package main

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed prices.json
var bundledPrices []byte

const priceURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"
const priceUpdateWarning = "暂时无法更新价格，已继续使用本地价格，请稍后重试。"

// Rates are USD per million tokens. Nil means unknown, while zero is a free rate.
type tokenPrice struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
}

type priceTier struct {
	Above int64 `json:"above"`
	tokenPrice
}

type modelPrice struct {
	tokenPrice
	Tiers []priceTier `json:"tiers,omitempty"`
}

type priceVersion struct {
	ID          int64
	EffectiveAt float64
	Prices      map[string]modelPrice
	Overrides   map[string]modelPrice
}

type priceBook struct {
	conn     *sql.DB
	versions []*priceVersion
}

var tierField = regexp.MustCompile(`^input_cost_per_token_above_([0-9]+)k_tokens$`)

func parseCatalog(raw []byte) (map[string]modelPrice, error) {
	var entries map[string]map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	prices := map[string]modelPrice{}
	for model, entry := range entries {
		if entry["mode"] != "chat" && entry["mode"] != "completion" {
			continue
		}
		read := func(key string) *float64 {
			if n, ok := entry[key].(float64); ok && n >= 0 && !math.IsInf(n, 0) {
				n *= 1e6
				return &n
			}
			return nil
		}
		rates := func(suffix string) tokenPrice {
			return tokenPrice{read("input_cost_per_token" + suffix), read("output_cost_per_token" + suffix), read("cache_read_input_token_cost" + suffix), read("cache_creation_input_token_cost" + suffix)}
		}
		p := modelPrice{tokenPrice: rates("")}
		if p.Input == nil || p.Output == nil {
			continue
		}
		for key := range entry {
			if match := tierField.FindStringSubmatch(key); match != nil {
				var above int64
				fmt.Sscan(match[1], &above)
				p.Tiers = append(p.Tiers, priceTier{Above: above * 1000, tokenPrice: rates(strings.TrimPrefix(key, "input_cost_per_token"))})
			}
		}
		sort.Slice(p.Tiers, func(i, j int) bool { return p.Tiers[i].Above < p.Tiers[j].Above })
		prices[model] = p
	}
	if len(prices) == 0 {
		return nil, fmt.Errorf("价格源没有有效的文本模型单价")
	}
	return prices, nil
}

func initPricing(conn *sql.DB) error {
	if _, err := conn.Exec(`CREATE TABLE IF NOT EXISTS price_versions (id INTEGER PRIMARY KEY, effective_at REAL NOT NULL, prices TEXT NOT NULL, overrides TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS price_state (id INTEGER PRIMARY KEY CHECK(id=1), checked_at REAL NOT NULL, error TEXT NOT NULL)`); err != nil {
		return err
	}
	var count int
	if err := conn.QueryRow("SELECT COUNT(*) FROM price_versions").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	var seed struct {
		UpdatedAt time.Time       `json:"updated_at"`
		Models    json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(bundledPrices, &seed); err != nil {
		return err
	}
	prices, err := parseCatalog(seed.Models)
	if err != nil {
		return err
	}
	if err := savePriceVersion(conn, prices, map[string]modelPrice{}); err != nil {
		return err
	}
	_, err = conn.Exec("INSERT INTO price_state VALUES (1, ?, '')", float64(seed.UpdatedAt.Unix()))
	return err
}

func savePriceVersion(conn *sql.DB, prices, overrides map[string]modelPrice) error {
	p, err := json.Marshal(prices)
	if err != nil {
		return err
	}
	o, err := json.Marshal(overrides)
	if err != nil {
		return err
	}
	_, err = conn.Exec("INSERT INTO price_versions (effective_at, prices, overrides) VALUES (?,?,?)", float64(time.Now().UnixNano())/1e9, string(p), string(o))
	return err
}

func loadPriceBook(conn *sql.DB) (*priceBook, error) {
	book := &priceBook{conn: conn}
	rows, err := conn.Query("SELECT id, effective_at FROM price_versions ORDER BY effective_at, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		v := &priceVersion{}
		if err := rows.Scan(&v.ID, &v.EffectiveAt); err != nil {
			return nil, err
		}
		book.versions = append(book.versions, v)
	}
	return book, rows.Err()
}

func (b *priceBook) load(index int) (*priceVersion, error) {
	v := b.versions[index]
	if v.Prices != nil {
		return v, nil
	}
	var prices, overrides string
	if err := b.conn.QueryRow("SELECT prices, overrides FROM price_versions WHERE id=?", v.ID).Scan(&prices, &overrides); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(prices), &v.Prices); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(overrides), &v.Overrides); err != nil {
		return nil, err
	}
	return v, nil
}

func priceKey(provider, model string) string { return provider + "\n" + model }

func (v *priceVersion) find(provider, model string) (modelPrice, bool, bool) {
	if p, ok := v.Overrides[priceKey(provider, model)]; ok {
		return p, true, true
	}
	// Match exact names only. In particular, never strip arbitrary model suffixes.
	provider = strings.ToLower(provider)
	switch provider {
	case "codex":
		provider = "openai"
	case "claude":
		provider = "anthropic"
	}
	for _, key := range []string{provider + "/" + model, model} {
		if p, ok := v.Prices[key]; ok {
			return p, false, true
		}
	}
	return modelPrice{}, false, false
}

type costQuote struct {
	Cost         *float64 `json:"cost"`
	CostStatus   string   `json:"cost_status"`
	PriceVersion int64    `json:"price_version,omitempty"`
	PriceBasis   string   `json:"price_basis,omitempty"`
}

func calculateCost(e outEvent, p modelPrice) *float64 {
	r := p.tokenPrice
	for _, tier := range p.Tiers {
		if e.Input > tier.Above {
			if tier.Input != nil {
				r.Input = tier.Input
			}
			if tier.Output != nil {
				r.Output = tier.Output
			}
			if tier.CacheRead != nil {
				r.CacheRead = tier.CacheRead
			}
			if tier.CacheWrite != nil {
				r.CacheWrite = tier.CacheWrite
			}
		}
	}
	counts := []int64{e.Input - e.Cache - e.CacheCreation, e.Output, e.Cache, e.CacheCreation}
	rates := []*float64{r.Input, r.Output, r.CacheRead, r.CacheWrite}
	cost := 0.0
	for i, count := range counts {
		if count < 0 || (count > 0 && rates[i] == nil) {
			return nil
		}
		if count > 0 {
			cost += float64(count) * *rates[i] / 1e6
		}
	}
	if math.IsInf(cost, 0) || math.IsNaN(cost) {
		return nil
	}
	return &cost
}

func (b *priceBook) quote(e outEvent) (costQuote, error) {
	if e.Input == 0 && e.Output == 0 && e.Cache == 0 && e.CacheCreation == 0 {
		zero := 0.0
		return costQuote{Cost: &zero, CostStatus: "priced", PriceBasis: "no_tokens"}, nil
	}
	index := sort.Search(len(b.versions), func(i int) bool { return b.versions[i].EffectiveAt > e.Ts }) - 1
	start := index
	if start < 0 {
		start = 0
	}
	// An old unpriced request may use the first later complete price, explicitly
	// marked as an estimate. An already priced request never follows new prices.
	for i := start; i < len(b.versions); i++ {
		v, err := b.load(i)
		if err != nil {
			return costQuote{}, err
		}
		if p, custom, ok := v.find(e.Provider, e.Model); ok {
			if cost := calculateCost(e, p); cost != nil {
				basis := "catalog"
				if custom {
					basis = "custom"
				}
				if i > index {
					basis = "historical_estimate"
				}
				return costQuote{cost, "priced", v.ID, basis}, nil
			}
		}
	}
	return costQuote{CostStatus: "unpriced"}, nil
}

func pricingResponse() ([]byte, error) {
	ensureDB()
	mu.Lock()
	defer mu.Unlock()
	if db == nil {
		return nil, fmt.Errorf("用量数据库不可用")
	}
	book, err := loadPriceBook(db)
	if err != nil {
		return nil, err
	}
	v, err := book.load(len(book.versions) - 1)
	if err != nil {
		return nil, err
	}
	type priceRow struct {
		Model    string      `json:"model"`
		Provider string      `json:"provider"`
		Price    *modelPrice `json:"price"`
		Custom   bool        `json:"custom"`
	}
	models := map[string]priceRow{}
	rows, err := db.Query("SELECT DISTINCT json_extract(payload, '$.model'), COALESCE(json_extract(payload, '$.provider'), '') FROM usage_events WHERE json_valid(payload)")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var model sql.NullString
		var provider string
		if err := rows.Scan(&model, &provider); err != nil {
			rows.Close()
			return nil, err
		}
		name := model.String
		if name == "" {
			name = "unknown"
		}
		models[priceKey(provider, name)] = priceRow{Model: name, Provider: provider}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for key := range v.Overrides {
		parts := strings.SplitN(key, "\n", 2)
		if len(parts) == 2 {
			models[key] = priceRow{Model: parts[1], Provider: parts[0]}
		}
	}
	result := []priceRow{}
	for _, row := range models {
		if p, custom, ok := v.find(row.Provider, row.Model); ok {
			row.Price = &p
			row.Custom = custom
		}
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Model == result[j].Model {
			return result[i].Provider < result[j].Provider
		}
		return result[i].Model < result[j].Model
	})
	var checked float64
	var updateErr string
	if err := db.QueryRow("SELECT checked_at, error FROM price_state WHERE id=1").Scan(&checked, &updateErr); err != nil {
		return nil, err
	}
	if updateErr != "" {
		updateErr = priceUpdateWarning
	}
	return json.Marshal(map[string]any{"models": result, "currency": "USD", "version": v.ID, "updated_at": v.EffectiveAt, "checked_at": checked, "update_error": updateErr, "source": priceURL})
}

func setCustomPrice(body []byte) error {
	var request struct {
		Model    string      `json:"model"`
		Provider string      `json:"provider"`
		Price    *modelPrice `json:"price"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return fmt.Errorf("无效价格 JSON")
	}
	request.Model = strings.TrimSpace(request.Model)
	if request.Model == "" || strings.ContainsAny(request.Model+request.Provider, "\n\r") {
		return fmt.Errorf("请输入有效模型名称")
	}
	if p := request.Price; p != nil {
		if p.Input == nil || p.Output == nil || len(p.Tiers) > 0 {
			return fmt.Errorf("自定义价格必须填写输入、输出单价，不支持自定义阶梯")
		}
		for _, rate := range []*float64{p.Input, p.Output, p.CacheRead, p.CacheWrite} {
			if rate != nil && (*rate < 0 || math.IsNaN(*rate) || math.IsInf(*rate, 0)) {
				return fmt.Errorf("单价必须是非负有限数字")
			}
		}
	}
	ensureDB()
	mu.Lock()
	defer mu.Unlock()
	if db == nil {
		return fmt.Errorf("用量数据库不可用")
	}
	book, err := loadPriceBook(db)
	if err != nil {
		return err
	}
	v, err := book.load(len(book.versions) - 1)
	if err != nil {
		return err
	}
	key := priceKey(request.Provider, request.Model)
	if request.Price == nil {
		delete(v.Overrides, key)
	} else {
		v.Overrides[key] = *request.Price
	}
	return savePriceVersion(db, v.Prices, v.Overrides)
}

var priceRefreshMu sync.Mutex
var pricingUpdatesEnabled = true
var priceHTTPClient = &http.Client{Timeout: 15 * time.Second}

func refreshPrices() error {
	if !priceRefreshMu.TryLock() {
		return fmt.Errorf("价格表正在更新")
	}
	defer priceRefreshMu.Unlock()
	mu.Lock()
	path := lastOpened
	mu.Unlock()
	resp, err := priceHTTPClient.Get(priceURL)
	var raw []byte
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			err = fmt.Errorf("价格源返回 HTTP %d", resp.StatusCode)
		} else {
			raw, err = io.ReadAll(io.LimitReader(resp.Body, 12<<20))
		}
	}
	var prices map[string]modelPrice
	if err == nil {
		prices, err = parseCatalog(raw)
	}
	mu.Lock()
	defer mu.Unlock()
	if db == nil || path != lastOpened {
		return fmt.Errorf("数据库配置已更改，请重新更新")
	}
	if err == nil {
		var book *priceBook
		book, err = loadPriceBook(db)
		if err == nil {
			var v *priceVersion
			v, err = book.load(len(book.versions) - 1)
			if err == nil {
				oldJSON, _ := json.Marshal(v.Prices)
				newJSON, _ := json.Marshal(prices)
				if string(oldJSON) != string(newJSON) {
					err = savePriceVersion(db, prices, v.Overrides)
				}
			}
		}
	}
	message := ""
	if err != nil {
		log.Printf("cpa-usage-ma: price update failed: %v", err)
		message = priceUpdateWarning
	}
	_, stateErr := db.Exec("UPDATE price_state SET checked_at=?, error=? WHERE id=1", float64(time.Now().Unix()), message)
	if err != nil {
		return fmt.Errorf("%s", message)
	}
	return stateErr
}

func maybeRefreshPrices() {
	if !pricingUpdatesEnabled {
		return
	}
	mu.Lock()
	var checked float64
	if db != nil {
		_ = db.QueryRow("SELECT checked_at FROM price_state WHERE id=1").Scan(&checked)
	}
	mu.Unlock()
	if checked > 0 && time.Since(time.Unix(int64(checked), 0)) >= 24*time.Hour {
		go refreshPrices()
	}
}
