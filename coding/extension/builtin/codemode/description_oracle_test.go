package codemode_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/codemode"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// createCodemodeDescription against Pi's tool.js: the whole model-facing description for random tool sets (identifier-hostile names, descriptions with padding and non-ASCII,
// guidelines with blank entries, nested schemas, output schemas), namespaces, deferred tools, the models flag and inline budgets from none to a few tokens.
var docsPath = regexp.MustCompile(`Read \S*codemode\.md first`)

func firstDifference(got, want string) string {
	gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := range max(len(gl), len(wl)) {
		var g, w string
		if i < len(gl) {
			g = gl[i]
		}
		if i < len(wl) {
			w = wl[i]
		}
		if g != w {
			return fmt.Sprintf("line %d:\n  got  %q\n  want %q", i+1, g, w)
		}
	}
	return "identical"
}

func TestCodemodeDescriptionMatchesPi(t *testing.T) {
	probes := descriptionProbes()
	type outcome struct {
		Text string `json:"text"`
		Err  string `json:"err"`
	}
	var want []outcome
	pioracle.Run(t, `
const m = await load("pi-coding-agent/extensions/codemode/tool.js");
emit(input.map((p) => {
	try {
		const options = {
			models: p.models,
			namespaces: new Map(Object.entries(p.namespaces)),
			deferred: new Set(p.deferred ?? []),
			guidelines: new Map(Object.entries(p.guidelines)),
			inlineBudget: p.budget ?? undefined,
		};
		return { text: m.createCodemodeDescription(p.tools, options), err: "" };
	} catch (e) { return { text: "", err: String(e && e.message) }; }
}));
`, probes, &want, "HOME=/home/oracle")
	bad := 0
	for i, p := range probes {
		got, errText := goDescription(p)
		// The docs path names the agent directory, which the Pi process and this test place differently.
		got, wantText := docsPath.ReplaceAllString(got, "Read <docs> first"), docsPath.ReplaceAllString(want[i].Text, "Read <docs> first")
		if got != wantText || errText != want[i].Err {
			if bad++; bad <= 3 {
				t.Errorf("probe %d (%d tools, models=%v, budget=%v): err=%q want %q\n%s", i, len(p.Tools), p.Models, p.Budget, errText, want[i].Err, firstDifference(got, wantText))
			}
		}
	}
	if bad > 3 {
		t.Errorf("%d of %d descriptions differ from Pi", bad, len(probes))
	}
}

type oracleTool struct {
	Name         string `json:"name"`
	Label        string `json:"label"`
	Description  string `json:"description"`
	Parameters   any    `json:"parameters"`
	OutputSchema any    `json:"outputSchema,omitempty"`
}
type oracleNamespace struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Instructions string `json:"instructions,omitempty"`
}
type descriptionProbe struct {
	Tools      []oracleTool               `json:"tools"`
	Models     bool                       `json:"models"`
	Namespaces map[string]oracleNamespace `json:"namespaces"`
	Deferred   []string                   `json:"deferred"`
	Guidelines map[string][]string        `json:"guidelines"`
	Budget     *int                       `json:"budget"`
}

