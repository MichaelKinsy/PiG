package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

const rpcUsageProbeDir = "testdata/d82-w7"

// rpcUsageBody is the body rpc-usage-probe.mjs serves: every SSE chunk carries a growing usage, and all of it arrives in one write.
func rpcUsageBody() string {
	chunk := func(content string, output int, finish string) string {
		delta := "{}"
		if content != "" {
			delta = fmt.Sprintf(`{"content":%q}`, content)
		}
		reason := "null"
		if finish != "" {
			reason = fmt.Sprintf("%q", finish)
		}
		return fmt.Sprintf("data: {\"id\":\"chatcmpl-w7\",\"model\":\"strict\",\"choices\":[{\"index\":0,\"delta\":%s,\"finish_reason\":%s}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":%d,\"total_tokens\":%d}}\n\n", delta, reason, output, 10+output)
	}
	return chunk("a", 1, "") + chunk("b", 2, "") + chunk("c", 3, "") + chunk("d", 4, "") + chunk("e", 5, "stop") + "data: [DONE]\n\n"
}

// rpcUsageFrames reduces an RPC frame the way rpc-usage-probe.mjs does.
func rpcUsageFrames(t *testing.T, frames []any) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, frame := range frames {
		raw, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		var event struct {
			Type    string                `json:"type"`
			Usage   *struct{ Output int } `json:"usage"`
			Message *struct {
				Role       string                `json:"role"`
				Content    json.RawMessage       `json:"content"`
				Usage      *struct{ Output int } `json:"usage"`
				StopReason string                `json:"stopReason"`
			} `json:"message"`
			Update *struct {
				Type  string `json:"type"`
				Delta string `json:"delta"`
			} `json:"assistantMessageEvent"`
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		entry := map[string]any{"type": event.Type}
		delta := ""
		if event.Update != nil && (event.Update.Type == "text_delta" || event.Type == "message_update") {
			delta = event.Update.Delta
		}
		switch {
		case event.Message != nil && event.Message.Role == "assistant" && (event.Type == "message_start" || event.Type == "message_update" || event.Type == "message_end"):
			usage := event.Usage
			if usage == nil {
				usage = event.Message.Usage
			}
			var blocks []struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(event.Message.Content, &blocks); err != nil {
				t.Fatal(err)
			}
			var text strings.Builder
			for _, block := range blocks {
				text.WriteString(block.Text)
			}
			if delta != "" {
				entry["delta"] = delta
			}
			entry["outputTokens"] = float64(usage.Output)
			if event.Message.StopReason != "" {
				entry["stopReason"] = event.Message.StopReason
			}
			entry["text"] = text.String()
		case event.Type == "message_update" && event.Usage != nil:
			if delta != "" {
				entry["delta"] = delta
			}
			entry["outputTokens"] = float64(event.Usage.Output)
		default:
			continue
		}
		out = append(out, entry)
	}
	return out
}

// rpcUsageServer serves rpcUsageBody in one write, as the probe's loopback server does.
func rpcUsageServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		body := rpcUsageBody()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		_, _ = io.WriteString(w, body)
	}))
}

// rpcUsageModel builds the model the real CLI builds, through Model Runtime and its forwarding layers, which set the start state.
func rpcUsageModel(t *testing.T, server *httptest.Server) (*coding.Services, *ai.Model) {
	t.Helper()
	// The real CLI reaches the provider through Model Runtime and its forwarding layers, which set the start state; use the same path.
	agentDir := t.TempDir()
	config := fmt.Sprintf(`{"providers":{"p":{"baseUrl":%q,"api":"openai-completions","apiKey":"k","models":[{"id":"strict","name":"strict"}]}}}`, server.URL+"/v1")
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	model, err := coding.BuildModel("p/strict", services)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.Provider.Close() })
	return services, model
}

