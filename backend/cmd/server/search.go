package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This trusted, administrator-configured service is separate from billable upstreams.
// It can be on a private Docker network and never receives upstream credentials.
type catalogSearch struct {
	endpoint, model, revision, key, apiKey string
	minScore                               float64
	client                                 *http.Client
}

func newCatalogSearch() (*catalogSearch, error) {
	endpoint := strings.TrimSpace(env("SEARCH_EMBEDDING_URL", ""))
	if endpoint == "" {
		return nil, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("SEARCH_EMBEDDING_URL must be a full HTTP(S) embeddings endpoint")
	}
	threshold, err := strconv.ParseFloat(env("SEARCH_MIN_SCORE", "0.75"), 64)
	if err != nil || math.IsNaN(threshold) || threshold < -1 || threshold > 1 {
		return nil, fmt.Errorf("SEARCH_MIN_SCORE must be between -1 and 1")
	}
	s := &catalogSearch{endpoint: endpoint, model: env("SEARCH_EMBEDDING_MODEL", "intfloat/multilingual-e5-small"), revision: env("SEARCH_EMBEDDING_REVISION", "1"), apiKey: env("SEARCH_EMBEDDING_API_KEY", ""), minScore: threshold,
		client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	s.key = searchHash(s.endpoint + "\x00" + s.model + "\x00" + s.revision + "\x00e5-prefix-v1")
	return s, nil
}

func searchHash(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }

func (s *catalogSearch) embed(ctx context.Context, texts []string, query bool) ([][]float64, error) {
	input := make([]string, len(texts))
	prefix := "passage: "
	if query {
		prefix = "query: "
	}
	for i, text := range texts {
		input[i] = prefix + text
	}
	body, err := json.Marshal(map[string]any{"model": s.model, "input": input})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", s.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embedding request invalid")
	}
	req.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding service unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding service HTTP %d", resp.StatusCode)
	}
	var result struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(raw) > 8<<20 || json.Unmarshal(raw, &result) != nil || len(result.Data) != len(input) {
		return nil, fmt.Errorf("invalid embedding response")
	}
	vectors := make([][]float64, len(input))
	dimension := 0
	for _, row := range result.Data {
		if row.Index < 0 || row.Index >= len(input) || vectors[row.Index] != nil || !normalizeVector(row.Embedding) {
			return nil, fmt.Errorf("invalid embedding vector or index")
		}
		if dimension != 0 && len(row.Embedding) != dimension {
			return nil, fmt.Errorf("inconsistent embedding dimensions")
		}
		dimension = len(row.Embedding)
		vectors[row.Index] = row.Embedding
	}
	return vectors, nil
}

func normalizeVector(v []float64) bool {
	if len(v) == 0 || len(v) > 8192 {
		return false
	}
	norm := 0.0
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return false
		}
		norm += x * x
	}
	if norm == 0 || math.IsInf(norm, 0) {
		return false
	}
	norm = math.Sqrt(norm)
	for i := range v {
		v[i] /= norm
	}
	return true
}

func cosine(a, b []float64) (float64, bool) {
	if len(a) == 0 || len(a) != len(b) {
		return 0, false
	}
	score := 0.0
	for i := range a {
		score += a[i] * b[i]
	}
	return score, !math.IsNaN(score) && !math.IsInf(score, 0)
}

type catalogDocument struct {
	toolID               int64
	endpointID           string
	item                 catalogItem
	text, hash, keywords string
	exact                []string
}

func makeCatalogDocument(t Tool, ep Endpoint) catalogDocument {
	description := strings.TrimSpace(ep.Description)
	if description == "" {
		description = strings.TrimSpace(t.Description)
	}
	item := catalogItem{Currency: "CNY", Ref: toolRef(t.ID, ep.ID), Name: ep.Name, Provider: t.Name, Description: description, SearchDescription: ep.SearchDescription, Price: effectivePrice(t, ep), InputSchema: ep.InputSchema, Example: endpointExample(t, ep), Type: t.UpstreamType, SchemaAvailable: len(ep.InputSchema) > 0 && string(ep.InputSchema) != "null"}
	// Put endpoint-specific capability information first; provider prose is auxiliary.
	text := strings.Join([]string{ep.Name, ep.SearchDescription, ep.Description, t.Name, t.Slug, ep.ID, t.Category, t.Description}, "\n")
	return catalogDocument{toolID: t.ID, endpointID: ep.ID, item: item, text: text, hash: searchHash(text), keywords: strings.ToLower(text), exact: []string{strings.ToLower(ep.Name), strings.ToLower(ep.ID), strings.ToLower(t.Name), strings.ToLower(t.Slug), strings.ToLower(item.Ref)}}
}

