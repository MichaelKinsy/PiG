package codemode

// pi: packages/durable/src/codemode/codemode.ts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's createCodemodeTool and createCodemodeExtension, its module constants and its three documents (codemode.ts,
// imported as-is under Node) are built for each set of options below, and what they declare is compared with what
// CreateTool, CreateExtension, ModuleVersion, ToolName, RunDoc, JournalDoc and StoreDoc declare: names, descriptions, the
// parameters schema, output limits, constrained sampling, callers and replay mode, the document kinds, versions, scopes,
// initial values and checkpoint decisions over 140 delta counts, and the text of the `codemode` section for callable
// tools that include internal and deferred ones.

type codemodeOptions struct {
	Models       *bool        `json:"models"`
	InlineBudget *int         `json:"inlineBudget"`
	Namespaces   []describeNS `json:"namespaces"`
}

type codemodeOp struct {
	Op       string            `json:"op"`
	Options  codemodeOptions   `json:"options"`
	Callable []json.RawMessage `json:"callable"`
}

type docAnswer struct {
	Kind        string          `json:"kind"`
	Version     int             `json:"version"`
	Scope       string          `json:"scope"`
	History     *string         `json:"history"`
	Fork        *string         `json:"fork"`
	Initial     json.RawMessage `json:"initial"`
	Checkpoints []bool          `json:"checkpoints"`
}

type codemodeStatic struct {
	ModuleVersion int       `json:"moduleVersion"`
	ToolName      string    `json:"toolName"`
	Run           docAnswer `json:"run"`
	Journal       docAnswer `json:"journal"`
	Store         docAnswer `json:"store"`
}

type codemodeVariant struct {
	Tool struct {
		Name                string          `json:"name"`
		Description         string          `json:"description"`
		Parameters          json.RawMessage `json:"parameters"`
		OutputLimits        json.RawMessage `json:"outputLimits"`
		ConstrainedSampling json.RawMessage `json:"constrainedSampling"`
		Callers             []string        `json:"callers"`
		Replay              string          `json:"replay"`
	} `json:"tool"`
	Extension struct {
		Name     string   `json:"name"`
		Tools    []string `json:"tools"`
		Sections []string `json:"sections"`
	} `json:"extension"`
}

// canonical decodes JSON to a value whose comparison ignores member order and number spelling.
func canonical(t *testing.T, raw []byte) any {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return value
}

