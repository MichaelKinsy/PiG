// SPDX-License-Identifier: MIT

//go:build linux

package perfgate

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/perfgate"
)

// writeSource is n bytes of TypeScript, the file a large write tool call carries.
func writeSource(n int) string {
	var source strings.Builder
	for line := 0; source.Len() < n; line++ {
		fmt.Fprintf(&source, "export function step%d(input: number[]): number { return input.reduce((a, b) => a + b * %d, %d); }\n", line, line%97, line)
	}
	return source.String()[:n]
}

// largeWriteServer is an OpenAI-compatible endpoint that answers the first request of a prompt with a write tool call whose arguments stream 16 bytes per SSE record, opened with empty arguments as providers send it, and the request after the tool result with a short text.
func largeWriteServer(t *testing.T, content func() string) *httptest.Server {
	t.Helper()
	chunk := func(delta map[string]any, finish string) string {
		choice := map[string]any{"index": 0, "delta": delta}
		if finish != "" {
			choice["finish_reason"] = finish
		}
		encoded, err := json.Marshal(map[string]any{"id": "c", "object": "chat.completion.chunk", "created": 1, "model": "fake-model", "choices": []any{choice}})
		if err != nil {
			t.Error(err)
		}
		return "data: " + string(encoded) + "\n\n"
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		var out strings.Builder
		out.WriteString(chunk(map[string]any{"role": "assistant", "content": ""}, ""))
		if strings.Contains(string(body), `"role":"tool"`) {
			out.WriteString(chunk(map[string]any{"content": "Done: write."}, ""))
			out.WriteString(chunk(map[string]any{}, "stop"))
		} else {
			arguments, err := json.Marshal(map[string]string{"path": "big/source.ts", "content": content()})
			if err != nil {
				t.Error(err)
			}
			out.WriteString(chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_0", "type": "function", "function": map[string]any{"name": "write", "arguments": ""}}}}, ""))
			for i := 0; i < len(arguments); i += 16 {
				piece := string(arguments[i:min(i+16, len(arguments))])
				out.WriteString(chunk(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": piece}}}}, ""))
			}
			out.WriteString(chunk(map[string]any{}, "tool_calls"))
		}
		out.WriteString("data: [DONE]\n\n")
		_, _ = io.WriteString(w, out.String())
	}))
	t.Cleanup(server.Close)
	return server
}

// A file written through a tool call in print mode: the cost of each 16-byte arguments record stays constant as the file grows from 100 KB to 200 KB, so the whole call costs what its bytes cost. Parsing, the events and every observer's copy of the partial message are on this path; a parse or copy of the whole arguments per record makes the per-record cost double with the file.
func TestLargeWriteProcessGate(t *testing.T) {
	home := newPigHome(t)
	// The server goroutine reads size while it answers the pig process; nothing the race detector sees orders that read after the write below.
	var size atomic.Int64
	server := largeWriteServer(t, func() string { return writeSource(int(size.Load())) })
	agent := strings.TrimPrefix(envValue(home.env, "PIG_CODING_AGENT_DIR"), "PIG_CODING_AGENT_DIR=")
	models := `{"providers":{"fake":{"baseUrl":"` + server.URL + `/v1","api":"openai-completions","apiKey":"fake","models":[{"id":"fake-model","contextWindow":200000,"maxTokens":8192}]}}}`
	if err := os.WriteFile(filepath.Join(agent, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-ne", "--no-session", "--model", "fake/fake-model", "-p", "Write the file."}
	written := filepath.Join(home.cwd, "big", "source.ts")
	measure := func(kilobytes int) (records float64, allocs, bytes float64) {
		length := kilobytes * 1024
		size.Store(int64(length))
		record := home.run(args, nil)
		data, err := os.ReadFile(written)
		if err != nil || len(data) != length {
			t.Fatalf("the write tool wrote %d bytes (%v), want %d", len(data), err, length)
		}
		encoded, _ := json.Marshal(map[string]string{"path": "big/source.ts", "content": writeSource(length)})
		return float64((len(encoded) + 15) / 16), float64(record.AllocObjects), float64(record.AllocBytes)
	}
	measure(1) // The first start in a new home materializes caches.
	base, baseAllocs, baseBytes := measure(1)
	half, halfAllocs, halfBytes := measure(100)
	whole, wholeAllocs, wholeBytes := measure(200)
	first := [2]float64{(halfAllocs - baseAllocs) / (half - base), (halfBytes - baseBytes) / (half - base)}
	second := [2]float64{(wholeAllocs - halfAllocs) / (whole - half), (wholeBytes - halfBytes) / (whole - half)}
	t.Logf("perfgate: process.large_write per arguments record from 1 to 100 KB: %.1f allocs %.0f B; from 100 to 200 KB: %.1f allocs %.0f B", first[0], first[1], second[0], second[1])
	if second[0] > 1.25*first[0] || second[1] > 1.25*first[1] {
		t.Errorf("perfgate: process.large_write per-record cost grows with the file: %.1f allocs %.0f B per record up to 100 KB, %.1f allocs %.0f B from 100 to 200 KB", first[0], first[1], second[0], second[1])
	}
	perfgate.Check(t, "process.large_write.record_allocs", second[0])
	perfgate.Check(t, "process.large_write.record_bytes", second[1])
}

func envValue(env []string, name string) string {
	for _, entry := range env {
		if strings.HasPrefix(entry, name+"=") {
			return entry
		}
	}
	return ""
}
