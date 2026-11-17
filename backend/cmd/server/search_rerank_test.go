package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestBailianEmbeddingProtocol(t *testing.T) {
	for _, query := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Model      string   `json:"model"`
				Input      []string `json:"input"`
				Dimensions int      `json:"dimensions"`
				Encoding   string   `json:"encoding_format"`
			}
			if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer embedding-secret" || json.NewDecoder(r.Body).Decode(&req) != nil {
				t.Error("invalid embedding request or credentials")
			}
			if req.Model != "qwen3.7-text-embedding" || len(req.Input) != 1 || req.Input[0] != "地图" || (req.Dimensions != 2 && req.Dimensions != 3) || req.Encoding != "float" {
				t.Errorf("wrong model preprocessing: %+v", req)
			}
			ioJSON(w, `{"data":[{"index":0,"embedding":[3,4]}]}`)
		}))
		s := &catalogSearch{endpoint: server.URL, model: "qwen3.7-text-embedding", format: "openai", dimensions: 2, apiKey: "embedding-secret", client: server.Client()}
		v, err := s.embed(context.Background(), []string{"地图"}, query)
		if err != nil || len(v) != 1 || v[0][0] != 0.6 || v[0][1] != 0.8 {
			t.Fatalf("embedding failed: %v %v", v, err)
		}
		s.dimensions = 3
		if _, err := s.embed(context.Background(), []string{"地图"}, query); err == nil {
			t.Fatal("accepted unexpected vector dimensions")
		}
		server.Close()
	}
}

func TestRerankProtocols(t *testing.T) {
	for _, format := range []string{"dashscope", "openai"} {
		t.Run(format, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req map[string]json.RawMessage
				if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer rerank-secret" || json.NewDecoder(r.Body).Decode(&req) != nil {
					t.Error("invalid rerank request")
					w.WriteHeader(400)
					return
				}
				input, params := req, req
				if format == "dashscope" {
					if json.Unmarshal(req["input"], &input) != nil || json.Unmarshal(req["parameters"], &params) != nil {
						t.Error("invalid DashScope nesting")
						w.WriteHeader(400)
						return
					}
				}
				var q, instruction, model string
				var texts []string
				var topN int
				json.Unmarshal(input["query"], &q)
				json.Unmarshal(input["documents"], &texts)
				json.Unmarshal(params["instruct"], &instruction)
				json.Unmarshal(params["top_n"], &topN)
				json.Unmarshal(req["model"], &model)
				if q != "地图" || len(texts) != 2 || topN != 2 || instruction != toolRerankInstruction || model != "qwen3-rerank" {
					t.Errorf("wrong rerank payload: %s", req)
				}
				rows := `[{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.01}]`
				if format == "dashscope" {
					ioJSON(w, `{"output":{"results":`+rows+`}}`)
				} else {
					ioJSON(w, `{"results":`+rows+`}`)
				}
			}))
			defer server.Close()
			s := &catalogReranker{endpoint: server.URL, model: "qwen3-rerank", apiKey: "rerank-secret", format: format, client: server.Client()}
			rows, err := s.rank(context.Background(), "地图", []string{"查询天气", "地图服务"})
			if err != nil || len(rows) != 2 || *rows[0].Index != 1 {
				t.Fatalf("rerank failed: %v %v", rows, err)
			}
		})
	}
}

