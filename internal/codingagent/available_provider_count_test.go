package codingagent

import (
	"os"
	"strings"
	"testing"
)

// processEssentialEnv names the variables the OS needs to resolve temp and system directories. Windows falls back to C:\\WINDOWS for t.TempDir
// when TEMP, TMP, USERPROFILE and SystemRoot are empty, and the write is then denied. Names are compared upper-cased (Windows names are case-insensitive).
var processEssentialEnv = map[string]bool{
	"PATH": true, "HOME": true, "TMPDIR": true, "TEMP": true, "TMP": true, "USERPROFILE": true, "SYSTEMROOT": true, "WINDIR": true,
}

// AvailableProviderCount counts every provider with configured auth, as Pi's updateAvailableProviderCount counts the providers of
// getAvailable() (interactive-mode.ts:5252-5256). A built-in provider outside the seven the registry used to list (xAI, Mistral,
// DeepSeek and so on) counts too, so /sprite create does not report "no configured provider" for a user whose only key is one of them.
func TestAvailableProviderCountCountsEveryBuiltInProvider(t *testing.T) {
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		// Windows keeps per-drive working directories as variables named "=C:"; the entry splits to an empty name, and Setenv rejects it (ERROR_INVALID_PARAMETER).
		if name == "" || processEssentialEnv[strings.ToUpper(name)] {
			continue
		}
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", t.TempDir())
	for _, env := range []string{"XAI_API_KEY", "MISTRAL_API_KEY", "DEEPSEEK_API_KEY"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, "k")
			if got := NewModelRegistry(t.TempDir()).AvailableProviderCount(); got != 1 {
				t.Errorf("AvailableProviderCount with only %s = %d, want 1", env, got)
			}
		})
	}
}
