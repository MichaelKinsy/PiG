package codingagent

import "testing"

// bug-report.ts collectBugReportMetadata: `options.hint?.trim() || null`. String.prototype.trim strips U+FEFF and keeps U+0085.
func TestBugReportMetadataHintTrimsLikeJavaScript(t *testing.T) {
	for _, tc := range []struct {
		hint string
		want *string
	}{
		{"\ufeff\u00a0what broke\u2028", new("what broke")},
		{"\u0085what broke\u0085", new("\u0085what broke\u0085")},
		{"\ufeff \u2029", nil},
		{"\u0085", new("\u0085")},
		{"", nil},
	} {
		metadata, err := CollectBugReportMetadata(BugReportInputs{}, BugReportOptions{Hint: tc.hint}, false, fixedBugReportTime())
		if err != nil {
			t.Fatal(err)
		}
		switch got := metadata.Hint; {
		case got == nil && tc.want == nil:
		case got == nil || tc.want == nil || *got != *tc.want:
			t.Errorf("hint %q = %v, want %v", tc.hint, got, tc.want)
		}
	}
}

// /bug trims its arguments for the editor's prefill and the editor's answer with String.prototype.trim (interactive-mode.ts:3243 `.trim()`,
// bug-report.ts:133 `hint.trim()`), which keeps U+0085: Go's strings.TrimSpace would strip it and lose a one-character hint.
func TestBugReportPromptTrimsLikeJavaScript(t *testing.T) {
	var prefill string
	sc := &SlashContext{
		Args: "\ufeff\u0085 broke\u2028",
		ShowBugReportEditor: func(_, _, initial string) (string, bool) {
			prefill = initial
			return "\ufeff\u0085\u00a0", true
		},
		ShowBugReportSelector: func(_ string, options []string, _ string) (string, bool) { return options[0], true },
	}
	choices, ok := promptBugReportChoices(sc)
	if !ok {
		t.Fatal("the prompt was cancelled")
	}
	if prefill != "\u0085 broke" {
		t.Errorf("prefill = %q, want %q", prefill, "\u0085 broke")
	}
	if choices.hint != "\u0085" {
		t.Errorf("hint = %q, want %q", choices.hint, "\u0085")
	}
}
