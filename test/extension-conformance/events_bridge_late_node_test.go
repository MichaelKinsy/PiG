package extensionconformance

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Upstream has one EventEmitter per loader (event-bus.ts:12-33), so a listener hears every emit, whenever its extension loaded. A native realm's first bus call switches the Host registry on; a Node realm that is spawning at that moment has started with its in-heap EventEmitter and has no connection yet to migrate over. It must join the registry once it registers, or a native emit never reaches its listeners. The first native call is swept across the Node realm's load, so one of the delays lands between its spawn and its connection.
func TestNativeEventBusFirstNativeCallWhileNodeRealmSpawns(t *testing.T) {
	root := findModuleRoot(t)
	for delay := time.Duration(0); delay <= time.Second; delay += 20 * time.Millisecond {
		finished := false
		t.Run(delay.String(), func(t *testing.T) {
			rig := newBusRig(t)
			fused := sdk.New("fused")
			if _, err := rig.host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "fused", Enabled: true}, fused.RunWithConn); err != nil {
				t.Fatal(err)
			}
			spec := busSpec("node", "isolated", "late", busListenerAt{"ch", "record"})
			spec.Log = rig.log
			spec.cwd = rig.cwd
			cfg := busConfig(t, root, spec)
			loaded := make(chan error, 1)
			go func() {
				_, err := rig.host.Load(t.Context(), cfg)
				loaded <- err
			}()
			time.Sleep(delay)
			if err := fused.Events().Emit("first", nil); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-loaded:
				// The Node realm had loaded before the first native call: later delays test nothing new.
				finished = true
				if err != nil {
					t.Fatal(err)
				}
			default:
				if err := <-loaded; err != nil {
					t.Fatal(err)
				}
			}
			if err := fused.Events().Emit("ch", map[string]any{"v": 1}); err != nil {
				t.Fatal(err)
			}
			rig.expect(`late|ch|{"v":1}`)
		})
		if finished {
			break
		}
	}
}
