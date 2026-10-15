package pigletbuild

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
	modzip "golang.org/x/mod/zip"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	pigletartifact "github.com/MichaelKinsy/PiG/coding/piglet/artifact"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// keptWorkSumLine is a go.work.sum line no build of the fixture needs. Go drops
// such a line when it rewrites go.work.sum; the overlay must keep it.
const keptWorkSumLine = "example.com/kept v1.0.0/go.mod h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// publishFixtureModule writes one module version to a file:// proxy and returns
// its zip and go.mod checksum lines.
func publishFixtureModule(t *testing.T, proxy, path, version string, files map[string]string) (zipSum, modSum string) {
	t.Helper()
	src := t.TempDir()
	for name, content := range files {
		writeFixtureFile(t, filepath.Join(src, filepath.FromSlash(name)), content)
	}
	dir := filepath.Join(proxy, filepath.FromSlash(path), "@v")
	zipPath := filepath.Join(dir, version+".zip")
	writeFixtureFile(t, zipPath, "")
	zipFile, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := modzip.CreateFromDir(zipFile, module.Version{Path: path, Version: version}, src); err != nil {
		t.Fatal(err)
	}
	if err := zipFile.Close(); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(dir, version+".mod"), files["go.mod"])
	writeFixtureFile(t, filepath.Join(dir, version+".info"), `{"Version":"`+version+`","Time":"2026-01-01T00:00:00Z"}`)
	writeFixtureFile(t, filepath.Join(dir, "list"), version+"\n")
	zipHash, err := dirhash.HashZip(zipPath, dirhash.Hash1)
	if err != nil {
		t.Fatal(err)
	}
	modHash, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(files["go.mod"])), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return path + " " + version + " " + zipHash + "\n", path + " " + version + "/go.mod " + modHash + "\n"
}

// ambiguousImportWorkspace is a fake Pig checkout in workspace mode with a
// second workspace module, and a fused member whose own go.sum is complete in
// module mode.
type ambiguousImportWorkspace struct {
	root, member string
}

// newAmbiguousImportWorkspace reproduces the a2a-go failure offline. The member
// imports example.com/amb/v2/pkg from module example.com/amb/v2. Module mode
// never sees module example.com/amb, so the member's go.sum lacks it. The
// workspace's other module requires example.com/amb v1.0.0, so a workspace
// build must read that version's zip to prove it does not also provide
// v2/pkg, and needs a zip hash no go.sum in the workspace has.
func newAmbiguousImportWorkspace(t *testing.T) ambiguousImportWorkspace {
	t.Helper()
	sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	proxy := t.TempDir()
	_, v1Mod := publishFixtureModule(t, proxy, "example.com/amb", "v1.0.0", map[string]string{
		"go.mod": "module example.com/amb\n\ngo 1.26\n", "amb.go": "package amb\n",
	})
	v2Zip, v2Mod := publishFixtureModule(t, proxy, "example.com/amb/v2", "v2.0.0", map[string]string{
		"go.mod": "module example.com/amb/v2\n\ngo 1.26\n", "pkg/pkg.go": "package pkg\n\nconst V = \"v2\"\n",
	})
	env := []string{"GOMODCACHE=" + t.TempDir(), "GOSUMDB=off", "GONOSUMDB=", "GOPRIVATE=", "GOTOOLCHAIN=local", "GOFLAGS=-modcacherw"}
	warm := exec.Command("go", "mod", "download", "example.com/amb@v1.0.0", "example.com/amb/v2@v2.0.0")
	warm.Dir = t.TempDir()
	warm.Env = append(os.Environ(), append(slices.Clone(env), "GOWORK=off", "GOPROXY="+fileProxyURL(proxy))...)
	if output, err := warm.CombinedOutput(); err != nil {
		t.Fatalf("warm module cache: %v\n%s", err, output)
	}
	// From here on the module cache is the only source.
	for _, kv := range append(env, "GOPROXY=off", "GOWORK=") {
		key, value, _ := strings.Cut(kv, "=")
		t.Setenv(key, value)
	}

	sdk := modfile.AutoQuote(filepath.ToSlash(sdkRoot))
	root := physicalTempDir(t)
	writeFixtureFile(t, filepath.Join(root, "go.mod"), "module example.com/pig\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => "+sdk+"\n")
	writeFixtureFile(t, filepath.Join(root, "go.sum"), "")
	writeFixtureFile(t, filepath.Join(root, "cmd", "pig", "main.go"), "package main\n\nimport _ \"example.com/pig/coding/extension/host/fusepack\"\n\nfunc main() {}\n")
	fuseDir := filepath.Join(root, "coding", "extension", "host", "fusepack")
	writeFixtureFile(t, filepath.Join(fuseDir, "base.go"), `package fusepack
import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
var factories = map[string]func() *sdk.Extension{}
func registerFactory(name string, factory func() *sdk.Extension) { factories[name] = factory }
`)
	writeFixtureFile(t, filepath.Join(fuseDir, "registry_generated.go"), "package fusepack\n")
	writeFixtureFile(t, filepath.Join(root, "other", "go.mod"), "module example.com/other\n\ngo 1.26\n\nrequire example.com/amb v1.0.0\n")
	writeFixtureFile(t, filepath.Join(root, "other", "go.sum"), v1Mod)
	writeFixtureFile(t, filepath.Join(root, "other", "other.go"), "package other\n")
	writeFixtureFile(t, filepath.Join(root, "go.work"), "go 1.26\n\nuse (\n\t.\n\t./other\n)\n")
	writeFixtureFile(t, filepath.Join(root, "go.work.sum"), keptWorkSumLine+"\n")

	member := physicalTempDir(t)
	writeFixtureFile(t, filepath.Join(member, "go.mod"), "module example.com/review\n\ngo 1.26\n\nrequire (\n\tgithub.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n\texample.com/amb/v2 v2.0.0\n)\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => "+sdk+"\n")
	writeFixtureFile(t, filepath.Join(member, "go.sum"), v2Zip+v2Mod)
	writeFixtureFile(t, filepath.Join(member, "extension.go"), `package review
import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"example.com/amb/v2/pkg"
)
func Extension() *sdk.Extension { return sdk.New("review-" + pkg.V) }
`)
	// The member's go.sum is complete for the member's own build.
	moduleMode := exec.Command("go", "build", "./...")
	moduleMode.Dir = member
	moduleMode.Env = append(os.Environ(), "GOWORK=off")
	if output, err := moduleMode.CombinedOutput(); err != nil {
		t.Fatalf("member does not build in module mode: %v\n%s", err, output)
	}
	return ambiguousImportWorkspace{root: root, member: member}
}

