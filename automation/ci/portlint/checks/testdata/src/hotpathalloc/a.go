package hotpathalloc

import (
	"fmt"
	"maps"
	"slices"
	"time"
)

// A hot path formats, copies and arms a timer per call.
//
//pig:hotpath
func hot(id int, items []string, set map[string]int, done chan struct{}) string {
	label := fmt.Sprintf("item-%d", id) // want `fmt.Sprintf on a hot path`
	_ = slices.Clone(items)             // want `slices.Clone on a hot path`
	_ = maps.Clone(set)                 // want `maps.Clone on a hot path`
	_ = append([]string(nil), items...) // want `append\(\[\]T\(nil\), x...\) on a hot path`
	select {
	case <-done:
	case <-time.After(time.Second): // want `time.After on a hot path`
	}
	return label
}

// Off the hot path the same calls are fine.
func cold(id int, items []string) string {
	_ = slices.Clone(items)
	return fmt.Sprintf("item-%d", id)
}

// Appending to a non-nil slice is not a copy of the argument into new storage.
//
//pig:hotpath
func hotGood(dst, items []string) []string {
	return append(dst, items...)
}

// A longer directive that only starts with the hot-path one does not mark the function.
//
//pig:hotpathless
func notHot(id int) string {
	return fmt.Sprintf("item-%d", id)
}
