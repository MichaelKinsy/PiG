package runtime

import "github.com/MichaelKinsy/PiG/internal/jsnumber"

// jsNumber is `String(n)` for a JavaScript number.
func jsNumber(n float64) string { return jsnumber.String(n) }
