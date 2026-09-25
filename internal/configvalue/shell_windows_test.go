//go:build windows

package configvalue

import "testing"

// Upstream runs a !command config value through the configured bash on
// win32, so bash syntax works there.
func TestResolveBangCmdUsesBashOnWindows(t *testing.T) {
	ClearCache()
	if got := Resolve(`!printf '%s' "$((2+3))"`, nil); got != "5" {
		t.Fatalf("bash arithmetic = %q, want 5", got)
	}
}

// With no bash to start, upstream falls back to execSync's default shell,
// cmd.exe, which expands %VAR%.
func TestResolveBangCmdFallsBackToCmdWithoutBash(t *testing.T) {
	ClearCache()
	empty := t.TempDir()
	t.Setenv("ProgramFiles", empty)
	t.Setenv("ProgramFiles(x86)", "")
	t.Setenv("PATH", empty)
	t.Setenv("PIG_CONFIG_VALUE_TEST", "from-cmd")
	if got := Resolve("!echo %PIG_CONFIG_VALUE_TEST%", nil); got != "from-cmd" {
		t.Fatalf("cmd.exe fallback = %q, want from-cmd", got)
	}
}
