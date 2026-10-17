package requestvalidity

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func rules(violations []Violation) string {
	names := make([]string, len(violations))
	for i, v := range violations {
		names[i] = v.Rule
	}
	return strings.Join(names, ",")
}

// Each case is a request body written from the provider's documented contract, not captured from PiG output.
func TestCheckBodyContracts(t *testing.T) {
	cases := []struct {
		name string
		api  string
		body string
		want string
	}{
		{"anthropic valid parallel", "anthropic-messages", `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"tool_use","id":"a"},{"type":"tool_use","id":"b"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"a"},{"type":"tool_result","tool_use_id":"b"},{"type":"text","text":"go on"}]}]}`, ""},
		// The owner's HTTP 400: results b and c after a user message that follows result a.
		{"anthropic result after summary", "anthropic-messages", `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"tool_use","id":"a"},{"type":"tool_use","id":"b"},{"type":"tool_use","id":"c"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"a"},{"type":"text","text":"summary"},{"type":"tool_result","tool_use_id":"b"},{"type":"tool_result","tool_use_id":"c"}]}]}`, "result-first"},
		{"anthropic result in later message", "anthropic-messages", `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"tool_use","id":"a"},{"type":"tool_use","id":"b"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"a"}]},{"role":"assistant","content":[{"type":"text","text":"x"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"b"}]}]}`, "call-has-result,result-has-call"},
		{"anthropic user text between", "anthropic-messages", `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"tool_use","id":"a"}]},{"role":"user","content":"interrupt"},{"role":"user","content":[{"type":"tool_result","tool_use_id":"a"}]}]}`, "between-call-and-result,result-has-call"},
		{"anthropic duplicate id", "anthropic-messages", `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"tool_use","id":"a"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"a"}]},{"role":"assistant","content":[{"type":"tool_use","id":"a"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"a"}]}]}`, "duplicate-call-id"},
		{"anthropic bad id", "anthropic-messages", `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"tool_use","id":"call 1|x"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call 1|x"}]}]}`, "call-id-format"},
		{"anthropic empty text", "anthropic-messages", `{"messages":[{"role":"user","content":[{"type":"text","text":""}]}]}`, "non-empty-content"},
		{"anthropic ends with assistant", "anthropic-messages", `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":[{"type":"text","text":"x"}]}]}`, "last-role"},
		{"anthropic tool role", "anthropic-messages", `{"messages":[{"role":"tool","content":"x"},{"role":"user","content":"hi"}]}`, "role-allowed"},

		{"chat valid", "openai-completions", `{"messages":[{"role":"system","content":"s"},{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"id":"a"},{"id":"b"}]},{"role":"tool","tool_call_id":"a"},{"role":"tool","tool_call_id":"b"},{"role":"user","content":"next"}]}`, ""},
		{"chat system between", "openai-completions", `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"id":"a"}]},{"role":"system","content":"update"},{"role":"tool","tool_call_id":"a"}]}`, "between-call-and-result,result-has-call"},
		{"chat missing result", "openai-completions", `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"id":"a"},{"id":"b"}]},{"role":"tool","tool_call_id":"a"},{"role":"user","content":"next"}]}`, "between-call-and-result"},
		{"chat orphan tool", "openai-completions", `{"messages":[{"role":"user","content":"hi"},{"role":"tool","tool_call_id":"a"}]}`, "result-has-call"},
		{"chat duplicate result", "openai-completions", `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"id":"a"}]},{"role":"tool","tool_call_id":"a"},{"role":"tool","tool_call_id":"a"}]}`, "duplicate-result"},

		{"mistral id format", "mistral-conversations", `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"id":"short"}]},{"role":"tool","tool_call_id":"short"}]}`, "call-id-format"},
		{"mistral ends assistant", "mistral-conversations", `{"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"x"}]}`, "last-role"},

		{"responses valid", "openai-responses", `{"input":[{"role":"user","content":"hi"},{"type":"reasoning"},{"type":"function_call","call_id":"a"},{"type":"function_call","call_id":"b"},{"type":"function_call_output","call_id":"a"},{"type":"function_call_output","call_id":"b"},{"role":"user","content":"next"}]}`, ""},
		// One assistant message whose text and reasoning follow its call: Pi emits the items in block order and the API documents no error for it.
		{"responses text after call", "openai-responses", `{"input":[{"role":"user","content":"hi"},{"type":"function_call","call_id":"a"},{"type":"message","role":"assistant","content":[]},{"type":"reasoning"},{"type":"function_call","call_id":"b"},{"type":"function_call_output","call_id":"a"},{"type":"function_call_output","call_id":"b"}]}`, ""},
		{"responses user between", "openai-responses", `{"input":[{"role":"user","content":"hi"},{"type":"function_call","call_id":"a"},{"role":"user","content":"x"},{"type":"function_call_output","call_id":"a"}]}`, "between-call-and-result,result-has-call"},
		{"responses missing output", "openai-responses", `{"input":[{"role":"user","content":"hi"},{"type":"function_call","call_id":"a"}]}`, "call-has-result"},
		{"responses orphan output", "openai-codex-responses", `{"input":[{"role":"user","content":"hi"},{"type":"function_call_output","call_id":"a"}]}`, "result-has-call"},

		{"google valid", "google-generative-ai", `{"contents":[{"role":"user","parts":[{"text":"hi"}]},{"role":"model","parts":[{"functionCall":{"name":"f"}},{"functionCall":{"name":"g"}}]},{"role":"user","parts":[{"functionResponse":{"name":"f"}},{"functionResponse":{"name":"g"}}]}]}`, ""},
		{"google response count", "google-vertex", `{"contents":[{"role":"user","parts":[{"text":"hi"}]},{"role":"model","parts":[{"functionCall":{"name":"f"}},{"functionCall":{"name":"g"}}]},{"role":"user","parts":[{"functionResponse":{"name":"f"}}]}]}`, "call-has-result"},
		{"google call after model", "google-generative-ai", `{"contents":[{"role":"user","parts":[{"text":"hi"}]},{"role":"model","parts":[{"text":"x"}]},{"role":"model","parts":[{"functionCall":{"name":"f"}}]},{"role":"user","parts":[{"functionResponse":{"name":"f"}}]}]}`, "call-turn-order"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rules(CheckBody(tc.api, []byte(tc.body))); got != tc.want {
				t.Fatalf("violations = %q, want %q: %v", got, tc.want, CheckBody(tc.api, []byte(tc.body)))
			}
		})
	}
}