func (a *App) catalogDocuments(ctx context.Context) ([]catalogDocument, error) {
	rows, err := a.db.Query(ctx, `SELECT to_jsonb(t) FROM tools t WHERE enabled=true ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	docs := []catalogDocument{}
	for rows.Next() {
		var raw []byte
		var t Tool
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &t); err != nil {
			return nil, err
		}
		for _, ep := range t.Endpoints {
			if !ep.Disabled && !ep.Missing {
				docs = append(docs, makeCatalogDocument(t, ep))
			}
		}
	}
	return docs, rows.Err()
}

type catalogVector struct {
	hash   string
	vector []float64
}

func (a *App) catalogVectors(ctx context.Context) (map[string]catalogVector, error) {
	rows, err := a.db.Query(ctx, `SELECT tool_id,endpoint_id,document_hash,embedding FROM catalog_embeddings WHERE model_key=$1`, a.search.key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]catalogVector{}
	for rows.Next() {
		var id int64
		var ep, hash string
		var v []float64
		if err = rows.Scan(&id, &ep, &hash, &v); err != nil {
			return nil, err
		}
		if normalizeVector(v) {
			out[toolRef(id, ep)] = catalogVector{hash, v}
		}
	}
	return out, rows.Err()
}

// Backfill existing catalogs on startup and refresh changed endpoints periodically.
// Slow embedding calls never hold a database transaction or block tool writes.
func (a *App) runSearchIndexer(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if err := a.indexCatalog(ctx); err != nil && ctx.Err() == nil {
			log.Printf("catalog search: index refresh failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (a *App) indexCatalog(ctx context.Context) error {
	docs, err := a.catalogDocuments(ctx)
	if err != nil {
		return fmt.Errorf("cannot read catalog")
	}
	cached, err := a.catalogVectors(ctx)
	if err != nil {
		return fmt.Errorf("cannot read vector index")
	}
	pending := []catalogDocument{}
	for _, d := range docs {
		if v, ok := cached[d.item.Ref]; !ok || v.hash != d.hash {
			pending = append(pending, d)
		}
	}
	for start := 0; start < len(pending); start += 16 {
		end := start + 16
		if end > len(pending) {
			end = len(pending)
		}
		batch := pending[start:end]
		texts := make([]string, len(batch))
		for i, d := range batch {
			texts[i] = d.text
		}
		vectors, err := a.search.embed(ctx, texts, false)
		if err != nil {
			return err
		}
		for i, d := range batch {
			_, err = a.db.Exec(ctx, `INSERT INTO catalog_embeddings(tool_id,endpoint_id,model_key,document_hash,embedding) VALUES($1,$2,$3,$4,$5)
    ON CONFLICT(tool_id,endpoint_id,model_key) DO UPDATE SET document_hash=EXCLUDED.document_hash,embedding=EXCLUDED.embedding,updated_at=now()`, d.toolID, d.endpointID, a.search.key, d.hash, vectors[i])
			if err != nil {
				return fmt.Errorf("cannot save vector index")
			}
		}
	}
	return nil
}

type searchStatus struct {
	Mode     string `json:"mode"`
	Degraded bool   `json:"degraded"`
	Reason   string `json:"reason,omitempty"`
	Indexed  int    `json:"indexed"`
	Total    int    `json:"total"`
}
type rankedDocument struct {
	doc   catalogDocument
	score float64
	exact bool
}

func keywordScore(d catalogDocument, q string) (float64, bool) {
	for _, name := range d.exact {
		if name == q {
			return 100, true
		}
	}
	score := 0.0
	for _, term := range strings.Fields(q) {
		if strings.Contains(d.keywords, term) {
			score++
		}
		if strings.Contains(strings.ToLower(d.item.Name), term) {
			score += 3
		}
	}
	if strings.Contains(d.keywords, q) {
		score += 2
	}
	return score, false
}

func rankCatalog(docs []catalogDocument, q string, query []float64, cached map[string]catalogVector, threshold float64) ([]catalogItem, int, bool) {
	lexical, semantic := []rankedDocument{}, []rankedDocument{}
	indexed := 0
	mismatch := false
	for _, d := range docs {
		score, exact := keywordScore(d, q)
		if score > 0 {
			lexical = append(lexical, rankedDocument{d, score, exact})
		}
		if v, ok := cached[d.item.Ref]; ok && v.hash == d.hash {
			if len(query) == 0 {
				continue
			}
			similarity, valid := cosine(query, v.vector)
			if !valid {
				mismatch = true
				continue
			}
			indexed++
			if similarity >= threshold {
				semantic = append(semantic, rankedDocument{d, similarity, false})
			}
		}
	}
	order := func(items []rankedDocument) {
		sort.Slice(items, func(i, j int) bool {
			if items[i].score == items[j].score {
				return items[i].doc.item.Ref < items[j].doc.item.Ref
			}
			return items[i].score > items[j].score
		})
	}
	order(lexical)
	order(semantic)
	fused := map[string]rankedDocument{}
	for _, branch := range [][]rankedDocument{lexical, semantic} {
		for rank, hit := range branch {
			if rank >= 50 {
				break
			}
			ref := hit.doc.item.Ref
			v := fused[ref]
			v.doc = hit.doc
			v.exact = v.exact || hit.exact
			v.score += 1.0 / float64(60+rank+1)
			fused[ref] = v
		}
	}
	ranked := make([]rankedDocument, 0, len(fused))
	for _, v := range fused {
		ranked = append(ranked, v)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].exact != ranked[j].exact {
			return ranked[i].exact
		}
		if ranked[i].score == ranked[j].score {
			return ranked[i].doc.item.Ref < ranked[j].doc.item.Ref
		}
		return ranked[i].score > ranked[j].score
	})
	items := make([]catalogItem, 0, len(ranked))
	for _, v := range ranked {
		items = append(items, v.doc.item)
	}
	return items, indexed, mismatch
}

func (a *App) searchCatalog(ctx context.Context, docs []catalogDocument, q string) ([]catalogItem, searchStatus) {
	status := searchStatus{Mode: "keyword", Total: len(docs)}
	var vector []float64
	var cached map[string]catalogVector
	threshold := 1.0
	if a.search != nil {
		threshold = a.search.minScore
		searchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		var err error
		cached, err = a.catalogVectors(searchCtx)
		if err != nil {
			status.Degraded = true
			status.Reason = "index_unavailable"
		} else if len(cached) == 0 && len(docs) > 0 {
			status.Degraded = true
			status.Reason = "index_pending"
		} else if len(docs) > 0 {
			var vectors [][]float64
			vectors, err = a.search.embed(searchCtx, []string{q}, true)
			if err != nil {
				status.Degraded = true
				status.Reason = "embedding_unavailable"
			} else {
				vector = vectors[0]
				status.Mode = "hybrid"
			}
		}
	}
	items, indexed, mismatch := rankCatalog(docs, q, vector, cached, threshold)
	status.Indexed = indexed
	if len(vector) > 0 && (indexed < len(docs) || mismatch) {
		status.Degraded = true
		status.Reason = "index_pending"
		if mismatch {
			status.Reason = "dimension_mismatch"
		}
		if indexed == 0 {
			status.Mode = "keyword"
		}
	}
	return items, status
}

// Cursor signatures bind relevance pages to the query and ordered candidates.
// If a refresh or degradation changes that order, restart instead of skipping hits.
type searchCursor struct {
	Query     string `json:"q"`
	Signature string `json:"s"`
	Offset    int    `json:"o"`
}

func catalogPage(items []catalogItem, q, cursor string, limit int) ([]catalogItem, string, error) {
	start := 0
	signature := ""
	if q == "" {
		sort.Slice(items, func(i, j int) bool { return items[i].Ref < items[j].Ref })
		start = sort.Search(len(items), func(i int) bool { return items[i].Ref > cursor })
	} else {
		refs := make([]string, len(items))
		for i := range items {
			refs[i] = items[i].Ref
		}
		signature = searchHash(strings.Join(refs, "\x00"))
		if cursor != "" {
			raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(cursor, "s1."))
			var c searchCursor
			if !strings.HasPrefix(cursor, "s1.") || err != nil || json.Unmarshal(raw, &c) != nil || c.Query != searchHash(q) || c.Signature != signature || c.Offset < 0 || c.Offset >= len(items) {
				return nil, "", fmt.Errorf("搜索游标无效或结果已更新，请从第一页重新搜索")
			}
			start = c.Offset
		}
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	page := append([]catalogItem{}, items[start:end]...)
	for i := range page {
		page[i].InputSchema = nil
		page[i].Example = nil
	}
	next := ""
	if end < len(items) {
		if q == "" {
			next = items[end-1].Ref
		} else {
			raw, _ := json.Marshal(searchCursor{searchHash(q), signature, end})
			next = "s1." + base64.RawURLEncoding.EncodeToString(raw)
		}
	}
	return page, next, nil
}
