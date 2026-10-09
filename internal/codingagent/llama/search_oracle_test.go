package llama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type searchOracleModel struct {
	ID        string  `json:"id"`
	Downloads float64 `json:"downloads"`
}

type searchOracleOp struct {
	Key  string `json:"key,omitempty"`
	Wait int    `json:"wait,omitempty"`
}

type searchOracleScenario struct {
	Widths  []int               `json:"widths"`
	Delay   int                 `json:"delay"`
	Limit   int                 `json:"limit"`
	Catalog []searchOracleModel `json:"catalog"`
	Ops     []searchOracleOp    `json:"ops"`
}

type searchOracleResult struct {
	Frames  [][][]string `json:"frames"`
	Settled []any        `json:"settled"`
	Calls   []string     `json:"calls"`
}

func searchOracleScenarios() []searchOracleScenario {
	rng := rand.New(rand.NewPCG(4, 6))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	ids := []string{"ggml-org/gemma-3-4b-it-GGUF", "unsloth/Qwen3-8B-GGUF", "bartowski/Llama-3.1-8B-Instruct-GGUF", "TheBloke/Mistral-7B-v0.1-GGUF", "lmstudio-community/DeepSeek-R1-GGUF", "owner/a", "Owner/B", "x/y", "ggml-org/gemma-3-4b-it-GGUF", "a/very-long-repository-name-that-keeps-going-and-going-for-truncation", "日本/モデル", "microsoft/Phi-4-mini-instruct-gguf", "google/gemma-2-27b-GGUF", "qwen/qwen2.5-coder-32b-instruct-gguf", "ibm/granite-3.3-8b-GGUF", "meta/llama-guard-GGUF"}
	downloads := []float64{0, 1, 999, 1000, 1049, 1050, 9999, 10000, 99999, 100000, 999999, 1000000, 1049999, 9999999, 10000000, 12345678, 1e9}
	letters := strings.Split("abcdefghijklmnopqrstuvwxyz/: -_", "")
	keyPool := append(append([]string{}, letters...), "\x7f", "\x7f", "\x1b[B", "\x1b[A", "\r", "\x1b", "\x15", "\x1b[3~", "\x1b[D", "\x1b[C", "\x01", "\x05", "q", "g", "e", "r", "r", "m", "o")
	var scenarios []searchOracleScenario
	for range 600 {
		scenario := searchOracleScenario{Widths: []int{14, 30, 64}, Delay: []int{0, 0, 1, 60, 150, 1000}[rng.IntN(6)], Limit: []int{0, 1, 3, 8, 12, 30, 60}[rng.IntN(7)]}
		for range 8 + rng.IntN(32) {
			scenario.Catalog = append(scenario.Catalog, searchOracleModel{ID: pick(ids), Downloads: downloads[rng.IntN(len(downloads))]})
		}
		if rng.IntN(10) < 4 {
			// A directed sequence: a query that gets results, then navigation, then edits that shrink the filtered list and queries typed again (cache hits).
			word := pick([]string{"gem", "qwen", "llama", "ggml", "a", "gguf", "mi", "x/y", "ab"})
			for _, c := range word {
				scenario.Ops = append(scenario.Ops, searchOracleOp{Key: string(c)})
			}
			scenario.Ops = append(scenario.Ops, searchOracleOp{Wait: 2000})
			for range 1 + rng.IntN(7) {
				scenario.Ops = append(scenario.Ops, searchOracleOp{Key: pick([]string{"\x1b[A", "\x1b[B", "\x1b[B", "\x1b[A"})})
			}
			for range rng.IntN(4) {
				scenario.Ops = append(scenario.Ops, searchOracleOp{Key: pick([]string{"\x7f", "\x7f", "e", "m", "g"})})
			}
			if rng.IntN(2) == 0 {
				scenario.Ops = append(scenario.Ops, searchOracleOp{Wait: 2000}, searchOracleOp{Key: "\x1b[B"}, searchOracleOp{Key: "\x1b[A"})
			}
			if rng.IntN(2) == 0 {
				for range 6 {
					scenario.Ops = append(scenario.Ops, searchOracleOp{Key: "\x7f"})
				}
				for _, c := range word {
					scenario.Ops = append(scenario.Ops, searchOracleOp{Key: string(c)})
				}
				scenario.Ops = append(scenario.Ops, searchOracleOp{Wait: 1}, searchOracleOp{Key: "\x1b[B"})
			}
		}
		for range 3 + rng.IntN(10) {
			switch rng.IntN(9) {
			case 0:
				scenario.Ops = append(scenario.Ops, searchOracleOp{Wait: []int{1, 100, 300, 499, 500, 501, 559, 560, 561, 640, 650, 651, 700, 1000, 1500, 2000}[rng.IntN(16)]})
			case 1:
				for _, c := range pick([]string{"gem", "qwen", "err", "llama", "zz", "ab", "owner/a", "x/y:q4"}) {
					scenario.Ops = append(scenario.Ops, searchOracleOp{Key: string(c)})
				}
			default:
				scenario.Ops = append(scenario.Ops, searchOracleOp{Key: keyPool[rng.IntN(len(keyPool))]})
			}
		}
		if rng.IntN(2) == 0 {
			scenario.Ops = append(scenario.Ops, searchOracleOp{Wait: 2000})
		}
		scenarios = append(scenarios, scenario)
	}
	return scenarios
}

