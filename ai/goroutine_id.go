package ai

import "github.com/MichaelKinsy/PiG/internal/goroutineid"

// currentGoroutineID identifies the calling goroutine (goroutineid.Current); tests count its calls. A stream observation
// records its consumer's identity because Result cannot otherwise tell the goroutine that runs the consumer callback,
// which yields it as `await stream.result()` does, from a goroutine that only waits.
//
// A call formats the goroutine's stack header, so a goroutine that runs many callbacks asks once (CurrentStreamOwner).
var currentGoroutineID = goroutineid.Current
