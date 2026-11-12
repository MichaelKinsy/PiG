package codemode

// pi: packages/durable/src/codemode/describe.ts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's codemodeDescription, namespacesOf, sampleOf and codemodeSection (describe.ts, imported as-is under Node, with Pi's
// own pi-codemode declarations) answer for seeded random tool sets. Description, NamespacesOf, sampleOf and Section must
// answer the same text. The tool sets are the shapes the code branches on: tool names that are and are not identifiers,
// extensions that declare namespaces (also the same name twice, and one without tools) and bring tools several of them name, descriptions with blank and multi-line text, schemas of every JSON Schema kind, and a
// budget swept one token at a time across the whole range, so every place a tool does or does not fit is compared.

type describeOp struct {
	Op         string            `json:"op"`
	Models     bool              `json:"models"`
	Tools      []json.RawMessage `json:"tools"`
	Tool       json.RawMessage   `json:"tool,omitempty"`
	Extensions []describeExt     `json:"extensions"`
	Budget     int               `json:"budget"`
}

// describeExt is an extension of the oracle's ops: the tools it brings, by name, and the namespace it declares.
type describeExt struct {
	Name      string      `json:"name"`
	Tools     []string    `json:"tools"`
	Namespace *describeNS `json:"namespace,omitempty"`
}

type describeNS struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions,omitempty"`
}

type describeAnswer struct {
	OK  *json.RawMessage `json:"ok"`
	Err *string          `json:"err"`
}

var describeWords = []string{"alpha", "beta", "gamma", "path", "file", "count", "name", "value", "items", "id", "url", "query", "limit", "offset"}

var describeTexts = []string{
	"", " ", "A tool.", "Reads a file.\nSecond line.", "  padded  ", "Has */ a comment close", "Has `backticks` and ${dollar}",
	"Line one\r\nline two", "Tab\there", "Unicode: é日本 😀", "A very long description " + strings.Repeat("that goes on and on ", 20),
	"* leading star\n* bullet", "/** nested */", "Ends with backslash\\", "quote \" and ' marks", "<script>&amp;</script>",
}

func randomName(rng *rand.Rand, i int) string {
	pools := [][]string{
		{"read", "write", "bash", "edit", "grep", "find", "ls", "fetch"},
		{"mcp__github__list_issues", "mcp__github__search", "mcp__github__create_pull_request", "mcp__github__get_file", "mcp__github__x"},
		{"mcp__slack__post", "mcp__slack__history", "mcp__slackbot__ping", "mcp__slack__"},
		{"my-tool", "a.b", "class", "default", "9lives", "tool name", "É", "x$y", "_private", "returnValue", "if", "tools", "text", "constructor", "__proto__", "toString"},
	}
	pool := pools[rng.IntN(len(pools))]
	name := pool[rng.IntN(len(pool))]
	if rng.IntN(2) == 0 {
		name = fmt.Sprintf("%s_%d", name, i)
	}
	return name
}

func randomSchema(rng *rand.Rand, depth int) string {
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	desc := func() string {
		if rng.IntN(3) == 0 {
			return `,"description":` + quote(describeTexts[rng.IntN(len(describeTexts))])
		}
		return ""
	}
	kinds := 12
	if depth <= 0 {
		kinds = 6
	}
	switch rng.IntN(kinds) {
	case 0:
		return `{"type":"string"` + desc() + `}`
	case 1:
		return `{"type":"number"` + desc() + `}`
	case 2:
		return `{"type":"integer"` + desc() + `}`
	case 3:
		return `{"type":"boolean"` + desc() + `}`
	case 4:
		values := []string{`"a"`, `"b-c"`, `"it's"`, `"q\"uote"`, `1`, `true`, `null`}
		rng.Shuffle(len(values), func(i, j int) { values[i], values[j] = values[j], values[i] })
		return `{"enum":[` + strings.Join(values[:1+rng.IntN(3)], ",") + `]` + desc() + `}`
	case 5:
		return []string{`{"type":"null"}`, `{"const":"fixed"}`, `{"type":["string","null"]}`, `{}`, `{"type":"string","format":"date-time"}`, `{"const":5}`}[rng.IntN(6)]
	case 6, 7:
		return `{"type":"array","items":` + randomSchema(rng, depth-1) + desc() + `}`
	case 8:
		return `{"anyOf":[` + randomSchema(rng, depth-1) + `,` + randomSchema(rng, depth-1) + `]` + desc() + `}`
	case 9:
		return `{"oneOf":[` + randomSchema(rng, depth-1) + `,` + randomSchema(rng, depth-1) + `,` + randomSchema(rng, depth-1) + `]}`
	default:
		return randomObject(rng, depth-1)
	}
}

