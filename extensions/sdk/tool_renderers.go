package sdk

import (
	"errors"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// ToolRenderShell mirrors upstream ToolDefinition.renderShell.
type ToolRenderShell string

const (
	// ToolRenderShellDefault draws the renderers inside the standard tool card.
	ToolRenderShellDefault ToolRenderShell = "default"
	// ToolRenderShellSelf lets the renderers draw their own framing.
	ToolRenderShellSelf ToolRenderShell = "self"
)

// ToolRenderContext mirrors upstream ToolRenderContext for a renderer that
// returns lines. State is the tool card's renderer state: it starts empty and
// is shared by the call and result renderers of one card. Invalidate asks the
// host to run both renderers again, as upstream context.invalidate() does.
type ToolRenderContext struct {
	Args             map[string]any
	ToolCallID       string
	Cwd              string
	ExecutionStarted bool
	ArgsComplete     bool
	IsPartial        bool
	Expanded         bool
	ShowImages       bool
	IsError          bool
	// OutputPad is the horizontal padding configured by the outputPad setting. Renderers with renderShell "self" apply it themselves.
	OutputPad int
	// DurationMs is the recorded execution time of a final result in milliseconds. Absent while the result is partial and for results stored without one. upstream: ToolRenderContext.durationMs
	DurationMs *int64
	State      map[string]any
	Invalidate func()
}

// ToolRenderResult is the result upstream renderResult receives: text and
// image content blocks and the tool's details.
type ToolRenderResult struct {
	Content []map[string]any `json:"content"`
	Details any              `json:"details,omitempty"`
}

// ToolRenderResultOptions mirrors upstream ToolRenderResultOptions.
type ToolRenderResultOptions struct {
	Expanded  bool `json:"expanded"`
	IsPartial bool `json:"isPartial"`
}

// ToolRenderCallFunc renders a tool call into terminal lines at width, as the
// component upstream renderCall returns renders.
type ToolRenderCallFunc func(ctx Context, args map[string]any, render ToolRenderContext, width int) ([]string, error)

// ToolRenderResultFunc renders a tool result into terminal lines at width, as
// the component upstream renderResult returns renders.
type ToolRenderResultFunc func(ctx Context, result ToolRenderResult, options ToolRenderResultOptions, render ToolRenderContext, width int) ([]string, error)

// ToolRenderCallViewFunc renders a tool call as a [kit.View], which the host
// renders at width, as the component upstream renderCall returns renders.
type ToolRenderCallViewFunc func(ctx Context, args map[string]any, render ToolRenderContext, width int) (kit.View, error)

// ToolRenderResultViewFunc renders a tool result as a [kit.View], which the
// host renders at width, as the component upstream renderResult returns
// renders.
type ToolRenderResultViewFunc func(ctx Context, result ToolRenderResult, options ToolRenderResultOptions, render ToolRenderContext, width int) (kit.View, error)

// ToolRenderers are a tool's upstream renderShell, renderCall and
// renderResult. A renderer that returns an error draws upstream's fallback in
// its place. CallView and ResultView are the kit forms (D107): a view form
// declares its phase rendered and wins over the line form set for the same
// phase.
type ToolRenderers struct {
	Shell      ToolRenderShell
	Call       ToolRenderCallFunc
	Result     ToolRenderResultFunc
	CallView   ToolRenderCallViewFunc
	ResultView ToolRenderResultViewFunc
}

// rendersCall reports whether the renderers draw the call phase.
func (r ToolRenderers) rendersCall() bool { return r.Call != nil || r.CallView != nil }

// rendersResult reports whether the renderers draw the result phase.
func (r ToolRenderers) rendersResult() bool { return r.Result != nil || r.ResultView != nil }

// SetToolRenderers sets the renderers of the registered tool name.
func (e *Extension) SetToolRenderers(name string, renderers ToolRenderers) {
	e.toolMu.Lock()
	defer e.toolMu.Unlock()
	var updated *toolDef
	for i := range e.tools {
		if e.tools[i].Name != name {
			continue
		}
		e.tools[i].RenderShell = ""
		if renderers.Shell == ToolRenderShellSelf {
			e.tools[i].RenderShell = string(ToolRenderShellSelf)
		}
		e.tools[i].RendersCall = renderers.rendersCall()
		e.tools[i].RendersResult = renderers.rendersResult()
		updated = &e.tools[i]
	}
	e.toolRenderMu.Lock()
	if e.toolRenderers == nil {
		e.toolRenderers = make(map[string]ToolRenderers)
	}
	e.toolRenderers[name] = renderers
	e.toolRenderMu.Unlock()
	if updated != nil {
		e.publishTool(*updated)
	}
}

// toolRenderCard is one tool card's renderer state. Renders of one card run
// one at a time, as upstream renders a card on one thread.
type toolRenderCard struct {
	mu    sync.Mutex
	state map[string]any
}

type renderToolRequest struct {
	Card      string                  `json:"card"`
	Renderers string                  `json:"renderers"`
	Phase     string                  `json:"phase"`
	Args      map[string]any          `json:"args"`
	Result    *ToolRenderResult       `json:"result"`
	Options   ToolRenderResultOptions `json:"options"`
	Context   struct {
		ToolCallID       string `json:"toolCallId"`
		Cwd              string `json:"cwd"`
		ExecutionStarted bool   `json:"executionStarted"`
		ArgsComplete     bool   `json:"argsComplete"`
		IsPartial        bool   `json:"isPartial"`
		Expanded         bool   `json:"expanded"`
		ShowImages       bool   `json:"showImages"`
		IsError          bool   `json:"isError"`
		OutputPad        int    `json:"outputPad"`
		DurationMs       *int64 `json:"durationMs"`
	} `json:"context"`
	Width int `json:"width"`
}

// toolRenderOutput is a renderer's lines, or its view when view is set.
type toolRenderOutput struct {
	lines []string
	view  *kit.View
}

// renderTool answers a render_tool request with the renderer's lines or view.
func (e *Extension) renderTool(ctx Context, name string, raw json.RawMessage) (toolRenderOutput, error) {
	var request renderToolRequest
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &request); err != nil {
			return toolRenderOutput{}, err
		}
	}
	e.toolRenderMu.Lock()
	renderers, ok := e.toolRenderers[name]
	if request.Renderers != "" {
		renderers, ok = e.resolvedToolRenderers[request.Renderers]
	}
	card := e.toolRenderCards[request.Card]
	if card == nil {
		card = &toolRenderCard{state: map[string]any{}}
		if e.toolRenderCards == nil {
			e.toolRenderCards = make(map[string]*toolRenderCard)
		}
		e.toolRenderCards[request.Card] = card
	}
	e.toolRenderMu.Unlock()
	if !ok {
		return toolRenderOutput{}, errors.New("unknown tool renderer: " + name)
	}
	card.mu.Lock()
	defer card.mu.Unlock()
	render := ToolRenderContext{
		Args:             request.Args,
		ToolCallID:       request.Context.ToolCallID,
		Cwd:              request.Context.Cwd,
		ExecutionStarted: request.Context.ExecutionStarted,
		ArgsComplete:     request.Context.ArgsComplete,
		IsPartial:        request.Context.IsPartial,
		Expanded:         request.Context.Expanded,
		ShowImages:       request.Context.ShowImages,
		IsError:          request.Context.IsError,
		OutputPad:        request.Context.OutputPad,
		DurationMs:       request.Context.DurationMs,
		State:            card.state,
		Invalidate: func() {
			_ = e.conn.notify("tool_render_invalidate", map[string]string{"card": request.Card})
		},
	}
	if request.Phase == "result" {
		result := ToolRenderResult{Content: []map[string]any{}}
		if request.Result != nil {
			result = *request.Result
		}
		switch {
		case renderers.ResultView != nil:
			view, err := renderers.ResultView(ctx, result, request.Options, render, request.Width)
			return toolRenderOutput{view: &view}, err
		case renderers.Result != nil:
			lines, err := renderers.Result(ctx, result, request.Options, render, request.Width)
			return toolRenderOutput{lines: lines}, err
		}
		return toolRenderOutput{}, errors.New("tool " + name + " has no result renderer")
	}
	switch {
	case renderers.CallView != nil:
		view, err := renderers.CallView(ctx, request.Args, render, request.Width)
		return toolRenderOutput{view: &view}, err
	case renderers.Call != nil:
		lines, err := renderers.Call(ctx, request.Args, render, request.Width)
		return toolRenderOutput{lines: lines}, err
	}
	return toolRenderOutput{}, errors.New("tool " + name + " has no call renderer")
}

// releaseToolRenderCard drops the state of a tool card the host no longer
// shows.
func (e *Extension) releaseToolRenderCard(raw json.RawMessage) {
	var payload struct {
		Card string `json:"card"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return
	}
	e.toolRenderMu.Lock()
	delete(e.toolRenderCards, payload.Card)
	e.toolRenderMu.Unlock()
}
