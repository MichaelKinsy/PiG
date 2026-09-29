package subprocess

import (
	"context"
	"errors"
	"fmt"

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
		target = &extension.RemoteOverlayFocusTarget{}
		switch request.Target.Kind {
		case "null":
		case "editor":
			target.Editor = true
		case "overlay":
			b.mu.RLock()
			other, ok := b.customOverlays[customOverlayOwnerPrefix(extName, owner)+request.Target.Key].(*overlayProxy)
			b.mu.RUnlock()
			if !ok {
				return nil, fmt.Errorf("unknown overlay focus target: %s", request.Target.Key)
			}
			other.mu.Lock()
			target.Overlay = other.target
			other.mu.Unlock()
		default:
			return nil, fmt.Errorf("unknown overlay focus target kind: %s", request.Target.Kind)
		}
	}
	state, err := proxy.Control(ctx, request.Action, request.Hidden, target)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(state)
	return &CallResultPayload{Result: data}, err
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
