package subprocess

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type remoteOverlayController interface {
	Control(context.Context, string, bool, *extension.RemoteOverlayFocusTarget) (extension.RemoteOverlayState, error)
}

func (p *overlayProxy) Control(ctx context.Context, action string, hidden bool, focus *extension.RemoteOverlayFocusTarget) (extension.RemoteOverlayState, error) {
	p.mu.Lock()
	target, closed := p.target, p.closed
	p.mu.Unlock()
	if closed {
		return extension.RemoteOverlayState{}, nil
	}
	controller, ok := target.(remoteOverlayController)
	if !ok {
		return extension.RemoteOverlayState{}, errors.New("mounted overlay does not expose controls")
	}
	return controller.Control(ctx, action, hidden, focus)
}

func (b *UIBridge) handleCustomControl(ctx context.Context, extName string, owner *Conn, args json.RawMessage) (*CallResultPayload, error) {
	var request RemoteOverlayControlPayload
	if err := json.Unmarshal(args, &request); err != nil {
		return nil, err
	}
	b.mu.RLock()
	proxy, ok := b.customOverlays[customOverlayOwnerPrefix(extName, owner)+request.Key].(*overlayProxy)
	b.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown overlay: %s", request.Key)
	}
	var target *extension.RemoteOverlayFocusTarget
	if request.Target != nil {
		resolved, err := b.overlayFocusTarget(extName, owner, *request.Target)
		if err != nil {
			return nil, err
		}
		target = &resolved
	}
	state, err := proxy.Control(ctx, request.Action, request.Hidden, target)
	if err != nil {
		return nil, err
	}
	return b.focusResult(extName, owner, nil, state)
}

// overlayFocusTarget resolves an explicit focus target the extension named: nothing, the editor, or one of its mounted overlays.
func (b *UIBridge) overlayFocusTarget(extName string, owner *Conn, request RemoteOverlayTargetPayload) (extension.RemoteOverlayFocusTarget, error) {
	var target extension.RemoteOverlayFocusTarget
	switch request.Kind {
	case "null":
	case "editor":
		target.Editor = true
	case "overlay":
		b.mu.RLock()
		other, ok := b.customOverlays[customOverlayOwnerPrefix(extName, owner)+request.Key].(*overlayProxy)
		b.mu.RUnlock()
		if !ok {
			return target, fmt.Errorf("unknown overlay focus target: %s", request.Key)
		}
		other.mu.Lock()
		target.Overlay = other.target
		other.mu.Unlock()
	default:
		return target, fmt.Errorf("unknown overlay focus target kind: %s", request.Kind)
	}
	return target, nil
}

// focusResult answers a call that may have moved TUI focus with state and the key of the extension's mounted overlay that holds
// focus now (state.FocusOwner), so the extension clears the focused flag of its other components as Pi's setFocus does.
func (b *UIBridge) focusResult(extName string, owner *Conn, ok *bool, state extension.RemoteOverlayState) (*CallResultPayload, error) {
	result := RemoteOverlayFocusResult{OK: ok, RemoteOverlayState: state}
	if state.FocusOwner != nil {
		prefix := customOverlayOwnerPrefix(extName, owner)
		b.mu.RLock()
		for fullKey, value := range b.customOverlays {
			key, mine := strings.CutPrefix(fullKey, prefix)
			proxy, isProxy := value.(*overlayProxy)
			if !mine || !isProxy {
				continue
			}
			proxy.mu.Lock()
			target := proxy.target
			proxy.mu.Unlock()
			if target != nil && any(target) == state.FocusOwner {
				result.FocusedKey = key
				break
			}
		}
		b.mu.RUnlock()
	}
	data, err := json.Marshal(result)
	return &CallResultPayload{Result: data}, err
}

