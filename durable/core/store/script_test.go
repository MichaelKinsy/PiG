// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package store_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/rec"
	"github.com/MichaelKinsy/PiG/durable/core/store"
)

// J builds an ordered JSON object from alternating keys and values.
func J(kv ...any) *jv.Object {
	o := jv.NewObjectCap(len(kv) / 2)
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

func num(v int64) float64 { return float64(v) }

// commitScript is a sequence of StorageWrite batches in the JSON form pi-durable's tests use. The same text drives pi-durable
// main's SqliteStorage (testdata/oracle.mjs) and, through apply, the core's store.
type commitScript struct{ commits []*jv.Object }

func (s *commitScript) add(mint int, writes ...any) {
	s.commits = append(s.commits, J("mint", num(int64(mint)), "writes", writes))
}

func (s *commitScript) text() string {
	list := make([]any, len(s.commits))
	for i, c := range s.commits {
		list[i] = c
	}
	return jv.Stringify(J("commits", list))
}

func get(o *jv.Object, k string) any { v, _ := o.Get(k); return v }

func idOf(v any) int64 { f, _ := v.(float64); return int64(f) }

// apply stages one commit's writes on the store, the way the session does, and commits them.
func applyCommit(t testing.TB, h *testHost, st *store.Store, c *jv.Object) (int64, error) {
	t.Helper()
	for i := 0; i < int(idOf(get(c, "mint"))); i++ {
		st.MintID()
	}
	var seq int64
	err := h.run(st, func() error {
		tx := st.Begin()
		for _, w := range get(c, "writes").([]any) {
			wo := w.(*jv.Object)
			switch get(wo, "type") {
			case "conversation":
				tx.Conversation(&rec.Conversation{V: get(wo, "value").(*jv.Object)})
			case "entry":
				v := get(wo, "value").(*jv.Object)
				row, err := history.NewEntryRow(jv.Append(nil, v), 0)
				if err != nil {
					t.Fatal(err)
				}
				tx.EntryRow(row)
			case "task":
				task := &rec.Task{V: get(wo, "value").(*jv.Object)}
				tx.Task(task, task.Append(nil))
			case "submission":
				sub := &rec.Submission{V: get(wo, "value").(*jv.Object)}
				tx.Submission(sub, sub.Append(nil))
			case "document.create":
				content := get(wo, "content").(*jv.Object)
				tx.DocumentCreate(&rec.Document{V: get(wo, "record").(*jv.Object)}, int(idOf(get(content, "version"))), get(content, "value"))
			case "document.copy":
				src := get(wo, "source").(*jv.Object)
				at := int64(0)
				if f, ok := get(src, "at").(float64); ok {
					at = int64(f)
				}
				tx.DocumentCopy(&rec.Document{V: get(wo, "record").(*jv.Object)}, idOf(get(src, "id")), at)
			case "document.change":
				content := get(wo, "content").(*jv.Object)
				if get(content, "kind") == "base" {
					tx.DocumentBase(idOf(get(wo, "id")), int(idOf(get(content, "version"))), get(content, "value"))
				} else {
					tx.DocumentDelta(idOf(get(wo, "id")), int(idOf(get(content, "version"))), jv.Append(nil, get(content, "ops")))
				}
			case "document.retire":
				tx.DocumentRetire(idOf(get(wo, "id")))
			default:
				t.Fatalf("unknown write type %v", get(wo, "type"))
			}
		}
		var err error
		seq, err = st.Commit(h.b, tx)
		return err
	})
	return seq, err
}

func nodeAvailable() bool {
	_, err := exec.LookPath("node")
	return err == nil
}

// repoRoot finds the module root from the package directory.
func repoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for ; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		if dir == filepath.Dir(dir) {
			t.Fatal("go.mod not found")
		}
	}
}

// piSource is the pi-durable main checkout (CONTRACT_PI_SOURCE); the test skips without it.
func piSource(t testing.TB) string {
	t.Helper()
	p := os.Getenv("CONTRACT_PI_SOURCE")
	if p == "" {
		t.Skip("CONTRACT_PI_SOURCE is not set (pi-durable main checkout, durable/contract/PIN)")
	}
	if !nodeAvailable() {
		t.Skip("node is not available")
	}
	return p
}

