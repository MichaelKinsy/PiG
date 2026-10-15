package docsdrift

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every variable install.sh documents in its own header is a variable the install pages must explain, because a user who pipes the script to sh has no other place to learn it.
func TestInstallScriptVariablesAreDocumented(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "docs", "site", "public", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	header, _, _ := strings.Cut(string(data), "\n#\n# On Android")
	vars := regexp.MustCompile(`(?m)^#   (PIG_[A-Z_]+)\s`).FindAllStringSubmatch(header, -1)
	if len(vars) < 6 {
		t.Fatalf("read %d variables from the install.sh header; the reader is broken", len(vars))
	}
	pages := map[string]string{}
	for _, src := range corpus(t) {
		if src.Rel == "docs/site/docs/install-troubleshooting.md" || src.Rel == "internal/pigdocs/content/install.md" || src.Rel == "internal/pigdocs/content/troubleshooting.md" {
			pages[src.Rel] = src.Body
		}
	}
	if len(pages) != 3 {
		t.Fatalf("the install pages are missing from the corpus: %d of 3", len(pages))
	}
	for rel, body := range pages {
		for _, v := range vars {
			if !strings.Contains(body, "`"+v[1]+"`") {
				t.Errorf("%s does not explain %s, which install.sh reads", rel, v[1])
			}
		}
	}
	// The script must read what its header promises: a documented variable that the code ignores is worse than a missing one.
	for _, v := range vars {
		if strings.Count(string(data), v[1]) < 2 {
			t.Errorf("install.sh documents %s in its header but never reads it", v[1])
		}
	}
}
