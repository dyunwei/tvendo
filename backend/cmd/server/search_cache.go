package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type catalogSearchCache struct {
	items   []catalogItem
	status  searchStatus
	expires time.Time
}

func (a *App) searchCatalog(ctx context.Context, docs []catalogDocument, q string) ([]catalogItem, searchStatus) {
	if a.search == nil || a.search.reranker == nil {
		return a.searchCatalogUncached(ctx, docs, q)
	}
	// Include prices, schemas and all capability text: edits must not return stale
	// catalog metadata or reuse a ranking made against a different document.
	var key strings.Builder
	key.WriteString(q)
	for _, d := range docs {
		raw, _ := json.Marshal(d.item)
		key.WriteByte(0)
		key.WriteString(d.hash)
		key.WriteByte(0)
		key.WriteString(d.keywords)
		key.Write(raw)
	}
	cacheKey := searchHash(key.String())
	s := a.search
	s.cacheMu.Lock()
	cached, ok := s.cache[cacheKey]
	s.cacheMu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return append([]catalogItem{}, cached.items...), cached.status
	}
	items, status := a.searchCatalogUncached(ctx, docs, q)
	if status.Reranked && !status.Degraded {
		s.cacheMu.Lock()
		if s.cache == nil {
			s.cache = make(map[string]catalogSearchCache)
		}
		if len(s.cache) >= 128 {
			// A bounded opportunistic cache; never persist user queries or model keys.
			clear(s.cache)
		}
		s.cache[cacheKey] = catalogSearchCache{append([]catalogItem{}, items...), status, time.Now().Add(30 * time.Second)}
		s.cacheMu.Unlock()
	}
	return items, status
}
