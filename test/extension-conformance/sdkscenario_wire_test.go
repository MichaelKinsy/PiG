package extensionconformance

import (
	"bytes"
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/sdkscenarios"
)

// wireLog keeps the result bytes of every response an extension sent the host, as the SDK wrote them: the host's inbound observer
// sees each frame once decoded, with a response's result still the bytes on the wire. A row that is about an SDK's encoder, not
// about what the host makes of it, reads them here.
type wireLog struct {
	mu      sync.Mutex
	results [][]byte
}

func (w *wireLog) observe(_ string, env *subprocess.Envelope) {
	if env.Type != subprocess.MsgResponse || env.Response == nil || len(env.Response.Result) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.results = append(w.results, bytes.Clone(env.Response.Result))
}

// last returns the result of the latest response.
func (w *wireLog) last() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.results) == 0 {
		return nil
	}
	return w.results[len(w.results)-1]
}

// runToolWire runs the tool as a tool step does, then records how the latest result left the extension: whether the lone
// surrogate the tool returned (the text "a", U+D83D, "b") travelled as the escape \ud83d every SDK and JSON.stringify write, and
// whether the raw bytes of a surrogate (WTF-8 ED A0 BD, which no JSON reader outside the host's keeps) appear on the wire.
func (r *scenarioRig) runToolWire(ctx context.Context, step sdkscenarios.Step, lines *[]string) {
	r.runTool(ctx, step, lines)
	wire := r.wire.last()
	scenariof(lines, "tool %s wire carries the escape \\ud83d: %t, raw surrogate bytes: %t", step.Name,
		bytes.Contains(wire, []byte(`a\ud83db`)), bytes.Contains(wire, []byte("\xed\xa0\xbd")))
}
