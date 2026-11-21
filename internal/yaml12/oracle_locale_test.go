package yaml12

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// requireEnUSOracleLocale guards a Node oracle script whose output depends on V8/ICU's default locale (Date.prototype.toString
// prints localized long zone names; yaml12's generated zonenames_gen.go holds en-US names only). On Unix/macOS the child
// process pins LC_ALL=en_US.UTF-8 (which wins over LC_MESSAGES and LANG in ICU's uprv_getPOSIXIDForCategory). On Windows ICU
// reads the user account's culture via GetLocaleInfoEx and ignores LC_ALL, and Node has no CLI flag to override ICU's default
// locale, so if a Windows host's user culture is not en-US the script fails loudly instead of producing misleading zone-name diffs.
const requireEnUSOracleLocale = `
const __defaultLocale = new Intl.DateTimeFormat().resolvedOptions().locale;
if (__defaultLocale !== "en-US") {
  throw new Error("yaml12 oracle requires Node default locale en-US (got " + __defaultLocale + "); on Unix LC_ALL=en_US.UTF-8 pins it, on Windows ICU reads the user account culture");
}
`

// runEnUSOracle runs body in a Node child pinned to LC_ALL=en_US.UTF-8 and asserts that Node's ICU default locale is en-US.
func runEnUSOracle(t *testing.T, body string, input, output any, env ...string) {
	t.Helper()
	fullEnv := make([]string, 0, len(env)+1)
	fullEnv = append(fullEnv, "LC_ALL=en_US.UTF-8")
	fullEnv = append(fullEnv, env...)
	pioracle.Run(t, requireEnUSOracleLocale+body, input, output, fullEnv...)
}
