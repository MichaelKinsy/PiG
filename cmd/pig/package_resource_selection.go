package main

import (
	"slices"

	"github.com/MichaelKinsy/PiG/tui"
)

// Ports packages/coding-agent/src/core/package-manager.ts

func resourcePrecedenceRank(item tui.ResourceItem) int {
	if item.Origin == "package" {
		return 4
	}
	rank := 0
	if item.Scope != "project" {
		rank = 2
	}
	if item.Source != "local" {
		rank++
	}
	return rank
}

// canonicalResourceItems applies toResolvedPaths: stable precedence sorting precedes canonical-path deduplication, retaining the winning spelling and metadata.
func canonicalResourceItems(items []tui.ResourceItem) []tui.ResourceItem {
	slices.SortStableFunc(items, func(a, b tui.ResourceItem) int {
		return resourcePrecedenceRank(a) - resourcePrecedenceRank(b)
	})
	seen := make(map[string]bool, len(items))
	return slices.DeleteFunc(items, func(item tui.ResourceItem) bool {
		key := configItemKey(item)
		duplicate := seen[key]
		seen[key] = true
		return duplicate
	})
}
