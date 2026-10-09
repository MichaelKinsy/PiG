//go:build !pig_strip_node_extensions

package subprocess

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
)

// D65: the user agent PiG sends is pig/<coding.Version> (<platform> <release>;
// <arch>). The Node runtime learns the composite version from the host that
// spawns it (PIG_PRODUCT_VERSION), so the vendored getPiUserAgent reports the
// same identity as the Go host's ai.PiUserAgent in every isolation mode.
func TestNodeExtensionUserAgentNamesPiGAndItsVersion(t *testing.T) {
	entry := filepath.Join(t.TempDir(), "user-agent.mjs")
	write(t, entry, `import { getPackageDir } from "@earendil-works/pi-coding-agent";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
export default async function (pi) {
	const { getPiUserAgent } = await import(pathToFileURL(join(getPackageDir(), "utils", "pi-user-agent.js")).href);
	pi.registerCommand("user-agent", {
		description: JSON.stringify({ env: process.env.PIG_PRODUCT_VERSION, userAgent: getPiUserAgent("0.87.1") }),
		handler: async () => {},
	});
}
`)
	platform := runtime.GOOS
	if platform == "windows" {
		platform = "win32"
	}
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			t.Setenv("PIG_PRODUCT_VERSION", "inherited-and-stale")
			h := NewHost(t.TempDir())
			description := loadNodeCommandDescription(t, h, entry, isolation, "user-agent")
			var got struct{ Env, UserAgent string }
			if err := json.Unmarshal([]byte(description), &got); err != nil {
				t.Fatalf("probe %q: %v", description, err)
			}
			if got.Env != ai.ProductVersion {
				t.Errorf("PIG_PRODUCT_VERSION = %q, want the host's %q", got.Env, ai.ProductVersion)
			}
			if want := pigidentity.UserAgentProduct + "/" + ai.ProductVersion + " (" + platform + " "; !strings.HasPrefix(got.UserAgent, want) {
				t.Errorf("Node user agent = %q, want prefix %q", got.UserAgent, want)
			}
		})
	}
}
