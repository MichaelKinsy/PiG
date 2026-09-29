// Ports packages/coding-agent/src/core/export-html/tool-renderer.ts
package export

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

type renderableComponent interface {
	Render(width int) []string
}

type renderedToolHTML struct {
	CallHTML            string `json:"callHtml,omitempty"`
	ResultHTMLCollapsed string `json:"resultHtmlCollapsed,omitempty"`
	ResultHTMLExpanded  string `json:"resultHtmlExpanded,omitempty"`
}

type toolHTMLRenderer struct {
	defs             map[string]extension.ToolDefinition
	cwd              string
	width            int
	renderedCall     map[string]any
	renderedResult   map[string]any
	renderedState    map[string]any
	renderedArgsJSON map[string]json.RawMessage
	// cardPrefix names this export's renderer cards in extension processes.
	cardPrefix string
	// unresponsive are tools whose extension did not answer a render.
	unresponsive map[string]bool
}

func newToolHTMLRenderer(tools []extension.RegisteredTool, cwd string, width int) *toolHTMLRenderer {
	defs := make(map[string]extension.ToolDefinition, len(tools))
	for _, tool := range tools {
		defs[tool.Definition.Name] = tool.Definition
	}
	if width <= 0 {
		width = 100
	}
	return &toolHTMLRenderer{
		defs:             defs,
		cwd:              cwd,
		width:            width,
		renderedCall:     map[string]any{},
		renderedResult:   map[string]any{},
		renderedState:    map[string]any{},
		renderedArgsJSON: map[string]json.RawMessage{},
		cardPrefix:       "export-" + strconv.FormatUint(exportCardSeq.Add(1), 10) + "-",
		unresponsive:     map[string]bool{},
	}
}

var exportCardSeq atomic.Uint64

func (r *toolHTMLRenderer) getState(toolCallID string) any {
	if state, ok := r.renderedState[toolCallID]; ok {
		return state
	}
	state := map[string]any{}
	r.renderedState[toolCallID] = state
	return state
}

func (r *toolHTMLRenderer) renderContext(toolCallID string, lastComponent any, expanded, isPartial, isError bool) extension.ToolRenderContext {
	return extension.ToolRenderContext{
		Args:             r.renderedArgsJSON[toolCallID],
		ToolCallID:       toolCallID,
		Invalidate:       func() {},
		LastComponent:    lastComponent,
		State:            r.getState(toolCallID),
		Cwd:              r.cwd,
		ExecutionStarted: true,
		ArgsComplete:     true,
		IsPartial:        isPartial,
		Expanded:         expanded,
		ShowImages:       false,
		IsError:          isError,
		Card:             r.cardPrefix + toolCallID,
	}
}

var ansiEscapeRegex = lazyregexp.New(`\x1b\[[\d;]*m`)

func isBlankRenderedLine(line string) bool {
	// ECMAScript WhiteSpace and LineTerminator include BOM but not NEL; only SGR sequences are stripped.
	const whitespace = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"
	return strings.Trim(ansiEscapeRegex.ReplaceAllString(line, ""), whitespace) == ""
}

func trimRenderedResultLines(lines []string) []string {
	start := 0
	end := len(lines)
	for start < end && isBlankRenderedLine(lines[start]) {
		start++
	}
	for end > start && isBlankRenderedLine(lines[end-1]) {
		end--
	}
	return lines[start:end]
}

// synchronousRenderer is a component an extension process renders: the
// export asks it for its frame and waits, as upstream calls the renderer and
// renders the component in one step.
type synchronousRenderer interface {
	RenderNow(width int) (lines []string, failed, answered bool)
}

// componentLines renders a renderer's component at width. ok is false when
// the renderer threw or its extension did not answer, which upstream's
// try/catch turns into the default tool rendering.
func (r *toolHTMLRenderer) componentLines(toolName string, component any) (lines []string, ok bool) {
	if remote, isRemote := component.(synchronousRenderer); isRemote {
		if r.unresponsive[toolName] {
			return nil, false
		}
		lines, failed, answered := remote.RenderNow(r.width)
		if !answered {
			// A renderer that exceeded the inactivity boundary is not asked
			// again during this export, so one stalled extension costs one
			// boundary rather than one per tool call.
			r.unresponsive[toolName] = true
		}
		return lines, answered && !failed
	}
	renderable, isRenderable := component.(renderableComponent)
	if !isRenderable || renderable == nil {
		return nil, false
	}
	return renderable.Render(r.width), true
}

func (r *toolHTMLRenderer) renderCall(toolCallID, toolName string, argsJSON json.RawMessage) (html string) {
	def, ok := r.defs[toolName]
	if !ok || def.RenderCall == nil {
		return ""
	}
	defer func() {
		// upstream: packages/coding-agent/src/core/export-html/tool-renderer.ts:renderCall
		if recover() != nil {
			html = ""
		}
	}()
	r.renderedArgsJSON[toolCallID] = argsJSON
	component := def.RenderCall(argsJSON, tui.ActiveTheme(), r.renderContext(toolCallID, r.renderedCall[toolCallID], false, true, false))
	r.renderedCall[toolCallID] = component
	lines, ok := r.componentLines(toolName, component)
	if !ok {
		return ""
	}
	return ansiLinesToHTML(lines)
}

