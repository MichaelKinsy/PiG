package codingagent

import "testing"

// interactive-mode.ts handleNameCommand: `text.replace(/^\/name\s*/, "").trim()`. \s and trim strip JS whitespace (U+FEFF, U+00A0, U+2028 ...) and not U+0085.
func TestNameHandlerTrimsLikeJavaScript(t *testing.T) {
	for _, tc := range []struct{ args, want string }{
		{"\ufeffwork\ufeff", "work"},
		{"\u00a0work\u00a0", "work"},
		{"\u2028work\u2029", "work"},
		{"\u0085work\u0085", "\u0085work\u0085"},
		{"  two words  ", "two words"},
	} {
		var set []string
		sc := &SlashContext{
			Args:           tc.args,
			CurrentSession: func() *Session { return nil },
			SetSessionName: func(name string) error { set = append(set, name); return nil },
			Append:         func(string) {},
			ShowWarning:    func(string) {},
		}
		if err := nameHandler(sc); err != nil {
			t.Fatal(err)
		}
		if len(set) != 1 || set[0] != tc.want {
			t.Errorf("/name %q set %q, want %q", tc.args, set, tc.want)
		}
	}
}