// descriptionProbes is 600 seeded tool sets with namespaces, deferred tools, guidelines, the models flag and inline budgets.
func descriptionProbes() []descriptionProbe {
	rng := rand.New(rand.NewSource(12))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	name := func() string {
		return pick("read", "read_notes", "mcp__github__issues", "mcp__github__list-pulls", "mcp__docs__search", "my tool", "class", "1abc", "a.b", "über", "日本語", "x-y", "a_b", "A_B", "mcp__s__t", "codemode", "delete", "𝒳tool", "return", "tool_search")
	}
	text := func() string {
		return pick("", "Reads a file.", "  padded description \n", "Multi\nline\n\ndescription", "Contains */ comment end", "日本語の説明", strings.Repeat("Long ", 80), "Emoji 😀 tool.", "\ttabbed", "Trailing space  ", "\ufeffBOM padded\u00a0", "\u0085NEL kept\u0085", "\ufeff")
	}
	var schema func(depth int) any
	schema = func(depth int) any {
		prim := []any{
			map[string]any{"type": "string"}, map[string]any{"type": "number"}, map[string]any{"type": "integer"}, map[string]any{"type": "boolean"},
			map[string]any{"type": "string", "enum": []any{"a", "b c"}}, map[string]any{"const": "x"}, map[string]any{"type": []any{"string", "null"}}, map[string]any{},
		}
		if depth == 0 || rng.Intn(3) == 0 {
			return prim[rng.Intn(len(prim))]
		}
		switch rng.Intn(4) {
		case 0:
			return map[string]any{"type": "array", "items": schema(depth - 1)}
		case 1:
			return map[string]any{"anyOf": []any{schema(depth - 1), schema(depth - 1)}}
		default:
			props := map[string]any{}
			var required []any
			for range 1 + rng.Intn(3) {
				key := pick("path", "limit", "my-key", "a b", "class", "x.y", "日本", "id")
				p := schema(depth - 1)
				if m, ok := p.(map[string]any); ok && rng.Intn(2) == 0 {
					m["description"] = pick("The path.", "multi\nline", "has */ end", "")
				}
				props[key] = p
				if rng.Intn(2) == 0 {
					required = append(required, key)
				}
			}
			s := map[string]any{"type": "object", "properties": props}
			if len(required) > 0 {
				s["required"] = required
			}
			return s
		}
	}
	var probes []descriptionProbe
	for range 600 {
		var p descriptionProbe
		seen := map[string]bool{}
		for range 1 + rng.Intn(8) {
			n := name()
			if seen[n] {
				continue
			}
			seen[n] = true
			tl := oracleTool{Name: n, Label: n, Description: text(), Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
			if rng.Intn(2) == 0 {
				tl.Parameters = schema(3)
			}
			if rng.Intn(3) == 0 {
				tl.OutputSchema = schema(2)
			}
			if rng.Intn(6) == 0 {
				// An MCP CallToolResult output schema: content array of objects, boolean isError, object _meta and an optional structuredContent.
				properties := map[string]any{"content": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}, "isError": map[string]any{"type": "boolean"}, "_meta": map[string]any{"type": "object"}}
				switch rng.Intn(3) {
				case 0:
					properties["structuredContent"] = schema(2)
				case 1:
					properties["structuredContent"] = true
				}
				tl.OutputSchema = map[string]any{"type": "object", "properties": properties}
			}
			p.Tools = append(p.Tools, tl)
		}
		p.Models = rng.Intn(2) == 0
		p.Namespaces = map[string]oracleNamespace{}
		p.Guidelines = map[string][]string{}
		for _, tl := range p.Tools {
			if rng.Intn(3) == 0 {
				ns := oracleNamespace{Name: pick("mcp__github", "mcp__docs", "Alpha", "zeta", "my server", "Zeta", "alpha", "béta", "Beta", "mcp__Docs")}
				if rng.Intn(2) == 0 {
					ns.Description = pick("GitHub server", "  spaced ", "multi\nline", "\ufeff", "\u0085server\u00a0")
				}
				if rng.Intn(3) == 0 {
					ns.Instructions = "Be careful."
				}
				p.Namespaces[tl.Name] = ns
			}
			if rng.Intn(6) == 0 {
				p.Deferred = append(p.Deferred, tl.Name)
			}
			if rng.Intn(4) == 0 {
				p.Guidelines[tl.Name] = []string{pick("Use it.", "  ", "", " padded guideline ", "multi\nline", "\ufeff", "\u0085", "\u00a0guide\ufeff")}
				if rng.Intn(2) == 0 {
					p.Guidelines[tl.Name] = append(p.Guidelines[tl.Name], "Second.")
				}
			}
		}
		if rng.Intn(2) == 0 {
			// Fixed budgets, and random ones so that a section's cost sometimes equals the remaining budget exactly.
			b := []int{0, 1, 20, 60, 150, 400, 1000, 3000}[rng.Intn(8)]
			if rng.Intn(3) != 0 {
				b = rng.Intn(500)
			}
			p.Budget = &b
		}
		probes = append(probes, p)
	}
	return probes
}

// goDescription is PiG's description of a probe and the text of its error.
func goDescription(p descriptionProbe) (string, string) {
	var tools []extension.AgentTool
	for _, tl := range p.Tools {
		params, _ := json.Marshal(tl.Parameters)
		at := extension.AgentTool{Name: tl.Name, Label: tl.Label, Description: tl.Description, Parameters: params}
		if tl.OutputSchema != nil {
			at.OutputSchema, _ = json.Marshal(tl.OutputSchema)
		}
		tools = append(tools, at)
	}
	options := codemode.DescriptionOptions{Models: p.Models, Namespaces: map[string]extension.ToolNamespace{}, Deferred: map[string]bool{}, Guidelines: p.Guidelines, InlineBudget: p.Budget}
	for k, ns := range p.Namespaces {
		options.Namespaces[k] = extension.ToolNamespace{Name: ns.Name, Description: ns.Description, Instructions: ns.Instructions}
	}
	for _, d := range p.Deferred {
		options.Deferred[d] = true
	}
	got, err := codemode.CreateDescription(tools, options)
	if err != nil {
		return got, err.Error()
	}
	return got, ""
}

// TestCodemodeDescriptionProbesDump prints the corpus for the Pi side of the extensions-runtime/79 parity scenario.
func TestCodemodeDescriptionProbesDump(t *testing.T) {
	line, err := json.Marshal(descriptionProbes())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("codemode-description-probes:%s\n", line)
}

// TestCodemodeDescriptionProbesParity prints PiG's description of each probe as one JSON line [text, error], the docs path replaced, for the extensions-runtime/79 parity scenario.
func TestCodemodeDescriptionProbesParity(t *testing.T) {
	for _, p := range descriptionProbes() {
		text, errText := goDescription(p)
		var out bytes.Buffer
		encoder := json.NewEncoder(&out)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode([]string{docsPath.ReplaceAllString(text, "Read <docs> first"), errText}); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("codemode-description-observation:%s", out.String())
	}
}
