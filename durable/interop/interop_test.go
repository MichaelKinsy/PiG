// Cross-runtime proof that PiG's Go Pi Durable and Pi Durable 1.0.4 (Node, the pinned packages) share one store.
//
//   - TestScriptedSessionStoresMatchPiDurable: the same scripted session (documents, a tool that starts a task, a
//     fork with an init, compaction, a reset) run by each runtime on a fresh SQLite file leaves the same rows.
//   - TestGoOpensAndResumesAStoreWrittenByPiDurable: Node writes the store; Go reads it through the Harness API,
//     resumes it (new input, a second fork turn, compaction); Node then reads what Go wrote.
//   - TestPiDurableOpensAndResumesAStoreWrittenByGo: the same the other way round.
//
// The Node program is app.mjs, scenario.mjs, resume.mjs, apidump.mjs and rawdump.mjs in this directory; app_test.go is
// its Go twin and the two must stay the same program.

package interop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// node runs a script of this directory and returns its standard output.
func node(t *testing.T, args ...string) []byte {
	t.Helper()
	ensureDependencies(t)
	command := exec.Command("node", args...)
	command.Dir = "."
	command.Env = append(os.Environ(), "NODE_NO_WARNINGS=1")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		t.Fatalf("node %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return out
}

var dependenciesReady bool

// ensureDependencies requires the pinned Pi packages (package-lock.json), which `make durable-interop-deps` installs
// before `make test`. It fails when they are missing: an unavailable dependency is a blocker, not a pass, and a test
// does not reach the network or write into the source tree.
func ensureDependencies(t *testing.T) {
	t.Helper()
	if dependenciesReady {
		return
	}
	if _, err := os.Stat(filepath.Join("node_modules", "@earendil-works", "pi-durable", "package.json")); err != nil {
		t.Fatalf("the pinned Pi Durable packages are not installed in durable/interop: run `make durable-interop-deps`: %v", err)
	}
	dependenciesReady = true
}

func decode(t *testing.T, data []byte) any {
	t.Helper()
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return value
}

// differences lists up to limit paths at which a and b differ.
func differences(path string, a, b any, limit int, out *[]string) {
	if len(*out) >= limit {
		return
	}
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: %v != %v", path, summarize(a), summarize(b)))
			return
		}
		keys := map[string]bool{}
		for key := range av {
			keys[key] = true
		}
		for key := range bv {
			keys[key] = true
		}
		sorted := make([]string, 0, len(keys))
		for key := range keys {
			sorted = append(sorted, key)
		}
		sort.Strings(sorted)
		for _, key := range sorted {
			x, inA := av[key]
			y, inB := bv[key]
			switch {
			case !inA:
				*out = append(*out, fmt.Sprintf("%s.%s: only in the second: %s", path, key, summarize(y)))
			case !inB:
				*out = append(*out, fmt.Sprintf("%s.%s: only in the first: %s", path, key, summarize(x)))
			default:
				differences(path+"."+key, x, y, limit, out)
			}
		}
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			*out = append(*out, fmt.Sprintf("%s: length %d != %s", path, len(av), summarize(b)))
			return
		}
		for i := range av {
			differences(fmt.Sprintf("%s[%d]", path, i), av[i], bv[i], limit, out)
		}
	default:
		if !reflect.DeepEqual(a, b) {
			*out = append(*out, fmt.Sprintf("%s: %s != %s", path, summarize(a), summarize(b)))
		}
	}
}

func summarize(value any) string {
	text, _ := json.Marshal(value)
	if len(text) > 120 {
		return string(text[:120]) + "..."
	}
	return string(text)
}

func expectSame(t *testing.T, what string, first, second any) {
	t.Helper()
	if reflect.DeepEqual(first, second) {
		return
	}
	var out []string
	differences("$", first, second, 25, &out)
	t.Fatalf("%s differ (first: Pi Durable or the earlier reader, second: Go):\n%s", what, strings.Join(out, "\n"))
}

func TestScriptedSessionStoresMatchPiDurable(t *testing.T) {
	dir := t.TempDir()
	piStore, goStore := filepath.Join(dir, "pi.sqlite"), filepath.Join(dir, "go.sqlite")
	node(t, "scenario.mjs", piStore)
	runScenario(t, goStore)
	expectSame(t, "raw tables of the scripted session", decode(t, node(t, "rawdump.mjs", piStore)), rawDump(t, goStore))
}

func TestSecondScriptedSessionStoresMatchPiDurable(t *testing.T) {
	dir := t.TempDir()
	piStore, goStore := filepath.Join(dir, "pi.sqlite"), filepath.Join(dir, "go.sqlite")
	node(t, "scenario2.mjs", piStore)
	runScenario2(t, goStore)
	expectSame(t, "raw tables of the second scripted session", decode(t, node(t, "rawdump.mjs", piStore)), rawDump(t, goStore))
}

