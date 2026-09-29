package subprocess

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestPortWave12TriggerCompactExtension(t *testing.T) {
	// packages/coding-agent/test/trigger-compact-extension.test.ts:28
	t.Run("only auto-compacts when context usage crosses the threshold", func(t *testing.T) {
		isolateExampleHost(t)
		dir := t.TempDir()
		for name, source := range map[string]string{
			"trigger-compact.ts": upstreamExamplePath(t, "trigger-compact"),
			"index.mjs":          filepath.Join("testdata", "port-wave-12", "trigger-compact.mjs"),
		} {
			data, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(dir, name), string(data))
		}
		var tokens atomic.Int64
		ext := loadExampleWithActions(t, filepath.Join(dir, "index.mjs"), &HostCallbacks{
			IsIdle:             func() bool { return true },
			IsProjectTrusted:   func() bool { return true },
			HasPendingMessages: func() bool { return false },
			GetSystemPrompt:    func() string { return "" },
			GetContextUsage: func() *extension.ContextUsage {
				n := int(tokens.Load())
				percent := float64(n) / 2000
				return &extension.ContextUsage{Tokens: &n, ContextWindow: 200000, Percent: &percent}
			},
		})
		handler := requireExampleHandler(t, ext, "turn_end")
		// The same loaded factory retains previousTokens across all four calls.
		for _, step := range []struct {
			tokens int64
			calls  int64
		}{{110000, 0}, {120000, 0}, {95000, 0}, {105000, 1}} {
			tokens.Store(step.tokens)
			result, err := handler(map[string]any{"type": "turn_end"}, t.Context())
			if err != nil {
				t.Fatal(err)
			}
			raw, ok := result.(json.RawMessage)
			if !ok {
				t.Fatalf("compact spy count = %#v, want wire result", result)
			}
			var got int64
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if got != step.calls {
				t.Fatalf("after %d tokens: compact called %d times, want %d", step.tokens, got, step.calls)
			}
		}
	})
}
