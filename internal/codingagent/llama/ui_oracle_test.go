package llama

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type llamaOracleModel struct {
	ID     string   `json:"id"`
	Status string   `json:"status"`
	Args   []string `json:"args,omitempty"`
	Meta   *struct {
		NCtx      *float64 `json:"n_ctx,omitempty"`
		NCtxTrain *float64 `json:"n_ctx_train,omitempty"`
	} `json:"meta,omitempty"`
}

type llamaOracleState struct {
	Title   string   `json:"title"`
	Model   string   `json:"model"`
	Message string   `json:"message"`
	Ratio   *float64 `json:"ratio,omitempty"`
	Detail  string   `json:"detail,omitempty"`
}

type llamaOracleOp struct {
	Kind    string             `json:"kind"`
	URL     string             `json:"url"`
	Models  []llamaOracleModel `json:"models"`
	Title   string             `json:"title"`
	Options []string           `json:"options,omitempty"`
	Message string             `json:"message"`
	States  []llamaOracleState `json:"states,omitempty"`
	Keys    []string           `json:"keys"`
}

type llamaOracleScenario struct {
	Theme  string        `json:"theme"`
	Widths []int         `json:"widths"`
	Op     llamaOracleOp `json:"op"`
}

type llamaOracleResult struct {
	Frames  [][][]string `json:"frames"`
	Settled []any        `json:"settled"`
}

