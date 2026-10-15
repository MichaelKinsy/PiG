package codingagent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
	"github.com/MichaelKinsy/PiG/internal/pigdocs"
)

// The docs section of every system prompt (session, extension and durable prompts leave PigDocsPath empty and take the default) names the directory GetDocsPath returns. A leading ~ in PIG_HOME or XDG_CONFIG_HOME is expanded in both, so a prompt never names a literal ~/x/docs.
func TestPromptDocsSectionNamesGetDocsPathWithATildeConfigRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, tc := range []struct{ name, pigHome, xdg, want string }{
		{"PIG_HOME", "~/x", "", pigdocs.DocsDir(filepath.Join(home, "x"))},
		{"XDG_CONFIG_HOME", "", "~/cfg", pigdocs.DocsDir(filepath.Join(home, "cfg", "pig"))},
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
	if !filepath.IsAbs(got) || filepath.Base(filepath.Dir(got)) != "docs" || !strings.Contains(got, "relative-home") {
		t.Fatalf("GetDocsPath = %q, want an absolute path to a bundle in relative-home/docs", got)
	}
	if prompt := prompts.BuildDefaultPrompt(prompts.Options{}); !strings.Contains(prompt, "Additional docs: "+got+"\n") {
		t.Fatalf("the prompt's docs section does not name %q", got)
	}
}
