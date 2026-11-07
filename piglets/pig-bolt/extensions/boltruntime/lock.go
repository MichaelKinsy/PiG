package boltruntime

import (
	"crypto/ed25519"
	"encoding/hex"
)

// toolchainVersion is the Pi-Bolt release this Piglet downloads. Pi-Bolt (https://github.com/opensec-git/Pi-Bolt, MIT, © 2025 Mario Zechner,
// © 2026 OpenSec) publishes a Bun 1.4.3 canary with an ahead-of-time compiler for JavaScriptCore; the build below uses it as its compiler.
const toolchainVersion = "1.1.2"

// releaseURL hosts the release assets of toolchainVersion.
const releaseURL = "https://github.com/opensec-git/Pi-Bolt/releases/download/bolt-v" + toolchainVersion

// sourcesURL names the corresponding source of the compiler (`sources.json` and `patches/` of the release commit) for the notices.
const sourcesURL = "https://github.com/opensec-git/Pi-Bolt/tree/1098b1ce19cf67daf8ab0995d7ef7e2a2bea8681"

// checksumsFile and signatureFile are the release's checksum list and its Ed25519 signature over the file's bytes.
const (
	checksumsFile = "SHA256SUMS"
	signatureFile = "SHA256SUMS.sig"
)

// releaseKey is the raw Ed25519 public key of Pi-Bolt's release signature, pinned here: it is not fetched from the repository whose release it
// verifies. The key's PEM file keys/release.pub at commit 1098b1ce (bolt-v1.1.2; the same file, byte for byte, as at 6088a1fe of 0.7.4) has SHA-256 ca62e43535443d2bb7235c954d565ec843dabf58a457e4bfaf27de5df4594a51.
var releaseKey = ed25519.PublicKey(mustHex("a0b6e826ab4a6a82123ea7df934def1d9afed6c4411b7b8844859e28e7b0f9a6"))

// archive is one runtime archive of the release for a platform: its SHA-256 as the signed checksum list names it, and the SHA-256 of the
// compiler executable inside it. Both are pinned; the signed list must agree with the pin.
type archive struct {
	platform string
	name     string
	sha256   string
	exeSHA   string
}

// archives are the platforms Pi-Bolt builds a compiler for. Other platforms stay on the runtimes PiG already has.
var archives = []archive{
	{"linux-x64", "pi-bolt-runtime-linux-x64.tar.gz", "7ac58dc777bbf07572c12dddd81f89e7ed8243ff840d89c91595581f2d9a24c2", "899a8faf69684dcdbb48e2cf32c40fec493f374f823e358b9292d58dcd42ddcc"},
	{"darwin-arm64", "pi-bolt-runtime-darwin-arm64.tar.gz", "53114a7acffe181bcfc4b26a7274927d49f65e15c030dc03abb5e331307471f4", "f69729e684c5cee7da1c165947e75276d2d2f23f168bde629b80fe3bd95e9eb0"},
}

func archiveFor(platform string) (archive, bool) {
	for _, a := range archives {
		if a.platform == platform {
			return a, true
		}
	}
	return archive{}, false
}

func mustHex(s string) []byte {
	out, err := hex.DecodeString(s)
	if err != nil {
		panic("boltruntime: invalid hex " + s)
	}
	return out
}
