package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/coding/pigletbuild"
	"github.com/MichaelKinsy/PiG/internal/npmpublish/npmtest"
)

// TestPigletPublishedToNPMIsAddedBackAndValidates is the end-to-end contract of
// npm publication: a Package and a Piglet that names it by local path are
// published to a local registry stub with the real commands, and
// `pig piglet add npm:<name>` registers exactly what publication produced,
// resolving the Package from the same registry.
func TestPigletPublishedToNPMIsAddedBackAndValidates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	fake := npmtest.Install(t)
	cwd := t.TempDir()
	t.Chdir(cwd)

	author := t.TempDir()
	packageDir := filepath.Join(author, "packages", "review")
	for name, body := range map[string]string{
		"packages/review/package.json":               goodPackageJSON,
		"packages/review/extensions/runner/index.js": "export default function extension(pi) {}\n",
		"porter.yaml":       "name: porter\ndescription: Hauls things\nrelease:\n  version: 1.2.3\nsystemPrompt:\n  file: prompts/system.md\npackages:\n  review: local:./packages/review\nextensions:\n  - name: runner\n    origins: [package:review]\n",
		"prompts/system.md": "You haul things.\n",
		"README.md":         "# Porter\n",
	} {
		path := filepath.Join(author, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pigletPath := filepath.Join(author, "porter.yaml")

	// 1. The Piglet cannot be published before its Package is on npm, and nothing is published.
	var stdout, stderr strings.Builder
	if code := pigletbuild.RunPigletPublishCommand([]string{pigletPath, "--to", "npm", "--yes"}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), `Package "review" (npm:@acme/review@^1.2.0) is not on npm; publish it first`) {
		t.Fatalf("publish Piglet before its Package: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if len(fake.Publishes()) != 0 {
		t.Fatalf("published: %v", fake.Publishes())
	}

	// 2. Publish the Package with the helper, then the Piglet, then add the Piglet from npm.
	if code, out, errOut := runPackagePublishForTest(t, packageDir, "--to", "npm", "--yes"); code != 0 {
		t.Fatalf("publish Package: code=%d stdout=%s stderr=%s", code, out, errOut)
	}
	stdout.Reset()
	stderr.Reset()
	if code := pigletbuild.RunPigletPublishCommand([]string{pigletPath, "--to", "npm", "--yes"}, &stdout, &stderr); code != 0 {
		t.Fatalf("publish Piglet: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var added, addErr strings.Builder
	if code := piglet.RunCommand([]string{"piglet", "add", "npm:porter", "--no-input"}, &added, &addErr); code != 0 {
		t.Fatalf("add: code=%d stdout=%s stderr=%s", code, added.String(), addErr.String())
	}
	if !strings.Contains(added.String(), "Added porter (1 extensions, 0 skills)") {
		t.Fatalf("add output: %s", added.String())
	}

	// 3. What was registered is the generated source, its origin is the npm version, and its prompt came along.
	registered, err := os.ReadFile(filepath.Join(home, "piglets", "porter.yaml"))
	if err != nil || !strings.Contains(string(registered), "review: npm:@acme/review@^1.2.0") || strings.Contains(string(registered), "local:") {
		t.Fatalf("registered Piglet = %q, %v", registered, err)
	}
	if prompt, err := os.ReadFile(filepath.Join(home, "piglets", "prompts", "system.md")); err != nil || string(prompt) != "You haul things.\n" {
		t.Fatalf("registered prompt = %q, %v", prompt, err)
	}
	var origin struct {
		Source          string `json:"source"`
		ResolvedVersion string `json:"resolvedVersion"`
		Integrity       string `json:"integrity"`
	}
	data, err := os.ReadFile(filepath.Join(home, "piglets", "porter.origin.json"))
	if err != nil || json.Unmarshal(data, &origin) != nil {
		t.Fatalf("origin record: %s, %v", data, err)
	}
	if origin.Source != "npm:porter" || origin.ResolvedVersion != "1.2.3" || !strings.HasPrefix(origin.Integrity, "sha512-fake-porter-1.2.3") {
		t.Fatalf("origin = %+v", origin)
	}

	// 4. The registered Piglet validates: its Package resolves from the registry too.
	stdout.Reset()
	stderr.Reset()
	if code := piglet.RunCommand([]string{"piglet", "validate", "porter"}, &stdout, &stderr); code != 0 {
		t.Fatalf("validate: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}

	installed := map[string]bool{}
	for _, call := range fake.Calls() {
		if len(call.Args) > 1 && call.Args[0] == "install" {
			installed[call.Args[1]] = true
		}
	}
	if !installed["porter"] || !installed["@acme/review@^1.2.0"] {
		t.Fatalf("npm installs = %v, want the Piglet and its Package from the registry", installed)
	}

	// 5. Publication is safe to repeat: the same name@version is refused.
	stdout.Reset()
	stderr.Reset()
	if code := pigletbuild.RunPigletPublishCommand([]string{pigletPath, "--to", "npm", "--yes"}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "porter@1.2.3 is already on npm") {
		t.Fatalf("republish: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if got := len(fake.Publishes()); got != 2 {
		t.Fatalf("publishes = %d, want the Piglet and the Package once each", got)
	}
}
