package subprocess

import (
	"context"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
)

// earlyModelStream is the model stream a Host with no UI bridge uses for the pi-ai calls a module makes while it loads. Pi runs pi-ai in the extension's own process,
// so a module may call it at top level or in its factory in any host, an SDK session included; the Session-owned surface is not there yet.
var earlyModelStream atomic.Pointer[func(context.Context, map[string]any, map[string]any) (*ai.AssistantMessageEventStream, error)]

// SetEarlyModelStream installs the stream every Host without a UI bridge uses for load-time pi-ai calls. The coding package installs it once, as it is a
// model-request conversion that package owns.
func SetEarlyModelStream(stream func(context.Context, map[string]any, map[string]any) (*ai.AssistantMessageEventStream, error)) {
	earlyModelStream.Store(&stream)
}

func isEarlyModelStreamCall(method string) bool {
	return method == "modelStream" || method == "cancelModelStream"
}

// earlyModelBridge is a bridge that carries only the early model stream, so the same handler serves the stream and its cancellation.
func (h *Host) earlyModelBridge() *UIBridge {
	h.earlyBridgeOnce.Do(func() {
		h.earlyBridge = NewUIBridge(func() {})
		if stream := earlyModelStream.Load(); stream != nil {
			h.earlyBridge.SetHostAction("streamModel", *stream)
		}
	})
	return h.earlyBridge
}
