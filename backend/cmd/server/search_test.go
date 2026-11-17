package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Opt-in, read-only diagnosis against an existing catalog. Never creates or updates data.
func TestLiveCatalogSearchDiagnostics(t *testing.T) {
	dbURL := os.Getenv("TEST_CATALOG_DATABASE_URL")
	endpoint := os.Getenv("TEST_EMBEDDING_URL")
	if dbURL == "" || endpoint == "" {
		t.Skip("set TEST_CATALOG_DATABASE_URL and TEST_EMBEDDING_URL to diagnose an existing catalog")
	}
	t.Setenv("SEARCH_EMBEDDING_URL", endpoint)
	s, err := newCatalogSearch()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal("cannot connect to diagnostic database")
	}
	defer db.Close()
	a := &App{db: db, search: s}
	docs, err := a.catalogDocuments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cached, err := a.catalogVectors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	q := strings.ToLower(strings.Join(strings.Fields(env("TEST_CATALOG_QUERY", "网页抓取")), " "))
	vectors, err := s.embed(ctx, []string{q}, true)
	if err != nil {
		t.Fatal(err)
	}
	threshold := s.minScore
	if s.reranker != nil {
		threshold = -1
	}
	for _, d := range docs {
		keyword, exact := keywordScore(d, q)
		v, ok := cached[d.item.Ref]
		if !ok || v.hash != d.hash {
			t.Logf("%s (%s): keyword=%.0f exact=%t vector=missing_or_stale", d.item.Name, d.item.Ref, keyword, exact)
			continue
		}
		similarity, valid := cosine(vectors[0], v.vector)
		t.Logf("%s (%s): keyword=%.0f exact=%t cosine=%.6f valid=%t semantic_hit=%t", d.item.Name, d.item.Ref, keyword, exact, similarity, valid, valid && similarity >= threshold)
	}
	items, indexed, mismatch := rankCatalog(docs, q, vectors[0], cached, threshold)
	t.Logf("query=%q threshold=%.4f indexed=%d total=%d dimension_mismatch=%t", q, threshold, indexed, len(docs), mismatch)
	for i, item := range items {
		t.Logf("rank=%d name=%s ref=%s", i+1, item.Name, item.Ref)
	}
	if s.reranker != nil && len(items) > 0 {
		if len(items) > s.reranker.candidates {
			items = items[:s.reranker.candidates]
		}
		byRef := map[string]string{}
		for _, d := range docs {
			byRef[d.item.Ref] = d.text
		}
		texts := make([]string, len(items))
		for i, item := range items {
			texts[i] = byRef[item.Ref]
		}
		rows, err := s.reranker.rank(ctx, q, texts)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			t.Logf("rerank name=%s score=%.6f cutoff=%.4f filtered=%t", items[*row.Index].Name, *row.Score, s.reranker.minScore, *row.Score < s.reranker.minScore)
		}
	}
}