func TestBedrockContract(t *testing.T) {
	call := Turn{Role: "assistant", Calls: []string{"a"}}
	result := Turn{Role: "user", Results: []string{"a"}}
	if got := rules(Bedrock.Check([]Turn{{Role: "user"}, call, result, {Role: "assistant"}, {Role: "user"}})); got != "" {
		t.Fatalf("valid conversation: %q", got)
	}
	if got := rules(Bedrock.Check([]Turn{{Role: "user"}, {Role: "user"}})); got != "role-alternation" {
		t.Fatalf("repeated role: %q", got)
	}
	if got := rules(Bedrock.Check([]Turn{{Role: "assistant"}, {Role: "user"}})); got != "first-role" {
		t.Fatalf("first role: %q", got)
	}
}

func TestEnforceIsOnInTests(t *testing.T) {
	if !Enabled() {
		t.Fatal("the checker must be on in a test binary")
	}
	// Enforce with a violation and no collector ends the process; TestFailEndsTheProcess runs it in a child.
	Enforce(Allow(context.Background()), "anthropic-messages", []Violation{{Rule: "result-has-call", Detail: "x"}})
	Enforce(context.Background(), "anthropic-messages", nil)
}

// An agent loop recovers a provider's panic into an error turn; the violation must still stop the process.
func TestFailEndsTheProcessEvenInsideARecover(t *testing.T) {
	if os.Getenv("REQUESTVALIDITY_CHILD") == "1" {
		func() {
			defer func() { _ = recover() }()
			Enforce(context.Background(), "anthropic-messages", []Violation{{Rule: "result-has-call", Detail: "orphan"}})
		}()
		os.Exit(0)
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestFailEndsTheProcessEvenInsideARecover$")
	cmd.Env = append(os.Environ(), "REQUESTVALIDITY_CHILD=1", "PIG_REQUEST_VALIDITY_REPORT=")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "requestvalidity: invalid anthropic-messages request") || !strings.Contains(string(out), "result-has-call: orphan") {
		t.Fatalf("the child survived or did not name the violation: err=%v\n%s", err, out)
	}
}

func TestEnforceReport(t *testing.T) {
	path := t.TempDir() + "/report.tsv"
	t.Setenv("PIG_REQUEST_VALIDITY_REPORT", path)
	Enforce(context.Background(), "openai-completions", []Violation{{Rule: "call-has-result", Detail: "line1\nline2"}})
	data, err := os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(data), "openai-completions\tcall-has-result\tline1 line2\t") {
		t.Fatalf("report = %q, %v", data, err)
	}
}

func TestCollectRecordsInsteadOfPanicking(t *testing.T) {
	ctx, collector := Collect(context.Background())
	Enforce(ctx, "openai-completions", []Violation{{Rule: "call-has-result", Detail: "a"}})
	got := collector.Violations()
	if len(got) != 1 || got[0] != "openai-completions call-has-result: a" {
		t.Fatalf("violations = %q", got)
	}
}

func TestCollectAllRecordsRequestsWhoseContextCarriesNoCollector(t *testing.T) {
	collector := CollectAll(t)
	Enforce(context.Background(), "openai-completions", []Violation{{Rule: "duplicate-result", Detail: "b"}})
	got := collector.Violations()
	if len(got) != 1 || got[0] != "openai-completions duplicate-result: b" {
		t.Fatalf("violations = %q", got)
	}
}
