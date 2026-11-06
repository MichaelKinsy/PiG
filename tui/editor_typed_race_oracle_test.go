package tui_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

// The probes of editor_typed_race.mjs type keys without letting them settle, as a fast typist or a slow provider does: Pi's editor.ts touches no request when a key triggers nothing, so a request still waiting out its 20 ms
// debounce, or queued behind one that is unfinished, starts against the text current then and asks the provider about it. The log is what the provider was asked, in order, and the editor once everything settled.

// A raceOp is one terminal read of Keys (a key, or several that arrive together), or a pause of Sleep milliseconds.
type raceOp struct {
	Keys  []string `json:"keys,omitempty"`
	Sleep int      `json:"sleep,omitempty"`
}

// Slow is how the provider answers: raceImmediate at once, raceAborts after raceSlow unless the request is cancelled first, raceIgnoresAbort after raceSlow whatever happens to the request.
type raceProbe struct {
	Slow int      `json:"slow"`
	Ops  []raceOp `json:"ops"`
}

type raceLog struct {
	Queries []string `json:"queries"`
	Text    string   `json:"text"`
	Cursor  struct{ Line, Col int }
	Rows    []string `json:"rows"`
}

const (
	raceImmediate    = 0
	raceAborts       = 1
	raceIgnoresAbort = 2
	raceSlow         = 40 * time.Millisecond
)

var raceTokens = []string{"alpha", "alpine", "beta", "alé", "al/dir", "résumé"}

// raceAnswer is the answer testdata/editor_typed_race.mjs gives for the text before the cursor.
func raceAnswer(before string) *tui.AutocompleteSuggestions {
	if match := typedAutocompleteCommandRe.FindStringSubmatch(before); match != nil {
		if items := typedAutocompleteMatching(typedAutocompleteCommands, match[1]); len(items) > 0 {
			return &tui.AutocompleteSuggestions{Items: items, Prefix: before}
		}
		return nil
	}
	if match := typedAutocompleteArgumentRe.FindStringSubmatch(before); match != nil {
		if items := typedAutocompleteMatching(typedAutocompleteSkills, match[1]); len(items) > 0 {
			return &tui.AutocompleteSuggestions{Items: items, Prefix: match[1]}
		}
		return nil
	}
	if match := typedAutocompleteTokenRe.FindStringSubmatch(before); match != nil {
		if items := typedAutocompleteMatching(raceTokens, match[2]); len(items) > 0 {
			return &tui.AutocompleteSuggestions{Items: items, Prefix: match[1] + match[2]}
		}
	}
	return nil
}

// raceQueries records the text before the cursor of every query a provider receives.
type raceQueries struct {
	mu   sync.Mutex
	list []string
}

func (q *raceQueries) add(before string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.list = append(q.list, before)
}

func (q *raceQueries) all() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]string{}, q.list...)
}

