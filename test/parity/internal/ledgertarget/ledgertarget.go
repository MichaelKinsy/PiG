// Package ledgertarget pins the platform the interface ledger is derived for. The committed mapping and gap baseline are what
// CI's Linux job derives: Linux-only files (*_linux.go, //go:build linux tests, norace gates) are evidence there and absent on
// macOS or Windows, so a derivation on another host reopens rows CI closes. Every loader that feeds the ledger loads packages
// for this target, and the derivation cache keys on it, whatever the host.
package ledgertarget

import (
	"os"
	"strings"
)

const (
	// GOOS and GOARCH are CI's interface-closure job (ubuntu, amd64).
	GOOS   = "linux"
	GOARCH = "amd64"
)

// Env is the process environment with GOOS and GOARCH set to the target, for packages.Config.Env.
func Env() []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GOOS=") || strings.HasPrefix(kv, "GOARCH=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GOOS="+GOOS, "GOARCH="+GOARCH)
}
