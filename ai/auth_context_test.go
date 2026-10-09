package ai

import (
	"os"
	"path/filepath"
	"testing"
)

// packages/ai/src/auth/context.ts:19-43 (defaultProviderAuthContext).
func TestDefaultProviderAuthContext(t *testing.T) {
	ctx := DefaultProviderAuthContext()
	// :23-26 an env value counts only when it is a string with a non-blank ECMAScript trim; a nonblank value is returned untrimmed.
	t.Setenv("PIG_CTX_SET", "  value \n")
	t.Setenv("PIG_CTX_BLANK", " \t\u00a0\ufeff\n")
	t.Setenv("PIG_CTX_EMPTY", "")
	if got, ok := ctx.Env("PIG_CTX_SET"); !ok || got != "  value \n" {
		t.Errorf("set value = %q, %v", got, ok)
	}
	for _, name := range []string{"PIG_CTX_BLANK", "PIG_CTX_EMPTY", "PIG_CTX_UNSET_ENTIRELY"} {
		if got, ok := ctx.Env(name); ok || got != "" {
			t.Errorf("%s = %q, %v; want absent", name, got, ok)
		}
	}

	// :28-40 file existence follows a leading "~" to the home directory; a missing file is false.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.WriteFile(filepath.Join(home, "token"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		want bool
	}{
		{filepath.Join(home, "token"), true},
		{"~/token", true},
		{"~" + string(filepath.Separator) + "dir", true},
		{"~/missing", false},
		{filepath.Join(home, "missing"), false},
		{"token", false},
	} {
		if got := ctx.FileExists(tc.path); got != tc.want {
			t.Errorf("FileExists(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
