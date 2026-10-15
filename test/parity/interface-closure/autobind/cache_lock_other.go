//go:build !unix

package main

// lock is a no-op where advisory file locks are unavailable: concurrent processes may compute the same content-addressed entry twice.
func (c *ledgerCache) lock(string, string) (release func()) { return func() {} }
