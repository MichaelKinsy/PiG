package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// upstream: packages/ai/src/api/openai-codex-responses.ts:84 (toolChoice?: "auto" | "none" | "required"), :512 (streamSimple forwards options.toolChoice)
// and :562 (tool_choice: options?.toolChoice ?? "auto"): the request carries the caller's choice, and "auto" when there is none.
func TestCodexToolChoiceDefaultsToAutoAndForwardsTheOption(t *testing.T) {
	for _, tc := range []struct {
		name   string
		choice any
		want   string
	}{
		{"absent defaults to auto", nil, "auto"},
		{"auto", "auto", "auto"},
		{"typed none", ToolChoiceNone, "none"},
		{"none", "none", "none"},
		{"required", "required", "required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				encoded, _ := io.ReadAll(r.Body)
				decoded, err := decodeZstdRawFrameForTest(encoded)
				if err != nil {
					t.Error(err)
				} else if err := json.Unmarshal(decoded, &body); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(reviewSSEDone))
			}))
			defer server.Close()
			stream, err := reviewCodexProvider(t, server.URL, "acct_tool_choice").Stream(context.Background(), reviewCodexCtx("hi"), StreamOptions{Transport: TransportSSE, ToolChoice: tc.choice})
			if err != nil {
				t.Fatal(err)
			}
			_ = stream.Result()
			if got := body["tool_choice"]; got != tc.want {
				t.Errorf("tool_choice = %v, want %s", got, tc.want)
			}
		})
	}
}
