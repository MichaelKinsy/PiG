package subprocess

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// viewImageStoreFor returns the connection's image store (D107). A nil
// connection (an in-memory test bridge) gets a store of its own.
func (c *Conn) viewImageStoreFor() *viewImageStore {
	if c == nil {
		return newViewImageStore()
	}
	c.viewImagesOnce.Do(func() { c.viewImages = newViewImageStore() })
	return c.viewImages
}

// newConnViewSurface makes a view surface whose events and evictions go to
// conn. key is the ui.custom key events carry ("" for surfaces without input).
func newConnViewSurface(conn *Conn, key string) *viewSurface {
	s := newViewSurface(key, conn.viewImageStoreFor())
	if conn == nil {
		return s
	}
	s.report = conn.reportView
	s.sendEvicted = func(refs []string) {
		args, _ := json.Marshal(ViewEvictedPayload{Refs: refs})
		_ = conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyUIViewEvicted, Args: args}})
	}
	if key != "" {
		s.sendEvent = func(ev ViewEventPayload) {
			args, _ := json.Marshal(ev)
			_ = conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyUIViewEvent, Args: args}})
		}
	}
	return s
}

// acceptView installs a frame on s and reports a rejected one once per
// surface (§5.2, §13.4). It reports whether the surface shows the frame, new
// or equal to the one it showed; false means the frame was rejected and the
// surface keeps its previous one.
func acceptView(s *viewSurface, surface string, raw json.RawMessage, lines []string) bool {
	err := s.accept(raw, lines)
	switch {
	case err == nil, errors.Is(err, errViewUnchanged):
		return true
	default:
		s.mu.Lock()
		first := !s.rejected
		s.rejected = true
		report := s.report
		s.mu.Unlock()
		if first {
			if report == nil {
				report = reportViewOnStderr
			}
			report(surface, err)
		}
		return false
	}
}

// viewDiagnosticOut is where a rejected view is reported when no runner
// shows extension errors and the terminal is not drawn by the TUI.
var viewDiagnosticOut io.Writer = os.Stderr

// reportViewOnStderr writes the diagnostic; a failed write has nowhere else
// to go.
func reportViewOnStderr(surface string, err error) {
	_, _ = fmt.Fprintf(viewDiagnosticOut, "%s\n", viewRejectionMessage(surface, err))
}

func viewRejectionMessage(surface string, err error) string {
	return fmt.Sprintf("extension view rejected on %s (the surface keeps its previous frame): %v", surface, err)
}

// reportViewRejection reports a rejected view of conn's extension through
// the runner's error listeners, the channel every mode shows extension
// errors on (the TUI's chat, RPC's extension_error, print's stderr). Without
// a bound runner it writes to stderr, except in the TUI, which owns the
// terminal.
func (h *Host) reportViewRejection(conn *Conn, surface string, err error) {
	path := conn.name
	h.mu.Lock()
	if me := h.exts[conn.name]; me != nil {
		path, _ = extensionSourcePaths(me.config)
	}
	mode := h.mode
	h.mu.Unlock()
	diagnostic := &extension.ExtensionError{ExtensionPath: path, Event: "view", Error: viewRejectionMessage(surface, err)}
	if h.providerRuntime != nil && h.providerRuntime.ReportError(diagnostic) {
		return
	}
	if mode != "tui" {
		reportViewOnStderr(surface, err)
	}
}

// Compile-time check: the host's view surface is what the UI draws.
var _ extension.ViewSurface = (*viewSurface)(nil)

// rendererView is the view of a renderer's results (D107): tool, message
// and entry renderers answer a request at a width with lines, a view, or
// both. The surface keeps the host-owned state across results.
type rendererView struct {
	mu            sync.Mutex
	surface       *viewSurface
	authoritative bool
}

// apply interprets a renderer result rendered for width and returns the
// lines to paint; ok is false for a rejected view, whose result is treated
// as a failed render.
func (r *rendererView) apply(conn *Conn, repaint func(), result RenderResult, width int) (lines []string, ok bool) {
	if !hasView(result.View) {
		r.drop()
		return result.Lines, true
	}
	r.mu.Lock()
	if r.surface == nil {
		r.surface = newConnViewSurface(conn, "")
		r.surface.repaint = repaint
	}
	surface := r.surface
	r.mu.Unlock()
	if !acceptView(surface, "renderer", result.View, result.Lines) {
		return nil, false
	}
	r.mu.Lock()
	r.authoritative = result.Lines == nil
	r.mu.Unlock()
	if result.Lines != nil {
		return result.Lines, true
	}
	return surface.Render(width), true
}

// live renders an authoritative view again at width, so a loader or a theme
// change shows without a new request; ok is false without one.
func (r *rendererView) live(width int) ([]string, bool) {
	r.mu.Lock()
	surface, authoritative := r.surface, r.authoritative
	r.mu.Unlock()
	if surface == nil || !authoritative {
		return nil, false
	}
	return surface.Render(width), true
}

// frontendView returns the structure of the result laid out at width.
func (r *rendererView) frontendView(width int) *frontend.View {
	r.mu.Lock()
	surface := r.surface
	r.mu.Unlock()
	if surface == nil {
		return nil
	}
	return surface.FrontendView(width)
}

// drop ends the view, as a result without one replaces it.
func (r *rendererView) drop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.surface != nil {
		r.surface.close()
		r.surface = nil
	}
	r.authoritative = false
}
