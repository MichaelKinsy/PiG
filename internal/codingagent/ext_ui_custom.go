package codingagent

// Ports InteractiveMode.showExtensionCustom (packages/coding-agent/src/modes/interactive/interactive-mode.ts:2863-2935)
// for an extension that runs in the host process.

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// customHost is the `tui` a custom component's factory receives.
type customHost struct{ m *InteractiveMode }

func (h customHost) RequestRender() {
	if h.m.tuiInst != nil {
		h.m.tuiInst.RequestRender()
	}
}

type customResult struct {
	value any
	err   error
}

// customCall is one Custom call. mu guards the fields below it; mounted, overlay and savedText belong to the owner loop.
type customCall struct {
	u      *ExtUIContext
	result chan customResult

	mu       sync.Mutex
	closed   bool // done was called, or the call ended
	building bool // the factory is running on the owner loop
	value    any

	component tui.Component
	handle    *tui.OverlayHandle
	mounted   bool
	torn      bool
	disposed  bool
	isOverlay bool
	savedText string
}

// customFactoryOf accepts the named factory type or the equal function literal.
func customFactoryOf(factory any) (extension.CustomFactory, bool) {
	switch f := factory.(type) {
	case extension.CustomFactory:
		return f, f != nil
	case func(extension.CustomHost, extension.Theme, extension.KeybindingsManager, func(any)) (extension.Component, error):
		return f, f != nil
	}
	return nil, false
}

func customOptionsOf(opts any) extension.CustomOptions {
	switch o := opts.(type) {
	case extension.CustomOptions:
		return o
	case *extension.CustomOptions:
		if o != nil {
			return *o
		}
	}
	return extension.CustomOptions{}
}

// Custom runs an in-process factory and shows its component in the editor slot, or over the screen, until the
// factory's done callback ends the call with a result.
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:2863-2935 (showExtensionCustom)
func (u *ExtUIContext) Custom(ctx context.Context, factory any, opts any) (any, error) {
	create, ok := customFactoryOf(factory)
	if !ok {
		return nil, fmt.Errorf("custom extension components need an extension.CustomFactory, got %T (a subprocess extension uses RunRemoteOverlay)", factory)
	}
	if u.m.tuiInst == nil || u.m.editorContainer == nil || u.m.editor == nil {
		return nil, errors.New("no TUI available")
	}
	options := customOptionsOf(opts)
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if u.m.runCtx != nil {
		stop := context.AfterFunc(u.m.runCtx, cancel)
		defer stop()
	}
	call := &customCall{u: u, result: make(chan customResult, 1), isOverlay: options.Overlay}
	var posted sync.WaitGroup
	defer posted.Wait()
	u.m.runOnMain(ctx, func() { call.build(ctx, create, options, &posted) })
	// The owner loop builds the component before any task queued after this one, so a later extension call applies
	// after it, as upstream's synchronous showExtensionCustom does.
	extension.CallInitiated(ctx)

	select {
	case res := <-call.result:
		return res.value, res.err
	case <-ctx.Done():
		call.markClosed(nil)
		cleanupCtx := u.m.runCtx
		if cleanupCtx == nil {
			cleanupCtx = context.Background()
		}
		u.m.runOnMain(cleanupCtx, func() {
			call.teardown()
			call.dispose()
		})
		return nil, ctx.Err()
	}
}

// markClosed records the first done and reports whether this call was it.
func (c *customCall) markClosed(value any) (first, building bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false, c.building
	}
	c.closed, c.value = true, value
	return true, c.building
}

// done is the callback the factory receives. The first call ends the Custom call with its result; later calls do
// nothing. It runs on any goroutine, including the owner loop.
func (c *customCall) done(ctx context.Context, posted *sync.WaitGroup) func(any) {
	return func(value any) {
		first, building := c.markClosed(value)
		if !first || building {
			// While the factory runs, its owner-loop caller finishes the call when the factory returns.
			return
		}
		c.finishAsync(ctx, posted)
	}
}