func randomObject(rng *rand.Rand, depth int) string {
	count := rng.IntN(5)
	var props, required []string
	used := map[string]bool{}
	for range count {
		name := describeWords[rng.IntN(len(describeWords))]
		if rng.IntN(5) == 0 {
			name = []string{"with-dash", "class", "9x", "a b", "q\\\"uote", "é", "$ref", "__proto__"}[rng.IntN(8)]
		}
		if used[name] {
			continue
		}
		used[name] = true
		b, _ := json.Marshal(name)
		props = append(props, string(b)+`:`+randomSchema(rng, depth))
		if rng.IntN(2) == 0 {
			required = append(required, string(b))
		}
	}
	out := `{"type":"object","properties":{` + strings.Join(props, ",") + `}`
	if len(required) > 0 {
		out += `,"required":[` + strings.Join(required, ",") + `]`
	}
	switch rng.IntN(5) {
	case 0:
		out += `,"additionalProperties":false`
	case 1:
		out += `,"additionalProperties":true`
	case 2:
		out += `,"additionalProperties":` + randomSchema(rng, depth-1)
	}
	return out + `}`
}

func randomTool(rng *rand.Rand, i int) string {
	name, _ := json.Marshal(randomName(rng, i))
	description, _ := json.Marshal(describeTexts[rng.IntN(len(describeTexts))])
	out := `{"name":` + string(name) + `,"description":` + string(description) + `,"parameters":` + randomObject(rng, 2)
	if rng.IntN(4) == 0 {
		out += `,"structuredOutputSchema":` + randomObject(rng, 2)
	}
	return out + `}`
}

// randomExtensions are extensions bringing random subsets of the tools: some declare a namespace (one without tools too, and
// two of the same name), some none; a tool several of them name belongs to the first.
func randomExtensions(rng *rand.Rand, tools []json.RawMessage) []describeExt {
	names := make([]string, len(tools))
	for i, raw := range tools {
		var named struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &named); err != nil {
			panic(err)
		}
		names[i] = named.Name
	}
	all := []*describeNS{
		{Name: "github", Description: "GitHub MCP server"},
		{Name: "slack", Description: "  Slack\nMCP server  ", Instructions: "ignored here"},
		{Name: "slackbot", Description: "Bot"},
		{Name: "mcp", Description: "Every MCP tool"},
		{Name: "Apple", Description: " "},
		{Name: "zeta", Description: "Last", Instructions: "Z first"},
		{Name: "Éclair", Description: ""},
		{Name: "github", Description: "The same name, later"},
		{Name: "aaa", Description: "Sorted first"},
		nil,
	}
	var picked []describeExt
	for i, ns := range all {
		if rng.IntN(2) == 0 {
			continue
		}
		extension := describeExt{Name: fmt.Sprintf("extension-%d", i), Tools: []string{}, Namespace: ns}
		for _, name := range names {
			if rng.IntN(3) == 0 {
				extension.Tools = append(extension.Tools, name)
			}
		}
		picked = append(picked, extension)
	}
	rng.Shuffle(len(picked), func(i, j int) { picked[i], picked[j] = picked[j], picked[i] })
	return append([]describeExt{}, picked...)
}

// namespaceOracleSkip skips a test that needs Pi's code mode namespaces declared by extensions (Pi 5e24c6b1f, after the committed
// pin 1.5.2): the pin's mirror has describe.ts with prefix namespaces and cannot answer. `make test-carried-oracles` runs the test
// with the effective pin overridden to that commit; a pin that reaches the commit runs it in every run, and this skip, the make
// target's entry and CARRIED_ORACLE_* go with that pin bump (the skip is the one pioracle.Bin gives an oracle that is not built).
func namespaceOracleSkip(t testing.TB) {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "durable", "src", "codemode", "describe.ts"))
	if err != nil || !strings.Contains(string(source), "export function namespacesOf(") {
		t.Skip("the pin's Pi mirror has prefix namespaces (Pi 5e24c6b1f declares them in extensions, after the committed pin): run make test-carried-oracles")
	}
}

func askDescribePi(t *testing.T, ops []describeOp) []describeAnswer {
	t.Helper()
	namespaceOracleSkip(t)
	input, err := json.Marshal(ops)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/describe_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
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
	return answers
}

// registration is the tool as the harness holds it: the schema decoded the way the tool search decodes one, which
// keeps the property order, and a structured output schema as a map.
func registration(t *testing.T, raw json.RawMessage) *durable.ToolRegistration {
	t.Helper()
	var schema ai.ToolSchema
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	tool := &durable.ToolRegistration{ToolSchema: schema}
	var extra struct {
		Structured map[string]any `json:"structuredOutputSchema"`
	}
	if err := json.Unmarshal(raw, &extra); err != nil {
		t.Fatal(err)
	}
	tool.StructuredOutputSchema = extra.Structured
	return tool
}

// extensionsOf are the extensions of an op as the harness holds them: tools by name, the namespace declared.
func extensionsOf(in []describeExt) []*durable.Extension {
	out := make([]*durable.Extension, len(in))
	for i, each := range in {
		out[i] = &durable.Extension{Name: each.Name}
		for _, name := range each.Tools {
			out[i].Tools = append(out[i].Tools, &durable.ToolRegistration{ToolSchema: ai.ToolSchema{Name: name}})
		}
		if each.Namespace != nil {
			out[i].Namespace = &durable.ToolNamespace{Name: each.Namespace.Name, Description: each.Namespace.Description, Instructions: each.Namespace.Instructions}
		}
	}
	return out
}

