package codemode

// pi: packages/durable/src/codemode/codemode.ts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	cm "github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's private helpers of codemode.ts (cut, outcomeOf, textOf, errorResult, asToolCall, countImages, describe, sameJson)
// run as Pi wrote them under Node, through a load hook that only appends an export (testdata/codemode_helpers_oracle.mjs),
// over seeded inputs; the Go helpers must answer the same.

type helperOp struct {
	Op         string        `json:"op"`
	Text       string        `json:"text"`
	Max        int           `json:"max"`
	Value      *string       `json:"value"`
	Name       string        `json:"name"`
	Structured bool          `json:"structured"`
	Result     *helperResult `json:"result,omitempty"`
	Message    string        `json:"message"`
	Call       *helperCall   `json:"call,omitempty"`
	A          *string       `json:"a"`
	B          *string       `json:"b"`
}

type helperResult struct {
	IsError          bool                     `json:"isError"`
	Diagnostics      []durable.ToolDiagnostic `json:"diagnostics"`
	StructuredOutput *string                  `json:"structuredOutput"`
}

type helperCall struct {
	ID     int     `json:"id"`
	Target string  `json:"target"`
	Name   string  `json:"name"`
	Args   *string `json:"args"`
}

type helperAnswer struct {
	OK  json.RawMessage `json:"ok"`
	Err *string         `json:"err"`
}

var helperValues = []string{
	`""`, `"text"`, `"a\nb"`, `0`, `1`, `-1.5`, `true`, `false`, `null`, `[]`, `{}`,
	`[{"type":"text","text":"one"}]`, `[{"type":"text","text":"one"},{"type":"text","text":"two"}]`,
	`[{"type":"image","data":"AAAA","mimeType":"image/png"}]`, `[{"type":"text","text":"a"},{"type":"image","data":"x","mimeType":"image/png"},{"type":"text","text":"b"}]`,
	`[{"type":"text"}]`, `[{"text":"no type"}]`, `[null]`, `[1,"x"]`, `["text"]`,
	`{"output":[{"type":"image"},{"type":"text"},{"type":"image"}]}`, `{"output":[]}`, `{"output":"x"}`, `{"output":[null,1,{"type":"image"}]}`, `{"output":{"type":"image"}}`,
	`{"a":1,"b":[1,2,{"c":null}]}`, `{"b":[1,2,{"c":null}],"a":1}`, `{"a":1,"b":[1,2,{"c":0}]}`, `{"a":1}`, `{"a":1,"b":2}`, `[1,2]`, `[2,1]`, `[1,2,3]`, `{"0":1}`, `[1]`,
	`{"exitCode":1,"output":"boom"}`, `"error text"`,
}

func helperOps() []helperOp {
	rng := rand.New(rand.NewPCG(20261119, 9))
	var ops []helperOp
	texts := []string{"", "a", "abc", strings.Repeat("x", 199), strings.Repeat("x", 200), strings.Repeat("x", 201), strings.Repeat("y", 499), strings.Repeat("y", 500), strings.Repeat("y", 501), "😀😀😀😀😀😀", "é日本語 ok", "line\nbreak"}
	for _, text := range texts {
		for _, max := range []int{3, 4, 5, 6, 7, 10, 200, 500} {
			ops = append(ops, helperOp{Op: "cut", Text: text, Max: max})
		}
	}
	for i := range helperValues {
		v := helperValues[i]
		ops = append(ops, helperOp{Op: "textOf", Value: &v}, helperOp{Op: "countImages", Value: &v})
		for j := range helperValues {
			w := helperValues[j]
			ops = append(ops, helperOp{Op: "sameJson", A: &v, B: &w})
		}
	}
	ops = append(ops, helperOp{Op: "textOf"}, helperOp{Op: "countImages"}, helperOp{Op: "sameJson"}, helperOp{Op: "sameJson", A: &helperValues[1]})
	severities := []durable.ToolDiagnosticSeverity{"error", "warn", "info"}
	for range 1500 {
		result := &helperResult{IsError: rng.IntN(2) == 0, Diagnostics: []durable.ToolDiagnostic{}}
		for range rng.IntN(4) {
			result.Diagnostics = append(result.Diagnostics, durable.ToolDiagnostic{Severity: severities[rng.IntN(3)], Message: []string{"first", "second", "", "multi\nline"}[rng.IntN(4)]})
		}
		if rng.IntN(5) > 0 {
			result.StructuredOutput = &helperValues[rng.IntN(len(helperValues))]
		}
		ops = append(ops, helperOp{Op: "outcome", Name: []string{"read", "bash", "x"}[rng.IntN(3)], Structured: rng.IntN(2) == 0, Result: result})
	}
	for _, message := range []string{"", "boom", "multi\nline"} {
		ops = append(ops, helperOp{Op: "errorResult", Message: message})
	}
	argsList := []*string{nil, new(`[]`), new(`["m"]`), new(`["m",{"q":1}]`), new(`["m",null]`), new(`[null,{"q":1}]`), new(`{"model":"m"}`), new(`"s"`), new(`["a","b","c"]`), new(`[[],[]]`)}
	for _, target := range []string{"global", "tool"} {
		for _, name := range []string{"models.classify", "models.generateImages", "other", "searchTools", ""} {
			for _, args := range argsList {
				call := &helperCall{ID: 7, Target: target, Name: name, Args: args}
				ops = append(ops, helperOp{Op: "asToolCall", Call: call}, helperOp{Op: "describe", Call: call})
			}
		}
	}
	return ops
}

