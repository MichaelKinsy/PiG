//go:build !unix

package env

// nonBlock is zero where there are no FIFOs to open.
const nonBlock = 0