func llamaOracleScenarios() []llamaOracleScenario {
	rng := rand.New(rand.NewPCG(3, 8))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	ids := []string{"Qwen3-8B", "qwen3-8b", "gemma-2-9b", "Gemma-2-27B", "llama-3.1-8b-instruct", "Llama-3.1-70B", "mistral_7b", "ünïcode-model", "z", "a", "A", "Z", "10", "9", "model.gguf", "owner/repo:Q4_K_M", "ggml-org/gemma-3-4b-it-GGUF:Q4_K_M-very-long-quant-name-for-truncation", "日本語モデル", "model 2", "model-2", "model_2", "ä", "e", "é", "f", "résumé", "resume", "Résumé", "co-op", "coop", "a-b", "ab", "a_b", "Ab", "aB"}
	statuses := []string{"loaded", "unloaded", "loading", "downloading", "sleeping"}
	argLists := [][]string{nil, {"--ctx-size", "8192"}, {"-c", "32000"}, {"-ctx", "0"}, {"--ctx-size", "abc"}, {"-c", "1500"}, {"-c", "999"}, {"--ctx-size"}, {"-c", "-5"}, {"-c", "1e4"}, {"--x", "-c", "2048"}, {"-c", "", "-c", "4096"}, {"-ctx", "4096", "-c", "8192"}, {"-c", "1499.5"}, {"-c", "Infinity"}, {"-c", " 4096 "}}
	numbers := []float64{0, 1, 999, 1000, 1499, 1500, 1501, 2048, 4096, 32768, 128000, 262144, 2500}
	keys := []string{"\r", "\r", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[A", "\x1b", "\x1b[6~", "\x1b[5~", "\x1b[H", "\x1b[F", "a", "/", "\t", "\x03", "j", "k", "\n", " "}
	pickKeys := func(max int) []string {
		out := make([]string, rng.IntN(max+1))
		for i := range out {
			out[i] = pick(keys)
		}
		return out
	}
	texts := []string{"Load model", "Delete?", "Really stop the download?", "line one\nline two", "日本語 title", "x", "", "a very long title that should be cut when the terminal is narrow", "multi\n\nblank"}
	var scenarios []llamaOracleScenario
	for range 700 {
		scenario := llamaOracleScenario{Theme: pick([]string{"dark", "light"}), Widths: []int{12, 24, 44, 80}}
		op := llamaOracleOp{}
		switch rng.IntN(6) {
		case 0, 1:
			op.Kind, op.Models, op.URL = "models", []llamaOracleModel{}, pick([]string{"http://127.0.0.1:8080", "http://localhost:11434/v1", "https://a-very-long-host-name.example.com:8443/some/long/path/segment"})
			for range rng.IntN(9) {
				model := llamaOracleModel{ID: pick(ids), Status: pick(statuses), Args: argLists[rng.IntN(len(argLists))]}
				if rng.IntN(2) == 0 {
					model.Meta = &struct {
						NCtx      *float64 `json:"n_ctx,omitempty"`
						NCtxTrain *float64 `json:"n_ctx_train,omitempty"`
					}{}
					if rng.IntN(2) == 0 {
						n := numbers[rng.IntN(len(numbers))]
						model.Meta.NCtx = &n
					}
					if rng.IntN(2) == 0 {
						n := numbers[rng.IntN(len(numbers))]
						model.Meta.NCtxTrain = &n
					}
				}
				op.Models = append(op.Models, model)
			}
		case 2:
			op.Kind, op.Title = "select", pick(texts)
			for range 1 + rng.IntN(15) {
				op.Options = append(op.Options, pick(ids))
			}
		case 3:
			op.Kind, op.Title, op.Message = "confirm", pick(texts), pick(texts)
		case 4:
			op.Kind, op.URL, op.Message = "connectionError", "http://127.0.0.1:8080", pick(texts)
		default:
			if rng.IntN(2) == 0 {
				op.Kind, op.Title, op.Message = "status", pick(texts), pick(texts)
			} else {
				op.Kind = "progress"
				for range 1 + rng.IntN(4) {
					state := llamaOracleState{Title: pick(texts), Model: pick(ids), Message: pick(texts), Detail: pick([]string{"", "", "3.2 GB / 8 GB", "日本語"})}
					if rng.IntN(3) != 0 {
						ratio := []float64{0, 0.5, 1, -0.2, 1.7, 0.333, 0.995, 0.004, 0.125, 0.875, 0.0125, 0.0375, 0.4999}[rng.IntN(13)]
						state.Ratio = &ratio
					}
					op.States = append(op.States, state)
				}
			}
		}
		op.Keys = pickKeys(7)
		scenario.Op = op
		scenarios = append(scenarios, scenario)
	}
	return scenarios
}

// runLlamaOracleWithPig drives one scenario against Pig's view, taking each step's expected settlement from Pi's record only to know whether to wait for the
// blocking flow to return; what settled and what the frames show are compared, not assumed.
func runLlamaOracleWithPig(t testing.TB, scenario llamaOracleScenario, expectedSettled []any) llamaOracleResult {
	t.Helper()
	tui.SetTheme(scenario.Theme)
	view := NewLlamaView(func() {})
	frames := func() [][]string {
		out := make([][]string, len(scenario.Widths))
		for i, width := range scenario.Widths {
			out[i] = view.Render(width)
		}
		return out
	}
	var result llamaOracleResult
	step := 0
	record := func(settled any) {
		result.Frames = append(result.Frames, frames())
		result.Settled = append(result.Settled, settled)
		step++
	}
	record(nil)
	var mu sync.Mutex
	var outcome any
	var progressStop <-chan struct{}
	done := make(chan struct{})
	op := scenario.Op
	waitMounted := func() {
		deadline := time.Now().Add(5 * time.Second)
		for {
			view.mu.Lock()
			ready := view.inputHandler != nil
			view.mu.Unlock()
			if ready {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("flow %s never mounted its list", op.Kind)
			}
			time.Sleep(time.Millisecond)
		}
	}
	finish := func(value any) {
		mu.Lock()
		outcome = value
		mu.Unlock()
		close(done)
	}
	switch op.Kind {
	case "models":
		models := make([]LlamaModelInfo, len(op.Models))
		for i, m := range op.Models {
			models[i] = LlamaModelInfo{ID: m.ID, Status: LlamaModelInfoStatus{Value: LlamaModelStatus(m.Status), Args: m.Args}}
			if m.Meta != nil {
				models[i].Meta = &LlamaModelMeta{NCtx: m.Meta.NCtx, NCtxTrain: m.Meta.NCtxTrain}
			}
		}
		go func() {
			action := view.ShowModels(op.URL, models)
			model := any(nil)
			if action.Type == LlamaManagerActionModel {
				model = action.Model.ID
			}
			finish(map[string]any{"type": string(action.Type), "model": model})
		}()
		waitMounted()
	case "select":
		go func() {
			value, ok := view.Select(op.Title, op.Options)
			if !ok {
				finish(nil)
				return
			}
			finish(value)
		}()
		waitMounted()
	case "confirm":
		go func() { finish(view.Confirm(op.Title, op.Message)) }()
		waitMounted()
	case "connectionError":
		go func() { finish(view.ConnectionError(op.URL, op.Message)) }()
		waitMounted()
	case "status":
		view.ShowStatus(op.Title, op.Message)
	case "progress":
		toState := func(s llamaOracleState) ProgressState {
			return ProgressState{LlamaProgress: LlamaProgress{Message: s.Message, Ratio: s.Ratio, Detail: s.Detail}, Title: s.Title, Model: s.Model}
		}
		view.UpdateProgress(toState(op.States[0])) // ignored: no progress is showing yet
		progressStop = view.Progress(toState(op.States[0]))
		for _, state := range op.States[1:] {
			record(nil)
			view.UpdateProgress(toState(state))
		}
	}
	settledNow := func(expected any) any {
		if progressStop != nil {
			select {
			case <-progressStop:
				return "stopped"
			default:
				return nil
			}
		}
		if op.Kind == "status" {
			return nil
		}
		wait := 2 * time.Millisecond
		if expected != nil {
			wait = 5 * time.Second
		} else if expectedSettled == nil {
			wait = 10 * time.Millisecond // no Pi record to say whether this step settles: give the flow time to return
		}
		select {
		case <-done:
		case <-time.After(wait):
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		return outcome
	}
	expectedAt := func(i int) any {
		if i < len(expectedSettled) {
			return expectedSettled[i]
		}
		return nil
	}
	record(settledNow(expectedAt(step)))
	for _, key := range op.Keys {
		view.HandleInput(key)
		record(settledNow(expectedAt(step)))
	}
	return result
}

// LlamaView (extensions/llama/ui.ts) against pinned Pi over 700 seeded scenarios, at four widths, for every flow that does not search: the loading view, the model list (loaded
// models first, then locale order, context label from n_ctx, n_ctx_train or the launch arguments), select, confirm, connection error, status and the progress bar with
// clamped ratio, rounded percentage and detail, each with a seeded key sequence; every frame's bytes and what each step settled must agree.
func TestLlamaViewMatchesPi(t *testing.T) {
	scenarios := llamaOracleScenarios()
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/llama_ui.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []llamaOracleResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, scenario := range scenarios {
		got := runLlamaOracleWithPig(t, scenario, expected[i].Settled)
		if reflect.DeepEqual(normalizeLlamaResult(got), normalizeLlamaResult(expected[i])) {
			continue
		}
		if failures++; failures > 4 {
			continue
		}
		report := fmt.Sprintf("scenario %d: %d frames, Pi %d", i, len(got.Frames), len(expected[i].Frames))
		for s := 0; s < min(len(got.Frames), len(expected[i].Frames)); s++ {
			if !reflect.DeepEqual(got.Settled[s], expected[i].Settled[s]) {
				report += fmt.Sprintf("\n  step %d settled Pig %v Pi %v", s, got.Settled[s], expected[i].Settled[s])
				break
			}
			if !reflect.DeepEqual(got.Frames[s], expected[i].Frames[s]) {
				for w := range scenario.Widths {
					if !reflect.DeepEqual(got.Frames[s][w], expected[i].Frames[s][w]) {
						report += fmt.Sprintf("\n  step %d width %d\n  Pig %q\n  Pi  %q", s, scenario.Widths[w], got.Frames[s][w], expected[i].Frames[s][w])
						break
					}
				}
				break
			}
		}
		op, _ := json.Marshal(scenario.Op)
		t.Errorf("%s\n  op %s", report, op)
	}
	if failures > 4 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, len(scenarios))
	}
}

// normalizeLlamaResult turns a nil frame list into an empty one, as JSON null and [] both mean no lines.
func normalizeLlamaResult(r llamaOracleResult) llamaOracleResult {
	for _, step := range r.Frames {
		for w := range step {
			if step[w] == nil {
				step[w] = []string{}
			}
		}
	}
	return r
}

// TestLlamaViewProbeDump prints the scenarios for the Pi side of the llama-view parity scenario.
func TestLlamaViewProbeDump(t *testing.T) {
	line, err := json.Marshal(llamaOracleScenarios())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("llamaview-probes:%s\n", line)
}

// TestLlamaViewParity prints Pig's frames and settlements, one JSON line per scenario, for the llama-view parity scenario.
func TestLlamaViewParity(t *testing.T) {
	for _, scenario := range llamaOracleScenarios() {
		result := runLlamaOracleWithPig(t, scenario, nil)
		var line bytes.Buffer
		encoder := json.NewEncoder(&line)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(normalizeLlamaResult(result)); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("llamaview-observation:%s", line.String())
	}
}
