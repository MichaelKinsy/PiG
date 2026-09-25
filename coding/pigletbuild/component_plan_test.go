package pigletbuild

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/extension/installresolver"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	pigletartifact "github.com/MichaelKinsy/PiG/coding/piglet/artifact"
)

func TestAC2ResolutionRecordIncludesComponentPlan(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	pigletPath := filepath.Join(root, "piglet.yaml")
	if err := os.WriteFile(pigletPath, []byte("name: review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := piglet.Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	host := Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
	lock, err := buildPortableLock(parsed, nil, Options{
		Targets: []Target{host}, Sandbox: Sandbox{Native: host},
		BakedSettings: []byte("name: review\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(lock.ComponentPlan.Digest, "sha256:") {
		t.Fatalf("component plan = %#v", lock.ComponentPlan)
	}
	if err := pigletartifact.ValidateRecord(lock.ResolutionRecord); err != nil {
		t.Fatalf("ValidateRecord() error = %v", err)
	}
	if lock.ResolutionRecord.Resolution.ComponentPlan.Digest != lock.ComponentPlan.Digest {
		t.Fatalf("resolution component digest = %q, want %q", lock.ResolutionRecord.Resolution.ComponentPlan.Digest, lock.ComponentPlan.Digest)
	}
}

func TestAC2RecordLocksSelectedExtensionOrigin(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	extensionDir := filepath.Join(root, "extensions", "review")
	if err := os.MkdirAll(extensionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pigletPath := filepath.Join(root, "piglet.yaml")
	data := []byte("name: review\nextensions:\n  - name: review\n    origins:\n      - local:extensions/missing\n      - local:extensions/review\n")
	if err := os.WriteFile(pigletPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := piglet.Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	cells := []subprocess.CellSpec{{
		Strategy: subprocess.CellStrategyPackedGo, Language: "go",
		Extensions: []subprocess.ExtConfig{{Name: "review", Source: extensionDir, ContentHash: strings.Repeat("a", 64)}},
	}}
	inputs, err := buildInputs(parsed, cells)
	if err != nil {
		t.Fatal(err)
	}
	var extensionInput buildInput
	for _, input := range inputs {
		if input.Kind == "extension" && input.Name == "review" {
			extensionInput = input
			break
		}
	}
	// The recorded origin is the local source's identity: an absolute host
	// path with the host's separators.
	if !strings.Contains(extensionInput.Source, filepath.Join("extensions", "review")) || strings.Contains(extensionInput.Source, filepath.Join("extensions", "missing")) {
		t.Fatalf("extension source = %q", extensionInput.Source)
	}

	target := Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
	delivery := BuildPlan(extensionInputsFromCells(cells), Options{
		Targets: []Target{target}, Sandbox: Sandbox{Native: target},
	})
	componentPlan, err := buildPigletComponentPlan(cells, delivery, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if got := componentPlan.Components[0].Origin.Source; got != extensionInput.Source {
		t.Fatalf("component origin source = %q, want %q", got, extensionInput.Source)
	}
}

func TestAC2RecordLocksSelectedPackageMemberOrigin(t *testing.T) {
	root := t.TempDir()
	packageDir := filepath.Join(root, "package")
	extensionDir := filepath.Join(packageDir, "extensions", "review")
	if err := os.MkdirAll(extensionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "package.json"), []byte(`{"name":"base"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extensionDir, "index.js"), []byte("export default function extension(pi) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pigletPath := filepath.Join(root, "piglet.yaml")
	pigletYAML := "name: review\npackages:\n  base: local:./package\nextensions:\n  - name: review\n    origins: [package:base]\n"
	if err := os.WriteFile(pigletPath, []byte(pigletYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := piglet.Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	resolvedPackageDir, err := filepath.EvalSymlinks(packageDir)
	if err != nil {
		t.Fatal(err)
	}
	installresolver.SetMaterializer(func(_, source, _ string, _, _ io.Writer) (string, error) {
		if source != resolvedPackageDir {
			t.Fatalf("materializer source = %q", source)
		}
		return resolvedPackageDir, nil
	})
	t.Cleanup(func() { installresolver.SetMaterializer(nil) })
	cells := []subprocess.CellSpec{{
		Strategy: subprocess.CellStrategyPackedGo, Language: "go",
		Extensions: []subprocess.ExtConfig{{Name: "review", Source: extensionDir, ContentHash: strings.Repeat("a", 64)}},
	}}
	inputs, err := buildInputs(parsed, cells)
	if err != nil {
		t.Fatal(err)
	}
	var extensionInput buildInput
	for _, input := range inputs {
		if input.Kind == "extension" && input.Name == "review" {
			extensionInput = input
			break
		}
	}
	if extensionInput.Package != "base" || extensionInput.Source != "package:base" {
		t.Fatalf("extension input = %#v", extensionInput)
	}

	target := Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
	delivery := BuildPlan(extensionInputsFromCells(cells), Options{
		Targets: []Target{target}, Sandbox: Sandbox{Native: target},
	})
	componentPlan, err := buildPigletComponentPlan(cells, delivery, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if componentPlan.Components[0].Origin.Package != "base" {
		t.Fatalf("component origin = %#v", componentPlan.Components[0].Origin)
	}
}

func TestAC7NativeBuildProducesLinkedBinaryRecord(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	pigletPath := filepath.Join(root, "piglet.yaml")
	if err := os.WriteFile(pigletPath, []byte("name: review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := piglet.Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	artifactPath := writeFakePigArtifact(t, root, "pig-review")
	host := Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
	lock, err := buildNativeLock(parsed, nil, Options{
		Targets: []Target{host}, Sandbox: Sandbox{Native: host},
		Version: "1.0.0", BakedSettings: []byte("name: review\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	records, err := buildBinaryRecords(lock, parsed, artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := pigletartifact.ValidateBinaryLink(records.Resolution, records.Binary); err != nil {
		t.Fatalf("ValidateBinaryLink() error = %v", err)
	}
	if records.Binary.Binary.ResolutionDigest != records.Resolution.Digest {
		t.Fatalf("Piglet Binary record = %#v", records.Binary.Binary)
	}
}

func TestAC2NativeBuildDispositionUsesComponentPlan(t *testing.T) {
	t.Parallel()

	cell := subprocess.CellSpec{
		Strategy: subprocess.CellStrategyPackedGo, Language: "go",
		Extensions: []subprocess.ExtConfig{{Name: "review"}},
	}
	fused := pigletartifact.Plan{Components: []pigletartifact.Component{{
		Kind: pigletartifact.ComponentKindExtension, Name: "review",
		Realization: pigletartifact.RealizationFused, Materialization: pigletartifact.MaterializationBinary,
	}}}
	realization, materialization, err := cellComponentDisposition(cell, fused)
	if err != nil {
		t.Fatal(err)
	}
	if realization != pigletartifact.RealizationFused || materialization != pigletartifact.MaterializationBinary {
		t.Fatalf("fused disposition = %q/%q", realization, materialization)
	}

	subprocessPlan := fused
	subprocessPlan.Components = []pigletartifact.Component{{
		Kind: pigletartifact.ComponentKindExtension, Name: "review",
		Realization: pigletartifact.RealizationSubprocess, Materialization: pigletartifact.MaterializationRelease,
	}}
	realization, materialization, err = cellComponentDisposition(cell, subprocessPlan)
	if err != nil {
		t.Fatal(err)
	}
	if realization != pigletartifact.RealizationSubprocess || materialization != pigletartifact.MaterializationRelease {
		t.Fatalf("subprocess disposition = %q/%q", realization, materialization)
	}

	mixedCell := cell
	mixedCell.Extensions = append(mixedCell.Extensions, subprocess.ExtConfig{Name: "other"})
	mixedPlan := fused
	mixedPlan.Components = append(slices.Clone(fused.Components), pigletartifact.Component{
		Kind: pigletartifact.ComponentKindExtension, Name: "other",
		Realization: pigletartifact.RealizationSubprocess, Materialization: pigletartifact.MaterializationBinary,
	})
	if _, _, err := cellComponentDisposition(mixedCell, mixedPlan); err == nil || !strings.Contains(err.Error(), "mixed realization") {
		t.Fatalf("mixed disposition error = %v", err)
	}
}

func TestAC2AdapterPreservesRuntimeDecisions(t *testing.T) {
	t.Parallel()

	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cells := []subprocess.CellSpec{
		{
			Strategy: subprocess.CellStrategyPackedGo, Language: "go",
			Extensions: []subprocess.ExtConfig{{Name: "review"}},
		},
		{
			Strategy: subprocess.CellStrategyPackedPython, Language: "python",
			Extensions: []subprocess.ExtConfig{{Name: "analysis"}},
		},
	}
	target := Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
	delivery := BuildPlan(extensionInputsFromCells(cells), Options{
		Targets: []Target{target}, Sandbox: Sandbox{Native: target},
	})
	plan, err := buildPigletComponentPlan(cells, delivery, []buildInput{
		{Kind: "extension", Name: "review", Source: "package:base", Package: "base", Digest: digest},
		{Kind: "extension", Name: "analysis", Source: "content-addressed", Digest: digest},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Components) != 2 {
		t.Fatalf("component count = %d, want 2", len(plan.Components))
	}
	if plan.Components[0].Name != "analysis" || plan.Components[0].Realization != pigletartifact.RealizationSubprocess || plan.Components[0].Materialization != pigletartifact.MaterializationExternal {
		t.Fatalf("analysis component = %#v", plan.Components[0])
	}
	if plan.Components[1].Name != "review" || plan.Components[1].Realization != pigletartifact.RealizationFused || plan.Components[1].Materialization != pigletartifact.MaterializationBinary || plan.Components[1].Origin.Package != "base" {
		t.Fatalf("review component = %#v", plan.Components[1])
	}
}
