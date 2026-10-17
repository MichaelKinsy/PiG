package ai

import (
	"runtime"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// ProductVersion is PiG's composite release version for outbound identifiers.
// It defaults to pigversion.Version because importing coding would create a cycle.
var ProductVersion = pigversion.Version

// pig divergence (D65): upstream getPiUserAgent
// (packages/ai/src/utils/pi-user-agent.ts) reports "pi (<platform> <release>;
// <arch>)" from Node's os module, or "pi (browser)" with no Node/Bun runtime.
// PiG has no browser build and always runs as a native process, so
// PiUserAgent reports the same platform/release/arch shape with PiG's own
// product name and composite version instead: "pig/<coding.Version>
// (<platform> <release>; <arch>)" (owner decision, delivery/OWNER-DECISIONS.md
// Q4).
func PiUserAgent() string {
	return pigidentity.UserAgentProduct + "/" + ProductVersion + " (" + piUserAgentPlatform(runtime.GOOS) + " " + userAgentRelease.get() + "; " + piUserAgentArch(runtime.GOARCH) + ")"
}

// releaseCache reads the kernel release once: every request's User-Agent header reports it, and the release cannot change while the process runs, so the uname system call (five sysctls on darwin) is not repeated for each request.
type releaseCache struct {
	read  func() string
	once  sync.Once
	value string
}

func (cache *releaseCache) get() string {
	cache.once.Do(func() { cache.value = cache.read() })
	return cache.value
}

var userAgentRelease = &releaseCache{read: piUserAgentRelease}

// piUserAgentPlatform maps runtime.GOOS to the vocabulary Node's
// os.platform() uses, since upstream's user agent reports that value.
func piUserAgentPlatform(goos string) string {
	if goos == "windows" {
		return "win32"
	}
	return goos
}

// piUserAgentArch maps runtime.GOARCH to the vocabulary Node's os.arch()
// uses, since upstream's user agent reports that value.
func piUserAgentArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	default:
		return goarch
	}
}
