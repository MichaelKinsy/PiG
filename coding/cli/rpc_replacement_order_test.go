package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// rpc-mode.ts:808-810 dispatches every stdin line at once (`void handleInputLine(line)`), and the Session-replacing commands and export_html
// await. A get_state in the same batch therefore answers first and still reports the old Session file; the command answers afterwards
// (comb-audit row 58, measured against Pi 1.1.0 for all five commands).
func TestRPCSessionReplacementAnswersAfterALaterCommandOfItsBatch(t *testing.T) {
	source, err := os.ReadFile("../../test/parity/scenarios/rpc/testdata/session/sessions/source.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, command string }{
		{"new_session", `{"id":"1","type":"new_session"}`},
		{"fork", `{"id":"1","type":"fork","entryId":"user-1"}`},
		{"clone", `{"id":"1","type":"clone"}`},
		{"switch_session", `{"id":"1","type":"switch_session","sessionPath":"OTHER"}`},
		{"export_html", `{"id":"1","type":"export_html"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			current, other := filepath.Join(dir, "current.jsonl"), filepath.Join(dir, "other.jsonl")
			for _, path := range []string{current, other} {
				if err := os.WriteFile(path, source, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + dir}, "--offline", "--no-extensions", "--model", "test-faux/faux-1", "--session", current, "--session-dir", filepath.Join(dir, "sessions"))
			command := tc.command
			switch tc.name {
			case "switch_session":
				command = `{"id":"1","type":"switch_session","sessionPath":` + jsonString(other) + `}`
			case "export_html":
				command = `{"id":"1","type":"export_html","outputPath":` + jsonString(filepath.Join(dir, "export.html")) + `}`
			}
			if _, err := io.WriteString(p.stdin, command+"\n"+`{"id":"2","type":"get_state"}`+"\n"); err != nil {
				t.Fatal(err)
			}
			var order []string
			var stateFile string
			p.await("both responses", func(record rpcRecord) bool {
				if record["type"] != "response" {
					return false
				}
				id, _ := record["id"].(string)
				if record["success"] != true {
					t.Fatalf("%s failed: %v", id, record)
				}
				if id == "2" {
					data, _ := record["data"].(map[string]any)
					stateFile, _ = data["sessionFile"].(string)
				}
				order = append(order, id)
				return len(order) == 2
			})
			if order[0] != "2" || order[1] != "1" {
				t.Errorf("response order = %v, want [2 1]: get_state answers before the awaited command", order)
			}
			if stateFile != current {
				t.Errorf("get_state sessionFile = %q, want the old Session file %q", stateFile, current)
			}
			p.sendJSON(map[string]any{"id": "3", "type": "get_state"})
			p.await("state after the command", func(record rpcRecord) bool {
				if record["id"] != "3" {
					return false
				}
				data, _ := record["data"].(map[string]any)
				file, _ := data["sessionFile"].(string)
				replaced := tc.name != "export_html"
				if replaced && file == current {
					t.Errorf("get_state after the answer still reports the old Session file %q", file)
				}
				if !replaced && file != current {
					t.Errorf("get_state after export_html reports %q, want %q", file, current)
				}
				return true
			})
		})
	}
}

// Two Session-replacing commands of one batch answer in dispatch order, and the later one decides the final Session: Pi 1.1.0 answers switch_session before a new_session dispatched after it and ends on the new Session, every run.
func TestRPCSessionReplacementsOfOneBatchSettleInDispatchOrder(t *testing.T) {
	source, err := os.ReadFile("../../test/parity/scenarios/rpc/testdata/session/sessions/source.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		dir := t.TempDir()
		current, other := filepath.Join(dir, "current.jsonl"), filepath.Join(dir, "other.jsonl")
		for _, path := range []string{current, other} {
			if err := os.WriteFile(path, source, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + dir}, "--offline", "--no-extensions", "--model", "test-faux/faux-1", "--session", current, "--session-dir", filepath.Join(dir, "sessions"))
		if _, err := io.WriteString(p.stdin, `{"id":"1","type":"switch_session","sessionPath":`+jsonString(other)+`}`+"\n"+`{"id":"2","type":"new_session"}`+"\n"); err != nil {
			t.Fatal(err)
		}
		var order []string
		p.await("both replacements", func(record rpcRecord) bool {
			if record["type"] != "response" {
				return false
			}
			id, _ := record["id"].(string)
			if record["success"] != true {
				t.Fatalf("%s failed: %v", id, record)
			}
			order = append(order, id)
			return len(order) == 2
		})
		if order[0] != "1" || order[1] != "2" {
			t.Fatalf("response order = %v, want [1 2]: replacements of one batch settle in dispatch order", order)
		}
		p.sendJSON(map[string]any{"id": "3", "type": "get_state"})
		p.await("final state", func(record rpcRecord) bool {
			if record["id"] != "3" {
				return false
			}
			data, _ := record["data"].(map[string]any)
			if file, _ := data["sessionFile"].(string); file == current || file == other {
				t.Errorf("final sessionFile = %q, want new_session's file: the later replacement applies last", file)
			}
			return true
		})
	}
}

// Several replacements requested in one batch run one after another in request order. They used to start together once the batch drained and
// rebind the RPC host from several goroutines at once: a race build reported the data race in Runner.BindCommandActions and about one run in
// two never answered and never exited at stdin end.
func TestRPCReplacementsOfOneBatchRunInRequestOrder(t *testing.T) {
	source, err := os.ReadFile("../../test/parity/scenarios/rpc/testdata/session/sessions/source.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for run := range 4 {
		dir := t.TempDir()
		current, other := filepath.Join(dir, "current.jsonl"), filepath.Join(dir, "other.jsonl")
		for _, path := range []string{current, other} {
			if err := os.WriteFile(path, source, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + dir}, "--offline", "--no-extensions", "--model", "test-faux/faux-1", "--session", current, "--session-dir", filepath.Join(dir, "sessions"))
		batch := `{"id":"1","type":"switch_session","sessionPath":` + jsonString(other) + `}` + "\n" +
			`{"id":"2","type":"new_session"}` + "\n" +
			`{"id":"3","type":"new_session"}` + "\n" +
			`{"id":"4","type":"get_state"}` + "\n"
		if _, err := io.WriteString(p.stdin, batch); err != nil {
			t.Fatal(err)
		}
		var order []string
		p.await("four responses", func(record rpcRecord) bool {
			if record["type"] != "response" {
				return false
			}
			if record["success"] != true {
				t.Fatalf("run %d: %v failed: %v", run, record["id"], record)
			}
			order = append(order, responseID(t, record))
			return len(order) == 4
		})
		if want := []string{"4", "1", "2", "3"}; !slices.Equal(order, want) {
			t.Fatalf("run %d: response order = %v, want %v", run, order, want)
		}
		p.closeAndWait("after the replacements answered")
	}
}

// A clone requested after a new_session of the same batch resolves its entry in the Session that was current when it was requested, so it
// succeeds, answers after new_session and ends on the clone's Session, as in Pi 1.1.0 (new_session, get_state, clone: get_state twice, then
// new_session, clone). A clone of an unsaved Session fails before the earlier new_session answers.
func TestRPCCloneAfterNewSessionOfOneBatchUsesTheRequestingSession(t *testing.T) {
	source, err := os.ReadFile("../../test/parity/scenarios/rpc/testdata/session/sessions/source.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	current := filepath.Join(dir, "current.jsonl")
	if err := os.WriteFile(current, source, 0o600); err != nil {
		t.Fatal(err)
	}
	p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + dir}, "--offline", "--no-extensions", "--model", "test-faux/faux-1", "--session", current, "--session-dir", filepath.Join(dir, "sessions"))
	batch := `{"id":"1","type":"new_session"}` + "\n" + `{"id":"2","type":"get_state"}` + "\n" + `{"id":"3","type":"clone"}` + "\n"
	if _, err := io.WriteString(p.stdin, batch); err != nil {
		t.Fatal(err)
	}
	var order []string
	p.await("three responses", func(record rpcRecord) bool {
		if record["type"] != "response" {
			return false
		}
		if record["success"] != true {
			t.Fatalf("%v failed: %v", record["id"], record)
		}
		order = append(order, responseID(t, record))
		return len(order) == 3
	})
	if want := []string{"2", "1", "3"}; !slices.Equal(order, want) {
		t.Fatalf("response order = %v, want %v", order, want)
	}
	p.sendJSON(map[string]any{"id": "4", "type": "get_state"})
	p.await("final state", func(record rpcRecord) bool {
		if record["id"] != "4" {
			return false
		}
		data, _ := record["data"].(map[string]any)
		if file, _ := data["sessionFile"].(string); file == current || file == "" {
			t.Errorf("final sessionFile = %q, want the clone's new file", file)
		}
		if data["messageCount"] != float64(2) {
			t.Errorf("final messageCount = %v, want 2: the clone carries the cloned Session's messages, not the empty new_session", data["messageCount"])
		}
		return true
	})
	p.closeAndWait("after the replacements answered")
}

// The same batch on an in-memory Session (--no-session): Pi 1.1.0 branches the Session that requested the clone, so the clone succeeds and
// carries its messages. PiG branched the in-memory Session that new_session had just installed and failed with "Entry ... not found".
func TestRPCCloneAfterNewSessionOfOneBatchBranchesTheRequestingInMemorySession(t *testing.T) {
	dir := t.TempDir()
	p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + dir}, "--offline", "--no-extensions", "--no-session", "--model", "test-faux/faux-1")
	p.sendJSON(map[string]any{"id": "prompt", "type": "prompt", "message": "hi"})
	var before float64
	p.await("the prompt's run to settle", func(record rpcRecord) bool { return record["type"] == "agent_end" })
	p.sendJSON(map[string]any{"id": "before", "type": "get_state"})
	p.await("state before", func(record rpcRecord) bool {
		if record["id"] != "before" {
			return false
		}
		data, _ := record["data"].(map[string]any)
		before, _ = data["messageCount"].(float64)
		return true
	})
	if before == 0 {
		t.Fatal("the prompt left no messages to clone")
	}
	if _, err := io.WriteString(p.stdin, `{"id":"1","type":"new_session"}`+"\n"+`{"id":"2","type":"clone"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	answered := 0
	p.await("both responses", func(record rpcRecord) bool {
		if record["type"] != "response" || (record["id"] != "1" && record["id"] != "2") {
			return false
		}
		if record["success"] != true {
			t.Fatalf("%v failed: %v", record["id"], record)
		}
		answered++
		return answered == 2
	})
	p.sendJSON(map[string]any{"id": "after", "type": "get_state"})
	p.await("state after", func(record rpcRecord) bool {
		if record["id"] != "after" {
			return false
		}
		data, _ := record["data"].(map[string]any)
		if data["messageCount"] != before {
			t.Errorf("messageCount after the clone = %v, want %v from the cloned Session", data["messageCount"], before)
		}
		return true
	})
	p.closeAndWait("after the clone")
}

// A replacement starts after every line of its input batch was dispatched (rpc-mode.ts:808-810: Pi handles the whole chunk before the
// replacement's createRuntime resumes). An export of a large Session keeps the input loop busy for longer than the replacement needs, so a
// replacement that started at once would be applied before the get_state behind it was handled.
func TestRPCReplacementStartsAfterItsBatchWasDispatched(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "current.jsonl")
	var session strings.Builder
	session.WriteString(`{"type":"session","version":3,"id":"rpc-large","timestamp":"2026-01-01T00:00:00Z","cwd":"."}` + "\n")
	parent := "null"
	for i := range 4000 {
		id := fmt.Sprintf("m%d", i)
		role, content := "user", fmt.Sprintf(`[{"type":"text","text":"question %d %s"}]`, i, strings.Repeat("x", 200))
		if i%2 == 1 {
			role, content = "assistant", fmt.Sprintf(`[{"type":"text","text":"answer %d %s"}],"api":"test-faux","provider":"test-faux","model":"faux-1","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop"`, i, strings.Repeat("y", 200))
		}
		fmt.Fprintf(&session, `{"type":"message","id":%q,"parentId":%s,"timestamp":"2026-01-01T00:00:01Z","message":{"role":%q,"content":%s,"timestamp":1767225603000}}`+"\n", id, parent, role, content)
		parent = `"` + id + `"`
	}
	if err := os.WriteFile(current, []byte(session.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + dir}, "--offline", "--no-extensions", "--model", "test-faux/faux-1", "--session", current, "--session-dir", filepath.Join(dir, "sessions"))
	batch := `{"id":"new","type":"new_session"}` + "\n" +
		`{"id":"export","type":"export_html","outputPath":` + jsonString(filepath.Join(dir, "export.html")) + `}` + "\n" +
		`{"id":"state","type":"get_state"}` + "\n"
	if _, err := io.WriteString(p.stdin, batch); err != nil {
		t.Fatal(err)
	}
	answered := 0
	p.await("every response", func(record rpcRecord) bool {
		if record["type"] != "response" {
			return false
		}
		if record["success"] != true {
			t.Fatalf("%v failed: %v", record["id"], record)
		}
		if record["id"] == "state" {
			data, _ := record["data"].(map[string]any)
			if file, _ := data["sessionFile"].(string); file != current {
				t.Errorf("get_state sessionFile = %q, want %q: the replacement applied before its batch was handled", file, current)
			}
		}
		answered++
		return answered == 3
	})
}

// Pi's compact() awaits abort() before it creates its abort controller, so a compact line of the same batch aborts nothing of an earlier one:
// two pipelined compacts of a Session that is too small both answer "Nothing to compact" (Pi 1.1.0, 3 of 3). Pig ran each compact on its
// own goroutine, and the second claimed the Session while the first was starting, cancelling it (Compaction cancelled in 2 of 3 runs).
func TestRPCCompactsOfOneBatchDoNotCancelEachOther(t *testing.T) {
	source, err := os.ReadFile("../../test/parity/scenarios/rpc/testdata/session/sessions/source.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for run := range 10 {
		dir := t.TempDir()
		current := filepath.Join(dir, "current.jsonl")
		if err := os.WriteFile(current, source, 0o600); err != nil {
			t.Fatal(err)
		}
		p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + dir}, "--offline", "--no-extensions", "--model", "test-faux/faux-1", "--session", current, "--session-dir", filepath.Join(dir, "sessions"))
		if _, err := io.WriteString(p.stdin, `{"id":"1","type":"compact"}`+"\n"+`{"id":"2","type":"compact"}`+"\n"); err != nil {
			t.Fatal(err)
		}
		var order []string
		p.await("both compact responses", func(record rpcRecord) bool {
			if record["type"] != "response" {
				return false
			}
			id, _ := record["id"].(string)
			if message, _ := record["error"].(string); message != "Nothing to compact (session too small)" {
				t.Fatalf("run %d: compact %s answered %v, want \"Nothing to compact (session too small)\"", run, id, record)
			}
			order = append(order, id)
			return len(order) == 2
		})
		if order[0] != "1" || order[1] != "2" {
			t.Fatalf("run %d: response order = %v, want [1 2]", run, order)
		}
		p.closeAndWait("after both compacts answered")
	}
}

