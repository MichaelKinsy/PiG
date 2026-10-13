package pigletbuild

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/cellpack"
)

func TestOverlayCellsLeavesSourceTreeUntouched(t *testing.T) {
	root := t.TempDir()
	cellsDir := filepath.Join(root, "coding", "extension", "host", "cellpack", "cells")
	if err := os.MkdirAll(cellsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(cellsDir, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte("{\"cells\":[]}"), 0o644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "isolated")
	if err := os.WriteFile(binary, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	overlay, err := newBuildOverlay(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry := cellpack.CellEntry{Binary: "isolated/go/auth-extension"}
	if err := overlayCells(overlay, root, cellpack.Manifest{Cells: []cellpack.CellEntry{entry}}, []stagedCell{{entry: entry, binaryPath: binary}}); err != nil {
		t.Fatal(err)
	}
	stagedPath := filepath.Join(cellsDir, "isolated", "go", "auth-extension")
	if overlay.replace[stagedPath] != binary {
		t.Fatalf("overlay does not serve the cell binary: %v", overlay.replace)
	}
	manifest, err := os.ReadFile(overlay.replace[manifestPath])
	if err != nil || !strings.Contains(string(manifest), "isolated/go/auth-extension") {
		t.Fatalf("overlay manifest = %q, %v", manifest, err)
	}
	if _, err := os.Stat(stagedPath); !os.IsNotExist(err) {
		t.Fatalf("cell payload was written into the source tree: %v", err)
	}
	if committed, _ := os.ReadFile(manifestPath); string(committed) != "{\"cells\":[]}" {
		t.Fatalf("committed manifest changed: %q", committed)
	}
}

func TestRenderFuseRegistryDefersFactoryConstruction(t *testing.T) {
	got := renderFuseRegistry([]fusedEntry{{Name: "trace", Pkg: "ext_trace", Package: "example.com/extensions/trace", Factory: "Extension"}})
	if strings.Contains(got, "Extension().RunWithConn") {
		t.Fatalf("generated registry eagerly constructs extension factory:\n%s", got)
	}
	if !strings.Contains(got, `registerFactory("trace", ext_trace.Extension)`) || !strings.Contains(got, `ext_trace "example.com/extensions/trace"`) {
		t.Fatalf("generated registry does not lazily register factory:\n%s", got)
	}
}

func TestOverlayFuseImportsFactoryPackageWithoutWritingGoMod(t *testing.T) {
	root := t.TempDir()
	fuseDir := filepath.Join(root, "coding", "extension", "host", "fusepack")
	if err := os.MkdirAll(fuseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	goMod := []byte("module example.com/pig\n\ngo 1.26\n")
	registry := []byte("package fusepack\n")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), goMod, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fuseDir, "registry_generated.go"), registry, 0o644); err != nil {
		t.Fatal(err)
	}
	extRoot := t.TempDir()
	overlay, err := newBuildOverlay(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := overlayFuse(overlay, root, []fusedEntry{{
		Name: "review", Pkg: "ext_review", Factory: "Extension", Root: extRoot,
		ModulePath: "example.com/extensions", Package: "example.com/extensions/review",
	}}, false); err != nil {
		t.Fatal(err)
	}
	stagedMod, err := os.ReadFile(overlay.modfile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"example.com/extensions v0.0.0", "example.com/extensions => " + extRoot} {
		if !strings.Contains(string(stagedMod), want) {
			t.Fatalf("staged go.mod missing %q:\n%s", want, stagedMod)
		}
	}
	stagedRegistry, err := os.ReadFile(overlay.replace[filepath.Join(fuseDir, "registry_generated.go")])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stagedRegistry), `ext_review "example.com/extensions/review"`) {
		t.Fatalf("registry does not import factory package:\n%s", stagedRegistry)
	}
	if committed, _ := os.ReadFile(filepath.Join(root, "go.mod")); string(committed) != string(goMod) {
		t.Fatalf("go.mod was written:\n%s", committed)
	}
	if committed, _ := os.ReadFile(filepath.Join(fuseDir, "registry_generated.go")); string(committed) != string(registry) {
		t.Fatalf("fuse registry was written:\n%s", committed)
	}
}

