package piglet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

// The fixture lineage is testdata/lineage/{base,mid,leaf}.yaml. Its expected
// digests are computed here from the fixture bytes; the expected changes are
// written out from what the fixture files declare.

func fixtureDigest(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "lineage", name+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", "lineage", name+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func goldenEqual(t *testing.T, golden, got string) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", "lineage", golden))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("%s differs\n--- got ---\n%s--- want ---\n%s", golden, got, want)
	}
}

func TestLineageOfASourceChainsRootFirstWithEachLinksChanges(t *testing.T) {
	out, err := Lineage(fixturePath(t, "leaf"), "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var names, versions, digests []string
	for _, link := range out.Chain {
		names, versions, digests = append(names, link.Name), append(versions, link.Version), append(digests, link.Digest)
	}
	if !slices.Equal(names, []string{"base", "mid", "leaf"}) || !slices.Equal(versions, []string{"1.0.0", "1.1.0", "2.0.0"}) {
		t.Fatalf("chain = %v %v", names, versions)
	}
	if want := []string{fixtureDigest(t, "base"), fixtureDigest(t, "mid"), fixtureDigest(t, "leaf")}; !slices.Equal(digests, want) {
		t.Fatalf("digests = %v, want %v", digests, want)
	}
	if out.Chain[0].Changes != nil {
		t.Fatalf("the root has no base to change: %+v", out.Chain[0].Changes)
	}
	mid, leaf := out.Chain[1].Changes, out.Chain[2].Changes
	// mid.yaml: extends.remove.skills [beta], adds skill gamma, tools [read] against [read, bash].
	if !slices.Equal(mid.Skills.Added, []string{"gamma"}) || !slices.Equal(mid.Skills.Removed, []string{"beta"}) || !slices.Equal(mid.Tools.Removed, []string{"bash"}) || len(mid.Tools.Added)+len(mid.Extensions.Added)+len(mid.Permissions.Changed) != 0 {
		t.Fatalf("mid changes = %+v", mid)
	}
	// leaf.yaml: the one rewritten skill, alpha.
	if !slices.Equal(leaf.Skills.Changed, []string{"alpha"}) || len(leaf.Skills.Added)+len(leaf.Skills.Removed)+len(leaf.Tools.Removed) != 0 {
		t.Fatalf("leaf changes = %+v", leaf)
	}
	var text bytes.Buffer
	RenderLineage(&text, out)
	goldenEqual(t, "lineage_source.golden", text.String())
}

func TestLineageJSONSchema(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := cmdLineage([]string{fixturePath(t, "leaf"), "--json", "--workspace", t.TempDir()}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &doc); err != nil {
		t.Fatal(err)
	}
	keys := func(m map[string]any) []string {
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}
	if got := keys(doc); !slices.Equal(got, []string{"advisories", "chain", "kind", "piglet"}) {
		t.Fatalf("top-level keys = %v", got)
	}
	if advisories, ok := doc["advisories"].([]any); !ok || len(advisories) != 0 {
		t.Fatalf("advisories must be an empty array, got %#v", doc["advisories"])
	}
	chain := doc["chain"].([]any)
	if got := keys(chain[0].(map[string]any)); !slices.Equal(got, []string{"digest", "name", "source", "version"}) {
		t.Fatalf("root link keys = %v", got)
	}
	link := chain[1].(map[string]any)
	if got := keys(link); !slices.Equal(got, []string{"changes", "digest", "name", "source", "version"}) {
		t.Fatalf("link keys = %v", got)
	}
	if got := keys(link["changes"].(map[string]any)); !slices.Equal(got, []string{"extensions", "packages", "permissions", "settings", "skills", "slots", "strip", "tools"}) {
		t.Fatalf("changes keys = %v", got)
	}
}

func fixtureInventory(t *testing.T) []PigletInfo {
	t.Helper()
	return []PigletInfo{
		{Name: "base", Path: fixturePath(t, "base"), Location: "user"},
		{Name: "mid", Path: fixturePath(t, "mid"), Location: "user", Records: []RecordInfo{{ReleaseVersion: "1.1.0"}}},
		{Name: "leaf", Path: fixturePath(t, "leaf"), Location: "workspace"},
		{Name: "lone", Location: "binary", Records: []RecordInfo{{ReleaseVersion: "0.3.0"}}},
	}
}

func TestTreeArrangesTheInventoryByExtends(t *testing.T) {
	roots, err := BuildTree(fixtureInventory(t), "leaf", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 || roots[0].Name != "base" || roots[1].Name != "lone" {
		t.Fatalf("roots = %+v", roots)
	}
	mid := roots[0].Children[0]
	if mid.Name != "mid" || !mid.Installed || mid.Children[0].Name != "leaf" || !mid.Children[0].Current || mid.Current {
		t.Fatalf("mid = %+v", mid)
	}
	var text bytes.Buffer
	RenderTree(&text, roots)
	goldenEqual(t, "tree.golden", text.String())
}

func TestTreeDepthLimitAndJSON(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	roots, _ := BuildTree(fixtureInventory(t), "", "")
	limitDepth(roots[0], 1)
	var text bytes.Buffer
	RenderTree(&text, roots[:1])
	goldenEqual(t, "tree_depth1.golden", text.String())
	data, err := json.Marshal(map[string]any{"piglets": roots[:1]})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Piglets []map[string]any `json:"piglets"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	node := doc.Piglets[0]
	for _, key := range []string{"name", "installed", "current", "children"} {
		if _, ok := node[key]; !ok {
			t.Fatalf("tree node lacks %q: %v", key, node)
		}
	}
	if kids := node["children"].([]any); len(kids) != 1 || kids[0].(map[string]any)["children"] == nil || len(kids[0].(map[string]any)["children"].([]any)) != 0 {
		t.Fatalf("depth 1 keeps the child but not the grandchild as an empty array: %v", node["children"])
	}
}

// signedFixtureBinary signs a stand-in Binary whose manifest records the
// digests the fixture leaf resolves to.
func signedFixtureBinary(t *testing.T, source string) string {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	key := filepath.Join(t.TempDir(), "author.key")
	if code, _, stderr := runSigningCommand(t, "keygen", key); code != 0 {
		t.Fatalf("keygen: %s", stderr)
	}
	private, err := signature.ReadPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := ResolveEffectiveWithOptions(source, ResolveOptions{Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "pig-leaf")
	if err := os.WriteFile(binary, []byte("a Piglet Binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The build signs the digest of the baked Piglet definition, not the extends graph digest.
	baked, err := Bake(resolution.Piglet, filepath.Dir(source))
	if err != nil {
		t.Fatal(err)
	}
	manifest := signature.Manifest{Piglet: "leaf", ReleaseVersion: "2.0.0", Target: "test/test", PigVersion: "test", PigletDigest: digestPigletBytes(baked), SourceDigest: resolution.SourceDigest}
	if _, err := signature.Sign(binary, manifest, private); err != nil {
		t.Fatal(err)
	}
	return binary
}

func TestVerifyRechecksTheRecordedDigestsAgainstTheSource(t *testing.T) {
	source := filepath.Join(t.TempDir(), "leaf.yaml")
	copyFixtureChain(t, filepath.Dir(source))
	binary := signedFixtureBinary(t, source)

	if code, stdout, stderr := runSigningCommand(t, "verify", binary, "--source", source); code != 0 || !strings.Contains(stdout, "Provenance: matches") {
		t.Fatalf("matching source: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	verifyJSON := func() (int, ProvenanceCheck) {
		t.Helper()
		code, stdout, _ := runSigningCommand(t, "verify", binary, "--source", source, "--json")
		var out struct {
			Verified   bool             `json:"verified"`
			Provenance *ProvenanceCheck `json:"provenance"`
		}
		if err := json.Unmarshal([]byte(stdout), &out); err != nil || out.Provenance == nil || out.Verified != (code == 0) {
			t.Fatalf("verify --json: code=%d stdout=%q err=%v", code, stdout, err)
		}
		return code, *out.Provenance
	}
	// A comment changes the source file but not the Piglet definition the build bakes.
	data, _ := os.ReadFile(source)
	if err := os.WriteFile(source, append(data, []byte("# edited after the build\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, check := verifyJSON(); code != 1 || check.Matches || len(check.Mismatches) != 1 || !strings.HasPrefix(check.Mismatches[0], "source digest:") {
		t.Fatalf("a comment-only edit must fail only the source digest: code=%d provenance=%+v", code, check)
	}
	// A content change alters both the source and the baked definition.
	if err := os.WriteFile(source, []byte(strings.Replace(string(data), "first, rewritten", "first, edited after the build", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, check := verifyJSON(); code != 1 || check.Matches || len(check.Mismatches) != 2 || !strings.HasPrefix(check.Mismatches[0], "source digest:") || !strings.HasPrefix(check.Mismatches[1], "Piglet digest:") {
		t.Fatalf("a content edit must fail both digests: code=%d provenance=%+v", code, check)
	}
	if code, _, _ := runSigningCommand(t, "verify", binary); code != 0 {
		t.Fatalf("without --source the signature alone is checked: code=%d", code)
	}
}

// A Piglet with a workspace:-anchored agentEnv.devContainer resolves only
// against a workspace that holds it, as its build did. verify --workspace and
// lineage --workspace name that workspace for the provenance check; the test's
// working directory has no devcontainer, so without one the check fails.
func TestProvenanceResolvesAgainstTheNamedWorkspace(t *testing.T) {
	source := filepath.Join(t.TempDir(), "dev.yaml")
	if err := os.WriteFile(source, []byte("name: dev\nagentEnv:\n  devContainer: workspace:.devcontainer/devcontainer.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".devcontainer", "devcontainer.json"), []byte(`{"image":"golang:1.26"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	resolution, err := ResolveEffectiveWithOptions(source, ResolveOptions{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	baked, err := Bake(resolution.Piglet, filepath.Dir(source))
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(t.TempDir(), "author.key")
	if code, _, stderr := runSigningCommand(t, "keygen", key); code != 0 {
		t.Fatalf("keygen: %s", stderr)
	}
	private, err := signature.ReadPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "pig-dev")
	if err := os.WriteFile(binary, []byte("a Piglet Binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := signature.Manifest{Piglet: "dev", ReleaseVersion: "1.0.0", Target: "test/test", PigVersion: "test", PigletDigest: digestPigletBytes(baked), SourceDigest: resolution.SourceDigest}
	if _, err := signature.Sign(binary, manifest, private); err != nil {
		t.Fatal(err)
	}

	if code, stdout, stderr := runSigningCommand(t, "verify", binary, "--source", source, "--workspace", workspace); code != 0 || !strings.Contains(stdout, "Provenance: matches") {
		t.Fatalf("verify --workspace: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, _ := runSigningCommand(t, "verify", binary, "--source", source); code != 1 || !strings.Contains(stdout, "devcontainer.json") {
		t.Fatalf("verify from a directory without the devcontainer: code=%d stdout=%q", code, stdout)
	}
	out, err := Lineage(binary, source, workspace)
	if err != nil || out.Provenance == nil || !out.Provenance.Matches {
		t.Fatalf("lineage --workspace: provenance=%+v err=%v", out.Provenance, err)
	}
}

func TestLineageOfABinaryReportsItsSignedIdentityAndChecksTheSource(t *testing.T) {
	source := filepath.Join(t.TempDir(), "leaf.yaml")
	copyFixtureChain(t, filepath.Dir(source))
	binary := signedFixtureBinary(t, source)
	out, err := Lineage(binary, source, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != "binary" || out.Binary.Name != "leaf" || out.Binary.Version != "2.0.0" || !strings.HasPrefix(out.Binary.Signer, "ed25519:") || len(out.Chain) != 3 || !out.Provenance.Matches {
		t.Fatalf("out = %+v binary=%+v", out, out.Binary)
	}
	alone, err := Lineage(binary, "", t.TempDir())
	if err != nil || len(alone.Chain) != 0 || alone.Provenance != nil {
		t.Fatalf("a Binary alone records no extends chain: %+v err=%v", alone, err)
	}
}

func copyFixtureChain(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"base", "mid", "leaf"} {
		data, err := os.ReadFile(filepath.Join("testdata", "lineage", name+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".yaml"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A Binary whose executable bytes changed after signing keeps its manifest, which still names the source's digests. Lineage must report the failed
// signature and exit 1, not present the manifest's claims as a signed identity with a matching provenance.
func TestLineageOfATamperedBinaryReportsTheFailedSignature(t *testing.T) {
	source := filepath.Join(t.TempDir(), "leaf.yaml")
	copyFixtureChain(t, filepath.Dir(source))
	binary := signedFixtureBinary(t, source)
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	data[0] ^= 0xff
	if err := os.WriteFile(binary, data, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := cmdLineage([]string{binary, "--source", source, "--workspace", t.TempDir()}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stdout.String(), "  signature: FAILED: Piglet Binary executable bytes changed after signing") {
		t.Fatalf("tampered Binary: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	out, err := Lineage(binary, "", t.TempDir())
	if err != nil || out.Binary.Verified || !strings.HasPrefix(out.Binary.Signature, "FAILED: ") {
		t.Fatalf("tampered Binary lineage = %+v err=%v", out.Binary, err)
	}
	intact := signedFixtureBinary(t, source)
	if out, err := Lineage(intact, "", t.TempDir()); err != nil || !out.Binary.Verified || !strings.HasPrefix(out.Binary.Signature, "signed by ") {
		t.Fatalf("intact Binary lineage = %+v err=%v", out.Binary, err)
	}
}
