package listencounter

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Extension is the Go twin of listen-counter.mjs: every tool answers what the Node one answers. Tool names carry the extension name so both register in one session. pig additive (D232).
func Extension() *sdk.Extension {
	const name = "go"
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	instance := hex.EncodeToString(buf)
	var calls atomic.Int64
	var mu sync.Mutex
	var heard []string
	text := func(value string) map[string]any { return map[string]any{"content": value} }

	ext := sdk.New("listen-counter-" + name)
	_, _ = ext.Events().On("listen-counter", func(_ sdk.Context, data any) error {
		from, _ := data.(map[string]any)["from"].(string)
		mu.Lock()
		heard = append(heard, from)
		mu.Unlock()
		return nil
	})
	ext.Tool(name+"_counter", "this instance's call count", sdk.Schema{"type": "object"}, func(sdk.Context, map[string]any) (any, error) {
		return text(fmt.Sprint(calls.Add(1))), nil
	})
	ext.Tool(name+"_ping", "emit on the event bus", sdk.Schema{"type": "object"}, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if err := ctx.Events().Emit("listen-counter", map[string]any{"from": name}); err != nil {
			return nil, err
		}
		return text("pinged"), nil
	})
	ext.Tool(name+"_received", "what this instance heard", sdk.Schema{"type": "object"}, func(sdk.Context, map[string]any) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		return text("received=" + strings.Join(heard, ",")), nil
	})
	ext.Tool(name+"_slow", "wait until the host cancels the call", sdk.Schema{"type": "object"}, func(ctx sdk.Context, _ map[string]any) (any, error) {
		<-ctx.Done()
		return text("cancelled"), nil
	})
	ext.OnSessionShutdown(func(sdk.Context, map[string]any) (any, error) {
		f, err := os.OpenFile(os.Getenv("LISTEN_FIXTURE_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		_, err = fmt.Fprintf(f, "%s:%s\n", name, instance)
		return nil, err
	})
	return ext
}
