package toolsearch

// pi: packages/durable/src/tool-search/tool-search.ts

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand/v2"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's tool_search tool (tool-search.ts, imported as-is under Node) runs for seeded random deferred tools and queries; the
// Go tool must give the same result for each: the same error text for an empty query or a limit that is not a positive
// integer, and otherwise the same output text, details and control. The tools carry the shapes the search text and the
// result text branch on: parameters with descriptions at every level (properties, items, anyOf, oneOf, allOf), blank and
// multi-line descriptions, names with underscores, and queries that match one, several or none of them. The tool's own
// declaration (name, description, parameters, callers, replay mode) is compared too.

type toolSearchCase struct {
	Deferred []json.RawMessage `json:"deferred"`
	Query    string            `json:"query"`
	Limit    *float64          `json:"limit"`
}

type toolSearchAnswer struct {
	Result *struct {
		Output []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"output"`
		Details map[string]any `json:"details"`
		Control map[string]any `json:"control"`
	} `json:"result"`
	Error *string `json:"error"`
}

var toolSearchWords = []string{"github", "issue", "issues", "repo", "repos", "list", "get", "create", "search", "file", "files", "path", "query", "number", "user", "label", "labels", "comment", "pull", "request", "branch", "commit"}

var toolSearchDescriptions = []string{
	"", "   ", "Get a GitHub issue by number", "List the repositories of a user\nSecond line", "  \n  leading blank lines\nthen text",
	"Windows\r\nline endings\r\nhere", "Lone\rcarriage return", "Trailing spaces   \nnext", "Search code across repositories", "\tTabbed description\t",
	"Create an issue. Labels and comments are optional.", "Unicode: é日本 😀", "Plain",
}

func toolSearchSchema(rng *rand.Rand, depth int) string {
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	desc := func() string {
		if rng.IntN(2) == 0 {
			return `,"description":` + quote(toolSearchWords[rng.IntN(len(toolSearchWords))]+" "+toolSearchWords[rng.IntN(len(toolSearchWords))])
		}
		return ""
	}
	pick := rng.IntN(7)
	if depth <= 0 {
		pick = rng.IntN(3)
	}
	switch pick {
	case 0:
		return `{"type":"string"` + desc() + `}`
	case 1:
		return `{"type":"number"` + desc() + `}`
	case 2:
		return `{"type":"boolean"}`
	case 3:
		return `{"type":"array","items":` + toolSearchSchema(rng, depth-1) + desc() + `}`
	case 4:
		return `{"anyOf":[` + toolSearchSchema(rng, depth-1) + `,` + toolSearchSchema(rng, depth-1) + `]` + desc() + `}`
	case 5:
		key := []string{"oneOf", "allOf"}[rng.IntN(2)]
		return `{"` + key + `":[` + toolSearchSchema(rng, depth-1) + `,` + toolSearchSchema(rng, depth-1) + `]}`
	default:
		return toolSearchObject(rng, depth-1)
	}
}

func toolSearchObject(rng *rand.Rand, depth int) string {
	var props []string
	used := map[string]bool{}
	for range rng.IntN(4) {
		name := toolSearchWords[rng.IntN(len(toolSearchWords))]
		if used[name] {
			continue
		}
		used[name] = true
		b, _ := json.Marshal(name)
		props = append(props, string(b)+":"+toolSearchSchema(rng, depth))
	}
	out := `{"type":"object","properties":{` + strings.Join(props, ",") + `}`
	if rng.IntN(3) == 0 {
		b, _ := json.Marshal(toolSearchWords[rng.IntN(len(toolSearchWords))] + " object")
		out += `,"description":` + string(b)
	}
	return out + `}`
}

func toolSearchTools(rng *rand.Rand, count int) []json.RawMessage {
	tools := []json.RawMessage{}
	used := map[string]bool{}
	for range count {
		name := toolSearchWords[rng.IntN(len(toolSearchWords))] + "_" + toolSearchWords[rng.IntN(len(toolSearchWords))]
		if rng.IntN(4) == 0 {
			name += "_" + toolSearchWords[rng.IntN(len(toolSearchWords))]
		}
		if used[name] {
			continue
		}
		used[name] = true
		nameJSON, _ := json.Marshal(name)
		description, _ := json.Marshal(toolSearchDescriptions[rng.IntN(len(toolSearchDescriptions))])
		tools = append(tools, json.RawMessage(`{"name":`+string(nameJSON)+`,"description":`+string(description)+`,"parameters":`+toolSearchObject(rng, 2)+`}`))
	}
	return tools
}

type oracleAPI struct {
	durable.ToolExecutionApi
	deferred []*durable.ToolRegistration
}

func (api oracleAPI) Agent(context.Context) (durable.Agent, error) {
	return durable.Agent{Deferred: api.deferred}, nil
}

func TestToolSearchMatchesPiOnSeededDeferredTools(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261118, 5))
	limits := []*float64{nil, new(1.0), new(2.0), new(3.0), new(8.0), new(100.0), new(0.0), new(-1.0), new(2.5), new(0.5), new(1e21), new(1e300)}
	queries := []string{"", "   ", "\n", "issue", "issues", "GitHub issue", "list repos", "the of a", "zzzz", "create_issue", "searchCode", "files path", "labels comments", "x"}
	var cases []toolSearchCase
	for range 500 {
		tools := toolSearchTools(rng, rng.IntN(14))
		for range 12 {
			query := queries[rng.IntN(len(queries))]
			if rng.IntN(2) == 0 {
				query = toolSearchWords[rng.IntN(len(toolSearchWords))] + " " + toolSearchWords[rng.IntN(len(toolSearchWords))]
			}
			cases = append(cases, toolSearchCase{Deferred: tools, Query: query, Limit: limits[rng.IntN(len(limits))]})
		}
	}
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/tool_search_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var pi struct {
		Declaration struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			Parameters  map[string]any `json:"parameters"`
			Callers     []string       `json:"callers"`
			Replay      string         `json:"replay"`
		} `json:"declaration"`
		Constants struct {
			Name  string `json:"name"`
			Limit int    `json:"limit"`
		} `json:"constants"`
		Extension struct {
			Name  string   `json:"name"`
			Tools []string `json:"tools"`
		} `json:"extension"`
		Out []toolSearchAnswer `json:"out"`
	}
	if err := json.Unmarshal(output, &pi); err != nil || len(pi.Out) != len(cases) {
		t.Fatalf("oracle output: %v (%d answers for %d cases)", err, len(pi.Out), len(cases))
	}

	extension := CreateExtension()
	if extension.Name != pi.Extension.Name || len(extension.Tools) != len(pi.Extension.Tools) || len(extension.Tools) != 1 || extension.Tools[0].Name != pi.Extension.Tools[0] {
		t.Errorf("extension %q with %d tools, Pi %+v", extension.Name, len(extension.Tools), pi.Extension)
	}
	tool := CreateTool()
	if tool.Name != pi.Declaration.Name || tool.Name != pi.Constants.Name || ToolName != pi.Constants.Name || DefaultLimit != pi.Constants.Limit {
		t.Errorf("name %q / %q, default limit %d; Pi %q / %q, %d", tool.Name, ToolName, DefaultLimit, pi.Declaration.Name, pi.Constants.Name, pi.Constants.Limit)
	}
	if tool.Description != pi.Declaration.Description {
		t.Errorf("description %q, Pi %q", tool.Description, pi.Declaration.Description)
	}
	var ours map[string]any
	encoded, err := json.Marshal(tool.Parameters)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &ours); err != nil || !reflect.DeepEqual(ours, pi.Declaration.Parameters) {
		t.Errorf("parameters %s, Pi %v", encoded, pi.Declaration.Parameters)
	}
	if len(tool.Callers) != len(pi.Declaration.Callers) || (len(tool.Callers) == 1 && string(tool.Callers[0]) != pi.Declaration.Callers[0]) || string(tool.Replay) != pi.Declaration.Replay {
		t.Errorf("callers %v, replay %q; Pi %v, %q", tool.Callers, tool.Replay, pi.Declaration.Callers, pi.Declaration.Replay)
	}

	loadedSome, none, failed := 0, 0, 0
	for i, c := range cases {
		deferred := make([]*durable.ToolRegistration, len(c.Deferred))
		for j, raw := range c.Deferred {
			var schema ai.ToolSchema
			if err := json.Unmarshal(raw, &schema); err != nil {
				t.Fatal(err)
			}
			deferred[j] = &durable.ToolRegistration{ToolSchema: schema}
		}
		args := map[string]any{"query": c.Query}
		if c.Limit != nil {
			args["limit"] = *c.Limit
		}
		got, err := tool.Execute(t.Context(), args, oracleAPI{deferred: deferred})
		want := pi.Out[i]
		label := func() string { b, _ := json.Marshal(c); return string(b) }
		switch {
		case want.Error != nil:
			failed++
			if err == nil {
				t.Errorf("%s: Pi fails with %q, PiG returns %+v", label(), *want.Error, got)
			} else if err.Error() != *want.Error {
				t.Errorf("%s: PiG error %q, Pi %q", label(), err, *want.Error)
			}
		case err != nil:
			t.Errorf("%s: PiG fails with %q, Pi returns %+v", label(), err, want.Result)
		default:
			if len(got.Output) != len(want.Result.Output) || len(got.Output) != 1 {
				t.Errorf("%s: %d output items, Pi %d", label(), len(got.Output), len(want.Result.Output))
				continue
			}
			text, ok := got.Output[0].(ai.TextContent)
			if !ok || text.Text != want.Result.Output[0].Text || want.Result.Output[0].Type != "text" {
				t.Errorf("%s: output %+v, Pi %+v", label(), got.Output[0], want.Result.Output[0])
			}
			var details map[string]any
			encoded, _ := json.Marshal(got.Details)
			_ = json.Unmarshal(encoded, &details)
			if !got.HasDetails || !reflect.DeepEqual(details, want.Result.Details) {
				t.Errorf("%s: details %s, Pi %v", label(), encoded, want.Result.Details)
			}
			switch {
			case want.Result.Control == nil && got.Control != nil:
				t.Errorf("%s: control %+v, Pi none", label(), got.Control)
			case want.Result.Control != nil:
				var control map[string]any
				encoded, _ := json.Marshal(got.Control)
				_ = json.Unmarshal(encoded, &control)
				if !reflect.DeepEqual(control, want.Result.Control) {
					t.Errorf("%s: control %s, Pi %v", label(), encoded, want.Result.Control)
				}
			}
			if strings.HasPrefix(text.Text, "Loaded ") {
				loadedSome++
			} else {
				none++
			}
		}
	}
	if loadedSome < 100 || none < 50 || failed < 50 {
		t.Errorf("the corpus reached %d loads, %d empty results and %d failures", loadedSome, none, failed)
	}
}