// largeRPCSession is a 4000-entry Session whose compaction is not trivial.
func largeRPCSession() string {
	var session strings.Builder
	session.WriteString(`{"type":"session","version":3,"id":"rpc-large","timestamp":"2026-01-01T00:00:00Z","cwd":"."}` + "\n")
	parent := "null"
	for i := range 4000 {
		id := fmt.Sprintf("m%d", i)
		role, content := "user", fmt.Sprintf(`[{"type":"text","text":"question %d %s"}]`, i, strings.Repeat("x", 200))
		if i%2 == 1 {
			role, content = "assistant", fmt.Sprintf(`[{"type":"text","text":"answer %d %s"}],"api":"test-faux","provider":"test-faux","model":"faux-1","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop"`, i, strings.Repeat("y", 200))
		}
		fmt.Fprintf(&session, `{"type":"message","id":%q,"parentId":%s,"timestamp":"2026-01-01T00:00:01Z","message":{"role":%q,"content":%s,"timestamp":1767225603000}}`+"\n", id, parent, role, content)
		parent = `"` + id + `"`
	}
	return session.String()
}

// A Session replacement disposes the outgoing Session, which aborts its compaction (agent-session.ts:1393-1399) and does not wait for it. Pi's compaction yields
// before its summary request (getAuth rejects with the signal's reason, agent-session.ts:572-608), so a replacement of the same batch tears down first.
// Pi 1.1.0 (test-faux, a 1 MB Session; ordinary and TEST_FAUX_SLOW_COMPACTION summaries) answers each compact of these batches with an error and writes
// no compaction entry; a lone compact answers "This operation was aborted". PiG completed a compaction that was fast enough, or that a clone or fork
// followed, and wrote an entry.
func TestRPCReplacementAbortsTheCompactionOfItsBatch(t *testing.T) {
	session := largeRPCSession()
	const aborted = "This operation was aborted"
	for _, batch := range []struct {
		name     string
		commands []string
		// errors maps each compact id to its answer; "" accepts any error (Pi's second overlapping compact fails with its own TypeError).
		errors map[string]string
	}{
		{"compact then new_session", []string{`{"id":"1","type":"compact"}`, `{"id":"2","type":"new_session"}`}, map[string]string{"1": aborted}},
		{"new_session then compact", []string{`{"id":"2","type":"new_session"}`, `{"id":"1","type":"compact"}`}, map[string]string{"1": aborted}},
		{"compact then clone", []string{`{"id":"1","type":"compact"}`, `{"id":"2","type":"clone"}`}, map[string]string{"1": aborted}},
		{"compact then fork", []string{`{"id":"1","type":"compact"}`, `{"id":"2","type":"fork","entryId":"m3998"}`}, map[string]string{"1": aborted}},
		{"two compacts then new_session", []string{`{"id":"1","type":"compact"}`, `{"id":"3","type":"compact"}`, `{"id":"2","type":"new_session"}`}, map[string]string{"1": "", "3": ""}},
	} {
		for _, speed := range []struct{ name, env string }{{"ordinary summary", "TEST_FAUX_SLOW_COMPACTION="}, {"slow summary", "TEST_FAUX_SLOW_COMPACTION=1"}} {
			t.Run(batch.name+"/"+speed.name, func(t *testing.T) {
				dir := t.TempDir()
				current := filepath.Join(dir, "current.jsonl")
				if err := os.WriteFile(current, []byte(session), 0o600); err != nil {
					t.Fatal(err)
				}
				p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", speed.env, "PIG_HOME=" + dir}, "--offline", "--no-extensions", "--model", "test-faux/faux-1", "--session", current, "--session-dir", filepath.Join(dir, "sessions"))
				if _, err := io.WriteString(p.stdin, strings.Join(batch.commands, "\n")+"\n"); err != nil {
					t.Fatal(err)
				}
				responses := map[string]rpcRecord{}
				p.await("every response", func(record rpcRecord) bool {
					if record["type"] != "response" {
						return false
					}
					responses[responseID(t, record)] = record
					return len(responses) == len(batch.commands)
				})
				for id, want := range batch.errors {
					if got := responses[id]; got["success"] == true {
						t.Errorf("compact %s succeeded, want it aborted by the replacement", id)
					} else if want != "" && got["error"] != want {
						t.Errorf("compact %s answered %q, want %q", id, got["error"], want)
					}
				}
				if replacement := responses["2"]; replacement["success"] != true {
					t.Errorf("replacement failed: %v", replacement["error"])
				}
				p.closeAndWait("after the replacement")
				data, err := os.ReadFile(current)
				if err != nil {
					t.Fatal(err)
				}
				if n := strings.Count(strings.ReplaceAll(string(data), " ", ""), `"type":"compaction"`); n != 0 {
					t.Errorf("the outgoing Session file has %d compaction entries, want 0", n)
				}
			})
		}
	}
}

