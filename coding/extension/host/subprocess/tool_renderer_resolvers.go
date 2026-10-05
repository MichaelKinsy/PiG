package subprocess

// Ports the host side of packages/coding-agent/src/core/extensions/runner.ts resolveToolRenderers for extensions in
// other processes (pi.registerToolRenderer, Pi 1.0.1).

import (
	"encoding/json"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// toolRendererResolution is one tool's answer from an extension's resolvers.
type toolRendererResolution struct {
	done      bool
	use       string
	renderers *extension.ToolRenderers
}

// toolRendererResolvers is one extension process's resolver state: how many resolvers it registered and the answer
// per tool. Every registration (load, restart, recovery) and every later resolver registration drops the answers; a
// reload builds a new managedExt.
type toolRendererResolvers struct {
	count atomic.Int64
	mu    sync.Mutex
	tools map[string]*toolRendererResolution
}

// SetToolRenderersResolvedFunc sets the callback run when an extension process answers a tool renderer resolution, so
// the mode draws the tool's cards again with the answer. It runs on a host goroutine.
func (h *Host) SetToolRenderersResolvedFunc(fn func(toolName string)) {
	h.toolRenderersResolved.Store(&fn)
}

func (h *Host) toolRenderersResolvedFunc() func(string) {
	if fn := h.toolRenderersResolved.Load(); fn != nil && *fn != nil {
		return *fn
	}
	return nil
}

func toolRenderersDecl(renderers *extension.ToolRenderers) *ToolRenderersDecl {
	if renderers == nil {
		return nil
	}
	return &ToolRenderersDecl{
		RenderShell:   string(renderers.RenderShell),
		RendersCall:   renderers.RenderCall != nil,
		RendersResult: renderers.RenderResult != nil,
	}
}

// makeToolRendererResolver returns the resolver the runner calls for this extension. It never waits for the extension
// process: the first resolution of a tool asks the process on a host goroutine and returns next() meanwhile, and later
// resolutions use its answer.
//
// pig divergence (D89): next() is evaluated before the extension is asked and reaches its resolvers as a marker, so
// they can return it but not call its renderers; until the extension answers, a tool draws with next()'s renderers.
func (h *Host) makeToolRendererResolver(me *managedExt) extension.ToolRendererResolver {
	return func(toolName string, next func() *extension.ToolRenderers) *extension.ToolRenderers {
		// The UI loop and an export can resolve at once, so the captured managedExt is never reassigned.
		current := me.current()
		state := &current.toolRendererResolvers
		if state.count.Load() == 0 {
			return next()
		}
		state.mu.Lock()
		resolution := state.tools[toolName]
		if resolution == nil {
			resolution = &toolRendererResolution{}
			if state.tools == nil {
				state.tools = map[string]*toolRendererResolution{}
			}
			state.tools[toolName] = resolution
			state.mu.Unlock()
			nextRenderers := next()
			h.goToolRendererResolution(func() { h.resolveToolRenderers(current, toolName, nextRenderers, resolution) })
			return nextRenderers
		}
		done, use, renderers := resolution.done, resolution.use, resolution.renderers
		state.mu.Unlock()
		switch {
		case !done || use == "next":
			return next()
		case use == "own":
			return renderers
		default:
			return nil
		}
	}
}

// resolveToolRenderers asks the extension process for toolName's renderers and records the answer. A failed request
// records next(), as a resolver that throws leaves the card to the remaining renderers.
func (h *Host) resolveToolRenderers(me *managedExt, toolName string, next *extension.ToolRenderers, resolution *toolRendererResolution) {
	answer := ResolvedToolRenderers{Use: "next"}
	if conn := me.connection(); conn != nil {
		args, err := json.Marshal(ResolveToolRenderersPayload{Tool: toolName, Next: toolRenderersDecl(next)})
		if err == nil {
			resp, err := conn.requestWithInactivity(h.recoveryContext, &Envelope{
				Type:    MsgRequest,
				Request: &RequestPayload{Method: RequestResolveToolRenderers, Tool: toolName, Args: args},
			}, rendererInactivity, RequestResolveToolRenderers+" "+toolName)
			if err == nil && resp != nil && resp.Response != nil && resp.Response.Error == nil {
				var got ResolvedToolRenderers
				if json.Unmarshal(resp.Response.Result, &got) == nil && (got.Use == "own" || got.Use == "none" || got.Use == "next") {
					answer = got
				}
			}
		}
	}
	var renderers *extension.ToolRenderers
	if answer.Use == "own" {
		renderers = &extension.ToolRenderers{RenderShell: extension.ToolRenderShell(answer.RenderShell)}
		if answer.RendersCall {
			renderers.RenderCall = h.makeResolvedToolRenderCall(me, toolName, answer.Renderers)
		}
		if answer.RendersResult {
			renderers.RenderResult = h.makeResolvedToolRenderResult(me, toolName, answer.Renderers)
		}
	}
	state := &me.toolRendererResolvers
	state.mu.Lock()
	resolution.done, resolution.use, resolution.renderers = true, answer.Use, renderers
	state.mu.Unlock()
	// Cards already show next()'s renderers, so only an answer that differs draws them again; drawing them again
	// would start their renderer state over.
	if answer.Use == "next" || (answer.Use == "none" && next == nil) {
		return
	}
	if fn := h.toolRenderersResolvedFunc(); fn != nil {
		fn(toolName)
	}
}

// goToolRendererResolution runs a resolution request as a task Shutdown joins. Once Shutdown waits, a resolution keeps
// next() and asks nothing.
func (h *Host) goToolRendererResolution(resolve func()) {
	h.toolRendererResolutionsMu.Lock()
	defer h.toolRendererResolutionsMu.Unlock()
	if h.toolRendererResolutionsClosed {
		return
	}
	h.toolRendererResolutions.Go(resolve)
}

// closeToolRendererResolutions stops new resolution requests and waits for the running ones.
func (h *Host) closeToolRendererResolutions() {
	h.toolRendererResolutionsMu.Lock()
	h.toolRendererResolutionsClosed = true
	h.toolRendererResolutionsMu.Unlock()
	h.toolRendererResolutions.Wait()
}

// setToolRendererCount records the resolver count an extension reported after loading, dropping earlier answers.
func (me *managedExt) setToolRendererCount(raw json.RawMessage) {
	var payload ToolRenderersPayload
	if json.Unmarshal(raw, &payload) != nil {
		return
	}
	me.resetToolRenderers(payload.Count)
}

// resetToolRenderers records the resolver count of a registration and drops the answers of the previous process or
// resolver set, whose renderer ids no longer exist.
func (me *managedExt) resetToolRenderers(count int) {
	state := &me.toolRendererResolvers
	state.mu.Lock()
	state.count.Store(int64(count))
	state.tools = nil
	state.mu.Unlock()
}

// makeResolvedToolRenderCall draws a call with renderers an extension's resolver returned.
func (h *Host) makeResolvedToolRenderCall(me *managedExt, toolName, renderers string) extension.ToolRenderCallFunc {
	return func(args json.RawMessage, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
		return h.toolRenderProxyFor(me, toolName, "call", context, RenderToolPayload{Args: args, Renderers: renderers})
	}
}

// makeResolvedToolRenderResult draws a result with renderers an extension's resolver returned.
func (h *Host) makeResolvedToolRenderResult(me *managedExt, toolName, renderers string) extension.ToolRenderResultFunc {
	return func(result extension.AgentToolResult, options extension.ToolRenderResultOptions, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
		args, _ := context.Args.(json.RawMessage)
		return h.toolRenderProxyFor(me, toolName, "result", context, RenderToolPayload{
			Args:      args,
			Result:    renderToolResultPayload(result),
			Options:   &options,
			Renderers: renderers,
		})
	}
}
