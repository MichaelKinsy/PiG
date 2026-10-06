package piglet

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/installresolver"
)

// npmFixture is an authored Piglet directory: one local Package with a
// package.json, one extension taken from it, a README and a LICENSE.
type npmFixture struct {
	root   string
	piglet string
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newNPMFixture(t *testing.T, packageJSON, header string) npmFixture {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	root := t.TempDir()
	packageDir := filepath.Join(root, "packages", "review")
	writeFile(t, filepath.Join(packageDir, "package.json"), packageJSON)
	writeFile(t, filepath.Join(packageDir, "extensions", "runner", "index.js"), "export default function extension(pi) {}\n")
	writeFile(t, filepath.Join(root, "README.md"), "# Reviewer\n\nAuthored README.\n")
	writeFile(t, filepath.Join(root, "LICENSE"), "MIT License\n")
	path := filepath.Join(root, "reviewer.yaml")
	writeFile(t, path, header+"packages:\n  review: local:./packages/review\nextensions:\n  - name: runner\n    origins: [package:review]\n")
	installresolver.SetMaterializer(func(_, source, _ string, _, _ io.Writer) (string, error) { return source, nil })
	t.Cleanup(func() { installresolver.SetMaterializer(nil) })
	return npmFixture{root: root, piglet: path}
}

const reviewPackageJSON = `{"name":"@acme/review","version":"1.2.0","pi":{"extensions":["extensions/*"]}}`
const releasedReviewer = "# Authored comment.\nname: reviewer\ndescription: Focused code-review agent\nrelease:\n  version: 1.2.3\n"

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, data)
	}
	return out
}

