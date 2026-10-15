package piglet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// `pig piglet show` previews an inline system prompt cut at 77 UTF-16 units. The cut never splits a character into invalid UTF-8: a cut between
// the halves of a surrogate pair writes U+FFFD, as Node writes a lone surrogate.
func TestShowCutsTheSystemPromptPreviewAtCharacters(t *testing.T) {
	// cmdShow validates the installed Piglet inventory under PIG_HOME and the project; the caller's home and directory must not decide the result.
	t.Setenv("PIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	for _, tc := range []struct{ text, want string }{
		{strings.Repeat("界", 100), strings.Repeat("界", 77) + "..."},
		{strings.Repeat("😀", 50), strings.Repeat("😀", 38) + "\uFFFD..."},
	} {
		t.Setenv("PIG_HOME", t.TempDir())
		path := filepath.Join(t.TempDir(), "preview.yaml")
		if err := os.WriteFile(path, []byte("name: preview\nsystemPrompt:\n  text: "+tc.text+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, errOut strings.Builder
		if code := cmdShow([]string{path}, &out, &errOut); code != 0 {
			t.Fatalf("show code=%d err=%s", code, errOut.String())
		}
		if want := "System prompt: " + tc.want + " ("; !utf8.ValidString(out.String()) || !strings.Contains(out.String(), want) {
			t.Fatalf("show =\n%s\nwant a line starting %q", out.String(), want)
		}
	}
}