// handleCustomShowOverlay mounts a raw overlay for a component the extension showed through its factory's tui (Pi's
// TUI.showOverlay, tui.ts:729-826). The call returns once the overlay is on the stack, so the extension's handle is synchronous as
// Pi's is; no ui.custom call waits on it. The overlay ends with its handle's hide, or when the extension's connection ends (reload,
// shutdown, or a crashed process), which removes it from the stack.
func (b *UIBridge) handleCustomShowOverlay(ctx context.Context, extName string, owner *Conn, ui extension.UIContext, args json.RawMessage) (*CallResultPayload, error) {
	var request RemoteOverlayShowPayload
	if err := json.Unmarshal(args, &request); err != nil {
		return nil, fmt.Errorf("parse showOverlay args: %w", err)
	}
	if request.Key == "" {
		return nil, errors.New("showOverlay requires a key")
	}
	notShown := false
	remoteTUI, ok := ui.(extension.RemoteTUI)
	if !ok {
		return b.focusResult(extName, owner, &notShown, extension.RemoteOverlayState{})
	}
	b.mu.RLock()
	_, taken := b.customOverlays[customOverlayOwnerPrefix(extName, owner)+request.Key]
	b.mu.RUnlock()
	if taken {
		return nil, fmt.Errorf("overlay key already mounted: %s", request.Key)
	}
	proxy := b.customOverlayProxyFor(extName, owner, request.Key)
	//portlint:allow ctxmapping the overlay outlives this call, which returns once it is mounted, and Pi's TUI.showOverlay takes no signal (tui.ts:729); it ends with its handle's hide or its connection (the goroutine below calls end)
	lifetime, end := context.WithCancel(context.Background())
	host := &customOverlayHost{ctx: lifetime, bridge: b, extName: extName, owner: owner, key: request.Key}
	handle, ended, ok := remoteTUI.ShowRemoteOverlay(extension.RemoteOverlayOptions{Overlay: true, Layout: request.OverlayOptions}, host)
	if !ok {
		end()
		b.unregisterCustomOverlay(extName, owner, request.Key)
		return b.focusResult(extName, owner, &notShown, extension.RemoteOverlayState{})
	}
	proxy.SetTarget(handle)
	var lost <-chan struct{}
	if owner != nil {
		lost = owner.Done()
	}
	go func() {
		select {
		case <-ended:
		case <-lost:
			// The extension can no longer draw or hide it: take it off the stack.
			proxy.Close(nil)
			<-ended
		}
		proxy.Close(nil)
		end()
		b.unregisterCustomOverlay(extName, owner, request.Key)
	}()
	state, err := proxy.Control(ctx, "", false, nil)
	if err != nil {
		return nil, err
	}
	shown := true
	return b.focusResult(extName, owner, &shown, state)
}

// handleCustomHideOverlay is Pi's TUI.hideOverlay (tui.ts:829-841) for the tui a factory receives: it pops the topmost overlay,
// whichever component it shows.
func (b *UIBridge) handleCustomHideOverlay(ctx context.Context, extName string, owner *Conn, ui extension.UIContext) (*CallResultPayload, error) {
	remoteTUI, ok := ui.(extension.RemoteTUI)
	if !ok {
		return b.focusResult(extName, owner, nil, extension.RemoteOverlayState{})
	}
	focused, err := remoteTUI.HideTopOverlay(ctx)
	if err != nil {
		return nil, err
	}
	return b.focusResult(extName, owner, nil, extension.RemoteOverlayState{FocusOwner: focused})
}

// handleCustomSetFocus is Pi's TUI.setFocus (tui.ts:598-600) for the tui a factory receives. The target is nothing, the editor,
// or a mounted overlay of the extension.
func (b *UIBridge) handleCustomSetFocus(ctx context.Context, extName string, owner *Conn, ui extension.UIContext, args json.RawMessage) (*CallResultPayload, error) {
	var request struct {
		Target RemoteOverlayTargetPayload `json:"target"`
	}
	if err := json.Unmarshal(args, &request); err != nil {
		return nil, fmt.Errorf("parse setFocus args: %w", err)
	}
	target, err := b.overlayFocusTarget(extName, owner, request.Target)
	if err != nil {
		return nil, err
	}
	remoteTUI, ok := ui.(extension.RemoteTUI)
	if !ok {
		return b.focusResult(extName, owner, nil, extension.RemoteOverlayState{})
	}
	focused, err := remoteTUI.SetRemoteFocus(ctx, target)
	if err != nil {
		return nil, err
	}
	return b.focusResult(extName, owner, nil, extension.RemoteOverlayState{FocusOwner: focused})
}

func sendCustomOpened(ctx context.Context, owner *Conn, key string, proxy *overlayProxy) error {
	state, err := proxy.Control(ctx, "", false, nil)
	if err != nil {
		return err
	}
	args, err := json.Marshal(struct {
		Key string `json:"key"`
		extension.RemoteOverlayState
	}{Key: key, RemoteOverlayState: state})
	if err != nil {
		return err
	}
	return owner.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: NotifyUICustomOpened, Args: args}})
}
