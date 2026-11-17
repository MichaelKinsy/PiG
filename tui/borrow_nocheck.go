//go:build !pigborrowcheck

package tui

// The default build does not check that callers leave the lines of Container.Render and Box.Render alone (see borrow_check.go): the empty
// functions are inlined away.

func (c *Container) sealPublicLocked()   {}
func (c *Container) verifyPublicLocked() {}
func (b *Box) sealCacheLines()           {}
func (b *Box) verifyCacheLines()         {}
