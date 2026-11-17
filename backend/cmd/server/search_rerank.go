package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const toolRerankInstruction = "Given a tool search request, retrieve tools whose documented capabilities directly satisfy the request. Related topics alone are not sufficient. Do not assume capabilities absent from the tool description."

type catalogReranker struct {
	endpoint, model, apiKey, format string
	minScore                        float64
	candidates                      int
	client                          *http.Client
}

func newCatalogReranker(client *http.Client) (*catalogReranker, error) {
	endpoint := strings.TrimSpace(env("SEARCH_RERANK_URL", ""))
	if endpoint == "" {
		return nil, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("SEARCH_RERANK_URL must be a full HTTP(S) rerank endpoint")
	}
	format := env("SEARCH_RERANK_FORMAT", "dashscope")
	if format != "dashscope" && format != "openai" {
		return nil, fmt.Errorf("SEARCH_RERANK_FORMAT must be dashscope or openai")
	}
	// This starting cutoff is configurable, not a calibrated probability of relevance.
	minScore, err := strconv.ParseFloat(env("SEARCH_RERANK_MIN_SCORE", "0.55"), 64)
	if err != nil || math.IsNaN(minScore) || minScore < 0 || minScore > 1 {
		return nil, fmt.Errorf("SEARCH_RERANK_MIN_SCORE must be between 0 and 1")
	}
	candidates, err := strconv.Atoi(env("SEARCH_RERANK_CANDIDATES", "30"))
	if err != nil || candidates < 1 || candidates > 100 {
		return nil, fmt.Errorf("SEARCH_RERANK_CANDIDATES must be between 1 and 100")
	}
	return &catalogReranker{endpoint: endpoint, model: env("SEARCH_RERANK_MODEL", "qwen3-rerank"), apiKey: env("SEARCH_RERANK_API_KEY", ""), format: format, minScore: minScore, candidates: candidates, client: client}, nil
}

// Bound bytes rather than guessing a multilingual tokenizer's token count.
// This is deliberately below qwen3-rerank's 4,000-token item limit.
func boundedSearchText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	s = s[:limit]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

type rerankResult struct {
	Index *int     `json:"index"`
	Score *float64 `json:"relevance_score"`
}

func (s *catalogReranker) rank(ctx context.Context, query string, documents []string) ([]rerankResult, error) {
	if len(query) > 3000 {
		return nil, fmt.Errorf("rerank query exceeds input budget")
	}
	payload := map[string]any{"model": s.model}
	if s.format == "dashscope" {
		payload["input"] = map[string]any{"query": query, "documents": documents}
		payload["parameters"] = map[string]any{"top_n": len(documents), "return_documents": false, "instruct": toolRerankInstruction}
	} else {
		payload["query"] = query
		payload["documents"] = documents
		payload["top_n"] = len(documents)
		payload["return_documents"] = false
		payload["instruct"] = toolRerankInstruction
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("cannot encode rerank request")
	}
	req, err := http.NewRequestWithContext(ctx, "POST", s.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("invalid rerank request")
	}
	req.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rerank service unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rerank service HTTP %d", resp.StatusCode)
	}
	var result struct {
		Results []rerankResult `json:"results"`
		Output  struct {
			Results []rerankResult `json:"results"`
		} `json:"output"`
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 || json.Unmarshal(raw, &result) != nil {
		return nil, fmt.Errorf("invalid rerank response")
	}
	rows := result.Results
	if s.format == "dashscope" {
		rows = result.Output.Results
	}
	// Request all candidates: missing, duplicate or invented indices are failures,
	// not evidence that a tool is irrelevant. Do not consume returned document text.
	if len(rows) != len(documents) {
		return nil, fmt.Errorf("incomplete rerank response")
	}
	seen := make(map[int]bool, len(rows))
	for _, row := range rows {
		if row.Index == nil || row.Score == nil || *row.Index < 0 || *row.Index >= len(documents) || seen[*row.Index] || math.IsNaN(*row.Score) || math.IsInf(*row.Score, 0) || *row.Score < 0 || *row.Score > 1 {
			return nil, fmt.Errorf("invalid rerank index or score")
		}
		seen[*row.Index] = true
	}
	return rows, nil
}

func (a *App) rerankCatalog(ctx context.Context, docs []catalogDocument, q string, items []catalogItem, status searchStatus) ([]catalogItem, searchStatus) {
	s := a.search.reranker
	if len(items) > s.candidates {
		items = items[:s.candidates]
	}
	byRef := make(map[string]catalogDocument, len(docs))
	for _, d := range docs {
		byRef[d.item.Ref] = d
	}
	texts := make([]string, 0, len(items))
	budget := 100000 // conservative bound below the 120k-token request limit
	for _, item := range items {
		text := byRef[item.Ref].text
		cost := len(q) + len(text)
		if len(texts) > 0 && cost > budget {
			break
		}
		texts = append(texts, text)
		budget -= cost
	}
	items = items[:len(texts)]
	status.Candidates = len(items)
	// Independent stage timeout; the embedding stage already has a 3s budget.
	rerankCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := s.rank(rerankCtx, q, texts)
	if err != nil {
		status.Degraded = true
		if status.Reason == "" {
			status.Reason = "rerank_unavailable"
		}
		status.Mode = "keyword"
		// Never silently restore unverified semantic candidates on failure.
		fallback, _, _ := rankCatalog(docs, q, nil, nil, 1)
		return fallback, status
	}
	status.Reranked = true
	sort.Slice(rows, func(i, j int) bool {
		if *rows[i].Score == *rows[j].Score {
			return items[*rows[i].Index].Ref < items[*rows[j].Index].Ref
		}
		return *rows[i].Score > *rows[j].Score
	})
	out := make([]catalogItem, 0, len(rows))
	for _, row := range rows {
		if *row.Score < s.minScore {
			status.Filtered++
			continue
		}
		out = append(out, items[*row.Index])
	}
	return out, status
}
