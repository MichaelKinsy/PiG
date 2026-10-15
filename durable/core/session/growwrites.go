//go:build !tinygo

package session

import "slices"

// growWrites makes room for n more writes in one allocation: a 20,000-task finalize appends 20,000 writes, and doubling
// would allocate about twice their size in garbage. The TinyGo build keeps append's doubling (growwrites_tinygo.go).
func growWrites(writes []Write, n int) []Write { return slices.Grow(writes, n) }
