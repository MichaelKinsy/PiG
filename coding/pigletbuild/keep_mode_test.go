package pigletbuild

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	pigletartifact "github.com/MichaelKinsy/PiG/coding/piglet/artifact"
	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
	"github.com/MichaelKinsy/PiG/internal/pigstrip/leakgate"
)

// keepModePiglet keeps four built-in tools and no built-in extension: the
// pig-core shape of MINIMAL-BASE-DESIGN, which stays minimal as later cores
// add built-ins.
const keepModePiglet = "name: core\nmodel:\n  provider: test-faux\n  name: faux-1\nstrip:\n  keep:\n    tools: [read, bash, edit, write]\n    extensions: []\n"

// A keep-mode Piglet builds a signed Binary whose record states the keep
// lists and the table, which answers a print-mode prompt, whose --help lacks
// every stripped tool and extension, and which `pig piglet show` reports with
// the keep-mode lists above the expanded rows and the strip delta report.
func TestKeepModePigletBuildsASignedBinary(t *testing.T) {
	root := t.TempDir()
	pigletPath := filepath.Join(root, "core.yaml")
	if err := os.WriteFile(pigletPath, []byte(keepModePiglet), 0o644); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "author.key")
	keyID, err := signature.GenerateKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	artifact := buildNativeBinary(t, root, pigletPath, "pig-core", "--sign-key", keyPath)

	var stdout, stderr strings.Builder
	if code := piglet.RunCommand([]string{"piglet", "verify", artifact}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "Signature: signed by "+keyID) {
		t.Fatalf("pig piglet verify: exit %d\n%s%s", code, stdout.String(), stderr.String())
	}
	if output := fauxPrompt(t, artifact); output != "42" {
		t.Fatalf("keep-mode Binary -p printed %q, want 42", output)
	}
	code, help := startPigletBinary(t, artifact, "--help")
	if code != 0 {
		t.Fatalf("--help: exit %d\n%s", code, help)
	}
	for _, tool := range []string{"read", "bash", "edit", "write"} {
		if !strings.Contains(help, "  "+tool+strings.Repeat(" ", 11-len(tool))+"- ") {
			t.Errorf("--help lacks the kept tool %s:\n%s", tool, help)
		}
	}
	for _, stripped := range []struct{ list, id string }{
		{pigstrip.ListTools, "grep"}, {pigstrip.ListTools, "find"}, {pigstrip.ListTools, "ls"},
		{pigstrip.ListExtensions, "codemode"}, {pigstrip.ListExtensions, "mcp"}, {pigstrip.ListExtensions, "tool-search"},
		{pigstrip.ListExtensions, "llama.cpp"}, {pigstrip.ListExtensions, "pig-login"},
	} {
		patterns, err := leakgate.Names(stripped.list, stripped.id)
		if err != nil {
			t.Fatal(err)
		}
		if found := leakgate.Find(help, []string{"Edit files with find/replace", "~/.pig/agent/sessions/--path--/session.jsonl"}, patterns); len(found) > 0 {
			t.Errorf("--help still shows stripped %s %s:\n  %s", stripped.list, stripped.id, strings.Join(found, "\n  "))
		}
	}

	record := newestRecord(t, "core")
	if !slices.EqualFunc(record.Binary.StripKeep, []pigletartifact.StripIDs{{Kind: "extension", IDs: []string{}}, {Kind: "tool", IDs: []string{"bash", "edit", "read", "write"}}}, func(a, b pigletartifact.StripIDs) bool {
		return a.Kind == b.Kind && slices.Equal(a.IDs, b.IDs)
	}) {
		t.Fatalf("record stripKeep = %#v", record.Binary.StripKeep)
	}
	if len(record.Binary.StripTable) != len(pigstrip.Lists()) || record.Binary.PigVersion != coding.PigVersion {
		t.Fatalf("record stripTable = %#v (pigVersion %s)", record.Binary.StripTable, record.Binary.PigVersion)
	}
	for _, id := range pigstrip.Known(pigstrip.ListExtensions) {
		if !slices.Contains(record.Binary.Strip, pigletartifact.StripEntry{Kind: "extension", ID: id, Disposition: "binary"}) {
			t.Errorf("record strip lacks the expanded extension %s: %#v", id, record.Binary.Strip)
		}
	}

	stdout.Reset()
	stderr.Reset()
	if code := piglet.RunCommand([]string{"piglet", "show", pigletPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("pig piglet show: exit %d\n%s", code, stderr.String())
	}
	for _, want := range []string{
		"\nSlots:\n  tools  keep([read, bash, edit, write])\n  extensions  keep([])\n  tools.find  stripped(runtime)\n",
		"  extensions.mcp  stripped(binary)\n",
		"\nStrip table: PiG " + coding.PigVersion + " → " + coding.PigVersion + " adds no built-in IDs\n",
		"    Slots:\n      extensions  keep([])\n      tools  keep([bash, edit, read, write])\n",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("pig piglet show lacks %q:\n%s", want, stdout.String())
		}
	}
}

