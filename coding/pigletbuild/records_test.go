package pigletbuild

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	pigletartifact "github.com/MichaelKinsy/PiG/coding/piglet/artifact"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestAC10PigletRecords(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	banned := []string{"BuildReceipt", "readBuildReceipt", "writeBuildReceipt", "compareReceiptLock", ".receipt.json", `json:"receipt`}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, term := range banned {
			if strings.Contains(string(data), term) {
				t.Errorf("%s retains removed duplicate receipt term %q", entry.Name(), term)
			}
		}
	}
	if _, err := os.Stat("receipt.go"); !os.IsNotExist(err) {
		t.Errorf("removed duplicate receipt implementation still exists: %v", err)
	}
}

func TestHashTreeIncludesExecutableMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\ntrue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := hashTree(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if info.Mode()&0o111 == 0 {
		t.Skipf("the host file system has no executable permission bit (mode after chmod 0755: %v)", info.Mode())
	}
	second, err := hashTree(path)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("mode change did not affect lock digest: %s", first)
	}
}

func TestBuildBinaryRecordsContainsNoPigletContents(t *testing.T) {
	root := t.TempDir()
	pigletPath := filepath.Join(root, "release.yaml")
	pigletData := []byte("name: release\ndescription: TOP-SECRET-VALUE\nrelease:\n  version: 1.2.0\n")
	if err := os.WriteFile(pigletPath, pigletData, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := piglet.Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	artifactPath := writeFakePigArtifact(t, root, "pig-release")
	host := Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
	lock, err := buildNativeLock(p, nil, Options{Targets: []Target{host}, Sandbox: Sandbox{Native: host}, Version: "1.2.0", BakedSettings: []byte("name: release\n")})
	if err != nil {
		t.Fatal(err)
	}
	records, err := buildBinaryRecords(lock, p, artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := pigletartifact.ValidateBinaryLink(records.Resolution, records.Binary); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "TOP-SECRET-VALUE") || strings.Contains(string(data), string(pigletData)) {
		t.Fatalf("records leaked Piglet contents: %s", data)
	}
	if records.Binary.Binary == nil || !strings.HasPrefix(records.Binary.Binary.BuilderIdentity, "native:") || records.Binary.Binary.Artifact.FileName != filepath.Base(artifactPath) || !records.Binary.Binary.Verification.Passed {
		t.Fatalf("binary record = %#v", records.Binary)
	}
}

func TestWriteBinaryRecordsWritesOnlyManagedRecordsAndArtifact(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	artifactPath := filepath.Join(t.TempDir(), "pig-release")
	artifactData := []byte("binary")
	if err := os.WriteFile(artifactPath, artifactData, 0o755); err != nil {
		t.Fatal(err)
	}
	wrong := testBinaryRecords(t, digestBytes(artifactData), int64(len(artifactData)+1))
	if _, err := writeBinaryRecords(wrong, artifactPath); err == nil || !strings.Contains(err.Error(), "artifact does not match") {
		t.Fatalf("mismatched artifact error = %v", err)
	}
	records := testBinaryRecords(t, digestBytes(artifactData), int64(len(artifactData)))
	binaryPath, err := writeBinaryRecords(records, artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(binaryPath, codingagent.PigletRecordsDir()) || !strings.Contains(binaryPath, string(pigletartifact.RecordKindBinary)) {
		t.Fatalf("binary record path = %s", binaryPath)
	}
	binaryData, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	parsedBinary, err := pigletartifact.ParseRecord(binaryData)
	if err != nil || parsedBinary.Digest != records.Binary.Digest {
		t.Fatalf("binary record = %#v, %v", parsedBinary, err)
	}
	resolutionPath := filepath.Join(
		codingagent.PigletRecordsDir(), "release", strings.Repeat("a", 64), "1.2.0",
		string(pigletartifact.RecordKindResolution), strings.TrimPrefix(records.Resolution.Digest, "sha256:")+".json",
	)
	resolutionData, err := os.ReadFile(resolutionPath)
	if err != nil {
		t.Fatal(err)
	}
	parsedResolution, err := pigletartifact.ParseRecord(resolutionData)
	if err != nil || parsedResolution.Digest != records.Resolution.Digest {
		t.Fatalf("resolution record = %#v, %v", parsedResolution, err)
	}
	managedArtifact := filepath.Join(
		codingagent.PigletArtifactsDir(), "release", strings.Repeat("a", 64), "1.2.0", "linux-amd64",
		strings.TrimPrefix(records.Binary.Binary.Artifact.Digest, "sha256:"), "pig-release",
	)
	managedData, err := os.ReadFile(managedArtifact)
	if err != nil || string(managedData) != string(artifactData) {
		t.Fatalf("managed artifact data=%q err=%v", managedData, err)
	}
	if _, err := os.Stat(artifactPath + ".receipt.json"); !os.IsNotExist(err) {
		t.Fatalf("duplicate adjacent receipt exists: %v", err)
	}
	if _, err := writeBinaryRecords(records, artifactPath); err != nil {
		t.Fatalf("idempotent record write failed: %v", err)
	}
}

func testBinaryRecords(t *testing.T, artifactDigest string, artifactSize int64) binaryBuildRecords {
	t.Helper()
	plan, err := pigletartifact.BuildPlan(nil)
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := pigletartifact.NewResolutionRecord("release", "1.2.0", time.Unix(1, 0), pigletartifact.ResolutionInput{
		SourceDigest: "sha256:" + strings.Repeat("a", 64), EffectiveDigest: "sha256:" + strings.Repeat("b", 64), ComponentPlan: plan,
	})
	if err != nil {
		t.Fatal(err)
	}
	binary, err := pigletartifact.NewBinaryRecord("release", "1.2.0", time.Unix(2, 0), resolution, pigletartifact.BinaryInput{
		Target: "linux/amd64", PigVersion: "1", PigSourceRevision: "revision", PigSourceDigest: "sha256:" + strings.Repeat("c", 64),
		Builder: "native", BuilderIdentity: "native:revision", Toolchains: map[string]string{"go": "go version test"},
		Artifact:     pigletartifact.Artifact{Digest: artifactDigest, Size: artifactSize, FileName: "pig-release"},
		Verification: pigletartifact.Verification{Policy: "basic", Passed: true, Checks: []string{"artifact-sha256", "artifact-version-smoke"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return binaryBuildRecords{Resolution: resolution, Binary: binary}
}

func TestValidateNativeTargetsRejectsUnsupportedTarget(t *testing.T) {
	host := Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
	if err := validateNativeTargets([]Target{host}); err != nil {
		t.Fatal(err)
	}
	other := Target{OS: "linux", Arch: "amd64"}
	if other == host {
		other = Target{OS: "darwin", Arch: "arm64"}
	}
	if err := validateNativeTargets([]Target{host, other}); err == nil || !strings.Contains(err.Error(), "container or remote builder") {
		t.Fatalf("error = %v", err)
	}
}

func TestBuildInputsLocksLocalGoReplacements(t *testing.T) {
	root := t.TempDir()
	extension := filepath.Join(root, "extension")
	replacement := filepath.Join(root, "sdk")
	if err := os.MkdirAll(extension, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(replacement, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extension, "go.mod"), []byte("module example.com/ext\n\ngo 1.26\n\nreplace example.com/sdk => ../sdk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(replacement, "sdk.go"), []byte("package sdk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pigletPath := filepath.Join(root, "piglet.yaml")
	pigletYAML := "name: release\nextensions:\n  - name: ext\n    origins: [local:extension]\n"
	if err := os.WriteFile(pigletPath, []byte(pigletYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := piglet.Parse(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	cells := []subprocess.CellSpec{{Extensions: []subprocess.ExtConfig{{Name: "ext", Source: extension, ContentHash: strings.Repeat("a", 64)}}}}
	inputs, err := buildInputs(parsed, cells)
	if err != nil {
		t.Fatal(err)
	}
	var dependency *buildInput
	for i := range inputs {
		if inputs[i].Kind == "extension-dependency" {
			dependency = &inputs[i]
		}
	}
	if dependency == nil || dependency.Digest == "" || !strings.Contains(dependency.Source, filepath.ToSlash(replacement)) {
		t.Fatalf("inputs = %#v", inputs)
	}
}

func TestRecordSourcePreservesNPMRegistryAndGitSubdirectoryIdentity(t *testing.T) {
	npmSource, npmVersion, err := recordSource("npm:@acme/tools@1.2.3?registry=https%3A%2F%2Fnpm.example.com", 0, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if npmSource != "npm:@acme/tools?registry=https%3A%2F%2Fnpm.example.com" || npmVersion != "1.2.3" {
		t.Fatalf("npm record source = %q version=%q", npmSource, npmVersion)
	}
	gitSource, gitVersion, err := recordSource("git:https://github.com/acme/tools@v2#subdirectory=plugins%2Freview", 0, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if gitSource != "git:github.com/acme/tools#subdirectory=plugins/review" || gitVersion != "v2" {
		t.Fatalf("git record source = %q version=%q", gitSource, gitVersion)
	}
}

func TestRecordSourceRedactsContributedLocator(t *testing.T) {
	const secretLocator = "market/release?token=secret"
	source, _, err := recordSource("marketplace:"+secretLocator, 0, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(source, secretLocator) || strings.Contains(source, "secret") || !strings.HasPrefix(source, "marketplace:sha256:") {
		t.Fatalf("source = %q", source)
	}
}

func TestAC4ExtendsLineageIsPinnedInRecord(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "base.yaml")
	child := filepath.Join(dir, "child.yaml")
	if err := os.WriteFile(base, []byte("name: base\nrelease:\n  version: 1.4.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte("name: child\nextends:\n  source: local:./base.yaml\n  version: ^1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := loadPiglet(child)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := buildInputs(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || inputs[0].Kind != "piglet-base" || inputs[0].Source != "local:base.yaml" || inputs[0].Version != "1.4.0" || !strings.HasPrefix(inputs[0].Digest, "sha256:") {
		t.Fatalf("inputs = %#v", inputs)
	}
}

func TestHashTreeExcludesVirtualenvArtifacts(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/mod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	baseline, err := hashTree(root)
	if err != nil {
		t.Fatal(err)
	}

	// A virtualenv is a developer-local, version-control-ignored artifact that
	// is not a build input. Its interpreter is a symlink, which the lock
	// otherwise refuses, and its contents differ per machine: so hashing it
	// would both break the build and make the lock digest machine-dependent.
	venv := filepath.Join(root, ".venv", "bin")
	if err := os.MkdirAll(venv, 0o755); err != nil {
		t.Fatal(err)
	}
	interpreter := filepath.Join(t.TempDir(), "python3.12")
	if err := os.WriteFile(interpreter, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, interpreter, filepath.Join(venv, "python"))

	withVenv, err := hashTree(root)
	if err != nil {
		t.Fatalf("virtualenv broke the build lock: %v", err)
	}
	if withVenv != baseline {
		t.Fatalf("virtualenv changed the build lock digest: %s != %s", withVenv, baseline)
	}
}

func TestHashTreeIgnoresMarkdownSymlinksAndRejectsSourceSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "instructions.md")
	if err := os.WriteFile(outside, []byte("instructions"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/mod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, outside, filepath.Join(root, "AGENTS.md"))
	if _, err := hashTree(root); err != nil {
		t.Fatalf("Markdown symlink affected build lock: %v", err)
	}
	testenv.Symlink(t, outside, filepath.Join(root, "source.go"))
	if _, err := hashTree(root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("source symlink error = %v", err)
	}
}