func (w ambiguousImportWorkspace) cell() subprocess.CellSpec {
	return subprocess.CellSpec{
		Key: "packed-go:review", Strategy: subprocess.CellStrategyPackedGo, Language: "go",
		Extensions: []subprocess.ExtConfig{{
			Name: "review", Source: w.member, RuntimeKind: "subprocess", RuntimeLanguage: "go",
			EntrypointKind: "factory", ModulePath: "example.com/review", Package: "example.com/review", Factory: "Extension",
		}},
	}
}

// A Pig checkout builds in workspace mode, which needs checksums go mod tidy
// never writes into a member's go.sum, such as the zip hash that settles
// whether a package path is provided by two modules. Go writes them to
// go.work.sum, and the build overlay forbids that write, so the build failed
// with "updates to go.sum needed". The native build lists the packages once in
// a scratch workspace and overlays what Go adds, keeping every existing line,
// and never writes the source tree.
func TestBuildNativeArtifactCompletesWorkspaceChecksumsOffline(t *testing.T) {
	w := newAmbiguousImportWorkspace(t)
	makeReadOnly(t, w.root)
	before := snapshotTree(t, w.root)

	// Without the completion step, the fused workspace build fails.
	entries, err := collectFused(w.cell())
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := newBuildOverlay(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := overlayFuse(overlay, w.root, entries, nil); err != nil {
		t.Fatal(err)
	}
	overlayPath, err := overlay.write()
	if err != nil {
		t.Fatal(err)
	}
	source := pigSource{Root: w.root}
	goToolchain := toolchain.GoToolchain{Command: "go"}
	plain := source.goCommand(context.Background(), goToolchain, pigletBinaryBuildArgs(filepath.Join(t.TempDir(), "pig"), "", overlayPath, nil)...)
	if output, err := plain.CombinedOutput(); err == nil || !strings.Contains(string(output), "updates to go.sum needed") {
		t.Fatalf("fixture does not need a workspace-only checksum: err=%v\n%s", err, output)
	}

	// The production build completes the checksums and links.
	scratch := filepath.Join(t.TempDir(), "workspace-sums")
	if err := completeWorkspaceSums(context.Background(), source, goToolchain, overlayPath, scratch, nil); err != nil {
		t.Fatalf("completeWorkspaceSums: %v", err)
	}
	got := backingOf(t, overlayPath, filepath.Join(w.root, "go.work.sum"))
	if !strings.Contains(got, "example.com/amb v1.0.0 h1:") {
		t.Errorf("go.work.sum lacks the zip hash the workspace build needs:\n%s", got)
	}
	if !strings.Contains(got, keptWorkSumLine) {
		t.Errorf("go.work.sum lost an existing line:\n%s", got)
	}

	plan := pigletartifact.Plan{Components: []pigletartifact.Component{{
		Kind: pigletartifact.ComponentKindExtension, Name: "review",
		Realization: pigletartifact.RealizationFused, Materialization: pigletartifact.MaterializationBinary,
	}}}
	artifact := filepath.Join(t.TempDir(), "pig-review")
	var stdout, stderr bytes.Buffer
	if _, err := buildNativeArtifact(context.Background(), source, &piglet.Piglet{Name: "review"}, []subprocess.CellSpec{w.cell()}, plan, nil, Options{}, artifact, &stdout, &stderr); err != nil {
		t.Fatalf("buildNativeArtifact: %v\nstdout:\n%s\nstderr:\n%s", err, &stdout, &stderr)
	}
	if _, err := os.Stat(artifact); err != nil {
		t.Fatal(err)
	}
	after := snapshotTree(t, w.root)
	for path, value := range after {
		if before[path] != value {
			t.Errorf("build changed source path %s", path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			t.Errorf("build removed source path %s", path)
		}
	}
}

// A module-cache source builds with GOWORK=off, so no workspace checksums apply.
func TestCompleteWorkspaceSumsSkipsAModuleCacheSource(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.26.0\n\nuse .\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := completeWorkspaceSums(context.Background(), pigSource{Root: root, ModuleVersion: "v0.5.0"}, toolchain.GoToolchain{Command: filepath.Join(root, "no-such-go")}, filepath.Join(root, "no-overlay.json"), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("a module-cache source must not run the go command: %v", err)
	}
}

// The scratch workspace resolves from another directory, so every relative
// workspace module and local replacement becomes absolute under the source root.
func TestScratchWorkFileAnchorsRelativePaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "pig")
	data := []byte("go 1.26.0\n\ntoolchain go1.27.2\n\nuse (\n\t.\n\t./extensions/sdk\n)\n\nreplace example.com/local => ./local\n\nreplace example.com/remote v1.0.0 => example.com/fork v1.0.1\n")
	out, err := scratchWorkFile(filepath.Join(root, "go.work"), data, root)
	if err != nil {
		t.Fatal(err)
	}
	work, err := modfile.ParseWork("go.work", out, nil)
	if err != nil {
		t.Fatalf("parse rewritten go.work: %v\n%s", err, out)
	}
	var uses []string
	for _, use := range work.Use {
		uses = append(uses, use.Path)
	}
	if want := []string{root, filepath.Join(root, "extensions", "sdk")}; strings.Join(uses, "|") != strings.Join(want, "|") {
		t.Errorf("use = %q, want %q", uses, want)
	}
	if work.Go == nil || work.Go.Version != "1.26.0" || work.Toolchain == nil || work.Toolchain.Name != "go1.27.2" {
		t.Errorf("go/toolchain not kept:\n%s", out)
	}
	if len(work.Replace) != 2 ||
		work.Replace[0].New.Path != filepath.Join(root, "local") ||
		work.Replace[1].Old.Version != "v1.0.0" || work.Replace[1].New.Path != "example.com/fork" || work.Replace[1].New.Version != "v1.0.1" {
		t.Errorf("replace not rewritten:\n%s", out)
	}
}

// The completed go.work.sum keeps every line the overlay had, in order, and
// appends each line Go added once, whatever Go kept of the original. A CRLF
// line matches its LF form.
func TestAppendMissingLinesKeepsBaseAndAddsOnlyNewLines(t *testing.T) {
	base := []byte("a v1 h1:A=\nb v1 h1:B=")
	extra := []byte("c v1 h1:C=\r\n  b v1 h1:B=  \r\n\nc v1 h1:C=\nd v1 h1:D=\n")
	if got, want := string(appendMissingLines(base, extra)), "a v1 h1:A=\nb v1 h1:B=\nc v1 h1:C=\nd v1 h1:D=\n"; got != want {
		t.Fatalf("appendMissingLines = %q, want %q", got, want)
	}
	if got := string(appendMissingLines(nil, []byte("a v1 h1:A=\n"))); got != "a v1 h1:A=\n" {
		t.Fatalf("appendMissingLines(empty base) = %q", got)
	}
}

func backingOf(t *testing.T, overlayPath, target string) string {
	t.Helper()
	data, err := os.ReadFile(overlayPath)
	if err != nil {
		t.Fatal(err)
	}
	var o struct{ Replace map[string]string }
	if err := json.Unmarshal(data, &o); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(o.Replace[target])
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
