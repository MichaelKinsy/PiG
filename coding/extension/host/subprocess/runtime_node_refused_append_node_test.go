//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// The program of the issue (a host that refuses pi.appendEntry), through the real Host and Node runtime, with the host's other appends around it. Each read is a host-synced tool call after the append; every one must report exactly the host's session.
func TestNodeExtensionReadsExactlyTheSessionTheHostHolds(t *testing.T) {
	cases := []struct {
		name         string
		types        []string
		refuse       func(string) bool
		nativeBefore string
		want         []string // the custom types the host holds afterwards
	}{
		{
			name:   "one refused append",
			types:  []string{"fixture.denied"},
			refuse: func(string) bool { return true },
		},
		{
			name:   "accepted append between refused ones",
			types:  []string{"fixture.denied", "fixture.kept", "fixture.denied"},
			refuse: func(customType string) bool { return customType == "fixture.denied" },
			want:   []string{"fixture.kept"},
		},
		{
			name:         "native message the host appended while the append was in flight",
			types:        []string{"fixture.kept", "fixture.denied", "fixture.raced"},
			refuse:       func(customType string) bool { return customType == "fixture.denied" },
			nativeBefore: "fixture.raced",
			want:         []string{"fixture.kept", "fixture.raced"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
			defer cancel()
			root := t.TempDir()
			source := filepath.Join(root, "fixture.mjs")
			if err := os.WriteFile(source, []byte(refusedAppendFixture), 0o600); err != nil {
				t.Fatal(err)
			}
			host := &refusingHost{refuse: tc.refuse, nativeBefore: tc.nativeBefore}
			host.native("m1")
			bridge := NewUIBridge(func() {})
			bridge.SetActions(host.callbacks())
			h := NewHostWithConfigRoot(root, filepath.Join(root, "config"))
			h.SetUIBridge(bridge)
			defer h.Shutdown("test complete")
			loaded, err := h.Load(ctx, ExtConfig{Name: "fixture", Source: source, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			params, err := json.Marshal(map[string]any{"types": tc.types})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := loaded.Tools["append_probe"].Definition.Execute(ctx, "append", params, nil); err != nil {
				t.Fatal(err)
			}
			host.mu.Lock()
			calls := append([]string{}, host.calls...)
			var held []string
			for _, record := range host.records {
				if customType, ok := record["customType"].(string); ok {
					held = append(held, customType)
				}
			}
			host.mu.Unlock()
			if strings.Join(calls, ",") != strings.Join(tc.types, ",") {
				t.Fatalf("the host received appends %v, want %v", calls, tc.types)
			}
			if strings.Join(held, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("the host holds custom entries %v, want %v", held, tc.want)
			}
			for round := range 2 {
				result, err := loaded.Tools["read_probe"].Definition.Execute(ctx, "read", json.RawMessage(`{}`), nil)
				if err != nil {
					t.Fatal(err)
				}
				if got, want := ai.ContentText(result.Content), host.view(); got != want {
					t.Fatalf("read %d: the extension's mirror differs from the host's session\n mirror %s\n host   %s", round, got, want)
				}
			}
		})
	}
}
