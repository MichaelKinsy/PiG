package tui_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type awaitedAcceptOp struct {
	Keys    *string `json:"keys,omitempty"`
	SetText *string `json:"setText,omitempty"`
}

type awaitedAcceptProbe struct {
	Ops []awaitedAcceptOp `json:"ops"`
}

type awaitedAcceptLog struct {
	Text      string `json:"text"`
	Cursor    struct{ Line, Col int }
	Lines     []string    `json:"lines"`
	Callbacks [][2]string `json:"callbacks"`
	Rows      []string    `json:"rows"`
}

// awaitedAcceptEditor is a Pig editor on the given provider with the owner services of the interactive loop: settle runs the request a key made, when it made one, to its published answer.
func awaitedAcceptEditor(t *testing.T, provider tui.AutocompleteProvider) (*tui.Editor, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	e := tui.NewEditor()
	e.BorderColor = func(text string) string { return tui.ActiveTheme().Fg("borderMuted", text) }
	e.SetFocused(true)
	e.SetAutocompleteMaxVisible(5)
	var workers sync.WaitGroup
	applies := make(chan func(), 16)
	finished := make(chan struct{}, 16)
	errs := make(chan error, 16)
	started, consumed := 0, 0
	e.SetAsyncApply(func(apply func()) { applies <- apply })
	e.SetAutocompleteTaskOwner(ctx, func(task func()) {
		started++
		workers.Go(func() {
			task()
			finished <- struct{}{}
		})
	}, func(err error) { errs <- err })
	settle := func() {
		t.Helper()
		e.FlushAutocomplete()
		for consumed < started || len(applies) > 0 || len(errs) > 0 {
			select {
			case apply := <-applies:
				apply()
			case err := <-errs:
				t.Errorf("autocomplete error: %v", err)
			case <-finished:
				consumed++
			case <-ctx.Done():
				t.Fatalf("autocomplete did not complete: %v", ctx.Err())
			}
		}
	}
	t.Cleanup(func() {
		e.AutocompleteCancel()
		cancel()
		for consumed < started {
			<-finished
			consumed++
		}
		workers.Wait()
	})
	e.SetAutocomplete(provider)
	return e, settle
}

// awaitedAcceptSkillsProvider is the provider testdata/editor_awaited_accept.mjs defines for Pi: the "load-skills" command awaits its argument completions.
func awaitedAcceptSkillsProvider(t *testing.T) tui.AutocompleteProvider {
	skills := []string{"skill-a", "skill-b", "tool-a", "éskill-a", "日本-a"}
	return tui.NewCombinedProvider([]tui.SlashCommand{{
		Name: "load-skills", Description: "Load skills",
		AwaitArgumentCompletions: func(prefix string) ([]tui.AutocompleteItem, error) {
			var items []tui.AutocompleteItem
			for _, skill := range skills {
				if strings.HasPrefix(skill, prefix) {
					items = append(items, tui.AutocompleteItem{Value: skill, Label: skill})
				}
			}
			return items, nil
		},
	}}, t.TempDir(), "")
}

// useAwaitedAcceptTerminal puts the terminal state Pi's oracle runs under in place for the test and restores the state it found on cleanup.
func useAwaitedAcceptTerminal(t *testing.T) {
	t.Helper()
	previousKitty, previousCapabilities, previousTheme := tui.IsKittyProtocolActive(), tui.GetCapabilities(), tui.ActiveTheme().Name
	t.Cleanup(func() {
		tui.SetKittyProtocolActive(previousKitty)
		tui.SetCapabilities(previousCapabilities)
		tui.SetTheme(previousTheme)
	})
	setAwaitedAcceptTerminal()
}

func setAwaitedAcceptTerminal() {
	tui.SetKittyProtocolActive(false)
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme("dark")
}

// pigAwaitedAcceptLog runs the probe through Pig's editor on a fresh provider.
func pigAwaitedAcceptLog(t *testing.T, probe awaitedAcceptProbe, newProvider func(*testing.T) tui.AutocompleteProvider) []awaitedAcceptLog {
	t.Helper()
	setAwaitedAcceptTerminal()
	e, settle := awaitedAcceptEditor(t, newProvider(t))
	var callbacks [][2]string
	e.OnSubmit = func(text string) { callbacks = append(callbacks, [2]string{"submit", text}) }
	e.OnChange = func(text string) { callbacks = append(callbacks, [2]string{"change", text}) }
	var log []awaitedAcceptLog
	for _, op := range probe.Ops {
		callbacks = nil
		switch {
		case op.Keys != nil:
			e.HandleInput(*op.Keys)
		case op.SetText != nil:
			e.SetText(*op.SetText)
		}
		settle()
		entry := awaitedAcceptLog{Text: e.Text(), Lines: e.GetLines(), Callbacks: callbacks, Rows: e.Render(40)}
		if entry.Callbacks == nil {
			entry.Callbacks = [][2]string{}
		}
		cursor := e.GetCursor()
		entry.Cursor.Line, entry.Cursor.Col = cursor.Line, cursor.Col
		log = append(log, entry)
	}
	return log
}

