package sessionentry

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// session-manager.ts:183-197: SessionEntry is the union of the eleven named entry types and FileEntry adds SessionHeader. The Go members are
// MessageEntry (SessionMessageEntry) and ThinkingLevelEntry (ThinkingLevelChangeEntry) besides the nine that keep their name.
var (
	_ SessionEntry = MessageEntry{}
	_ SessionEntry = ThinkingLevelEntry{}
	_ SessionEntry = ModelChangeEntry{}
	_ SessionEntry = UsageEntry{}
	_ SessionEntry = CompactionEntry{}
	_ SessionEntry = BranchSummaryEntry{}
	_ SessionEntry = CustomEntry{}
	_ SessionEntry = CustomMessageEntry{}
	_ SessionEntry = ContextEditEntry{}
	_ SessionEntry = LabelEntry{}
	_ SessionEntry = SessionInfoEntry{}
	_ FileEntry    = SessionHeader{}
	_ FileEntry    = SessionEntry(MessageEntry{})
)

// marshalLine writes a record as session-manager.ts does, JSON.stringify: '<', '>' and '&' stay literal (Session's marshalSessionLine).
func marshalLine(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// unionRecords is one record of every member of session-manager.ts SessionEntry, written the way Pi writes it (key order of the appendX methods)
// with a member the type does not name (as a newer Pi may add one), kept in the member order it was written in.
var unionRecords = []struct {
	name   string
	raw    string
	member reflect.Type
}{
	{"message", `{"type":"message","id":"a1","parentId":null,"timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"hi","timestamp":1},"future":{"z":1,"a":2}}`, reflect.TypeFor[MessageEntry]()},
	{"thinking_level_change", `{"type":"thinking_level_change","id":"a2","parentId":"a1","timestamp":"2026-01-01T00:00:01.000Z","thinkingLevel":"high","future":1}`, reflect.TypeFor[ThinkingLevelEntry]()},
	{"model_change", `{"type":"model_change","id":"a3","parentId":"a2","timestamp":"2026-01-01T00:00:02.000Z","provider":"anthropic","modelId":"claude-opus-4-5","future":true}`, reflect.TypeFor[ModelChangeEntry]()},
	{"usage", `{"type":"usage","id":"a4","parentId":"a3","timestamp":"2026-01-01T00:00:03.000Z","kind":"cache_warm","provider":"anthropic","model":"m","usage":{"input":1,"output":2,"cacheRead":3,"cacheWrite":4,"totalTokens":10,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"note":"warmed","x-future":[1,{"b":2,"a":1}]}`, reflect.TypeFor[UsageEntry]()},
	{"compaction", `{"type":"compaction","id":"a5","parentId":"a4","timestamp":"2026-01-01T00:00:04.000Z","summary":"s","firstKeptEntryId":"a1","tokensBefore":7,"details":{"zeta":1,"alpha":2},"fromHook":true,"systemMessage":{"role":"system","content":"x"},"x-future":[1,{"b":2,"a":1}]}`, reflect.TypeFor[CompactionEntry]()},
	{"branch_summary", `{"type":"branch_summary","id":"a6","parentId":"a1","timestamp":"2026-01-01T00:00:05.000Z","fromId":"a5","summary":"bs","details":{"b":1,"a":2},"fromHook":false,"x-future":[1,{"b":2,"a":1}]}`, reflect.TypeFor[BranchSummaryEntry]()},
	{"custom", `{"type":"custom","id":"a7","parentId":"a6","timestamp":"2026-01-01T00:00:06.000Z","customType":"ext","data":{"k":[1,2,{"x":null}]},"x-future":[1,{"b":2,"a":1}]}`, reflect.TypeFor[CustomEntry]()},
	{"custom_message", `{"type":"custom_message","id":"a8","parentId":"a7","timestamp":"2026-01-01T00:00:07.000Z","customType":"ext","content":"<b>&</b>","display":true,"details":{"d":1},"x-future":[1,{"b":2,"a":1}]}`, reflect.TypeFor[CustomMessageEntry]()},
	{"context_edit", `{"type":"context_edit","id":"a9","parentId":"a8","timestamp":"2026-01-01T00:00:08.000Z","targetId":"a1","replacement":{"content":"edited"},"x-future":[1,{"b":2,"a":1}]}`, reflect.TypeFor[ContextEditEntry]()},
	{"label", `{"type":"label","id":"b1","parentId":"a9","timestamp":"2026-01-01T00:00:09.000Z","targetId":"a1","label":"mark","x-future":[1,{"b":2,"a":1}]}`, reflect.TypeFor[LabelEntry]()},
	{"session_info", `{"type":"session_info","id":"b2","parentId":"b1","timestamp":"2026-01-01T00:00:10.000Z","name":"named","x-future":[1,{"b":2,"a":1}]}`, reflect.TypeFor[SessionInfoEntry]()},
}

// Each type name decodes to its own member of the union and writes back the record it was read from: the unknown member, the member order, and
// the literal '<' and '&' survive, as the JSON object Pi parses and stringifies again (session-manager.ts _rewriteFile).
func TestDecodeSessionEntryReturnsTheNamedMemberAndKeepsTheRecord(t *testing.T) {
	seen := map[reflect.Type]bool{}
	for _, row := range unionRecords {
		t.Run(row.name, func(t *testing.T) {
			entry := DecodeSessionEntry(json.RawMessage(row.raw))
			if got := reflect.TypeOf(entry); got != row.member {
				t.Fatalf("decoded to %v, want %v", got, row.member)
			}
			seen[row.member] = true
			if base := entry.Base(); base.Type != row.name || base.ID == "" || base.Timestamp == "" {
				t.Errorf("Base() = %+v, want type %q with an id and a timestamp", base, row.name)
			}
			if string(entry.Raw()) != row.raw {
				t.Errorf("Raw() = %s\nwant    %s", entry.Raw(), row.raw)
			}
			out, err := marshalLine(entry)
			if err != nil || string(out) != row.raw {
				t.Errorf("Marshal = %s, %v\nwant      %s", out, err, row.raw)
			}
		})
	}
	if len(seen) != 11 {
		t.Fatalf("the table reaches %d members of SessionEntry, want 11", len(seen))
	}
}

// parentId is null for a root entry and a string otherwise, so Base keeps the difference a nil pointer makes.
func TestDecodeSessionEntryBaseKeepsParent(t *testing.T) {
	root := DecodeSessionEntry(json.RawMessage(unionRecords[0].raw)).Base()
	if root.ParentID != nil {
		t.Errorf("root parent = %q, want nil", *root.ParentID)
	}
	child := DecodeSessionEntry(json.RawMessage(unionRecords[1].raw)).Base()
	if child.ParentID == nil || *child.ParentID != "a1" {
		t.Errorf("child parent = %v, want a1", child.ParentID)
	}
}

// Pi casts what JSON.parse returns and checks no member (session-manager.ts parseSessionEntries), so a record that no member describes stays an
// entry that writes back as read: an unknown type, a known type whose members have the wrong JSON type, and a JSON value that is no object.
func TestDecodeSessionEntryKeepsRecordsNoMemberDescribes(t *testing.T) {
	for _, row := range []struct {
		name, raw, typ string
	}{
		{"unknown type", `{"type":"from_the_future","id":"x","parentId":null,"timestamp":"t","extra":{"b":1,"a":2}}`, "from_the_future"},
		{"known type with ill-typed member", `{"type":"compaction","id":"c","parentId":null,"timestamp":"t","summary":5}`, "compaction"},
		{"message whose message is a string", `{"type":"message","id":"m","parentId":null,"timestamp":"t","message":"x"}`, "message"},
		{"no type", `{"id":"x"}`, ""},
		{"array", `[1,2]`, ""},
		{"string", `"s"`, ""},
		{"number", `7`, ""},
		{"ill-typed id", `{"type":"message","id":5}`, ""},
	} {
		t.Run(row.name, func(t *testing.T) {
			entry := DecodeSessionEntry(json.RawMessage(row.raw))
			if _, ok := entry.(RawEntry); !ok {
				t.Fatalf("decoded to %T, want RawEntry", entry)
			}
			if entry.Base().Type != row.typ {
				t.Errorf("Base().Type = %q, want %q", entry.Base().Type, row.typ)
			}
			out, err := marshalLine(entry)
			if err != nil || string(out) != row.raw || string(entry.Raw()) != row.raw {
				t.Errorf("Marshal = %s, %v, Raw = %s, want %s", out, err, entry.Raw(), row.raw)
			}
		})
	}
}

// loadEntriesFromFile accepts a header with type "session" and a string id, whatever its version (session-manager.ts:662-666), and the header
// keeps the members it was written with. Anything else with type "session" is no header.
func TestDecodeFileEntryReturnsTheHeaderOrAnEntry(t *testing.T) {
	const header = `{"type":"session","version":3,"id":"abc","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/w","parentSession":"/p","future":[1]}`
	got, ok := DecodeFileEntry(json.RawMessage(header)).(SessionHeader)
	if !ok || got.ID != "abc" || got.Version != 3 || got.CWD != "/w" || got.ParentSession != "/p" {
		t.Fatalf("DecodeFileEntry(header) = %+v, %v", got, ok)
	}
	if out, err := marshalLine(got); err != nil || string(out) != header || string(got.Raw()) != header {
		t.Errorf("header writes %s, %v, Raw %s, want %s", out, err, got.Raw(), header)
	}
	stringVersion := `{"type":"session","version":"3","id":"abc","timestamp":"t","cwd":"/w"}`
	lenient, ok := DecodeFileEntry(json.RawMessage(stringVersion)).(SessionHeader)
	if !ok || lenient.Version != CurrentSessionVersion || string(lenient.Raw()) != stringVersion {
		t.Errorf("a header with a string version = %+v, %v, want the current version and the record it was read from", lenient, ok)
	}
	for _, raw := range []string{`{"type":"session"}`, `{"type":"session","id":5}`, `{"type":"session","id":null}`} {
		if _, isHeader := DecodeFileEntry(json.RawMessage(raw)).(SessionHeader); isHeader {
			t.Errorf("%s decoded as a header", raw)
		}
		if DecodeFileEntry(json.RawMessage(raw)).Raw() == nil {
			t.Errorf("%s lost its record", raw)
		}
	}
	entry, ok := DecodeFileEntry(json.RawMessage(unionRecords[0].raw)).(MessageEntry)
	if !ok || entry.ID != "a1" {
		t.Errorf("DecodeFileEntry(message) = %+v, %v", entry, ok)
	}
}

// A member built in memory writes its fields in declaration order with parentId null for a root, '<', '>' and '&' literal (JSON.stringify), and
// Raw is the same bytes.
func TestBuiltMembersWriteTheirFields(t *testing.T) {
	label := "<mark>&"
	for _, row := range []struct {
		entry SessionEntry
		want  string
	}{
		{LabelEntry{SessionEntryBase: SessionEntryBase{Type: "label", ID: "l", Timestamp: "t"}, TargetID: "x", Label: &label},
			`{"type":"label","id":"l","parentId":null,"timestamp":"t","targetId":"x","label":"<mark>&"}`},
		{ContextEditEntry{SessionEntryBase: SessionEntryBase{Type: "context_edit", ID: "e", Timestamp: "t"}, TargetID: "x"},
			`{"type":"context_edit","id":"e","parentId":null,"timestamp":"t","targetId":"x","replacement":null}`},
		{SessionInfoEntry{SessionEntryBase: SessionEntryBase{Type: "session_info", ID: "i", Timestamp: "t"}, Name: "n"},
			`{"type":"session_info","id":"i","parentId":null,"timestamp":"t","name":"n"}`},
	} {
		out, err := marshalLine(row.entry)
		if err != nil || string(out) != row.want || string(row.entry.Raw()) != row.want {
			t.Errorf("%T writes %s, %v, Raw %s, want %s", row.entry, out, err, row.entry.Raw(), row.want)
		}
	}
	header := SessionHeader{Type: "session", Version: 3, ID: "h", Timestamp: "t", CWD: "/w"}
	if out, err := marshalLine(header); err != nil || string(out) != `{"type":"session","version":3,"id":"h","timestamp":"t","cwd":"/w"}` {
		t.Errorf("header writes %s, %v", out, err)
	}
}

// Pi's own session fixtures (packages/coding-agent/test/fixtures) are files of version 1 and 2 records. Every line decodes to a member of
// FileEntry, the header first, and writes back its line, so a file loaded and rewritten is the file Pi holds.
func TestPiSessionFixturesRoundTrip(t *testing.T) {
	root := testenv.ModuleRoot(t)
	for name, wantTypes := range map[string]map[string]int{
		"before-compaction.jsonl": {"message": 990, "thinking_level_change": 5, "model_change": 5, "compaction": 2},
		"large-session.jsonl":     {"message": 914, "thinking_level_change": 103, "model_change": 1},
	} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, ".upstream", "current", "packages", "coding-agent", "test", "fixtures", name))
			if err != nil {
				t.Fatalf("Pi fixture unavailable (the .upstream mirror is required): %v", err)
			}
			types := map[string]int{}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			for i, line := range lines {
				entry := DecodeFileEntry(json.RawMessage(line))
				if i == 0 {
					if _, ok := entry.(SessionHeader); !ok {
						t.Fatalf("line 0 decoded to %T, want SessionHeader", entry)
					}
				} else if _, ok := entry.(SessionEntry); !ok {
					t.Fatalf("line %d decoded to %T, want a SessionEntry", i, entry)
				}
				if sessionEntry, ok := entry.(SessionEntry); ok {
					types[sessionEntry.Base().Type]++
				}
				out, err := marshalLine(entry)
				if err != nil || string(out) != line {
					t.Fatalf("line %d writes back differently: %v\n got %.200s\nwant %.200s", i, err, out, line)
				}
			}
			if !reflect.DeepEqual(types, wantTypes) {
				t.Errorf("entry types = %v, want %v", types, wantTypes)
			}
		})
	}
}