func TestOverlayFuseCompilesExtensionWithInternalPackages(t *testing.T) {
	root := t.TempDir()
	sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	goMod := "module example.com/pig\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => " + filepath.ToSlash(sdkRoot) + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	fuseDir := filepath.Join(root, "coding", "extension", "host", "fusepack")
	if err := os.MkdirAll(fuseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := `package fusepack
import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
var factories = map[string]func() *sdk.Extension{}
func registerFactory(name string, factory func() *sdk.Extension) { factories[name] = factory }
`
	if err := os.WriteFile(filepath.Join(fuseDir, "base.go"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fuseDir, "registry_generated.go"), []byte("package fusepack\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	extRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(extRoot, "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(extRoot, "internal", "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	extGoMod := "module example.com/extensions\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"
	if err := os.WriteFile(filepath.Join(extRoot, "go.mod"), []byte(extGoMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extRoot, "internal", "shared", "shared.go"), []byte("package shared\nfunc Name() string { return \"review\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	extensionSource := `package review
import (
  sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
  "example.com/extensions/internal/shared"
)
func Extension() *sdk.Extension { return sdk.New(shared.Name()) }
`
	if err := os.WriteFile(filepath.Join(extRoot, "review", "extension.go"), []byte(extensionSource), 0o644); err != nil {
		t.Fatal(err)
	}
	overlay, err := newBuildOverlay(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := overlayFuse(overlay, root, []fusedEntry{{
		Name: "review", Pkg: "ext_review", Factory: "Extension", Root: extRoot,
		ModulePath: "example.com/extensions", Package: "example.com/extensions/review",
	}}, false); err != nil {
		t.Fatal(err)
	}
	overlayPath, err := overlay.write()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-overlay", overlayPath, "-modfile", overlay.modfile, "./coding/extension/host/fusepack")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fused multi-package extension did not compile: %v\n%s", err, output)
	}
}

func TestPigletBinaryBuildArgsBakesReleaseVersionOnly(t *testing.T) {
	bare := pigletBinaryBuildArgs("/tmp/pig-x", "", "", "")
	if strings.Contains(strings.Join(bare, " "), "main.PigletBinaryVersion") {
		t.Fatalf("no release version should bake no version: %v", bare)
	}
	if bare[0] != "build" || bare[len(bare)-1] != "./cmd/pig" {
		t.Fatalf("unexpected build args: %v", bare)
	}
	baked := pigletBinaryBuildArgs("/tmp/pig-x", "1.2.3", "/tmp/overlay.json", "")
	joined := strings.Join(baked, " ")
	if strings.Contains(joined, "DefaultUpdateURL") {
		t.Fatalf("build baked a publication endpoint: %v", baked)
	}
	if !strings.Contains(joined, "main.PigletBinaryVersion=1.2.3") {
		t.Fatalf("Piglet Binary version not baked via ldflags: %v", baked)
	}
}

func TestPigletBinaryBuildArgsDisablesAutomaticVCSStamping(t *testing.T) {
	for _, version := range []string{"", "1.2.3"} {
		args := pigletBinaryBuildArgs("/tmp/pig-x", version, "", "")
		if !slices.Contains(args, "-buildvcs=false") {
			t.Fatalf("version %q: build did not disable automatic VCS stamping: %v", version, args)
		}
	}
}

// A Piglet Binary is a redistributable artifact, so it must not carry debug
// symbols or absolute build paths regardless of whether a release version is set.
func TestPigletBinaryBuildArgsStripsAndTrims(t *testing.T) {
	for _, version := range []string{"", "1.2.3"} {
		joined := strings.Join(pigletBinaryBuildArgs("/tmp/pig-x", version, "", ""), " ")
		if !strings.Contains(joined, "-trimpath") {
			t.Fatalf("version %q: build did not trim paths: %s", version, joined)
		}
		if !strings.Contains(joined, "-s -w") {
			t.Fatalf("version %q: build did not strip symbols: %s", version, joined)
		}
	}
}

func TestSourceBuildEnvUsesCheckoutWorkspace(t *testing.T) {
	root := t.TempDir()
	env := []string{"PATH=/bin", "GOWORK=off", "GOFLAGS=-mod=mod -modcacherw"}
	if got := sourceBuildEnv(root, env); strings.Join(got, "\n") != strings.Join(env, "\n") {
		t.Fatalf("without go.work the environment must pass through unchanged, got %q", got)
	}
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.26\n\nuse .\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(sourceBuildEnv(root, env), "\n")
	for _, want := range []string{"PATH=/bin", "GOWORK=" + filepath.Join(root, "go.work"), "GOFLAGS=-modcacherw"} {
		if !strings.Contains(got, want) {
			t.Errorf("sourceBuildEnv missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "GOWORK=off") || strings.Contains(got, "-mod=mod") {
		t.Errorf("sourceBuildEnv kept GOWORK=off or -mod=mod:\n%s", got)
	}
}

func TestOverlayFusePromotesLocalReplacementsAndSeedsChecksums(t *testing.T) {
	for _, workspace := range []bool{false, true} {
		t.Run(map[bool]string{false: "modfile", true: "workspace"}[workspace], func(t *testing.T) {
			root := t.TempDir()
			fuseDir := filepath.Join(root, "coding", "extension", "host", "fusepack")
			if err := os.MkdirAll(fuseDir, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, data := range map[string]string{
				"go.mod": "module example.com/pig\n\ngo 1.26\n\nrequire golang.org/x/sys v0.47.0 // indirect\n",
				"go.sum": "golang.org/x/sys v0.47.0 h1:pig=\ngolang.org/x/sys v0.47.0/go.mod h1:pigmod=\n",
			} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(fuseDir, "registry_generated.go"), []byte("package fusepack\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			extRoot := t.TempDir()
			extGoMod := "module example.com/extensions\n\ngo 1.26\n\nrequire (\n\tgithub.com/MichaelKinsy/PiG/extensions/sdk v0.4.1\n\texample.com/dep v1.2.3\n)\n\nreplace example.com/local => ./vendored/local\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => ../sdk\n"
			for name, data := range map[string]string{
				"go.mod": extGoMod,
				"go.sum": "example.com/dep v1.2.3 h1:dep=\ngolang.org/x/sys v0.47.0/go.mod h1:pigmod=\n",
			} {
				if err := os.WriteFile(filepath.Join(extRoot, name), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			overlay, err := newBuildOverlay(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := overlayFuse(overlay, root, []fusedEntry{{
				Name: "review", Pkg: "ext_review", Factory: "Extension", Root: extRoot,
				ModulePath: "example.com/extensions", Package: "example.com/extensions/review",
			}}, workspace); err != nil {
				t.Fatal(err)
			}
			modPath, sumPath := overlay.modfile, filepath.Join(filepath.Dir(overlay.modfile), "go.sum")
			if workspace {
				if overlay.modfile != "" {
					t.Fatalf("workspace mode must overlay the root files, not write a -modfile: %s", overlay.modfile)
				}
				modPath, sumPath = overlay.replace[filepath.Join(root, "go.mod")], overlay.replace[filepath.Join(root, "go.sum")]
			} else if _, overlaid := overlay.replace[filepath.Join(root, "go.mod")]; overlaid {
				t.Fatal("modfile mode must not overlay the root go.mod, which the tidy step could not update")
			}
			stagedMod, err := os.ReadFile(modPath)
			if err != nil {
				t.Fatal(err)
			}
			if want := "example.com/local => " + filepath.ToSlash(filepath.Join(extRoot, "vendored", "local")); !strings.Contains(string(stagedMod), want) {
				t.Fatalf("build go.mod missing the local replacement %q:\n%s", want, stagedMod)
			}
			for _, unwanted := range []string{"extensions/sdk => ", "example.com/dep"} {
				if strings.Contains(string(stagedMod), unwanted) {
					t.Fatalf("build go.mod must leave %q to Pig's pins and the tidy step:\n%s", unwanted, stagedMod)
				}
			}
			stagedSum, err := os.ReadFile(sumPath)
			if err != nil {
				t.Fatal(err)
			}
			want := "example.com/dep v1.2.3 h1:dep=\ngolang.org/x/sys v0.47.0 h1:pig=\ngolang.org/x/sys v0.47.0/go.mod h1:pigmod=\n"
			if string(stagedSum) != want {
				t.Fatalf("build go.sum is not the sorted union:\n%s", stagedSum)
			}
			for _, name := range []string{"go.mod", "go.sum"} {
				if committed, _ := os.ReadFile(filepath.Join(root, name)); strings.Contains(string(committed), "dep") || strings.Contains(string(committed), "local") {
					t.Fatalf("%s was written:\n%s", name, committed)
				}
			}
		})
	}
}