func runRPCUsageThroughSubscribers(t *testing.T, backpressure bool) []map[string]any {
	t.Helper()
	server := rpcUsageServer(t)
	defer server.Close()
	services, model := rpcUsageModel(t, server)
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, SkipBuiltinTools: true, SessionDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	}()
	var frames []any
	unsubscribe := subscribeRPCEvents(session, func(frame any) { frames = append(frames, frame) }, func(err error) { t.Error(err) })
	defer unsubscribe()
	if backpressure {
		defer subscribeStdoutBackpressure(session.Agent(), func() {})()
	}
	if _, err := session.Send(t.Context(), "hi"); err != nil {
		t.Fatal(err)
	}
	return rpcUsageFrames(t, frames)
}

func readRPCUsageGolden(t *testing.T) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(rpcUsageProbeDir, "rpc-usage-pi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden []map[string]any
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	return golden
}

// The goldens are real Pi 0.87.1 output: --mode rpc without an extension, and --mode rpc with coding/testdata/d82-w7/message-start-wait-extension.mjs, whose message_start handler awaits a timer (the extension golden's first frame shows the content streamed during that await; TestSessionNodeExtensionAwaitLetsProviderAdvance asserts it through PiG's Session). This proves they are not stale.
func TestRPCUsageGoldenMatchesPi(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the Pi differential probe: %v", err)
	}
	cli, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cli); err != nil {
		t.Fatalf("the pinned Pi package is required (run make test-sdk-ts setup): %v", err)
	}
	extension, err := filepath.Abs(filepath.Join("..", "..", "coding", "testdata", "d82-w7", "message-start-wait-extension.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	for golden, probeExtension := range map[string]string{"rpc-usage-pi.json": "", "rpc-usage-extension-pi.json": extension} {
		t.Run(golden, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "rpc-usage.json")
			command := exec.CommandContext(t.Context(), node, filepath.Join(rpcUsageProbeDir, "rpc-usage-probe.mjs"), node, cli, out)
			command.Env = append(os.Environ(), "PROBE_EXTENSION="+probeExtension)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("probe failed: %v\n%s", err, output)
			}
			live, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(rpcUsageProbeDir, golden))
			if err != nil {
				t.Fatal(err)
			}
			if string(live) != string(want) {
				t.Fatalf("%s is stale; regenerate it with the probe.\nlive: %s", golden, live)
			}
		})
	}
}

// rpc-mode.ts:354-363 serializes each event in the Session subscriber and then, in an Agent listener registered after the Session's, awaits waitForRawStdoutBackpressure (output-guard.ts:95-101). The write callback runs after every promise reaction, so the provider finishes its buffered body before the next event is copied and serialized. Real Pi shows the final usage on the first message_update.
func TestRPCStdoutBackpressureLetsProviderFinishBufferedBody(t *testing.T) {
	want := readRPCUsageGolden(t)
	for run := range 5 {
		if got := runRPCUsageThroughSubscribers(t, true); !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d frames differ from Pi\n got: %v\nwant: %v", run, got, want)
		}
	}
}

// print-mode.ts:108-118 gives JSON mode the same Session subscriber and Agent backpressure listener as RPC. Real Pi's --mode json output for the same body is the golden.
func TestJSONModeStdoutBackpressureMatchesPi(t *testing.T) {
	want := readRPCUsageGolden(t)
	for run := range 5 {
		server := rpcUsageServer(t)
		services, model := rpcUsageModel(t, server)
		host := printModeRuntime{Services: services, Session: coding.SessionStartOptions{Model: model, SkipBuiltinTools: true, SessionDir: t.TempDir()}}
		result := runPrintModeForTest(t, host, printModeOptions{Mode: "json", InitialMessage: "hi"})
		server.Close()
		if result.err != nil || result.stderr != "" {
			t.Fatalf("run %d: %v, stderr=%q", run, result.err, result.stderr)
		}
		var frames []any
		for line := range strings.SplitSeq(strings.TrimSpace(result.stdout), "\n") {
			frames = append(frames, json.RawMessage(line))
		}
		if got := rpcUsageFrames(t, frames); !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d frames differ from Pi\n got: %v\nwant: %v", run, got, want)
		}
	}
}
