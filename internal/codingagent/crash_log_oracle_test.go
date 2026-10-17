package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// crashLogCorpus holds crashes.json texts a crash log can hold besides what Pi writes: members Pi does not know, members of other types, hand edits.
func crashLogCorpus() []string {
	rec := func(extra string) string {
		return `{"timestamp":"2026-10-05T12:00:00.000Z","version":"1.0.4","kind":"fatal_error","message":"boom","stack":null,"sessionFile":null,"cwd":"/w"` + extra + `}`
	}
	recent := `"` + time.Now().UTC().Add(-time.Hour).Format("2006-01-02T15:04:05.000Z") + `"`
	old := `"2020-01-01T00:00:00.000Z"`
	return []string{
		`[]`, `{}`, `null`, `not json`, ``, `[1,"a",null,[],{}]`,
		`[` + rec(``) + `]`,
		`[` + rec(`,"extra":{"b":1,"a":[1,2,{"z":null}]},"n":1e21,"m":1.0,"neg":-0,"s":"é\u2028\u0000"`) + `]`,
		`[{"message":"m","timestamp":"t","z":1,"notified":"yes","stack":5,"cwd":null,"kind":1,"version":[1]}]`,
		`[{"timestamp":"` + `2026-10-05` + `","message":"date only"}]`,
		`[{"timestamp":` + recent + `,"message":"recent","notified":false}]`,
		`[{"timestamp":` + recent + `,"message":"recent","notified":0}]`,
		`[{"timestamp":` + recent + `,"message":"recent","notified":0.0}]`,
		`[{"timestamp":` + recent + `,"message":"recent","notified":-0}]`,
		`[{"timestamp":` + recent + `,"message":"recent","notified":null}]`,
		`[{"timestamp":` + recent + `,"message":"recent","notified":""}]`,
		`[{"timestamp":` + recent + `,"message":"recent","notified":"0"}]`,
		`[{"timestamp":` + recent + `,"message":"recent","notified":[]}]`,
		`[{"timestamp":` + recent + `,"message":"recent","notified":{}}]`,
		`[{"timestamp":` + recent + `,"message":"recent","notified":1}]`,
		`[{"timestamp":` + recent + `,"message":"recent","notified":2}]`,
		`[{"timestamp":` + recent + `,"message":"recent","notified":0.5}]`,
		`[{"timestamp":` + recent + `,"message":"announced","notified":"yes","k":1},{"timestamp":` + recent + `,"message":"announced","notified":2},{"timestamp":` + recent + `,"message":"pending"}]`,
		`[{"timestamp":` + recent + `,"message":"recent","k":2,"notified":false,"after":1}]`,
		`[{"timestamp":` + recent + `,"message":"recent","2":"b","1":"a","x":1}]`,
		`[{"timestamp":` + recent + `,"message":"one","extra":true},{"timestamp":` + old + `,"message":"two"},{"timestamp":` + recent + `,"message":"three","notified":true},{"timestamp":"Thu, 01 Jan 2099 00:00:00 GMT","message":"future"}]`,
		`[{"timestamp":` + old + `,"message":"stale"}]`,
		`[{"timestamp":"","message":""}]`,
		`[{"timestamp":1,"message":"m"},{"timestamp":"t","message":2},{"timestamp":"t"}]`,
		`[{"timestamp":` + recent + `,"message":"dup","message":"last","k":1,"k":2}]`,
		`[` + rec(``) + `,` + rec(`,"i":1`) + `,` + rec(`,"i":2`) + `,` + rec(`,"i":3`) + `,` + rec(`,"i":4`) + `,` + rec(`,"i":5`) + `]`,
	}
}

const crashLogOracleBody = `
const mod = await load("pi-coding-agent/core/crash-log.js");
const fs = await import("node:fs");
const path = await import("node:path");
const out = [];
for (const [i, text] of input.corpus.entries()) {
	const file = path.join(input.dir, "case-" + i + ".json");
	fs.writeFileSync(file, text);
	const read = mod.readCrashLog(file);
	const taken = mod.takeUnnotifiedCrash(file, input.now);
	const afterTake = fs.readFileSync(file, "utf8");
	fs.writeFileSync(file, text);
	const error = new Error("later");
	error.stack = undefined;
	const recorded = mod.recordCrash({ kind: "fatal_error", error, cwd: "/w" }, file);
	const afterRecord = fs.readFileSync(file, "utf8");
	out.push({ read, taken: taken ?? null, afterTake, afterRecord, recordedKeys: recorded ? Object.keys(recorded) : null });
}
emit(out);
`

type crashOracleResult struct {
	Read         []any    `json:"read"`
	Taken        any      `json:"taken"`
	AfterTake    string   `json:"afterTake"`
	AfterRecord  string   `json:"afterRecord"`
	RecordedKeys []string `json:"recordedKeys"`
}

