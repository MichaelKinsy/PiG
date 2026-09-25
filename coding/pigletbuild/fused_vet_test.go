package pigletbuild

import (
	"bytes"
	"context"
	"go/build"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	pigletartifact "github.com/MichaelKinsy/PiG/coding/piglet/artifact"
)

func TestBuildNativeArtifactRejectsFusedProcessHazards(t *testing.T) {
	tests := []struct {
		name   string
		source string
		symbol string
	}{
		{name: "exit", source: "package hazard\nimport \"os\"\nfunc Extension() { os.Exit(1) }\n", symbol: "os.Exit"},
		{name: "aliased exit", source: "package hazard\nimport \"os\"\nvar exit = os.Exit\nfunc Extension() { exit(1) }\n", symbol: "os.Exit"},
		{name: "chdir", source: "package hazard\nimport \"os\"\nfunc Extension() { _ = os.Chdir(\"/\") }\n", symbol: "os.Chdir"},
		{name: "fatal", source: "package hazard\nimport \"log\"\nfunc Extension() { log.Fatal(\"stop\") }\n", symbol: "log.Fatal"},
		{name: "print", source: "package hazard\nimport \"fmt\"\nfunc Extension() { fmt.Println(\"noise\") }\n", symbol: "fmt.Println"},
		{name: "stdout", source: "package hazard\nimport (\"fmt\"; \"os\")\nfunc Extension() { _, _ = fmt.Fprintln(os.Stdout, \"noise\") }\n", symbol: "os.Stdout"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			extRoot := writeFusedVetModule(t, test.source)
			cell := fusedVetCell(extRoot)
			plan := pigletartifact.Plan{Components: []pigletartifact.Component{{
				Kind: pigletartifact.ComponentKindExtension, Name: "hazard",
				Realization: pigletartifact.RealizationFused, Materialization: pigletartifact.MaterializationBinary,
			}}}
			t.Setenv("PIG_SOURCE_ROOT", t.TempDir())
			_, err := buildNativeArtifact(context.Background(), &piglet.Piglet{Name: "hazard"}, []subprocess.CellSpec{cell}, plan, nil, Options{}, filepath.Join(t.TempDir(), "pig-hazard"), &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), fusedProcessHazardDiagnostic) || !strings.Contains(err.Error(), test.symbol) {
				t.Fatalf("build error = %v, want %s naming %s", err, fusedProcessHazardDiagnostic, test.symbol)
			}
		})
	}
}

func TestVetFusedPackagesInspectsLocalDependencyClosure(t *testing.T) {
	root := writeFusedVetModule(t, "package hazard\nimport \"example.com/hazard/internal/die\"\nfunc Extension() { die.Now() }\n")
	helperDir := filepath.Join(root, "internal", "die")
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(helperDir, "die.go"), []byte("package die\nimport \"os\"\nfunc Now() { os.Exit(1) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := collectFused(fusedVetCell(root))
	if err != nil {
		t.Fatal(err)
	}
	err = vetFusedPackages(context.Background(), entries)
	if err == nil || !strings.Contains(err.Error(), "os.Exit") || !strings.Contains(err.Error(), filepath.Join("internal", "die", "die.go")) {
		t.Fatalf("vet error = %v, want transitive os.Exit diagnostic", err)
	}
}

func TestVetFusedPackagesAllowsStderrAndReturnedErrors(t *testing.T) {
	root := writeFusedVetModule(t, "package hazard\nimport (\"errors\"; \"fmt\"; \"os\")\nfunc Extension() error { _, _ = fmt.Fprintln(os.Stderr, \"diagnostic\"); return errors.New(\"stop\") }\n")
	entries, err := collectFused(fusedVetCell(root))
	if err != nil {
		t.Fatal(err)
	}
	if err := vetFusedPackages(context.Background(), entries); err != nil {
		t.Fatalf("safe fused package rejected: %v", err)
	}
}

func writeFusedVetModule(t *testing.T, source string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/hazard\n\ngo 1.26.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "extension.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func fusedVetCell(root string) subprocess.CellSpec {
	return subprocess.CellSpec{
		Key: "packed-go:hazard", Strategy: subprocess.CellStrategyPackedGo, Language: "go",
		Extensions: []subprocess.ExtConfig{{
			Name: "hazard", Source: root, RuntimeKind: "subprocess", RuntimeLanguage: "go",
			EntrypointKind: "factory", ModulePath: "example.com/hazard", Package: "example.com/hazard", Factory: "Extension",
		}},
	}
}

func TestFusedVetUsesNativeBuildCGOSelection(t *testing.T) {
	root := writeFusedVetModule(t, "package hazard\nfunc Extension() {}\n")
	cgoSource := "package hazard\n/* */\nimport \"C\"\nimport \"os\"\nfunc exitPig() { os.Exit(1) }\n"
	if err := os.WriteFile(filepath.Join(root, "hazard_cgo.go"), []byte(cgoSource), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := collectFused(fusedVetCell(root))
	if err != nil {
		t.Fatal(err)
	}
	err = vetFusedPackages(context.Background(), entries)
	hasHazard := err != nil && strings.Contains(err.Error(), "os.Exit")
	if hasHazard != build.Default.CgoEnabled {
		t.Fatalf("CGO_ENABLED selection mismatch: vet os.Exit hazard = %t, native build CGO = %t; error = %v", hasHazard, build.Default.CgoEnabled, err)
	}
}

func TestFusedVetUsesHostBuildConstraints(t *testing.T) {
	root := writeFusedVetModule(t, "package hazard\nfunc Extension() {}\n")
	excluded := "//go:build !" + runtime.GOOS + "\n\npackage hazard\nimport \"os\"\nfunc excluded() { os.Exit(1) }\n"
	if err := os.WriteFile(filepath.Join(root, "excluded.go"), []byte(excluded), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := collectFused(fusedVetCell(root))
	if err != nil {
		t.Fatal(err)
	}
	if err := vetFusedPackages(context.Background(), entries); err != nil {
		t.Fatalf("build-excluded hazard rejected: %v", err)
	}
}