func buildNPM(t *testing.T, options NPMSourceOptions) NPMSource {
	t.Helper()
	source, err := BuildNPMSource(options)
	if err != nil {
		t.Fatalf("BuildNPMSource: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(source.Dir) })
	return source
}

func TestBuildNPMSourceRewritesLocalPackagesToNPMRefs(t *testing.T) {
	f := newNPMFixture(t, reviewPackageJSON, releasedReviewer)
	original, err := os.ReadFile(f.piglet)
	if err != nil {
		t.Fatal(err)
	}

	source := buildNPM(t, NPMSourceOptions{Path: f.piglet})

	if source.Name != "reviewer" || source.Version != "1.2.3" {
		t.Fatalf("name/version = %q %q", source.Name, source.Version)
	}
	if got := source.Packages["review"]; got != "npm:@acme/review@^1.2.0" {
		t.Fatalf("rewritten Package = %q", got)
	}
	yaml, err := os.ReadFile(filepath.Join(source.Dir, "piglet.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(yaml), "review: npm:@acme/review@^1.2.0") || strings.Contains(string(yaml), "local:") {
		t.Fatalf("piglet.yaml:\n%s", yaml)
	}
	if !strings.Contains(string(yaml), "# Authored comment.") {
		t.Fatalf("authored comment was dropped:\n%s", yaml)
	}
	manifest := readJSON(t, filepath.Join(source.Dir, "package.json"))
	if manifest["name"] != "reviewer" || manifest["version"] != "1.2.3" || manifest["description"] != "Focused code-review agent" {
		t.Fatalf("package.json = %v", manifest)
	}
	if keywords, _ := manifest["keywords"].([]any); !slices.Equal(keywords, []any{"pig-piglet"}) {
		t.Fatalf("keywords = %v", manifest["keywords"])
	}
	pig, _ := manifest["pig"].(map[string]any)
	if pig["piglet"] != "piglet.yaml" || pig["binaries"] != nil {
		t.Fatalf("pig = %v", pig)
	}
	for _, name := range []string{"piglet.yaml", "package.json", "README.md", "LICENSE"} {
		if !slices.Contains(source.Files, name) {
			t.Fatalf("files = %v lacks %s", source.Files, name)
		}
	}
	if readme, _ := os.ReadFile(filepath.Join(source.Dir, "README.md")); string(readme) != "# Reviewer\n\nAuthored README.\n" {
		t.Fatalf("README = %q", readme)
	}
	if after, _ := os.ReadFile(f.piglet); !bytes.Equal(after, original) {
		t.Fatalf("authored Piglet changed:\n%s", after)
	}
	if err := CheckRemoteAddable(filepath.Join(source.Dir, "piglet.yaml")); err != nil {
		t.Fatalf("generated source is not addable: %v", err)
	}
}

func TestBuildNPMSourceLeavesPortablePackagesAlone(t *testing.T) {
	f := newNPMFixture(t, reviewPackageJSON, releasedReviewer)
	writeFile(t, f.piglet, releasedReviewer+"packages:\n  review: local:./packages/review\n  base: npm:@acme/base@^2.0.0\n  forked: git:github.com/acme/forked@v1.0.0\n")

	source := buildNPM(t, NPMSourceOptions{Path: f.piglet})

	want := map[string]string{"review": "npm:@acme/review@^1.2.0", "base": "npm:@acme/base@^2.0.0", "forked": "git:github.com/acme/forked@v1.0.0"}
	for alias, ref := range want {
		if source.Packages[alias] != ref {
			t.Fatalf("Package %s = %q, want %q", alias, source.Packages[alias], ref)
		}
	}
}

func TestBuildNPMSourceRefusesLocalPackageWithoutNPMIdentity(t *testing.T) {
	for name, packageJSON := range map[string]string{
		"no name":    `{"version":"1.0.0","pi":{"extensions":["extensions/*"]}}`,
		"no version": `{"name":"@acme/review","pi":{"extensions":["extensions/*"]}}`,
		"private":    `{"name":"@acme/review","version":"1.0.0","private":true,"pi":{"extensions":["extensions/*"]}}`,
		"bad semver": `{"name":"@acme/review","version":"one","pi":{"extensions":["extensions/*"]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newNPMFixture(t, packageJSON, releasedReviewer)
			_, err := BuildNPMSource(NPMSourceOptions{Path: f.piglet})
			if err == nil {
				t.Fatal("publication accepted a local Package that has no npm identity")
			}
			for _, want := range []string{`Package "review"`, "local:./packages/review", "publish it first", "--package-map review=npm:<name>@<range>"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q lacks %q", err, want)
				}
			}
		})
	}
}

// A local Package that publishes to its own registry (publishConfig.registry) is named with that registry, so the published Piglet installs it from there and not from npm's default registry.
func TestBuildNPMSourceCarriesAPackagesPublishRegistry(t *testing.T) {
	for _, tc := range []struct{ registry, want string }{
		{"https://npm.pkg.github.com/", "npm:@acme/review@^1.2.0?registry=https%3A%2F%2Fnpm.pkg.github.com"},
		{"https://registry.zeta-labs.example/npm/", "npm:@acme/review@^1.2.0?registry=https%3A%2F%2Fregistry.zeta-labs.example%2Fnpm"},
	} {
		f := newNPMFixture(t, `{"name":"@acme/review","version":"1.2.0","publishConfig":{"registry":"`+tc.registry+`"},"pi":{"extensions":["extensions/*"]}}`, releasedReviewer)
		source := buildNPM(t, NPMSourceOptions{Path: f.piglet})
		if got := source.Packages["review"]; got != tc.want {
			t.Fatalf("Package = %q, want %q", got, tc.want)
		}
		data, err := os.ReadFile(filepath.Join(source.Dir, "piglet.yaml"))
		if err != nil || !strings.Contains(string(data), tc.want) {
			t.Fatalf("piglet.yaml = %s, %v", data, err)
		}
		if err := CheckRemoteAddable(filepath.Join(source.Dir, "piglet.yaml")); err != nil {
			t.Fatalf("generated source is not addable: %v", err)
		}
	}
}

// A publish registry that an npm: source cannot name is refused rather than dropped.
func TestBuildNPMSourceRefusesAnUnsupportedPublishRegistry(t *testing.T) {
	for _, registry := range []string{"http://localhost:4873/", "https://user:secret@registry.example/", "ftp://registry.example/"} {
		f := newNPMFixture(t, `{"name":"@acme/review","version":"1.2.0","publishConfig":{"registry":"`+registry+`"},"pi":{"extensions":["extensions/*"]}}`, releasedReviewer)
		_, err := BuildNPMSource(NPMSourceOptions{Path: f.piglet})
		if err == nil || !strings.Contains(err.Error(), "publishConfig.registry") || !strings.Contains(err.Error(), "--package-map review=") {
			t.Fatalf("registry %s: error = %v", registry, err)
		}
	}
}

func TestBuildNPMSourcePackageMapSuppliesTheNPMRef(t *testing.T) {
	f := newNPMFixture(t, `{"name":"@acme/review","pi":{"extensions":["extensions/*"]}}`, releasedReviewer)
	source := buildNPM(t, NPMSourceOptions{Path: f.piglet, PackageMap: map[string]string{"review": "npm:@acme/mapped@^3.1.0"}})
	if source.Packages["review"] != "npm:@acme/mapped@^3.1.0" {
		t.Fatalf("Package = %q", source.Packages["review"])
	}
}

func TestBuildNPMSourceRejectsBadPackageMap(t *testing.T) {
	f := newNPMFixture(t, reviewPackageJSON, releasedReviewer)
	for name, tc := range map[string]struct {
		packageMap map[string]string
		want       string
	}{
		"unknown alias": {map[string]string{"other": "npm:x@^1"}, `--package-map names "other"`},
		"not npm":       {map[string]string{"review": "./packages/review"}, "must be an npm: reference"},
		"no range":      {map[string]string{"review": "npm:@acme/review"}, "needs a version range"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := BuildNPMSource(NPMSourceOptions{Path: f.piglet, PackageMap: tc.packageMap})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestBuildNPMSourceRefusesWhatAddWouldRefuse(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"local extension origin": {releasedReviewer + "extensions:\n  - name: fixture\n    origins: [local:./extensions/fixture]\n", `extension "fixture"`},
		"extends":                {releasedReviewer + "extends:\n  source: npm:@acme/base@^1.0.0\n", "extends"},
		"dev container":          {releasedReviewer + "agentEnv:\n  devContainer: .devcontainer/devcontainer.json\n", "agentEnv.devContainer"},
		"local secret file":      {releasedReviewer + "secrets:\n  - name: token\n    from:\n      file: ~/token\n", `secret "token"`},
	} {
		t.Run(name, func(t *testing.T) {
			f := newNPMFixture(t, reviewPackageJSON, releasedReviewer)
			writeFile(t, filepath.Join(f.root, "extensions", "fixture", "index.js"), "export default function extension(pi) {}\n")
			writeFile(t, f.piglet, tc.body+"packages:\n  review: local:./packages/review\n")
			_, err := BuildNPMSource(NPMSourceOptions{Path: f.piglet})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestBuildNPMSourceRequiresAPublishableVersion(t *testing.T) {
	for name, header := range map[string]string{
		"missing": "name: reviewer\n",
		"invalid": "name: reviewer\nrelease:\n  version: v1\n",
		// Go's semver accepts the shorthand 1.2, and PiG releases may use it, but npm rejects it as an invalid version.
		"short": "name: reviewer\nrelease:\n  version: \"1.2\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := newNPMFixture(t, reviewPackageJSON, header)
			_, err := BuildNPMSource(NPMSourceOptions{Path: f.piglet})
			if err == nil || !strings.Contains(err.Error(), "release.version") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestBuildNPMSourceCopiesThePromptFile(t *testing.T) {
	f := newNPMFixture(t, reviewPackageJSON, releasedReviewer+"systemPrompt:\n  file: prompts/review.md\n")
	writeFile(t, filepath.Join(f.root, "prompts", "review.md"), "Review carefully.\n")

	source := buildNPM(t, NPMSourceOptions{Path: f.piglet})

	if data, err := os.ReadFile(filepath.Join(source.Dir, "prompts", "review.md")); err != nil || string(data) != "Review carefully.\n" {
		t.Fatalf("prompt = %q, %v", data, err)
	}
	if !slices.Contains(source.Files, "prompts/review.md") {
		t.Fatalf("files = %v", source.Files)
	}
	if err := CheckRemoteAddable(filepath.Join(source.Dir, "piglet.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestBuildNPMSourceRefusesAPromptOutsideThePigletDirectory(t *testing.T) {
	f := newNPMFixture(t, reviewPackageJSON, releasedReviewer+"systemPrompt:\n  file: ../outside.md\n")
	writeFile(t, filepath.Join(filepath.Dir(f.root), "outside.md"), "secret\n")
	t.Cleanup(func() { _ = os.Remove(filepath.Join(filepath.Dir(f.root), "outside.md")) })
	if _, err := BuildNPMSource(NPMSourceOptions{Path: f.piglet}); err == nil {
		t.Fatal("publication copied a prompt from outside the Piglet directory")
	}
}

func TestBuildNPMSourceTakesMetadataFromTheAuthorsPackageJSON(t *testing.T) {
	f := newNPMFixture(t, reviewPackageJSON, releasedReviewer)
	writeFile(t, filepath.Join(f.root, "package.json"), `{
  "name": "@acme/reviewer-piglet",
  "version": "9.9.9",
  "description": "ignored in favour of the Piglet's own",
  "keywords": ["review", "pig-piglet"],
  "license": "Apache-2.0",
  "author": "Acme <dev@acme.example>",
  "repository": {"type": "git", "url": "git+https://github.com/acme/reviewer.git"},
  "homepage": "https://acme.example/reviewer",
  "scripts": {"prepublishOnly": "echo never"},
  "dependencies": {"left-pad": "1.0.0"},
  "private": true
}`)

	source := buildNPM(t, NPMSourceOptions{Path: f.piglet})

	if source.Name != "@acme/reviewer-piglet" || source.Version != "1.2.3" {
		t.Fatalf("name/version = %q %q (release.version decides the version)", source.Name, source.Version)
	}
	manifest := readJSON(t, filepath.Join(source.Dir, "package.json"))
	if manifest["license"] != "Apache-2.0" || manifest["author"] != "Acme <dev@acme.example>" || manifest["homepage"] != "https://acme.example/reviewer" {
		t.Fatalf("package.json = %v", manifest)
	}
	if repository, _ := manifest["repository"].(map[string]any); repository["url"] != "git+https://github.com/acme/reviewer.git" {
		t.Fatalf("repository = %v", manifest["repository"])
	}
	if keywords, _ := manifest["keywords"].([]any); !slices.Equal(keywords, []any{"pig-piglet", "review"}) {
		t.Fatalf("keywords = %v", manifest["keywords"])
	}
	for _, field := range []string{"scripts", "dependencies", "private"} {
		if _, present := manifest[field]; present {
			t.Fatalf("package.json copied %s from the author's file: %v", field, manifest)
		}
	}
}

func TestBuildNPMSourceNameOverrideAndValidation(t *testing.T) {
	f := newNPMFixture(t, reviewPackageJSON, releasedReviewer)
	source := buildNPM(t, NPMSourceOptions{Path: f.piglet, NPMName: "@acme/reviewer"})
	if source.Name != "@acme/reviewer" {
		t.Fatalf("name = %q", source.Name)
	}
	for _, bad := range []string{"Reviewer", "@acme", "has space", "a/b", "-x"} {
		if _, err := BuildNPMSource(NPMSourceOptions{Path: f.piglet, NPMName: bad}); err == nil || !strings.Contains(err.Error(), "npm package name") {
			t.Fatalf("name %q: error = %v", bad, err)
		}
	}
}

func TestBuildNPMSourceGeneratesAREADMEWhenThereIsNone(t *testing.T) {
	f := newNPMFixture(t, reviewPackageJSON, releasedReviewer)
	if err := os.Remove(filepath.Join(f.root, "README.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.root, "LICENSE")); err != nil {
		t.Fatal(err)
	}
	source := buildNPM(t, NPMSourceOptions{Path: f.piglet})
	readme, err := os.ReadFile(filepath.Join(source.Dir, "README.md"))
	if err != nil || !strings.Contains(string(readme), "pig piglet add npm:reviewer") || !strings.Contains(string(readme), "Focused code-review agent") {
		t.Fatalf("README = %q, %v", readme, err)
	}
	if slices.Contains(source.Files, "LICENSE") || len(source.Warnings) == 0 {
		t.Fatalf("files = %v warnings = %v", source.Files, source.Warnings)
	}
}

func TestBuildNPMSourceRecordsTheSignedBinary(t *testing.T) {
	f := newNPMFixture(t, reviewPackageJSON, releasedReviewer)
	binaries := &NPMBinaries{Ref: "github:acme/reviewer/reviewer@1.2.3", Signer: "ed25519:abc"}
	source := buildNPM(t, NPMSourceOptions{Path: f.piglet, Binaries: binaries})
	pig, _ := readJSON(t, filepath.Join(source.Dir, "package.json"))["pig"].(map[string]any)
	got, _ := pig["binaries"].(map[string]any)
	if got["ref"] != binaries.Ref || got["signer"] != binaries.Signer || pig["piglet"] != "piglet.yaml" {
		t.Fatalf("pig = %v", pig)
	}
}

func TestBuildNPMSourceFailureLeavesNoStagingDirectory(t *testing.T) {
	f := newNPMFixture(t, `{"name":"@acme/review"}`, releasedReviewer)
	tmp := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, tmp)
	}
	if _, err := BuildNPMSource(NPMSourceOptions{Path: f.piglet}); err == nil {
		t.Fatal("expected an error")
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Fatalf("staging left behind: %v", entries)
	}
}
