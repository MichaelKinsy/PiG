package piglet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The effective view of a resolved Piglet keeps the resolved local paths and
// the source metadata that lets them validate, and revalidates cleanly.
func TestPigletWithEffectiveDefaultsPreservesResolvedSourceMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "ext"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "piglet.yaml")
	if err := os.WriteFile(source, []byte("name: demo\nextensions:\n  - name: demo\n    origins: [local:./ext]\nagentEnv:\n  image: ghcr.io/acme/dev:1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolution, err := ResolveEffectiveWithOptions(source, ResolveOptions{Workspace: root})
	if err != nil {
		t.Fatal(err)
	}
	effective, err := pigletWithEffectiveDefaults(resolution.Piglet)
	if err != nil {
		t.Fatal(err)
	}
	if effective.SourcePath() != resolution.Piglet.SourcePath() || effective.SourcePath() == "" {
		t.Fatalf("source path = %q, want %q", effective.SourcePath(), resolution.Piglet.SourcePath())
	}
	if effective.AgentEnv == nil || effective.AgentEnv.PigRuntime == nil || effective.AgentEnv.PigRuntime.Mode != "inject" || effective.AgentEnv.Policy == nil || effective.AgentEnv.Policy.Preset != "standard" {
		t.Fatalf("effective agent environment defaults missing: %#v", effective.AgentEnv)
	}
	if resolution.Piglet.AgentEnv.PigRuntime != nil {
		t.Fatal("effective view mutated the resolved Piglet")
	}
	if err := effective.Validate(); err != nil {
		t.Fatalf("effective Piglet does not revalidate: %v", err)
	}
	if effective == resolution.Piglet {
		t.Fatal("effective view aliases the resolved Piglet")
	}
}

// Issue #98 through extends: each lineage source anchors its own local
// origins before the merge, so the effective view holds absolute origins from
// two directories plus an inherited package alias. Every one must render, in
// both output modes, at the exact anchored path.
func TestRunCommandShowEffectiveAcceptsInheritedLocalPaths(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	for _, dir := range []string{"base/ext", "base/package", "base/skills/helper", "child/ext"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"base/piglet.yaml":  "name: base\nrelease:\n  version: 1.0.0\npackages:\n  shared: local:./package\nextensions:\n  - name: basic\n    origins: [local:./ext]\nskills:\n  - name: helper\n    origins: [local:./skills/helper]\n",
		"child/piglet.yaml": "name: child\nextends:\n  source: local:../base/piglet.yaml\n  version: ^1.0.0\nextensions:\n  - name: extra\n    origins: [package:shared, local:./ext]\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	local := func(path string) string { return "local:" + filepath.Join(root, filepath.FromSlash(path)) }
	source := filepath.Join(root, "child", "piglet.yaml")

	var stdout, stderr strings.Builder
	if code := RunCommand([]string{"piglet", "show", source, "--effective"}, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("human: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, line := range []string{
		"Piglet: child\n",
		"  basic  tools=all  origin=" + local("base/ext") + "\n",
		"  extra  tools=all  origin=package:shared\n",
		"  helper  origin=" + local("base/skills/helper") + "\n",
	} {
		if !strings.Contains(stdout.String(), line) {
			t.Fatalf("human output lacks %q:\n%s", line, stdout.String())
		}
	}

	stdout.Reset()
	if code := RunCommand([]string{"piglet", "show", source, "--effective", "--json"}, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("json: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var document struct {
		Effective bool `json:"effective"`
		Piglet    struct {
			Name       string            `json:"name"`
			Packages   map[string]string `json:"packages"`
			Extensions []struct {
				Name    string   `json:"name"`
				Origins []string `json:"origins"`
			} `json:"extensions"`
			Skills []struct {
				Name    string   `json:"name"`
				Origins []string `json:"origins"`
			} `json:"skills"`
		} `json:"piglet"`
	}
	if err := json.Unmarshal([]byte(stdout.String()), &document); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout.String())
	}
	got := document.Piglet
	if !document.Effective || got.Name != "child" || len(got.Packages) != 1 || got.Packages["shared"] != local("base/package") ||
		len(got.Extensions) != 2 || got.Extensions[0].Name != "basic" || !slices.Equal(got.Extensions[0].Origins, []string{local("base/ext")}) ||
		got.Extensions[1].Name != "extra" || !slices.Equal(got.Extensions[1].Origins, []string{"package:shared", local("child/ext")}) ||
		len(got.Skills) != 1 || got.Skills[0].Name != "helper" || !slices.Equal(got.Skills[0].Origins, []string{local("base/skills/helper")}) {
		t.Fatalf("effective JSON = %s", stdout.String())
	}
}