func TestEmbeddingProtocol(t *testing.T) {
	for _, query := range []bool{true, false} {
		t.Run(map[bool]string{true: "query", false: "passage"}[query], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-key" {
					t.Error("wrong embedding request")
				}
				var request struct {
					Model string   `json:"model"`
					Input []string `json:"input"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil || request.Model != "intfloat/multilingual-e5-small" || len(request.Input) != 2 {
					t.Error("invalid request")
					w.WriteHeader(400)
					return
				}
				prefix := "passage: "
				if query {
					prefix = "query: "
				}
				if request.Input[0] != prefix+"中文" || request.Input[1] != prefix+"English" {
					t.Error("missing E5 prefixes")
				}
				// Responses may be reordered. Restore by index and normalize.
				ioJSON(w, `{"data":[{"index":1,"embedding":[0,2]},{"index":0,"embedding":[3,0]}]}`)
			}))
			defer server.Close()
			s := &catalogSearch{endpoint: server.URL, model: "intfloat/multilingual-e5-small", apiKey: "test-key", client: server.Client()}
			v, err := s.embed(context.Background(), []string{"中文", "English"}, query)
			if err != nil || v[0][0] != 1 || v[0][1] != 0 || v[1][1] != 1 {
				t.Fatalf("vectors %v, error %v", v, err)
			}
		})
	}
}
func ioJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

func TestEmbeddingRejectsInvalidResponses(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"data":[]}`, `{"data":[{"index":0,"embedding":[0,0]},{"index":1,"embedding":[1,0]}]}`,
		`{"data":[{"index":0,"embedding":[1,0]},{"index":0,"embedding":[1,0]}]}`,
		`{"data":[{"index":0,"embedding":[1,0]},{"index":2,"embedding":[1,0]}]}`,
		`{"data":[{"index":0,"embedding":[1,0]},{"index":1,"embedding":[1]}]}`,
		`{"data":[{"index":0,"embedding":[1e999]},{"index":1,"embedding":[1]}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ioJSON(w, body) }))
			defer server.Close()
			s := &catalogSearch{endpoint: server.URL, client: server.Client()}
			if _, err := s.embed(context.Background(), []string{"a", "b"}, true); err == nil {
				t.Fatal("accepted invalid vectors")
			}
		})
	}
	for _, v := range [][]float64{{math.NaN()}, {math.Inf(1)}, {0, 0}, nil} {
		if normalizeVector(v) {
			t.Fatal("accepted nonfinite/empty vector")
		}
	}
}

func TestEmbeddingCancellationAndRedirect(t *testing.T) {
	var redirected atomic.Bool
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Store(true) }))
	defer sink.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	t.Setenv("SEARCH_EMBEDDING_URL", source.URL)
	s, err := newCatalogSearch()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.embed(context.Background(), []string{"q"}, true); err == nil || redirected.Load() {
		t.Fatal("followed embedding redirect")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.embed(ctx, []string{"q"}, true); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestSearchConfiguration(t *testing.T) {
	t.Setenv("SEARCH_EMBEDDING_URL", "")
	if s, err := newCatalogSearch(); err != nil || s != nil {
		t.Fatal("optional search should be disabled")
	}
	for _, u := range []string{"localhost:7860", "file:///tmp/model", "http://user:password@localhost", "http://localhost?q=secret"} {
		t.Setenv("SEARCH_EMBEDDING_URL", u)
		if _, err := newCatalogSearch(); err == nil {
			t.Fatalf("accepted %s", u)
		}
	}
	t.Setenv("SEARCH_EMBEDDING_URL", "http://localhost:7860/v1/embeddings")
	t.Setenv("SEARCH_MIN_SCORE", "NaN")
	if _, err := newCatalogSearch(); err == nil {
		t.Fatal("accepted NaN")
	}
	t.Setenv("SEARCH_MIN_SCORE", "0.75")
	a, err := newCatalogSearch()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SEARCH_EMBEDDING_REVISION", "changed")
	b, err := newCatalogSearch()
	if err != nil || a.key == b.key {
		t.Fatal("model revision did not isolate cache")
	}
}

func searchTestDocuments() []catalogDocument {
	provider := Tool{ID: 12, Slug: "fixture", Name: "Fixture"}
	return []catalogDocument{
		makeCatalogDocument(provider, Endpoint{ID: "web", Name: "Web search", Description: "Search public web pages for addresses and opening hours."}),
		makeCatalogDocument(provider, Endpoint{ID: "calculator", Name: "Calculator", Description: "Calculate arithmetic expressions."}),
		makeCatalogDocument(provider, Endpoint{ID: "location", Name: "Device location", Description: "Get device GPS coordinates."}),
	}
}

func TestHybridRankingAndStaleVectors(t *testing.T) {
	docs := searchTestDocuments()
	cached := map[string]catalogVector{
		docs[0].item.Ref: {docs[0].hash, []float64{1, 0}},
		docs[1].item.Ref: {docs[1].hash, []float64{0, 1}},
		docs[2].item.Ref: {docs[2].hash, []float64{0, 1}},
	}
	items, indexed, mismatch := rankCatalog(docs, "查询机构地址", []float64{1, 0}, cached, 0.75)
	if len(items) != 1 || items[0].Ref != docs[0].item.Ref || indexed != 3 || mismatch {
		t.Fatalf("cross-language semantic candidate missing: %+v", items)
	}
	// An exact name remains first even when another tool is more similar.
	items, _, _ = rankCatalog(docs, "calculator", []float64{1, 0}, cached, 0.75)
	if len(items) != 2 || items[0].Name != "Calculator" {
		t.Fatal("exact name lost priority")
	}
	docs[0].hash = "changed"
	items, indexed, _ = rankCatalog(docs, "查询机构地址", []float64{1, 0}, cached, 0.75)
	if len(items) != 0 || indexed != 2 {
		t.Fatal("stale vector used")
	}
	items, _, _ = rankCatalog(docs, "web search", nil, cached, 0.75)
	if len(items) == 0 || items[0].Name != "Web search" {
		t.Fatal("stale vector removed lexical result")
	}
	cached[docs[1].item.Ref] = catalogVector{docs[1].hash, []float64{1, 0, 0}}
	_, _, mismatch = rankCatalog(docs, "查询机构地址", []float64{1, 0}, cached, 0.75)
	if !mismatch {
		t.Fatal("dimension mismatch not detected")
	}
}

func TestSearchMetadataAndMCPMerge(t *testing.T) {
	ep := Endpoint{ID: "web", MCPName: "web", Name: "Web", Description: "Original English description", SearchDescription: "公开地址查询"}
	provider := Tool{ID: 1, Description: "配置示例，需核实上游 API 后启用。", Endpoints: []Endpoint{ep}}
	before := makeCatalogDocument(provider, ep)
	if before.item.Description != ep.Description {
		t.Fatalf("catalog description includes provider prose: %q", before.item.Description)
	}
	if score, _ := keywordScore(before, "配置示例"); score == 0 {
		t.Fatal("provider description not searchable")
	}
	if fallback := makeCatalogDocument(provider, Endpoint{ID: "empty", Description: " \n "}); fallback.item.Description != provider.Description {
		t.Fatalf("missing provider description fallback: %q", fallback.item.Description)
	}
	if score, _ := keywordScore(before, "地址查询"); score == 0 {
		t.Fatal("supplement not searchable")
	}
	merged, _ := mergeMCP([]Endpoint{ep}, []Endpoint{{ID: "web", MCPName: "web", Name: "Web", Description: "Updated remote text"}})
	if merged[0].Description != "Updated remote text" || merged[0].SearchDescription != ep.SearchDescription {
		t.Fatal("sync lost local description")
	}
	if before.hash == makeCatalogDocument(provider, merged[0]).hash {
		t.Fatal("remote update did not invalidate embedding")
	}
	text := " Updated supplement "
	out, err := applyBulk(provider, BulkMCPRequest{IDs: []string{"web"}, SearchDescription: &text})
	if err != nil || out[0].SearchDescription != "Updated supplement" || out[0].Description != ep.Description {
		t.Fatal("supplement edit overwrote upstream metadata")
	}
	text = ""
	out, err = applyBulk(provider, BulkMCPRequest{IDs: []string{"web"}, SearchDescription: &text})
	if err != nil || out[0].SearchDescription != "" {
		t.Fatal("cannot clear supplement")
	}
	if _, err = applyBulk(provider, BulkMCPRequest{IDs: []string{"web", "other"}, SearchDescription: &text}); err == nil {
		t.Fatal("accepted multi-endpoint description edit")
	}
	text = strings.Repeat("x", 6001)
	if _, err = applyBulk(provider, BulkMCPRequest{IDs: []string{"web"}, SearchDescription: &text}); err == nil {
		t.Fatal("accepted oversized supplement")
	}
}

func TestSearchPagination(t *testing.T) {
	items := []catalogItem{{Ref: "t3.YQ", Name: "most relevant", InputSchema: json.RawMessage(`{}`)}, {Ref: "t1.YQ"}, {Ref: "t2.YQ"}}
	page, cursor, err := catalogPage(items, "query", "", 1)
	if err != nil || page[0].Ref != "t3.YQ" || cursor == "" || page[0].InputSchema != nil {
		t.Fatal("relevance pagination failed")
	}
	page, next, err := catalogPage(items, "query", cursor, 1)
	if err != nil || page[0].Ref != "t1.YQ" || next == "" {
		t.Fatal("second page failed")
	}
	for _, bad := range []string{"garbage", "t3.YQ", cursor + "bad"} {
		if _, _, err = catalogPage(items, "query", bad, 1); err == nil {
			t.Fatal("invalid cursor accepted")
		}
	}
	if _, _, err = catalogPage(items, "different", cursor, 1); err == nil {
		t.Fatal("cursor reused with another query")
	}
	changed := append([]catalogItem{}, items...)
	changed[0], changed[1] = changed[1], changed[0]
	if _, _, err = catalogPage(changed, "query", cursor, 1); err == nil {
		t.Fatal("changed ranking accepted")
	}
	page, cursor, err = catalogPage(items, "", "", 1)
	if err != nil || page[0].Ref != "t1.YQ" || cursor != "t1.YQ" {
		t.Fatal("directory compatibility broken")
	}
	page, _, err = catalogPage(items, "", cursor, 50)
	if err != nil || len(page) != 2 {
		t.Fatal("directory next page failed")
	}
	page, next, err = catalogPage(nil, "query", "", 10)
	if err != nil || len(page) != 0 || next != "" {
		t.Fatal("empty page failed")
	}
}

func TestKeywordSearchWithoutModel(t *testing.T) {
	a := &App{}
	items, status := a.searchCatalog(context.Background(), searchTestDocuments(), "web")
	if len(items) != 1 || status.Mode != "keyword" || status.Degraded {
		t.Fatalf("keyword fallback: %+v %+v", items, status)
	}
}

func TestLiveEmbedding(t *testing.T) {
	endpoint := os.Getenv("TEST_EMBEDDING_URL")
	if endpoint == "" {
		t.Skip("set TEST_EMBEDDING_URL for a real E5 service")
	}
	t.Setenv("SEARCH_EMBEDDING_URL", endpoint)
	s, err := newCatalogSearch()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	docs := searchTestDocuments()
	texts := make([]string, len(docs))
	for i, d := range docs {
		texts[i] = d.text
	}
	vectors, err := s.embed(ctx, texts, false)
	if err != nil {
		t.Fatal(err)
	}
	queries, err := s.embed(ctx, []string{"网页搜索", "计算加减乘除"}, true)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{0, 1} {
		best := -1
		score := -2.0
		for j, v := range vectors {
			similarity, ok := cosine(queries[i], v)
			if !ok {
				t.Fatal("inconsistent dimensions")
			}
			t.Logf("query %d -> %s: %.4f", i, docs[j].item.Name, similarity)
			if similarity > score {
				score = similarity
				best = j
			}
		}
		if best != want {
			t.Fatalf("query %d best match %d, want %d", i, best, want)
		}
	}
}

// Called by the existing opt-in integration suite, which owns an empty test DB.
func verifyCatalogSearchIntegration(t *testing.T, a *App) {
	t.Helper()
	ctx := context.Background()
	var requests atomic.Int64
	var unavailable atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if unavailable.Load() {
			w.WriteHeader(503)
			return
		}
		var req struct {
			Input []string `json:"input"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			w.WriteHeader(400)
			return
		}
		data := []map[string]any{}
		for i, text := range req.Input {
			vector := []float64{0, 1}
			if strings.HasPrefix(text, "query: ") || strings.Contains(text, "Search public web") {
				vector = []float64{1, 0}
			}
			data = append(data, map[string]any{"index": i, "embedding": vector})
		}
		writeJSON(w, 200, map[string]any{"data": data})
	}))
	defer server.Close()
	a.search = &catalogSearch{endpoint: server.URL, model: "intfloat/multilingual-e5-small", key: "integration-search", minScore: 0.75, client: server.Client()}
	defer func() { a.search = nil }()
	endpoints := []Endpoint{
		{ID: "web", Name: "Web Search", Description: "Search public web pages for company addresses", SearchDescription: "查询机构公开地址"},
		{ID: "disabled", Name: "Disabled", Description: "Search public web", Disabled: true},
		{ID: "missing", Name: "Missing", Description: "Search public web", Missing: true},
	}
	raw, _ := json.Marshal(endpoints)
	var id int64
	if err := a.db.QueryRow(ctx, `INSERT INTO tools(slug,name,category,enabled,endpoints) VALUES('search-fixture','Search Fixture','Testing',true,$1) RETURNING id`, raw).Scan(&id); err != nil {
		t.Fatal(err)
	}
	defer a.db.Exec(ctx, `DELETE FROM tools WHERE id=$1`, id)
	get := func(q string) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		// Set Query explicitly so Chinese and whitespace are correctly escaped.
		r := httptest.NewRequest("GET", "/v1/catalog", nil)
		values := r.URL.Query()
		values.Set("q", q)
		values.Set("limit", "1")
		r.URL.RawQuery = values.Encode()
		a.catalog(w, r, User{})
		if w.Code != 200 {
			t.Fatalf("catalog %d: %s", w.Code, w.Body.String())
		}
		var result struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Data
	}
	result := get("位置查询")
	if result["search"].(map[string]any)["reason"] != "index_pending" {
		t.Fatal("missing cold-start state")
	}
	if err := a.indexCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	calls := requests.Load()
	if err := a.indexCatalog(ctx); err != nil || requests.Load() != calls {
		t.Fatal("unchanged catalog was re-embedded")
	}
	result = get("位置查询")
	hits := result["items"].([]any)
	if len(hits) != 1 || hits[0].(map[string]any)["tool_ref"] != toolRef(id, "web") || result["search"].(map[string]any)["degraded"] != false {
		t.Fatalf("cross-language lookup: %+v", result)
	}
	vectors, err := a.catalogVectors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := vectors[toolRef(id, "disabled")]; ok {
		t.Fatal("disabled endpoint indexed")
	}
	if _, ok := vectors[toolRef(id, "missing")]; ok {
		t.Fatal("missing endpoint indexed")
	}
	unavailable.Store(true)
	result = get("Web Search")
	if len(result["items"].([]any)) != 1 || result["search"].(map[string]any)["reason"] != "embedding_unavailable" {
		t.Fatalf("failed degradation: %+v", result)
	}
	unavailable.Store(false)
	// A description edit immediately invalidates the previous vector.
	endpoints[0].Description += " Updated description"
	raw, _ = json.Marshal(endpoints)
	if _, err = a.db.Exec(ctx, `UPDATE tools SET endpoints=$2 WHERE id=$1`, id, raw); err != nil {
		t.Fatal(err)
	}
	result = get("位置查询")
	if len(result["items"].([]any)) != 0 || result["search"].(map[string]any)["degraded"] != true {
		t.Fatal("stale vector used after edit")
	}
	if err = a.indexCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	if len(get("位置查询")["items"].([]any)) != 1 {
		t.Fatal("updated vector not searchable")
	}
	// A new process/configuration can reuse persisted vectors without an embed.
	oldKey := a.search.key
	a.search.key = "new-model-revision"
	if get("位置查询")["search"].(map[string]any)["reason"] != "index_pending" {
		t.Fatal("model revisions shared vectors")
	}
	a.search.key = oldKey
	calls = requests.Load()
	if err = a.indexCatalog(ctx); err != nil || requests.Load() != calls {
		t.Fatal("persisted vectors were not reused")
	}
	if _, err = a.db.Exec(ctx, `UPDATE tools SET enabled=false WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if len(get("位置查询")["items"].([]any)) != 0 {
		t.Fatal("disabled provider appeared via cached vector")
	}
	// Exercise the full rerank/cache path against persisted vectors in this dedicated test DB.
	if _, err = a.db.Exec(ctx, `UPDATE tools SET enabled=true WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	var rerankRequests atomic.Int64
	var rerankUnavailable atomic.Bool
	var noRelevantTools atomic.Bool
	reranker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rerankRequests.Add(1)
		if rerankUnavailable.Load() {
			w.WriteHeader(503)
			return
		}
		var request struct {
			Documents []string `json:"documents"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("invalid rerank request")
		}
		rows := make([]map[string]any, len(request.Documents))
		for i, text := range request.Documents {
			score := 0.01
			if strings.Contains(text, "Search public web") && !noRelevantTools.Load() {
				score = 0.9
			}
			rows[i] = map[string]any{"index": i, "relevance_score": score}
		}
		writeJSON(w, 200, map[string]any{"results": rows})
	}))
	defer reranker.Close()
	a.search.reranker = &catalogReranker{endpoint: reranker.URL, format: "openai", minScore: 0.1, candidates: 30, client: reranker.Client()}
	result = get("位置查询")
	if len(result["items"].([]any)) != 1 || result["search"].(map[string]any)["reranked"] != true {
		t.Fatalf("rerank not applied: %+v", result)
	}
	calls = requests.Load()
	if len(get("位置查询")["items"].([]any)) != 1 || requests.Load() != calls || rerankRequests.Load() != 1 {
		t.Fatal("repeated search did not reuse inference")
	}
	if _, err = a.db.Exec(ctx, `UPDATE tools SET endpoints=jsonb_set(endpoints, '{0,price_micros}', '1234') WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	result = get("位置查询")
	if rerankRequests.Load() != 2 || result["items"].([]any)[0].(map[string]any)["price_micros"] != float64(1234) {
		t.Fatalf("cache returned stale prices: %+v", result)
	}
	rerankUnavailable.Store(true)
	result = get("地图")
	if len(result["items"].([]any)) != 0 || result["search"].(map[string]any)["reason"] != "rerank_unavailable" {
		t.Fatalf("unsafe rerank fallback: %+v", result)
	}
	rerankUnavailable.Store(false)
	if get("地图")["search"].(map[string]any)["reranked"] != true {
		t.Fatal("cached degraded results")
	}
	a.searchFallbackRef = toolRef(id, "web")
	defer func() { a.searchFallbackRef = "" }()
	noRelevantTools.Store(true)
	result = get("不存在的专用能力")
	suggested := result["suggested_tools"].([]any)
	if len(result["items"].([]any)) != 0 || len(suggested) != 1 || suggested[0].(map[string]any)["tool_ref"] != a.searchFallbackRef || suggested[0].(map[string]any)["reason"] != "no_relevant_tool" {
		t.Fatalf("fallback mixed with relevance matches: %+v", result)
	}
	// Reuse cached empty results, but still check the current catalog for suggestions.
	calls = rerankRequests.Load()
	if len(get("不存在的专用能力")["suggested_tools"].([]any)) != 1 || rerankRequests.Load() != calls {
		t.Fatal("cached no-match lost suggestions")
	}
	if len(get("位置查询")["suggested_tools"].([]any)) != 0 {
		t.Fatal("fallback shown with matched results")
	}
	rerankUnavailable.Store(true)
	if len(get("模型服务故障")["suggested_tools"].([]any)) != 0 {
		t.Fatal("degraded search advertised no-match fallback")
	}
	rerankUnavailable.Store(false)
	if _, err = a.db.Exec(ctx, `UPDATE tools SET endpoints=jsonb_set(endpoints, '{0,disabled}', 'true') WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if len(get("不存在的专用能力")["suggested_tools"].([]any)) != 0 {
		t.Fatal("disabled endpoint suggested")
	}
	if _, err = a.db.Exec(ctx, `UPDATE tools SET enabled=false WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	result = get("位置查询")
	if len(result["items"].([]any)) != 0 || len(result["suggested_tools"].([]any)) != 0 {
		t.Fatal("cache exposed disabled endpoint or provider")
	}
}

func TestLiveToolDiscovery(t *testing.T) {
	endpoint := os.Getenv("TEST_EMBEDDING_URL")
	if endpoint == "" {
		t.Skip("set TEST_EMBEDDING_URL to evaluate cross-language tool discovery")
	}
	t.Setenv("SEARCH_EMBEDDING_URL", endpoint)
	s, err := newCatalogSearch()
	if err != nil {
		t.Fatal(err)
	}
	provider := Tool{ID: 99, Name: "Discovery evaluation", Slug: "evaluation"}
	endpoints := []Endpoint{
		{ID: "web", Name: "Web Search", Description: "Search public web pages to find company addresses, place information and opening hours.", SearchDescription: "搜索公开网页，可查机构地址、地点介绍和营业时间。不读取设备当前位置，不提供专门的路线规划。"},
		{ID: "fetch", Name: "Fetch Web Page", Description: "Extract text and content from a specified web page URL."},
		{ID: "calculator", Name: "Calculator", Description: "Calculate arithmetic expressions, totals and percentages."},
		{ID: "email", Name: "Send Email", Description: "Send an email message to a recipient address."},
		{ID: "ocr", Name: "Image OCR", Description: "Extract printed text from an image."},
		{ID: "image", Name: "Image Generation", Description: "Generate images and illustrations from a text prompt."},
		{ID: "translate", Name: "Translation", Description: "Translate text between languages."},
		{ID: "weather", Name: "Weather Forecast", Description: "Get current weather conditions and forecasts for a city."},
		{ID: "exchange", Name: "Currency Exchange", Description: "Look up exchange rates between currencies."},
		{ID: "repository", Name: "Repository Search", Description: "Find source code repositories by name or programming language."},
	}
	docs := make([]catalogDocument, len(endpoints))
	texts := make([]string, len(endpoints))
	for i, ep := range endpoints {
		docs[i] = makeCatalogDocument(provider, ep)
		texts[i] = docs[i].text
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	vectors, err := s.embed(ctx, texts, false)
	if err != nil {
		t.Fatal(err)
	}
	cache := map[string]catalogVector{}
	for i, d := range docs {
		cache[d.item.Ref] = catalogVector{d.hash, vectors[i]}
	}
	cases := []struct{ query, want string }{{"位置查询", "web"}, {"地图", "web"}, {"地址查询", "web"}, {"计算预算总金额", "calculator"}, {"提取图片文字", "ocr"}, {"给客户发送邮件", "email"}}
	queries := make([]string, len(cases))
	for i, c := range cases {
		queries[i] = c.query
	}
	qv, err := s.embed(ctx, queries, true)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		hits, _, _ := rankCatalog(docs, c.query, qv[i], cache, s.minScore)
		found := false
		names := []string{}
		for rank, hit := range hits {
			if rank >= 5 {
				break
			}
			names = append(names, hit.Name)
			if hit.Ref == toolRef(provider.ID, c.want) {
				found = true
			}
		}
		t.Logf("%s -> %v", c.query, names)
		if !found {
			t.Errorf("expected %s in top five for %s", c.want, c.query)
		}
	}
}
