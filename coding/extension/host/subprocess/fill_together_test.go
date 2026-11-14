package subprocess

import (
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

// goroutineID is the number in the first line of the calling goroutine's stack ("goroutine 7 [running]:").
func goroutineID() string {
	buf := make([]byte, 64)
	return strings.Fields(string(buf[:runtime.Stack(buf, false)]))[1]
}

// A lone config has nothing to be filled together with, so fillTogether fills it on the caller's goroutine. Every accepted config is filled once, whatever their number.
func TestFillTogetherFillsALoneConfigOnTheCallersGoroutine(t *testing.T) {
	enabled := func(cfg *ExtConfig) bool { return cfg.Enabled }
	for _, tc := range []struct {
		name    string
		configs []ExtConfig
		include func(*ExtConfig) bool
		filled  []string
		lone    bool
	}{
		{"one config, every config accepted", []ExtConfig{{Name: "a"}}, nil, []string{"a"}, true},
		{"one enabled config among three", []ExtConfig{{Name: "a"}, {Name: "b", Enabled: true}, {Name: "c"}}, enabled, []string{"b"}, true},
		{"two enabled configs", []ExtConfig{{Name: "a", Enabled: true}, {Name: "b"}, {Name: "c", Enabled: true}}, enabled, []string{"a", "c"}, false},
		{"no config accepted", []ExtConfig{{Name: "a"}}, enabled, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := goroutineID()
			var mu sync.Mutex
			var filled, elsewhere []string
			fillTogether(tc.configs, tc.include, func(cfg *ExtConfig) {
				id := goroutineID()
				mu.Lock()
				defer mu.Unlock()
				filled = append(filled, cfg.Name)
				if id != caller {
					elsewhere = append(elsewhere, cfg.Name)
				}
			})
			slices.Sort(filled)
			if !slices.Equal(filled, tc.filled) {
				t.Fatalf("filled %q, want %q", filled, tc.filled)
			}
			if tc.lone && len(elsewhere) != 0 {
				t.Fatalf("the lone config %q was filled on another goroutine", elsewhere)
			}
		})
	}
}