// finishAsync ends the call on the owner loop without blocking a caller that already runs there.
func (c *customCall) finishAsync(ctx context.Context, posted *sync.WaitGroup) {
	if c.u.m.postUITask(c.finish) {
		return
	}
	posted.Go(func() { c.u.m.runOnMain(ctx, c.finish) })
}

// build runs the factory on the owner loop and mounts what it returns.
func (c *customCall) build(ctx context.Context, create extension.CustomFactory, options extension.CustomOptions, posted *sync.WaitGroup) {
	m := c.u.m
	if ctx.Err() != nil {
		return
	}
	c.savedText = m.editor.Text()
	c.mu.Lock()
	c.building = true
	c.mu.Unlock()
	built, err := c.callFactory(create, c.done(ctx, posted))
	c.mu.Lock()
	c.building = false
	closed, value := c.closed, c.value
	c.mu.Unlock()
	if closed {
		// upstream: close() already ran restoreEditor for an editor-slot component (interactive-mode.ts:2890-2893), and
		// the factory's component is never mounted (interactive-mode.ts:2900).
		if !options.Overlay {
			c.restoreEditor()
		}
		c.result <- customResult{value: value}
		return
	}
	component, isComponent := built.(tui.Component)
	if err == nil && !isComponent {
		err = fmt.Errorf("custom component factory returned %T, not a component", built)
	}
	if err != nil {
		if first, _ := c.markClosed(nil); !first {
			// upstream: the rejection handler returns when close() already ran (interactive-mode.ts:2926); the finish
			// that done posted resolves the call with its result.
			return
		}
		if !options.Overlay {
			c.restoreEditor()
		}
		c.result <- customResult{err: err}
		return
	}
	c.component = component
	if options.Overlay {
		c.handle = m.tuiInst.OpenOverlay(component, remoteOverlayTUIOptions(extension.RemoteOverlayOptions{Overlay: true, Layout: options.Layout}))
	} else {
		m.editorContainer.SetChildren(component)
		m.tuiInst.SetFocus(component)
	}
	c.mounted = true
	m.tuiInst.RequestRender()
}

// callFactory turns a panic in the factory into the error upstream's promise rejection is.
func (c *customCall) callFactory(create extension.CustomFactory, done func(any)) (built extension.Component, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("custom component factory panicked: %v", r)
		}
	}()
	return create(customHost{c.u.m}, tui.ActiveTheme(), tui.GetTUIKeybindings(), done)
}

// finish ends the call on the owner loop: it takes the component down, resolves the call with the result, and only
// then disposes the component.
func (c *customCall) finish() {
	c.teardown()
	c.mu.Lock()
	value := c.value
	c.mu.Unlock()
	// The first done posts the only finish, and build sends no result once done has run, so this send never waits.
	c.result <- customResult{value: value}
	c.dispose()
}

// teardown restores the editor slot or hides the overlay, once. upstream: close, restoreEditor.
func (c *customCall) teardown() {
	if c.torn || !c.mounted {
		return
	}
	c.torn = true
	if c.isOverlay {
		if c.handle != nil {
			c.handle.Close()
		}
		return
	}
	c.restoreEditor()
}

// restoreEditor puts the editor back in its slot with the text it held when the component opened and focuses it.
func (c *customCall) restoreEditor() {
	m := c.u.m
	m.editorContainer.SetChildren(m.editor)
	m.editor.SetText(c.savedText)
	m.tuiInst.SetFocus(m.editor)
	m.tuiInst.RequestRender()
}

// dispose runs the component's Dispose, ignoring what it does. upstream: interactive-mode.ts:2891-2895.
func (c *customCall) dispose() {
	if c.disposed || !c.mounted {
		return
	}
	c.disposed = true
	disposer, ok := c.component.(interface{ Dispose() })
	if !ok {
		return
	}
	defer func() { _ = recover() }()
	disposer.Dispose()
}
