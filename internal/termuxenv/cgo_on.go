//go:build cgo

package termuxenv

// cgoResolver reports that Go's net package resolves names through the C
// library, which on Android is bionic's resolver and needs no configuration
// file.
const cgoResolver = true