// BenchmarkDecodeFileEntryPiFixture decodes every record of Pi's large-session.jsonl, the cost a session load pays per entry beyond reading the file.
func BenchmarkDecodeFileEntryPiFixture(b *testing.B) {
	data, err := os.ReadFile(filepath.Join(testenv.ModuleRoot(b), ".upstream", "current", "packages", "coding-agent", "test", "fixtures", "large-session.jsonl"))
	if err != nil {
		b.Fatalf("Pi fixture unavailable (the .upstream mirror is required): %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		for _, line := range lines {
			DecodeFileEntry(json.RawMessage(line))
		}
	}
}

// A decoded member writes the record it was read from, so Base, the members a new entry is built from, carries no record: a member built
// from a decoded entry's Base writes its own fields, not the record of the entry it was built from.
func TestMemberBuiltFromADecodedBaseWritesItsFields(t *testing.T) {
	for _, raw := range []string{
		`{"type":"label","id":"l","parentId":null,"timestamp":"t","targetId":"x","label":"old","future":1}`,
		`{"type":"label","id":"l","parentId":null,"timestamp":"t","targetId":5}`,
	} {
		decoded := DecodeSessionEntry(json.RawMessage(raw))
		label := "new"
		built := LabelEntry{SessionEntryBase: decoded.Base(), TargetID: "y", Label: &label}
		const want = `{"type":"label","id":"l","parentId":null,"timestamp":"t","targetId":"y","label":"new"}`
		if out, err := marshalLine(built); err != nil || string(out) != want || string(built.Raw()) != want {
			t.Errorf("member built from the Base of %T writes %s, %v, Raw %s, want %s", decoded, out, err, built.Raw(), want)
		}
	}
}
