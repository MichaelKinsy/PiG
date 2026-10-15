package bunruntime

import (
	"crypto/ed25519"
	"encoding/hex"
)

// bunVersion is the Bun release this Piglet installs. PiG's runtime lock (coding/extension/host/tsruntime/lock.go) pins the same
// executables; a Bun the lock does not pin does not run.
const bunVersion = "1.4.2"

// releaseURL is the GitHub release that hosts the archives and their signed checksums.
const releaseURL = "https://github.com/oven-sh/bun/releases/download/bun-v" + bunVersion

// signingKey is the Ed25519 public key of Robobun <robobun@oven.sh>, which signs Bun's SHASUMS256.txt.asc; signingFingerprint is
// its OpenPGP v4 fingerprint F3DC C08A 8572 C074 9B3E 1888 8EAB 4D40 A7B2 2B59. They are pinned here, not fetched with the release.
var (
	signingKey         = ed25519.PublicKey(mustHex("929a40a9a5e5d119113b3e8d7ddbe5c1877652e5157c72e4d8d35823311d9d3f"))
	signingFingerprint = mustHex("f3dcc08a8572c0749b3e18888eab4d40a7b22b59")
)

// asset is one release archive: its name without .zip, the archive's SHA-256 as the signed SHASUMS256.txt lists it, and the SHA-256 of
// the bun executable inside it.
type asset struct {
	name     string
	zipSHA   string
	exeSHA   string
	exeName  string
	platform string
	musl     bool
}

// assets are the archives for PiG's eight release targets. Bun 1.4.2's baseline archives carry the same executable as these.
var assets = []asset{
	{"bun-linux-x64", "36368faef7527875d5ffa52e53cd48021741f2a83eb6208a8dd64068d422a913", "a83d263767d839e4d2649ca8e35d07159c7afc99afdc96d731ced29e056dda0c", "bun", "linux-x64", false},
	{"bun-linux-x64-musl", "4835eca59d6da70f4674f5642f6e459dcadab773695b2ed9922d131057989742", "16b72935ffd7a503b978c186874539c92aade4e3515b70a5abf5db2581fdef7d", "bun", "linux-x64", true},
	{"bun-linux-aarch64", "54328bbc2d9c8e0c9f892c544d66c57a83b84139e34909e5ee81758f1ac8fda7", "616f267a34278ff5ac282df37ffdfba1d7141f4f6926bca99af2cd6ef3ad32b1", "bun", "linux-arm64", false},
	{"bun-linux-aarch64-musl", "71760b6c8ea30623b81a4907cb815d48e2ea266f2e73e751534a44a0607950df", "1101cd0aa92ea214c2aaf4bb3761ca3c76a90aae0e6f94c07efb4e8c4f18a8fc", "bun", "linux-arm64", true},
	{"bun-darwin-x64", "80520d7e17526308c9185d261679ac6d27798d3803a0e9f7ff9121ab8affb012", "2fa513af22ac59e03aae640cad302e73cb1ddb0f6398501e2ddccf7dcd613596", "bun", "darwin-x64", false},
	{"bun-darwin-aarch64", "90987a3a16d7db556d886ac3d551e7b6d3edf0a1cf43acaed622e8676be1d12f", "35d20dd0263e5c950194434b925454fdfa9ba6e4467da960410fa05b08a7a5b5", "bun", "darwin-arm64", false},
	{"bun-linux-x64-android", "1e90fc0d3b83cb847b34338aff2765a7034bf53c50414344b3b0bf07a330a144", "12222610d9a72265ecb5955e09e009bc2b32cd9624427f8a76bec34965917054", "bun", "android-x64", false},
	{"bun-linux-aarch64-android", "a1c7e2983f1bb65146beb256a4d72449f23042412bc2cf278aa6397ba27e0274", "f2970336bfccf36e4e8a408b25c627122f210e941672f184198c31f4dbd377a0", "bun", "android-arm64", false},
	{"bun-windows-x64", "ce4c17497b2f29712a99d3d53f028de28cd42e3bacb8589599e7f000e49b6405", "15277c59ccd6c6c20f8dc9716c2b59c1776320d606b6a8658f70be8799519ca4", "bun.exe", "windows-x64", false},
	{"bun-windows-aarch64", "a7a16b876a305fd1029c66dbd27007b4f6112ae896532f675878731a21e50cfd", "3d7e98d3201c55c3bde6c069a5dfa0d34da9edc9145187c76594f685f8aa54c6", "bun.exe", "windows-arm64", false},
}

// assetFor returns the archive for a platform, or false when Bun has none.
func assetFor(pinned []asset, platform string, musl bool) (asset, bool) {
	for _, a := range pinned {
		if a.platform == platform && a.musl == musl {
			return a, true
		}
	}
	return asset{}, false
}

func mustHex(s string) []byte {
	out, err := hex.DecodeString(s)
	if err != nil {
		panic("bunruntime: invalid hex " + s)
	}
	return out
}
