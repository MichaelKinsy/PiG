package turn_test

// pi: packages/durable/src/harness/tool.ts (beforeTool hooks :60-75, task result with control :400-405)

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

// tool.ts:400-405: a completed call's task result is `{entryId, control}` when the tool's result has a control object (copied as strict JSON) and `{entryId}` alone when it has none.
// mutation-checked: dropping the control copy, or copying it only when absent, fails the first case.
func TestToolTaskResultCarriesTheControlTheToolReturns(t *testing.T) {
	for _, tc := range []struct {
		name, result, wantControl string
	}{
		{"with control", `{"content":[],"control":{"stop":true}}`, `"control":{"stop":true}`},
		{"without control", `{"content":[]}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.toolRound(`{}`)
			r.toolRuns[0].then(mustParse(t, tc.result), nil)
			r.run()
			tools := r.tasks(turn.KindTool)
			if len(tools) != 1 {
				t.Fatalf("tool tasks = %d", len(tools))
			}
			outcome := outcomeOf(t, tools[0])
			if !strings.Contains(outcome, `"status":"completed"`) {
				t.Fatalf("outcome = %s", outcome)
			}
			if tc.wantControl == "" {
				if strings.Contains(outcome, `"control"`) {
					t.Errorf("outcome %s has a control the tool did not return", outcome)
				}
			} else if !strings.Contains(outcome, tc.wantControl) {
				t.Errorf("outcome %s lacks %s", outcome, tc.wantControl)
			}
		})
	}
}

// tool.ts:60-75: a beforeTool hook that returns `arguments` replaces the arguments the tool runs with (a `block` wins, and an empty decision leaves them).
// mutation-checked: applying the replacement only when it is absent fails it.
func TestBeforeToolHookReplacesTheArgumentsTheToolRunsWith(t *testing.T) {
	for _, tc := range []struct{ name, decision, want string }{
		{"replacement", `{"arguments":{"text":"changed"}}`, `{"text":"changed"}`},
		{"empty decision", `{}`, `{"text":"x"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.hooks["beforeTool"] = func(int, *jv.Object) (any, error) { return mustParse(t, tc.decision), nil }
			r.toolRound(`{"text":"x"}`)
			if len(r.toolRuns) != 1 {
				t.Fatalf("tool runs = %d", len(r.toolRuns))
			}
			if got := jv.Stringify(r.toolRuns[0].run.Arguments); got != tc.want {
				t.Errorf("the tool ran with %s, want %s", got, tc.want)
			}
		})
	}
}

// tool.ts:198-206: a tool with `retain: "tail"` is given an output window ({maxBytes, maxLines, minIntervalMs, bytesPerSecond}); a head-retaining tool is given none.
// mutation-checked: comparing the retain mode the other way round fails both cases.
func TestToolRunGetsAnOutputWindowOnlyForTailRetention(t *testing.T) {
	for _, tc := range []struct {
		retain     string
		wantWindow bool
	}{{"tail", true}, {"head", false}, {"", false}} {
		t.Run("retain "+tc.retain, func(t *testing.T) {
			r := newRig(t)
			bytes, lines := 1000, 20
			tool := echoTool()
			tool.Limits = turn.ToolLimits{MaxBytes: &bytes, MaxLines: &lines, Retain: tc.retain}
			r.agent.Tools = []turn.Tool{tool}
			r.start("hi")
			r.resume()
			r.finish(toolUseWith("echo", `{}`))
			if len(r.toolRuns) != 1 {
				t.Fatalf("tool runs = %d", len(r.toolRuns))
			}
			window := r.toolRuns[0].run.OutputWindow
			if (window != nil) != tc.wantWindow {
				t.Fatalf("output window = %v, want one: %v", window, tc.wantWindow)
			}
			if window != nil {
				got := jv.Stringify(window)
				if !strings.Contains(got, `"maxBytes":1000`) || !strings.Contains(got, `"maxLines":20`) || !strings.Contains(got, `"minIntervalMs"`) || !strings.Contains(got, `"bytesPerSecond"`) {
					t.Errorf("window = %s", got)
				}
			}
		})
	}
}