// runOracle runs testdata/oracle.mjs and returns its output.
func runOracle(t testing.TB, script *commitScript, args ...string) (string, error) {
	t.Helper()
	root := repoRoot(t)
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "script.json")
	if err := os.WriteFile(scriptPath, []byte(script.text()), 0o644); err != nil {
		t.Fatal(err)
	}
	argv := []string{"--no-warnings", "--import", filepath.Join(root, "durable/contract/hooks/register.mjs"),
		filepath.Join(root, "durable/core/store/testdata/oracle.mjs"), args[0], scriptPath}
	cmd := exec.Command("node", append(argv, args[1:]...)...)
	cmd.Env = append(os.Environ(), "CONTRACT_PI_SOURCE="+piSource(t), "CONTRACT_TRACE="+filepath.Join(dir, "trace.jsonl"), "ORACLE_DIR="+dir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// piOutcomes parses the per-commit lines of the oracle's output.
func piOutcomes(t testing.TB, out string) []string {
	t.Helper()
	var res []string
	for l := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if !strings.HasPrefix(l, `{"commit"`) {
			continue
		}
		o, err := jv.ParseString(l)
		if err != nil {
			t.Fatalf("oracle line %q: %v", l, err)
		}
		ob := o.(*jv.Object)
		if e, ok := ob.Get("error"); ok {
			res = append(res, "error="+e.(string))
		} else {
			res = append(res, "seq="+strconv.FormatInt(idOf(get(ob, "seq")), 10))
		}
	}
	return res
}

func sameOutcomes(t testing.TB, out string, results []string) {
	t.Helper()
	pi := piOutcomes(t, out)
	if len(pi) != len(results) {
		t.Fatalf("pi ran %d commits, core %d\n%s", len(pi), len(results), out)
	}
	for i := range results {
		if pi[i] != results[i] {
			t.Fatalf("commit %d: pi %s, core %s", i, pi[i], results[i])
		}
	}
}

// checkAgainstPi runs the script through pi-durable main and compares every Pi table of the core's database, and every
// commit's outcome, with it. results are the core's per-commit outcomes ("seq=N" or "error=message").
func checkAgainstPi(t testing.TB, script *commitScript, corePath string, results []string) {
	t.Helper()
	out, err := runOracle(t, script, "compare", corePath)
	sameOutcomes(t, out, results)
	if err != nil || !strings.Contains(out, "\nOK ") {
		t.Fatalf("store differs from pi-durable main: %v\n%s", err, out)
	}
}

// runScript applies the script to a fresh store and returns the per-commit outcomes.
func runScript(t testing.TB, script *commitScript) (*testHost, *store.Store, []string) {
	t.Helper()
	h := newHost(t, "")
	st, _, err := h.openStore(store.Options{EmptyNew: true})
	if err != nil {
		t.Fatal(err)
	}
	var results []string
	for _, c := range script.commits {
		seq, err := applyCommit(t, h, st, c)
		if err == nil {
			results = append(results, "seq="+strconv.FormatInt(seq, 10))
		} else {
			var rej *store.RejectedError
			_ = errors.As(err, &rej)
			results = append(results, "error="+err.Error())
		}
	}
	return h, st, results
}

func TestScriptMatchesPi(t *testing.T) {
	piSource(t)
	script := basicScript()
	h, _, results := runScript(t, script)
	h.close()
	checkAgainstPi(t, script, h.path, results)
}

func TestRandomScriptsMatchPi(t *testing.T) {
	piSource(t)
	for seed := uint64(1); seed <= 12; seed++ {
		script := randomScript(seed, 60)
		h, _, results := runScript(t, script)
		h.close()
		checkAgainstPi(t, script, h.path, results)
	}
}

// badScript commits invalid batches between valid ones; pi-durable refuses each with its own message and the core must
// refuse it identically and leave the same store.
func badScript() *commitScript {
	s := basicScript()
	live := docRecord(14, "pi.live", nil, "conversation", 1, "latest", "initial")
	s.add(1, entryW(20, 1, "pi.user"), entryW(20, 1, "pi.user"))                                                                                    // written twice
	s.add(1, taskW(21, 1, "k", "pending"), subW(21, 1, "queued", nil))                                                                              // two record types
	s.add(0, docCreateW(docRecord(30, "pi.x", nil, "session", 0, "", ""), 1, docValue(0, "")), docBaseW(30, 1, docValue(1, "")))                    // two content commands
	s.add(0, docBaseW(999, 1, docValue(0, "")))                                                                                                     // unknown document
	s.add(0, docBaseW(10, 1, docValue(0, "")))                                                                                                      // retired document
	s.add(0, docDeltaW(14, 7, opSet("n", num(1))))                                                                                                  // version transition without a base
	s.add(1, docCreateW(docRecord(31, "pi.live", nil, "conversation", 1, "latest", "initial"), 1, docValue(0, "")))                                 // a second current incarnation
	s.add(0, docCreateW(live, 1, docValue(0, "")))                                                                                                  // the ID of an existing document
	s.add(0, docRetireW(14), docRetireW(14))                                                                                                        // retired twice
	s.add(1, docCopyW(docRecord(32, "pi.live", nil, "conversation", 11, "latest", "initial"), 14, "current"), docDeltaW(14, 1, opSet("n", num(5)))) // source changed in the copy batch
	s.add(1, docCopyW(docRecord(33, "pi.other", nil, "conversation", 11, "latest", "initial"), 14, "current"))                                      // source does not match
	s.add(1, docCopyW(docRecord(34, "pi.live", nil, "conversation", 11, "latest", "initial"), 998, "current"))                                      // source unreadable
	s.add(0, taskW(22, 1, "k", "pending"))
	return s
}

