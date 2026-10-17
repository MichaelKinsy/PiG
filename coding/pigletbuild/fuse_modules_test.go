package pigletbuild

import (
	"bytes"
	"io"
	"io/fs"
	"maps"
	"net/url"
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
)

// fusedDepModule is the external module a fixture member requires. The test
// serves it from a file:// proxy into a private module cache, then builds with
// GOPROXY=off, so the build is hermetic and offline.
const fusedDepModule = "example.com/dep"

type fusedModuleFixture struct {
	root    string // fake Pig source
	member  string // fused member module root
	goEnv   []string
	sums    map[string]string // version -> go.sum lines
	sdkRoot string
}

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// publishProxyModule writes one version of fusedDepModule to a file:// proxy
// and returns its go.sum lines.
func publishProxyModule(t *testing.T, proxy, version string) string {
	t.Helper()
	goMod := "module " + fusedDepModule + "\n\ngo 1.26\n"
	src := t.TempDir()
	writeFixtureFile(t, filepath.Join(src, "go.mod"), goMod)
	writeFixtureFile(t, filepath.Join(src, "dep.go"), "package dep\n\nconst Version = \""+version+"\"\n")
	dir := filepath.Join(proxy, fusedDepModule, "@v")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(dir, version+".zip")
	zipFile, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := modzip.CreateFromDir(zipFile, module.Version{Path: fusedDepModule, Version: version}, src); err != nil {
		t.Fatal(err)
	}
	if err := zipFile.Close(); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(dir, version+".mod"), goMod)
	writeFixtureFile(t, filepath.Join(dir, version+".info"), `{"Version":"`+version+`","Time":"2026-01-01T00:00:00Z"}`)
	list, _ := os.ReadFile(filepath.Join(dir, "list"))
	writeFixtureFile(t, filepath.Join(dir, "list"), string(list)+version+"\n")
	zipHash, err := dirhash.HashZip(zipPath, dirhash.Hash1)
	if err != nil {
		t.Fatal(err)
	}
	modHash, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(goMod)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fusedDepModule + " " + version + " " + zipHash + "\n" + fusedDepModule + " " + version + "/go.mod " + modHash + "\n"
}

// newFusedModuleFixture builds a fake Pig source pinning example.com/dep v1.0.0
// and a fused member requiring v1.1.0, with both versions in a warm private
// module cache.
func newFusedModuleFixture(t *testing.T, workspace bool) fusedModuleFixture {
	t.Helper()
	sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	proxy := t.TempDir()
	sums := map[string]string{}
	for _, version := range []string{"v1.0.0", "v1.1.0"} {
		sums[version] = publishProxyModule(t, proxy, version)
	}
	modCache := t.TempDir()
	goEnv := append(os.Environ(),
		"GOMODCACHE="+modCache, "GOSUMDB=off", "GONOSUMDB=", "GOPRIVATE=", "GOTOOLCHAIN=local",
		"GOFLAGS=-modcacherw", "GOWORK=off", "GOPROXY="+fileProxyURL(proxy))
	warm := exec.Command("go", "mod", "download", fusedDepModule+"@v1.0.0", fusedDepModule+"@v1.1.0")
	warm.Dir = t.TempDir()
	warm.Env = goEnv
	if output, err := warm.CombinedOutput(); err != nil {
		t.Fatalf("warm module cache: %v\n%s", err, output)
	}
	// From here on the build is offline: the module cache is the only source.
	goEnv = append(goEnv, "GOPROXY=off")

	// The Go command resolves a workspace's `use .` against the physical
	// working directory, so the fixture uses physical paths.
	root := physicalTempDir(t)
	writeFixtureFile(t, filepath.Join(root, "go.mod"), "module example.com/pig\n\ngo 1.26\n\nrequire (\n\tgithub.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n\t"+fusedDepModule+" v1.0.0\n)\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => "+modfile.AutoQuote(filepath.ToSlash(sdkRoot))+"\n")
	writeFixtureFile(t, filepath.Join(root, "go.sum"), sums["v1.0.0"])
	writeFixtureFile(t, filepath.Join(root, "pigdep", "pigdep.go"), "package pigdep\n\nimport \"example.com/dep\"\n\nconst Version = dep.Version\n")
	fuseDir := filepath.Join(root, "coding", "extension", "host", "fusepack")
	writeFixtureFile(t, filepath.Join(fuseDir, "base.go"), `package fusepack
import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
var factories = map[string]func() *sdk.Extension{}
func registerFactory(name string, factory func() *sdk.Extension) { factories[name] = factory }
`)
	writeFixtureFile(t, filepath.Join(fuseDir, "registry_generated.go"), "package fusepack\n")
	if workspace {
		writeFixtureFile(t, filepath.Join(root, "go.work"), "go 1.26\n\nuse .\n")
		writeFixtureFile(t, filepath.Join(root, "go.work.sum"), "")
	}

	member := physicalTempDir(t)
	writeFixtureFile(t, filepath.Join(member, "go.mod"), "module example.com/extensions\n\ngo 1.26\n\nrequire (\n\tgithub.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n\t"+fusedDepModule+" v1.1.0\n)\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => "+modfile.AutoQuote(filepath.ToSlash(sdkRoot))+"\n")
	writeFixtureFile(t, filepath.Join(member, "go.sum"), sums["v1.1.0"])
	writeFixtureFile(t, filepath.Join(member, "review", "extension.go"), `package review
import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"example.com/dep"
)
func Extension() *sdk.Extension { return sdk.New("review-" + dep.Version) }
`)
	return fusedModuleFixture{root: root, member: member, goEnv: goEnv, sums: sums, sdkRoot: sdkRoot}
}

