package main

import (
	"fmt"
	"strings"
)

type catalogSuggestion struct {
	catalogItem
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

func configuredSearchFallbackRef() (string, error) {
	ref := strings.TrimSpace(env("SEARCH_FALLBACK_TOOL_REF", ""))
	if ref != "" {
		if _, _, err := parseToolRef(ref); err != nil {
			return "", fmt.Errorf("SEARCH_FALLBACK_TOOL_REF must be a valid tool_ref")
		}
	}
	return ref, nil
}

func catalogSuggestions(docs []catalogDocument, query string, matches []catalogItem, status searchStatus, fallbackRef string) []catalogSuggestion {
	suggestions := []catalogSuggestion{}
	// Inspect the complete result set, not a page. A model outage is not a
	// reliable no-match result and must never activate this recommendation.
	if strings.TrimSpace(query) == "" || len(matches) != 0 || status.Degraded || fallbackRef == "" {
		return suggestions
	}
	// docs is loaded from the current enabled catalog on every request, outside
	// the ranking cache. Disabled, missing and deleted tools cannot be suggested.
	for _, d := range docs {
		if d.item.Ref == fallbackRef {
			item := d.item
			item.InputSchema, item.Example = nil, nil
			return append(suggestions, catalogSuggestion{
				catalogItem: item,
				Reason:      "no_relevant_tool",
				Message:     "未找到匹配工具，可尝试用此工具搜索公开信息；不代表它具备原任务所需能力。调用前查看工具详情，并确认费用授权。",
			})
		}
	}
	return suggestions
}
