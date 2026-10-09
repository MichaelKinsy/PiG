package cli

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Pi rpc-mode.ts:786 awaits handleCommand even when its inner await already resolved. It cannot resume inside jsonl.ts:onData's synchronous line loop.
func TestRPCResponseTurnDefersAlreadyFulfilledAwait(t *testing.T) {
	var got []any
	write := func(response any) { got = append(got, response) }
	turn := &rpcResponseTurn{write: write}
	turn.begin()
	turn.complete("cycle") // The async operation has already completed, not merely been started.
	if len(got) != 0 {
		t.Fatalf("awaited response escaped the input turn: %v", got)
	}
	write("abort-retry")
	turn.end()
	turn.complete("later")
	if want := []any{"abort-retry", "cycle", "later"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("responses=%v want=%v", got, want)
	}
}

func TestRPCResponseTurnEvaluatesContinuationAfterAdmission(t *testing.T) {
	value := 1
	var got []any
	turn := &rpcResponseTurn{write: func(v any) { got = append(got, v) }}
	turn.begin()
	turn.after(func() { turn.complete(value) })
	value = 2
	turn.end()
	if !reflect.DeepEqual(got, []any{2}) {
		t.Fatalf("continuation observed pre-await state: %v", got)
	}
}

func TestRPCResponseTurnProtectsLaterInputAndDoesNotStrandCompletions(t *testing.T) {
	var got []any
	executor := &rpcResponseTurn{write: func(v any) { got = append(got, v) }}
	executor.begin()
	executor.end()
	executor.begin()
	var work sync.WaitGroup
	for i := range 100 {
		work.Go(func() { executor.complete(i) })
	}
	work.Wait() // Completion producers must not wait for this input callback to finish.
	if len(got) != 0 {
		t.Fatalf("completion ran inside later input callback: %v", got)
	}
	executor.end()
	if len(got) != 100 {
		t.Fatalf("lost completed operations: %d", len(got))
	}
	for i := range 100 {
		work.Go(func() { executor.complete(i) })
	}
	for range 100 {
		executor.begin()
		executor.end()
	}
	work.Wait()
	executor.begin()
	executor.end()
	if len(got) != 200 {
		t.Fatalf("stranded completion at idle handoff: %d", len(got))
	}
}

