package pigletbuild

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	pigletrelease "github.com/MichaelKinsy/PiG/coding/piglet/release"
	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

const (
	publishTestCommit     = "0123456789abcdef0123456789abcdef01234567"
	publishTestPigVersion = "pig-publish-test"
)

// signingFakeBuilder builds a small signed stand-in Binary for each requested
// target with the signing key the publish request supplies.
type signingFakeBuilder struct {
	t          *testing.T
	name       string
	unready    map[string]bool
	pigVersion func(Target) string
	built      map[string][]byte
	buildCalls int
}

func newSigningFakeBuilder(t *testing.T) *signingFakeBuilder {
	return &signingFakeBuilder{t: t, name: "fake", built: map[string][]byte{}}
}

func (b *signingFakeBuilder) Name() string { return b.name }

func (b *signingFakeBuilder) Probe(_ context.Context, request BuilderRequest) BuilderReadiness {
	target := request.Options.Targets[0].String()
	if b.unready[target] {
		return BuilderReadiness{Builder: b.name, Code: "target-unavailable", Message: "fake builder cannot build " + target}
	}
	return BuilderReadiness{Builder: b.name, Ready: true}
}

func (b *signingFakeBuilder) Build(_ context.Context, request BuilderRequest) (BuilderResult, error) {
	b.buildCalls++
	target := request.Options.Targets[0]
	if request.Options.SignKey == nil || len(request.Options.BakedSettings) == 0 || len(request.Cells) == 0 {
		return BuilderResult{}, fmt.Errorf("fake builder got an incomplete request: %+v", request.Options)
	}
	if err := os.WriteFile(request.Output, []byte("fake Piglet Binary for "+target.String()+"\n"), 0o755); err != nil {
		return BuilderResult{}, err
	}
	pigVersion := publishTestPigVersion
	if b.pigVersion != nil {
		pigVersion = b.pigVersion(target)
	}
	sourceDigest, _, err := hashFile(request.Piglet.SourcePath())
	if err != nil {
		return BuilderResult{}, err
	}
	digest := "sha256:" + strings.Repeat("c", 64)
	if _, err := signature.Sign(request.Output, signature.Manifest{
		Piglet: request.Piglet.Name, ReleaseVersion: request.Options.Version, Target: target.String(), PigVersion: pigVersion,
		PigletDigest: digest, SourceDigest: sourceDigest, ResolutionDigest: digest, ComponentPlanDigest: digest,
	}, request.Options.SignKey); err != nil {
		return BuilderResult{}, err
	}
	data, err := os.ReadFile(request.Output)
	if err != nil {
		return BuilderResult{}, err
	}
	b.built[target.String()] = data
	return BuilderResult{Builder: b.name, Artifact: request.Output}, nil
}

func (b *signingFakeBuilder) builders() ([]BuilderBackend, error) {
	return []BuilderBackend{b}, nil
}

// writePublishPiglet writes a buildable Piglet with one local Go extension
// and returns its source path.
func writePublishPiglet(t *testing.T, header string) string {
	t.Helper()
	root := t.TempDir()
	extension := filepath.Join(root, "hello")
	if err := os.MkdirAll(extension, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(extension, "go.mod"):       "module example.com/porter/hello\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n",
		filepath.Join(extension, "extension.go"): "package hello\n\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\n\nfunc Extension() *sdk.Extension {\n\treturn sdk.New(\"hello\")\n}\n",
		filepath.Join(root, "porter.yaml"):       header + "extensions:\n  - name: hello\n    origins: [local:./hello]\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(root, "porter.yaml")
}

const releasedPorter = "name: porter\nrelease:\n  version: 1.2.3\n"

func writePublishKey(t *testing.T) (string, ed25519.PrivateKey, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "author.key")
	id, err := signature.GenerateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	key, err := signature.ReadPrivateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, key, id
}

// publishTestEnv isolates PIG_HOME, the builder configuration, and the
// temporary directory, and installs the fake gh. It returns the temporary
// directory publish stages releases in.
func publishTestEnv(t *testing.T) (fakeGH, string) {
	t.Helper()
	gh := installFakeGH(t)
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_BUILDERS_FILE", filepath.Join(t.TempDir(), "builders.json"))
	tmp := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, tmp)
	}
	return gh, tmp
}

