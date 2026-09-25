package pico3

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func atomicityLastRecord(t *testing.T, dir, name string) JsonObject {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(string(must(os.ReadFile(filepath.Join(dir, name))))), "\n")
	var record JsonObject
	check(t, json.Unmarshal([]byte(lines[len(lines)-1]), &record))
	return record
}

func assertAtomicityMarker(t *testing.T, dir string, task Id) {
	t.Helper()
	marker := atomicityLastRecord(t, dir, "main.jsonl")
	taskFile := fmt.Sprintf("task-%d.jsonl", task)
	equal(t, marker["seq"], atomicityLastRecord(t, dir, taskFile)["seq"], "task publication")
	equal(t, marker["seq"], atomicityLastRecord(t, dir, "sticky-1.jsonl")["seq"], "sticky publication")
	var refs []string
	for _, ref := range arr(marker, "refs") {
		refs = append(refs, ref.(string))
	}
	slices.Sort(refs)
	want := []string{"sticky-1.jsonl", taskFile}
	slices.Sort(want)
	equal(t, refs, want, "sidecar refs")
	equal(t, arr(marker, "writes"), []any{}, "marker without table writes")
}

func TestTornAdmissionDropsInboxAndInputTogether(t *testing.T) {
	gate := &testGate{}
	env := openEnv(t, openOptions{backend: "jsonl", models: newFake(fakeOptions{respond: echoScript, gate: gate})})
	active := env.send(env.root, "A")
	gate.Arrivals(t, 1)
	queued := env.send(env.root, "F")
	env.close()
	equal(t, atomicityLastRecord(t, env.dir, "main.jsonl")["seq"], atomicityLastRecord(t, env.dir, "sticky-1.jsonl")["seq"], "same commit")
	chopLastRecord(t, filepath.Join(env.dir, "main.jsonl"))
	next := openEnv(t, openOptions{backend: "jsonl", dir: env.dir})
	equal(t, must(next.storage.Input(bg, queued.Id)), (*Input)(nil), "unpublished input absent")
	equal(t, arr(next.sticky(), "inbox"), []any{}, "unpublished inbox absent")
	next.idle()
	equal(t, next.input(active.Id).Status, InputDone, "earlier input preserved")
}

func TestThirdPartyCheckpointAndStickySharePublication(t *testing.T) {
	gate := &testGate{}
	var ran []string
	var state *Namespace
	kind := &Kind{Name: "two-files", Initial: func(context.Context, Task, *Runtime) (Step, error) { return Step{Next: Checkpoint{"phase": "a"}}, nil }, Phases: map[string]PhaseHandler{
		"a": func(ctx context.Context, _ Task, rt *Runtime) (Step, error) {
			ran = append(ran, "a")
			_, err := rt.Commit(ctx, func(_ context.Context, tx *Tx, _ Task) (any, error) {
				if err := tx.Checkpoint(Checkpoint{"phase": "b"}); err != nil {
					return nil, err
				}
				must(tx.Plugins(state)).Set("mark", "b")
				return nil, nil
			})
			if err != nil {
				return Step{}, err
			}
			return done(Completed(nil)), gate.Wait(ctx)
		},
		"b": func(ctx context.Context, _ Task, _ *Runtime) (Step, error) {
			ran = append(ran, "b")
			return done(Completed(nil)), gate.Wait(ctx)
		},
	}}
	setup := func(t *testing.T, h *Harness) {
		state = must(h.Namespace("atomicity", NamespaceDefaults{Sticky: JsonObject{"mark": "initial"}}, nil))
	}
	env := openEnv(t, openOptions{backend: "jsonl", taskKinds: []*Kind{kind}, setup: setup})
	ref := createTestTask(t, env, kind, nil)
	gate.Arrivals(t, 1)
	env.close()
	assertAtomicityMarker(t, env.dir, ref.Id)
	env = openEnv(t, openOptions{backend: "jsonl", dir: env.dir, taskKinds: []*Kind{kind}, setup: setup})
	gate.Arrivals(t, 2)
	equal(t, ran, []string{"a", "b"}, "intact checkpoint")
	equal(t, obj(obj(env.sticky(), "plugins"), "atomicity")["mark"], "b", "intact sticky")
	env.close()
	equal(t, arr(atomicityLastRecord(t, env.dir, "main.jsonl"), "writes"), []any{}, "read-only recovery writes no main record")
	chopLastRecord(t, filepath.Join(env.dir, "main.jsonl"))
	storage := must(OpenJsonlStorage(bg, env.dir, JsonlOptions{Fsync: new(false)}))
	equal(t, Phase(must(storage.Task(bg, ref.Id)).Checkpoint), "a", "unpublished checkpoint discarded")
	equal(t, obj(obj(must(storage.Doc(bg, StickyDoc(1))), "plugins"), "atomicity")["mark"], nil, "unpublished sticky discarded")
	check(t, storage.Close(bg))
	env = openEnv(t, openOptions{backend: "jsonl", dir: env.dir, taskKinds: []*Kind{kind}, setup: setup})
	gate.Arrivals(t, 3)
	equal(t, ran, []string{"a", "b", "a"}, "torn phase repeated")
	equal(t, obj(obj(env.sticky(), "plugins"), "atomicity")["mark"], "b", "pair rewritten")
	gate.Open()
	env.untilTerminal(ref.Id)
}

func TestStaleTerminalSidecarAndCompleteCorruption(t *testing.T) {
	env := openEnv(t, openOptions{backend: "jsonl"})
	ref := createTestTask(t, env, Kinds.Plugin, JsonObject{"handler": "missing", "input": nil})
	env.untilTerminal(ref.Id)
	env.close()
	stale := JsonObject{"seq": 1, "maxId": 1, "writes": []any{JsonObject{"type": "task.patch", "patch": JsonObject{"id": float64(ref.Id), "status": "running", "checkpoint": JsonObject{"phase": "started"}}}}}
	check(t, os.WriteFile(filepath.Join(env.dir, fmt.Sprintf("task-%d.jsonl", ref.Id)), append(mustJSON(stale), '\n'), 0600))
	next := openEnv(t, openOptions{backend: "jsonl", dir: env.dir})
	equal(t, must(next.h.GetTask(bg, ref.Id)).Status, TaskTerminal, "stale sidecar cannot resurrect")
	next.close()
	file := filepath.Join(env.dir, "main.jsonl")
	good := must(os.ReadFile(file))
	for _, tail := range []string{"{\"seq\":1,\"maxId\":1,\"writes\":[]}\n", "{\"seq\":\"x\"}\n"} {
		corrupt := append(slices.Clone(good), []byte(tail)...)
		check(t, os.WriteFile(file, corrupt, 0600))
		storage, err := OpenJsonlStorage(bg, env.dir, JsonlOptions{Fsync: new(false)})
		if err == nil {
			check(t, storage.Close(bg))
			t.Fatal("complete corruption accepted")
		}
		equal(t, string(must(os.ReadFile(file))), string(corrupt), "complete corruption preserved")
	}
	check(t, os.WriteFile(file, good, 0600))
}
