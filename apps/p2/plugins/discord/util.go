package discord

import (
	"fmt"
	"os"
	"math"
	"time"
)

func timeNow() time.Time { return time.Now() }

// floorHalf is Math.round: halves round up.
func floorHalf(value float64) float64 { return math.Floor(value + 0.5) }

func dbg(text string) {
	f, err := os.OpenFile("/tmp/discord-dbg.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		fmt.Fprintf(f, "%d %s\n", os.Getpid(), text)
		f.Close()
	}
}