// manualClock is a virtual clock: a timer runs only when Advance reaches it, and Advance returns once every goroutine a timer started has finished or is asleep on the clock.
type manualClock struct {
	mu      sync.Mutex
	cond    *sync.Cond
	now     int
	seq     int
	timers  []*manualTimer
	running int
}

type manualTimer struct {
	ctx     context.Context
	clock   *manualClock
	due     int
	seq     int
	run     func()
	wake    chan struct{}
	stopped bool
	fired   bool
}

func newManualClock() *manualClock {
	c := &manualClock{}
	c.cond = sync.NewCond(&c.mu)
	return c
}

func (c *manualClock) schedule(d time.Duration, timer *manualTimer) {
	c.mu.Lock()
	timer.clock, timer.due, timer.seq = c, c.now+int(d/time.Millisecond), c.seq
	c.seq++
	c.timers = append(c.timers, timer)
	c.mu.Unlock()
}

// AfterFunc starts f on its own goroutine when the clock reaches d from now.
func (c *manualClock) AfterFunc(d time.Duration, f func()) debounceTimer {
	timer := &manualTimer{run: f}
	c.schedule(d, timer)
	return timer
}

// Sleep blocks the calling goroutine, which must have been started by a timer, until the clock reaches d from now or ctx is cancelled and Settle runs.
func (c *manualClock) Sleep(ctx context.Context, d time.Duration) {
	timer := &manualTimer{wake: make(chan struct{}), ctx: ctx}
	c.schedule(d, timer)
	c.mu.Lock()
	c.running--
	c.cond.Broadcast()
	c.mu.Unlock()
	<-timer.wake
}

// Stop reports whether it stopped the timer before it fired.
func (t *manualTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.fired || t.stopped {
		return false
	}
	t.stopped = true
	return true
}

// Settle wakes every sleeper whose context is cancelled and waits for the goroutines the clock started to finish or sleep again.
func (c *manualClock) Settle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wakeCancelled()
	c.quiesce()
}

func (c *manualClock) wakeCancelled() {
	for _, timer := range c.timers {
		if timer.wake != nil && !timer.fired && !timer.stopped && timer.ctx != nil && timer.ctx.Err() != nil {
			timer.fired = true
			c.running++
			close(timer.wake)
		}
	}
}

func (c *manualClock) quiesce() {
	for c.running > 0 {
		c.cond.Wait()
	}
}