func TestCodemodeDeclarationsMatchPi(t *testing.T) {
	yes, no := true, false
	budgets := []*int{nil, new(0), new(1), new(40), new(150), new(400), new(3000), new(100000)}
	variants := []codemodeOptions{
		{Namespaces: []describeNS{}},
		{Models: &yes, Namespaces: []describeNS{}},
		{Models: &no, Namespaces: []describeNS{}},
		{Models: &no, InlineBudget: new(5), Namespaces: []describeNS{{Name: "github", Description: "GitHub", Prefix: "mcp__github__"}}},
	}
	ops := []codemodeOp{{Op: "static"}}
	for _, v := range variants {
		ops = append(ops, codemodeOp{Op: "variant", Options: v})
	}
	repo := json.RawMessage(`{"name":"read","description":"Read a file.","parameters":{"type":"object","properties":{"path":{"type":"string","description":"File path"}},"required":["path"]}}`)
	callable := []json.RawMessage{
		repo,
		json.RawMessage(`{"name":"mcp__github__list_issues","description":"List issues.","parameters":{"type":"object","properties":{"state":{"type":"string"}}}}`),
		json.RawMessage(`{"name":"mcp__github__search","description":"Search code.","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}`),
		json.RawMessage(`{"name":"pi.codemode.classify","description":"Internal.","parameters":{"type":"object","properties":{}}}`),
		json.RawMessage(`{"name":"pi.codemode.generate_images","description":"Internal too.","parameters":{"type":"object","properties":{}}}`),
		json.RawMessage(`{"name":"deferred_tool","description":"Found with searchTools.","deferred":true,"parameters":{"type":"object","properties":{}}}`),
		json.RawMessage(`{"name":"write","description":"Write a file.","parameters":{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}}}}`),
	}
	namespaces := []describeNS{{Name: "github", Description: "GitHub MCP server", Prefix: "mcp__github__"}}
	for _, budget := range budgets {
		ops = append(ops, codemodeOp{Op: "section", Options: codemodeOptions{InlineBudget: budget, Namespaces: namespaces}, Callable: callable})
	}
	for _, callableSet := range [][]json.RawMessage{{}, callable[3:6], callable[4:5], callable[5:6]} {
		ops = append(ops, codemodeOp{Op: "section", Options: codemodeOptions{Namespaces: namespaces}, Callable: callableSet})
	}
	input, err := json.Marshal(ops)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/codemode_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var answers []describeAnswer
	if err := json.Unmarshal(output, &answers); err != nil || len(answers) != len(ops) {
		t.Fatalf("oracle output: %v (%d answers for %d ops)", err, len(answers), len(ops))
	}
	for i, op := range ops {
		if answers[i].Err != nil {
			t.Fatalf("op %d (%s): Pi threw %q", i, op.Op, *answers[i].Err)
		}
		switch op.Op {
		case "static":
			var pi codemodeStatic
			if err := json.Unmarshal(*answers[i].OK, &pi); err != nil {
				t.Fatal(err)
			}
			if ModuleVersion != pi.ModuleVersion || ToolName != pi.ToolName {
				t.Errorf("ModuleVersion %d, ToolName %q; Pi %d, %q", ModuleVersion, ToolName, pi.ModuleVersion, pi.ToolName)
			}
			for name, c := range map[string]struct {
				token durable.AnyDocToken
				pi    docAnswer
			}{"RunDoc": {RunDoc, pi.Run}, "JournalDoc": {JournalDoc, pi.Journal}, "StoreDoc": {StoreDoc, pi.Store}} {
				compareDocument(t, name, c.token.AnyDefinition(), c.pi)
			}
		case "variant":
			var pi codemodeVariant
			if err := json.Unmarshal(*answers[i].OK, &pi); err != nil {
				t.Fatal(err)
			}
			compareVariant(t, op.Options, pi)
		case "section":
			var want *string
			if answers[i].OK != nil {
				var text string
				if err := json.Unmarshal(*answers[i].OK, &text); err != nil {
					t.Fatal(err)
				}
				want = &text
			}
			callable := make([]*durable.ToolRegistration, len(op.Callable))
			for j, raw := range op.Callable {
				callable[j] = registration(t, raw)
				var flag struct {
					Deferred bool `json:"deferred"`
				}
				if err := json.Unmarshal(raw, &flag); err != nil {
					t.Fatal(err)
				}
				callable[j].Deferred = flag.Deferred
			}
			extension := CreateExtension(ToolOptions{Models: op.Options.Models, InlineBudget: op.Options.InlineBudget, Namespaces: namespacesOf(op.Options.Namespaces)})
			got, err := extension.Sections[0].Render(context.Background(), durable.PromptInput{Agent: durable.Agent{Callable: callable}})
			if err != nil {
				t.Fatal(err)
			}
			label := fmt.Sprintf("section %d (budget %v, %d callable)", i, op.Options.InlineBudget, len(callable))
			switch {
			case want == nil && got != nil:
				t.Errorf("%s: PiG renders %q, Pi omits the section", label, *got)
			case want != nil && got == nil:
				t.Errorf("%s: PiG omits the section, Pi renders %q", label, *want)
			case want != nil && *got != *want:
				t.Errorf("%s differs from Pi\nPiG:\n%s\nPi:\n%s", label, *got, *want)
			}
		}
	}
}

