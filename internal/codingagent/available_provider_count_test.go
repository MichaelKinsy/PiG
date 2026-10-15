package codingagent

import (
	"os"
	"strings"
	"testing"
)

// AvailableProviderCount counts every provider with configured auth, as Pi's updateAvailableProviderCount counts the providers of
// getAvailable() (interactive-mode.ts:5252-5256). A built-in provider outside the seven the registry used to list (xAI, Mistral,
// DeepSeek and so on) counts too, so /sprite create does not report "no configured provider" for a user whose only key is one of them.
func TestAvailableProviderCountCountsEveryBuiltInProvider(t *testing.T) {
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name == "PATH" || name == "HOME" || name == "TMPDIR" {
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
