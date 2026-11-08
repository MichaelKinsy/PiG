package terminals

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"sync"
)

// hostSources are the files the terminal host runs, the Go counterparts of the files Pi hashes in `terminalHostVersion`
// (detached.ts): main.ts is host.go (with writer.go and regexp.go, which the host's frames and waits use), daemon.ts and
// envs.ts are hostenv, ghostty.ts is the ghostty package, with protocol, detached, keys, plain and the wasm module as in Pi.
// Pi's log-preload.ts and runtime/logs.ts have no counterpart here: the host logs through the p2 runtime's logger, which
// the executable shares with everything else.
//
// Pi reads the files at run time. A Go program is its executable, so the sources are embedded: the version still changes
// when the host's code does, and not when another part of p2 does (which hashing the executable would).
//
//go:embed host.go writer.go regexp.go protocol.go detached.go detached_unix.go detached_windows.go keys.go plain.go
//go:embed hostenv/daemon.go hostenv/remote.go
//go:embed ghostty/ghostty.go ghostty/glue.go ghostty/keys.go
//go:embed vendor/ghostty/ghostty-vt.wasm
var hostSources embed.FS

// hostSourceFiles is the order they are hashed in, each preceded by its name (as Pi does).
var hostSourceFiles = []string{
	"host.go", "writer.go", "regexp.go", "hostenv/daemon.go", "protocol.go", "detached.go", "detached_unix.go", "detached_windows.go",
	"hostenv/remote.go", "ghostty/ghostty.go", "ghostty/glue.go", "ghostty/keys.go", "keys.go", "plain.go",
	"vendor/ghostty/ghostty-vt.wasm",
}

var hostVersion = sync.OnceValues(func() (string, error) {
	hash := sha256.New()
	for _, file := range hostSourceFiles {
		contents, err := hostSources.ReadFile(file)
		if err != nil {
			return "", err
		}
		hash.Write([]byte(file))
		hash.Write(contents)
	}
	return hex.EncodeToString(hash.Sum(nil))[:16], nil
})

// HostVersion is the terminal host's code version: a hash of the files it runs, 16 hex digits.
func HostVersion() (string, error) { return hostVersion() }
