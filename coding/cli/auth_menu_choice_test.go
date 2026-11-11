package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi's packages/ai/src/cli.ts reads a numbered menu answer with `Number.parseInt(answer, 10) - 1` (:41 for a select prompt, :109 for the provider list), so the leading
// digits after JavaScript whitespace and an optional sign count and whatever follows is ignored. The expected choices are what Node 24 returns for
// Number.parseInt(answer, 10): "2abc" 2, " 3" 3, "\u00a02" 2, "+1" 1, "1.9" 1, "2e1" 2, "0x2" 0, "-1" -1, "" NaN and an Arabic-Indic digit NaN.
func TestMenuChoiceReadsTheAnswerAsParseInt(t *testing.T) {
	for _, c := range []struct {
		answer string
		count  int
		want   int
		ok     bool
	}{
		{"2abc", 3, 2, true},
		{" 3", 3, 3, true},
		{"\u00a02", 3, 2, true},
		{"\ufeff1", 3, 1, true},
		{"+1", 3, 1, true},
		{"1.9", 3, 1, true},
		{"2e1", 3, 2, true},
		{"3", 3, 3, true},
		{"4", 3, 0, false},
		{"0", 3, 0, false},
		{"0x2", 3, 0, false},
		{"-1", 3, 0, false},
		{"", 3, 0, false},
		{"x", 3, 0, false},
		{"\u0662", 3, 0, false},
	} {
		got, ok := menuChoice(c.answer, c.count)
		if got != c.want || ok != c.ok {
			t.Errorf("menuChoice(%q, %d) = %d, %v; want %d, %v", c.answer, c.count, got, ok, c.want, c.ok)
		}
	}
}

// pig logout asks which stored OAuth provider to remove with the same numbered menu, so "2abc" picks the second provider as Pi's parseInt would.
func TestLogoutMenuTakesTheLeadingDigits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"anthropic":{"type":"oauth","refresh":"r","access":"a","expires":1},"kimi-coding":{"type":"oauth","access":"a","refresh":"r","expires":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := ai.NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		answer, want string
		fails        bool
	}{
		{"2abc\n", "kimi-coding", false},
		{" 1\n", "anthropic", false},
		{"3\n", "", true},
		{"abc\n", "", true},
	} {
		read, write, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := write.WriteString(c.answer); err != nil {
			t.Fatal(err)
		}
		_ = write.Close()
		stdin := os.Stdin
		os.Stdin = read
		got, err := resolveLogoutProvider(store, "")
		os.Stdin = stdin
		_ = read.Close()
		if c.fails {
			if err == nil || err.Error() != "Invalid selection" {
				t.Errorf("answer %q: got %q, %v; want Invalid selection", c.answer, got, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("answer %q: got %q, %v; want %q", c.answer, got, err, c.want)
		}
	}
}
