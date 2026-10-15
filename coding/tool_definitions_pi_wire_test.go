package coding

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// upstream: packages/coding-agent/src/core/tools/{read,bash,edit,write,grep,find,ls,powershell}.ts (create<Tool>ToolDefinition): each built-in tool is declared to the model with
// Pi's name, label, description, promptSnippet, promptGuidelines, parameter schema (every property description included), constrainedSampling and renderShell. The expected
// values are what Pi's own code returns: testdata/pi_tool_definitions.json is the output of testdata/dump_pi_tool_definitions.mjs run against the pinned Pi 1.1.0 package
// (`node dump_pi_tool_definitions.mjs <pi-coding-agent>/dist/index.js > pi_tool_definitions.json`; create<Tool>ToolDefinition("/work")).
// mutation-checked: a changed word of any description, snippet or guideline, a dropped required member, a changed property type or description, a changed label, a dropped
// constrainedSampling and parameters encoded from the bare map (sorted members) each fail it.
func TestBuiltInToolDefinitionsMatchPisOwn(t *testing.T) {
	data, err := os.ReadFile("testdata/pi_tool_definitions.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]map[string]any
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	var goldenText map[string]map[string]json.RawMessage
	if err := json.Unmarshal(data, &goldenText); err != nil {
		t.Fatal(err)
	}
	bash, err := tools.CreateBashToolDefinition("/work", nil)
	if err != nil {
		t.Fatal(err)
	}
	definitions := map[string]extension.ToolDefinition{
		"Read": CreateReadToolDefinition("/work", nil), "Bash": bash, "Edit": CreateEditToolDefinition("/work", nil), "Write": CreateWriteToolDefinition("/work", nil),
		"Grep": CreateGrepToolDefinition("/work", nil), "Find": CreateFindToolDefinition("/work", nil), "Ls": CreateLsToolDefinition("/work", nil),
		"PowerShell": CreatePowerShellToolDefinition("/work", nil),
	}
	if len(golden) != len(definitions) {
		t.Fatalf("golden has %d tools, test builds %d", len(golden), len(definitions))
	}
	for name, want := range golden {
		t.Run(name, func(t *testing.T) {
			definition, ok := definitions[name]
			if !ok {
				t.Fatalf("no Go definition for %s", name)
			}
			encoded, err := json.Marshal(definition)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatal(err)
			}
			projected := map[string]any{}
			for key := range want {
				projected[key] = got[key]
			}
			// A member Pi leaves undefined is absent from the Go definition too.
			for _, key := range []string{"promptSnippet", "promptGuidelines", "constrainedSampling", "renderShell", "executionMode"} {
				if _, expected := want[key]; !expected {
					if value, present := got[key]; present {
						t.Errorf("%s = %v, Pi leaves it undefined", key, value)
					}
				}
			}
			// Pi hands extensions (getAllTools) and SDK callers the TypeBox object, so JSON.stringify writes its members in declaration order: the parameters must match as text, not only as a value.
			var wantParameters, gotParameters bytes.Buffer
			if err := json.Compact(&wantParameters, goldenText[name]["parameters"]); err != nil {
				t.Fatal(err)
			}
			if err := json.Compact(&gotParameters, definition.Parameters); err != nil {
				t.Fatal(err)
			}
			if gotParameters.String() != wantParameters.String() {
				t.Errorf("parameters member order differs from Pi's:\ngot  %s\nwant %s", gotParameters.String(), wantParameters.String())
			}
			if !reflect.DeepEqual(projected, want) {
				gotJSON, _ := json.MarshalIndent(projected, "", " ")
				wantJSON, _ := json.MarshalIndent(want, "", " ")
				t.Fatalf("definition differs from Pi's:\ngot  %s\nwant %s", gotJSON, wantJSON)
			}
		})
	}
}

// agent-session.ts getAllTools returns each registered definition's TypeBox parameters, so an extension's JSON.stringify(tool.parameters) shows Pi's member order. Probed
// (Pi 1.1.0 and PiG, an extension writing getAllTools() at session_start): Pi gives {"type":"object","required":["path"],"properties":{"path":…,"offset":…,"limit":…}} for read; PiG
// gave sorted members, {"properties":{"limit":…,"offset":…,"path":…},"required":["path"],"type":"object"}.
func TestGetAllToolsKeepsPisParameterMemberOrder(t *testing.T) {
	data, err := os.ReadFile("testdata/pi_tool_definitions.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]map[string]json.RawMessage
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	session, err := sessionFromServices(t, nil, CreateAgentSessionFromServicesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, info := range session.GetAllTools() {
		var want []byte
		for name, definition := range golden {
			if strings.EqualFold(name, info.Name) {
				want = definition["parameters"]
			}
		}
		if want == nil {
			continue
		}
		var wantText, gotText bytes.Buffer
		if err := json.Compact(&wantText, want); err != nil {
			t.Fatal(err)
		}
		if err := json.Compact(&gotText, info.Parameters); err != nil {
			t.Fatal(err)
		}
		if gotText.String() != wantText.String() {
			t.Errorf("%s parameters:\ngot  %s\nwant %s", info.Name, gotText.String(), wantText.String())
		}
		checked++
	}
	if checked < 7 {
		t.Fatalf("checked %d built-in tools, want the seven every platform registers", checked)
	}
}