func TestDescribeMatchesPiOnSeededToolSets(t *testing.T) {
	namespaceOracleSkip(t)
	rng := rand.New(rand.NewPCG(20261117, 3))
	ops := []describeOp{{Op: "constants"}, {Op: "description", Models: false}, {Op: "description", Models: true}}
	type setup struct {
		tools      []json.RawMessage
		extensions []describeExt
	}
	var sets []setup
	for i := range 14 {
		var tools []json.RawMessage
		for j := range 1 + rng.IntN(8) {
			tools = append(tools, json.RawMessage(randomTool(rng, i*10+j)))
		}
		sets = append(sets, setup{tools, randomExtensions(rng, tools)})
	}
	sets = append(sets, setup{[]json.RawMessage{}, []describeExt{}}, setup{[]json.RawMessage{}, randomExtensions(rng, []json.RawMessage{json.RawMessage(`{"name":"x"}`)})})
	// The upper end of each sweep: a budget no tool set needs more than.
	full := make([]describeOp, len(sets))
	for i, set := range sets {
		full[i] = describeOp{Op: "section", Tools: set.tools, Extensions: set.extensions, Budget: 1 << 30}
	}
	fullAnswers := askDescribePi(t, full)
	for i, set := range sets {
		var text string
		if fullAnswers[i].OK != nil {
			_ = json.Unmarshal(*fullAnswers[i].OK, &text)
		}
		for budget := 0; budget <= len(text)/4+8; budget++ {
			ops = append(ops, describeOp{Op: "section", Tools: set.tools, Extensions: set.extensions, Budget: budget})
		}
		for _, tool := range set.tools {
			ops = append(ops, describeOp{Op: "sample", Tool: tool})
		}
	}
	// The namespace of every tool, over extensions that bring the same tools in different orders.
	named := []json.RawMessage{}
	for _, name := range []string{"read", "mcp__github__list_issues", "mcp__github__x", "mcp__slack__post", "mcp__slackbot__ping", "mcp__other", "my-tool", "zed", "Éa", "", "mcp__", "mcp"} {
		quoted, _ := json.Marshal(name)
		named = append(named, json.RawMessage(`{"name":`+string(quoted)+`}`))
	}
	for range 12 {
		ops = append(ops, describeOp{Op: "namespacesOf", Extensions: randomExtensions(rng, named)})
	}
	answers := askDescribePi(t, ops)
	texts, sections := 0, 0
	for i, op := range ops {
		var want string
		wantNil := answers[i].OK == nil
		if answers[i].Err != nil {
			t.Fatalf("op %d (%s): Pi threw %q on %s", i, op.Op, *answers[i].Err, func() string { b, _ := json.Marshal(op); return string(b) }())
		}
		if !wantNil && op.Op != "namespacesOf" && op.Op != "constants" {
			if err := json.Unmarshal(*answers[i].OK, &want); err != nil {
				t.Fatal(err)
			}
		}
		switch op.Op {
		case "constants":
			var pi struct {
				Budget int `json:"budget"`
			}
			if err := json.Unmarshal(*answers[i].OK, &pi); err != nil || DefaultInlineBudget != pi.Budget {
				t.Errorf("DefaultInlineBudget = %d, Pi %d (%v)", DefaultInlineBudget, pi.Budget, err)
			}
		case "description":
			if got := Description(op.Models); got != want {
				t.Errorf("Description(%v) differs from Pi", op.Models)
			}
		case "sample":
			got, err := sampleOf(registration(t, op.Tool))
			if err != nil {
				t.Fatal(err)
			}
			texts++
			if got != want {
				t.Errorf("sampleOf(%s) = %q, Pi %q", op.Tool, got, want)
			}
		case "section":
			tools := make([]*durable.ToolRegistration, len(op.Tools))
			for j, raw := range op.Tools {
				tools[j] = registration(t, raw)
			}
			got, err := Section(tools, extensionsOf(op.Extensions), op.Budget)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case wantNil && got != nil:
				t.Errorf("Section with no tools = %q, Pi undefined", *got)
			case !wantNil && got == nil:
				t.Errorf("budget %d: Section = nil, Pi %q", op.Budget, want)
			case !wantNil && *got != want:
				t.Errorf("budget %d of set %d: Section differs from Pi\nPiG:\n%s\nPi:\n%s", op.Budget, i, *got, want)
			}
			sections++
		case "namespacesOf":
			var piNames map[string]string
			if err := json.Unmarshal(*answers[i].OK, &piNames); err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for tool, namespace := range NamespacesOf(extensionsOf(op.Extensions)) {
				got[tool] = namespace.Name
			}
			if !reflect.DeepEqual(got, piNames) {
				t.Errorf("NamespacesOf = %v, Pi %v", got, piNames)
			}
		}
	}
	if sections < 500 || texts < 20 {
		t.Errorf("only %d sections and %d samples reached the comparison", sections, texts)
	}
}