func (r *toolHTMLRenderer) renderResult(toolCallID, toolName string, result agent.AgentToolResult) (out renderedToolHTML) {
	def, ok := r.defs[toolName]
	if !ok || def.RenderResult == nil {
		return renderedToolHTML{}
	}
	defer func() {
		// upstream: packages/coding-agent/src/core/export-html/tool-renderer.ts:renderResult
		if recover() != nil {
			out = renderedToolHTML{}
		}
	}()
	collapsedComponent := def.RenderResult(result, extension.ToolRenderResultOptions{Expanded: false, IsPartial: false}, tui.ActiveTheme(), r.renderContext(toolCallID, r.renderedResult[toolCallID], false, false, result.IsError))
	r.renderedResult[toolCallID] = collapsedComponent
	collapsedLines, ok := r.componentLines(toolName, collapsedComponent)
	if !ok {
		return renderedToolHTML{}
	}
	collapsed := ansiLinesToHTML(trimRenderedResultLines(collapsedLines))

	expandedComponent := def.RenderResult(result, extension.ToolRenderResultOptions{Expanded: true, IsPartial: false}, tui.ActiveTheme(), r.renderContext(toolCallID, r.renderedResult[toolCallID], true, false, result.IsError))
	r.renderedResult[toolCallID] = expandedComponent
	expandedLines, ok := r.componentLines(toolName, expandedComponent)
	if !ok {
		return renderedToolHTML{}
	}
	expanded := ansiLinesToHTML(trimRenderedResultLines(expandedLines))

	out = renderedToolHTML{ResultHTMLExpanded: expanded}
	if collapsed != "" && collapsed != expanded {
		out.ResultHTMLCollapsed = collapsed
	}
	return out
}

var templateRenderedTools = map[string]struct{}{
	"bash":  {},
	"read":  {},
	"write": {},
	"edit":  {},
	"ls":    {},
}

func parseToolResultImages(v any) []ai.ImageContent {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var images []ai.ImageContent
	if err := json.Unmarshal(data, &images); err != nil {
		return nil
	}
	return images
}

// toolResultContent retains the ordered text/image content passed to extension renderers.
func toolResultContent(v any) []ai.ToolResultMessageContent {
	if s, ok := v.(string); ok {
		return []ai.ToolResultMessageContent{ai.TextContent{Text: s}}
	}
	blocks, _ := v.([]any)
	content := make([]ai.ToolResultMessageContent, 0, len(blocks))
	for _, rawBlock := range blocks {
		block, _ := rawBlock.(map[string]any)
		switch block["type"] {
		case "text":
			content = append(content, ai.TextContent{Text: blockString(block["text"]), TextSignature: blockString(block["textSignature"])})
		case "image":
			for _, image := range parseToolResultImages([]any{block}) {
				content = append(content, image)
			}
		}
	}
	return content
}

func RenderCustomTools(data *SessionData, tools []extension.RegisteredTool, cwd string, width int) {
	if data == nil || len(data.Entries) == 0 || len(tools) == 0 {
		return
	}
	renderer := newToolHTMLRenderer(tools, cwd, width)
	rendered := map[string]renderedToolHTML{}

	for _, raw := range data.Entries {
		var entry map[string]any
		// upstream: coding-agent/src/core/session-manager.ts:parseSessionEntries
		if err := json.Unmarshal(raw, &entry); err != nil {
			continue
		}
		if entry["type"] != "message" {
			continue
		}
		msg, _ := entry["message"].(map[string]any)
		if msg == nil {
			continue
		}
		role, _ := msg["role"].(string)
		switch role {
		case "assistant":
			// Mirrors upstream export-html/index.ts:194-203.
			content, _ := msg["content"].([]any)
			for _, rawBlock := range content {
				block, _ := rawBlock.(map[string]any)
				if block == nil || block["type"] != "toolCall" {
					continue
				}
				callID, _ := block["id"].(string)
				toolName, _ := block["name"].(string)
				if callID == "" {
					continue
				}
				if _, isTemplate := templateRenderedTools[toolName]; isTemplate {
					continue
				}
				argsJSON, err := json.Marshal(block["arguments"])
				if err != nil {
					continue
				}
				if callHTML := renderer.renderCall(callID, toolName, argsJSON); callHTML != "" {
					rendered[callID] = renderedToolHTML{CallHTML: callHTML}
				}
			}
		case "toolResult":
			// Mirrors upstream export-html/index.ts:206-226: tool results are
			// flat toolResult-role messages, and the tool name comes off the
			// message rather than a lookup of the originating call.
			callID, _ := msg["toolCallId"].(string)
			if callID == "" {
				continue
			}
			toolName, _ := msg["toolName"].(string)
			existing, hasExisting := rendered[callID]
			if _, isTemplate := templateRenderedTools[toolName]; isTemplate && !hasExisting {
				continue
			}
			result := agent.AgentToolResult{
				Content: toolResultContent(msg["content"]),
				Details: msg["details"],
				IsError: blockBool(msg["isError"]),
			}
			piece := renderer.renderResult(callID, toolName, result)
			if piece.CallHTML == "" {
				piece.CallHTML = existing.CallHTML
			}
			if piece.CallHTML != "" || piece.ResultHTMLCollapsed != "" || piece.ResultHTMLExpanded != "" {
				rendered[callID] = piece
			}
		}
	}

	if len(rendered) == 0 {
		return
	}
	data.RenderedTools = make(map[string]map[string]any, len(rendered))
	for id, html := range rendered {
		entry := map[string]any{}
		if html.CallHTML != "" {
			entry["callHtml"] = html.CallHTML
		}
		if html.ResultHTMLCollapsed != "" {
			entry["resultHtmlCollapsed"] = html.ResultHTMLCollapsed
		}
		if html.ResultHTMLExpanded != "" {
			entry["resultHtmlExpanded"] = html.ResultHTMLExpanded
		}
		if len(entry) > 0 {
			data.RenderedTools[id] = entry
		}
	}
	if len(data.RenderedTools) == 0 {
		data.RenderedTools = nil
	}
}

func blockString(v any) string {
	s, _ := v.(string)
	return s
}

func blockBool(v any) bool {
	b, _ := v.(bool)
	return b
}
