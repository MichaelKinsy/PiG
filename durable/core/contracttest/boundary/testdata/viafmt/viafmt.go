// Package viafmt is a deliberate violation through the standard library: fmt imports reflect, and only encoding/binary's
// route is accepted.
package viafmt

import "fmt"

func Name(v int) string { return fmt.Sprint(v) }
