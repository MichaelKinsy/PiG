package export

import (
	"encoding/json"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
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
	}
}

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
	}
}

func isBlankRenderedLine(line string) bool {
	return strings.TrimSpace(string(tools.StripANSI([]byte(line)))) == ""
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

func renderComponentHTML(component any, width int) string {
	renderable, ok := component.(renderableComponent)
	if !ok || renderable == nil || rendersAsynchronously(component) {
		return ""
	}
	return ansiLinesToHTML(renderable.Render(width))
}

// rendersAsynchronously reports a component an extension process renders. Its
// frame arrives after the export has finished, so the export uses the default
// tool rendering for it.
func rendersAsynchronously(component any) bool {
	_, async := component.(tui.RendererFallback)
	return async
}

func (r *toolHTMLRenderer) renderCall(toolCallID, toolName string, argsJSON json.RawMessage) string {
	def, ok := r.defs[toolName]
	if !ok || def.RenderCall == nil {
		return ""
	}
	r.renderedArgsJSON[toolCallID] = argsJSON
	component := def.RenderCall(argsJSON, tui.ActiveTheme(), r.renderContext(toolCallID, r.renderedCall[toolCallID], false, true, false))
	r.renderedCall[toolCallID] = component
	return renderComponentHTML(component, r.width)
}

func (r *toolHTMLRenderer) renderResult(toolCallID, toolName string, result agent.AgentToolResult) renderedToolHTML {
	def, ok := r.defs[toolName]
	if !ok || def.RenderResult == nil {
		return renderedToolHTML{}
	}
	collapsedComponent := def.RenderResult(result, extension.ToolRenderResultOptions{Expanded: false, IsPartial: false}, tui.ActiveTheme(), r.renderContext(toolCallID, r.renderedResult[toolCallID], false, false, result.IsError))
	r.renderedResult[toolCallID] = collapsedComponent
	if rendersAsynchronously(collapsedComponent) {
		return renderedToolHTML{}
	}
	collapsedRenderable, _ := collapsedComponent.(renderableComponent)
	collapsed := ""
	if collapsedRenderable != nil {
		collapsed = ansiLinesToHTML(trimRenderedResultLines(collapsedRenderable.Render(r.width)))
	}

	expandedComponent := def.RenderResult(result, extension.ToolRenderResultOptions{Expanded: true, IsPartial: false}, tui.ActiveTheme(), r.renderContext(toolCallID, r.renderedResult[toolCallID], true, false, result.IsError))
	r.renderedResult[toolCallID] = expandedComponent
	expandedRenderable, _ := expandedComponent.(renderableComponent)
	expanded := ""
	if expandedRenderable != nil {
		expanded = ansiLinesToHTML(trimRenderedResultLines(expandedRenderable.Render(r.width)))
	}

	out := renderedToolHTML{ResultHTMLExpanded: expanded}
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

// toolResultContent flattens a toolResult message's content into the text and
// images an AgentToolResult carries. Upstream's ToolResultMessage.content is
// `(TextContent | ImageContent)[]`; text blocks join with newlines, mirroring
// ai.ToolResultContent's array handling. A bare string is accepted because
// extensions may emit one.
func toolResultContent(v any) (string, []ai.ImageContent) {
	if s, ok := v.(string); ok {
		return s, nil
	}
	blocks, ok := v.([]any)
	if !ok {
		return "", nil
	}
	var text strings.Builder
	var images []ai.ImageContent
	for _, rawBlock := range blocks {
		block, _ := rawBlock.(map[string]any)
		if block == nil {
			continue
		}
		switch block["type"] {
		case "text":
			if text.Len() > 0 {
				text.WriteString("\n")
			}
			text.WriteString(blockString(block["text"]))
		case "image":
			if img := parseToolResultImages([]any{block}); len(img) > 0 {
				images = append(images, img...)
			}
		}
	}
	return text.String(), images
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
			text, images := toolResultContent(msg["content"])
			result := agent.AgentToolResult{
				Content: text,
				Images:  images,
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
