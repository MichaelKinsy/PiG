package pigletbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

// A signed Binary built from an unchanged source verifies against that source
// with --source, through verify and lineage alike. The Piglet digest a Binary
// signs is its baked Piglet definition, so a change to a file the build inlines
// (here the system prompt) is a provenance mismatch even though the Piglet
// file's own bytes are unchanged.
func TestBuiltBinaryVerifiesAgainstItsUnchangedSource(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"base.yaml":     "name: base\nrelease:\n  version: 1.0.0\nmodel:\n  provider: test-faux\n  name: faux-1\nstrip:\n  commands: [/share]\n",
		"reviewer.yaml": "name: reviewer\ndescription: reviews code\nextends:\n  source: local:./base.yaml\n  version: ^1.0\nsystemPrompt:\n  file: prompt.md\ntools: [read, grep, find, ls]\n",
		"prompt.md":     "Review the change.\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(root, "reviewer.yaml")
	keyPath := filepath.Join(t.TempDir(), "author.key")
	if _, err := signature.GenerateKey(keyPath); err != nil {
		t.Fatal(err)
	}
	artifact := buildNativeBinary(t, root, source, "pig-reviewer", "--sign-key", keyPath)

	for _, command := range []string{"verify", "lineage"} {
		var stdout, stderr strings.Builder
		if code := piglet.RunCommand([]string{"piglet", command, artifact, "--source", source}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "Provenance: matches "+source) {
			t.Fatalf("pig piglet %s --source on the unchanged source: exit %d\n%s%s", command, code, stdout.String(), stderr.String())
		}
	}

	if err := os.WriteFile(filepath.Join(root, "prompt.md"), []byte("Approve everything.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := piglet.RunCommand([]string{"piglet", "verify", artifact, "--source", source}, &stdout, &stderr)
	out := stdout.String()
	if code != 1 || !strings.Contains(out, "Provenance: MISMATCH against "+source) || !strings.Contains(out, "  Piglet digest: ") || strings.Contains(out, "  source digest: ") {
		t.Fatalf("a changed inlined prompt must fail only the Piglet digest: exit %d\n%s%s", code, out, stderr.String())
	}
}
