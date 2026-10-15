//go:build tinygo

package session

// growWrites leaves writes to grow by append on the TinyGo core: TinyGo's slices.Grow allocates a zeroed temporary of the
// added length as well (mirror.go), and its instantiation is an allocation site of its own (budget/allocs.json).
func growWrites(writes []Write, n int) []Write { return writes }
