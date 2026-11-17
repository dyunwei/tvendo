package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCatalogSuggestions(t *testing.T) {
	d := makeCatalogDocument(Tool{ID: 1, Name: "Search provider"}, Endpoint{ID: "web", Name: "Web search", Description: "Search public information", Price: 1234, InputSchema: json.RawMessage(`{"type":"object"}`)})
	d.item.Example = json.RawMessage(`{"query":"example"}`)
	for _, tc := range []struct {
		name    string
		query   string
		matches []catalogItem
		status  searchStatus
		ref     string
		docs    []catalogDocument
		want    int
	}{
		{"no match", "地图", nil, searchStatus{Mode: "hybrid", Reranked: true}, d.item.Ref, []catalogDocument{d}, 1},
		{"keyword only", "地图", nil, searchStatus{Mode: "keyword"}, d.item.Ref, []catalogDocument{d}, 1},
		{"has match", "网页搜索", []catalogItem{d.item}, searchStatus{}, d.item.Ref, []catalogDocument{d}, 0},
		{"empty query", "  ", nil, searchStatus{Mode: "directory"}, d.item.Ref, []catalogDocument{d}, 0},
		{"rerank failed", "地图", nil, searchStatus{Degraded: true, Reason: "rerank_unavailable"}, d.item.Ref, []catalogDocument{d}, 0},
		{"index pending", "地图", nil, searchStatus{Degraded: true, Reason: "index_pending", Reranked: true}, d.item.Ref, []catalogDocument{d}, 0},
		{"not configured", "地图", nil, searchStatus{}, "", []catalogDocument{d}, 0},
		{"tool disabled or missing", "地图", nil, searchStatus{}, d.item.Ref, nil, 0},
		{"another ref", "地图", nil, searchStatus{}, toolRef(2, "web"), []catalogDocument{d}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := catalogSuggestions(tc.docs, tc.query, tc.matches, tc.status, tc.ref)
			if got == nil || len(got) != tc.want {
				t.Fatalf("unexpected suggestions: %+v", got)
			}
			if tc.want == 0 {
				return
			}
			if got[0].Ref != d.item.Ref || got[0].Price != 1234 || got[0].Reason != "no_relevant_tool" || got[0].Message == "" || got[0].InputSchema != nil || got[0].Example != nil {
				t.Fatalf("incorrect suggestion metadata: %+v", got)
			}
			raw, err := json.Marshal(got)
			if err != nil || !strings.Contains(string(raw), `"tool_ref"`) || strings.Contains(string(raw), "input_schema") {
				t.Fatalf("invalid public suggestion: %s %v", raw, err)
			}
			if d.item.InputSchema == nil || d.item.Example == nil {
				t.Fatal("suggestion mutated source catalog")
			}
		})
	}
}

func TestSearchFallbackConfiguration(t *testing.T) {
	for _, valid := range []string{"", toolRef(12, "search"), " " + toolRef(12, "路径/参数") + " "} {
		t.Setenv("SEARCH_FALLBACK_TOOL_REF", valid)
		ref, err := configuredSearchFallbackRef()
		if err != nil || ref != strings.TrimSpace(valid) {
			t.Fatalf("valid fallback rejected: %v", err)
		}
	}
	for _, invalid := range []string{"web search", "t0.YQ", "https://example.com", "t1."} {
		t.Setenv("SEARCH_FALLBACK_TOOL_REF", invalid)
		if _, err := configuredSearchFallbackRef(); err == nil {
			t.Fatal("invalid fallback accepted")
		}
	}
}