func (f fusedModuleFixture) entry() fusedEntry {
	return fusedEntry{
		Name: "review", Pkg: "ext_review", Factory: "Extension", Root: f.member,
		ModulePath: "example.com/extensions", Package: "example.com/extensions/review",
	}
}

// snapshotTree records every path, mode, and content under root.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		value := info.Mode().String()
		if entry.Type().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += "\x00" + string(data)
		}
		tree[path] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// makeReadOnly removes write permission from the whole tree, as for an
// installed or shared Pig checkout, and restores it for TempDir cleanup.
func makeReadOnly(t *testing.T, root string) {
	t.Helper()
	var dirs []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			dirs = append(dirs, path)
		}
		return os.Chmod(path, info.Mode().Perm()&^0o222)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, dir := range dirs {
			_ = os.Chmod(dir, 0o755)
		}
	})
}

func physicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func buildFusedFixture(t *testing.T, f fusedModuleFixture, env []string) {
	t.Helper()
	overlay, err := newBuildOverlay(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	graph, err := overlayFuse(overlay, f.root, []fusedEntry{f.entry()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	overlayPath, err := overlay.write()
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, f.root)
	cmd := exec.Command("go", "build", "-mod=readonly", "-overlay", overlayPath, "./coding/extension/host/fusepack", "./pigdep")
	cmd.Dir = f.root
	cmd.Env = env
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fused member with an external module did not build offline from a read-only source: %v\n%s", err, output)
	}
	after := snapshotTree(t, f.root)
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
	if want := `fused member "review" raises Pig's example.com/dep v1.0.0 to v1.1.0 (minimum version selection)`; !slices.Equal(graph.raised, []string{want}) {
		t.Fatalf("raised = %q, want %q", graph.raised, want)
	}
	// The module the build compiled is the one the vet scope note names.
	if want := fusedDepModule + "@v1.1.0"; !slices.Equal(graph.unvetted, []string{want}) {
		t.Fatalf("unvetted = %q, want %q", graph.unvetted, want)
	}
}

// A workspace checkout build reads checksums through go.work.sum. Without the
// member's checksums in the overlay, the Go command writes them into the
// source checkout and fails on a read-only one.
func TestOverlayFuseBuildsExternalModuleFromReadOnlyWorkspaceCheckout(t *testing.T) {
	f := newFusedModuleFixture(t, true)
	makeReadOnly(t, f.root)
	buildFusedFixture(t, f, sourceBuildEnv(f.root, f.goEnv))
}

// The release path builds a module-cache source tree with GOWORK=off. A
// dependency module's requirements do not reach the main module, so without
// the member's requirements and checksums in the overlay the readonly build
// needs go.mod and go.sum updates.
func TestOverlayFuseBuildsExternalModuleWithoutWorkspaceReadonlyOffline(t *testing.T) {
	f := newFusedModuleFixture(t, false)
	makeReadOnly(t, f.root)
	buildFusedFixture(t, f, moduleSourceBuildEnv(f.goEnv))
}

func fuseModuleConflict(t *testing.T, pigMod string, members map[string]string) error {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "go.mod"), pigMod)
	overlay, err := newBuildOverlay(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var entries []fusedEntry
	for _, name := range slices.Sorted(maps.Keys(members)) {
		memberRoot := t.TempDir()
		writeFixtureFile(t, filepath.Join(memberRoot, "go.mod"), members[name])
		modulePath := "example.com/" + name
		entries = append(entries, fusedEntry{Name: name, Pkg: "ext_" + name, Factory: "Extension", Root: memberRoot, ModulePath: modulePath, Package: modulePath})
	}
	_, err = overlayFuse(overlay, root, entries, nil)
	return err
}

