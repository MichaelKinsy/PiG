package subprocess_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
)

// Runs the vendored coding-agent modules that make Pi-owned requests (version
// check, remote catalog, bug-report upload) with fetch replaced by a recorder,
// and reports what each would have sent. Pi 0.87.1 sends these to pi.dev with
// its own "pi/<version>" or "pi (...)" user agent; PiG must not (D26, D62, D64).
const identityBehaviorScript = `
import { pathToFileURL } from "node:url";
import { join } from "node:path";
const root = process.argv[1];
const load = (path) => import(pathToFileURL(join(root, path)).href);
const calls = [];
globalThis.fetch = async (url, init = {}) => {
  calls.push({ url: String(url), userAgent: new Headers(init.headers ?? {}).get("user-agent") });
  return new Response("{}", { status: 404 });
};
const out = {};
const agentUA = await load("pi-coding-agent/utils/pi-user-agent.js");
const aiUA = await load("pi-ai/utils/pi-user-agent.js");
out.codingAgentUserAgent = agentUA.getPiUserAgent("0.87.1");
out.aiUserAgent = aiUA.getPiUserAgent();

const version = await load("pi-coding-agent/utils/version-check.js");
await version.getLatestPiRelease("0.87.1");
out.versionCheck = calls.splice(0);

const catalog = await load("pi-coding-agent/core/remote-catalog-provider.js");
const provider = catalog.withRemoteCatalog({ id: "openrouter", getModels: () => [] });
await provider.refreshModels({ allowNetwork: true, force: true, stored: undefined, signal: new AbortController().signal, publish: async () => true });
out.catalog = calls.splice(0);

const upload = await load("pi-coding-agent/core/bug-report-upload.js");
out.bugReportError = await upload.uploadBugReport({}).then(() => "uploaded", (error) => error.message);
out.bugReportCalls = calls.splice(0);
console.log(JSON.stringify(out));
`

func TestVendoredRuntimeRequestsAreNeverMadeAsPi(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("runtime-node", "shims", "pi-dist"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", identityBehaviorScript, root)
	cmd.Env = append(os.Environ(), "PIG_PRODUCT_VERSION=9.8.7+0.87.1", "PI_OFFLINE=")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, output)
	}
	var got struct {
		CodingAgentUserAgent string
		AIUserAgent          string
		VersionCheck         []struct{ URL, UserAgent string }
		Catalog              []struct{ URL, UserAgent string }
		BugReportError       string
		BugReportCalls       []struct{ URL, UserAgent string }
	}
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	platform := runtime.GOOS
	if platform == "windows" {
		platform = "win32"
	}
	for name, ua := range map[string]string{"coding-agent": got.CodingAgentUserAgent, "pi-ai": got.AIUserAgent} {
		if want := pigidentity.UserAgentProduct + "/9.8.7+0.87.1 (" + platform + " "; !strings.HasPrefix(ua, want) {
			t.Errorf("%s user agent = %q, want %q prefix (D65 shape: pig/<version> (<platform> <release>; <arch>))", name, ua, want)
		}
	}
	for name, calls := range map[string][]struct{ URL, UserAgent string }{"version check": got.VersionCheck, "remote catalog": got.Catalog} {
		if len(calls) != 1 {
			t.Errorf("%s made %d requests, want 1", name, len(calls))
			continue
		}
		if !strings.HasPrefix(calls[0].URL, pigidentity.HostedOrigin+"/") {
			t.Errorf("%s requested %q, want PiG's hosted origin %s (D64)", name, calls[0].URL, pigidentity.HostedOrigin)
		}
		if !strings.HasPrefix(calls[0].UserAgent, pigidentity.UserAgentProduct+"/") {
			t.Errorf("%s user agent = %q, want %s/<version>", name, calls[0].UserAgent, pigidentity.UserAgentProduct)
		}
	}
	if len(got.VersionCheck) == 1 && got.VersionCheck[0].URL != pigidentity.HostedOrigin+"/api/latest-version" {
		t.Errorf("version check URL = %q", got.VersionCheck[0].URL)
	}
	if !strings.Contains(got.BugReportError, "does not upload bug reports") || len(got.BugReportCalls) != 0 {
		t.Errorf("bug report upload = %q with %d requests, want a refusal that sends nothing (D62)", got.BugReportError, len(got.BugReportCalls))
	}
}
