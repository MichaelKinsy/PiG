package discord

import (
	"math"
	"time"
)

func timeNow() time.Time { return time.Now() }

// floorHalf is Math.round: halves round up.
func floorHalf(value float64) float64 { return math.Floor(value + 0.5) }
