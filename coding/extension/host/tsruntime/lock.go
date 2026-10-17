package tsruntime

// LockEntry pins one executable PiG runs as a Bun runtime. A Bun runtime whose executable digest is not in the lock for its version
// and platform does not run: the lock, not the manifest, decides which Bun binaries PiG trusts.
type LockEntry struct {
	Runtime  string
	Version  string
	Platform string
	// Variant names the release archive the executable comes from, for review.
	Variant string
	SHA256  string
}

// Lock is the runtime lock. Each digest is the SHA-256 of the bun executable inside the release archive
// https://github.com/oven-sh/bun/releases/download/bun-v1.4.2/<variant>.zip, whose digest SHASUMS256.txt lists; that file carries a valid
// OpenPGP signature by Robobun <robobun@oven.sh>, key F3DCC08A8572C0749B3E18888EAB4D40A7B22B59. Bun 1.4.2's baseline archives
// carry the same executable as their default archives.
var Lock = []LockEntry{
	{Runtime: "bun", Version: "1.4.2", Platform: "linux-x64", Variant: "bun-linux-x64", SHA256: "a83d263767d839e4d2649ca8e35d07159c7afc99afdc96d731ced29e056dda0c"},
	{Runtime: "bun", Version: "1.4.2", Platform: "linux-x64", Variant: "bun-linux-x64-musl", SHA256: "16b72935ffd7a503b978c186874539c92aade4e3515b70a5abf5db2581fdef7d"},
	{Runtime: "bun", Version: "1.4.2", Platform: "linux-arm64", Variant: "bun-linux-aarch64", SHA256: "616f267a34278ff5ac282df37ffdfba1d7141f4f6926bca99af2cd6ef3ad32b1"},
	{Runtime: "bun", Version: "1.4.2", Platform: "linux-arm64", Variant: "bun-linux-aarch64-musl", SHA256: "1101cd0aa92ea214c2aaf4bb3761ca3c76a90aae0e6f94c07efb4e8c4f18a8fc"},
	{Runtime: "bun", Version: "1.4.2", Platform: "darwin-x64", Variant: "bun-darwin-x64", SHA256: "2fa513af22ac59e03aae640cad302e73cb1ddb0f6398501e2ddccf7dcd613596"},
	{Runtime: "bun", Version: "1.4.2", Platform: "darwin-arm64", Variant: "bun-darwin-aarch64", SHA256: "35d20dd0263e5c950194434b925454fdfa9ba6e4467da960410fa05b08a7a5b5"},
	{Runtime: "bun", Version: "1.4.2", Platform: "android-x64", Variant: "bun-linux-x64-android", SHA256: "12222610d9a72265ecb5955e09e009bc2b32cd9624427f8a76bec34965917054"},
	{Runtime: "bun", Version: "1.4.2", Platform: "android-arm64", Variant: "bun-linux-aarch64-android", SHA256: "f2970336bfccf36e4e8a408b25c627122f210e941672f184198c31f4dbd377a0"},
	{Runtime: "bun", Version: "1.4.2", Platform: "windows-x64", Variant: "bun-windows-x64", SHA256: "15277c59ccd6c6c20f8dc9716c2b59c1776320d606b6a8658f70be8799519ca4"},
	{Runtime: "bun", Version: "1.4.2", Platform: "windows-arm64", Variant: "bun-windows-aarch64", SHA256: "3d7e98d3201c55c3bde6c069a5dfa0d34da9edc9145187c76594f685f8aa54c6"},
}

// locked reports whether the lock pins digest as runtime at version for platform.
func locked(lock []LockEntry, runtime, version, platform, digest string) bool {
	for _, entry := range lock {
		if entry.Runtime == runtime && entry.Version == version && entry.Platform == platform && entry.SHA256 == digest {
			return true
		}
	}
	return false
}