// A recorded crash holds this process's time and version; every other character of the file must match Pi's.
var crashNewRecordPattern = regexp.MustCompile(`("timestamp": ")[^"]*(",\n\s*"version": ")[^"]*(",\n\s*"kind": "fatal_error",\n\s*"message": "later")`)

func jsonSemanticallyEqual(t *testing.T, got []byte, want any) bool {
	t.Helper()
	var decoded any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("undecodable JSON %q: %v", got, err)
	}
	return reflect.DeepEqual(decoded, want)
}

// TestCrashLogMatchesPi runs readCrashLog, takeUnnotifiedCrash and recordCrash of Pi 1.0.4 and of Pig over the same crashes.json texts and compares what they return and what they leave in the file.
func TestCrashLogMatchesPi(t *testing.T) {
	corpus := crashLogCorpus()
	now := time.Now()
	var want []crashOracleResult
	pioracle.Run(t, crashLogOracleBody, map[string]any{"corpus": corpus, "dir": t.TempDir(), "now": now.UnixMilli()}, &want)
	if len(want) != len(corpus) {
		t.Fatalf("oracle returned %d results for %d texts", len(want), len(corpus))
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "crashes.json")
	taken := 0
	for i, text := range corpus {
		w := want[i]
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		read := ReadCrashLog(path)
		got := make([]any, len(read))
		for j, record := range read {
			encoded, err := record.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(encoded, &got[j]); err != nil {
				t.Fatal(err)
			}
		}
		if len(got) == 0 && len(w.Read) == 0 {
			got = w.Read
		}
		if !reflect.DeepEqual(got, w.Read) {
			t.Errorf("text %d %s: ReadCrashLog = %v, Pi %v", i, text, got, w.Read)
		}
		crash, ok := TakeUnnotifiedCrash(path, now)
		if ok != (w.Taken != nil) {
			t.Errorf("text %d %s: TakeUnnotifiedCrash ok = %v, Pi %v", i, text, ok, w.Taken != nil)
		}
		if ok {
			taken++
			if encoded, _ := crash.MarshalJSON(); !jsonSemanticallyEqual(t, encoded, w.Taken) {
				t.Errorf("text %d: taken record = %s, Pi %v", i, encoded, w.Taken)
			}
		}
		afterTake, _ := os.ReadFile(path)
		if string(afterTake) != w.AfterTake {
			t.Errorf("text %d %s: file after take =\n%s\nPi\n%s", i, text, afterTake, w.AfterTake)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		RecordCrash(CrashInput{Kind: "fatal_error", Message: "later", CWD: "/w", Version: "1.0.4"}, path, now)
		afterRecord, _ := os.ReadFile(path)
		normalize := func(text string) string { return crashNewRecordPattern.ReplaceAllString(text, "${1}T${2}V${3}") }
		if normalize(string(afterRecord)) != normalize(w.AfterRecord) {
			t.Errorf("text %d %s: file after record =\n%s\nPi\n%s", i, text, afterRecord, w.AfterRecord)
		}
	}
	if taken < 5 {
		t.Fatalf("only %d corpus texts announced a crash; the corpus no longer reaches that path", taken)
	}
}

type stackExtensionCase struct {
	Path         string `json:"path"`
	ResolvedPath string `json:"resolvedPath"`
	SourceInfo   struct {
		Path    string `json:"path"`
		Source  string `json:"source"`
		Scope   string `json:"scope"`
		Origin  string `json:"origin"`
		BaseDir string `json:"baseDir,omitempty"`
	} `json:"sourceInfo"`
}

func stackExtension(path, source, origin, baseDir string) stackExtensionCase {
	var e stackExtensionCase
	e.Path, e.ResolvedPath = path, path
	e.SourceInfo.Path, e.SourceInfo.Source, e.SourceInfo.Scope, e.SourceInfo.Origin, e.SourceInfo.BaseDir = path, source, "user", origin, baseDir
	return e
}

// TestFindExtensionStackMatchesMatchesPi compares findExtensionStackMatches of Pi 1.0.4 and of Pig over stacks that differ in the places the matching is sensitive to: separators, spacing, percent escapes, case of drive letters, and what follows the path.
func TestFindExtensionStackMatchesMatchesPi(t *testing.T) {
	extensions := []stackExtensionCase{
		stackExtension("/ext/a.ts", "/ext/a.ts", "top-level", "/ext"),
		stackExtension("/ext/b/index.ts", "/ext/b/index.ts", "top-level", "/ext"),
		stackExtension("/ext/c b/index.js", "/ext/c b/index.js", "top-level", "/ext"),
		stackExtension("/pk/node_modules/p/x.ts", "npm:p", "package", "/pk/node_modules/p"),
		stackExtension("/pk/s.ts", "/pk/s.ts", "package", "/pk"),
		stackExtension("/pk/git/g.ts", "git:github.com/u/g", "package", "/pk/git"),
		stackExtension("C:\\Users\\r\\.pi\\e.ts", "C:\\Users\\r\\.pi\\e.ts", "top-level", "C:\\Users\\r\\.pi"),
		stackExtension("<inline:1>", "<inline:1>", "top-level", ""),
		stackExtension("/ext/é/i.mjs", "/ext/é/i.mjs", "top-level", "/ext"),
		stackExtension("/ext/dir/", "/ext/dir/", "top-level", "/ext"),
	}
	frames := []string{
		"    at f (file:///ext/a.ts:1:2)", "    at /ext/a.ts:1:2", "    at /ext/a.ts", "    at /ext/a.ts ", "    at /ext/a.tsx:1:2", "    at /ext/a.ts\u00a0x", "    at /ext/a.ts\u2003x", "    at /ext/a.ts\ufeffx", "    at /ext/a.ts\u0085x",
		"    at g (/ext/b/deep/w.ts:1:1)", "    at /ext/b/index.ts:1:1", "    at /ext/b:1", "    at /ext/b/", "    at /ext/c%20b/w.js:1:1", "    at /ext/c b/w.js:1:1", "    at /ext/c%2Fb/w.js", "    at %E0%A4%A", "    at /ext/%C3%A9/i.mjs:1:1", "    at /ext/é/x",
		"    at /pk/node_modules/p/src/w.ts:1:1", "    at /pk/node_modules/pp/w.ts", "    at /pk/s.ts:3:3", "    at /pk/s.tsx", "    at /pk/other.ts", "    at /pk/git/z/q.ts", "    at C:\\Users\\r\\.pi\\e.ts:1:1", "    at c:\\users\\r\\.pi\\E.TS:1:1", "    at file:///C:/Users/r/.pi/e.ts:1:1",
		"    at <inline:1>", "    at <inline:1>:2:3", "\tat /ext/a.ts:1:1", "  at\t/ext/a.ts:1:1", "at /ext/a.ts:1:1", "   at", "   at ", "   atx /ext/a.ts", "  /ext/dir/x.ts:1:1", "    at /ext/dir/x.ts",
		"\u00a0 at /ext/a.ts:1:1", "\u2003at /ext/a.ts:1:1", "\ufeff at /ext/a.ts:1:1", "\u0085 at /ext/a.ts:1:1",
		// The form of a Go trace's source line is not a frame of a JavaScript stack.
		"\t/ext/a.ts:12", "\t/ext/a.ts:12 +0x1d", "\t/pk/node_modules/p/src/w.ts:3",
	}
	var stacks []string
	for _, header := range []string{"Error: boom", "Error: /ext/a.ts failed", "", "Error: 100%"} {
		for _, frame := range frames {
			stacks = append(stacks, header+"\n"+frame)
		}
		stacks = append(stacks, header+"\n"+frames[0]+"\n"+frames[19]+"\n"+frames[25])
	}
	stacks = append(stacks, "", "Error: only header", "no newline at /ext/a.ts")

	var want [][]string
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/crash-log.js");
const all = [];
for (const stack of input.stacks) {
	const row = [];
	for (let mask = 0; mask < 1; mask++) row.push(mod.findExtensionStackMatches(stack, input.extensions));
	all.push(row[0]);
}
emit(all);`, map[string]any{"stacks": stacks, "extensions": extensions}, &want)
	metadata := make([]ExtensionStackMetadata, len(extensions))
	for i, e := range extensions {
		metadata[i] = ExtensionStackMetadata{Path: e.Path, ResolvedPath: e.ResolvedPath, SourceInfo: ResourceSourceInfo{Path: e.SourceInfo.Path, Source: e.SourceInfo.Source, Scope: e.SourceInfo.Scope, Origin: e.SourceInfo.Origin, BaseDir: e.SourceInfo.BaseDir}}
	}
	matched := 0
	for i, stack := range stacks {
		got := FindExtensionStackMatches(stack, metadata)
		if len(got) == 0 {
			got = []string{}
		}
		if want[i] == nil {
			want[i] = []string{}
		}
		if len(want[i]) > 0 {
			matched++
		}
		if !reflect.DeepEqual(got, want[i]) {
			t.Errorf("stack %q: matches = %q, Pi %q", stack, got, want[i])
		}
	}
	if matched < 20 {
		t.Fatalf("only %d stacks matched an extension in Pi; the corpus no longer reaches the match path", matched)
	}
}
