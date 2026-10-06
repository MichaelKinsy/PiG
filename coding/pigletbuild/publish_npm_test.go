package pigletbuild

import (
	"context"
	"crypto/ed25519"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/installresolver"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	pigletrelease "github.com/MichaelKinsy/PiG/coding/piglet/release"
	"github.com/MichaelKinsy/PiG/internal/npmpublish/npmtest"
)

const reviewPackageJSON = `{"name":"@acme/review","version":"1.2.0","pi":{"extensions":["extensions/*"]}}`

// npmPublishEnv isolates PIG_HOME and the temporary directory, installs the
// fake npm, and lets local Packages materialize at their own path.
func npmPublishEnv(t *testing.T) (*npmtest.Fake, string) {
	t.Helper()
	fake := npmtest.Install(t)
	fake.Seed("@acme/review", "1.2.0")
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_BUILDERS_FILE", filepath.Join(t.TempDir(), "builders.json"))
	tmp := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, tmp)
	}
	installresolver.SetMaterializer(func(_, source, _ string, _, _ io.Writer) (string, error) { return source, nil })
	t.Cleanup(func() { installresolver.SetMaterializer(nil) })
	return fake, tmp
}

// writeNPMPiglet writes the porter Piglet with one local Package that has the given package.json.
func writeNPMPiglet(t *testing.T, packageJSON string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"pkgs/review/package.json":               packageJSON,
		"pkgs/review/extensions/runner/index.js": "export default function extension(pi) {}\n",
		"README.md":                              "# Porter\n",
		"LICENSE":                                "MIT\n",
		"porter.yaml":                            releasedPorter + "description: Hauls things\npackages:\n  review: local:./pkgs/review\nextensions:\n  - name: runner\n    origins: [package:review]\n",
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(root, "porter.yaml")
}

func runNPMPublish(t *testing.T, deps npmPublishDeps, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	code := runPublishNPM(context.Background(), args, &stdout, &stderr, deps)
	return code, stdout.String(), stderr.String()
}

func assertNoStaging(t *testing.T, tmp string) {
	t.Helper()
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

func TestPublishNPMDryRunShowsThePlanAndPublishesNothing(t *testing.T) {
	fake, tmp := npmPublishEnv(t)
	source := writeNPMPiglet(t, reviewPackageJSON)

	code, stdout, stderr := runNPMPublish(t, npmPublishDeps{}, source, "--to", "npm")

	if code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"Piglet npm publish dry run\nPiglet: porter@1.2.3 (not on npm yet)\n",
		"Package review: npm:@acme/review@^1.2.0\n",
		"Signed Binary: none recorded\n",
		"Would run: npm publish ",
		"Dry run only; rerun with --yes to publish.\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	for _, listed := range []string{"piglet.yaml", "package.json", "README.md", "LICENSE"} {
		if !strings.Contains(stdout+stderr, "npm notice "+listed) {
			t.Errorf("the tarball listing lacks %s:\nstdout=%s\nstderr=%s", listed, stdout, stderr)
		}
	}
	if len(fake.Publishes()) != 0 || fake.Published("porter", "1.2.3") {
		t.Fatalf("dry run published: %v", fake.Publishes())
	}
	if files := treeFiles(t, os.Getenv("PIG_HOME")); len(files) != 0 {
		t.Fatalf("dry run wrote managed state: %v", files)
	}
	assertNoStaging(t, tmp)
}

