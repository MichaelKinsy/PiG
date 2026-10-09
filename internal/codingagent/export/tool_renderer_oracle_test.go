package export

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type toolOracleTool struct {
	Call   []string `json:"call"`
	Result *struct {
		Collapsed []string `json:"collapsed"`
		Expanded  []string `json:"expanded"`
	} `json:"result"`
	ThrowCall       bool `json:"throwCall"`
	ThrowCallRender bool `json:"throwCallRender"`
	ThrowResult     int  `json:"throwResult"`
	GetThrows       bool `json:"getThrows"`
	InfoFirst       bool `json:"infoFirst"`
	Plain           bool `json:"plain"`
}

type toolOracleBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

type toolOracleOp struct {
	Op      string            `json:"op"`
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Args    string            `json:"args,omitempty"`
	Content []toolOracleBlock `json:"content,omitempty"`
	Details *string           `json:"details"`
	IsError bool              `json:"isError,omitempty"`
}

type toolOracleScenario struct {
	Width int                        `json:"width"`
	Cwd   string                     `json:"cwd"`
	Tools map[string]*toolOracleTool `json:"tools"`
	Ops   []toolOracleOp             `json:"ops"`
}

type toolOracleOut struct {
	HTML      string `json:"html,omitempty"`
	Collapsed string `json:"collapsed,omitempty"`
	Expanded  string `json:"expanded,omitempty"`
}

var toolOracleLines = []string{"", " ", "\t", "\x1b[1m \x1b[0m", "\x1b[0m", "\u00a0", "\ufeff", "\u0085", "\u2028", "\u200b", "\u205f", "\u3000", "\u2009", "\u1680", "\u202f", "\u180e", "text", "\x1b[31mred\x1b[0m", "a<b&c>\"d'", "日本語", "😀", "\x1b[38;5;99mx", "\x1b[1;2m", "  indented", "trailing  ", "\x1b]8;;http://x\x07link\x1b]8;;\x07"}

func toolOracleScenarios() []toolOracleScenario {
	rng := rand.New(rand.NewPCG(5, 9))
	pickLines := func(max int) []string {
		lines := make([]string, rng.IntN(max+1))
		for i := range lines {
			lines[i] = toolOracleLines[rng.IntN(len(toolOracleLines))]
		}
		return lines
	}
	argsChoices := []string{`{"path":"a.txt"}`, `[1,2]`, `"s"`, `null`, `{}`, `7`, `true`}
	detailsChoices := []string{"", `1`, `"x"`, `[1,2]`, `{"a":1}`, `true`, `null`}
	names := []string{"t1", "t2", "callOnly", "resultOnly", "none", "throwCall", "throwRender", "throwResult1", "throwResult2", "throwResult3", "throwResult4", "lookupThrows", "unknown"}
	var scenarios []toolOracleScenario
	for range 1500 {
		scenario := toolOracleScenario{Width: []int{0, 0, 1, 20, 100, 200}[rng.IntN(6)], Cwd: []string{"/work", "", "/a b/c"}[rng.IntN(3)], Tools: map[string]*toolOracleTool{}}
		for _, name := range names {
			tool := &toolOracleTool{InfoFirst: rng.IntN(2) == 0, Plain: rng.IntN(4) == 0}
			switch name {
			case "unknown":
				continue
			case "none":
				scenario.Tools[name] = tool
				continue
			case "callOnly":
				tool.Call = pickLines(3)
			case "resultOnly":
			default:
				tool.Call = pickLines(3)
			}
			if name != "callOnly" {
				collapsed := pickLines(4)
				expanded := collapsed
				if rng.IntN(2) == 0 {
					expanded = append(append([]string(nil), collapsed...), pickLines(3)...)
				}
				if rng.IntN(4) == 0 {
					expanded = pickLines(4)
				}
				if expanded == nil {
					expanded = []string{}
				}
				tool.Result = &struct {
					Collapsed []string `json:"collapsed"`
					Expanded  []string `json:"expanded"`
				}{collapsed, expanded}
			}
			switch name {
			case "throwCall":
				tool.ThrowCall = true
			case "throwRender":
				tool.ThrowCallRender = true
				tool.ThrowResult = 3 + rng.IntN(2)
			case "throwResult1", "throwResult2", "throwResult3", "throwResult4":
				tool.ThrowResult = int(name[len(name)-1] - '0')
			case "lookupThrows":
				tool.GetThrows = true
			}
			scenario.Tools[name] = tool
		}
		for range 1 + rng.IntN(8) {
			op := toolOracleOp{ID: []string{"c1", "c2", "c3"}[rng.IntN(3)], Name: names[rng.IntN(len(names))]}
			if rng.IntN(2) == 0 {
				op.Op, op.Args = "call", argsChoices[rng.IntN(len(argsChoices))]
			} else {
				op.Op, op.IsError = "result", rng.IntN(4) == 0
				for range rng.IntN(3) {
					if rng.IntN(4) == 0 {
						op.Content = append(op.Content, toolOracleBlock{Type: "image", Data: "QUJD", MimeType: "image/png"})
					} else {
						op.Content = append(op.Content, toolOracleBlock{Type: "text", Text: toolOracleLines[rng.IntN(len(toolOracleLines))]})
					}
				}
				if d := detailsChoices[rng.IntN(len(detailsChoices))]; d != "" {
					op.Details = &d
				}
			}
			scenario.Ops = append(scenario.Ops, op)
		}
		scenarios = append(scenarios, scenario)
	}
	return scenarios
}

