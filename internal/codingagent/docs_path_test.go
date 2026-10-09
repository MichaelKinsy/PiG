package codingagent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
)

// Pi config.ts getDocsPath is the one directory every documentation page is joined to (auth-guidance.ts:9-10, system-prompt.ts:164, codemode/tool.ts:135). PiG's bundle lives under the config root (D22), so the path follows PIG_HOME, then XDG_CONFIG_HOME, then the home directory, and login guidance names pages beneath it.
func TestGetDocsPathFollowsTheConfigRootAndLoginGuidanceNamesItsPages(t *testing.T) {
	pigHome := t.TempDir()
	t.Setenv("PIG_HOME", pigHome)
	if got, want := GetDocsPath(), filepath.Join(pigHome, "docs"); got != want {
		t.Fatalf("GetDocsPath with PIG_HOME = %q, want %q", got, want)
	}
	help := ProviderLoginHelp()
	for _, page := range []string{"providers.md", "models.md"} {
		if want := filepath.Join(pigHome, "docs", page); !strings.Contains(help, want) {
			t.Fatalf("ProviderLoginHelp = %q, want it to name %q", help, want)
		}
	}

	xdg := t.TempDir()
	t.Setenv("PIG_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got, want := GetDocsPath(), filepath.Join(xdg, "pig", "docs"); got != want {
		t.Fatalf("GetDocsPath with XDG_CONFIG_HOME = %q, want %q", got, want)
	}
}

// The docs section of every system prompt (session, extension and durable prompts leave PigDocsPath empty and take the default) names the directory GetDocsPath returns. A leading ~ in PIG_HOME or XDG_CONFIG_HOME is expanded in both, so a prompt never names a literal ~/x/docs.
func TestPromptDocsSectionNamesGetDocsPathWithATildeConfigRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, tc := range []struct{ name, pigHome, xdg, want string }{
		{"PIG_HOME", "~/x", "", filepath.Join(home, "x", "docs")},
		{"XDG_CONFIG_HOME", "", "~/cfg", filepath.Join(home, "cfg", "pig", "docs")},
	} {
		t.Setenv("PIG_HOME", tc.pigHome)
		t.Setenv("XDG_CONFIG_HOME", tc.xdg)
		if got := GetDocsPath(); got != tc.want {
			t.Fatalf("%s: GetDocsPath = %q, want %q", tc.name, got, tc.want)
		}
		prompt := prompts.BuildDefaultPrompt(prompts.Options{})
		if !strings.Contains(prompt, "Additional docs: "+tc.want+"\n") {
			t.Errorf("%s: the prompt's docs section does not name %q:\n%s", tc.name, tc.want, prompt)
		}
		if strings.Contains(prompt, "~/") {
			t.Errorf("%s: the prompt names an unexpanded ~ path", tc.name)
		}
	}
}

// config.ts:449 getDocsPath is resolve(join(<package dir>, "docs")): absolute whatever the config root looks like. A relative PIG_HOME resolves against the working directory, and the system prompt's default docs path is the same directory.
func TestGetDocsPathIsAbsoluteForARelativeConfigRoot(t *testing.T) {
	t.Setenv("PIG_HOME", "relative-home")
	got := GetDocsPath()
	if !filepath.IsAbs(got) || filepath.Base(got) != "docs" || !strings.Contains(got, "relative-home") {
		t.Fatalf("GetDocsPath = %q, want an absolute path ending relative-home/docs", got)
	}
	if prompt := prompts.BuildDefaultPrompt(prompts.Options{}); !strings.Contains(prompt, "Additional docs: "+got+"\n") {
		t.Fatalf("the prompt's docs section does not name %q", got)
	}
}