func TestRerankRejectsInvalidResponses(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"results":[]}`, `{"results":[{"index":0,"relevance_score":0.8}]}`,
		`{"results":[{"index":0,"relevance_score":0.8},{"index":0,"relevance_score":0.4}]}`,
		`{"results":[{"index":-1,"relevance_score":0.8},{"index":1,"relevance_score":0.4}]}`,
		`{"results":[{"index":0,"relevance_score":0.8},{"index":2,"relevance_score":0.4}]}`,
		`{"results":[{"index":0,"relevance_score":1.1},{"index":1,"relevance_score":0.4}]}`,
		`{"results":[{"index":0},{"index":1,"relevance_score":0.4}]}`,
		`{"results":[{"relevance_score":0.8},{"index":1,"relevance_score":0.4}]}`,
		`{"results":[{"index":0,"relevance_score":null},{"index":1,"relevance_score":0.4}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ioJSON(w, body) }))
			defer server.Close()
			s := &catalogReranker{endpoint: server.URL, format: "openai", client: server.Client()}
			if _, err := s.rank(context.Background(), "地图", []string{"天气", "地图"}); err == nil {
				t.Fatal("accepted corrupt or incomplete results")
			}
		})
	}
}

func TestRerankFiltersFalseSemanticHit(t *testing.T) {
	provider := Tool{ID: 1, Name: "Tools"}
	docs := []catalogDocument{
		makeCatalogDocument(provider, Endpoint{ID: "weather", Name: "Query Weather", Description: "查询城市天气"}),
		makeCatalogDocument(provider, Endpoint{ID: "maps", Name: "Maps", Description: "Display maps and plan routes"}),
	}
	cache := map[string]catalogVector{
		docs[0].item.Ref: {docs[0].hash, []float64{0.9, 0.435889894}},
		docs[1].item.Ref: {docs[1].hash, []float64{0.6, 0.8}},
	}
	// The right tool is below the old cosine cutoff. Broad recall keeps it.
	candidates, _, _ := rankCatalog(docs, "地图", []float64{1, 0}, cache, -1)
	if len(candidates) != 2 || candidates[0].Name != "Query Weather" {
		t.Fatal("fixture does not reproduce vector misranking")
	}
	body := `{"results":[{"index":0,"relevance_score":0.01},{"index":1,"relevance_score":0.9}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ioJSON(w, body) }))
	defer server.Close()
	a := &App{search: &catalogSearch{reranker: &catalogReranker{endpoint: server.URL, format: "openai", minScore: 0.1, candidates: 30, client: server.Client()}}}
	items, status := a.rerankCatalog(context.Background(), docs, "地图", candidates, searchStatus{Mode: "hybrid"})
	if len(items) != 1 || items[0].Name != "Maps" || !status.Reranked || status.Filtered != 1 || status.Degraded {
		t.Fatalf("wrong tool survived: %+v %+v", items, status)
	}
	body = `{"results":[{"index":0,"relevance_score":0.01},{"index":1,"relevance_score":0.02}]}`
	items, status = a.rerankCatalog(context.Background(), docs, "订机票", candidates, searchStatus{Mode: "hybrid"})
	if len(items) != 0 || !status.Reranked || status.Filtered != 2 || status.Degraded {
		t.Fatalf("no-match query should be empty: %+v %+v", items, status)
	}
	// An HTTP-200 error envelope must degrade to keywords, not restore weather.
	body = `{"code":"InvalidApiKey","message":"secret upstream text"}`
	items, status = a.rerankCatalog(context.Background(), docs, "maps", candidates, searchStatus{Mode: "hybrid"})
	if len(items) != 1 || items[0].Name != "Maps" || status.Reranked || !status.Degraded || status.Mode != "keyword" || status.Reason != "rerank_unavailable" {
		t.Fatalf("unsafe semantic fallback: %+v %+v", items, status)
	}
}

func TestCapabilityDocumentsAndExactLookup(t *testing.T) {
	provider := Tool{ID: 1, Name: "Maps and Weather", Description: "地图、天气、邮件等所有能力"}
	d := makeCatalogDocument(provider, Endpoint{ID: "weather", Name: "Weather", Description: strings.Repeat("查询天气", 2000)})
	if len(d.text) > 3000 || !utf8.ValidString(d.text) || strings.Contains(d.text, "地图") {
		t.Fatal("semantic document is unbounded or polluted by provider prose")
	}
	if score, _ := keywordScore(d, "地图"); score == 0 {
		t.Fatal("provider keyword lookup compatibility lost")
	}
	// No database or inference clients are present; ID lookups must bypass both.
	a := &App{search: &catalogSearch{reranker: &catalogReranker{}}}
	for _, q := range []string{strings.ToLower(d.item.Ref), d.endpointID} {
		items, status := a.searchCatalog(context.Background(), []catalogDocument{d}, q)
		if len(items) != 1 || items[0].Ref != d.item.Ref || status.Degraded || status.Reranked {
			t.Fatalf("identifier lookup invoked inference: %+v %+v", items, status)
		}
	}
}

func TestBailianSearchConfiguration(t *testing.T) {
	t.Setenv("SEARCH_EMBEDDING_URL", "https://example.com/embeddings")
	t.Setenv("SEARCH_EMBEDDING_MODEL", "qwen3.7-text-embedding")
	t.Setenv("SEARCH_EMBEDDING_FORMAT", "auto")
	t.Setenv("SEARCH_EMBEDDING_DIMENSIONS", "1024")
	t.Setenv("SEARCH_RERANK_URL", "https://example.com/reranks")
	t.Setenv("SEARCH_RERANK_FORMAT", "openai")
	s, err := newCatalogSearch()
	if err != nil || s.format != "openai" || s.dimensions != 1024 || s.reranker == nil || s.reranker.model != "qwen3-rerank" {
		t.Fatalf("Bailian configuration failed: %+v %v", s, err)
	}
	key := s.key
	t.Setenv("SEARCH_EMBEDDING_DIMENSIONS", "512")
	s, err = newCatalogSearch()
	if err != nil || s.key == key {
		t.Fatal("dimension change did not isolate vector cache")
	}
	for name, value := range map[string]string{
		"SEARCH_RERANK_URL":           "http://user:secret@example.com/rerank",
		"SEARCH_RERANK_FORMAT":        "invalid",
		"SEARCH_RERANK_MIN_SCORE":     "NaN",
		"SEARCH_RERANK_CANDIDATES":    "101",
		"SEARCH_EMBEDDING_DIMENSIONS": "invalid",
		"SEARCH_EMBEDDING_FORMAT":     "invalid",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, value)
			if _, err := newCatalogSearch(); err == nil {
				t.Fatalf("accepted invalid %s", name)
			}
		})
	}
	t.Setenv("SEARCH_EMBEDDING_URL", "")
	if _, err := newCatalogSearch(); err == nil {
		t.Fatal("accepted orphan rerank configuration")
	}
}

func TestRerankTransportFailures(t *testing.T) {
	var targetCalls int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++ }))
	defer target.Close()
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusTemporaryRedirect} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", target.URL)
			w.WriteHeader(status)
		}))
		t.Setenv("SEARCH_RERANK_URL", server.URL)
		s, err := newCatalogReranker(&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.rank(context.Background(), "地图", []string{"天气"}); err == nil {
			t.Fatalf("accepted HTTP %d", status)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err = s.rank(ctx, "地图", []string{"天气"}); err == nil {
			t.Fatal("ignored cancelled request")
		}
		server.Close()
	}
	if targetCalls != 0 {
		t.Fatal("followed reranker redirect")
	}
}

func TestRerankRequestBudget(t *testing.T) {
	provider := Tool{ID: 1}
	docs := make([]catalogDocument, 100)
	items := make([]catalogItem, 100)
	for i := range docs {
		docs[i] = makeCatalogDocument(provider, Endpoint{ID: strings.Repeat("x", i+1), Description: strings.Repeat("文", 2000)})
		items[i] = docs[i].item
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string   `json:"query"`
			Documents []string `json:"documents"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			t.Error("invalid request")
		}
		budget := 0
		rows := make([]map[string]any, len(req.Documents))
		for i, text := range req.Documents {
			budget += len(req.Query) + len(text)
			rows[i] = map[string]any{"index": i, "relevance_score": 0.01}
		}
		if len(req.Documents) == 0 || len(req.Documents) >= 100 || budget > 100000 {
			t.Errorf("unbounded rerank request: docs=%d bytes=%d", len(req.Documents), budget)
		}
		writeJSON(w, 200, map[string]any{"results": rows})
	}))
	defer server.Close()
	a := &App{search: &catalogSearch{reranker: &catalogReranker{endpoint: server.URL, format: "openai", candidates: 100, minScore: 0.1, client: server.Client()}}}
	out, status := a.rerankCatalog(context.Background(), docs, strings.Repeat("q", 3000), items, searchStatus{Mode: "hybrid"})
	if len(out) != 0 || !status.Reranked || status.Candidates >= 100 {
		t.Fatalf("request budget not respected: %+v", status)
	}
}

