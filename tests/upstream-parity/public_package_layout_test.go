package parity

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestPublicPackageDocumentation(t *testing.T) {
	root := pigRepoRoot(t)
	want := map[string]string{
		"./agent":                             "Package agent ",
		"./ai":                                "Package ai ",
		"./coding":                            "Package coding ",
		"./coding/extension":                  "Package extension ",
		"./coding/extension/host/runtimecell": "Package runtimecell ",
		"./coding/pigletbuild":                "Package pigletbuild ",
		"./cmd/pig":                           "Command pig ",
		"./tui":                               "Package tui ",
	}
	for packagePath, prefix := range want {
		cmd := exec.Command("go", "list", "-f", "{{.Doc}}", packagePath)
		cmd.Dir = root
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list %s: %v", packagePath, err)
		}
		if doc := strings.TrimSpace(string(output)); !strings.HasPrefix(doc, prefix) {
			t.Errorf("%s documentation = %q, want prefix %q", packagePath, doc, prefix)
		}
	}
}

func TestPublicPackageLayout(t *testing.T) {
	root := pigRepoRoot(t)
	for _, oldPath := range []string{"agentcore", "internal/tui", "pigproc", "resources", "coding/presentation", "schema/gui-extension"} {
		if _, err := os.Stat(filepath.Join(root, oldPath)); !os.IsNotExist(err) {
			t.Errorf("retired path %s still exists", oldPath)
		}
	}
	for _, currentPath := range []string{"agent", "ai", "coding", "tui", "piglets/standard", "piglets/porter"} {
		if info, err := os.Stat(filepath.Join(root, currentPath)); err != nil || !info.IsDir() {
			t.Errorf("public path %s is not a directory", currentPath)
		}
	}

	_ = agent.NewAgent(agent.AgentOptions{})
	_ = tui.NewText("public")

	packageDirs, err := os.ReadDir(filepath.Join(root, ".upstream", "current", "packages"))
	if err != nil {
		t.Fatal(err)
	}
	portMap, err := os.ReadFile(filepath.Join(root, "PORT_MAP.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range packageDirs {
		if !entry.IsDir() {
			continue
		}
		marker := "`packages/" + entry.Name() + "`"
		if !strings.Contains(string(portMap), marker) {
			t.Errorf("PORT_MAP.md does not state the scope of upstream package %s", entry.Name())
		}
	}
}
