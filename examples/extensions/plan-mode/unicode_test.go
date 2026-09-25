package planmode

import (
	"strings"
	"testing"
)

// The whitespace set and ASCII-only /i cases are from exact upstream utils.ts (no /u flag).
func TestPlanModeJavaScriptCommandPatterns(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    bool
	}{
		{"SED -n '1p' file", true},
		{"sed -N '1p' file", true},
		{"\u00a0ls", true},
		{"git\u00a0status", true},
		{"wget -O- https://example.invalid", true},
		{"wget -o- https://example.invalid", true},
		{"echo ok; npm\u00a0install package", false},
		{"echo ok; service\u00a0thing\u00a0restart", false},
		{"echo ok; git branch\u00a0-D name", false},
		{"\u0085ls", false},
		{"LS", false},
		{"ſed -n '1p' file", false},
		{"echo Kill", true},
		{"echo ſudo", true},
		{"echo '<>'", true},
		{"echo '<>>'", false},
	} {
		t.Run(tc.command, func(t *testing.T) {
			if got := IsSafeCommand(tc.command); got != tc.want {
				t.Errorf("IsSafeCommand(%q) = %v, want %v", tc.command, got, tc.want)
			}
			host := startExtensionHost(t, []string{"read", "bash"})
			host.invokeCommand("plan")
			result := host.invokeEvent("tool_call", map[string]any{"toolName": "bash", "input": map[string]any{"command": tc.command}})
			blocked := strings.Contains(string(result), `"block":true`)
			if blocked == tc.want {
				t.Errorf("tool_call(%q) = %s, want allowed=%v", tc.command, result, tc.want)
			}
		})
	}
}

func TestCleanStepTextJavaScriptUnicode(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"first\u00a0second", "First second"},
		{"ßeta", "SSeta"},
		{"ﬃrst", "FFIrst"},
		{"ǰoin", "J\u030coin"},
		{"\ufefffirst", "First"},
		{"\u0085first\u0085", "\u0085first\u0085"},
		{"\u001cfirst\u001c", "\u001cfirst\u001c"},
		{"Check\u00a0the\ufeffresult", "Result"},
		{"ChecK the result", "ChecK the result"},
		{"inſtall the result", "Inſtall the result"},
		{"😀first", "😀first"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			if got := CleanStepText(tc.text); got != tc.want {
				t.Fatalf("CleanStepText(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
	for _, space := range "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff" {
		if got := CleanStepText("first" + string(space) + "second"); got != "First second" {
			t.Errorf("JS whitespace U+%04X: %q", space, got)
		}
	}
}

func TestExtractTodoItemsJavaScriptWhitespaceAndLineStarts(t *testing.T) {
	for _, input := range []string{
		"Plan:\ufeff\n1.\u00a0Inspect the implementation",
		"Plan:\nNot a step*\r1. Inspect the implementation",
		"Plan:\nNot a step*\u20281. Inspect the implementation",
		"Plan:\nNot a step*\u20291. Inspect the implementation",
	} {
		items := ExtractTodoItems(input)
		if len(items) != 1 || items[0].Text != "Inspect the implementation" {
			t.Errorf("ExtractTodoItems(%q) = %+v", input, items)
		}
	}
}

func TestPlanModeRefinementUsesJavaScriptTrim(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"\ufeff\u00a0Add a regression test.\ufeff", "Add a regression test."},
		{"\ufeff", ""},
		{"\u0085", "\u0085"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			host := startExtensionHost(t, []string{"read"})
			host.selectValue = "Refine the plan"
			host.editorValue = tc.input
			host.invokeCommand("plan")
			host.invokeEvent("agent_end", map[string]any{"messages": []any{assistantMessage("Plan:\n1. Inspect the implementation")}})
			calls := host.callsFor("sendUserMessage")
			if tc.want == "" {
				if len(calls) != 0 {
					t.Fatalf("blank refinement sent: %v", calls)
				}
			} else if len(calls) != 1 || calls[0].args["content"] != tc.want {
				t.Fatalf("refinement = %v, want %q", calls, tc.want)
			}
		})
	}
}