// awaitedAcceptChunk is the outcome of one run of consecutive probes: the frames with a popup, the probes that differ from Pi, and the first few reports of those.
type awaitedAcceptChunk struct {
	popups, failures int
	reports          []string
}

// compareAwaitedAcceptProbesWithPi runs each probe through the Pi oracle script and Pig's editor and requires every op's text, cursor, lines, callbacks and rendered rows (popup included) to agree.
// Each Pig editor is its own and reads only the terminal state the test sets once, so runs of consecutive probes go through parallel subtests: a probe in a symbol context waits out the editor's 20 ms debounce.
func compareAwaitedAcceptProbesWithPi(t *testing.T, script string, probes []awaitedAcceptProbe, newProvider func(*testing.T) tui.AutocompleteProvider) {
	t.Helper()
	useAwaitedAcceptTerminal(t)
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/"+script, pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]awaitedAcceptLog
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	parts := max(1, min(8, runtime.NumCPU(), len(probes)/64))
	size := (len(probes) + parts - 1) / parts
	chunks := make([]awaitedAcceptChunk, parts)
	t.Run("probes", func(t *testing.T) {
		for part := range parts {
			t.Run(strconv.Itoa(part), func(t *testing.T) {
				t.Parallel()
				chunk := &chunks[part]
				for index := part * size; index < min(len(probes), (part+1)*size); index++ {
					probe := probes[index]
					got := pigAwaitedAcceptLog(t, probe, newProvider)
					for step := range probe.Ops {
						if len(got[step].Rows) > 3 {
							chunk.popups++
						}
						if reflect.DeepEqual(expected[index][step], got[step]) {
							continue
						}
						chunk.failures++
						if len(chunk.reports) < 6 {
							var ops []string
							for _, op := range probe.Ops[:step+1] {
								if op.Keys != nil {
									ops = append(ops, "keys:"+*op.Keys)
								} else {
									ops = append(ops, "setText:"+*op.SetText)
								}
							}
							chunk.reports = append(chunk.reports, fmt.Sprintf("probe %d step %d ops %q\n Pig %+v\n Pi  %+v", index, step, ops, got[step], expected[index][step]))
						}
						break
					}
				}
			})
		}
	})
	failures, popups, reported := 0, 0, 0
	for _, chunk := range chunks {
		failures += chunk.failures
		popups += chunk.popups
		for _, report := range chunk.reports {
			if reported++; reported <= 6 {
				t.Error(report)
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
	if popups == 0 {
		t.Error("no probe opened a popup")
	}
	t.Logf("%d probes, %d popup frames", len(probes), popups)
}

// awaitedAcceptMovedCursorProbes opens a popup of awaited command-argument completions (setText, then the key typed last), moves the cursor or edits with the popup open, and accepts it with Tab or Enter.
func awaitedAcceptMovedCursorProbes(setups [][2]string) []awaitedAcceptProbe {
	movers := []string{"", "\x1b[D", "\x01", "\x1b[1;5D", "\x1bb", "\x1b[C", "\x7f", "x", " ", "\x1b[H", "\x1b[F", "\x05"}
	accepts := []string{"\t", "\r"}
	var probes []awaitedAcceptProbe
	for _, setup := range setups {
		for _, mover := range movers {
			for _, second := range []string{"", "\x1b[D"} {
				for _, accept := range accepts {
					ops := []awaitedAcceptOp{{SetText: new(setup[0])}, {Keys: new(setup[1])}}
					for _, keys := range []string{mover, second, accept} {
						if keys != "" {
							ops = append(ops, awaitedAcceptOp{Keys: new(keys)})
						}
					}
					probes = append(probes, awaitedAcceptProbe{Ops: ops})
				}
			}
		}
	}
	return probes
}

// TestEditorAcceptsAnAwaitedPopupAtTheMovedCursorLikePi: Pi's confirm handler applies the popup the editor last published at the cursor it has now, so Pig must neither drop it nor query again.
func TestEditorAcceptsAnAwaitedPopupAtTheMovedCursorLikePi(t *testing.T) {
	probes := awaitedAcceptMovedCursorProbes([][2]string{{"/load-skills ", "s"}, {"/load-skills s", "k"}, {"/load-skills ", "t"}})
	compareAwaitedAcceptProbesWithPi(t, "editor_awaited_accept.mjs", probes, awaitedAcceptSkillsProvider)
}

// TestEditorAcceptsAnAwaitedPopupWithANonASCIIPrefixAtTheMovedCursorLikePi runs the same moves with non-ASCII text before the cursor. Pi's applyCompletion slices the line at cursorCol - prefix.length in UTF-16 units, and the prefix of the last published popup is not the text before the cursor once the cursor moved, so a splice at a byte offset lands elsewhere.
func TestEditorAcceptsAnAwaitedPopupWithANonASCIIPrefixAtTheMovedCursorLikePi(t *testing.T) {
	probes := awaitedAcceptMovedCursorProbes([][2]string{{"/load-skills é", "s"}, {"/load-skills ", "é"}, {"/load-skills és", "k"}, {"/load-skills ", "日"}, {"/load-skills 日", "本"}})
	compareAwaitedAcceptProbesWithPi(t, "editor_awaited_accept.mjs", probes, awaitedAcceptSkillsProvider)
}
