package pico3

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func chopLastRecord(t *testing.T, file string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(string(must(os.ReadFile(file)))), "\n")
	if len(lines) == 0 {
		t.Fatal("empty log")
	}
	check(t, os.WriteFile(file, []byte(strings.Join(lines[:len(lines)-1], "\n")+"\n"), 0600))
}

func TestJsonlPublicationMarkerControlsSidecar(t *testing.T) {
	for _, torn := range []string{"main", "sticky"} {
		t.Run(torn, func(t *testing.T) {
			env := openEnv(t, openOptions{backend: "jsonl"})
			check(t, env.root.Config().Set(bg, JsonObject{"followUpMode": "all"}))
			env.close()
			file := "main.jsonl"
			if torn == "sticky" {
				file = "sticky-1.jsonl"
			}
			chopLastRecord(t, filepath.Join(env.dir, file))
			storage, err := OpenJsonlStorage(bg, env.dir, JsonlOptions{Fsync: new(false)})
			if torn == "sticky" {
				if err == nil {
					check(t, storage.Close(bg))
					t.Fatal("published missing sidecar accepted")
				}
				return
			}
			check(t, err)
			equal(t, must(storage.Doc(bg, StickyDoc(1)))["followUpMode"], "one-at-a-time", "unpublished override")
			check(t, storage.Close(bg))
			reopened := openEnv(t, openOptions{backend: "jsonl", dir: env.dir})
			check(t, reopened.root.Config().Set(bg, JsonObject{"followUpMode": "all"}))
			reopened.close()
			again := openEnv(t, openOptions{backend: "jsonl", dir: env.dir})
			equal(t, again.sticky()["followUpMode"], "all", "sequence safely reused")
		})
	}
}

func TestJsonlTornAndMalformedTails(t *testing.T) {
	env := openEnv(t, openOptions{backend: "jsonl"})
	_, err := env.root.Write(bg, NewEntry{Kind: "note"})
	check(t, err)
	env.close()
	file := filepath.Join(env.dir, "main.jsonl")
	original := must(os.ReadFile(file))
	check(t, os.WriteFile(file, append(append([]byte{}, original...), []byte(`{"seq":99,"maxId":9,"wri`)...), 0600))
	reopened := openEnv(t, openOptions{backend: "jsonl", dir: env.dir})
	_, err = reopened.root.Write(bg, NewEntry{Kind: "note"})
	check(t, err)
	reopened.close()
	again := openEnv(t, openOptions{backend: "jsonl", dir: env.dir})
	equal(t, len(again.entries()), 2, "torn tail repaired")
	again.close()
	complete := append(must(os.ReadFile(file)), []byte("{\"seq\":\"bad\"}\n")...)
	check(t, os.WriteFile(file, complete, 0600))
	if storage, err := OpenJsonlStorage(bg, env.dir, JsonlOptions{Fsync: new(false)}); err == nil {
		check(t, storage.Close(bg))
		t.Fatal("malformed complete record accepted")
	}
	equal(t, string(must(os.ReadFile(file))), string(complete), "corruption not rewritten")
}

func TestUnsafeToolCheckpointSurvivesOnlyWithMarker(t *testing.T) {
	for _, torn := range []bool{false, true} {
		t.Run(map[bool]string{false: "published", true: "torn"}[torn], func(t *testing.T) {
			gate := &testGate{}
			tool := newTool("x", toolOptions{replay: "unsafe", gate: gate})
			env := openEnv(t, openOptions{backend: "jsonl", tools: []*ToolDeclaration{tool.ToolDeclaration}})
			input := env.send(env.root, "tool:x")
			task := env.untilPhase("pi.tool", "started")
			gate.Arrivals(t, 1)
			env.close()
			assertAtomicityMarker(t, env.dir, task.Id)
			if torn {
				chopLastRecord(t, filepath.Join(env.dir, "main.jsonl"))
				storage := must(OpenJsonlStorage(bg, env.dir, JsonlOptions{Fsync: new(false)}))
				equal(t, must(storage.Task(bg, task.Id)).Checkpoint, Checkpoint(nil), "unpublished checkpoint absent")
				check(t, storage.Close(bg))
			}
			replacement := newTool("x", toolOptions{replay: "unsafe"})
			next := openEnv(t, openOptions{backend: "jsonl", dir: env.dir, tools: []*ToolDeclaration{replacement.ToolDeclaration}})
			next.idle()
			equal(t, next.input(input.Id).Status, InputDone, "settled")
			equal(t, must(next.h.GetTask(bg, task.Id)).Outcome.Status, OutcomeCompleted, "tool outcome")
			expected := int64(0)
			if torn {
				expected = 1
			}
			equal(t, replacement.calls.Load(), expected, "effect runs only without marker")
			if !torn && !strings.Contains(string(mustJSON(toolResultEntry(t, next).Model)), "interrupted") {
				t.Fatal("unsafe published effect lacks interrupted result")
			}
		})
	}
}