func runPublishForTest(t *testing.T, builders func() ([]BuilderBackend, error), args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	code := runPublish(context.Background(), args, &stdout, &stderr, builders)
	return code, stdout.String(), stderr.String()
}

func ghArgs(calls []fakeGHCall) [][]string {
	args := make([][]string, len(calls))
	for i, call := range calls {
		args[i] = call.Args
	}
	return args
}

func assertNoStagedRelease(t *testing.T, tmp string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(tmp, "pig-piglet-release-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("release staging left behind: %v (err=%v)", matches, err)
	}
}

func treeFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestPublishGitHubDryRunPlansWithoutBuildingOrUploading(t *testing.T) {
	gh, tmp := publishTestEnv(t)
	source := writePublishPiglet(t, releasedPorter)
	keyPath, _, keyID := writePublishKey(t)
	builder := newSigningFakeBuilder(t)

	code, stdout, stderr := runPublishForTest(t, builder.builders, source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath, "--targets", "linux/amd64,darwin/arm64")
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	want := `Piglet GitHub release dry run
Release: porter 1.2.3
Destination: github.com/acme/porter (tag v1.2.3)
Signer: ` + keyID + `
Source: git:github.com/acme/porter@v1.2.3
Assets:
  pig-porter-darwin-arm64  darwin/arm64  build with fake
  pig-porter-linux-amd64   linux/amd64   build with fake
  SHA256SUMS
  piglet-release.json
Pull with: pig piglet pull github:acme/porter@1.2.3
Dry run only; rerun with --yes to build, sign, and upload.
`
	if stdout != want {
		t.Fatalf("dry run output:\n%s\nwant:\n%s", stdout, want)
	}
	if got, want := ghArgs(gh.calls()), [][]string{{"release", "view", "v1.2.3", "--repo", "acme/porter", "--json", "tagName"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("gh calls = %q, want %q", got, want)
	}
	if builder.buildCalls != 0 || gh.assets() != nil {
		t.Fatalf("dry run built %d Binaries and uploaded %v", builder.buildCalls, gh.assets())
	}
	if files := treeFiles(t, os.Getenv("PIG_HOME")); len(files) != 0 {
		t.Fatalf("dry run wrote managed state: %v", files)
	}
	assertNoStagedRelease(t, tmp)
}

func TestPublishGitHubUploadsSignedAssetsChecksumsAndIndex(t *testing.T) {
	gh, tmp := publishTestEnv(t)
	source := writePublishPiglet(t, releasedPorter)
	keyPath, key, keyID := writePublishKey(t)
	builder := newSigningFakeBuilder(t)

	code, stdout, stderr := runPublishForTest(t, builder.builders, source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath,
		"--targets", "linux/amd64,darwin/arm64,windows/amd64", "--commit", publishTestCommit, "--yes")
	if code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Published porter 1.2.3 to https://github.com/acme/porter/releases/tag/v1.2.3\n") {
		t.Fatalf("stdout = %s", stdout)
	}
	names := []string{"pig-porter-darwin-arm64", "pig-porter-linux-amd64", "pig-porter-windows-amd64.exe"}
	targets := map[string]string{names[0]: "darwin/arm64", names[1]: "linux/amd64", names[2]: "windows/amd64"}
	sourceRef := "git:github.com/acme/porter@" + publishTestCommit
	notes := "Signed Piglet Binary release for porter 1.2.3.\n\nInstall it with PiG:\n\n    pig piglet pull github:acme/porter@1.2.3\n\n" +
		"Signer: " + keyID + "\nTargets: darwin/arm64, linux/amd64, windows/amd64\nSource: " + sourceRef + "\n\n" +
		"Each Binary carries an Ed25519 signature trailer. piglet-release.json is the DSSE-signed release index, and SHA256SUMS lists the SHA-256 of each Binary.\n"
	create := append(append([]string{"release", "create", "v1.2.3"}, names...),
		"SHA256SUMS", "piglet-release.json", "--repo", "acme/porter", "--title", "porter v1.2.3", "--notes", notes, "--target", publishTestCommit)
	calls := gh.calls()
	if got, want := ghArgs(calls), [][]string{{"release", "view", "v1.2.3", "--repo", "acme/porter", "--json", "tagName"}, create}; !reflect.DeepEqual(got, want) {
		t.Fatalf("gh calls:\n%q\nwant:\n%q", got, want)
	}
	for _, call := range calls {
		if call.PromptDisabled != "1" {
			t.Fatalf("gh ran without GH_PROMPT_DISABLED=1: %q", call.Args)
		}
	}

	assets := gh.assets()
	var sums strings.Builder
	binaries := map[string]pigletrelease.Binary{}
	for _, name := range names {
		built := builder.built[targets[name]]
		if built == nil || !reflect.DeepEqual(assets[name], built) {
			t.Fatalf("asset %s does not match the Binary built for %s", name, targets[name])
		}
		sum := sha256.Sum256(built)
		_, _ = fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
		binaries[targets[name]] = pigletrelease.Binary{URL: name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(built))}
	}
	if got := string(assets["SHA256SUMS"]); got != sums.String() {
		t.Fatalf("SHA256SUMS:\n%s\nwant:\n%s", got, sums.String())
	}
	if len(assets) != len(names)+2 {
		t.Fatalf("uploaded assets = %v", slices.Sorted(func(yield func(string) bool) {
			for name := range assets {
				if !yield(name) {
					return
				}
			}
		}))
	}
	verified, err := pigletrelease.Verify(assets["piglet-release.json"])
	if err != nil {
		t.Fatal(err)
	}
	wantIndex := pigletrelease.Index{
		Piglet: "porter", Version: "1.2.3", PigVersion: publishTestPigVersion, SourceRef: sourceRef,
		Signer:   signature.Signer{KeyID: keyID, PublicKey: base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))},
		Binaries: binaries,
	}
	if !reflect.DeepEqual(verified.Index, wantIndex) {
		t.Fatalf("signed index:\n%#v\nwant:\n%#v", verified.Index, wantIndex)
	}
	var envelope signature.Envelope
	if err := json.Unmarshal(assets["piglet-release.json"], &envelope); err != nil || envelope.PayloadType != pigletrelease.PayloadType {
		t.Fatalf("index envelope = %+v, err=%v", envelope, err)
	}
	assertNoStagedRelease(t, tmp)

	// The uploaded release is exactly what `pig piglet pull github:` reads.
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
	server := serveReleaseAssets(t, "/acme/porter/releases/download/v1.2.3/", assets)
	result, err := pigletrelease.Pull(context.Background(), server.URL+"/acme/porter/releases/download/v1.2.3/piglet-release.json", pigletrelease.Options{Target: "windows/amd64"})
	if err != nil {
		t.Fatalf("pull published release: %v", err)
	}
	pulled, err := os.ReadFile(result.Artifact)
	if err != nil || !reflect.DeepEqual(pulled, builder.built["windows/amd64"]) || result.SignerKeyID != keyID {
		t.Fatalf("pulled %+v (err=%v)", result, err)
	}
}