func TestRPCCycleAndAbortRetrySameInputCallback(t *testing.T) {
	home := t.TempDir()
	p := startRPCProcessAt(t, t.TempDir(), []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1"}, "--offline", "--no-extensions", "--no-session", "--model", "test-faux/faux-1")
	// One pipe write is one small, complete input batch. Separate writes do not define a Node data-callback boundary.
	p.send(strings.Join([]string{
		`{"id":"models","type":"get_available_models"}`,
		`{"id":"levels","type":"get_available_thinking_levels"}`,
		`{"id":"cycle","type":"cycle_model"}`,
		`{"id":"abort-retry","type":"abort_retry"}`,
	}, "\n"))
	var ids []any
	p.await("all batch responses", func(record rpcRecord) bool {
		if record["type"] == "response" {
			if record["success"] != true {
				t.Fatal(record)
			}
			ids = append(ids, record["id"])
			if record["id"] == "cycle" && record["data"] != nil {
				t.Fatalf("single-model cycle=%#v", record)
			}
		}
		return len(ids) == 4
	})
	if want := []any{"models", "levels", "abort-retry", "cycle"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("response order=%v want=%v", ids, want)
	}
	p.closeAndWait("after the input batch")
}

func TestRPCCycleResponseDoesNotWaitForFutureInput(t *testing.T) {
	home := t.TempDir()
	p := startRPCProcessAt(t, t.TempDir(), []string{"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1"}, "--offline", "--no-extensions", "--no-session", "--model", "test-faux/faux-1")
	p.send(`{"id":"cycle","type":"cycle_model"}`)
	p.await("cycle before any future input", func(r rpcRecord) bool { return isSuccessResponse(r, "cycle") })
	p.send(`{"id":"abort-retry","type":"abort_retry"}`)
	p.await("later abort_retry", func(r rpcRecord) bool { return isSuccessResponse(r, "abort-retry") })
	p.closeAndWait("after separate input turns")
}

func TestRPCCycleResultSamplesThinkingAfterAwait(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"models.json", "settings.json"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "test/parity", "scenarios", "rpc", "testdata", "cycle-thinking", "agent", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := startRPCProcessAt(t, t.TempDir(), []string{"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + agentDir}, "--offline", "--no-extensions", "--no-session", "--model", "fixture/model-one")
	p.send("{\"id\":\"one\",\"type\":\"cycle_model\"}\n{\"id\":\"two\",\"type\":\"cycle_model\"}\n{\"id\":\"abort-retry\",\"type\":\"abort_retry\"}")
	var ids []any
	seenThinking := false
	p.await("both cycle continuations", func(r rpcRecord) bool {
		if r["type"] == "thinking_level_changed" && r["level"] == "high" {
			seenThinking = true
		}
		if r["type"] != "response" {
			return false
		}
		if !seenThinking {
			t.Fatalf("response preceded synchronous thinking event: %v", r)
		}
		ids = append(ids, r["id"])
		if r["command"] == "cycle_model" {
			data, _ := r["data"].(map[string]any)
			if data["thinkingLevel"] != "high" {
				t.Fatalf("response sampled thinking before await: %v", r)
			}
		}
		return len(ids) == 3
	})
	if !reflect.DeepEqual(ids, []any{"abort-retry", "one", "two"}) {
		t.Fatalf("response order=%v", ids)
	}
	p.closeAndWait("after model cycle state sampling")
}

func BenchmarkRPCResponseTurn(b *testing.B) {
	write := func(v any) { writeJSONLine(io.Discard, v) }
	b.ReportAllocs()
	for b.Loop() {
		turn := &rpcResponseTurn{write: write}
		turn.begin()
		turn.complete(rpcSuccessNull(rpcStringID("cycle"), "cycle_model"))
		write(rpcSuccess(rpcStringID("abort-retry"), "abort_retry", nil))
		turn.end()
	}
}

// Pi agent-session.ts:2178-2238 applies each cycle's model before awaiting model_select. One blocked listener must not serialize another cycle's mutation or completion.
func TestRPCSecondCycleFinishesWhileFirstListenerWaits(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "agent")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	models := `{"providers":{"fixture":{"baseUrl":"http://127.0.0.1:1/v1","apiKey":"fixture-key","api":"openai-completions","models":[{"id":"model-one","name":"One"},{"id":"model-two","name":"Two"},{"id":"model-three","name":"Three"}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(models), 0600); err != nil {
		t.Fatal(err)
	}
	p := startRPCUIFixture(t, []string{"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + dir}, "--offline", "--no-session", "--model", "fixture/model-one", "--models", "fixture/model-one,fixture/model-two,fixture/model-three")
	p.send(`{"id":"arm","type":"prompt","message":"/block-model-select"}`)
	p.await("armed", func(r rpcRecord) bool { return isSuccessResponse(r, "arm") })
	p.send(`{"id":"one","type":"cycle_model"}`)
	var requestID string
	p.await("first model_select waiting", func(r rpcRecord) bool {
		if isUISelect(r, "Model selected") {
			requestID, _ = r["id"].(string)
		}
		return requestID != ""
	})
	p.send("{\"id\":\"two\",\"type\":\"cycle_model\"}\n{\"id\":\"state\",\"type\":\"get_state\"}")
	var ids []any
	p.await("second cycle and state", func(r rpcRecord) bool {
		if r["type"] != "response" {
			return false
		}
		if r["id"] == "one" {
			t.Fatalf("blocked first cycle completed early: %v", r)
		}
		ids = append(ids, r["id"])
		data, _ := r["data"].(map[string]any)
		model, _ := data["model"].(map[string]any)
		if r["success"] != true || model["id"] != "model-three" {
			t.Fatalf("next model not applied: %v", r)
		}
		return len(ids) == 2
	})
	if !reflect.DeepEqual(ids, []any{"state", "two"}) {
		t.Fatalf("responses=%v", ids)
	}
	p.sendJSON(map[string]any{"type": "extension_ui_response", "id": requestID, "value": "continue"})
	p.await("first cycle completes after release", func(r rpcRecord) bool {
		if r["type"] != "response" || r["id"] != "one" {
			return false
		}
		data, _ := r["data"].(map[string]any)
		model, _ := data["model"].(map[string]any)
		if r["success"] != true || model["id"] != "model-two" {
			t.Fatal(r)
		}
		return true
	})
	p.closeAndWait("after model_select release")
}
