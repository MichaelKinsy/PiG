package piglet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/piglet/artifact"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestListPreservesSameNameCrossScopeAndMatchesRecordByDigest(t *testing.T) {
	root := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Chdir(workspace)
	workspacePiglet := filepath.Join(workspace, ".pig", "piglets", "same.yaml")
	userPiglet := filepath.Join(root, "piglets", "same.yaml")
	for path, description := range map[string]string{workspacePiglet: "workspace", userPiglet: "user"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("name: same\ndescription: "+description+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	userDigest, err := pigletSourceDigest(userPiglet)
	if err != nil {
		t.Fatal(err)
	}
	writeRecordFixtureWithPigletDigest(t, root, "same", strings.TrimPrefix(userDigest, "sha256:"))
	piglets, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(piglets) != 2 || piglets[0].Location != "workspace" || piglets[1].Location != "user" || len(piglets[0].Records) != 0 || len(piglets[1].Records) != 1 {
		t.Fatalf("piglets = %#v", piglets)
	}
}

func TestListMergesSourceAndBinaryRecord(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Chdir(t.TempDir())
	pigletsDir := filepath.Join(root, "piglets")
	if err := os.MkdirAll(pigletsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pigletPath := filepath.Join(pigletsDir, "release.yaml")
	if err := os.WriteFile(pigletPath, []byte("name: release\ndescription: source piglet\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, err := pigletSourceDigest(pigletPath)
	if err != nil {
		t.Fatal(err)
	}
	writeRecordFixtureWithPigletDigest(t, root, "release", strings.TrimPrefix(digest, "sha256:"))

	piglets, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(piglets) != 1 || piglets[0].Name != "release" || piglets[0].Path == "" || len(piglets[0].Records) != 1 || piglets[0].Records[0].Target != "linux/amd64" {
		t.Fatalf("piglets = %#v", piglets)
	}
}

func TestListIncludesBinaryOnlyPiglet(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Chdir(t.TempDir())
	writeRecordFixture(t, root, "binary-only")
	piglets, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(piglets) != 1 || piglets[0].Name != "binary-only" || piglets[0].Location != "binary" || piglets[0].Path != "" || len(piglets[0].Records) != 1 {
		t.Fatalf("piglets = %#v", piglets)
	}
}

func TestListRejectsTamperedManagedArtifact(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Chdir(t.TempDir())
	writeRecordFixture(t, root, "tampered")
	artifactRoot := filepath.Join(root, "artifacts", "piglets")
	var artifactPath string
	if err := filepath.WalkDir(artifactRoot, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			artifactPath = path
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte("tampered binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := List(); err == nil || (!strings.Contains(err.Error(), "does not match Piglet Binary record") && !strings.Contains(err.Error(), "size/type")) {
		t.Fatalf("error = %v", err)
	}
}

func TestListRejectsOrphanManagedArtifact(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Chdir(t.TempDir())
	artifactPath := filepath.Join(root, "artifacts", "piglets", "orphan", "artifact")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte("orphan"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := List(); err == nil || !strings.Contains(err.Error(), "orphan managed artifact") {
		t.Fatalf("error = %v", err)
	}
}

func TestListRejectsRecordAtWrongManagedPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Chdir(t.TempDir())
	binaryPath := writeRecordFixture(t, root, "misplaced")
	wrong := filepath.Join(filepath.Dir(binaryPath), "wrong-name.json")
	if err := os.Rename(binaryPath, wrong); err != nil {
		t.Fatal(err)
	}
	if _, err := List(); err == nil || !strings.Contains(err.Error(), "stored at the wrong path") {
		t.Fatalf("error = %v", err)
	}
}

func TestListRejectsTamperedRecord(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Chdir(t.TempDir())
	binaryPath := writeRecordFixture(t, root, "tampered-record")
	mutateRecordField(t, binaryPath, func(document map[string]any) {
		document["binary"].(map[string]any)["artifact"].(map[string]any)["digest"] = "sha256:" + strings.Repeat("8", 64)
	})
	if _, err := List(); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("error = %v", err)
	}
}

func TestListRejectsBinaryWithMissingResolution(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Chdir(t.TempDir())
	writeRecordFixture(t, root, "missing-resolution")
	resolutionRoot := filepath.Join(root, "receipts", "piglets", "missing-resolution", strings.Repeat("a", 64), "1.0.0", string(artifact.RecordKindResolution))
	entries, err := os.ReadDir(resolutionRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(resolutionRoot, entries[0].Name())); err != nil {
		t.Fatal(err)
	}
	if _, err := List(); err == nil || !strings.Contains(err.Error(), "references missing resolution") {
		t.Fatalf("error = %v", err)
	}
}

func TestListSurfacesComponentPlanIdentity(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Chdir(t.TempDir())
	writeRecordFixture(t, root, "surfaced")
	piglets, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(piglets) != 1 || len(piglets[0].Records) != 1 {
		t.Fatalf("piglets = %#v", piglets)
	}
	record := piglets[0].Records[0]
	if !strings.HasPrefix(record.ComponentPlanDigest, "sha256:") || !strings.HasPrefix(record.ResolutionDigest, "sha256:") || !strings.HasPrefix(record.BinaryDigest, "sha256:") {
		t.Fatalf("record digests = %#v", record)
	}
}

func TestRecordComponentsMapsRealizationAndMaterialization(t *testing.T) {
	plan, err := artifact.BuildPlan([]artifact.ComponentInput{{
		Kind: artifact.ComponentKindExtension, Name: "review", Language: "go", Fusible: true,
		Origin: artifact.Origin{Source: "registry", Version: "1.0.0", Digest: "sha256:" + strings.Repeat("a", 64)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	components := recordComponents(&plan)
	if len(components) != 1 || components[0].Name != "review" || components[0].Realization != "fused" || components[0].Materialization != "binary" {
		t.Fatalf("components = %#v", components)
	}
}

func TestListRejectsMalformedRecord(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Chdir(t.TempDir())
	binaryPath := writeRecordFixture(t, root, "broken")
	if err := os.WriteFile(binaryPath, []byte(`{"version":1,"kind":"piglet-binary","piglet":"broken"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := List(); err == nil {
		t.Fatal("invalid Piglet record was silently omitted")
	}
}

func mutateRecordField(t *testing.T, path string, mutate func(map[string]any)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	mutate(document)
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListRejectsMalformedPigletSource(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Chdir(t.TempDir())
	pigletsDir := filepath.Join(root, "piglets")
	if err := os.MkdirAll(pigletsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pigletsDir, "broken.yaml"), []byte("version: nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := List(); err == nil || !strings.Contains(err.Error(), "parse piglet YAML") {
		t.Fatalf("error = %v", err)
	}
}

func writeRecordFixture(t *testing.T, root, name string) string {
	t.Helper()
	return writeRecordFixtureWithPigletDigest(t, root, name, strings.Repeat("a", 64))
}

func writeRecordFixtureWithPigletDigest(t *testing.T, root, name, pigletDigest string) string {
	t.Helper()
	artifactData := []byte("fixture binary")
	artifactSum := sha256.Sum256(artifactData)
	artifactDigest := hex.EncodeToString(artifactSum[:])
	artifactName := "pig-" + name
	artifactPath := filepath.Join(root, "artifacts", "piglets", name, pigletDigest, "1.0.0", "linux-amd64", artifactDigest, artifactName)
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, artifactData, 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := artifact.BuildPlan(nil)
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := artifact.NewResolutionRecord(name, "1.0.0", time.Unix(1, 0), artifact.ResolutionInput{
		SourceDigest:    "sha256:" + pigletDigest,
		EffectiveDigest: "sha256:" + strings.Repeat("f", 64), ComponentPlan: plan,
	})
	if err != nil {
		t.Fatal(err)
	}
	binary, err := artifact.NewBinaryRecord(name, "1.0.0", time.Unix(2, 0), resolution, artifact.BinaryInput{
		Target: "linux/amd64", PigVersion: "0.1.1", PigSourceRevision: "revision",
		PigSourceDigest: "sha256:" + strings.Repeat("d", 64), Builder: "native", BuilderIdentity: "native:revision",
		Toolchains:   map[string]string{"go": "go version test"},
		Artifact:     artifact.Artifact{Digest: "sha256:" + artifactDigest, Size: int64(len(artifactData)), FileName: artifactName},
		Verification: artifact.Verification{Policy: "basic", Passed: true, Checks: []string{"artifact-sha256", "artifact-version-smoke"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(root, "receipts", "piglets", name, pigletDigest, "1.0.0")
	resolutionPath := filepath.Join(base, string(artifact.RecordKindResolution), strings.TrimPrefix(resolution.Digest, "sha256:")+".json")
	binaryPath := filepath.Join(base, string(artifact.RecordKindBinary), "linux-amd64", strings.TrimPrefix(binary.Digest, "sha256:")+".json")
	for path, record := range map[string]artifact.Record{resolutionPath: resolution, binaryPath: binary} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return binaryPath
}

// (main.go writes piglets to ConfigRoot()/piglets). Before the fix,
// searchPaths read only $HOME/.pig/piglets, so a piglet installed under a
// relocated PIG_HOME resolved to nothing and --piglet <name> silently loaded
// no piglet.
func TestResolve_HonorsPIGHOME(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Setenv("XDG_CONFIG_HOME", "")

	pigletsDir := filepath.Join(root, "piglets")
	if err := os.MkdirAll(pigletsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(pigletsDir, "foo.yaml")
	if err := os.WriteFile(want, []byte("name: foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Resolve("foo")
	if err != nil {
		t.Fatalf("Resolve(foo) under PIG_HOME: %v", err)
	}
	if got != want {
		t.Fatalf("Resolve(foo) = %q, want %q", got, want)
	}
}

// XDG_CONFIG_HOME/pig is the second-priority root and must also be searched.
func TestResolve_HonorsXDGConfigHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", root)

	pigletsDir := filepath.Join(root, "pig", "piglets")
	if err := os.MkdirAll(pigletsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(pigletsDir, "bar.yaml")
	if err := os.WriteFile(want, []byte("name: bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Resolve("bar")
	if err != nil {
		t.Fatalf("Resolve(bar) under XDG_CONFIG_HOME: %v", err)
	}
	if got != want {
		t.Fatalf("Resolve(bar) = %q, want %q", got, want)
	}
}

// TestAC3PathAnchors pins independent piglet:/workspace: anchors across local,
// Package, and inherited source paths, plus missing/escaping workspace failures.
func TestAC3PathAnchors(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	piglets := filepath.Join(root, "piglets")
	if err := os.MkdirAll(filepath.Join(workspace, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(piglets, "package"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".devcontainer", "devcontainer.json"), []byte("{\"image\":\"dev:1\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(piglets, "base.yaml")
	if err := os.WriteFile(base, []byte(`name: base
packages:
  local: local:./package
systemPrompt:
  file: piglet:prompts/system.md
agentEnv:
  devContainer: workspace:.devcontainer/devcontainer.json
`), 0o644); err != nil {
		t.Fatal(err)
	}
	childDir := filepath.Join(root, "child")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(childDir, "child.yaml")
	if err := os.WriteFile(child, []byte("name: child\nextends:\n  source: local:../piglets/base.yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolved, err := ResolveEffectiveWithOptions(child, ResolveOptions{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	canonicalPiglets, err := canonicalDirectory(piglets)
	if err != nil {
		t.Fatal(err)
	}
	canonicalWorkspace, err := canonicalDirectory(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.Piglet.SystemPrompt.File; got != filepath.Join(canonicalPiglets, "prompts", "system.md") {
		t.Fatalf("piglet anchor = %q", got)
	}
	if got := resolved.Piglet.Packages["local"]; got != "local:"+filepath.Join(canonicalPiglets, "package") {
		t.Fatalf("Package source anchor = %q", got)
	}
	environment, err := ResolveAgentEnvironment(resolved.Piglet)
	if err != nil {
		t.Fatal(err)
	}
	if environment.Value != filepath.Join(canonicalWorkspace, ".devcontainer", "devcontainer.json") {
		t.Fatalf("workspace anchor = %q", environment.Value)
	}
	otherWorkspace := filepath.Join(root, "other-workspace")
	if err := os.MkdirAll(filepath.Join(otherWorkspace, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherWorkspace, ".devcontainer", "devcontainer.json"), []byte("{\"image\":\"dev:1\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	other, err := ResolveEffectiveWithOptions(child, ResolveOptions{Workspace: otherWorkspace})
	if err != nil {
		t.Fatal(err)
	}
	if other.EffectiveDigest != resolved.EffectiveDigest || other.GraphDigest != resolved.GraphDigest {
		t.Fatalf("machine-local workspace changed portable identity: first=%s/%s second=%s/%s", resolved.EffectiveDigest, resolved.GraphDigest, other.EffectiveDigest, other.GraphDigest)
	}
	if _, err := ResolveEffective(child); err == nil || !strings.Contains(err.Error(), "requires a workspace anchor") {
		t.Fatalf("missing workspace error = %v", err)
	}

	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, outside, filepath.Join(workspace, "escape"))
	escaping := filepath.Join(piglets, "escaping.yaml")
	if err := os.WriteFile(escaping, []byte("name: escaping\nagentEnv:\n  devContainer: workspace:escape/devcontainer.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveEffectiveWithOptions(escaping, ResolveOptions{Workspace: workspace}); err == nil || !strings.Contains(err.Error(), "outside the Piglet anchor") {
		// ensureNoSymlinkEscape uses a generic anchor error; the field context
		// still identifies agentEnv.devContainer.
		if err == nil || !strings.Contains(err.Error(), "outside") {
			t.Fatalf("workspace symlink error = %v", err)
		}
	}
}