func rawOf(value *string) json.RawMessage {
	if value == nil {
		return nil
	}
	return json.RawMessage(*value)
}

func canonicalOrNil(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	return canonical(t, raw)
}

func TestCodemodeHelpersMatchPi(t *testing.T) {
	ops := helperOps()
	input, err := json.Marshal(ops)
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
	var answers []helperAnswer
	if err := json.Unmarshal(output, &answers); err != nil || len(answers) != len(ops) {
		t.Fatalf("oracle output: %v (%d answers for %d ops)", err, len(answers), len(ops))
	}
	failures, rejected := 0, 0
	for i, op := range ops {
		want := answers[i]
		label := fmt.Sprintf("%s #%d", op.Op, i)
		if want.Err != nil {
			// Pi throws for an input no caller can produce (a missing text); the Go helper has no such input.
			t.Fatalf("%s: Pi threw %q", label, *want.Err)
		}
		switch op.Op {
		case "cut":
			var pi string
			_ = json.Unmarshal(want.OK, &pi)
			// A cut inside a surrogate pair leaves a lone surrogate: Pi's string holds it, and the Go string holds it as the
			// three bytes of its UTF-16 unit. Both stand for one invalid character, so the comparison is of valid text.
			if got := strings.ToValidUTF8(cut(op.Text, op.Max), "\uFFFD"); got != pi {
				t.Errorf("%s: cut(%q, %d) = %q, Pi %q", label, op.Text, op.Max, got, pi)
			}
		case "textOf":
			var pi string
			_ = json.Unmarshal(want.OK, &pi)
			var value any
			if op.Value != nil {
				_ = json.Unmarshal(rawOf(op.Value), &value)
			}
			if got := textOf(value); got != pi {
				t.Errorf("%s: textOf(%v) = %q, Pi %q", label, value, got, pi)
			}
		case "countImages":
			var pi int
			_ = json.Unmarshal(want.OK, &pi)
			var value any
			if op.Value != nil {
				_ = json.Unmarshal(rawOf(op.Value), &value)
			}
			if got := countImages(durable.NestedToolExecutionResult{StructuredOutput: value}); got != pi {
				t.Errorf("%s: countImages(%v) = %d, Pi %d", label, value, got, pi)
			}
		case "sameJson":
			var pi bool
			_ = json.Unmarshal(want.OK, &pi)
			if got := sameJSON(rawOf(op.A), rawOf(op.B)); got != pi {
				t.Errorf("%s: sameJSON(%v, %v) = %v, Pi %v", label, deref(op.A), deref(op.B), got, pi)
			}
		case "outcome":
			var pi struct {
				OK         bool    `json:"ok"`
				Value      *string `json:"value"`
				Message    *string `json:"message"`
				Properties *string `json:"properties"`
			}
			if err := json.Unmarshal(want.OK, &pi); err != nil {
				t.Fatal(err)
			}
			result := durable.NestedToolExecutionResult{IsError: op.Result.IsError, Diagnostics: op.Result.Diagnostics}
			if op.Result.StructuredOutput != nil {
				var value any
				_ = json.Unmarshal(rawOf(op.Result.StructuredOutput), &value)
				result.StructuredOutput, result.HasStructuredOutput = value, true
			}
			got := outcomeOf(op.Name, op.Structured, result)
			if !pi.OK {
				rejected++
			}
			message := ""
			if pi.Message != nil {
				message = *pi.Message
			}
			if got.OK != pi.OK || got.Message != message ||
				!reflect.DeepEqual(canonicalOrNil(t, got.Value), canonicalOrNil(t, rawOf(pi.Value))) ||
				!reflect.DeepEqual(canonicalOrNil(t, got.Properties), canonicalOrNil(t, rawOf(pi.Properties))) {
				failures++
				t.Errorf("%s: outcomeOf(%q, %v, %+v / %v) = %+v (value %s, properties %s), Pi %+v (value %v, properties %v)", label, op.Name, op.Structured, op.Result.Diagnostics, deref(op.Result.StructuredOutput), got, got.Value, got.Properties, pi, deref(pi.Value), deref(pi.Properties))
			}
		case "errorResult":
			var pi struct {
				Output  []struct{ Type, Text string } `json:"output"`
				IsError bool                          `json:"isError"`
			}
			if err := json.Unmarshal(want.OK, &pi); err != nil {
				t.Fatal(err)
			}
			got := errorResult(op.Message).result()
			text, _ := got.Output[0].(ai.TextContent)
			if len(got.Output) != 1 || len(pi.Output) != 1 || text.Text != pi.Output[0].Text || pi.Output[0].Type != "text" || got.IsError == nil || *got.IsError != pi.IsError {
				t.Errorf("%s: errorResult(%q) = %+v, Pi %+v", label, op.Message, got, pi)
			}
		case "asToolCall":
			var pi struct {
				ID     int     `json:"id"`
				Target string  `json:"target"`
				Name   string  `json:"name"`
				Args   *string `json:"args"`
			}
			if err := json.Unmarshal(want.OK, &pi); err != nil {
				t.Fatal(err)
			}
			got := asToolCall(cm.HostCall{ID: op.Call.ID, Target: op.Call.Target, Name: op.Call.Name, Args: rawOf(op.Call.Args)})
			if got.ID != pi.ID || got.Target != pi.Target || got.Name != pi.Name || !reflect.DeepEqual(canonicalOrNil(t, got.Args), canonicalOrNil(t, rawOf(pi.Args))) {
				t.Errorf("%s: asToolCall(%+v) = %+v (%s), Pi %+v (%v)", label, *op.Call, got, got.Args, pi, deref(pi.Args))
			}
		case "describe":
			var pi string
			_ = json.Unmarshal(want.OK, &pi)
			if got := describe(cm.HostCall{ID: op.Call.ID, Target: op.Call.Target, Name: op.Call.Name, Args: rawOf(op.Call.Args)}); got != pi {
				t.Errorf("%s: describe(%+v) = %q, Pi %q", label, *op.Call, got, pi)
			}
		}
	}
	if rejected < 100 {
		t.Errorf("only %d of the outcomes were rejections", rejected)
	}
	_ = failures
}