// oracleComponent is a component of fixed lines that records its identity, as the oracle's components do.
type oracleToolComponent struct {
	id          int
	lines       []string
	throwRender bool
	infoFirst   bool
	plain       bool
}

func (c *oracleToolComponent) Render(width int) []string {
	if c.throwRender {
		panic("render")
	}
	if c.plain {
		return append([]string(nil), c.lines...)
	}
	if c.infoFirst {
		return append([]string{fmt.Sprintf("w=%d", width)}, c.lines...)
	}
	return append(append([]string(nil), c.lines...), fmt.Sprintf("w=%d", width))
}
func (*oracleToolComponent) Invalidate() {}

// jsJSON is JSON.stringify for strings and string lists: no HTML escapes, and U+2028 and U+2029 written as they are.
func jsJSON(v any) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(v)
	return strings.NewReplacer("\\u2028", "\u2028", "\\u2029", "\u2029").Replace(strings.TrimSuffix(buf.String(), "\n"))
}

func oracleToolContextLine(kind string, ctx extension.ToolRenderContext, extra string) string {
	state := ctx.State.(map[string]any)
	n, _ := state["n"].(int)
	n++
	state["n"] = n
	last := "null"
	if ctx.LastComponent != nil {
		last = fmt.Sprint(ctx.LastComponent.(*oracleToolComponent).id)
	}
	args := "undefined"
	if raw, _ := ctx.Args.(json.RawMessage); len(raw) > 0 {
		args = string(raw)
	}
	duration := "undefined"
	if ctx.DurationMs != nil {
		duration = fmt.Sprint(*ctx.DurationMs)
	}
	argsValue, id, cwd, durationJSON := jsJSON(args), jsJSON(ctx.ToolCallID), jsJSON(ctx.Cwd), jsJSON(duration)
	return fmt.Sprintf(`ctx:{"kind":%q,"n":%d,"last":%s,"args":%s,"id":%s,"cwd":%s,"started":%v,"complete":%v,"partial":%v,"expanded":%v,"images":%v,"error":%v,"duration":%s,"pad":%d%s}`,
		kind, n, last, argsValue, id, cwd, ctx.ExecutionStarted, ctx.ArgsComplete, ctx.IsPartial, ctx.Expanded, ctx.ShowImages, ctx.IsError, durationJSON, ctx.OutputPad, extra)
}

