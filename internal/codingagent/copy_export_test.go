package codingagent

import (
	"slices"
	"sync"
)

// CaptureClipboard replaces the clipboard writer with one that records, and returns a reader of the texts copied so far. Call it off the owner loop.
func (h *TestHarness) CaptureClipboard() func() []string {
	var mu sync.Mutex
	var texts []string
	h.Do(func() {
		h.m.copyClipboard = func(text string) error {
			mu.Lock()
			defer mu.Unlock()
			texts = append(texts, text)
			return nil
		}
	})
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(texts)
	}
}