func raceSleep(ctx context.Context, slow int) error {
	if slow == raceIgnoresAbort {
		time.Sleep(raceSlow)
		return nil
	}
	select {
	case <-time.After(raceSlow):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// raceNativeProvider is the local provider: its answer is synchronous.
type raceNativeProvider struct{ queries *raceQueries }

func (raceNativeProvider) TriggerCharacters() []string { return []string{"$"} }

func (p raceNativeProvider) GetSuggestions(_ context.Context, lines []string, row, col int, _ tui.AutocompleteSuggestionOptions) *tui.AutocompleteSuggestions {
	before := lines[row][:col]
	p.queries.add(before)
	return raceAnswer(before)
}

func (raceNativeProvider) ApplyCompletion(lines []string, row, col int, _ tui.AutocompleteItem, _ string) ([]string, int, int) {
	return lines, row, col
}

// raceSlowNativeProvider awaits its answer, as a command's awaited argument completer does: the query is recorded when the request starts, the answer comes later from a worker.
type raceSlowNativeProvider struct {
	raceNativeProvider
	slow int
}

func (p raceSlowNativeProvider) SuggestionTask(lines []string, row, col int, _ bool) (string, func(context.Context) ([]tui.AutocompleteItem, error), bool) {
	before := lines[row][:col]
	p.queries.add(before)
	answer := raceAnswer(before)
	prefix := ""
	if answer != nil {
		prefix = answer.Prefix
	}
	return prefix, func(ctx context.Context) ([]tui.AutocompleteItem, error) {
		if err := raceSleep(ctx, p.slow); err != nil {
			return nil, err
		}
		if answer == nil {
			return nil, nil
		}
		return answer.Items, nil
	}, true
}

// raceLegacyProvider answers only @ tokens, and only through the deferred file search of an AsyncFileSearcher: the query is recorded when the search runs.
type raceLegacyProvider struct {
	queries *raceQueries
	slow    int
}

func (raceLegacyProvider) GetSuggestions(context.Context, []string, int, int, tui.AutocompleteSuggestionOptions) *tui.AutocompleteSuggestions {
	return nil
}

func (raceLegacyProvider) ApplyCompletion(lines []string, row, col int, _ tui.AutocompleteItem, _ string) ([]string, int, int) {
	return lines, row, col
}

func (p raceLegacyProvider) FileSearchTask(lines []string, row, col int) (string, func(context.Context) []tui.AutocompleteItem, bool) {
	before := lines[row][:col]
	match := typedAutocompleteTokenRe.FindStringSubmatch(before)
	if match == nil || match[1] != "@" {
		return "", nil, false
	}
	return match[1] + match[2], func(ctx context.Context) []tui.AutocompleteItem {
		p.queries.add(before)
		if p.slow != raceImmediate {
			if err := raceSleep(ctx, p.slow); err != nil {
				return nil
			}
		}
		return typedAutocompleteMatching(raceTokens, match[2])
	}, true
}

// raceLegacyProbe reports whether the probe stays inside what the legacy path shares with Pi's Editor: it debounces and defers only @ tokens, and answers nothing else.
func raceLegacyProbe(probe raceProbe) bool {
	var keys []string
	for _, op := range probe.Ops {
		keys = append(keys, op.Keys...)
	}
	text := strings.Join(keys, "")
	return strings.Contains(text, "@") && !strings.ContainsAny(text, "#$") && !strings.HasPrefix(text, "/") && !strings.Contains(text, "/load")
}

// raceEditor is a Pig editor on the extension provider chain ("async") or on the local provider ("native"), with the owner services of the interactive loop. It must run inside a synctest bubble, where time stands still until every goroutine waits on it.
func raceEditor(t *testing.T, mode string, slow int, queries *raceQueries) (*tui.Editor, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	applies := make(chan func(), 256)
	e := tui.NewEditor()
	e.BorderColor = func(text string) string { return tui.ActiveTheme().Fg("borderMuted", text) }
	e.SetFocused(true)
	e.SetAutocompleteMaxVisible(5)
	e.SetAsyncApply(func(apply func()) { applies <- apply })
	report := func(err error) { applies <- func() { t.Errorf("autocomplete error: %v", err) } }
	switch mode {
	case "legacy":
		// An editor with no task owner (a host that never calls SetAutocompleteTaskOwner): the local answer is applied at the key, a file search runs on its own goroutine. This editor has an owner loop (SetAsyncApply), where
		// the search takes the editor's text and applies its answer; with no loop at all (the extension view editor) it does both on its own goroutine, which races with the caller's keys.
		e.SetAutocomplete(raceLegacyProvider{queries, slow})
	case "native":
		e.SetAutocompleteTaskOwner(ctx, workers.Go, report)
		if slow != raceImmediate {
			e.SetAutocomplete(raceSlowNativeProvider{raceNativeProvider{queries}, slow})
		} else {
			e.SetAutocomplete(raceNativeProvider{queries})
		}
	case "async":
		input := func(work tui.AutocompleteWork) {
			workers.Go(func() {
				apply, err := work(ctx)
				applies <- func() {
					if err != nil {
						report(err)
					} else if apply != nil {
						apply()
					}
				}
			})
		}
		e.SetAsyncAutocomplete(&tui.AsyncAutocompleteProvider{
			TriggerCharacters: []string{"$"},
			GetSuggestions: func(ctx context.Context, lines []string, row, col int, _ bool) (*tui.AutocompleteSuggestions, error) {
				before := lines[row][:col]
				queries.add(before)
				if slow != raceImmediate {
					if err := raceSleep(ctx, slow); err != nil {
						return nil, err
					}
				}
				return raceAnswer(before), nil
			},
		}, ctx, workers.Go, input, report)
	}
	// settle runs what the owner loop would run now: every goroutine that can is blocked, so each queued apply is run and the goroutines run on.
	settle := func() {
		for {
			synctest.Wait()
			select {
			case apply := <-applies:
				apply()
			default:
				return
			}
		}
	}
	t.Cleanup(func() {
		e.AutocompleteCancel()
		cancel()
		workers.Wait()
	})
	return e, settle
}

func pigRaceLog(t *testing.T, mode string, probe raceProbe) raceLog {
	var log raceLog
	synctest.Test(t, func(t *testing.T) {
		queries := &raceQueries{}
		e, settle := raceEditor(t, mode, probe.Slow, queries)
		// The owner loop keeps running while time passes: a request that starts, or answers, in the meantime needs it.
		wait := func(ms int) {
			for range ms {
				time.Sleep(time.Millisecond)
				settle()
			}
		}
		for _, op := range probe.Ops {
			if op.Sleep > 0 {
				wait(op.Sleep)
				continue
			}
			// One terminal read: its keys one after another, then the checkpoint after it that starts the request they made.
			for _, keys := range op.Keys {
				e.HandleInput(keys)
			}
			e.FlushAutocomplete()
			settle()
		}
		wait(200)
		log.Queries, log.Text, log.Rows = queries.all(), e.Text(), e.Render(40)
		cursor := e.GetCursor()
		log.Cursor.Line, log.Cursor.Col = cursor.Line, cursor.Col
	})
	return log
}

func raceProbes() []raceProbe {
	typed := func(text string, tail ...raceOp) []raceOp {
		var ops []raceOp
		for _, r := range text {
			ops = append(ops, raceOp{Keys: []string{string(r)}})
		}
		return append(ops, tail...)
	}
	pause := func(ms int) raceOp { return raceOp{Sleep: ms} }
	var sets [][]raceOp
	for _, text := range []string{
		"@alé", "@al/", "@al ", "@al.", "@al", "@aléa", "@al é", "x @al-", "@résumé", "@al/d", "@alé/", "(@alé", "$alé", "#alé", "@al1é",
		"/load-skills ", "/load-skills é", "/load-skills s", "/load-skills sé", "/load-skills é ",
	} {
		sets = append(sets, typed(text))
	}
	// A pause between keys: the request the first keys made has started (the provider is still working, when it is slow) when the later keys come.
	for _, first := range []string{"@a", "@al", "#a", "$a"} {
		for _, last := range []string{"l", "é", "/", " ", "x", "-"} {
			sets = append(sets, typed(first, pause(30), raceOp{Keys: []string{last}}))
		}
		sets = append(sets, typed(first, pause(30), raceOp{Keys: []string{"l"}}, raceOp{Keys: []string{"é"}}), typed(first, pause(30), raceOp{Keys: []string{"l"}}, raceOp{Keys: []string{" "}}), typed(first, raceOp{Keys: []string{"l"}}, pause(30), raceOp{Keys: []string{"é"}}))
	}
	sets = append(sets, typed("/load-skills", pause(30), raceOp{Keys: []string{" "}}), typed("/load-skills ", pause(30), raceOp{Keys: []string{"é"}}), typed("/load-skills s", pause(30), raceOp{Keys: []string{"é"}}))
	// Keys that arrive in one read: the request the first of them made starts after the last, against the text and cursor then.
	read := func(keys ...string) []raceOp { return []raceOp{{Keys: keys}} }
	for _, keys := range [][]string{
		{"/", "m", "o", " "}, {"/", "m", "o", "é"}, {"/", "m", "o", "\x1b[D"}, {"/", "m", "o", "\x1b[D", "\x1b[D"}, {"/", "m", "\x7f"}, {"/", "m", "o", "\x1b[H"},
		{"/", "l", "o", "a", "d", "-", "s", "k", "i", "l", "l", "s", " "}, {"@", "a", "l", "é"}, {"@", "a", "l", "/"}, {"@", "a", "l", "\x1b[D"}, {"@", "a", "l", "\x7f"},
	} {
		sets = append(sets, read(keys...))
	}
	var probes []raceProbe
	for _, ops := range sets {
		for _, slow := range []int{raceImmediate, raceAborts, raceIgnoresAbort} {
			probes = append(probes, raceProbe{Slow: slow, Ops: ops})
		}
	}
	return probes
}

// piRaceLogs runs the probes through Pi's Editor in a few node processes at once; each probe waits for its own timers.
func piRaceLogs(t *testing.T, probes []raceProbe) []raceLog {
	t.Helper()
	processes := max(1, min(8, runtime.NumCPU()))
	size := (len(probes) + processes - 1) / processes
	parts := make([][]raceLog, processes)
	errs := make([]error, processes)
	var wg sync.WaitGroup
	for i := range processes {
		chunk := probes[min(len(probes), i*size):min(len(probes), (i+1)*size)]
		wg.Go(func() {
			input, err := json.Marshal(chunk)
			if err != nil {
				errs[i] = err
				return
			}
			cmd := exec.CommandContext(t.Context(), "node", "testdata/editor_typed_race.mjs", pigversion.UpstreamPackageVersion)
			cmd.Stdin = bytes.NewReader(input)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			output, err := cmd.Output()
			if err != nil {
				errs[i] = fmt.Errorf("Pi oracle: %w\n%s", err, &stderr)
				return
			}
			errs[i] = json.Unmarshal(output, &parts[i])
		})
	}
	wg.Wait()
	var logs []raceLog
	for i := range processes {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		logs = append(logs, parts[i]...)
	}
	return logs
}

// TestEditorTypedKeysThatTriggerNothingLeaveAPendingRequestAloneLikePi types each probe through Pi's Editor and through Pig's, on the extension provider chain, on the local provider with a task owner and on the local provider of an editor with no task owner (@ tokens only), with an immediate provider and a slow one, and requires the provider to be asked the same texts in the same order and the editor to end with the same rows.
func TestEditorTypedKeysThatTriggerNothingLeaveAPendingRequestAloneLikePi(t *testing.T) {
	useAwaitedAcceptTerminal(t)
	probes := raceProbes()
	expected := piRaceLogs(t, probes)
	if len(expected) != len(probes) {
		t.Fatalf("Pi returned %d logs for %d probes", len(expected), len(probes))
	}
	for _, mode := range []string{"async", "native", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			failures, popups, asked := 0, 0, 0
			for i, probe := range probes {
				if mode == "legacy" && !raceLegacyProbe(probe) {
					continue
				}
				got := pigRaceLog(t, mode, probe)
				want := expected[i]
				if mode == "legacy" {
					// The search of this path only runs where an @ token ends at the cursor; Pi asks its provider about every text and gets nothing for the others.
					var asked []string
					for _, query := range want.Queries {
						if match := typedAutocompleteTokenRe.FindStringSubmatch(query); match != nil && match[1] == "@" {
							asked = append(asked, query)
						}
					}
					want.Queries = asked
					if want.Queries == nil {
						want.Queries = []string{}
					}
				}
				if len(got.Queries) == 0 {
					got.Queries = []string{}
				}
				if len(want.Rows) > 3 {
					popups++
				}
				asked += len(want.Queries)
				if reflect.DeepEqual(got, want) {
					continue
				}
				if failures++; failures <= 6 {
					var ops []string
					for _, op := range probe.Ops {
						if op.Sleep > 0 {
							ops = append(ops, fmt.Sprintf("sleep %d", op.Sleep))
						} else {
							ops = append(ops, strings.Join(op.Keys, "+"))
						}
					}
					t.Errorf("probe %d slow=%d keys %s\n Pig queries %q text %q cursor %+v rows %d\n Pi  queries %q text %q cursor %+v rows %d", i, probe.Slow, strings.Join(ops, "|"),
						got.Queries, got.Text, got.Cursor, len(got.Rows), want.Queries, want.Text, want.Cursor, len(want.Rows))
					for row := range max(len(got.Rows), len(want.Rows)) {
						var pig, pi string
						if row < len(got.Rows) {
							pig = got.Rows[row]
						}
						if row < len(want.Rows) {
							pi = want.Rows[row]
						}
						if pig != pi {
							t.Errorf(" first row that differs: %d\n Pig %q\n Pi  %q", row, pig, pi)
							break
						}
					}
				}
			}
			if failures > 6 {
				t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
			}
			if popups == 0 || asked == 0 {
				t.Errorf("the probes opened %d popups and asked %d queries", popups, asked)
			}
			t.Logf("%d probes, %d with a popup, %d queries", len(probes), popups, asked)
		})
	}
}