func TestPublishNPMYesPublishesTheGeneratedSource(t *testing.T) {
	fake, tmp := npmPublishEnv(t)
	source := writeNPMPiglet(t, reviewPackageJSON)

	code, stdout, stderr := runNPMPublish(t, npmPublishDeps{}, source, "--to=npm", "--yes", "--access", "public", "--tag", "next", "--npm-name", "@acme/porter")

	if code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	publishes := fake.Publishes()
	if len(publishes) != 1 || !reflect.DeepEqual(publishes[0][2:], []string{"--tag", "next", "--access", "public", "--ignore-scripts"}) {
		t.Fatalf("publish calls = %v", publishes)
	}
	for _, want := range []string{"Published @acme/porter@1.2.3 to npm.", "Add it with: pig piglet add npm:@acme/porter", "pig-piglet"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	manifest := fake.Manifest("@acme/porter", "1.2.3")
	if manifest["name"] != "@acme/porter" || manifest["version"] != "1.2.3" || manifest["description"] != "Hauls things" {
		t.Fatalf("manifest = %v", manifest)
	}
	if !reflect.DeepEqual(manifest["keywords"], []any{"pig-piglet"}) {
		t.Fatalf("keywords = %v", manifest["keywords"])
	}
	published := filepath.Join(fake.PackageDir("@acme/porter", "1.2.3"), "piglet.yaml")
	data, err := os.ReadFile(published)
	if err != nil || !strings.Contains(string(data), "review: npm:@acme/review@^1.2.0") {
		t.Fatalf("published piglet.yaml = %q, %v", data, err)
	}
	if err := piglet.CheckRemoteAddable(published); err != nil {
		t.Fatalf("published source is not addable: %v", err)
	}
	for _, name := range []string{"README.md", "LICENSE"} {
		if _, err := os.Stat(filepath.Join(fake.PackageDir("@acme/porter", "1.2.3"), name)); err != nil {
			t.Errorf("published package lacks %s: %v", name, err)
		}
	}
	assertNoStaging(t, tmp)
}

func TestPublishNPMRefusesAnExistingVersionAndLeavesNoStaging(t *testing.T) {
	fake, tmp := npmPublishEnv(t)
	fake.Seed("porter", "1.2.3")
	source := writeNPMPiglet(t, reviewPackageJSON)

	code, stdout, stderr := runNPMPublish(t, npmPublishDeps{}, source, "--to", "npm", "--yes")

	if code != 1 || stdout != "" || !strings.Contains(stderr, "porter@1.2.3 is already on npm") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if len(fake.Publishes()) != 0 {
		t.Fatalf("published over an existing version: %v", fake.Publishes())
	}
	assertNoStaging(t, tmp)
}

func TestPublishNPMRefusesAPigletWhoseNPMPackageIsNotPublished(t *testing.T) {
	fake, tmp := npmPublishEnv(t)
	fake.Seed("@acme/other", "5.0.0")
	source := writeNPMPiglet(t, `{"name":"@acme/unpublished","version":"2.0.0","pi":{"extensions":["extensions/*"]}}`)

	code, _, stderr := runNPMPublish(t, npmPublishDeps{}, source, "--to", "npm", "--yes")
	if code != 1 || !strings.Contains(stderr, `Package "review" (npm:@acme/unpublished@^2.0.0) is not on npm; publish it first`) {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if len(fake.Publishes()) != 0 {
		t.Fatalf("published a Piglet that cannot be added: %v", fake.Publishes())
	}
	assertNoStaging(t, tmp)

	// A dry run still shows the plan, with the problem as a warning.
	code, stdout, stderr := runNPMPublish(t, npmPublishDeps{}, source, "--to", "npm")
	if code != 0 || !strings.Contains(stdout, `Warning: Package "review" (npm:@acme/unpublished@^2.0.0) is not on npm; publish it before the Piglet`) {
		t.Fatalf("dry run: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	// A version range that no published version satisfies is the same problem.
	mapped := []string{"--package-map", "review=npm:@acme/other@^6.0.0"}
	if code, _, stderr := runNPMPublish(t, npmPublishDeps{}, append([]string{source, "--to", "npm", "--yes"}, mapped...)...); code != 1 || !strings.Contains(stderr, "@acme/other@^6.0.0) is not on npm") {
		t.Fatalf("unsatisfied range: code=%d stderr=%s", code, stderr)
	}
}

func TestPublishNPMExplainsALocalPackageWithoutNPMIdentity(t *testing.T) {
	fake, tmp := npmPublishEnv(t)
	source := writeNPMPiglet(t, `{"pi":{"extensions":["extensions/*"]}}`)

	code, _, stderr := runNPMPublish(t, npmPublishDeps{}, source, "--to", "npm", "--yes")

	if code != 1 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	for _, want := range []string{`Package "review"`, "local:./pkgs/review", "publish it first", "--package-map review=npm:<name>@<range>"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if len(fake.Calls()) != 0 {
		t.Fatalf("npm ran for an unpublishable Piglet: %v", fake.Calls())
	}
	assertNoStaging(t, tmp)

	code, stdout, stderr := runNPMPublish(t, npmPublishDeps{}, source, "--to", "npm", "--yes", "--package-map", "review=npm:@acme/review@^1.2.0")
	if code != 0 || !strings.Contains(stdout, "Package review: npm:@acme/review@^1.2.0") {
		t.Fatalf("mapped publish: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

// githubTransport serves signed release indexes as github.com would.
func githubTransport(t *testing.T, assets map[string][]byte) *http.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		data, ok := assets[request.URL.EscapedPath()]
		if !ok {
			http.NotFound(w, request)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(server.Close)
	target := strings.TrimPrefix(server.URL, "http://")
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		clone := request.Clone(request.Context())
		clone.URL.Scheme, clone.URL.Host = "http", target
		return http.DefaultTransport.RoundTrip(clone)
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func signedIndex(t *testing.T, key ed25519.PrivateKey, release pigletrelease.GitHubRelease, name, version string) []byte {
	t.Helper()
	data, err := pigletrelease.Sign(pigletrelease.Index{
		Piglet: name, Version: version, PigVersion: publishTestPigVersion, SourceRef: "git:github.com/" + release.Repository + "@v" + version,
		GitHub: &release,
		Binaries: map[string]pigletrelease.Binary{
			"linux/amd64": {URL: "pig-" + name + "-linux-amd64", SHA256: strings.Repeat("a", 64), Size: 10},
		},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPublishNPMRecordsTheSignedBinaryReadFromTheReleaseIndex(t *testing.T) {
	fake, _ := npmPublishEnv(t)
	source := writeNPMPiglet(t, reviewPackageJSON)
	_, key, keyID := writePublishKey(t)
	namespaced := pigletrelease.GitHubRelease{Repository: "acme/porter", TagPrefix: "porter/"}
	client := githubTransport(t, map[string][]byte{
		"/acme/porter/releases/download/porter%2Fv1.2.3/piglet-release.json": signedIndex(t, key, namespaced, "porter", "1.2.3"),
	})

	code, stdout, stderr := runNPMPublish(t, npmPublishDeps{client: client}, source, "--to", "npm", "--yes", "--binaries", "github:acme/porter")

	if code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	pig, _ := fake.Manifest("porter", "1.2.3")["pig"].(map[string]any)
	want := map[string]any{"ref": "github:acme/porter/porter@1.2.3", "signer": keyID}
	if !reflect.DeepEqual(pig["binaries"], want) || pig["piglet"] != "piglet.yaml" {
		t.Fatalf("pig = %v, want binaries %v", pig, want)
	}
	if !strings.Contains(stdout, "Signed Binary: github:acme/porter/porter@1.2.3 (signer "+keyID+"; read from the signed release index)") {
		t.Fatalf("stdout:\n%s", stdout)
	}
}

func TestPublishNPMFindsAnUnprefixedBinaryRelease(t *testing.T) {
	fake, _ := npmPublishEnv(t)
	source := writeNPMPiglet(t, reviewPackageJSON)
	_, key, _ := writePublishKey(t)
	client := githubTransport(t, map[string][]byte{
		"/acme/porter/releases/download/v1.2.3/piglet-release.json": signedIndex(t, key, pigletrelease.GitHubRelease{Repository: "acme/porter"}, "porter", "1.2.3"),
	})
	if code, stdout, stderr := runNPMPublish(t, npmPublishDeps{client: client}, source, "--to", "npm", "--yes", "--binaries", "github:acme/porter"); code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	pig, _ := fake.Manifest("porter", "1.2.3")["pig"].(map[string]any)
	binaries, _ := pig["binaries"].(map[string]any)
	if binaries["ref"] != "github:acme/porter@1.2.3" {
		t.Fatalf("binaries = %v", binaries)
	}
}

func TestPublishNPMRefusesAMissingOrMismatchedBinaryRelease(t *testing.T) {
	_, key, _ := writePublishKey(t)
	other := signedIndex(t, key, pigletrelease.GitHubRelease{Repository: "acme/porter"}, "other", "1.2.3")
	for name, tc := range map[string]struct {
		assets map[string][]byte
		want   string
	}{
		"no release":    {map[string][]byte{}, "no signed release of porter 1.2.3 was found; publish it first with `pig piglet publish porter --to github`"},
		"other Piglet":  {map[string][]byte{"/acme/porter/releases/download/v1.2.3/piglet-release.json": other}, "the release is for Piglet other"},
		"damaged index": {map[string][]byte{"/acme/porter/releases/download/v1.2.3/piglet-release.json": []byte("{}")}, "Piglet release index"},
		"other version": {map[string][]byte{"/acme/porter/releases/download/v1.2.3/piglet-release.json": signedIndex(t, key, pigletrelease.GitHubRelease{Repository: "acme/porter"}, "porter", "9.9.9")}, "version 9.9.9, requested 1.2.3"},
	} {
		t.Run(name, func(t *testing.T) {
			fake, tmp := npmPublishEnv(t)
			source := writeNPMPiglet(t, reviewPackageJSON)
			client := githubTransport(t, tc.assets)
			code, _, stderr := runNPMPublish(t, npmPublishDeps{client: client}, source, "--to", "npm", "--yes", "--binaries", "github:acme/porter")
			if code != 1 || !strings.Contains(stderr, tc.want) {
				t.Fatalf("code=%d stderr=%s\nwant %q", code, stderr, tc.want)
			}
			if len(fake.Calls()) != 0 {
				t.Fatalf("npm ran after the binary release failed to verify: %v", fake.Calls())
			}
			assertNoStaging(t, tmp)
		})
	}
}

func TestPublishNPMUsesTheGitHubReleaseThisMachinePublished(t *testing.T) {
	gh, _ := publishTestEnv(t)
	fake := npmtest.Install(t)
	fake.Seed("@acme/review", "1.2.0")
	installresolver.SetMaterializer(func(_, source, _ string, _, _ io.Writer) (string, error) { return source, nil })
	t.Cleanup(func() { installresolver.SetMaterializer(nil) })
	source := writeNPMPiglet(t, reviewPackageJSON)
	keyPath, _, keyID := writePublishKey(t)
	builder := newSigningFakeBuilder(t)

	// A dry run of the GitHub publication records nothing; the real one does.
	if code, stdout, stderr := runPublishForTest(t, builder.builders, source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath, "--tag-prefix", "porter/"); code != 0 {
		t.Fatalf("github dry run: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if code, stdout, stderr := runNPMPublish(t, npmPublishDeps{}, source, "--to", "npm"); code != 0 || !strings.Contains(stdout, "Signed Binary: none recorded") {
		t.Fatalf("npm dry run before publication: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if code, stdout, stderr := runPublishForTest(t, builder.builders, source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath, "--tag-prefix", "porter/", "--yes"); code != 0 {
		t.Fatalf("github publish: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	_ = gh

	code, stdout, stderr := runNPMPublish(t, npmPublishDeps{}, source, "--to", "npm", "--yes")
	if code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	pig, _ := fake.Manifest("porter", "1.2.3")["pig"].(map[string]any)
	want := map[string]any{"ref": "github:acme/porter/porter@1.2.3", "signer": keyID}
	if !reflect.DeepEqual(pig["binaries"], want) {
		t.Fatalf("binaries = %v, want %v", pig["binaries"], want)
	}
	if !strings.Contains(stdout, "from the GitHub release you published") {
		t.Fatalf("stdout:\n%s", stdout)
	}

	// --no-binaries opts out.
	fake.Seed("unrelated", "1.0.0")
	code, stdout, _ = runNPMPublish(t, npmPublishDeps{}, source, "--to", "npm", "--no-binaries", "--npm-name", "porter-plain", "--yes")
	if code != 0 {
		t.Fatalf("no-binaries publish failed: %s", stdout)
	}
	if plain, _ := fake.Manifest("porter-plain", "1.2.3")["pig"].(map[string]any); plain["binaries"] != nil {
		t.Fatalf("--no-binaries recorded %v", plain["binaries"])
	}
}

func TestPublishNPMDamagedPublicationRecordIsReported(t *testing.T) {
	_, _ = npmPublishEnv(t)
	source := writeNPMPiglet(t, reviewPackageJSON)
	path, err := githubPublicationPath("porter", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runNPMPublish(t, npmPublishDeps{}, source, "--to", "npm")
	if code != 1 || !strings.Contains(stderr, "publication record") || !strings.Contains(stderr, "--binaries github:<owner/repo>") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
}

func TestPublishNPMOfflineFailsBeforeAnyNPMCall(t *testing.T) {
	fake, _ := npmPublishEnv(t)
	t.Setenv("PIG_OFFLINE", "1")
	source := writeNPMPiglet(t, reviewPackageJSON)
	code, _, stderr := runNPMPublish(t, npmPublishDeps{}, source, "--to", "npm")
	if code != 1 || !strings.Contains(stderr, "while offline") || len(fake.Calls()) != 0 {
		t.Fatalf("code=%d stderr=%s calls=%v", code, stderr, fake.Calls())
	}
}

func TestPublishNPMArgumentErrors(t *testing.T) {
	source := writeNPMPiglet(t, reviewPackageJSON)
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no piglet":       {[]string{"--to", "npm"}, "Piglet name or path is required"},
		"github only":     {[]string{source, "--to", "npm", "--repo", "acme/x"}, "--repo applies to --to github"},
		"yes and dry run": {[]string{source, "--to", "npm", "--yes", "--dry-run"}, "--yes and --dry-run cannot be combined"},
		"bad access":      {[]string{source, "--to", "npm", "--access", "private"}, "--access must be public or restricted"},
		"bad tag":         {[]string{source, "--to", "npm", "--tag", "1.0.0"}, "--tag"},
		"bad package map": {[]string{source, "--to", "npm", "--package-map", "review"}, "--package-map"},
		"duplicate map":   {[]string{source, "--to", "npm", "--package-map", "a=npm:x@^1", "--package-map", "a=npm:y@^1"}, `names "a" twice`},
		"bad binaries":    {[]string{source, "--to", "npm", "--binaries", "gitlab:acme/x"}, "must be github:<owner>/<repo>"},
		"both binaries":   {[]string{source, "--to", "npm", "--binaries", "github:acme/x", "--no-binaries"}, "cannot be combined"},
		"unknown option":  {[]string{source, "--to", "npm", "--bogus"}, `unknown option "--bogus"`},
		"two positionals": {[]string{source, "other", "--to", "npm"}, `unexpected argument "other"`},
		"missing value":   {[]string{source, "--to", "npm", "--npm-name"}, "--npm-name requires a value"},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			code := runPublish(context.Background(), tc.args, &stdout, &stderr, nil)
			if code != 2 || !strings.Contains(stderr.String(), tc.want) || !strings.Contains(stderr.String(), "pig piglet publish <name|path> --to npm") {
				t.Fatalf("code=%d stderr=%s\nwant %q", code, stderr.String(), tc.want)
			}
		})
	}
}

func TestPublishDestinationRoutingAndHelp(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := runPublish(context.Background(), []string{"x", "--to", "pypi"}, &stdout, &stderr, nil); code != 2 || !strings.Contains(stderr.String(), "use --to github or --to npm") {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runPublish(context.Background(), []string{"--to", "npm", "--help"}, &stdout, &stderr, nil); code != 0 || !strings.Contains(stdout.String(), "never reads or stores an npm token") || stderr.Len() != 0 {
		t.Fatalf("npm help: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if publishDestination([]string{"--to=npm"}) != "npm" || publishDestination([]string{"a", "--to", "github"}) != "github" || publishDestination([]string{"a"}) != "" {
		t.Fatal("publishDestination misreads --to")
	}
}

func TestPublishGitHubRecordsThePublicationOnlyWhenTheReleaseExists(t *testing.T) {
	gh, _ := publishTestEnv(t)
	source := writePublishPiglet(t, releasedPorter)
	keyPath, _, keyID := writePublishKey(t)
	builder := newSigningFakeBuilder(t)
	args := []string{source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath, "--yes"}

	gh.setFailCreate()
	if code, _, stderr := runPublishForTest(t, builder.builders, args...); code != 1 {
		t.Fatalf("failing create: code=%d stderr=%s", code, stderr)
	}
	if record, err := readGitHubPublication("porter", "1.2.3"); err != nil || record != nil {
		t.Fatalf("a failed release was recorded: %+v, %v", record, err)
	}

	if err := os.Remove(filepath.Join(gh.state, "fail-create")); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr := runPublishForTest(t, builder.builders, args...); code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	record, err := readGitHubPublication("porter", "1.2.3")
	want := &githubPublication{Repository: "acme/porter", Signer: keyID}
	if err != nil || !reflect.DeepEqual(record, want) {
		t.Fatalf("record = %+v, %v; want %+v", record, err, want)
	}
	if other, err := readGitHubPublication("porter", "9.9.9"); err != nil || other != nil {
		t.Fatalf("another version has a record: %+v, %v", other, err)
	}
	for _, bad := range [][2]string{{"../x", "1.0.0"}, {"porter", ".."}, {"", "1.0.0"}, {"a/b", "1.0.0"}} {
		if _, err := readGitHubPublication(bad[0], bad[1]); err == nil {
			t.Errorf("accepted the identity %q %q", bad[0], bad[1])
		}
	}
}