// tool.ts:416-427 fromSlot: a call aborted while it had reported output ends with that durable output as its content, before the `aborted` diagnostic; one that had reported none has empty content.
// mutation-checked: leaving the content empty when there is output fails the first case.
func TestAbortedToolResultKeepsTheOutputItReported(t *testing.T) {
	for _, tc := range []struct {
		name, output string
	}{{"with output", "partial out"}, {"without output", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.toolRound(`{}`)
			if tc.output != "" {
				if err := r.toolRuns[0].io.Output([]byte(tc.output), true, nil); err != nil {
					t.Fatal(err)
				}
				r.run()
				r.advance(100)
			}
			task := r.tasks(turn.KindGeneration)[0]
			r.abortTask(taskID(t, task))
			last := lastEntry(r)
			if !strings.Contains(last, "Tool echo was aborted") {
				t.Fatalf("result entry = %s", last)
			}
			if got := strings.Contains(last, `"text":"`+tc.output+`"`); tc.output != "" && !got {
				t.Errorf("result entry %s lacks the reported output %q", last, tc.output)
			}
			if tc.output == "" && strings.Contains(last, `"content":[{"type":"text","text":""`) {
				t.Errorf("result entry %s has an empty text block", last)
			}
		})
	}
}

// tool.ts:864-878 boundContent (Pi 1.2.0): when the joined text of a result's text items exceeds the limits, the bounded text replaces the first text item (head) or the last (tail), the other text items go, and non-text items stay in place. A tool's result carries its content as output (harness/types.ts:154-157).
// mutation-checked: treating non-text items as text, keeping the wrong text item or dropping the kept one fails the cases.
func TestBoundedToolResultKeepsTheBoundedTextAtTheFirstOrLastTextItem(t *testing.T) {
	const image = `{"type":"image","data":"AA==","mimeType":"image/png"}`
	for _, tc := range []struct {
		retain  string
		want    string
		notWant string
	}{
		{"head", `[{"type":"text","text":"a\nb\nc\n"},` + image, `d\ne\nf`},
		{"tail", image + `,{"type":"text","text":"d\ne\nf\n"}`, `a\nb\nc`},
	} {
		t.Run(tc.retain, func(t *testing.T) {
			r := newRig(t)
			lines := 3
			tool := echoTool()
			tool.Limits = turn.ToolLimits{MaxLines: &lines, Retain: tc.retain}
			r.agent.Tools = []turn.Tool{tool}
			r.start("hi")
			r.resume()
			r.finish(toolUseWith("echo", `{}`))
			r.toolRuns[0].then(mustParse(t, `{"output":[{"type":"text","text":"a\nb\nc\n"},`+image+`,{"type":"text","text":"d\ne\nf\n"}]}`), nil)
			r.run()
			last := lastEntry(r)
			if !strings.Contains(last, tc.want) {
				t.Errorf("result entry %s lacks %s", last, tc.want)
			}
			if strings.Contains(last, tc.notWant) {
				t.Errorf("result entry %s still has %s", last, tc.notWant)
			}
		})
	}
}

// tool.ts:341-373 (final result) and the progress commit: details a tool reports through its api show in the live slot while it runs and become the result's details when the result has none; details the result carries win.
// mutation-checked: publishing details only when unchanged, or letting the reported details replace the result's own, fails it.
func TestToolReportedDetailsReachTheLiveSlotAndTheResult(t *testing.T) {
	for _, tc := range []struct{ name, result, want, notWant string }{
		{"result has none", `{"content":[]}`, `"details":{"k":1}`, ""},
		{"result has its own", `{"content":[],"details":{"own":2}}`, `"details":{"own":2}`, `"k":1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			r.toolRound(`{}`)
			conv := int64(1)
			if err := r.toolRuns[0].io.Output([]byte("o"), true, nil); err != nil {
				t.Fatal(err)
			}
			r.toolRuns[0].io.Details(mustParse(t, `{"k":1}`), func(error) {})
			r.run()
			r.advance(100)
			if live := r.doc(turn.LiveDef, session.ID(conv)); !strings.Contains(live, `"details":{"k":1}`) {
				t.Fatalf("live while the tool runs = %s", live)
			}
			r.toolRuns[0].then(mustParse(t, tc.result), nil)
			r.run()
			last := lastEntry(r)
			if !strings.Contains(last, tc.want) {
				t.Errorf("result entry %s lacks %s", last, tc.want)
			}
			if tc.notWant != "" && strings.Contains(last, tc.notWant) {
				t.Errorf("result entry %s has %s", last, tc.notWant)
			}
		})
	}
}
