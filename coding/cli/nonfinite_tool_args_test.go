package cli

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// nonFiniteProvider streams one tool call whose arguments hold the infinities and NaN that Pi keeps in memory for `1e999`, then a text reply. Pi's JSON.stringify
// writes each as null in every event, session line and request: pi-coding-agent's SessionManager.appendMessage of the same call writes
// {"type":"toolCall","id":"c1","name":"t","arguments":{"n":null,"a":[null,null],"z":0,"k":0}}, and an agent_end or RPC event stringifies alike (captured with Pi's own
// SessionManager and Agent under node, pi 74f46864).
type nonFiniteProvider struct{ calls atomic.Int32 }

func (*nonFiniteProvider) ID() string   { return "faux" }
func (*nonFiniteProvider) Close() error { return nil }
func (p *nonFiniteProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	stream := ai.NewAssistantMessageEventStream()
	first := p.calls.Add(1) == 1
	go func() {
		base := ai.AssistantMessage{Provider: "faux", Model: "faux-1", API: "faux", StopReason: ai.StopReasonPending}
		_ = stream.Push(ai.StartEvent{Partial: &base})
		final := base
		if !first {
			final.Content = []ai.AssistantContentBlock{ai.TextContent{Text: "final"}}
			final.StopReason = ai.StopReasonStop
			_ = stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: &final})
			return
		}
		call := ai.ToolCall{ID: "c1", Name: "print_test_tool", Arguments: ai.JsonObject{"n": math.Inf(1), "a": []any{math.Inf(-1), math.NaN()}, "z": math.Copysign(0, -1)}}
		final.Content = []ai.AssistantContentBlock{call}
		final.StopReason = ai.StopReasonToolUse
		_ = stream.Push(ai.ToolCallStartEvent{ContentIndex: 0, Partial: &final})
		_ = stream.Push(ai.ToolCallEndEvent{ContentIndex: 0, ToolCall: call, Partial: &final})
		_ = stream.Push(ai.DoneEvent{Reason: ai.StopReasonToolUse, Message: &final})
	}()
	return stream, nil
}

var nonFiniteWantArguments = map[string]any{"n": nil, "a": []any{nil, nil}, "z": json.Number("0")}

func decodeNumbers(t *testing.T, text string) any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("%q: %v", text, err)
	}
	return value
}

// toolCallArguments finds the tool call c1 in a decoded event or session line and returns its arguments.
func toolCallArguments(value any) (arguments any, found bool) {
	switch value := value.(type) {
	case map[string]any:
		if value["type"] == "toolCall" && value["id"] == "c1" {
			return value["arguments"], true
		}
		if value["toolCallId"] == "c1" && value["args"] != nil {
			return value["args"], true
		}
		for _, item := range value {
			if arguments, found = toolCallArguments(item); found {
				return arguments, true
			}
		}
	case []any:
		for _, item := range value {
			if arguments, found = toolCallArguments(item); found {
				return arguments, true
			}
		}
	}
	return nil, false
}

// Print mode in JSON mode writes every event holding the call with null for each non-finite number, runs the tool, and the session file holds the same: the turn goes on.
func TestPrintJSONModeWritesNonFiniteToolArgumentsAsNull(t *testing.T) {
	host := printModeTestHost(t, &nonFiniteProvider{}, printTestTool{})
	result := runPrintModeForTest(t, host, printModeOptions{Mode: "json", InitialMessage: "go"})
	if result.err != nil || result.stderr != "" {
		t.Fatalf("run = %v, stderr %q", result.err, result.stderr)
	}
	seen := 0
	for line := range strings.SplitSeq(result.stdout, "\n") {
		if line == "" {
			continue
		}
		if strings.Contains(line, "Infinity") || strings.Contains(line, "NaN") {
			t.Fatalf("a line holds a non-JSON number: %.300s", line)
		}
		if arguments, found := toolCallArguments(decodeNumbers(t, line)); found {
			seen++
			if !reflect.DeepEqual(arguments, any(nonFiniteWantArguments)) {
				t.Errorf("arguments %v in %.200s, want %v", arguments, line, nonFiniteWantArguments)
			}
		}
	}
	if seen < 5 {
		t.Fatalf("only %d events held the tool call; stdout %s", seen, result.stdout)
	}
	if !strings.Contains(result.stdout, `"toolName":"print_test_tool","result":{"content":[{"type":"text","text":"tool output"}]},"isError":false`) {
		t.Fatalf("the tool did not run: %s", result.stdout)
	}
	files, _ := filepath.Glob(filepath.Join(host.Session.SessionDir, "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("session files %v", files)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	logged := 0
	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.Contains(line, `"c1"`) || !strings.Contains(line, `"toolCall"`) {
			continue
		}
		logged++
		if strings.Contains(line, "Infinity") || strings.Contains(line, "NaN") {
			t.Fatalf("session line holds a non-JSON number: %s", line)
		}
		if arguments, _ := toolCallArguments(decodeNumbers(t, line)); !reflect.DeepEqual(arguments, any(nonFiniteWantArguments)) {
			t.Errorf("session arguments %v, want %v", arguments, nonFiniteWantArguments)
		}
	}
	if logged == 0 {
		t.Fatalf("the session file has no tool call line: %s", data)
	}
}

// The RPC event frames of the same run carry null as well, and the next request (the tool call replayed to the provider) was built.
func TestRPCEventsWriteNonFiniteToolArgumentsAsNull(t *testing.T) {
	provider := &nonFiniteProvider{}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer services.Close()
	configureFauxAuth(t, services, provider.ID())
	session, err := coding.NewSession(services, coding.SessionOptions{
		Model:            &ai.Model{ID: "faux-1", Provider: provider, ProviderMeta: ai.ProviderMetadata{ProviderID: provider.ID()}, Capabilities: ai.ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 8192, SupportsToolUse: true}},
		SkipBuiltinTools: true, SessionDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var frames []string
	unsubscribe := subscribeRPCEvents(session, func(frame any) {
		raw, err := json.Marshal(frame)
		if err != nil {
			t.Errorf("frame %T: %v", frame, err)
			return
		}
		frames = append(frames, string(raw))
	}, func(err error) { t.Error(err) })
	defer unsubscribe()
	if _, err := session.Send(t.Context(), "go"); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, frame := range frames {
		if strings.Contains(frame, "Infinity") || strings.Contains(frame, "NaN") {
			t.Fatalf("a frame holds a non-JSON number: %.300s", frame)
		}
		if arguments, found := toolCallArguments(decodeNumbers(t, frame)); found {
			seen++
			if !reflect.DeepEqual(arguments, any(nonFiniteWantArguments)) {
				t.Errorf("arguments %v in %.200s, want %v", arguments, frame, nonFiniteWantArguments)
			}
		}
	}
	if seen < 3 || provider.calls.Load() != 2 {
		t.Fatalf("%d frames held the tool call, %d model calls (want 2)", seen, provider.calls.Load())
	}
}
