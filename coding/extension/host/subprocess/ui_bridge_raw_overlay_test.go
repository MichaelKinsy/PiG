package subprocess

import (
	"context"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// rawOverlayUI is a TUI as the bridge's raw overlays see it: ShowRemoteOverlay mounts a handle at once, controls move one focus
// owner, and an overlay ends (its ended channel closes) when its handle closes, as ExtUIContext.ShowRemoteOverlay's does.
type rawOverlayUI struct {
	extension.UIContext
	mu      sync.Mutex
	focused any
	shown   map[string]*rawOverlayHandle
	ops     []string
}

type rawOverlayHandle struct {
	ui     *rawOverlayUI
	label  string
	closed chan struct{}
	once   sync.Once
	ended  chan struct{}
	lines  []string
}

func (h *rawOverlayHandle) UpdateLines(lines []string) {
	h.ui.mu.Lock()
	h.lines = slices.Clone(lines)
	h.ui.mu.Unlock()
}

func (h *rawOverlayHandle) Close(any) { h.once.Do(func() { close(h.closed) }) }

func (h *rawOverlayHandle) Control(_ context.Context, action string, _ bool, _ *extension.RemoteOverlayFocusTarget) (extension.RemoteOverlayState, error) {
	h.ui.mu.Lock()
	switch action {
	case "focus":
		h.ui.focused = h
	case "hide":
		if h.ui.focused == h {
			h.ui.focused = nil
		}
	}
	if action != "" {
		h.ui.ops = append(h.ui.ops, h.label+":"+action)
	}
	state := extension.RemoteOverlayState{Focused: h.ui.focused == h, Visible: action != "hide", FocusOwner: h.ui.focused}
	h.ui.mu.Unlock()
	if action == "hide" {
		h.Close(nil)
	}
	return state, nil
}

func (u *rawOverlayUI) ShowRemoteOverlay(opts extension.RemoteOverlayOptions, _ extension.RemoteOverlayHost) (extension.RemoteOverlayHandle, <-chan struct{}, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	label := "?"
	if opts.Layout != nil && opts.Layout.Anchor != "" {
		label = opts.Layout.Anchor
	}
	handle := &rawOverlayHandle{ui: u, label: label, closed: make(chan struct{}), ended: make(chan struct{})}
	if opts.Layout == nil || !opts.Layout.NonCapturing {
		u.focused = handle
	}
	u.shown[label] = handle
	u.ops = append(u.ops, label+":show")
	go func() {
		<-handle.closed
		u.mu.Lock()
		u.ops = append(u.ops, label+":ended")
		delete(u.shown, label)
		u.mu.Unlock()
		close(handle.ended)
	}()
	return handle, handle.ended, true
}

func (u *rawOverlayUI) HideTopOverlay(context.Context) (any, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.ops = append(u.ops, "hideOverlay")
	u.focused = nil
	return u.focused, nil
}

func (u *rawOverlayUI) SetRemoteFocus(_ context.Context, target extension.RemoteOverlayFocusTarget) (any, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.ops = append(u.ops, "setFocus")
	u.focused = target.Overlay
	return u.focused, nil
}

func (u *rawOverlayUI) handle(label string) *rawOverlayHandle {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.shown[label]
}

// rawCall makes one synchronous extension→host call and decodes its RemoteOverlayFocusResult.
func rawCall(t *testing.T, bridge *UIBridge, owner *Conn, method string, args any) RemoteOverlayFocusResult {
	t.Helper()
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	result, err := bridge.handleCall(context.Background(), "ext", owner, &CallPayload{Method: method, Args: data})
	if err == nil && result.Error != nil {
		err = result.Error.ToError()
	}
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	var decoded RemoteOverlayFocusResult
	if err := json.Unmarshal(result.Result, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func waitClosed(t *testing.T, what string, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not happen", what)
	}
}

func (b *UIBridge) rawOverlayRegistered(owner *Conn, key string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.customOverlays[customOverlayOwnerPrefix("ext", owner)+key]
	return ok
}

func waitUnregistered(t *testing.T, bridge *UIBridge, owner *Conn, key string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for bridge.rawOverlayRegistered(owner, key) {
		if time.Now().After(deadline) {
			t.Fatalf("overlay %s stayed registered after it ended", key)
		}
		time.Sleep(time.Millisecond)
	}
}

// Pi's TUI.showOverlay (tui.ts:729-826) returns its handle at once, so a factory shows and focuses a panel before its own component
// is shown. ui.custom.showOverlay mounts the raw overlay and returns with no host call left waiting; its frames and controls use its
// key; every answer names the extension's overlay that holds focus; its handle's hide ends it and nothing else.
func TestRawOverlayShowsAtOnceAndEndsWithItsHide(t *testing.T) {
	ui := &rawOverlayUI{UIContext: extension.NoopUIContext, shown: map[string]*rawOverlayHandle{}}
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	layout := func(anchor string, nonCapturing bool) *extension.OverlayLayout {
		return &extension.OverlayLayout{Anchor: anchor, NonCapturing: nonCapturing}
	}
	alpha := rawCall(t, bridge, nil, CallUICustomShowOverlay, RemoteOverlayShowPayload{Key: "custom-1", OverlayOptions: layout("alpha", true)})
	if alpha.OK == nil || !*alpha.OK || alpha.Focused || alpha.FocusedKey != "" {
		t.Fatalf("non-capturing showOverlay answered %+v, want shown and unfocused", alpha)
	}
	if got := rawCall(t, bridge, nil, CallUICustomControl, RemoteOverlayControlPayload{Key: "custom-1", Action: "focus"}); !got.Focused || got.FocusedKey != "custom-1" {
		t.Fatalf("focus answered %+v, want custom-1 focused", got)
	}
	bridge.HandleNotifyFrom("ext", nil, &NotifyPayload{Method: NotifyUICustomRender, Args: json.RawMessage(`{"key":"custom-1","lines":["ALPHA"]}`)})
	if lines := ui.handle("alpha").lines; !slices.Equal(lines, []string{"ALPHA"}) {
		t.Fatalf("raw overlay frame = %q, want ALPHA", lines)
	}
	beta := rawCall(t, bridge, nil, CallUICustomShowOverlay, RemoteOverlayShowPayload{Key: "custom-2", OverlayOptions: layout("beta", false)})
	if !beta.Focused || beta.FocusedKey != "custom-2" {
		t.Fatalf("capturing showOverlay answered %+v, want custom-2 focused", beta)
	}
	if got := rawCall(t, bridge, nil, CallUICustomSetFocus, map[string]any{"target": RemoteOverlayTargetPayload{Kind: "overlay", Key: "custom-1"}}); got.FocusedKey != "custom-1" {
		t.Fatalf("setFocus answered %+v, want custom-1 focused", got)
	}
	if got := rawCall(t, bridge, nil, CallUICustomHideOverlay, struct{}{}); got.FocusedKey != "" {
		t.Fatalf("hideOverlay answered %+v, want nothing of the extension focused", got)
	}
	alphaHandle := ui.handle("alpha")
	if got := rawCall(t, bridge, nil, CallUICustomControl, RemoteOverlayControlPayload{Key: "custom-1", Action: "hide"}); got.Visible {
		t.Fatalf("hide answered %+v, want the overlay gone", got)
	}
	waitClosed(t, "alpha's end", alphaHandle.ended)
	waitUnregistered(t, bridge, nil, "custom-1")
	if ui.handle("beta") == nil {
		t.Fatal("alpha's hide ended beta too")
	}
	ui.mu.Lock()
	ops := slices.Clone(ui.ops)
	ui.mu.Unlock()
	want := []string{"alpha:show", "alpha:focus", "beta:show", "setFocus", "hideOverlay", "alpha:hide", "alpha:ended"}
	if !slices.Equal(ops, want) {
		t.Fatalf("UI operations = %q, want %q", ops, want)
	}
	betaHandle := ui.handle("beta")
	rawCall(t, bridge, nil, CallUICustomControl, RemoteOverlayControlPayload{Key: "custom-2", Action: "hide"})
	waitClosed(t, "beta's end", betaHandle.ended)
	waitUnregistered(t, bridge, nil, "custom-2")
}

// A raw overlay outlives the call that showed it, so the bridge takes it off the stack when the extension's connection ends (a reload,
// a shutdown or a crashed process): nothing can draw or hide it any more.
func TestRawOverlayEndsWithItsConnection(t *testing.T) {
	ui := &rawOverlayUI{UIContext: extension.NoopUIContext, shown: map[string]*rawOverlayHandle{}}
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	hostEnd, peer := net.Pipe()
	conn := NewConn("ext", hostEnd)
	conn.Start(t.Context())
	rawCall(t, bridge, conn, CallUICustomShowOverlay, RemoteOverlayShowPayload{Key: "custom-1", OverlayOptions: &extension.OverlayLayout{Anchor: "panel"}})
	handle := ui.handle("panel")
	select {
	case <-handle.ended:
		t.Fatal("the raw overlay ended while its connection was open")
	case <-time.After(20 * time.Millisecond):
	}
	// The extension process exits: its end of the socket closes.
	_ = peer.Close()
	waitClosed(t, "the raw overlay's end after its connection closed", handle.ended)
	waitUnregistered(t, bridge, conn, "custom-1")
}

// A UI without a TUI (print mode, RPC) shows nothing: the extension's handle controls nothing.
func TestRawOverlayWithoutTUIIsNotShown(t *testing.T) {
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(extension.NoopUIContext)
	got := rawCall(t, bridge, nil, CallUICustomShowOverlay, RemoteOverlayShowPayload{Key: "custom-1"})
	if got.OK == nil || *got.OK {
		t.Fatalf("showOverlay without a TUI answered %+v, want ok false", got)
	}
	if bridge.rawOverlayRegistered(nil, "custom-1") {
		t.Fatal("an overlay that was not shown stayed registered")
	}
}
