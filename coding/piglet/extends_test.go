package piglet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func writePigletSource(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name+".yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestAC4ExtendsLineage pins typed source/version resolution, exact digest
// lineage, cycle detection, and the maximum inheritance depth of eight.
func TestAC4ExtendsLineage(t *testing.T) {
	t.Run("exact lineage and version constraint", func(t *testing.T) {
		dir := t.TempDir()
		writePigletSource(t, dir, "base", "name: base\nrelease:\n  version: 1.4.0\ndescription: base\n")
		writePigletSource(t, dir, "middle", "name: middle\nextends:\n  source: local:./base.yaml\n  version: '>=1.0 <2.0'\ndescription: middle\n")
		child := writePigletSource(t, dir, "child", "name: child\nextends:\n  source: local:./middle.yaml\ndescription: child\n")

		resolved, err := ResolveEffective(child)
		if err != nil {
			t.Fatal(err)
		}
		if len(resolved.Lineage) != 3 || resolved.Lineage[0].Source != "local:base.yaml" || resolved.Lineage[1].Source != "local:middle.yaml" || resolved.Lineage[2].Source != "local:child.yaml" {
			t.Fatalf("lineage = %#v", resolved.Lineage)
		}
		for _, digest := range []string{resolved.Lineage[0].Digest, resolved.Lineage[1].Digest, resolved.Lineage[2].Digest, resolved.SourceDigest, resolved.EffectiveDigest, resolved.GraphDigest} {
			if !strings.HasPrefix(digest, "sha256:") {
				t.Fatalf("digest = %q", digest)
			}
		}
		if resolved.Piglet.Name != "child" || resolved.Piglet.Description != "child" || resolved.Piglet.Extends != nil || resolved.Piglet.Release != nil || resolved.Piglet.Build != nil {
			t.Fatalf("effective Piglet = %#v", resolved.Piglet)
		}
	})

	t.Run("version mismatch", func(t *testing.T) {
		dir := t.TempDir()
		writePigletSource(t, dir, "base", "name: base\nrelease:\n  version: 1.4.0\n")
		child := writePigletSource(t, dir, "child", "name: child\nextends:\n  source: local:./base.yaml\n  version: ^2.0\n")
		if _, err := ResolveEffective(child); err == nil || !strings.Contains(err.Error(), "does not satisfy") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("cycle", func(t *testing.T) {
		dir := t.TempDir()
		a := writePigletSource(t, dir, "a", "name: a\nextends:\n  source: local:./b.yaml\n")
		writePigletSource(t, dir, "b", "name: b\nextends:\n  source: local:./a.yaml\n")
		if _, err := ResolveEffective(a); err == nil || !strings.Contains(err.Error(), "extends cycle") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("depth eight passes and nine fails", func(t *testing.T) {
		dir := t.TempDir()
		writePigletSource(t, dir, "p0", "name: p0\n")
		for i := 1; i <= 9; i++ {
			writePigletSource(t, dir, "p"+string(rune('0'+i)), "name: p"+string(rune('0'+i))+"\nextends:\n  source: local:./p"+string(rune('0'+i-1))+".yaml\n")
		}
		if _, err := ResolveEffective(filepath.Join(dir, "p8.yaml")); err != nil {
			t.Fatalf("depth 8: %v", err)
		}
		if _, err := ResolveEffective(filepath.Join(dir, "p9.yaml")); err == nil || !strings.Contains(err.Error(), "depth exceeds 8") {
			t.Fatalf("depth 9 error = %v", err)
		}
	})
}

// TestAC5MergeAlgebra pins whole-field replacement, deterministic named
// collection replacement/addition/removal, independent path anchors, and
// dangling/absent-removal rejection.
func TestAC5MergeAlgebra(t *testing.T) {
	dir := t.TempDir()
	baseDir := filepath.Join(dir, "base")
	childDir := filepath.Join(dir, "child")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePigletSource(t, baseDir, "base", `name: base
description: base description
model:
  provider: base
  name: base-model
packages:
  keep: npm:@acme/keep@1
  remove-me: npm:@acme/remove@1
extensions:
  - name: first
    origins: [package:keep]
  - name: replace
    origins: [local:./base-extension]
skills:
  - name: remove-skill
    content: inherited
`)
	child := writePigletSource(t, childDir, "child", `name: child
extends:
  source: local:../base/base.yaml
  remove:
    packages: [remove-me]
    skills: [remove-skill]
model:
  provider: child
extensions:
  - name: replace
    origins: [local:./child-extension]
  - name: appended
skills:
  - name: child-skill
    content: child
`)

	canonicalChild, err := canonicalPigletPath(child)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveEffective(child)
	if err != nil {
		t.Fatal(err)
	}
	p := resolved.Piglet
	if p.Description != "base description" || p.Model == nil || p.Model.Provider != "child" || p.Model.Name != "" {
		t.Fatalf("whole-field merge: description=%q model=%#v", p.Description, p.Model)
	}
	if len(p.Packages) != 1 || p.Packages["keep"] == "" {
		t.Fatalf("packages = %#v", p.Packages)
	}
	if len(p.Extensions) != 3 || p.Extensions[0].Name != "first" || p.Extensions[1].Name != "replace" || p.Extensions[2].Name != "appended" {
		t.Fatalf("extensions order = %#v", p.Extensions)
	}
	if got := p.Extensions[1].Origins[0]; got != "local:"+filepath.Join(filepath.Dir(canonicalChild), "child-extension") {
		t.Fatalf("child origin = %q", got)
	}
	if got := p.Extensions[0].Origins[0]; got != "package:keep" {
		t.Fatalf("base origin = %q", got)
	}
	if len(p.Skills) != 1 || p.Skills[0].Name != "child-skill" {
		t.Fatalf("skills = %#v", p.Skills)
	}
	if p.Extends != nil || p.Release != nil || p.Build != nil {
		t.Fatalf("derivation metadata survived: %#v", p)
	}

	t.Run("absent removal", func(t *testing.T) {
		bad := writePigletSource(t, childDir, "absent", "name: absent\nextends:\n  source: local:../base/base.yaml\n  remove:\n    extensions: [missing]\n")
		if _, err := ResolveEffective(bad); err == nil || !strings.Contains(err.Error(), "is absent") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("dangling package replacement", func(t *testing.T) {
		bad := writePigletSource(t, childDir, "dangling", "name: dangling\nextends:\n  source: local:../base/base.yaml\npackages:\n  keep: npm:@acme/different@1\n")
		if _, err := ResolveEffective(bad); err == nil || !strings.Contains(err.Error(), "inherited extension") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("complete package replacement", func(t *testing.T) {
		child := writePigletSource(t, childDir, "replacement", "name: replacement\nextends:\n  source: local:../base/base.yaml\npackages:\n  keep: npm:@acme/different@1\nextensions:\n  - name: first\n    origins: [package:keep]\n")
		resolved, err := ResolveEffective(child)
		if err != nil {
			t.Fatal(err)
		}
		if got := resolved.Piglet.Packages["keep"]; got != "npm:@acme/different@1" {
			t.Fatalf("replacement source = %q", got)
		}
	})

	t.Run("escaping and symlink paths", func(t *testing.T) {
		escaping := writePigletSource(t, childDir, "escaping", "name: escaping\nsystemPrompt:\n  file: ../outside.md\n")
		if _, err := ResolveEffective(escaping); err == nil || !strings.Contains(err.Error(), "escapes the Piglet anchor") {
			t.Fatalf("escaping error = %v", err)
		}
		home := writePigletSource(t, childDir, "home", "name: home\nsystemPrompt:\n  file: ~/prompt.md\n")
		if _, err := ResolveEffective(home); err == nil || !strings.Contains(err.Error(), "relative piglet: path") {
			t.Fatalf("home path error = %v", err)
		}
		workspace := writePigletSource(t, childDir, "workspace", "name: workspace\nsystemPrompt:\n  file: workspace:prompt.md\n")
		if _, err := ResolveEffective(workspace); err == nil || !strings.Contains(err.Error(), "piglet:-anchored") {
			t.Fatalf("workspace anchor error = %v", err)
		}
		outside := filepath.Join(dir, "outside")
		if err := os.MkdirAll(outside, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(childDir, "link")
		testenv.Symlink(t, outside, link)
		symlink := writePigletSource(t, childDir, "symlink", "name: symlink\nsystemPrompt:\n  file: link/prompt.md\n")
		if _, err := ResolveEffective(symlink); err == nil || !strings.Contains(err.Error(), "resolves outside the Piglet anchor") {
			t.Fatalf("symlink error = %v", err)
		}
	})
}

// TestAC6PolicyWidening pins that narrowing is allowed while widening needs
// explicit author intent.
func TestAC6PolicyWidening(t *testing.T) {
	dir := t.TempDir()
	writePigletSource(t, dir, "base", "name: base\ntools: [read, write]\n")
	narrow := writePigletSource(t, dir, "narrow", "name: narrow\nextends:\n  source: local:./base.yaml\ntools: [read]\n")
	if _, err := ResolveEffective(narrow); err != nil {
		t.Fatalf("narrow: %v", err)
	}
	wide := writePigletSource(t, dir, "wide", "name: wide\nextends:\n  source: local:./base.yaml\ntools: [read, write, bash]\n")
	if _, err := ResolveEffective(wide); err == nil || !strings.Contains(err.Error(), "requires extends.allowWiden: true") {
		t.Fatalf("widen error = %v", err)
	}
	allowed := writePigletSource(t, dir, "allowed", "name: allowed\nextends:\n  source: local:./base.yaml\n  allowWiden: true\ntools: [read, write, bash]\n")
	if _, err := ResolveEffective(allowed); err != nil {
		t.Fatalf("allowed widening: %v", err)
	}

	writePigletSource(t, dir, "env-base", "name: env-base\nagentEnv:\n  image: dev:1\n  policy:\n    preset: elevated\n  mounts:\n    - source: ./safe\n      target: /data\n      readonly: true\n")
	envWide := writePigletSource(t, dir, "env-wide", "name: env-wide\nextends:\n  source: local:./env-base.yaml\nagentEnv:\n  image: dev:1\n  policy:\n    preset: elevated\n  mounts:\n    - source: ./other\n      target: /data\n      readonly: false\n")
	if _, err := ResolveEffective(envWide); err == nil || !strings.Contains(err.Error(), "agentEnv") {
		t.Fatalf("mount widening error = %v", err)
	}
}

// TestExtendsProductionCommands proves effective resolution is used by the
// public show/validate paths, while ordinary show still exposes authored source.
func TestExtendsProductionCommands(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	dir := t.TempDir()
	writePigletSource(t, dir, "base", "name: base\ndescription: inherited\n")
	child := writePigletSource(t, dir, "child", "name: child\nextends:\n  source: local:./base.yaml\n")

	var stdout, stderr strings.Builder
	if code := RunCommand([]string{"piglet", "show", child, "--effective", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("show --effective code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"description": "inherited"`) || strings.Contains(stdout.String(), `"extends"`) {
		t.Fatalf("effective output = %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := RunCommand([]string{"piglet", "validate", child, "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("validate code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"valid":true`) {
		t.Fatalf("validate output = %s", stdout.String())
	}
}
