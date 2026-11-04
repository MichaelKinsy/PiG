package packagemanager

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// pi: packages/coding-agent/src/core/pi-manifest.ts
// pi: packages/coding-agent/src/core/package-manager.ts

// Ports packages/coding-agent/test/suite/regressions/7187-malformed-package-manifest.test.ts: a package manifest whose `pi.skills` is a
// string is dropped, while its valid `pi.prompts` array is kept, so resolve() lists the prompt and not the skill.
func TestResolveIgnoresInvalidManifestResourceFieldsWithoutDroppingValidFields(t *testing.T) {
	_, agentDir, sm, pm := resolveFixtureWithHome(t)
	packageDir := filepath.Join(agentDir, "npm", "node_modules", "bad-package")
	skillPath := filepath.Join(packageDir, "skills", "bad", "SKILL.md")
	promptPath := filepath.Join(packageDir, "prompts", "valid.md")
	writeResolveFile(t, skillPath, "---\nname: bad\ndescription: Must not load\n---\n")
	writeResolveFile(t, promptPath, "Valid prompt\n")
	writeResolveFile(t, filepath.Join(packageDir, "package.json"), `{"name":"bad-package","version":"1.0.0","pi":{"skills":"./skills","prompts":["./prompts"]}}`)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "npm:bad-package"}}); err != nil {
		t.Fatal(err)
	}

	resolved, err := pm.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	skills := resourcePaths(resolved.Skills)
	prompts := resourcePaths(resolved.Prompts)
	if slices.Contains(skills, skillPath) {
		t.Errorf("skills = %v, must not contain %s: pi.skills is not an array", skills, skillPath)
	}
	if !slices.Contains(prompts, promptPath) {
		t.Errorf("prompts = %v, must contain %s: the valid pi.prompts field is kept", prompts, promptPath)
	}
}

func resourcePaths(resources []codingagent.ResolvedResource) []string {
	paths := make([]string, len(resources))
	for i, resource := range resources {
		paths[i] = resource.Path
	}
	return paths
}
