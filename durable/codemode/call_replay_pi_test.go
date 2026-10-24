package codemode

// pi: packages/durable/src/codemode/codemode.ts

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// codemode.ts:263-285 builds the namespaces of the start data: every tool whose name starts with a namespace's prefix is in
// that namespace's `tools`, in registry order. describeNamespace() answers from them, matching the name or its identifier
// form, and the first match is the one it describes.
func TestDescribeNamespaceListsEveryToolOfItsNamespaceAndMatchesItByIdentifier(t *testing.T) {
	chat := codemodeChatWith(t, ToolOptions{Namespaces: []Namespace{
		{Name: "my-github", Description: "Two words", Prefix: "mcp__my_github__"},
		{Name: "slack", Prefix: "mcp__slack__"},
	}})
	for _, name := range []string{"mcp__my_github__a", "mcp__slack__post", "mcp__my_github__b", "mcp__my_github__c"} {
		addTool(t, chat.setup.registry, tool(name, func(context.Context, toolArgs, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{}, nil
		}, durable.ReplayUnsafe))
	}
	run := runScript(t, `return {
		byName: await describeNamespace("my-github"),
		byIdentifier: await describeNamespace("my_github"),
		second: await describeNamespace("slack"),
	};`, chat)
	var got map[string]map[string]any
	if err := json.Unmarshal([]byte(scriptHeader.ReplaceAllString(run.text, "")), &got); err != nil {
		t.Fatalf("%q: %v", run.text, err)
	}
	github := map[string]any{"name": "my-github", "description": "Two words", "tools": []any{"mcp__my_github__a", "mcp__my_github__b", "mcp__my_github__c"}}
	slack := map[string]any{"name": "slack", "tools": []any{"mcp__slack__post"}}
	for key, want := range map[string]map[string]any{"byName": github, "byIdentifier": github, "second": slack} {
		if !equalJSON(got[key], want) {
			t.Errorf("%s = %s, want %s", key, mustJSON(got[key]), mustJSON(want))
		}
	}
}

// codemode.ts:456-458 `remaining <= 0`: a resume at or past the stored deadline ends the call; one millisecond before it
// does not.
func TestResumeAtTheDeadlineEndsTheCallAndOneMillisecondBeforeDoesNot(t *testing.T) {
	for _, c := range []struct {
		name    string
		advance time.Duration
		expired bool
	}{{"at the deadline", 300 * time.Millisecond, true}, {"one millisecond before it", 299 * time.Millisecond, false}} {
		t.Run(c.name, func(t *testing.T) {
			f := codemodeChat(t)
			clock := newManualClock()
			f.setup.now = clock.now
			f.vm.noTimer.Store(true)
			opened, root := f.restart(t, `// @options: {"timeout_ms": 300}
				await tools.hold({}); await tools.echo({ text: "late" });`, func(harness.Harness, durable.TaskId) {
				clock.advance(c.advance)
			})
			text, isError := resultText(t, root)
			passed := isError && strings.Contains(text, "deadline passed")
			if passed != c.expired {
				t.Errorf("result = %q (error %v), deadline passed = %v, want %v", text, isError, passed, c.expired)
			}
			if ran := f.echoCounts()["late"]; (ran == 0) != c.expired {
				t.Errorf("echo ran %d times, want it to run only when the deadline had not passed", ran)
			}
			mustClose(t, opened)
		})
	}
}

// A script's text is cut to a preview in UTF-16 units. A cut inside a surrogate pair leaves a lone surrogate. This pins what
// each side does with it today, so the difference stays visible until it is decided: Pi's JSON holds the escape `\ud83d`,
// PiG's string holds the unit as three bytes that encoding/json writes as three U+FFFD characters.
func TestACutInsideASurrogatePairIsAJSONEscapeInPiAndThreeReplacementCharactersInPiG(t *testing.T) {
	text := strings.Repeat("😀", 6)
	const max = 4
	input, err := json.Marshal([]helperOp{{Op: "cut", Text: text, Max: max}})
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/codemode_helpers_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	if want := `[{"ok":"\ud83d..."}]`; string(output) != want {
		t.Errorf("Pi's JSON = %s, want %s", output, want)
	}
	marshaled, err := json.Marshal(cut(text, max))
	if err != nil {
		t.Fatal(err)
	}
	if want := "\"\uFFFD\uFFFD\uFFFD...\""; string(marshaled) != want {
		t.Errorf("PiG's JSON = %s, want %s", marshaled, want)
	}
}

// models.ts MAX_CONCURRENT_MODEL_CALLS = 4 limits `models.classify()` and `models.generateImages()` only: a script's calls of
// ordinary tools all run at once, however many it makes.
func TestOrdinaryToolCallsAreNotLimitedToTheModelCallSlots(t *testing.T) {
	const calls = 6
	var started atomic.Int32
	allStarted := make(chan struct{})
	chat := codemodeChat(t)
	addTool(t, chat.setup.registry, tool("meet", func(ctx context.Context, _ toolArgs, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		if started.Add(1) == calls {
			close(allStarted)
		}
		select {
		case <-allStarted:
			return durable.ToolExecutionResult{}, nil
		case <-time.After(10 * time.Second):
			return durable.ToolExecutionResult{}, context.DeadlineExceeded
		}
	}, durable.ReplayUnsafe))
	run := runScript(t, `await Promise.all([1, 2, 3, 4, 5, 6].map((id) => tools.meet({ id }))); return "met";`, chat)
	if run.isError || !strings.Contains(run.text, "met") {
		t.Errorf("result %q (error %v), want all %d calls to run at once", run.text, run.isError, calls)
	}
}
