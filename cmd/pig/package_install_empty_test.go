package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/packagecontent"
)

// Installing a directory that contributes no resource used to print "Installed"
// and record it in settings. Nothing loaded, and nothing said so: the reported
// symptom was an extension that installed cleanly and never appeared.
//
// Upstream discovers package resources from extensions/, skills/, prompts/ and
// themes/, so a single extension's own directory is not a package in Pi either.
// The fix is to say so, not to widen what a package is.

func writeBareGoExtension(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demoext\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := "package demoext\n\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\n\n" +
		"func Extension() *sdk.Extension { return sdk.New(\"demoext\") }\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestInstallRejectsAnExtensionDirectoryAsAPackage(t *testing.T) {
	dir := writeBareGoExtension(t)

	err := verifyPackageContributesResources(filepath.Dir(dir), dir, false)
	if err == nil {
		t.Fatal("installing a bare extension directory as a package reported success; " +
			"it contributes no resources and would never load")
	}
	// The message has to name the working path, or the user is left knowing only
	// that it failed.
	for _, want := range []string{"is an extension, not a package", "pig -e "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

// The guard must not reject a real package. This is the case that would make the
// fix worse than the bug.
func TestInstallAcceptsAPackageUsingConventionDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "prompts", "review.md"), []byte("# review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyPackageContributesResources(filepath.Dir(root), root, false); err != nil {
		t.Fatalf("a package with a conventional prompts/ directory was rejected: %v", err)
	}
}

// Every resource kind a package can contribute must keep it installable. A kind
// missing from the count would reject a package that legitimately ships only
// that kind, which is the silent inverse of the bug being fixed.
func TestEveryPackageResourceKindCountsAsAContribution(t *testing.T) {
	cases := map[string]packagecontent.Resources{
		"extensions":        {ExtensionEntries: []string{"e"}},
		"skills":            {SkillDirs: []string{"s"}},
		"prompts":           {PromptFiles: []string{"p"}},
		"themes":            {ThemeFiles: []string{"t"}},
		"agents":            {AgentFiles: []string{"a"}},
		"mcpServers":        {MCPFiles: []string{"m"}},
		"hooks":             {HookFiles: []string{"h"}},
		"agentEnvironments": {AgentEnvironments: []string{"ae"}},
	}
	for kind, resources := range cases {
		if packageResourceCount(resources) != 1 {
			t.Errorf("a package contributing only %s counts as empty and would be rejected", kind)
		}
	}
	if packageResourceCount(packagecontent.Resources{}) != 0 {
		t.Error("an empty package counts as contributing something; the guard would never fire")
	}
}

// The guard must prove an extension rather than infer a language. Language
// inference reports a spec for any recognized build file, so these would all be
// refused, and misnamed as extensions, if the guard trusted it. Each is a
// package Pi installs without complaint.
func TestInstallDoesNotRefuseDirectoriesThatMerelyLookLikeCode(t *testing.T) {
	cases := map[string]map[string]string{
		"plain npm package":       {"package.json": `{"name":"demo","version":"1.0.0"}`},
		"npm package with pi key": {"package.json": `{"name":"demo","pi":{"skills":["skills"]}}`},
		"go module with no factory": {
			"go.mod":  "module demo\n\ngo 1.26\n",
			"util.go": "package demo\n\nfunc Helper() {}\n",
		},
		"rust crate with no manifest": {"Cargo.toml": "[package]\nname = \"demo\"\n"},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for file, body := range files {
				if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := verifyPackageContributesResources(filepath.Dir(dir), dir, false); err != nil {
				t.Errorf("refused a package Pi would install: %v", err)
			}
		})
	}
}
