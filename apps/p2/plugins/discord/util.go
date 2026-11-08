package discord

import (
	"context"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"math"
	"time"
)

func timeNow() time.Time { return time.Now() }

// floorHalf is Math.round: halves round up.
func floorHalf(value float64) float64 { return math.Floor(value + 0.5) }

// suspending makes each method suspend before it blocks on anything but the host's own primitives (Durable, the network): a
// handler runs in its call's section until it suspends, and a block there stops the host.
func suspending(methods map[string]contracts.MethodFunction) map[string]contracts.MethodFunction {
	for name, method := range methods {
		methods[name] = func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			chordctx.Suspend(ctx)
			return method(args, ctx)
		}
	}
	return methods
}