func TestInvalidCommitsMatchPi(t *testing.T) {
	piSource(t)
	script := badScript()
	h, _, results := runScript(t, script)
	h.close()
	rejected := 0
	for _, r := range results {
		if strings.HasPrefix(r, "error=") {
			rejected++
		}
	}
	if rejected < 10 {
		t.Fatalf("only %d of the bad commits were refused: %v", rejected, results)
	}
	checkAgainstPi(t, script, h.path, results)
}

// split returns the script's first n commits and the rest.
func (s *commitScript) split(n int) (*commitScript, *commitScript) {
	return &commitScript{commits: s.commits[:n]}, &commitScript{commits: s.commits[n:]}
}

// scopesOf lists the conversation and task IDs a script creates: the document scopes a Session loads before it writes.
func (s *commitScript) scopesOf() (convs, tasks []int64) {
	for _, c := range s.commits {
		for _, w := range get(c, "writes").([]any) {
			wo := w.(*jv.Object)
			switch get(wo, "type") {
			case "conversation":
				convs = append(convs, idOf(get(get(wo, "value").(*jv.Object), "id")))
			case "task":
				tasks = append(tasks, idOf(get(get(wo, "value").(*jv.Object), "id")))
			}
		}
	}
	return
}

// coldOpen opens the store file the way a restarted core does and loads the scopes the continuing script writes to.
func coldOpen(t testing.TB, path string, convs, tasks []int64) (*testHost, *store.Store) {
	t.Helper()
	h := newHost(t, path)
	return h, coldOpenOn(t, h, store.Options{}, convs, tasks)
}

// coldOpenOn is coldOpen on a prepared host.
func coldOpenOn(t testing.TB, h *testHost, opts store.Options, convs, tasks []int64) *store.Store {
	t.Helper()
	st, _, err := h.openStore(opts)
	if err != nil {
		t.Fatal(err)
	}
	load := func(kind rec.ScopeKind, owner int64) {
		if err := h.run(st, func() error { _, err := st.EnsureScope(h.b, kind, owner); return err }); err != nil {
			t.Fatal(err)
		}
	}
	load(rec.ScopeSession, 0)
	for _, c := range convs {
		load(rec.ScopeConversation, c)
	}
	for _, task := range tasks {
		load(rec.ScopeTask, task)
	}
	return st
}

func copyFile(t testing.TB, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestHandoffPiToCore: pi-durable writes the first part of a script, the core cold-opens the file and writes the rest;
// the result equals pi-durable continuing the same file (CONTRACT section 5.5, order P to X).
func TestHandoffPiToCore(t *testing.T) {
	piSource(t)
	for seed := uint64(21); seed <= 23; seed++ {
		script := randomScript(seed, 40)
		first, rest := script.split(20)
		dir := t.TempDir()
		base := filepath.Join(dir, "base.sqlite")
		if out, err := runOracle(t, first, "write", base); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		corePath := filepath.Join(dir, "core.sqlite")
		copyFile(t, base, corePath)
		convs, tasks := first.scopesOf()
		h, st := coldOpen(t, corePath, convs, tasks)
		var results []string
		for _, c := range rest.commits {
			seq, err := applyCommit(t, h, st, c)
			if err != nil {
				results = append(results, "error="+err.Error())
			} else {
				results = append(results, "seq="+strconv.FormatInt(seq, 10))
			}
		}
		h.close()
		out, err := runOracle(t, rest, "continue", base, corePath)
		sameOutcomes(t, out, results)
		if err != nil || !strings.Contains(out, "\nOK ") {
			t.Fatalf("seed %d: P to X differs from pi-durable: %v\n%s", seed, err, out)
		}
	}
}

// TestHandoffCoreToPi: the core writes the first part, pi-durable opens the file and writes the rest; the result equals
// pi-durable writing everything (order X to P).
func TestHandoffCoreToPi(t *testing.T) {
	piSource(t)
	for seed := uint64(31); seed <= 33; seed++ {
		script := randomScript(seed, 40)
		first, rest := script.split(20)
		h, _, results := runScript(t, first)
		h.close()
		dir := t.TempDir()
		ref := filepath.Join(dir, "ref.sqlite")
		if out, err := runOracle(t, script, "write", ref); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		out, err := runOracle(t, rest, "continue", h.path, ref)
		if err != nil || !strings.Contains(out, "\nOK ") {
			t.Fatalf("seed %d: X to P differs from pi-durable: %v\n%s", seed, err, out)
		}
		_ = results
	}
}