// A replacement that is refused or fails never reaches its teardown, so the compaction of its batch is not aborted (Pi 1.1.0, test-faux, a 1 MB Session:
// compact + fork of an unknown entry answers the fork with an error and the compact with success, and writes one compaction entry; the same for a
// switch_session to a directory, which answers EISDIR; a missing file would be created, not refused).
func TestRPCRefusedReplacementLeavesTheCompactionOfItsBatchAlone(t *testing.T) {
	session := largeRPCSession()
	for _, batch := range []struct{ name, replacement string }{
		{"fork of an unknown entry", `{"id":"2","type":"fork","entryId":"nope"}`},
		{"switch_session to a directory", `{"id":"2","type":"switch_session","sessionPath":"DIRECTORY"}`},
	} {
		t.Run(batch.name, func(t *testing.T) {
			dir := t.TempDir()
			current := filepath.Join(dir, "current.jsonl")
			if err := os.WriteFile(current, []byte(session), 0o600); err != nil {
				t.Fatal(err)
			}
			p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + dir}, "--offline", "--no-extensions", "--model", "test-faux/faux-1", "--session", current, "--session-dir", filepath.Join(dir, "sessions"))
			commands := `{"id":"1","type":"compact"}` + "\n" + strings.ReplaceAll(batch.replacement, `"DIRECTORY"`, jsonString(dir)) + "\n"
			if _, err := io.WriteString(p.stdin, commands); err != nil {
				t.Fatal(err)
			}
			responses := map[string]rpcRecord{}
			var order []string
			p.await("both responses", func(record rpcRecord) bool {
				if record["type"] == "compaction_start" {
					order = append(order, "compaction_start")
				}
				if record["type"] != "response" {
					return false
				}
				id := responseID(t, record)
				order = append(order, "response "+id)
				responses[id] = record
				return len(responses) == 2
			})
			// Pi writes compaction_start before the fork's error (Pi 1.1.0, 3 of 3 at both summary speeds): the compaction begins without waiting for anything.
			if batch.name == "fork of an unknown entry" && (len(order) == 0 || order[0] != "compaction_start") {
				t.Errorf("event order = %v, want compaction_start before the fork's error", order)
			}
			if responses["1"]["success"] != true {
				t.Errorf("compact answered %v, want success: the refused replacement does not abort it", responses["1"])
			}
			if responses["2"]["success"] == true {
				t.Errorf("replacement succeeded: %v", responses["2"])
			}
			p.closeAndWait("after both responses")
			data, err := os.ReadFile(current)
			if err != nil {
				t.Fatal(err)
			}
			if n := strings.Count(strings.ReplaceAll(string(data), " ", ""), `"type":"compaction"`); n != 1 {
				t.Errorf("the Session file has %d compaction entries, want 1", n)
			}
		})
	}
}

