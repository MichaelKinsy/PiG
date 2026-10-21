package ai

// Ports packages/ai/src/utils/version.ts (PI_AI_VERSION as ProductVersion).

import (
	"sync"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// ProductVersion is PiG's composite release version for outbound identifiers: the default version of a detailed User-Agent (PI_AI_VERSION upstream).
// It defaults to pigversion.Version because importing coding would create a cycle.
var ProductVersion = pigversion.Version

// pig divergence (D65): the detailed User-Agent of FormatUserAgent (identity.ts)
// reports "pi/<version> (<platform>; <runtime>; <arch>)" with Node's runtime
// description. PiG always runs as a native process, so it reports Node's os
// vocabulary instead: "pig/<coding.Version> (<platform> <release>; <arch>)"
// with PiG's own product name and composite version (owner decision,
// delivery/OWNER-DECISIONS.md Q4). The helpers below build the platform,
// release and arch fields.

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
