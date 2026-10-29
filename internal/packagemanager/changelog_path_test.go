package packagemanager

import (
	"os"
	"path/filepath"
	"testing"
)

// Ports the four changelogPath cases of packages/coding-agent/test/package-manager.test.ts (Pi b4b72a35, #8790): a Package's
// resources carry the manifest's changelogPath, else its CHANGELOG.md; a changelog outside the Package is ignored; a "pi" block
// that holds only metadata still auto-discovers the Package's resources.
func TestResolveExtensionSourcesExposesThePackageChangelogPath(t *testing.T) {
	const changelog = "## [1.0.0]\n\n- Initial release"
	cases := []struct {
		name  string
		files map[string]string
		want  string // relative to the package, "" for none
		ext   string
	}{
		{
			name: "manifest changelogPath",
			files: map[string]string{
				"package.json":      `{"name":"changelog-manifest-package","pi":{"extensions":["src/index.ts"],"changelogPath":"docs/CHANGELOG.md"}}`,
				"src/index.ts":      "export default function() {}",
				"docs/CHANGELOG.md": changelog,
			},
			want: "docs/CHANGELOG.md", ext: "src/index.ts",
		},
		{
			name:  "CHANGELOG.md by convention",
			files: map[string]string{"extensions/index.ts": "export default function() {}", "CHANGELOG.md": changelog},
			want:  "CHANGELOG.md", ext: "extensions/index.ts",
		},
		{
			name: "pi manifest that only declares changelogPath still auto-discovers",
			files: map[string]string{
				"package.json":        `{"name":"changelog-only-manifest-package","pi":{"changelogPath":"CHANGELOG.md"}}`,
				"extensions/index.ts": "export default function() {}",
				"CHANGELOG.md":        changelog,
			},
			want: "CHANGELOG.md", ext: "extensions/index.ts",
		},
		{
			name: "changelogPath outside the package root is ignored",
			files: map[string]string{
				"package.json": `{"name":"unsafe-changelog-package","pi":{"extensions":["src/index.ts"],"changelogPath":"../CHANGELOG.md"}}`,
				"src/index.ts": "export default function() {}",
				"CHANGELOG.md": "## [1.0.0]\n\n- Fallback should not be used",
			},
			want: "", ext: "src/index.ts",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, pm := resolveFixtureWithHome(t)
			outer := t.TempDir()
			pkg := filepath.Join(outer, "pkg")
			for name, content := range tc.files {
				writeResolveFile(t, filepath.Join(pkg, filepath.FromSlash(name)), content)
			}
			if err := os.WriteFile(filepath.Join(outer, "CHANGELOG.md"), []byte("## [1.0.0]\n\n- Outside"), 0o644); err != nil {
				t.Fatal(err)
			}
			resolved, err := pm.ResolveExtensionSources([]string{pkg}, false, true)
			if err != nil {
				t.Fatal(err)
			}
			extension := findResolved(resolved.Extensions, tc.ext)
			if extension == nil {
				t.Fatalf("%s is not resolved: %+v", tc.ext, resolved.Extensions)
			}
			want := ""
			if tc.want != "" {
				want = filepath.Join(pkg, filepath.FromSlash(tc.want))
			}
			if got := extension.Metadata.ChangelogPath; got != want {
				t.Errorf("ChangelogPath = %q, want %q", got, want)
			}
		})
	}
}