// Advance runs every timer due within ms, in due-time order and creation order within one instant.
func (c *manualClock) Advance(ms int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	target := c.now + ms
	for {
		c.wakeCancelled()
		c.quiesce()
		var next *manualTimer
		for _, timer := range c.timers {
			if timer.stopped || timer.fired || timer.due > target {
				continue
			}
			if next == nil || timer.due < next.due || (timer.due == next.due && timer.seq < next.seq) {
				next = timer
			}
		}
		if next == nil {
			break
		}
		c.now, next.fired = next.due, true
		c.running++
		if next.wake != nil {
			close(next.wake)
			continue
		}
		go func() {
			next.run()
			c.mu.Lock()
			c.running--
			c.cond.Broadcast()
			c.mu.Unlock()
		}()
	}
	c.now = target
}

func runSearchOracleWithPig(t testing.TB, scenario searchOracleScenario) searchOracleResult {
	clock := newManualClock()
	view := NewLlamaView(func() {})
	view.afterFunc = clock.AfterFunc
	var mu sync.Mutex
	var calls []string
	search := func(ctx context.Context, query string) ([]HuggingFaceModel, error) {
		clock.Sleep(ctx, time.Duration(scenario.Delay)*time.Millisecond)
		mu.Lock()
		calls = append(calls, fmt.Sprintf("%s|aborted=%v", query, ctx.Err() != nil))
		mu.Unlock()
		if strings.Contains(query, "err") {
			return nil, fmt.Errorf("search failed: %s", query)
		}
		var out []HuggingFaceModel
		for _, model := range scenario.Catalog {
			if len(query) > 2 || strings.Contains(strings.ToLower(model.ID), strings.ToLower(query[:1])) {
				out = append(out, HuggingFaceModel(model))
			}
		}
		if len(out) > scenario.Limit {
			out = out[:scenario.Limit]
		}
		return out, nil
	}
	var outcome any
	var done bool
	var outcomeMu sync.Mutex
	finished := make(chan struct{})
	go func() {
		model, ok := view.SearchModels(search)
		outcomeMu.Lock()
		done = true
		if ok {
			outcome = model
		}
		outcomeMu.Unlock()
		close(finished)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		view.mu.Lock()
		ready := view.inputHandler != nil
		view.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Error("search never mounted")
			return searchOracleResult{}
		}
		time.Sleep(time.Microsecond * 200)
	}
	var result searchOracleResult
	isDone := func() bool {
		outcomeMu.Lock()
		defer outcomeMu.Unlock()
		return done
	}
	snap := func() {
		frames := make([][]string, len(scenario.Widths))
		for i, width := range scenario.Widths {
			frames[i] = view.Render(width)
			if frames[i] == nil {
				frames[i] = []string{}
			}
		}
		result.Frames = append(result.Frames, frames)
		outcomeMu.Lock()
		if done {
			result.Settled = append(result.Settled, outcome)
		} else {
			result.Settled = append(result.Settled, nil)
		}
		outcomeMu.Unlock()
	}
	// A key that can close the search settles the flow on its own goroutine; give it time to return, as Pi's microtask flush does.
	settle := func(key string) {
		if key == "\r" || key == "\x1b" || key == "\x03" {
			select {
			case <-finished:
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	snap()
	for _, op := range scenario.Ops {
		if op.Wait > 0 {
			clock.Advance(op.Wait)
			snap()
			continue
		}
		if !isDone() {
			view.HandleInput(op.Key)
			clock.Settle() // a key that supersedes or closes a search cancels its context
			settle(op.Key)
		}
		snap()
	}
	if !isDone() {
		view.HandleInput("\x1b")
		clock.Settle()
	}
	// Pi never advances its clock again, so what it lists is what finished by now (a search aborted by the close finishes at once).
	mu.Lock()
	result.Calls = append([]string{}, calls...)
	mu.Unlock()
	go clock.Advance(1 << 20) // let a search that ignores its cancellation finish, so closing can return
	<-finished
	return result
}

// Keys sent after the search closed are not part of the comparison: Pi schedules a search for them that nobody waits for, and Pig, whose SearchModels waits for its pending search, must not.
//
// HuggingFaceSearch (extensions/llama/ui.ts) against pinned Pi over 600 seeded scenarios on a virtual clock: typing, deleting and cursor keys edit the query, a query
// of two characters starts a search after the 500 ms debounce (cached by lower-cased query, aborted by a newer one, failing with its message), results filter by fuzzy match,
// navigation wraps, enter takes an owner/repository query as typed or the selected result, escape closes; every frame's bytes, what each step settled and the searches that
// completed (with whether each had been aborted) must agree.
func TestLlamaSearchMatchesPi(t *testing.T) {
	scenarios := searchOracleScenarios()
	tui.SetTheme("dark")
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/llama_search.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []searchOracleResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	got := make([]searchOracleResult, len(scenarios))
	for i, scenario := range scenarios {
		got[i] = runSearchOracleWithPig(t, scenario)
	}
	failures := 0
	for i, scenario := range scenarios {

		if reflect.DeepEqual(got[i].Frames, expected[i].Frames) && reflect.DeepEqual(got[i].Settled, expected[i].Settled) && reflect.DeepEqual(got[i].Calls, expected[i].Calls) {
			continue
		}
		if failures++; failures > 4 {
			continue
		}
		report := fmt.Sprintf("scenario %d: %d frames, Pi %d; calls Pig %q Pi %q", i, len(got[i].Frames), len(expected[i].Frames), got[i].Calls, expected[i].Calls)
		for s := 0; s < min(len(got[i].Frames), len(expected[i].Frames)); s++ {
			if !reflect.DeepEqual(got[i].Settled[s], expected[i].Settled[s]) {
				report += fmt.Sprintf("\n  step %d settled Pig %v Pi %v", s, got[i].Settled[s], expected[i].Settled[s])
				break
			}
			if !reflect.DeepEqual(got[i].Frames[s], expected[i].Frames[s]) {
				for w := range scenario.Widths {
					if !reflect.DeepEqual(got[i].Frames[s][w], expected[i].Frames[s][w]) {
						report += fmt.Sprintf("\n  step %d width %d\n  Pig %q\n  Pi  %q", s, scenario.Widths[w], got[i].Frames[s][w], expected[i].Frames[s][w])
						break
					}
				}
				break
			}
		}
		ops, _ := json.Marshal(scenario.Ops)
		t.Errorf("%s\n  ops %s", report, ops)
	}
	if failures > 4 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, len(scenarios))
	}
}

