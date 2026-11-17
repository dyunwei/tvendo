package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Opt-in diagnosis: fresh vectors stay in memory; no live catalog/index writes.
func TestLiveCatalogRerankScores(t *testing.T) {
	dbURL := os.Getenv("TEST_CATALOG_DATABASE_URL")
	embeddingURL, rerankURL := os.Getenv("TEST_EMBEDDING_URL"), os.Getenv("TEST_RERANK_URL")
	if dbURL == "" || embeddingURL == "" || rerankURL == "" {
		t.Skip("set TEST_CATALOG_DATABASE_URL, TEST_EMBEDDING_URL and TEST_RERANK_URL for read-only live scores")
	}
	t.Setenv("SEARCH_EMBEDDING_URL", embeddingURL)
	t.Setenv("SEARCH_RERANK_URL", rerankURL)
	s, err := newCatalogSearch()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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
	cache := map[string]catalogVector{}
	byRef := map[string]catalogDocument{}
	for start := 0; start < len(docs); start += 10 {
		end := min(start+10, len(docs))
		texts := make([]string, end-start)
		for i, d := range docs[start:end] {
			texts[i] = d.text
			byRef[d.item.Ref] = d
		}
		vectors, err := s.embed(ctx, texts, false)
		if err != nil {
			t.Fatal(err)
		}
		for i, d := range docs[start:end] {
			cache[d.item.Ref] = catalogVector{d.hash, vectors[i]}
		}
	}
	fallbackRef, err := configuredSearchFallbackRef()
	if err != nil {
		t.Fatal(err)
	}
	queries := []string{"地图", "地图路线规划", "天气", "weather", "查询天气", "网页搜索", "搜索网页", "抓取网页", "提取指定网页 URL 的正文", "预订飞机票", "获取设备当前位置", "地址查询"}
	for _, q := range queries {
		vectors, err := s.embed(ctx, []string{q}, true)
		if err != nil {
			t.Fatal(err)
		}
		items, _, _ := rankCatalog(docs, q, vectors[0], cache, -1)
		if len(items) > s.reranker.candidates {
			items = items[:s.reranker.candidates]
		}
		if len(items) == 0 {
			t.Logf("query=%q no candidates", q)
			continue
		}
		texts := make([]string, len(items))
		for i, item := range items {
			texts[i] = byRef[item.Ref].text
		}
		rows, err := s.reranker.rank(ctx, q, texts)
		if err != nil {
			t.Fatal(err)
		}
		matches := []catalogItem{}
		for _, row := range rows {
			t.Logf("query=%q tool=%q score=%.6f kept=%t", q, items[*row.Index].Name, *row.Score, *row.Score >= s.reranker.minScore)
			if *row.Score >= s.reranker.minScore {
				matches = append(matches, items[*row.Index])
			}
		}
		suggestions := catalogSuggestions(docs, q, matches, searchStatus{Mode: "hybrid", Reranked: true}, fallbackRef)
		if len(matches) > 0 && len(suggestions) > 0 {
			t.Fatal("fallback mixed with relevance matches")
		}
		if len(matches) == 0 && fallbackRef != "" {
			if _, enabled := byRef[fallbackRef]; enabled && (len(suggestions) != 1 || suggestions[0].Ref != fallbackRef) {
				t.Fatal("missing enabled fallback")
			}
		}
		t.Logf("query=%q matches=%d suggestions=%d", q, len(matches), len(suggestions))
	}
}
