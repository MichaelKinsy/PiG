package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A Piglet that Stock PiG runs strips its APIs when the process starts (strip.apis). The provider bridge serves a process that has no
// session through pi-ai's compatibility layer, which builds the collection of built-in providers first. With an API stripped, a
// stream to a provider that has nothing to do with it is still served, and a stream for a model of the stripped provider is answered
// with the strip, in the words of internal/pigstrip. The strip is process state and the collection is built once per process, so each
// case runs in a process of its own: this test binary again, with the API to strip in its environment.
func TestProviderBridgeWithAnAPIStrippedAtRunTime(t *testing.T) {
	const stripEnv = "PIG_TEST_BRIDGE_STRIPPED_API"
	cases := map[string]struct{ provider, model string }{
		"bedrock-converse-stream": {"amazon-bedrock", "amazon.nova-2-lite-v1:0"},
		"google-vertex":           {"google-vertex", "gemini-2.5-flash"},
		"mistral-conversations":   {"mistral", "codestral-latest"},
	}
	if api := os.Getenv(stripEnv); api != "" {
		t.Cleanup(pigstrip.Strip(pigstrip.ListAPIs, api))
		server := bridgeServer(t, nil)
		run := startBridge(t)
		writeBridge(t, run.stdin, bridgeLine("other", "stream", true, server.URL))
		got := run.until(t, "other", "result")
		if raw, _ := json.Marshal(got[len(got)-1].Result); !strings.Contains(string(raw), "DONE") || !strings.Contains(string(raw), `"stopReason":"stop"`) {
			t.Fatalf("a stream to an unrelated provider with %s stripped: result %s", api, raw)
		}
		stripped := cases[api]
		line, _ := json.Marshal(map[string]any{
			"id": "stripped", "type": "stream", "simple": true,
			"model": map[string]any{"provider": stripped.provider, "id": stripped.model, "name": stripped.model, "api": api, "baseUrl": server.URL, "reasoning": false,
				"input": []string{"text"}, "contextWindow": 128000, "maxTokens": 4096, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}},
			"request": map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi", "timestamp": 0}}, "apiKey": "bridge-test-key"},
		})
		writeBridge(t, run.stdin, string(line)+"\n")
		want := "Provider " + stripped.provider + ": API " + api + " is stripped from this Piglet (strip.apis: " + api + ")"
		timeout := time.After(testbudget.Wait(t))
		for {
			select {
			case reply, ok := <-run.lines:
				if !ok {
					t.Fatalf("bridge output ended before the answer for the %s model", stripped.provider)
				}
				if reply.ID != "stripped" || (reply.Type != "error" && reply.Type != "result") {
					continue
				}
				if raw, _ := json.Marshal(reply); !strings.Contains(string(raw), want) {
					t.Fatalf("a stream for a %s model with %s stripped answered %s, want %q", stripped.provider, api, raw, want)
				}
				return
			case <-timeout:
				t.Fatalf("no answer for the %s model", stripped.provider)
			}
		}
	}
	for api := range cases {
		t.Run(api, func(t *testing.T) {
			t.Parallel()
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestProviderBridgeWithAnAPIStrippedAtRunTime$", "-test.count=1")
			cmd.Env = append(os.Environ(), stripEnv+"="+api)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("a process with %s stripped: %v\n%s", api, err, output)
			}
		})
	}
}
