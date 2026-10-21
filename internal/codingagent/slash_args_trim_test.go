package codingagent

import "testing"

// interactive-mode.ts:3243 (/bug) and :3310 (/compact) take the text after the command with String.prototype.trim, which strips U+FEFF, U+00A0 and the
// line separators but keeps U+0085.
var slashArgsTrimCases = []struct{ args, want string }{
	{"\ufeffwork\ufeff", "work"},
	{"\u00a0work\u2028", "work"},
	{"\u0085work\u0085", "\u0085work\u0085"},
	{"  two words  ", "two words"},
}

func TestCompactHandlerTrimsInstructionsLikeJavaScript(t *testing.T) {
	for _, tc := range slashArgsTrimCases {
		var got []string
		sc := &SlashContext{Args: tc.args, Append: func(string) {}, CompactSession: func(instructions string) error { got = append(got, instructions); return nil }}
		if err := compactHandler(sc); err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("/compact %q compacted with %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestBugCommandPrefillsItsHintTrimmedLikeJavaScript(t *testing.T) {
	for _, tc := range slashArgsTrimCases {
		var prefill []string
		sc := &SlashContext{Args: tc.args, ShowBugReportEditor: func(_, _, text string) (string, bool) { prefill = append(prefill, text); return "", false }}
		promptBugReportChoices(sc)
		if len(prefill) != 1 || prefill[0] != tc.want {
			t.Errorf("/bug %q prefilled %q, want %q", tc.args, prefill, tc.want)
		}
	}
}