func runToolOracleScenarioWithPig(scenario toolOracleScenario) []toolOracleOut {
	seq := 0
	getToolRenderers := func(name string) *extension.ToolRenderers {
		tool := scenario.Tools[name]
		if tool == nil {
			return nil
		}
		if tool.GetThrows {
			panic("lookup")
		}
		renderers := &extension.ToolRenderers{}
		if tool.Call != nil {
			renderers.RenderCall = func(args json.RawMessage, theme extension.Theme, ctx extension.ToolRenderContext) extension.Component {
				if tool.ThrowCall {
					panic("call")
				}
				seq++
				info := oracleToolContextLine("call", ctx, "")
				lines := append(append([]string(nil), tool.Call...), info)
				if tool.InfoFirst {
					lines = append([]string{info}, tool.Call...)
				}
				return &oracleToolComponent{id: seq, lines: lines, throwRender: tool.ThrowCallRender, infoFirst: tool.InfoFirst}
			}
		}
		if tool.Result != nil {
			renderers.RenderResult = func(result extension.AgentToolResult, options extension.ToolRenderResultOptions, theme extension.Theme, ctx extension.ToolRenderContext) extension.Component {
				if (!options.Expanded && tool.ThrowResult == 1) || (options.Expanded && tool.ThrowResult == 2) {
					panic("result")
				}
				texts := make([]string, 0, len(result.Content))
				for _, block := range result.Content {
					switch b := block.(type) {
					case ai.TextContent:
						texts = append(texts, b.Text)
					case ai.ImageContent:
						texts = append(texts, "[image:"+b.MimeType+"]")
					}
				}
				textsJSON := jsJSON(texts)
				details := "none"
				if result.Details != nil {
					encoded, _ := json.Marshal(result.Details)
					details = string(encoded)
				}
				detailsJSON := jsJSON(details)
				extra := fmt.Sprintf(`,"texts":%s,"details":%s,"isErrorResult":%v,"optionExpanded":%v,"optionPartial":%v,"themeIsGiven":%v`, textsJSON, detailsJSON, result.IsError, options.Expanded, options.IsPartial, theme != nil)
				lines := tool.Result.Collapsed
				throwRender := tool.ThrowResult == 3
				if options.Expanded {
					lines, throwRender = tool.Result.Expanded, tool.ThrowResult == 4
				}
				seq++
				if tool.Plain {
					return &oracleToolComponent{id: seq, lines: append([]string(nil), lines...), plain: true}
				}
				info := oracleToolContextLine("result", ctx, extra)
				all := append(append([]string(nil), lines...), info)
				if tool.InfoFirst {
					all = append([]string{info}, lines...)
				}
				return &oracleToolComponent{id: seq, lines: all, throwRender: throwRender, infoFirst: tool.InfoFirst}
			}
		}
		return renderers
	}
	renderer := newToolHTMLRenderer(getToolRenderers, scenario.Cwd, scenario.Width)
	var outs []toolOracleOut
	for _, op := range scenario.Ops {
		if op.Op == "call" {
			outs = append(outs, toolOracleOut{HTML: renderer.renderCall(op.ID, op.Name, json.RawMessage(op.Args))})
			continue
		}
		result := agent.AgentToolResult{IsError: op.IsError}
		for _, block := range op.Content {
			if block.Type == "text" {
				result.Content = append(result.Content, ai.TextContent{Text: block.Text})
			} else {
				result.Content = append(result.Content, ai.ImageContent{Data: block.Data, MimeType: block.MimeType})
			}
		}
		if op.Details != nil {
			var details any
			_ = json.Unmarshal([]byte(*op.Details), &details)
			result.Details = details
		}
		out := renderer.renderResult(op.ID, op.Name, result)
		outs = append(outs, toolOracleOut{Collapsed: out.ResultHTMLCollapsed, Expanded: out.ResultHTMLExpanded})
	}
	return outs
}

// createToolHtmlRenderer (core/export-html/tool-renderer.ts) against pinned Pi over 1500 seeded scenarios: calls and results of tools with call-only,
// result-only and no renderers, renderers that throw (when looked up, when called, when rendered, in the collapsed or the expanded pass), components that
// report the context they got (the per-call state, the previous component of the call or result, the stored arguments, the flags) and result lines made of
// blank and ANSI-only lines the trimming has to drop; a call that comes first, last, twice or never.
func TestToolHTMLRendererMatchesPi(t *testing.T) {
	scenarios := toolOracleScenarios()
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/tool_renderer.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]toolOracleOut
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, scenario := range scenarios {
		got := runToolOracleScenarioWithPig(scenario)
		g, _ := json.Marshal(got)
		e, _ := json.Marshal(expected[i])
		if !bytes.Equal(g, e) {
			if failures++; failures <= 4 {
				for k := range got {
					gk, _ := json.Marshal(got[k])
					ek, _ := json.Marshal(expected[i][k])
					if bytes.Equal(gk, ek) {
						continue
					}
					at := 0
					for at < len(gk) && at < len(ek) && gk[at] == ek[at] {
						at++
					}
					lo := max(at-120, 0)
					op, _ := json.Marshal(scenario.Ops[k])
					t.Errorf("scenario %d op %d %s (earlier ops: %d):\n  Pig ...%s\n  Pi  ...%s", i, k, op, k, gk[lo:min(at+160, len(gk))], ek[lo:min(at+160, len(ek))])
					break
				}
			}

		}
	}
	if failures > 4 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, len(scenarios))
	}
}

// TestToolHTMLRendererProbeDump prints the scenarios for the Pi side of the tool-renderer parity scenario.
func TestToolHTMLRendererProbeDump(t *testing.T) {
	line, err := json.Marshal(toolOracleScenarios())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("toolrenderer-probes:%s\n", line)
}

// TestToolHTMLRendererParity prints Pig's outputs, one JSON line per scenario, for the tool-renderer parity scenario.
func TestToolHTMLRendererParity(t *testing.T) {
	for _, scenario := range toolOracleScenarios() {
		// The stream keeps Go's escapes for U+2028 and U+2029: the reader's line pattern would stop at a raw one.
		var line bytes.Buffer
		encoder := json.NewEncoder(&line)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(runToolOracleScenarioWithPig(scenario)); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("toolrenderer-observation:%s", line.String())
	}
}
