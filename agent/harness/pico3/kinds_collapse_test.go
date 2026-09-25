package pico3

import (
	"context"
	"strings"
	"testing"
)

func summaryScript(messages []JsonObject, call int) fakeResponse {
	for _, message := range messages {
		if message["role"] == "user" && strings.Contains(str(message, "content"), "Respond with the summary only") {
			return textResponse("SUMMARY")
		}
	}
	return echoScript(messages, call)
}

func TestThresholdCollapseCreatesForegroundSummary(t *testing.T) {
	env := openEnv(t, openOptions{models: newFake(fakeOptions{respond: summaryScript}), root: &RootSpec{Rewindable: JsonObject{"model": testModel, "threshold": 100, "keepRecent": 30}}})
	for _, text := range []string{"one", "two", "three", "four"} {
		env.wait(env.send(env.root, text))
	}
	env.idle()
	collapse := firstOfKind(t, env.tasks(), "pi.collapse")
	equal(t, collapse.Background, false, "foreground")
	equal(t, str(asObject(collapse.Input), "reason"), "threshold", "reason")
	view := env.context(1)
	if view.Head == nil || view.Head.Kind != "pi.summary" || len(view.Entries) >= 8 {
		t.Fatalf("context not collapsed: %#v", view)
	}
	equal(t, contentOf(view.Head), "SUMMARY ", "summary")
}

func TestBeforeCollapseDeclineSummaryAndInstructions(t *testing.T) {
	for _, mode := range []string{"decline", "summary", "instructions"} {
		t.Run(mode, func(t *testing.T) {
			models := newFake(fakeOptions{respond: summaryScript})
			env := openEnv(t, openOptions{models: models, root: &RootSpec{Rewindable: JsonObject{"model": testModel, "keepRecent": 10}}, hooks: &hooksByKind{collapse: &CollapseHooks{BeforeCollapse: func(context.Context, string, Id, []Entry, HookApi) (*CollapseDecision, error) {
				switch mode {
				case "decline":
					return &CollapseDecision{Decline: true}, nil
				case "summary":
					return &CollapseDecision{Summary: new("HOOKED")}, nil
				default:
					return &CollapseDecision{Instructions: new("Be terse. Respond with the summary only")}, nil
				}
			}}}})
			for _, text := range []string{"one", "two", "three"} {
				env.wait(env.send(env.root, text))
			}
			before := models.Calls()
			id := must(env.root.Collapse(bg, nil))
			task := env.untilTerminal(id)
			switch mode {
			case "decline":
				equal(t, str(asObject(task.Outcome.Failure), "reason"), "declined", "decline")
				equal(t, models.Calls(), before, "no request")
			case "summary":
				equal(t, task.Outcome.Status, OutcomeCompleted, "completed")
				equal(t, models.Calls(), before, "no request")
				var summary Entry
				for _, entry := range env.entries() {
					if entry.Kind == "pi.summary" {
						summary = entry
					}
				}
				equal(t, contentOf(&summary), "HOOKED", "hook summary")
			default:
				equal(t, task.Outcome.Status, OutcomeCompleted, "completed")
				equal(t, models.Calls(), before+1, "one summary request")
				requests := models.Requests()
				last := requests[len(requests)-1]
				if !strings.Contains(str(last[len(last)-1], "content"), "Be terse") {
					t.Fatal("instructions lost")
				}
			}
		})
	}
}

func TestCollapseStalesWhenHeadChanges(t *testing.T) {
	gate := &testGate{}
	gate.Open()
	models := newFake(fakeOptions{respond: summaryScript, gate: gate})
	env := openEnv(t, openOptions{models: models, root: &RootSpec{Rewindable: JsonObject{"model": testModel, "keepRecent": 10}}})
	for _, text := range []string{"one", "two", "three"} {
		env.wait(env.send(env.root, text))
	}
	gate.Close()
	id := must(env.root.Collapse(bg, nil))
	gate.Arrivals(t, 4)
	check(t, env.root.Reset(bg, nil))
	gate.Open()
	task := env.untilTerminal(id)
	equal(t, str(asObject(task.Outcome.Failure), "reason"), "stale", "stale snapshot")
	for _, entry := range env.entries() {
		if entry.Kind == "pi.summary" {
			t.Fatal("stale summary published")
		}
	}
}

func TestManualCollapseRejectsEmptyRange(t *testing.T) {
	env := openEnv(t, openOptions{})
	env.wait(env.send(env.root, "one"))
	if _, err := env.root.Collapse(bg, nil); err == nil || !strings.Contains(err.Error(), "nothing to collapse") {
		t.Fatalf("empty collapse: %v", err)
	}
}
