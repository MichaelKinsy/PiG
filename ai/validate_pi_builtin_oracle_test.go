package ai_test

// pi: packages/ai/src/utils/validation.ts
//
// Corpus (b): the schemas of PiG's built-in tools (bash, edit, find, grep, ls, powershell, read, write, codemode, tool_search), the exact JSON the Go tools declare, against generated near-miss arguments:
// numeric strings, null optionals, one value for an array, wrong types, missing required properties, extra properties, edits arrays of one and many, unicode and lone surrogates.

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/codemode"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/toolsearch"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

type builtinTool struct {
	name   string
	schema string
}

func builtinTools(t testing.TB) []builtinTool {
	t.Helper()
	var list []builtinTool
	for _, tool := range []ai.ToolSchema{
		(&tools.BashTool{}).Schema(), (&tools.EditTool{}).Schema(), (&tools.FindTool{}).Schema(), (&tools.GrepTool{}).Schema(), (&tools.LsTool{}).Schema(),
		(&tools.PowerShellTool{}).Schema(), (&tools.ReadTool{}).Schema(), (&tools.WriteTool{}).Schema(),
	} {
		parameters, err := tool.ParametersJSON()
		if err != nil {
			t.Fatal(err)
		}
		list = append(list, builtinTool{tool.Name, string(parameters)})
	}
	list = append(list, builtinTool{codemode.ToolName, string(codemode.Definition(codemode.Options{}).Parameters)}, builtinTool{toolsearch.ToolName, string(toolsearch.Definition().Parameters)})
	return list
}

// nearMissValues are what a model sends where the schema wants something else.
var nearMissValues = []string{
	`null`, `""`, `" "`, `"10"`, `"10.9"`, `10.9`, `"1e2"`, `"0x10"`, `"0b11"`, `true`, `false`, `"TRUE"`, `"false"`, `"1"`, `"0"`, `"null"`, `"undefined"`, `"10n"`, `[]`, `{}`, `["2"]`, `"nonsense"`,
	`"é😀"`, `"\ud800"`, `"a\udc00b"`, `1e21`, `-0`, `-1`, `9007199254740993`, `{"a":"1"}`, `[null]`, `[[]]`, `"\u0000\n\t\"\\"`, `"line\u2028para\u2029"`,
}

// sample is a JSON text valid for the schema node.
func sample(node any) string {
	object, ok := node.(*chordjson.Object)
	if !ok {
		return `null`
	}
	switch kind, _ := object.Value("type").(string); kind {
	case "string":
		return `"x"`
	case "number", "integer":
		return `2`
	case "boolean":
		return `true`
	case "array":
		return `[` + sample(object.Value("items")) + `]`
	case "object":
		var members []string
		properties, _ := object.Value("properties").(*chordjson.Object)
		if properties != nil {
			for key, value := range properties.All() {
				members = append(members, strconv.Quote(key)+`:`+sample(value))
			}
		}
		return `{` + strings.Join(members, ",") + `}`
	}
	return `null`
}

// builtinProbes are the near-miss arguments of one tool schema.
func builtinProbes(t testing.TB, tool builtinTool) []piProbe {
	t.Helper()
	decoded, err := chordjson.Decode([]byte(tool.schema))
	if err != nil {
		t.Fatal(err)
	}
	root := decoded.(*chordjson.Object)
	properties := root.Value("properties").(*chordjson.Object)
	valid := sample(root)
	var base *chordjson.Object
	if parsed, err := chordjson.Decode([]byte(valid)); err != nil {
		t.Fatal(err)
	} else {
		base = parsed.(*chordjson.Object)
	}
	// with returns the valid arguments with the member key replaced by value (omitted when value is empty), in the schema's member order.
	with := func(key, value string) string {
		var members []string
		for name, member := range base.All() {
			switch {
			case name != key:
				text, _ := chordjson.Marshal(member)
				members = append(members, strconv.Quote(name)+`:`+string(text))
			case value != "":
				members = append(members, strconv.Quote(name)+`:`+value)
			}
		}
		return `{` + strings.Join(members, ",") + `}`
	}
	args := []string{valid, `{}`, `null`, `[]`, `"x"`, `7`, `true`, `[` + valid + `]`,
		`{"extra":{"untouched":"10"},` + strings.TrimPrefix(valid, `{`), `{"é":1,"\ud800":"\ud83d","` + "\u2028" + `":null,` + strings.TrimPrefix(valid, `{`)}
	for key, property := range properties.All() {
		args = append(args, with(key, ``))
		for _, value := range nearMissValues {
			args = append(args, with(key, value))
		}
		// One value for an array, and arrays of one and many items, whose items each near-miss in turn.
		node, _ := property.(*chordjson.Object)
		if node == nil || node.Value("type") != "array" {
			continue
		}
		items, _ := node.Value("items").(*chordjson.Object)
		item := sample(items)
		args = append(args, with(key, item), with(key, `[`+item+`]`), with(key, `[`+item+`,`+item+`,`+item+`]`), with(key, `[`+item+`,null,`+item+`]`), with(key, `[`+strings.Repeat(item+`,`, 40)+item+`]`))
		itemProperties, _ := items.Value("properties").(*chordjson.Object)
		if itemProperties == nil {
			continue
		}
		for itemKey := range itemProperties.All() {
			itemBase, _ := chordjson.Decode([]byte(item))
			for _, value := range nearMissValues {
				var members []string
				for name, member := range itemBase.(*chordjson.Object).All() {
					text, _ := chordjson.Marshal(member)
					if name == itemKey {
						text = []byte(value)
					}
					members = append(members, strconv.Quote(name)+`:`+string(text))
				}
				changed := `{` + strings.Join(members, ",") + `}`
				args = append(args, with(key, `[`+changed+`]`), with(key, `[`+item+`,`+changed+`,`+item+`]`))
			}
			var members []string
			for name, member := range itemBase.(*chordjson.Object).All() {
				if name != itemKey {
					text, _ := chordjson.Marshal(member)
					members = append(members, strconv.Quote(name)+`:`+string(text))
				}
			}
			args = append(args, with(key, `[{`+strings.Join(members, ",")+`}]`), with(key, `[{}]`))
		}
	}
	probes := make([]piProbe, len(args))
	for i, text := range args {
		if !json.Valid([]byte(text)) {
			t.Fatalf("generated arguments are not JSON: %s", text)
		}
		probes[i] = piProbe{tool.name, tool.schema, text}
	}
	return probes
}

func TestValidateToolArgumentsMatchesPiOnBuiltinTools(t *testing.T) {
	var probes []piProbe
	for _, tool := range builtinTools(t) {
		probes = append(probes, builtinProbes(t, tool)...)
	}
	accepted := comparePi(t, probes)
	if accepted < len(probes)/10 || accepted > len(probes)*9/10 {
		t.Errorf("probes are one-sided: %d accepted of %d", accepted, len(probes))
	}
}
