package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestQuoteIfNeeded(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"plain", "plain"},
		{"/home/user/.pig/sessions", "/home/user/.pig/sessions"},
		{"a-b_c.d~e:f@g", "a-b_c.d~e:f@g"},
		{"", "''"},
		{"has space", "'has space'"},
		{"weird;rm -rf", "'weird;rm -rf'"},
		{"it's", `'it'\''s'`},
	}
	for _, tc := range cases {
		if got := quoteIfNeeded(tc.in); got != tc.want {
			t.Errorf("quoteIfNeeded(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResumeCommand(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "sess.jsonl")
	if err := os.WriteFile(existing, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "gone.jsonl")

	t.Run("empty path returns empty (not persisted)", func(t *testing.T) {
		if got := resumeCommand("", "abc", ""); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
	t.Run("missing file returns empty", func(t *testing.T) {
		if got := resumeCommand(missing, "abc", ""); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
	t.Run("default dir omits --session-dir", func(t *testing.T) {
		got := resumeCommand(existing, "abc123", "")
		if want := "pig --session abc123"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("custom dir includes quoted --session-dir", func(t *testing.T) {
		got := resumeCommand(existing, "abc123", "/my sessions/dir")
		if want := "pig --session-dir '/my sessions/dir' --session abc123"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}
