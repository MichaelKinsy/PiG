package pigletbuild

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/extension/installresolver"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
)

func TestBakePigletInlinesAgentShapeAndStripsBuildOrigins(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.md"), []byte("BAKED PROMPT BODY"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(dir, "skills", "commit")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: commit\ndescription: Commit style\n---\n\nUse the repo commit format."), 0o644); err != nil {
		t.Fatal(err)
	}

	builtins := []string{"read", "grep"}
	extensionTools := []string{"ask"}
	source := &piglet.Piglet{
		Name:         "research",
		BuiltinTools: &builtins,
		Model:        &piglet.ModelConfig{Provider: "github-copilot", Name: "gpt-5-mini", Thinking: "high"},
		SystemPrompt: &piglet.PromptRef{File: "p.md"},
		Extensions: []piglet.ExtensionEntry{{
			Name:    "subagent",
			Origins: []string{"local:" + filepath.Join(dir, "extensions", "subagent")},
			Tools:   &extensionTools,
		}},
		Skills:    []piglet.SkillEntry{{Name: "commit", Origins: []string{"local:" + skillDir}}},
		Packages:  map[string]string{"base": "npm:@acme/base@1.0.0"},
		Build:     &piglet.BuildSpec{OutputName: "pig-research"},
		Discovery: &piglet.Discovery{Skills: []string{"workspace"}},
	}

	encoded, err := bakePiglet(source, dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := piglet.ParseBytes(encoded)
	if err != nil {
		t.Fatalf("embedded Piglet did not parse: %v\n%s", err, encoded)
	}
	if got.Model == nil || got.Model.Name != "gpt-5-mini" || got.Model.Thinking != "high" {
		t.Errorf("model = %+v", got.Model)
	}
	if got.SystemPrompt == nil || got.SystemPrompt.Text != "BAKED PROMPT BODY" || got.SystemPrompt.File != "" {
		t.Errorf("prompt = %+v", got.SystemPrompt)
	}
	if got.BuiltinTools == nil || !slices.Equal(*got.BuiltinTools, builtins) {
		t.Errorf("built-in tools = %#v", got.BuiltinTools)
	}
	if len(got.Extensions) != 1 || got.Extensions[0].Name != "subagent" || len(got.Extensions[0].Origins) != 0 || got.Extensions[0].Tools == nil || !slices.Equal(*got.Extensions[0].Tools, extensionTools) {
		t.Errorf("extensions = %+v", got.Extensions)
	}
	if len(got.Skills) != 1 || got.Skills[0].Name != "commit" || got.Skills[0].Description != "Commit style" || got.Skills[0].Content != "Use the repo commit format." || len(got.Skills[0].Origins) != 0 {
		t.Errorf("skills = %+v", got.Skills)
	}
	if len(got.Packages) != 0 || got.Build != nil {
		t.Errorf("build-only source survived: packages=%+v build=%+v", got.Packages, got.Build)
	}
	if got.Discovery == nil || !slices.Equal(got.Discovery.Skills, []string{"workspace"}) {
		t.Errorf("discovery = %+v", got.Discovery)
	}
}

func TestBakePigletRejectsBareSkillName(t *testing.T) {
	_, err := bakePiglet(&piglet.Piglet{Name: "bad", Skills: []piglet.SkillEntry{{Name: "local-only"}}}, "")
	if err == nil {
		t.Fatal("bakePiglet succeeded with bare skill name")
	}
}

func TestAC55BinaryBakePreservesPackageAliasesThroughResolution(t *testing.T) {
	root := t.TempDir()
	packageDir := filepath.Join(root, "package")
	skillDir := filepath.Join(packageDir, "skills", "commit")
	extensionDir := filepath.Join(packageDir, "extensions", "ask")
	for _, dir := range []string{skillDir, extensionDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(packageDir, "package.json"), []byte(`{"name":"devkit"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: commit\ndescription: Commit style\n---\n\nUse package commit style."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extensionDir, "index.js"), []byte("export default function extension(pi) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pigletPath := filepath.Join(root, "piglet.yaml")
	pigletYAML := "name: meta\npackages:\n  devkit: local:./package\nextensions:\n  - name: ask\n    origins: [package:devkit]\nskills:\n  - name: commit\n    origins: [package:devkit]\n"
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
			t.Fatalf("materializer source = %q, want %q", source, resolvedPackageDir)
		}
		return resolvedPackageDir, nil
	})
	t.Cleanup(func() { installresolver.SetMaterializer(nil) })
	cells := []subprocess.CellSpec{{
		Strategy: subprocess.CellStrategyPackedGo,
		Language: "go",
		Extensions: []subprocess.ExtConfig{{
			Name: "ask", Source: extensionDir, ContentHash: strings.Repeat("a", 64),
		}},
	}}
	inputs, err := buildInputs(parsed, cells)
	if err != nil {
		t.Fatal(err)
	}
	var packageInput, extensionInput, skillInput buildInput
	for _, input := range inputs {
		switch {
		case input.Kind == "package" && input.Name == "devkit":
			packageInput = input
		case input.Kind == "skill" && input.Name == "commit":
			skillInput = input
		case input.Kind == "extension" && input.Name == "ask":
			extensionInput = input
		}
	}
	if packageInput.Name == "" || extensionInput.Package != "devkit" || extensionInput.Source != "package:devkit" || skillInput.Package != "devkit" || skillInput.Source != "package:devkit" {
		t.Fatalf("Package-backed resolution inputs = package:%+v extension:%+v skill:%+v", packageInput, extensionInput, skillInput)
	}

	encoded, err := bakePiglet(parsed, root)
	if err != nil {
		t.Fatal(err)
	}
	baked, err := piglet.ParseBytes(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if baked.Skills[0].Content != "Use package commit style." || len(baked.Skills[0].Origins) != 0 {
		t.Fatalf("baked package skill = %+v", baked.Skills[0])
	}
	if len(baked.Packages) != 0 || len(baked.Extensions[0].Origins) != 0 {
		t.Fatalf("build origins survived: packages=%v extensions=%+v", baked.Packages, baked.Extensions)
	}
	if parsed.Packages["devkit"] == "" {
		t.Fatal("baking mutated the authored Package alias")
	}
}
