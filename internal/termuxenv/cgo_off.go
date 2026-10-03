//go:build !cgo

package termuxenv

// cgoResolver reports that Go's net package resolves names through its own DNS
// client, which reads /etc/resolv.conf.
const cgoResolver = false