// scenarios are the scripted sessions each runtime can write, over each storage.
var scenarios = []struct {
	name   string
	script string
	run    func(*testing.T, string)
}{
	{"first", "scenario.mjs", runScenario},
	{"second", "scenario2.mjs", runScenario2},
}

var storages = []struct{ name, file string }{{"sqlite", "pocket.sqlite"}, {"jsonl", "pocket-jsonl"}}

// TestScriptedSessionsViewIdentically runs each script by each runtime on each storage and compares what the Harness
// API shows. The SQLite tests above compare the rows themselves.
func TestScriptedSessionsViewIdentically(t *testing.T) {
	for _, storage := range storages {
		for _, scenario := range scenarios {
			t.Run(storage.name+"/"+scenario.name, func(t *testing.T) {
				dir := t.TempDir()
				ext := ""
				if storage.name == "sqlite" {
					ext = ".sqlite"
				}
				piStore, goStore := filepath.Join(dir, "pi"+ext), filepath.Join(dir, "go"+ext)
				node(t, scenario.script, piStore)
				scenario.run(t, goStore)
				expectSame(t, "API view of the scripted session", decode(t, node(t, "apidump.mjs", piStore)), apiDump(t, goStore))
			})
		}
	}
}

// jsonlDump is every file of a JSONL store directory, each line decoded and normalized as in the SQLite dump.
func jsonlDump(t *testing.T, dir string) any {
	t.Helper()
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(dir, file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var lines []any
		for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			var parsed any
			if err := json.Unmarshal([]byte(line), &parsed); err != nil {
				t.Fatalf("%s: %v", file.Name(), err)
			}
			lines = append(lines, normalize(parsed, ""))
		}
		if file.Name() == "main.jsonl" {
			lines = unordered(t, lines)
		}
		out[file.Name()] = lines
	}
	return viaJSON(t, out)
}

// unordered sorts the commit lines of main.jsonl by their text, without their sequence numbers. Two tasks that are
// ready at once commit in a fixed order in Pi's single-threaded scheduler and in either order in Go's concurrent one
// (the script's tool task and the job it started finish together); every record is unchanged by the order.
func unordered(t *testing.T, lines []any) []any {
	t.Helper()
	texts := make([]string, len(lines))
	for i, line := range lines {
		commit, ok := line.(map[string]any)
		if !ok {
			t.Fatalf("main.jsonl line %d is not an object", i)
		}
		delete(commit, "seq")
		encoded, err := json.Marshal(commit)
		if err != nil {
			t.Fatal(err)
		}
		texts[i] = string(encoded)
	}
	sort.Strings(texts)
	out := make([]any, len(texts))
	for i, text := range texts {
		out[i] = text
	}
	return out
}

// TestScriptedSessionJsonlFilesMatchPiDurable compares the JSONL files themselves: the same names, and the same
// records in the same order, as JSON values.
func TestScriptedSessionJsonlFilesMatchPiDurable(t *testing.T) {
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			dir := t.TempDir()
			piStore, goStore := filepath.Join(dir, "pi"), filepath.Join(dir, "go")
			node(t, scenario.script, piStore)
			scenario.run(t, goStore)
			expectSame(t, "JSONL files of the scripted session", jsonlDump(t, piStore), jsonlDump(t, goStore))
		})
	}
}

func TestGoOpensAndResumesAStoreWrittenByPiDurable(t *testing.T) {
	for _, storage := range storages {
		for _, scenario := range scenarios {
			t.Run(storage.name+"/"+scenario.name, func(t *testing.T) {
				store := filepath.Join(t.TempDir(), storage.file)
				node(t, scenario.script, store)
				// Go reads what Node wrote, through the Harness API.
				expectSame(t, "API view of the Pi-written store", decode(t, node(t, "apidump.mjs", store)), apiDump(t, store))
				// Go resumes it, and Node reads what Go then wrote.
				runResume(t, store)
				expectSame(t, "API view of the store Go resumed", decode(t, node(t, "apidump.mjs", store)), apiDump(t, store))
				// Node resumes the store Go resumed.
				node(t, "resume.mjs", store)
				expectSame(t, "API view after both runtimes resumed", decode(t, node(t, "apidump.mjs", store)), apiDump(t, store))
			})
		}
	}
}

func TestPiDurableOpensAndResumesAStoreWrittenByGo(t *testing.T) {
	for _, storage := range storages {
		for _, scenario := range scenarios {
			t.Run(storage.name+"/"+scenario.name, func(t *testing.T) {
				store := filepath.Join(t.TempDir(), storage.file)
				scenario.run(t, store)
				expectSame(t, "API view of the Go-written store", decode(t, node(t, "apidump.mjs", store)), apiDump(t, store))
				node(t, "resume.mjs", store)
				expectSame(t, "API view of the store Node resumed", decode(t, node(t, "apidump.mjs", store)), apiDump(t, store))
				runResume(t, store)
				expectSame(t, "API view after both runtimes resumed", decode(t, node(t, "apidump.mjs", store)), apiDump(t, store))
			})
		}
	}
}