// A fused member links the SDK of the Pig source it is fused into, so a
// member that requires a newer SDK than that source pins is a clear error, not
// a silent upgrade of the host contract.
func TestOverlayFuseRejectsMemberRequiringNewerSDK(t *testing.T) {
	pigMod := "module example.com/pig\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.4.0\n"
	err := fuseModuleConflict(t, pigMod, map[string]string{
		"review": "module example.com/review\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.5.0\n",
	})
	if err == nil {
		t.Fatal("a member requiring a newer SDK than Pig pins was fused")
	}
	for _, want := range []string{`fuse "review"`, "github.com/MichaelKinsy/PiG/extensions/sdk v0.5.0", "newer than the v0.4.0 this Pig source pins"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
}

// Go applies only the main module's replacements, so members' replacements are
// promoted; two members replacing one module differently cannot both hold.
func TestOverlayFuseRejectsConflictingMemberReplacements(t *testing.T) {
	pigMod := "module example.com/pig\n\ngo 1.26\n"
	err := fuseModuleConflict(t, pigMod, map[string]string{
		"alpha": "module example.com/alpha\n\ngo 1.26\n\nrequire example.com/shared v1.0.0\n\nreplace example.com/shared => example.com/shared-fork v1.0.1\n",
		"beta":  "module example.com/beta\n\ngo 1.26\n\nrequire example.com/shared v1.0.0\n\nreplace example.com/shared => example.com/shared-fork v1.0.2\n",
	})
	if err == nil || !strings.Contains(err.Error(), "conflicting replacements for example.com/shared") {
		t.Fatalf("conflicting member replacements = %v", err)
	}
}

// Minimum version selection never lowers a Pig requirement and adds a member's
// new modules as indirect requirements.
func TestMergeFusedModulesKeepsHigherPigVersion(t *testing.T) {
	pig, err := modfile.Parse("go.mod", []byte("module example.com/pig\n\ngo 1.26\n\nrequire example.com/dep v1.2.0\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	member := t.TempDir()
	writeFixtureFile(t, filepath.Join(member, "go.mod"), "module example.com/review\n\ngo 1.26\n\nrequire (\n\texample.com/dep v1.1.0\n\texample.com/other v0.3.0\n\texample.com/pig v0.9.0\n)\n")
	graph, err := mergeFusedModules(pig, []fusedEntry{{Name: "review", Root: member, ModulePath: "example.com/review", Package: "example.com/review"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.raised) != 0 {
		t.Fatalf("raised = %q", graph.raised)
	}
	formatted, err := pig.Format()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"example.com/dep v1.2.0\n", "example.com/other v0.3.0 // indirect\n", "example.com/review v0.0.0\n", "example.com/review => " + member} {
		if !bytes.Contains(formatted, []byte(want)) {
			t.Fatalf("merged go.mod missing %q:\n%s", want, formatted)
		}
	}
	if bytes.Contains(formatted, []byte("example.com/pig v")) {
		t.Fatalf("merged go.mod requires the main module:\n%s", formatted)
	}
}

// Across members, each module gets the highest version any of them requires,
// whichever member is read first, and a member's relative replacement names its
// directory from the member's root, since Pig's go.mod sits elsewhere.
func TestMergeFusedModulesTakesTheHighestMemberVersionAndAnchorsLocalReplacements(t *testing.T) {
	pig, err := modfile.Parse("go.mod", []byte("module example.com/pig\n\ngo 1.26\n\nrequire example.com/dep v1.0.0\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	alpha, beta := filepath.Join(parent, "a-member"), filepath.Join(parent, "b-member")
	// alpha sorts first and requires the lower version, beta the higher one.
	writeFixtureFile(t, filepath.Join(alpha, "go.mod"), "module example.com/alpha\n\ngo 1.26\n\nrequire example.com/dep v1.2.0\n")
	writeFixtureFile(t, filepath.Join(beta, "go.mod"), "module example.com/beta\n\ngo 1.26\n\nrequire (\n\texample.com/dep v1.3.0\n\texample.com/vendored v0.1.0\n)\n\nreplace example.com/vendored => ./third_party/vendored\n")
	graph, err := mergeFusedModules(pig, []fusedEntry{
		{Name: "alpha", Root: alpha, ModulePath: "example.com/alpha", Package: "example.com/alpha"},
		{Name: "beta", Root: beta, ModulePath: "example.com/beta", Package: "example.com/beta"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := `fused member "beta" raises Pig's example.com/dep v1.0.0 to v1.3.0 (minimum version selection)`; !slices.Equal(graph.raised, []string{want}) {
		t.Fatalf("raised = %q, want %q", graph.raised, want)
	}
	versions := map[string]string{}
	for _, r := range pig.Require {
		versions[r.Mod.Path] = r.Mod.Version
	}
	if versions["example.com/dep"] != "v1.3.0" {
		t.Fatalf("example.com/dep = %q, want v1.3.0", versions["example.com/dep"])
	}
	replacement := pigReplacement(pig, module.Version{Path: "example.com/vendored"})
	if want := filepath.Join(beta, "third_party", "vendored"); replacement == nil || replacement.New.Path != want || replacement.New.Version != "" {
		t.Fatalf("example.com/vendored replacement = %+v, want %s", replacement, want)
	}
}

// fileProxyURL is the GOPROXY value for a directory: the Go command wants file:// followed by an absolute path, so a
// Windows drive path (C:\x) becomes file:///C:/x, and file://C:/x is rejected as an invalid proxy URL with non-path elements.
func fileProxyURL(dir string) string {
	path := filepath.ToSlash(dir)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}
