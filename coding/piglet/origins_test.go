package piglet

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/installresolver"
	sourceref "github.com/MichaelKinsy/PiG/coding/source"
)

func TestResolveAgentEnvironmentFromPackageSource(t *testing.T) {
	packageRoot := t.TempDir()
	definition := filepath.Join(packageRoot, ".devcontainer", "go", "devcontainer.json")
	if err := os.MkdirAll(filepath.Dir(definition), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"name":"@acme/dev"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(definition, []byte(`{"image":"golang:1.26"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	installresolver.SetMaterializer(func(_, source, scope string, _, _ io.Writer) (string, error) {
		if source != "npm:@acme/dev@1" || scope != "user" {
			t.Fatalf("materialize source/scope = %q/%q", source, scope)
		}
		return packageRoot, nil
	})
	t.Cleanup(func() { installresolver.SetMaterializer(nil) })
	piglet := &Piglet{Name: "dev", AgentEnv: &AgentEnvironment{Source: "npm:@acme/dev@1"}}
	if err := piglet.Validate(); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveAgentEnvironment(piglet)
	if err != nil {
		t.Fatal(err)
	}
	if resolved == nil || resolved.Form != "source" || resolved.Value != definition {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestPigletMaterializationUsesWorkspaceScopeAndTypedLocalSource(t *testing.T) {
	workspace := t.TempDir()
	pigletDir := filepath.Join(workspace, ".pig", "piglets")
	if err := os.MkdirAll(pigletDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)
	canonicalWorkspace := canonicalPath(workspace)
	installresolver.SetMaterializer(func(cwd, source, scope string, _, _ io.Writer) (string, error) {
		want := filepath.Join(canonicalWorkspace, "resources", "base")
		if cwd != canonicalWorkspace || source != want || scope != "project" {
			t.Fatalf("materialize = cwd:%q source:%q scope:%q, want %q", cwd, source, scope, want)
		}
		return source, nil
	})
	t.Cleanup(func() { installresolver.SetMaterializer(nil) })
	piglet := &Piglet{sourceDir: pigletDir}
	if _, err := piglet.materialize("local:../../resources/base", sourceref.BareReject); err != nil {
		t.Fatal(err)
	}
}

func TestResolvePackageMembersMaterializesOnce(t *testing.T) {
	packageRoot := t.TempDir()
	extensionDir := filepath.Join(packageRoot, "extensions", "trace")
	if err := os.MkdirAll(extensionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extensionDir, "index.js"), []byte("export default function extension(pi) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(packageRoot, "skills", "review-source")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: review\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"name":"@acme/base"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	installresolver.SetMaterializer(func(_, source, scope string, _, _ io.Writer) (string, error) {
		calls++
		if source != "npm:@acme/base@1.0.0" || scope != "user" {
			t.Fatalf("materialize = %q/%q", source, scope)
		}
		return packageRoot, nil
	})
	t.Cleanup(func() { installresolver.SetMaterializer(nil) })
	piglet := &Piglet{
		Name:       "research",
		Packages:   map[string]string{"base": "npm:@acme/base@1.0.0"},
		Extensions: []ExtensionEntry{{Name: "trace", Origins: []string{"package:base"}}},
		Skills:     []SkillEntry{{Name: "review", Origins: []string{"package:base"}}},
	}
	if err := piglet.Validate(); err != nil {
		t.Fatal(err)
	}
	extensions, extensionErrors := ResolveExtensions(piglet)
	skills, skillErrors := ResolveSkills(piglet)
	if len(extensionErrors) != 0 || len(skillErrors) != 0 || len(extensions) != 1 || len(skills) != 1 {
		t.Fatalf("extensions=%v skills=%v errors=%v/%v", extensions, skills, extensionErrors, skillErrors)
	}
	if extensions[0].Path != filepath.Join(extensionDir, "index.js") || skills[0].Path != skillDir || calls != 1 {
		t.Fatalf("extensions=%v skills=%v calls=%d", extensions, skills, calls)
	}
}

func TestResolvePackageOriginFallsBackToTypedLocal(t *testing.T) {
	packageRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(`{"name":"empty"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	direct := filepath.Join(t.TempDir(), "fallback-review")
	if err := os.MkdirAll(direct, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(direct, "SKILL.md"), []byte("---\nname: review\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	installresolver.SetMaterializer(func(_, source, _ string, _, _ io.Writer) (string, error) {
		if source == "npm:empty" {
			return packageRoot, nil
		}
		return "", fmt.Errorf("unexpected source %q", source)
	})
	t.Cleanup(func() { installresolver.SetMaterializer(nil) })
	piglet := &Piglet{
		Name:     "research",
		Packages: map[string]string{"base": "npm:empty"},
		Skills:   []SkillEntry{{Name: "review", Origins: []string{"package:base", "local:" + direct}}},
	}
	resolved, errs := ResolveSkills(piglet)
	if len(errs) != 0 || len(resolved) != 1 || resolved[0].Path != direct || resolved[0].Origin != "local:"+direct {
		t.Fatalf("resolved=%+v errors=%v", resolved, errs)
	}
}

func TestValidatePackageAliasesAndTypedOrigins(t *testing.T) {
	valid := []byte("name: research\npackages:\n  base: npm:@acme/base@1.0.0\nextensions:\n  - name: trace\n    origins: [package:base, local:./trace]\n")
	if _, err := ParseBytes(valid); err != nil {
		t.Fatalf("valid Piglet rejected: %v", err)
	}
	cases := map[string]string{
		"unknown alias":       "name: x\nextensions:\n  - name: trace\n    origins: [package:missing]\n",
		"object origin":       "name: x\nextensions:\n  - name: trace\n    origins: [{path: ./trace}]\n",
		"bare origin":         "name: x\nextensions:\n  - name: trace\n    origins: [./trace]\n",
		"bare package source": "name: x\npackages:\n  base: package-name\n",
		"invalid alias":       "name: x\npackages:\n  'bad alias': npm:a\n",
		"duplicate origin":    "name: x\nextensions:\n  - name: trace\n    origins: [local:./trace, local:./trace]\n",
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseBytes([]byte(source)); err == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
}

func TestResolveExtensionsRelativeOriginUsesPigletDirectory(t *testing.T) {
	pigletDir := t.TempDir()
	extensionDir := filepath.Join(pigletDir, "extensions", "local")
	if err := os.MkdirAll(extensionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pigletPath := filepath.Join(pigletDir, "research.yaml")
	data := []byte("name: research\nextensions:\n  - name: local\n    origins: [local:./extensions/local]\n")
	if err := os.WriteFile(pigletPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	piglet, err := Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	resolved, errs := ResolveExtensions(piglet)
	if len(errs) != 0 || len(resolved) != 1 || resolved[0].Path != canonicalPath(extensionDir) {
		t.Fatalf("resolved=%+v errors=%v", resolved, errs)
	}
}

func TestResolveExtensionsOrderedTypedFallback(t *testing.T) {
	extensionDir := filepath.Join(t.TempDir(), "fallback")
	if err := os.MkdirAll(extensionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	piglet := &Piglet{Extensions: []ExtensionEntry{{
		Name: "fallback", Origins: []string{"local:./missing", "local:" + extensionDir},
	}}}
	resolved, errs := ResolveExtensions(piglet)
	if len(errs) != 0 || len(resolved) != 1 || resolved[0].Path != extensionDir {
		t.Fatalf("resolved=%+v errors=%v", resolved, errs)
	}
}

func TestResolveExtensionsContributedSource(t *testing.T) {
	const scheme = "originfixture"
	if !installresolver.SupportsSourceScheme(scheme) {
		if err := installresolver.RegisterSourceScheme(scheme); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	extensionDir := filepath.Join(root, "extensions", "subagent")
	if err := os.MkdirAll(extensionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extensionDir, "index.js"), []byte("export default function extension(pi) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	installresolver.SetMaterializer(func(_, source, _ string, _, _ io.Writer) (string, error) {
		if source != scheme+":catalog/subagent" {
			t.Fatalf("source = %q", source)
		}
		return root, nil
	})
	t.Cleanup(func() { installresolver.SetMaterializer(nil) })
	piglet := &Piglet{Extensions: []ExtensionEntry{{Name: "subagent", Origins: []string{scheme + ":catalog/subagent"}}}}
	resolved, errs := ResolveExtensions(piglet)
	if len(errs) != 0 || len(resolved) != 1 || resolved[0].Path != filepath.Join(extensionDir, "index.js") {
		t.Fatalf("resolved=%+v errors=%v", resolved, errs)
	}
}

func TestResolveExtensionWithoutOriginIsScopingOnly(t *testing.T) {
	resolved, errs := ResolveExtensions(&Piglet{Extensions: []ExtensionEntry{{Name: "scoping-only"}}})
	if len(errs) != 0 || len(resolved) != 0 {
		t.Fatalf("resolved=%v errors=%v", resolved, errs)
	}
}

func TestResolveSkillWithoutOriginFails(t *testing.T) {
	resolved, errs := ResolveSkills(&Piglet{Skills: []SkillEntry{{Name: "missing"}}})
	if len(errs) != 1 || len(resolved) != 0 {
		t.Fatalf("resolved=%v errors=%v", resolved, errs)
	}
}