// Opt-in: uses only synthetic tool descriptions, never updates the live catalog.
func TestLiveBailianRetrieval(t *testing.T) {
	embeddingURL, rerankURL := os.Getenv("TEST_EMBEDDING_URL"), os.Getenv("TEST_RERANK_URL")
	if embeddingURL == "" || rerankURL == "" {
		t.Skip("set TEST_EMBEDDING_URL and TEST_RERANK_URL to verify paid model retrieval")
	}
	t.Setenv("SEARCH_EMBEDDING_URL", embeddingURL)
	t.Setenv("SEARCH_RERANK_URL", rerankURL)
	s, err := newCatalogSearch()
	if err != nil {
		t.Fatal(err)
	}
	provider := Tool{ID: 1, Name: "Live fixtures"}
	endpoints := []Endpoint{
		{ID: "web", Name: "Search Web", Description: "Search public web pages and current facts using keywords. Does not provide map display, route planning, or device location."},
		{ID: "fetch", Name: "Fetch Web Content", Description: "Extract the main text from a specified web page URL. A URL is required."},
		{ID: "weather", Name: "Query Weather", Description: "查询全球城市的实时天气和未来几天预报。city 城市名中英文均可，days 预报天数 1~7。"},
	}
	docs := make([]catalogDocument, len(endpoints))
	texts := make([]string, len(docs))
	for i, ep := range endpoints {
		docs[i] = makeCatalogDocument(provider, ep)
		texts[i] = docs[i].text
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	vectors, err := s.embed(ctx, texts, false)
	if err != nil {
		t.Fatal(err)
	}
	cache := map[string]catalogVector{}
	for i, d := range docs {
		cache[d.item.Ref] = catalogVector{d.hash, vectors[i]}
	}
	cases := []struct{ query, want string }{
		{"天气", "weather"}, {"weather", "weather"}, {"搜索网页", "web"},
		{"抓取指定网页 URL 的正文", "fetch"}, {"地图", ""}, {"地图路线规划", ""}, {"预订飞机票", ""}, {"获取设备当前位置", ""},
	}
	a := &App{search: s}
	for _, c := range cases {
		qv, err := s.embed(ctx, []string{c.query}, true)
		if err != nil {
			t.Fatal(err)
		}
		hits, _, _ := rankCatalog(docs, c.query, qv[0], cache, -1)
		hits, status := a.rerankCatalog(ctx, docs, c.query, hits, searchStatus{Mode: "hybrid"})
		names := make([]string, len(hits))
		for i, hit := range hits {
			names[i] = hit.Name
		}
		t.Logf("%q -> %v (reranked=%t filtered=%d)", c.query, names, status.Reranked, status.Filtered)
		if status.Degraded || !status.Reranked {
			t.Fatalf("model service failed: %+v", status)
		}
		if c.want == "" {
			if len(hits) != 0 {
				t.Errorf("expected no suitable tool for %q", c.query)
			}
		} else if len(hits) == 0 || hits[0].Ref != toolRef(provider.ID, c.want) {
			t.Errorf("expected %s first for %q", c.want, c.query)
		}
	}
}