func deref(value *string) string {
	if value == nil {
		return "<undefined>"
	}
	return *value
}

// codemode.ts:196-197 ARGS_PREVIEW_CHARS = 200 and ERROR_PREVIEW_CHARS = 500, :199-201 cut(text, max): the call's details
// show the compact arguments and the error text cut to those lengths, ending in "...".
func TestNestedCallDetailsCutArgumentsAndErrorsToTheirPreviewLengths(t *testing.T) {
	chat := codemodeChat(t)
	addTool(t, chat.setup.registry, tool("sink", func(context.Context, toolArgs, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: strings.Repeat("e", 700)}}, IsError: new(true)}, nil
	}, durable.ReplayUnsafe))
	run := runScript(t, `await tools.echo({ text: "`+strings.Repeat("a", 300)+`" });
		try { await tools.sink({}); } catch (error) { return "caught"; }`, chat)
	byName := map[string]NestedCall{}
	for _, call := range run.details.Calls {
		byName[call.Name] = call
	}
	args := `{"text":"` + strings.Repeat("a", 300) + `"}`
	if got, want := byName["echo"].Args, args[:197]+"..."; got != want {
		t.Errorf("echo args (%d chars) = %q, want the first 197 characters and \"...\"", len(got), got)
	}
	if got, want := byName["sink"].Error, strings.Repeat("e", 497)+"..."; got != want {
		t.Errorf("sink error (%d chars) = %q, want the first 497 characters and \"...\"", len(got), got)
	}
	if got := byName["sink"].Args; got != "{}" {
		t.Errorf("a short argument list is shown whole, got %q", got)
	}
}

// codemode.ts:459-460 / :503 `wallMs = Date.now() - start.startedAt`, and output.ts's header `(wallMs / 1000).toFixed(1)`: the
// header's wall time is the Harness clock's time from the start of the call to its result.
func TestTheHeaderShowsTheWallTimeFromTheHarnessClock(t *testing.T) {
	chat := codemodeChat(t)
	clock := newManualClock()
	chat.setup.now = clock.now
	addTool(t, chat.setup.registry, tool("advance", func(context.Context, toolArgs, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		clock.advance(2500 * time.Millisecond)
		return durable.ToolExecutionResult{}, nil
	}, durable.ReplayUnsafe))
	run := runScript(t, `await tools.advance({}); return "done";`, chat)
	if !regexp.MustCompile(`^Script completed\nWall time 2\.5 seconds\n`).MatchString(run.text) {
		t.Errorf("header of %q, want a wall time of 2.5 seconds", run.text)
	}
}
