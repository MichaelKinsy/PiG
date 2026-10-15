package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// pi: packages/coding-agent/src/core/session-manager.ts parseSessionEntryLine (skips only blank, malformed and null lines), migrateV1ToV2 (a strict-mode property
// write on a primitive throws), _buildIndex and getEntries. A session file line that parses to a number, string, array or true is an entry for
// Pi, so SessionManager.open keeps it; a record without an id opens too. Version 1 files differ only where the migration writes an id.
func TestSessionOpenKeepsRecordsThatAreNotEntriesAsPiDoes(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required: ", err)
	}
	user := func(id, parent, text string, at int) string {
		return fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,"timestamp":"2026-01-01T00:00:0%d.000Z","message":{"role":"user","content":%q,"timestamp":%d}}`, id, parent, at, text, at)
	}
	type testCase struct {
		name    string
		version int
		lines   []string
	}
	var cases []testCase
	for _, version := range []int{1, 2, 3} {
		header := fmt.Sprintf(`{"type":"session","version":%d,"id":"abc","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/w"}`, version)
		for _, bad := range []string{"42", "-0.5", "1e21", `"text"`, "[1,2]", "true", `{"type":"message"}`, `{}`} {
			cases = append(cases,
				testCase{fmt.Sprintf("v%d %s in the middle", version, bad), version, []string{header, user("a1", "null", "hi", 1), bad, user("a2", `"a1"`, "hey", 2)}},
				testCase{fmt.Sprintf("v%d %s last", version, bad), version, []string{header, user("a1", "null", "hi", 1), bad}},
				testCase{fmt.Sprintf("v%d %s first", version, bad), version, []string{header, bad, user("a1", "null", "hi", 1)}})
		}
	}
	// getBranch stops at a falsy parentId, so an empty one ends the path as a missing one does.
	for _, version := range []int{2, 3} {
		header := fmt.Sprintf(`{"type":"session","version":%d,"id":"abc","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/w"}`, version)
		// An entry whose id is empty is indexed but no path reaches it: an empty id is falsy as a start and as a parent.
		cases = append(cases, testCase{fmt.Sprintf("v%d empty parentId", version), version, []string{header, user("a1", "null", "hi", 1), user("a2", `""`, "hey", 2)}}, testCase{fmt.Sprintf("v%d empty id and empty parentId", version), version, []string{header, user("a1", "null", "hi", 1), user("", "null", "empty", 2), user("a2", `""`, "hey", 3)}})
	}
	lines := make([][]string, len(cases))
	for i, c := range cases {
		lines[i] = c.lines
	}
	in, _ := json.Marshal(lines)
	cmd := exec.CommandContext(t.Context(), "node", "testdata/session_open.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var want []struct {
		Throws  string
		Entries []string
		Leaf    string
		Branch  []string
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	canonical := func(entry SessionEntry) string {
		text, cerr := jsonstringify.Canonicalize(entry.Raw())
		if cerr != nil {
			return string(entry.Raw())
		}
		return string(text)
	}
	dir := t.TempDir()
	var differences []string
	opened := 0
	for i, c := range cases {
		if want[i].Throws == "" {
			opened++
		}
		file := filepath.Join(dir, fmt.Sprintf("%d.jsonl", i))
		if err := os.WriteFile(file, []byte(strings.Join(c.lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		session, err := NewSessionManagerWithDir("/w", dir).Open(file, "/w")
		if want[i].Throws != "" {
			// Pi's TypeError text names the engine's property write; Go need only refuse the same files.
			if err == nil {
				differences = append(differences, fmt.Sprintf("%s: Pi throws %q, Go opens", c.name, want[i].Throws))
			}
			continue
		}
		if err != nil {
			differences = append(differences, fmt.Sprintf("%s: Pi opens, Go fails: %v", c.name, err))
			continue
		}
		entries := session.GetEntries()
		branch := session.GetBranch()
		// Known residual, not asserted: the version 1 migration gives an array record an id as a property JSON.stringify never writes, so Pi's
		// branch runs through the array; the Go record is its JSON and has no place to keep the id, so the branch stops at it.
		arrayRecord := c.version == 1 && strings.Contains(strings.Join(c.lines, "\n"), "\n[1,2]")
		if len(entries) != len(want[i].Entries) || (len(branch) != len(want[i].Branch) && !arrayRecord) {
			differences = append(differences, fmt.Sprintf("%s: Go has %d entries and a branch of %d, Pi has %d and %d", c.name, len(entries), len(branch), len(want[i].Entries), len(want[i].Branch)))
			continue
		}
		if c.version == 1 {
			// The version 1 migration gives every record a random id: the shape is compared, not the ids.
			continue
		}
		var got []string
		for _, entry := range entries {
			got = append(got, canonical(entry))
		}
		var gotBranch []string
		for _, entry := range branch {
			gotBranch = append(gotBranch, canonical(entry))
		}
		leaf := "undefined"
		// A record without an id leaves the leaf as the undefined key (sessionentry.Truthy is false for it), which Pi prints as "undefined".
		if id := session.GetLeafID(); id != nil && sessionentry.Truthy(*id) {
			leaf = *id
		}
		if !slices.Equal(got, want[i].Entries) || !slices.Equal(gotBranch, want[i].Branch) || leaf != want[i].Leaf {
			differences = append(differences, fmt.Sprintf("%s\n  go: entries %q leaf %q branch %q\n  pi: entries %q leaf %q branch %q", c.name, got, leaf, gotBranch, want[i].Entries, want[i].Leaf, want[i].Branch))
		}
	}
	if opened < len(cases)/2 {
		t.Fatalf("Pi opens only %d of %d files", opened, len(cases))
	}
	if len(differences) > 0 {
		t.Fatalf("%d of %d cases differ from Pi:\n%s", len(differences), len(cases), strings.Join(differences[:min(len(differences), 12)], "\n"))
	}
}

// pi: session-manager.ts migrateToCurrentVersion finds the header with `entries.find((e) => e.type === "session")`, which reads undefined for a
// record that is not an object, so one ahead of the header is passed over.
func TestMigrateSessionEntriesPassesOverRecordsThatAreNotObjectsWhenFindingTheHeader(t *testing.T) {
	header := `{"type":"session","version":2,"id":"abc","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/w"}`
	message := `{"type":"message","id":"a1","parentId":null,"timestamp":"t","message":{"role":"hookMessage","content":"x","timestamp":1}}`
	for _, bad := range []string{"42", `"s"`, "true", "[1]"} {
		entries := []FileEntry{sessionentry.DecodeFileEntry([]byte(bad)), sessionentry.DecodeFileEntry([]byte(header)), sessionentry.DecodeFileEntry([]byte(message))}
		if err := MigrateSessionEntries(entries); err != nil {
			t.Fatalf("%s before the header: %v", bad, err)
		}
		if got := string(entries[0].Raw()); got != bad {
			t.Errorf("%s was rewritten to %s", bad, got)
		}
		if got := string(entries[1].Raw()); !strings.Contains(got, `"version":3`) {
			t.Errorf("%s before the header: the header was not migrated: %s", bad, got)
		}
		if got := string(entries[2].Raw()); !strings.Contains(got, `"role":"custom"`) {
			t.Errorf("%s before the header: the version 2 migration did not run: %s", bad, got)
		}
	}
}

// session-manager.ts migrateV1ToV2 runs `entry.id = generateId(ids); entry.parentId = prevId; prevId = entry.id` on every record after the header. On an array that
// write is an unserialized property, so the record stays as it was and the chain still goes through it: the record after it is parented to the array's id, not to
// the record before it. On a number, string or boolean the write throws a TypeError in strict mode ("Cannot create property 'id' on <type> '<value>'").
func TestMigrateV1ToV2ChainsThroughAnArrayRecordAndRefusesAPrimitiveAsPiDoes(t *testing.T) {
	header := `{"type":"session","version":1,"id":"abc","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/w"}`
	message := `{"type":"message","timestamp":"t","message":{"role":"user","content":"x","timestamp":1}}`
	entries := []FileEntry{sessionentry.DecodeFileEntry([]byte(header)), sessionentry.DecodeFileEntry([]byte(message)), sessionentry.DecodeFileEntry([]byte("[1,2]")), sessionentry.DecodeFileEntry([]byte(message))}
	if err := MigrateSessionEntries(entries); err != nil {
		t.Fatal(err)
	}
	if got := string(entries[2].Raw()); got != "[1,2]" {
		t.Fatalf("the array record was rewritten to %s", got)
	}
	var first, last struct {
		ID       string  `json:"id"`
		ParentID *string `json:"parentId"`
	}
	if err := json.Unmarshal(entries[1].Raw(), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(entries[3].Raw(), &last); err != nil {
		t.Fatal(err)
	}
	if last.ParentID == nil || *last.ParentID == "" || *last.ParentID == first.ID {
		t.Fatalf("the record after the array has parentId %v: want the id the array took, which is neither the first record's %q nor empty", last.ParentID, first.ID)
	}
	for _, c := range []struct{ line, want string }{
		{"42", "Cannot create property 'id' on number '42'"}, {`"s"`, "Cannot create property 'id' on string 's'"}, {"true", "Cannot create property 'id' on boolean 'true'"},
	} {
		err := MigrateSessionEntries([]FileEntry{sessionentry.DecodeFileEntry([]byte(header)), sessionentry.DecodeFileEntry([]byte(c.line))})
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: error %v, want %q", c.line, err, c.want)
		}
	}
}
