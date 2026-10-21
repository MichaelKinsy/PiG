package subprocess_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
)

// vendoredIdentityPatches lists, per vendored file (relative to shims/pi-dist),
// the PiG product identity that automation/gen/pi-identity-patches.mjs applies
// to Pi's outbound identity and endpoint literals (D26). Each entry replaces one
// exact Pi line; the values are pigidentity's, the same ones the Go host sends.
// The table is spelled out here independently of the patch script, so a wrong
// or missing patch fails.
var vendoredIdentityPatches = map[string][][2]string{
	"pi-ai/utils/identity.js": {
		{`export const PI_ATTRIBUTION = { name: "pi", url: "https://pi.dev", categories: ["cli-agent"] };`,
			`export const PI_ATTRIBUTION = { name: "` + pigidentity.AttributionName + `", url: "` + pigidentity.AttributionURL + `", categories: ["` + pigidentity.AttributionCategory + `"] };`},
		{"    if (mode === \"short\")\n        return \"pi\";\n    const product = `pi/${getClientVersion(identity)}`;\n    const runtime = runtimeDescription();\n    return runtime ? `${product} (${process.platform}; ${runtime}; ${process.arch})` : `${product} (browser)`;",
			"// pig divergence (D26, D65): PiG's product identity replaces Pi's; see automation/gen/pi-identity-patches.mjs.\n    if (mode === \"short\")\n        return \"" + pigidentity.UserAgentProduct + "\";\n    return pigUserAgent(\"" + pigidentity.UserAgentProduct + "\", identity?.version);"},
		{`import { PI_AI_VERSION } from "./version.js";`, "import { PI_AI_VERSION } from \"./version.js\";\nimport { pigUserAgent } from \"../../../pig-identity.mjs\";"},
	},
	"pi-coding-agent/core/client-identity.js": {
		{"        version: VERSION,", "// pig divergence (D26, D65): PiG's product identity replaces Pi's; see automation/gen/pi-identity-patches.mjs.\n        version: process.env.PIG_PRODUCT_VERSION ?? VERSION,"},
	},
	"pi-coding-agent/utils/pi-user-agent.js": {
		{`    return formatUserAgent({ userAgent: "detailed", version });`, "// pig divergence (D26, D65): PiG's product identity replaces Pi's; see automation/gen/pi-identity-patches.mjs.\n    return formatUserAgent({ userAgent: \"detailed\" });"},
	},
	"pi-ai/auth/oauth/openai-codex.js": {
		{`async function createAuthorizationFlow(originator = "pi") {`, `async function createAuthorizationFlow(originator = "` + pigidentity.CodexOriginator + `") {`},
	},
	"pi-ai/auth/oauth/openai-chatgpt.js": {
		{`const AGENT_NAME_HINT = "Pi";`, `const AGENT_NAME_HINT = "` + pigidentity.ChatGPTAgentName + `";`},
	},
	"pi-ai/auth/oauth/xai.js": {
		{`referrer: "pi",`, `referrer: "` + pigidentity.XAIReferrer + `",`},
	},
	"pi-coding-agent/core/remote-catalog-provider.js": {
		{`const DEFAULT_CATALOG_BASE_URL = "https://pi.dev";`, `const DEFAULT_CATALOG_BASE_URL = "` + pigidentity.HostedOrigin + `";`},
	},
	"pi-coding-agent/utils/version-check.js": {
		{`const LATEST_VERSION_URL = "https://pi.dev/api/latest-version";`, `const LATEST_VERSION_URL = "` + pigidentity.HostedOrigin + `/api/latest-version";`},
	},
	"pi-coding-agent/package-manager-cli.js": {
		{`const DEFAULT_INSTALLER_API_BASE = "https://pi.dev/api/installer/releases";`, `const DEFAULT_INSTALLER_API_BASE = "` + pigidentity.HostedOrigin + `/api/installer/releases";`},
	},
	"pi-coding-agent/modes/interactive/interactive-mode.js": {
		{"void fetch(`https://pi.dev/api/report-install?version=", "void fetch(`" + pigidentity.HostedOrigin + "/api/report-install?version="},
	},
	// D62: PiG never uploads bug reports; D64: PiG's /share never sends a Radius token to a gateway.
	"pi-coding-agent/core/bug-report-upload.js": {
		{"export async function uploadBugReport(bundle, options = {}) {\n", "export async function uploadBugReport(bundle, options = {}) {\n    throw new Error(\"PiG does not upload bug reports (D62): export the report and attach it to a PiG issue instead.\"); // pig divergence (D26, D62)\n"},
	},
	"pi-coding-agent/modes/interactive/session-share.js": {
		{"async function tryShareViaRadius(tmpFile, context) {\n", "async function tryShareViaRadius(tmpFile, context) {\n    return false; // pig divergence (D26, D64): PiG never sends a Radius token to Radius's share gateway.\n"},
	},
}

// applyVendoredIdentityPatches returns want with the file's identity patches
// applied, requiring every pinned line to be present.
func applyVendoredIdentityPatches(t *testing.T, path string, want []byte) []byte {
	t.Helper()
	for _, patch := range vendoredIdentityPatches[path] {
		if !bytes.Contains(want, []byte(patch[0])) {
			t.Errorf("pinned %s no longer contains the identity literal %q: update automation/gen/pi-identity-patches.mjs", path, patch[0])
			continue
		}
		want = bytes.ReplaceAll(want, []byte(patch[0]), []byte(patch[1]))
	}
	return want
}

// vendor-manifest.json records each seam of a patched file. A file that also
// carries module-specifier rewrites keeps that label next to the D26 identity
// label, and only a file whose identity patches alone produce the vendored
// bytes is labeled D26 alone.
func TestVendorManifestLabelsEveryIdentityPatchedSeam(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("runtime-node", "shims", "vendor-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Files []struct {
			Path    string `json:"path"`
			Rewrite string `json:"rewrite"`
		} `json:"files"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	const identity = "D26 PiG product identity (automation/gen/pi-identity-patches.mjs)"
	labels := map[string]string{}
	for _, record := range manifest.Files {
		labels[record.Path] = record.Rewrite
	}
	for rel := range vendoredIdentityPatches {
		label, ok := labels["pi-dist/"+rel]
		if !ok {
			t.Errorf("vendor manifest omits %s", rel)
			continue
		}
		pkg, file, _ := strings.Cut(rel, "/")
		pinned := readPinned(t, append(append([]string{}, pinnedPackageDist[pkg]...), strings.Split(file, "/")...)...)
		vendored, err := os.ReadFile(filepath.Join("runtime-node", "shims", "pi-dist", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		want := identity
		if !bytes.Equal(applyVendoredIdentityPatches(t, rel, pinned), vendored) {
			want = "module specifiers; " + identity
		}
		if label != want {
			t.Errorf("%s manifest rewrite = %q, want %q", rel, label, want)
		}
	}
}