// TestLlamaSearchProbeDump prints the scenarios for the Pi side of the llama-search parity scenario.
func TestLlamaSearchProbeDump(t *testing.T) {
	line, err := json.Marshal(searchOracleScenarios())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("llamasearch-probes:%s\n", line)
}

// TestLlamaSearchParity prints Pig's frames, settlements and completed searches, one JSON line per scenario, for the llama-search parity scenario.
func TestLlamaSearchParity(t *testing.T) {
	tui.SetTheme("dark")
	for _, scenario := range searchOracleScenarios() {
		var line bytes.Buffer
		encoder := json.NewEncoder(&line)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(runSearchOracleWithPig(t, scenario)); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("llamasearch-observation:%s", line.String())
	}
}

// A key that reaches the search after it closed (the view keeps routing input to it until the flow shows something else) must not start a search timer:
// SearchModels returns after waiting for its pending search, so a timer added after that would run unowned (and Add racing that Wait panics).
func TestLlamaSearchStartsNoSearchAfterClose(t *testing.T) {
	clock := newManualClock()
	view := NewLlamaView(func() {})
	view.afterFunc = clock.AfterFunc
	finished := make(chan struct{})
	go func() {
		view.SearchModels(func(context.Context, string) ([]HuggingFaceModel, error) { return nil, nil })
		close(finished)
	}()
	for {
		view.mu.Lock()
		ready := view.inputHandler != nil
		view.mu.Unlock()
		if ready {
			break
		}
		time.Sleep(time.Millisecond)
	}
	view.HandleInput("\x1b")
	<-finished
	view.HandleInput("a")
	view.HandleInput("b")
	clock.mu.Lock()
	defer clock.mu.Unlock()
	if len(clock.timers) != 0 {
		t.Fatalf("typing after the search closed started %d timers", len(clock.timers))
	}
}
