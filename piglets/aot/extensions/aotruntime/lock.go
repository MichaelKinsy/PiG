package aotruntime

import (
	"crypto/ed25519"
	"encoding/hex"
)

// toolchainVersion is the Pi-Bolt release this Piglet downloads. Pi-Bolt (https://github.com/opensec-git/Pi-Bolt, MIT, © 2025 Mario Zechner,
// © 2026 OpenSec) publishes a Bun 1.4.3 canary with an ahead-of-time compiler for JavaScriptCore; the build below uses it as its compiler.
const toolchainVersion = "0.7.4"

// releaseURL hosts the release assets of toolchainVersion.
const releaseURL = "https://github.com/opensec-git/Pi-Bolt/releases/download/bolt-v" + toolchainVersion

// sourcesURL names the corresponding source of the compiler (`sources.json` and `patches/` of the release commit) for the notices.
const sourcesURL = "https://github.com/opensec-git/Pi-Bolt/tree/6088a1fe9395b10c666f6e25ba67c44b5d6453b8"

// checksumsFile and signatureFile are the release's checksum list and its Ed25519 signature over the file's bytes.
const (
	checksumsFile = "SHA256SUMS"
	signatureFile = "SHA256SUMS.sig"
)

// releaseKey is the raw Ed25519 public key of Pi-Bolt's release signature, pinned here: it is not fetched from the repository whose release it
// verifies. The key's PEM file keys/release.pub at commit 6088a1fe has SHA-256 ca62e43535443d2bb7235c954d565ec843dabf58a457e4bfaf27de5df4594a51.
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
	{"linux-x64", "pi-bolt-runtime-linux-x64.tar.gz", "43b5f41a07a8b6e1808921ca7d2f12b155fa717fa6656a68b4bd6c3399d9230a", "eed8510bbf2273306bd5d70c4c7b302ddedd2c200d48dcf0803c682dc321a2c0"},
	{"darwin-arm64", "pi-bolt-runtime-darwin-arm64.tar.gz", "f7a286369b9a01c6de4ca59f4209fc58b7168945513d469468c9233e53ec7b48", "ad7db62da6811451b225222b3e8d29109074acf5ff88303d4c7a67cdb7dfe54f"},
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
		panic("aotruntime: invalid hex " + s)
	}
	return out
}