func compareDocument(t *testing.T, name string, got *durable.AnyDocDefinition, pi docAnswer) {
	t.Helper()
	text := func(value *string) string {
		if value == nil {
			return ""
		}
		return *value
	}
	if got.Kind != pi.Kind || got.Version != pi.Version || string(got.Scope) != pi.Scope || string(got.History) != text(pi.History) || string(got.Fork) != text(pi.Fork) {
		t.Errorf("%s: kind %q v%d scope %q history %q fork %q; Pi %q v%d %q %q %q", name, got.Kind, got.Version, got.Scope, got.History, got.Fork, pi.Kind, pi.Version, pi.Scope, text(pi.History), text(pi.Fork))
	}
	initial, err := got.Initial(nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(canonical(t, encoded), canonical(t, pi.Initial)) {
		t.Errorf("%s: initial %s, Pi %s", name, encoded, pi.Initial)
	}
	if len(pi.Checkpoints) != 140 {
		t.Fatalf("Pi answered %d checkpoint decisions", len(pi.Checkpoints))
	}
	for deltas, want := range pi.Checkpoints {
		decision, err := got.CheckpointWhen(delta.NewJsonObject(0), nil, durable.CheckpointInfo{DeltasSinceBase: deltas})
		if err != nil || decision != want {
			t.Errorf("%s: CheckpointWhen with %d deltas = %v, %v; Pi %v", name, deltas, decision, err, want)
		}
	}
}

func compareVariant(t *testing.T, options codemodeOptions, pi codemodeVariant) {
	t.Helper()
	toolOptions := ToolOptions{Models: options.Models, InlineBudget: options.InlineBudget, Namespaces: namespacesOf(options.Namespaces)}
	tool := CreateTool(toolOptions)
	label := fmt.Sprintf("models %v", options.Models)
	if tool.Name != pi.Tool.Name || tool.Description != pi.Tool.Description {
		t.Errorf("%s: tool %q / description differs from Pi's %q", label, tool.Name, pi.Tool.Name)
	}
	parameters, err := tool.ParametersJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(canonical(t, parameters), canonical(t, pi.Tool.Parameters)) {
		t.Errorf("%s: parameters %s, Pi %s", label, parameters, pi.Tool.Parameters)
	}
	if tool.OutputLimits == nil || tool.OutputLimits.MaxBytes == nil || tool.OutputLimits.MaxLines == nil {
		t.Fatalf("%s: output limits %+v", label, tool.OutputLimits)
	}
	limits, _ := json.Marshal(map[string]int{"maxBytes": *tool.OutputLimits.MaxBytes, "maxLines": *tool.OutputLimits.MaxLines})
	if !reflect.DeepEqual(canonical(t, limits), canonical(t, pi.Tool.OutputLimits)) {
		t.Errorf("%s: output limits %s, Pi %s", label, limits, pi.Tool.OutputLimits)
	}
	sampling, _ := json.Marshal(tool.ConstrainedSampling)
	if !reflect.DeepEqual(canonical(t, sampling), canonical(t, pi.Tool.ConstrainedSampling)) {
		t.Errorf("%s: constrained sampling %s, Pi %s", label, sampling, pi.Tool.ConstrainedSampling)
	}
	var callers []string
	for _, caller := range tool.Callers {
		callers = append(callers, string(caller))
	}
	if !reflect.DeepEqual(callers, pi.Tool.Callers) || string(tool.Replay) != pi.Tool.Replay {
		t.Errorf("%s: callers %v replay %q; Pi %v %q", label, callers, tool.Replay, pi.Tool.Callers, pi.Tool.Replay)
	}
	extension := CreateExtension(toolOptions)
	var names, sections []string
	for _, each := range extension.Tools {
		names = append(names, each.Name)
	}
	for _, each := range extension.Sections {
		sections = append(sections, each.Key)
	}
	if extension.Name != pi.Extension.Name || !reflect.DeepEqual(names, pi.Extension.Tools) || !reflect.DeepEqual(sections, pi.Extension.Sections) {
		t.Errorf("%s: extension %q tools %v sections %v; Pi %q %v %v", label, extension.Name, names, sections, pi.Extension.Name, pi.Extension.Tools, pi.Extension.Sections)
	}
}
