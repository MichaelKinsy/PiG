package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type exportsOracleCase struct {
	Kind               string          `json:"kind"`
	Options            map[string]bool `json:"options,omitempty"`
	Tools              json.RawMessage `json:"tools,omitempty"`
	SupportsStrictMode bool            `json:"supportsStrictMode"`
	Message            json.RawMessage `json:"message,omitempty"`
	Pieces             []string        `json:"pieces"`
	Replace            *string         `json:"replace,omitempty"`
	label              string
}

type exportsOracleAnswer struct {
	OK  *string `json:"ok"`
	Err *string `json:"err"`
}

// Pi's own getResponsesToolSupport, convertResponsesTools (strict sampling through the Responses tool builder), splitErrorToolResultImages and
// ToolCallArgumentsStream.finish run in Node over the inputs the Go functions answer, so a pin move re-checks every expectation of
// strict_sampling_exports_test.go against Pi and not only against its constants.
func TestStrictSamplingExportsMatchPi(t *testing.T) {
	var cases []exportsOracleCase
	for mask := range 8 {
		cases = append(cases, exportsOracleCase{Kind: "toolSupport", label: fmt.Sprintf("toolSupport/%d", mask), Options: map[string]bool{
			"supportsMidConvoSystemMessages": mask&1 != 0, "supportsAdditionalTools": mask&2 != 0, "supportsToolSearch": mask&4 != 0}})
	}
	for name, tools := range strictSamplingFixedToolSets() {
		for _, strict := range []bool{true, false} {
			cases = append(cases, exportsOracleCase{Kind: "tools", label: fmt.Sprintf("tools/%s/strict=%t", name, strict), Tools: json.RawMessage("[" + tools + "]"), SupportsStrictMode: strict})
		}
	}
	image := func(data string) string { return `{"type":"image","data":"` + data + `","mimeType":"image/png"}` }
	text := func(value string) string { return `{"type":"text","text":"` + value + `"}` }
	const placeholder = "(image attached after the tool results)"
	for name, message := range map[string]string{
		"success":                 `{"role":"toolResult","toolCallId":"call_1","toolName":"shot","isError":false,"content":[` + text("ok") + `,` + image("a") + `]}`,
		"error without images":    `{"role":"toolResult","toolCallId":"call_1","toolName":"shot","isError":true,"content":[` + text("boom") + `]}`,
		"error with images":       `{"role":"toolResult","toolCallId":"call_1","toolName":"shot","isError":true,"content":[` + text("boom") + `,` + image("a") + `,` + image("b") + `,` + text("tail") + `,` + image("c") + `]}`,
		"image after placeholder": `{"role":"toolResult","toolCallId":"call_1","toolName":"shot","isError":true,"content":[` + text(placeholder) + `,` + image("a") + `]}`,
	} {
		cases = append(cases, exportsOracleCase{Kind: "split", label: "split/" + name, Message: json.RawMessage(message)})
	}
	stale := `{"fresh":2}`
	for name, c := range map[string]exportsOracleCase{
		"ordered":      {Pieces: []string{`{"b":`, `[1,2],"a":"x`, `y"}`}},
		"empty":        {Pieces: []string{}},
		"array":        {Pieces: []string{`[1]`}},
		"replace":      {Pieces: []string{`{"stale":1}`}, Replace: &stale},
		"repaired":     {Pieces: []string{`{"a":"x`, "\n", `y"}`}},
		"unterminated": {Pieces: []string{`{"a":`}},
		"scalar":       {Pieces: []string{`"x"`}},
	} {
		c.Kind, c.label = "finish", "finish/"+name
		cases = append(cases, c)
	}

	payload, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/strict_sampling_exports_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var answers []exportsOracleAnswer
	if err := json.Unmarshal(output, &answers); err != nil || len(answers) != len(cases) {
		t.Fatalf("oracle output: %v (%d answers for %d cases)", err, len(answers), len(cases))
	}
	for i, c := range cases {
		got, gotErr := goExportsAnswer(t, c)
		want := answers[i]
		switch {
		case want.Err != nil:
			if gotErr == "" || !strings.Contains(gotErr, *want.Err) {
				t.Errorf("%s: PiG error %q, Pi error %q", c.label, gotErr, *want.Err)
			}
		case gotErr != "":
			t.Errorf("%s: PiG error %q, Pi answered %v", c.label, gotErr, want.OK)
		case want.OK == nil && got != "undefined" || want.OK != nil && got != *want.OK:
			t.Errorf("%s:\n PiG %s\n Pi  %v", c.label, got, want.OK)
		}
	}
}

// goExportsAnswer is the Go function's answer as the JSON text Pi's JSON.stringify gives the same value, or "undefined", or its error message.
func goExportsAnswer(t *testing.T, c exportsOracleCase) (answer, failure string) {
	t.Helper()
	encode := func(value any) string {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("%s: %v", c.label, err)
		}
		return string(encoded)
	}
	switch c.Kind {
	case "toolSupport":
		return encode(GetResponsesToolSupport(ResponsesToolSupportOptions{
			SupportsMidConvoSystemMessages: c.Options["supportsMidConvoSystemMessages"], SupportsAdditionalTools: c.Options["supportsAdditionalTools"], SupportsToolSearch: c.Options["supportsToolSearch"]})), ""
	case "tools":
		var tools []ToolSchema
		if err := json.Unmarshal(c.Tools, &tools); err != nil {
			t.Fatal(err)
		}
		out, err := (&openAIResponsesProvider{}).convertTools(tools, c.SupportsStrictMode, false)
		if err != nil {
			return "", err.Error()
		}
		return encode(out), ""
	case "split":
		var message ToolResultMessage
		if err := json.Unmarshal(c.Message, &message); err != nil {
			t.Fatal(err)
		}
		split := SplitErrorToolResultImages(message)
		if split == nil {
			return "undefined", ""
		}
		return encode(struct {
			Content []TextContent  `json:"content"`
			Caption string         `json:"caption"`
			Images  []ImageContent `json:"images"`
		}{split.Content, split.Caption, split.Images}), ""
	}
	stream := NewToolCallArgumentsStream()
	for _, piece := range c.Pieces {
		stream.Append(piece)
	}
	if c.Replace != nil {
		stream.Replace(*c.Replace)
	}
	arguments, order, message := stream.Finish()
	encoded, err := MarshalInRecordedOrder(map[string]any(arguments), order)
	if err != nil {
		t.Fatal(err)
	}
	if message != "" {
		return `{"arguments":` + string(encoded) + `,"argumentsError":` + encode(message) + `}`, ""
	}
	return `{"arguments":` + string(encoded) + `}`, ""
}