func serveReleaseAssets(t *testing.T, prefix string, assets map[string][]byte) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		name, ok := strings.CutPrefix(request.URL.Path, prefix)
		data, exists := assets[name]
		if !ok || !exists {
			http.NotFound(w, request)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(server.Close)
	return server
}

// nonHostTargets returns count distinct targets that differ from the host.
func nonHostTargets(count int) []string {
	host := runtime.GOOS + "/" + runtime.GOARCH
	var targets []string
	for _, candidate := range []string{"darwin/arm64", "linux/arm64", "windows/amd64", "linux/amd64"} {
		if candidate != host && len(targets) < count {
			targets = append(targets, candidate)
		}
	}
	return targets
}

// With the stock builders, the native builder cannot build a non-host target
// and the container builder cannot sign, so publication lists each missing
// target with every builder's reason and neither builds nor calls gh.
func TestPublishGitHubMissingNonHostBuilderListsTargetsAndUploadsNothing(t *testing.T) {
	gh, tmp := publishTestEnv(t)
	config := `{"builders":[{"name":"linux-container","engine":"docker","image":"registry.example/pig-builder@sha256:` + strings.Repeat("a", 64) + `"}]}`
	if err := os.WriteFile(os.Getenv("PIG_BUILDERS_FILE"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	source := writePublishPiglet(t, releasedPorter)
	keyPath, _, _ := writePublishKey(t)
	missing := nonHostTargets(2)

	var stdout, stderr strings.Builder
	code := RunPigletPublishCommand([]string{source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath, "--targets", strings.Join(missing, ","), "--yes"}, &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	message := stderr.String()
	for _, want := range []string{
		"Piglet porter 1.2.3 has no signed Piglet Binary for 2 target(s); nothing was built or uploaded:",
		"  " + missing[0] + ": no build backend is ready:",
		"  " + missing[1] + ": no build backend is ready:",
		"builder linux-container unavailable (signing-unsupported)",
		"builder native unavailable (target-unavailable)",
		"publish them with --artifacts <dir>",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("stderr missing %q:\n%s", want, message)
		}
	}
	if calls := gh.calls(); calls != nil {
		t.Fatalf("missing targets still ran gh: %q", ghArgs(calls))
	}
	if files := treeFiles(t, os.Getenv("PIG_HOME")); len(files) != 0 {
		t.Fatalf("missing targets wrote managed state: %v", files)
	}
	assertNoStagedRelease(t, tmp)
}

func TestPublishGitHubRefusesExistingRelease(t *testing.T) {
	gh, _ := publishTestEnv(t)
	gh.setExists()
	source := writePublishPiglet(t, releasedPorter)
	keyPath, _, _ := writePublishKey(t)
	builder := newSigningFakeBuilder(t)

	code, stdout, stderr := runPublishForTest(t, builder.builders, source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath, "--yes")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "GitHub release v1.2.3 already exists in acme/porter; published Piglet releases are immutable") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if len(gh.calls()) != 1 || builder.buildCalls != 0 {
		t.Fatalf("existing release: gh calls %q, builds %d", ghArgs(gh.calls()), builder.buildCalls)
	}
}

func TestPublishGitHubCreateFailureReportsGhError(t *testing.T) {
	gh, tmp := publishTestEnv(t)
	gh.setFailCreate()
	source := writePublishPiglet(t, releasedPorter)
	keyPath, _, _ := writePublishKey(t)
	builder := newSigningFakeBuilder(t)

	code, _, stderr := runPublishForTest(t, builder.builders, source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath, "--yes")
	if code != 1 || !strings.Contains(stderr, "error: create GitHub release v1.2.3 in acme/porter: exit status 1: HTTP 422: Validation Failed") || strings.Count(stderr, "HTTP 422") != 1 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if strings.Contains(stderr, "Published") || gh.assets() != nil {
		t.Fatalf("failed create reported success or uploaded assets: %s", stderr)
	}
	assertNoStagedRelease(t, tmp)
}

func TestPublishGitHubRejectsBinariesFromDifferentPigVersions(t *testing.T) {
	gh, _ := publishTestEnv(t)
	source := writePublishPiglet(t, releasedPorter)
	keyPath, _, _ := writePublishKey(t)
	builder := newSigningFakeBuilder(t)
	builder.pigVersion = func(target Target) string { return "pig-" + target.OS }

	code, _, stderr := runPublishForTest(t, builder.builders, source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath, "--targets", "linux/amd64,darwin/arm64", "--yes")
	if code != 1 || !strings.Contains(stderr, "built by different PiG versions") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if len(gh.calls()) != 1 || gh.assets() != nil {
		t.Fatalf("mixed PiG versions reached gh release create: %q", ghArgs(gh.calls()))
	}
}

// writeSignedArtifact signs a stand-in Binary whose manifest names the given
// release identity and returns its path.
func writeSignedArtifact(t *testing.T, dir, name string, key ed25519.PrivateKey, manifest signature.Manifest) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("prebuilt Piglet Binary "+name+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("d", 64)
	manifest.PigletDigest, manifest.ResolutionDigest, manifest.ComponentPlanDigest = digest, digest, digest
	if manifest.PigVersion == "" {
		manifest.PigVersion = publishTestPigVersion
	}
	if _, err := signature.Sign(path, manifest, key); err != nil {
		t.Fatal(err)
	}
	return path
}

func releaseManifest(t *testing.T, source, target string) signature.Manifest {
	t.Helper()
	digest, _, err := hashFile(source)
	if err != nil {
		t.Fatal(err)
	}
	return signature.Manifest{Piglet: "porter", ReleaseVersion: "1.2.3", Target: target, SourceDigest: digest}
}

func unexpectedBuilders(t *testing.T) func() ([]BuilderBackend, error) {
	return func() ([]BuilderBackend, error) {
		t.Error("publish --artifacts consulted a builder")
		return nil, nil
	}
}

func TestPublishGitHubCollectsPrebuiltArtifactsByManifest(t *testing.T) {
	gh, tmp := publishTestEnv(t)
	source := writePublishPiglet(t, releasedPorter)
	keyPath, key, _ := writePublishKey(t)
	dist := t.TempDir()
	linux := writeSignedArtifact(t, dist, "piglet-linux-amd64", key, releaseManifest(t, source, "linux/amd64"))
	windows := writeSignedArtifact(t, dist, "anything.exe", key, releaseManifest(t, source, "windows/amd64"))

	code, stdout, stderr := runPublishForTest(t, unexpectedBuilders(t), source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath, "--artifacts", dist, "--yes")
	if code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"  pig-porter-linux-amd64        linux/amd64    from " + linux + "\n",
		"  pig-porter-windows-amd64.exe  windows/amd64  from " + windows + "\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	assets := gh.assets()
	for name, source := range map[string]string{"pig-porter-linux-amd64": linux, "pig-porter-windows-amd64.exe": windows} {
		data, err := os.ReadFile(source)
		if err != nil || !reflect.DeepEqual(assets[name], data) {
			t.Fatalf("asset %s does not match %s (err=%v)", name, source, err)
		}
	}
	verified, err := pigletrelease.Verify(assets["piglet-release.json"])
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(func(yield func(string) bool) {
		for target := range verified.Index.Binaries {
			if !yield(target) {
				return
			}
		}
	}); !reflect.DeepEqual(got, []string{"linux/amd64", "windows/amd64"}) {
		t.Fatalf("index targets = %v", got)
	}
	assertNoStagedRelease(t, tmp)
}

func TestPublishGitHubRejectsArtifactsOutsideThisRelease(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dist, source string, key ed25519.PrivateKey)
		args  []string
		want  string
	}{
		{
			name: "other signer",
			setup: func(t *testing.T, dist, source string, _ ed25519.PrivateKey) {
				_, other, _ := writePublishKey(t)
				writeSignedArtifact(t, dist, "a", other, releaseManifest(t, source, "linux/amd64"))
			},
			want: "; --sign-key is ",
		},
		{
			name: "other Piglet",
			setup: func(t *testing.T, dist, source string, key ed25519.PrivateKey) {
				manifest := releaseManifest(t, source, "linux/amd64")
				manifest.Piglet = "reviewer"
				writeSignedArtifact(t, dist, "a", key, manifest)
			},
			want: `names Piglet "reviewer"; this release has "porter"`,
		},
		{
			name: "other release version",
			setup: func(t *testing.T, dist, source string, key ed25519.PrivateKey) {
				manifest := releaseManifest(t, source, "linux/amd64")
				manifest.ReleaseVersion = "1.2.2"
				writeSignedArtifact(t, dist, "a", key, manifest)
			},
			want: `names release version "1.2.2"; this release has "1.2.3"`,
		},
		{
			name: "other Piglet source",
			setup: func(t *testing.T, dist, source string, key ed25519.PrivateKey) {
				manifest := releaseManifest(t, source, "linux/amd64")
				manifest.SourceDigest = "sha256:" + strings.Repeat("e", 64)
				writeSignedArtifact(t, dist, "a", key, manifest)
			},
			want: "names Piglet source digest",
		},
		{
			name: "unsigned file",
			setup: func(t *testing.T, dist, _ string, _ ed25519.PrivateKey) {
				if err := os.WriteFile(filepath.Join(dist, "notes.txt"), []byte("not a binary\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "notes.txt is not a signed Piglet Binary",
		},
		{
			name: "subdirectory",
			setup: func(t *testing.T, dist, _ string, _ ed25519.PrivateKey) {
				if err := os.Mkdir(filepath.Join(dist, "nested"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			want: "contains nested, which is not a regular file",
		},
		{
			name: "duplicate target",
			setup: func(t *testing.T, dist, source string, key ed25519.PrivateKey) {
				writeSignedArtifact(t, dist, "a", key, releaseManifest(t, source, "linux/amd64"))
				writeSignedArtifact(t, dist, "b", key, releaseManifest(t, source, "linux/amd64"))
			},
			want: "holds two Piglet Binaries for linux/amd64",
		},
		{
			name: "unrequested target",
			setup: func(t *testing.T, dist, source string, key ed25519.PrivateKey) {
				writeSignedArtifact(t, dist, "a", key, releaseManifest(t, source, "linux/amd64"))
				writeSignedArtifact(t, dist, "b", key, releaseManifest(t, source, "darwin/arm64"))
			},
			args: []string{"--targets", "linux/amd64"},
			want: "for darwin/arm64, which is not a requested target",
		},
		{
			name: "missing requested target",
			setup: func(t *testing.T, dist, source string, key ed25519.PrivateKey) {
				writeSignedArtifact(t, dist, "a", key, releaseManifest(t, source, "linux/amd64"))
			},
			args: []string{"--targets", "linux/amd64,windows/amd64"},
			want: "has no signed Piglet Binary for 1 target(s); nothing was built or uploaded:\n  windows/amd64: no signed Piglet Binary for this target in ",
		},
		{
			name:  "empty directory",
			setup: func(*testing.T, string, string, ed25519.PrivateKey) {},
			want:  "holds no Piglet Binaries",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gh, _ := publishTestEnv(t)
			source := writePublishPiglet(t, releasedPorter)
			keyPath, key, _ := writePublishKey(t)
			dist := t.TempDir()
			tc.setup(t, dist, source, key)
			args := append([]string{source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath, "--artifacts", dist, "--yes"}, tc.args...)
			code, stdout, stderr := runPublishForTest(t, unexpectedBuilders(t), args...)
			if code != 1 || stdout != "" || !strings.Contains(stderr, tc.want) {
				t.Fatalf("code=%d stdout=%s stderr=%s\nwant %q", code, stdout, stderr, tc.want)
			}
			if calls := gh.calls(); calls != nil {
				t.Fatalf("rejected artifacts still ran gh: %q", ghArgs(calls))
			}
		})
	}
}

func TestPublishGitHubUsageErrorsRunNothing(t *testing.T) {
	gh, _ := publishTestEnv(t)
	source := writePublishPiglet(t, releasedPorter)
	keyPath, _, _ := writePublishKey(t)
	base := []string{source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no Piglet", []string{"--to", "github"}, "Piglet name or path is required"},
		{"no destination", []string{source}, "--to is required"},
		{"npm destination", []string{source, "--to", "npm"}, `unsupported Piglet publish destination "npm"; use --to github`},
		{"no repository", []string{source, "--to=github", "--sign-key", keyPath}, "--to github requires --repo <owner/repo>"},
		{"bad repository", []string{source, "--to", "github", "--repo", "https://github.com/acme/porter", "--sign-key", keyPath}, "must be <owner>/<repo>"},
		{"no signing key", []string{source, "--to", "github", "--repo", "acme/porter"}, "--to github requires --sign-key"},
		{"yes and dry run", append(slices.Clone(base), "--yes", "--dry-run"), "--yes and --dry-run cannot be combined"},
		{"short commit", append(slices.Clone(base), "--commit", "abc123"), "--commit must be a full lowercase hexadecimal commit SHA"},
		{"artifacts with builder", append(slices.Clone(base), "--artifacts", "dist", "--builder", "native"), "cannot be combined with --builder"},
		{"missing value", append(slices.Clone(base), "--targets"), "--targets requires a value"},
		{"unknown option", append(slices.Clone(base), "--draft"), `unknown option "--draft"`},
		{"second Piglet", append(slices.Clone(base), "other"), `unexpected argument "other"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runPublishForTest(t, unexpectedBuilders(t), tc.args...)
			if code != 2 || stdout != "" || !strings.HasPrefix(stderr, "error: ") || !strings.Contains(stderr, tc.want) || !strings.Contains(stderr, publishUsageLine) {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
		})
	}
	if calls := gh.calls(); calls != nil {
		t.Fatalf("usage errors ran gh: %q", ghArgs(calls))
	}
	code, stdout, stderr := runPublishForTest(t, unexpectedBuilders(t), "--help")
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, publishUsageLine+"\n") || !strings.Contains(stdout, "never handles a GitHub token") {
		t.Fatalf("help: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestPublishGitHubReleaseValidationRunsNoGitHubCommand(t *testing.T) {
	cases := []struct {
		name   string
		header string
		args   []string
		env    map[string]string
		want   string
	}{
		{name: "no release version", header: "name: porter\n", want: `Piglet "porter" release.version is required for publication`},
		{name: "offline", header: releasedPorter, env: map[string]string{"PIG_OFFLINE": "1"}, want: `cannot publish Piglet "porter" to GitHub while offline`},
		{name: "Pi offline", header: releasedPorter, env: map[string]string{"PI_OFFLINE": "1"}, want: "while offline"},
		{name: "invalid target", header: releasedPorter, args: []string{"--targets", "linux"}, want: `target "linux" must be os/arch`},
		{name: "duplicate target", header: releasedPorter, args: []string{"--targets", "linux/amd64,linux/amd64"}, want: "target linux/amd64 is listed more than once"},
		{name: "credentialed source", header: releasedPorter, args: []string{"--source-ref", "git:https://user:token@github.com/acme/porter@v1.2.3"}, want: "must not include credentials"},
		{name: "local source", header: releasedPorter, args: []string{"--source-ref", "local:./porter"}, want: "must use npm or Git"},
		{name: "unreadable key", header: releasedPorter, args: []string{"--sign-key", "missing.key"}, want: "--sign-key:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gh, _ := publishTestEnv(t)
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			source := writePublishPiglet(t, tc.header)
			keyPath, _, _ := writePublishKey(t)
			builder := newSigningFakeBuilder(t)
			args := append([]string{source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath}, tc.args...)
			code, stdout, stderr := runPublishForTest(t, builder.builders, args...)
			if code != 1 || stdout != "" || !strings.Contains(stderr, tc.want) {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			if calls := gh.calls(); calls != nil || builder.buildCalls != 0 {
				t.Fatalf("invalid release ran gh %q or built %d", ghArgs(calls), builder.buildCalls)
			}
		})
	}
}

func TestPublishGitHubRecordsExplicitSourceRef(t *testing.T) {
	publishTestEnv(t)
	source := writePublishPiglet(t, releasedPorter+"build:\n  targets: [linux/amd64]\n")
	keyPath, _, _ := writePublishKey(t)
	builder := newSigningFakeBuilder(t)
	code, stdout, stderr := runPublishForTest(t, builder.builders, source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath, "--source-ref", "npm:@acme/porter@1.2.3")
	if code != 0 || !strings.Contains(stdout, "Source: npm:@acme/porter@1.2.3\n") || !strings.Contains(stdout, "  pig-porter-linux-amd64  linux/amd64  build with fake\n") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if err := piglet.ValidateRemoteSource("git:github.com/acme/porter@v1.2.3"); err != nil {
		t.Fatalf("default source ref is not a valid Git source: %v", err)
	}
}

// A real native signed build publishes through gh, installs through pull, and
// passes its own startup verification after installation.
func TestPublishGitHubRealSignedBuildRoundTripsThroughPull(t *testing.T) {
	gh, tmp := publishTestEnv(t)
	// Every module the build needs must come from the local module cache.
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	source := writePublishPiglet(t, releasedPorter)
	keyPath, _, keyID := writePublishKey(t)
	host := runtime.GOOS + "/" + runtime.GOARCH

	var stdout, stderr strings.Builder
	code := RunPigletPublishCommand([]string{source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath, "--builder", "native", "--yes"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("publish exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	assets := gh.assets()
	name := pigletrelease.AssetName("porter", host)
	if !strings.Contains(stdout.String(), "Piglet Binary signed by "+keyID) || assets[name] == nil || len(assets) != 3 {
		t.Fatalf("published assets %v from:\n%s", slices.Sorted(func(yield func(string) bool) {
			for asset := range assets {
				if !yield(asset) {
					return
				}
			}
		}), stdout.String())
	}
	assertNoStagedRelease(t, tmp)

	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
	server := serveReleaseAssets(t, "/acme/porter/releases/download/v1.2.3/", assets)
	result, err := pigletrelease.Pull(context.Background(), server.URL+"/acme/porter/releases/download/v1.2.3/piglet-release.json", pigletrelease.Options{})
	if err != nil {
		t.Fatalf("pull published release: %v", err)
	}
	if result.Target != host || result.SignerKeyID != keyID {
		t.Fatalf("pulled %+v", result)
	}
	if code, output := startPigletBinary(t, result.Artifact); code != 0 {
		t.Fatalf("pulled Piglet Binary did not start: exit %d\n%s", code, output)
	}
}

// The staged release passes the checks pull applies before gh uploads it: a
// signed Binary whose manifest pull would refuse is never uploaded.
func TestPublishGitHubRefusesAssetsPullWouldReject(t *testing.T) {
	gh, tmp := publishTestEnv(t)
	source := writePublishPiglet(t, releasedPorter)
	keyPath, key, _ := writePublishKey(t)
	dist := t.TempDir()
	path := filepath.Join(dist, "piglet-linux-amd64")
	if err := os.WriteFile(path, []byte("prebuilt Piglet Binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := releaseManifest(t, source, "linux/amd64")
	manifest.PigVersion = publishTestPigVersion
	manifest.PigletDigest, manifest.ResolutionDigest = "sha256:"+strings.Repeat("d", 64), "sha256:"+strings.Repeat("d", 64)
	manifest.ComponentPlanDigest = "not-a-digest"
	if _, err := signature.Sign(path, manifest, key); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runPublishForTest(t, unexpectedBuilders(t), source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath, "--artifacts", dist, "--yes")
	if code != 1 || !strings.Contains(stderr, "staged release asset pig-porter-linux-amd64 would fail pull verification") || !strings.Contains(stderr, `invalid component plan digest "not-a-digest"`) {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if got := ghArgs(gh.calls()); len(got) != 1 || got[0][1] != "view" || gh.assets() != nil {
		t.Fatalf("an asset pull would refuse reached gh release create: %q", got)
	}
	assertNoStagedRelease(t, tmp)
}

// A SemVer prerelease publishes as a GitHub prerelease so it never becomes the
// repository's latest release.
func TestPublishGitHubMarksSemVerPrereleases(t *testing.T) {
	gh, _ := publishTestEnv(t)
	source := writePublishPiglet(t, "name: porter\nrelease:\n  version: 2.0.0-rc.1\n")
	keyPath, _, _ := writePublishKey(t)
	builder := newSigningFakeBuilder(t)
	code, stdout, stderr := runPublishForTest(t, builder.builders, source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath, "--targets", "linux/amd64", "--yes")
	if code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	calls := ghArgs(gh.calls())
	if len(calls) != 2 || calls[1][2] != "v2.0.0-rc.1" || calls[1][len(calls[1])-1] != "--prerelease" {
		t.Fatalf("gh calls = %q", calls)
	}
	if _, ok := gh.assets()["pig-porter-linux-amd64"]; !ok {
		t.Fatalf("prerelease assets = %v", gh.assets())
	}
}

func TestPublishGitHubExplainsMissingGitHubCLI(t *testing.T) {
	publishTestEnv(t)
	source := writePublishPiglet(t, releasedPorter)
	keyPath, _, _ := writePublishKey(t)
	t.Setenv("PATH", t.TempDir())
	builder := newSigningFakeBuilder(t)
	code, stdout, stderr := runPublishForTest(t, builder.builders, source, "--to", "github", "--repo", "acme/porter", "--sign-key", keyPath)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "check GitHub release v1.2.3 in acme/porter: GitHub publication runs the GitHub CLI (gh), which is not on PATH") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}
