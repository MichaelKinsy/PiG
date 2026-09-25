package runtimecell

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
)

func TestBuildGoPackedCellImportsMultiPackageFactory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "extension"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "message"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/multi\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "message", "message.go"), []byte("package message\nfunc Text() string { return \"ok\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := `package extension
import (
  sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
  "example.com/multi/internal/message"
)
func Extension() *sdk.Extension {
  ext := sdk.New("multi")
  ext.Command("multi", message.Text(), func(sdk.Context, string) error { return nil })
  return ext
}
`
	if err := os.WriteFile(filepath.Join(root, "extension", "extension.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	cell, err := BuildGoPackedCell(context.Background(), t.TempDir(), "multi", []GoExtension{{
		Name: "multi", Root: root, ModulePath: "example.com/multi", Package: "example.com/multi/extension", Factory: "Extension", Hash: "source",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cell.BinaryPath); err != nil {
		t.Fatalf("packed runner missing: %v", err)
	}
}

// Module roots and local replacements are go.mod paths. A path containing a
// space must be quoted there, as in a Windows user profile or CI TEMP such as
// "pig extension acceptance"; unquoted, `go build` rejects the generated
// module ("usage: replace module/path ...").
func TestBuildGoPackedCellAcceptsPathsWithSpaces(t *testing.T) {
	base := filepath.Join(t.TempDir(), "path with spaces")
	shared := filepath.Join(base, "shared lib")
	root := filepath.Join(base, "ext root")
	for path, content := range map[string]string{
		filepath.Join(shared, "go.mod"):                  "module example.com/shared\n\ngo 1.26\n",
		filepath.Join(shared, "shared.go"):               "package shared\nfunc Text() string { return \"ok\" }\n",
		filepath.Join(root, "go.mod"):                    "module example.com/spaced\n\ngo 1.26\n\nrequire (\n\tgithub.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n\texample.com/shared v0.0.0\n)\n\nreplace example.com/shared => \"../shared lib\"\n",
		filepath.Join(root, "extension", "extension.go"): "package extension\nimport (\n  sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\n  \"example.com/shared\"\n)\nfunc Extension() *sdk.Extension {\n  ext := sdk.New(\"spaced\")\n  ext.Command(\"spaced\", shared.Text(), func(sdk.Context, string) error { return nil })\n  return ext\n}\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cell, err := BuildGoPackedCell(context.Background(), t.TempDir(), "spaced", []GoExtension{{
		Name: "spaced", Root: root, ModulePath: "example.com/spaced", Package: "example.com/spaced/extension", Factory: "Extension", Hash: "source",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cell.BinaryPath); err != nil {
		t.Fatalf("packed runner missing: %v", err)
	}
}

func TestBuildGoPackedCellIgnoresBrokenParentVCSMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "extension"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/broken-vcs\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := `package extension
import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
func Extension() *sdk.Extension { return sdk.New("broken-vcs") }
`
	if err := os.WriteFile(filepath.Join(root, "extension", "extension.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	git := exec.Command("git", "init", "--quiet")
	git.Dir = root
	if output, err := git.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "index"), []byte("broken index"), 0o644); err != nil {
		t.Fatal(err)
	}
	buildTemp := filepath.Join(root, "tmp")
	if err := os.Mkdir(buildTemp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", buildTemp)

	cell, err := BuildGoPackedCell(context.Background(), t.TempDir(), "broken-vcs", []GoExtension{{
		Name: "broken-vcs", Root: root, ModulePath: "example.com/broken-vcs", Package: "example.com/broken-vcs/extension", Factory: "Extension", Hash: "source",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cell.BinaryPath); err != nil {
		t.Fatalf("packed runner missing: %v", err)
	}
}

func TestRenderGoModDoesNotDuplicateSDKWorkspaceModule(t *testing.T) {
	root := t.TempDir()
	sdkRoot := filepath.Join(root, "sdk")
	extensionRoot := filepath.Join(root, "extension")
	if err := os.MkdirAll(sdkRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(extensionRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdkRoot, "go.mod"), []byte("module github.com/MichaelKinsy/PiG/extensions/sdk\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extensionRoot, "go.mod"), []byte("module example.com/extension\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := renderGoMod([]GoExtension{{
		Name:             "extension",
		Root:             extensionRoot,
		ModulePath:       "example.com/extension",
		WorkspaceModules: []string{sdkRoot},
	}}, sdkRoot)

	if !strings.Contains(result, "\ngo 1.26.0\n") {
		t.Fatalf("generated module language version is not normalized:\n%s", result)
	}
	if count := strings.Count(result, "require github.com/MichaelKinsy/PiG/extensions/sdk "); count != 1 {
		t.Fatalf("SDK requirement count = %d, want 1:\n%s", count, result)
	}
	if count := strings.Count(result, "replace github.com/MichaelKinsy/PiG/extensions/sdk => "); count != 1 {
		t.Fatalf("SDK replacement count = %d, want 1:\n%s", count, result)
	}
}

func TestCollectExtensionDeps_ParenthesizedBlocks(t *testing.T) {
	dir := t.TempDir()
	goMod := `module example.com/my-ext

go 1.26

require (
	github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0
	github.com/mattn/go-runewidth v0.0.23
	github.com/some/other v1.2.3
)

require github.com/clipperhouse/uax29/v2 v2.2.0 // indirect

replace (
	github.com/MichaelKinsy/PiG/extensions/sdk => /some/path
	github.com/some/other => ../local/other
)
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	extensions := []GoExtension{{Name: "test", Root: dir, Factory: "Extension"}}
	reqs, reps := collectExtensionDeps(extensions)

	// SDK deps should be filtered out.
	for _, r := range reqs {
		if strings.Contains(r, "pig/extensions/sdk") {
			t.Errorf("SDK should be filtered: %q", r)
		}
		if r == "(" {
			t.Errorf("bare '(' should not be a requirement (broken block parse)")
		}
	}

	// Parenthesized require entries must be collected.
	wantReqs := map[string]bool{
		"github.com/mattn/go-runewidth v0.0.23":               false,
		"github.com/some/other v1.2.3":                        false,
		"github.com/clipperhouse/uax29/v2 v2.2.0 // indirect": false,
	}
	for _, r := range reqs {
		if _, ok := wantReqs[r]; ok {
			wantReqs[r] = true
		}
	}
	for want, found := range wantReqs {
		if !found {
			t.Errorf("missing requirement %q; got %v", want, reqs)
		}
	}

	// Parenthesized replace entries must be collected (SDK filtered).
	wantReps := map[string]bool{
		"github.com/some/other => " + modfile.AutoQuote(filepath.ToSlash(filepath.Clean(filepath.Join(dir, "..", "local", "other")))): false,
	}
	for _, r := range reps {
		if strings.Contains(r, "pig/extensions/sdk") {
			t.Errorf("SDK replace should be filtered: %q", r)
		}
		if _, ok := wantReps[r]; ok {
			wantReps[r] = true
		}
	}
	for want, found := range wantReps {
		if !found {
			t.Errorf("missing replace %q; got %v", want, reps)
		}
	}
}