// newestRecord reads the Binary record PiG wrote for name.
func newestRecord(t *testing.T, name string) pigletartifact.Record {
	t.Helper()
	var found []pigletartifact.Record
	err := filepath.WalkDir(filepath.Join(codingagent.PigletRecordsDir(), name), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.Contains(path, string(pigletartifact.RecordKindBinary)) {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		record, err := pigletartifact.ParseRecord(data)
		if err != nil {
			return err
		}
		found = append(found, record)
		return nil
	})
	if err != nil || len(found) != 1 {
		t.Fatalf("Binary records for %s: %d, %v", name, len(found), err)
	}
	return found[0]
}

// writePreviousBinaryRecord stores a Binary record for name as an earlier
// core would have written it: built by PiG 0.4.0 against table, with keep.
func writePreviousBinaryRecord(t *testing.T, name string, keep, table []pigletartifact.StripIDs) {
	t.Helper()
	plan, err := pigletartifact.BuildPlan(nil)
	if err != nil {
		t.Fatal(err)
	}
	digest := func(c string) string { return "sha256:" + strings.Repeat(c, 64) }
	resolution, err := pigletartifact.NewResolutionRecord(name, "", time.Unix(1, 0), pigletartifact.ResolutionInput{
		SourceDigest: digest("a"), EffectiveDigest: digest("b"), ComponentPlan: plan,
	})
	if err != nil {
		t.Fatal(err)
	}
	binary, err := pigletartifact.NewBinaryRecord(name, "", time.Unix(2, 0), resolution, pigletartifact.BinaryInput{
		Target: "linux/amd64", PigVersion: "0.4.0", PigSourceRevision: "revision", PigSourceDigest: digest("c"),
		Builder: "native", BuilderIdentity: "native:revision",
		Artifact:     pigletartifact.Artifact{Digest: digest("d"), Size: 1, FileName: "pig-" + name},
		Verification: pigletartifact.Verification{Policy: "basic", Passed: true, Checks: []string{"artifact-sha256", "artifact-version-smoke"}},
		StripKeep:    keep, StripTable: table,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(codingagent.PigletRecordsDir(), name, "a", "unversioned", string(pigletartifact.RecordKindBinary), "linux-amd64", "previous.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Every `pig piglet build` of a Piglet with a previous Binary record prints
// the strip delta report before the build runs, even when the build then
// fails. The previous record's table lacks /trust and codemode, as a table
// from before a core added them would: codemode is left out by the keep-mode
// extensions list and /trust is now included by the deny-mode commands list.
func TestRunBuildPrintsTheStripDeltaReport(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", filepath.Join(root, "home"))
	t.Setenv("HOME", filepath.Join(root, "home"))
	pigletPath := filepath.Join(root, "core.yaml")
	if err := os.WriteFile(pigletPath, []byte(keepModePiglet), 0o644); err != nil {
		t.Fatal(err)
	}
	var table []pigletartifact.StripIDs
	for _, list := range piglet.StripTable() {
		table = append(table, pigletartifact.StripIDs{Kind: list.Kind, IDs: slices.DeleteFunc(slices.Clone(list.IDs), func(id string) bool {
			return id == "/trust" || id == "codemode"
		})})
	}
	run := func(t *testing.T, args ...string) (int, string, string) {
		t.Helper()
		var stdout, stderr strings.Builder
		code := runBuild(append([]string{pigletPath, "--format", "binary", "--builder", "no-such-builder", "--out", filepath.Join(root, "pig-core")}, args...), &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}
	if code, _, stderr := run(t); code == 0 || strings.Contains(stderr, "Strip table:") {
		t.Fatalf("a Piglet without a Binary record reports a strip delta: exit %d\n%s", code, stderr)
	}
	writePreviousBinaryRecord(t, "core", []pigletartifact.StripIDs{{Kind: "extension", IDs: []string{}}, {Kind: "tool", IDs: []string{"bash", "edit", "read", "write"}}}, table)
	want := "Strip table: PiG 0.4.0 → " + coding.PigVersion + " adds 2 built-in IDs\n" +
		"  left out (keep mode):     extensions.codemode\n" +
		"  now included (deny mode): commands./trust\n"
	code, _, stderr := run(t)
	if code == 0 || !strings.Contains(stderr, want) {
		t.Fatalf("exit %d, stderr lacks the report %q:\n%s", code, want, stderr)
	}
	if !strings.Contains(stderr, "no-such-builder") {
		t.Fatalf("the build did not run on to builder selection:\n%s", stderr)
	}
}
