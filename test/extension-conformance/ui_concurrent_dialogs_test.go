package extensionconformance

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi bdd9f039 (38847563, #11166) interactive-mode.ts: an extension may open several select, confirm, input and editor dialogs at the same
// time (Promise.all over ctx.ui calls). The host UI receives every one of them, none dropped and none queued behind another, and
// each call settles with its own answer in whatever order the user closes them. The row drives one command per SDK that opens
// its dialogs concurrently (a Promise for each in Node, a goroutine in Go, a thread in Python and Rust) against a host UI that
// holds every dialog until the test answers or dismisses it. The terminal's own stack (the later dialog is the visible one, closing it shows
// the earlier one again) is covered by TestExtensionDialogs* in internal/codingagent against the real InteractiveMode.

type dialogAnswer struct {
	value string
	err   error
}

type heldDialog struct {
	kind, title string
	settle      chan dialogAnswer
}

// dialogGate is the host UI of the row: it holds each dialog it receives, in arrival order, until the test answers or dismisses it.
type dialogGate struct {
	mu   sync.Mutex
	open []*heldDialog
}

func newDialogGate() *dialogGate { return &dialogGate{} }

// hold is a dialog call of the host UI: it returns the answer the test gives, or the context's error when the request is cancelled.
func (g *dialogGate) hold(ctx context.Context, kind, title string) (string, error) {
	dialog := &heldDialog{kind: kind, title: title, settle: make(chan dialogAnswer, 1)}
	g.mu.Lock()
	g.open = append(g.open, dialog)
	g.mu.Unlock()
	defer g.remove(dialog)
	select {
	case answer := <-dialog.settle:
		return answer.value, answer.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (g *dialogGate) remove(dialog *heldDialog) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.open = slices.DeleteFunc(g.open, func(held *heldDialog) bool { return held == dialog })
}

// openTitles lists the dialogs the host UI holds, in the order they arrived.
func (g *dialogGate) openTitles() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	titles := make([]string, 0, len(g.open))
	for _, dialog := range g.open {
		titles = append(titles, dialog.title)
	}
	return titles
}

// answer settles the held dialog with value, as the user's answer; dismiss settles it as the user's cancel, or an abort, does.
func (g *dialogGate) answer(t *testing.T, title, value string) {
	t.Helper()
	g.settle(t, title, dialogAnswer{value: value})
}

func (g *dialogGate) dismiss(t *testing.T, title string) {
	t.Helper()
	g.settle(t, title, dialogAnswer{err: context.Canceled})
}

func (g *dialogGate) settle(t *testing.T, title string, answer dialogAnswer) {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, dialog := range g.open {
		if dialog.title == title {
			dialog.settle <- answer
			g.open = slices.DeleteFunc(g.open, func(held *heldDialog) bool { return held == dialog })
			return
		}
	}
	t.Fatalf("host UI holds no dialog %q (it holds %v)", title, titlesOf(g.open))
}

func titlesOf(dialogs []*heldDialog) []string {
	titles := make([]string, 0, len(dialogs))
	for _, dialog := range dialogs {
		titles = append(titles, dialog.title)
	}
	return titles
}

// stackProbeAnswers is what the test answers each dialog of the probe with, and what the extension reports a dismissed one as.
var stackProbeAnswers = map[string]struct{ answer, dismissed string }{
	"A": {"a2", "cancelled"}, "B": {"true", "false"},
	"S": {"s2", "cancelled"}, "C": {"true", "false"}, "I": {"typed text", "cancelled"}, "E": {"edited text", "cancelled"},
}

// stackProbe runs the dialog-stack-probe command of the realization in the background and reads what it reports.
type stackProbe struct {
	t    *testing.T
	h    *harness
	done chan struct{}
}

func startStackProbe(t *testing.T, h *harness, scenario string) *stackProbe {
	t.Helper()
	h.ui.ClearRecorded()
	ctx, cancel := context.WithCancel(context.Background())
	probe := &stackProbe{t: t, h: h, done: make(chan struct{})}
	found := make(chan bool, 1)
	go func() {
		defer close(probe.done)
		found <- h.runner.ExecuteCommand(ctx, "dialog-stack-probe", scenario)
	}()
	t.Cleanup(func() {
		cancel()
		<-probe.done
	})
	want := map[string]int{"pair": 2, "mix": 4}[scenario]
	var held []string
	if !awaitCondition(testbudget.Context(t), func() bool {
		select {
		case ok := <-found:
			if !ok {
				t.Error("fixture has no dialog-stack-probe command")
			} else {
				t.Error("dialog-stack-probe returned before any dialog was answered")
			}
			return true
		default:
		}
		held = h.ui.dialogs.openTitles()
		return len(held) >= want
	}) || len(held) < want {
		t.Fatalf("host UI holds %v, want %d dialogs open at once", held, want)
	}
	return probe
}

// reported lists, in order, what the command told the host: each settled dialog's answer and, once every dialog settled, "stack done".
func (p *stackProbe) reported() []string {
	var out []string
	for _, message := range p.h.ui.Recorded() {
		if strings.HasPrefix(message, "stack ") {
			out = append(out, strings.TrimSuffix(message, ":info"))
		}
	}
	return out
}

// expectReported waits until the command reported exactly want. The command can settle a dialog only after the host UI answered it, so no
// further report can precede the next answer.
func (p *stackProbe) expectReported(want ...string) {
	p.t.Helper()
	var got []string
	if !awaitCondition(testbudget.Context(p.t), func() bool {
		got = p.reported()
		return slices.Equal(got, want)
	}) {
		p.t.Fatalf("the command reported %q, want %q", got, want)
	}
}

func stackSettled(title, value string) string {
	return "stack settled " + title + "=" + value
}

// expectDone waits for the command to return after every dialog settled.
func (p *stackProbe) expectDone() {
	p.t.Helper()
	pollUntilConformance(p.t, "the command did not return", func() bool {
		select {
		case <-p.done:
			return true
		default:
			return false
		}
	})
}

// requireOnePrompt checks the ui_prompt events of overlapping dialogs: Pi reports only the outermost prompt (runner.ts uiPromptDepth), so
// one start and one end that repeats the start's kind and title, whichever dialog the host received first.
func requireOnePrompt(t *testing.T, h *harness, titles ...string) {
	t.Helper()
	var prompts []string
	pollUntilConformance(t, "the ui_prompt end event never arrived", func() bool {
		prompts, _ = splitPromptNotifications(h.ui.Recorded())
		return len(prompts) >= 2
	})
	// The bridge answers a dialog only after the prompt events it queued reached their handlers, so none is still on its way.
	prompts, _ = splitPromptNotifications(h.ui.Recorded())
	if len(prompts) != 2 {
		t.Fatalf("overlapping dialogs reported %d ui_prompt events %q, want one start and one end", len(prompts), prompts)
	}
	for _, title := range titles {
		for _, kind := range []string{"select", "confirm", "input", "editor"} {
			start := "ui_prompt:ui_prompt_start:ui_prompt:" + kind + ":" + title + ":info"
			if prompts[0] == start {
				if want := strings.Replace(start, "ui_prompt_start", "ui_prompt_end", 1); prompts[1] != want {
					t.Fatalf("ui_prompt events = %q, want the end to repeat the start's kind and title (%q)", prompts, want)
				}
				return
			}
		}
	}
	t.Fatalf("ui_prompt start %q names none of the dialogs %v", prompts[0], titles)
}

// Every SDK (isolated, packed and fused) and the in-process runner: two dialogs opened from one command both reach the host UI at once,
// the answers settle each call with its own value in last-in-first-out and first-in-first-out order, and dismissing the earlier or the
// later one (the host's cancel, which an extension's abort or a timeout comes to) settles that call alone.
func TestConformance_ConcurrentDialogsAgreeAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	for _, tc := range append(allHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			h.ui.dialogs = newDialogGate()

			t.Run("last in first out", func(t *testing.T) {
				probe := startStackProbe(t, h, "pair")
				arrived := h.ui.dialogs.openTitles()
				if len(arrived) != 2 {
					t.Fatalf("host UI holds %v, want both dialogs", arrived)
				}
				earlier, later := arrived[0], arrived[1]
				probe.expectReported()
				h.ui.dialogs.answer(t, later, stackProbeAnswers[later].answer)
				probe.expectReported(stackSettled(later, stackProbeAnswers[later].answer))
				h.ui.dialogs.answer(t, earlier, stackProbeAnswers[earlier].answer)
				probe.expectReported(stackSettled(later, stackProbeAnswers[later].answer), stackSettled(earlier, stackProbeAnswers[earlier].answer), "stack done")
				probe.expectDone()
				requireOnePrompt(t, h, "A", "B")
			})
			t.Run("first in first out", func(t *testing.T) {
				probe := startStackProbe(t, h, "pair")
				arrived := h.ui.dialogs.openTitles()
				earlier, later := arrived[0], arrived[1]
				h.ui.dialogs.answer(t, earlier, stackProbeAnswers[earlier].answer)
				probe.expectReported(stackSettled(earlier, stackProbeAnswers[earlier].answer))
				h.ui.dialogs.answer(t, later, stackProbeAnswers[later].answer)
				probe.expectReported(stackSettled(earlier, stackProbeAnswers[earlier].answer), stackSettled(later, stackProbeAnswers[later].answer), "stack done")
				probe.expectDone()
			})
			t.Run("dismissing the hidden dialog settles it alone", func(t *testing.T) {
				probe := startStackProbe(t, h, "pair")
				arrived := h.ui.dialogs.openTitles()
				earlier, later := arrived[0], arrived[1]
				h.ui.dialogs.dismiss(t, earlier)
				probe.expectReported(stackSettled(earlier, stackProbeAnswers[earlier].dismissed))
				h.ui.dialogs.answer(t, later, stackProbeAnswers[later].answer)
				probe.expectReported(stackSettled(earlier, stackProbeAnswers[earlier].dismissed), stackSettled(later, stackProbeAnswers[later].answer), "stack done")
				probe.expectDone()
			})
			t.Run("dismissing the visible dialog settles it alone", func(t *testing.T) {
				probe := startStackProbe(t, h, "pair")
				arrived := h.ui.dialogs.openTitles()
				earlier, later := arrived[0], arrived[1]
				h.ui.dialogs.dismiss(t, later)
				probe.expectReported(stackSettled(later, stackProbeAnswers[later].dismissed))
				h.ui.dialogs.answer(t, earlier, stackProbeAnswers[earlier].answer)
				probe.expectReported(stackSettled(later, stackProbeAnswers[later].dismissed), stackSettled(earlier, stackProbeAnswers[earlier].answer), "stack done")
				probe.expectDone()
			})
			t.Run("a select, confirm, input and editor settle in any order", func(t *testing.T) {
				probe := startStackProbe(t, h, "mix")
				arrived := h.ui.dialogs.openTitles()
				if len(arrived) != 4 {
					t.Fatalf("host UI holds %v, want all four dialogs", arrived)
				}
				var want []string
				for position, index := range []int{1, 3, 0, 2} {
					title := arrived[index]
					h.ui.dialogs.answer(t, title, stackProbeAnswers[title].answer)
					want = append(want, stackSettled(title, stackProbeAnswers[title].answer))
					if position < 3 {
						probe.expectReported(want...)
					}
				}
				probe.expectReported(append(want, "stack done")...)
				probe.expectDone()
			})
		})
	}
}