func TestCollectExtensionDepsMakesLocalReplacementsAbsolute(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/ext\n\nreplace example.com/shared => ../shared\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, replacements := collectExtensionDeps([]GoExtension{{Name: "ext", Root: root}})
	want := "example.com/shared => " + modfile.AutoQuote(filepath.ToSlash(filepath.Clean(filepath.Join(root, "..", "shared"))))
	if len(replacements) != 1 || replacements[0] != want {
		t.Fatalf("replacements = %v, want %q", replacements, want)
	}
}

func TestRenderGoModLeavesTransitiveRequirementsOnExtensionModule(t *testing.T) {
	dir := t.TempDir()
	goMod := `module example.com/ext

go 1.26

require (
	github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0
	github.com/mattn/go-runewidth v0.0.23
)

replace github.com/MichaelKinsy/PiG/extensions/sdk => /sdk
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	extensions := []GoExtension{{Name: "test", Root: dir, Factory: "Extension"}}
	result := renderGoMod(extensions, "/sdk")

	if strings.Contains(result, "require (") {
		t.Errorf("renderGoMod should not emit unterminated 'require (' block:\n%s", result)
	}
	if strings.Contains(result, "require (\n") {
		t.Errorf("renderGoMod emitted parenthesized block opener:\n%s", result)
	}
	if !strings.Contains(result, "github.com/mattn/go-runewidth v0.0.23 // indirect") {
		t.Errorf("generated root is missing normalized transitive requirement:\n%s", result)
	}
}

func TestMergeGoSum_CollectsFromAllExtensions(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	goSum1 := "github.com/mattn/go-runewidth v0.0.23 h1:abc123=\ngithub.com/mattn/go-runewidth v0.0.23/go.mod h1:def456=\n"
	goSum2 := "github.com/other/pkg v1.0.0 h1:xyz789=\ngithub.com/mattn/go-runewidth v0.0.23 h1:abc123=\n"

	_ = os.WriteFile(filepath.Join(dir1, "go.sum"), []byte(goSum1), 0o644)
	_ = os.WriteFile(filepath.Join(dir2, "go.sum"), []byte(goSum2), 0o644)

	extensions := []GoExtension{
		{Name: "ext1", Root: dir1},
		{Name: "ext2", Root: dir2},
	}
	result := mergeGoSum(extensions)

	// All unique lines must be present.
	if !strings.Contains(result, "go-runewidth v0.0.23 h1:abc123=") {
		t.Errorf("missing runewidth hash entry:\n%s", result)
	}
	if !strings.Contains(result, "go-runewidth v0.0.23/go.mod h1:def456=") {
		t.Errorf("missing runewidth go.mod entry:\n%s", result)
	}
	if !strings.Contains(result, "other/pkg v1.0.0") {
		t.Errorf("missing other/pkg entry:\n%s", result)
	}

	// Duplicates must be deduplicated.
	count := strings.Count(result, "go-runewidth v0.0.23 h1:abc123=")
	if count != 1 {
		t.Errorf("runewidth hash appeared %d times, want 1:\n%s", count, result)
	}
}

func TestMergeGoSum_EmptyWhenNoSumFiles(t *testing.T) {
	dir := t.TempDir()
	extensions := []GoExtension{{Name: "ext1", Root: dir}}
	result := mergeGoSum(extensions)
	if result != "" {
		t.Errorf("expected empty string, got: %q", result)
	}
}