// An abort that reaches a compact before its request does not hide that the Session is too small to compact: Pi answers "Nothing to compact" for a
// compact and a new_session of one batch (Pi 1.1.0, 3 of 3).
func TestRPCReplacementKeepsTheNothingToCompactAnswer(t *testing.T) {
	source, err := os.ReadFile("../../test/parity/scenarios/rpc/testdata/session/sessions/source.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	current := filepath.Join(dir, "current.jsonl")
	if err := os.WriteFile(current, source, 0o600); err != nil {
		t.Fatal(err)
	}
	p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + dir}, "--offline", "--no-extensions", "--model", "test-faux/faux-1", "--session", current, "--session-dir", filepath.Join(dir, "sessions"))
	if _, err := io.WriteString(p.stdin, `{"id":"1","type":"compact"}`+"\n"+`{"id":"2","type":"new_session"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	got := map[string]rpcRecord{}
	p.await("both responses", func(record rpcRecord) bool {
		if record["type"] != "response" {
			return false
		}
		got[responseID(t, record)] = record
		return len(got) == 2
	})
	if got["1"]["error"] != "Nothing to compact (session too small)" {
		t.Errorf("compact answered %v, want \"Nothing to compact (session too small)\"", got["1"])
	}
}

// A switch_session whose file cannot be read fails before it waits for the replacement ahead of it, as a fork of an unknown entry does: Pi
// 1.1.0 answers `new_session, switch_session(<directory>), get_state` as 3, 2, 1 (EISDIR), where PiG answered 3, 1, 2.
func TestRPCFailedSwitchSessionAnswersBeforeTheReplacementAheadOfIt(t *testing.T) {
	source, err := os.ReadFile("../../test/parity/scenarios/rpc/testdata/session/sessions/source.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for run := range 3 {
		dir := t.TempDir()
		current := filepath.Join(dir, "current.jsonl")
		if err := os.WriteFile(current, source, 0o600); err != nil {
			t.Fatal(err)
		}
		p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + dir}, "--offline", "--no-extensions", "--model", "test-faux/faux-1", "--session", current, "--session-dir", filepath.Join(dir, "sessions"))
		commands := `{"id":"1","type":"new_session"}` + "\n" + `{"id":"2","type":"switch_session","sessionPath":` + jsonString(dir) + `}` + "\n" + `{"id":"3","type":"get_state"}` + "\n"
		if _, err := io.WriteString(p.stdin, commands); err != nil {
			t.Fatal(err)
		}
		var order []string
		var failure any
		p.await("three responses", func(record rpcRecord) bool {
			if record["type"] != "response" {
				return false
			}
			order = append(order, responseID(t, record))
			if record["id"] == "2" {
				failure = record["error"]
			}
			return len(order) == 3
		})
		if !slices.Equal(order, []string{"3", "2", "1"}) {
			t.Fatalf("run %d: answers = %v, want 3, 2, 1", run, order)
		}
		if s, _ := failure.(string); !strings.HasPrefix(s, "EISDIR") {
			t.Fatalf("switch_session answered %v, want EISDIR", failure)
		}
		p.closeAndWait("after the answers")
	}
}

// The early error of a replacement that waits behind another one follows the compaction_start of a compact that began at once: Pi 1.1.0 writes
// compaction_start, answers 3 (the switch_session's EISDIR), then 1 (the compact's abort) and 2 (new_session), 3 of 3 at both summary speeds.
func TestRPCEarlyReplacementErrorFollowsTheCompactionStart(t *testing.T) {
	session := largeRPCSession()
	for run := range 3 {
		dir := t.TempDir()
		current := filepath.Join(dir, "current.jsonl")
		if err := os.WriteFile(current, []byte(session), 0o600); err != nil {
			t.Fatal(err)
		}
		p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + dir}, "--offline", "--no-extensions", "--model", "test-faux/faux-1", "--session", current, "--session-dir", filepath.Join(dir, "sessions"))
		commands := `{"id":"1","type":"compact"}` + "\n" + `{"id":"2","type":"new_session"}` + "\n" + `{"id":"3","type":"switch_session","sessionPath":` + jsonString(dir) + `}` + "\n"
		if _, err := io.WriteString(p.stdin, commands); err != nil {
			t.Fatal(err)
		}
		var order []string
		p.await("three responses", func(record rpcRecord) bool {
			switch record["type"] {
			case "compaction_start":
				order = append(order, "compaction_start")
			case "response":
				order = append(order, responseID(t, record))
			}
			return len(order) >= 4
		})
		if want := []string{"compaction_start", "3", "1", "2"}; !slices.Equal(order, want) {
			t.Fatalf("run %d: order = %v, want %v", run, order, want)
		}
		p.closeAndWait("after the answers")
	}
}

// Pi's switch_session to a Session whose system message holds a content or toolsAdded member the loadout replay cannot read fails with the replay's TypeError (agent-session.ts:526 _restoreToolsFromTranscript, rpc-mode.ts error frame), and get_messages afterwards answers from the outgoing Session, which holds no message.
func TestRPCSwitchSessionFailsWithTheSystemReplayTypeError(t *testing.T) {
	cases := map[string]string{
		"content.jsonl": "content.filter is not a function",
		"tools.jsonl":   "number 5 is not iterable (cannot read property Symbol(Symbol.iterator))",
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile("../../test/parity/scenarios/rpc/testdata/system-replay-session/sessions/" + name)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			target := filepath.Join(dir, name)
			if err := os.WriteFile(target, source, 0o600); err != nil {
				t.Fatal(err)
			}
			p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + dir}, "--offline", "--no-extensions", "--model", "test-faux/faux-1", "--session-dir", filepath.Join(dir, "sessions"))
			p.sendJSON(map[string]any{"id": "switch", "type": "switch_session", "sessionPath": target})
			p.await("switch_session response", func(record rpcRecord) bool {
				if record["id"] != "switch" {
					return false
				}
				if record["type"] != "response" || record["command"] != "switch_session" || record["success"] != false || record["error"] != want {
					t.Errorf("switch_session answered %v, want a failed response with error %q", record, want)
				}
				return true
			})
			p.sendJSON(map[string]any{"id": "messages", "type": "get_messages"})
			p.await("get_messages response", func(record rpcRecord) bool {
				if record["id"] != "messages" {
					return false
				}
				data, _ := record["data"].(map[string]any)
				if messages, _ := data["messages"].([]any); record["success"] != true || len(messages) != 0 {
					t.Errorf("get_messages after the failed switch = %v, want the outgoing Session's no messages", record)
				}
				return true
			})
		})
	}
}

// responseID is a response's id. A response to a line PiG could not parse has none; the test fails with the record instead of
// panicking, which would stop every later test of the package.
func responseID(t *testing.T, record rpcRecord) string {
	t.Helper()
	id, ok := record["id"].(string)
	if !ok {
		t.Fatalf("response without an id: %v", record)
	}
	return id
}
