package subprocess_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
)

// Imports the vendored coding-agent modules that would upload to Radius and calls them as an extension that imports
// Pi's classes would: the bug-report upload with a token and a loopback gateway, and the session share with the
// "radius" destination. fetch is replaced by a recorder that refuses; the session's model runtime reports every
// access, because the share reads the Radius credential from it before it sends anything.
const radiusUploadScript = `
import { pathToFileURL } from "node:url";
import { join } from "node:path";
const [root, gateway] = process.argv.slice(1);
const load = (path) => import(pathToFileURL(join(root, path)).href);
const fetchCalls = [];
globalThis.fetch = async (url) => {
  fetchCalls.push(String(url));
  throw new Error("the network was used");
};
const out = {};

const upload = await load("pi-coding-agent/core/bug-report-upload.js");
const bundle = { metadata: { id: "x" }, diagnostics: {}, sessionJsonl: "{}\n", summary: "summary" };
out.bugReportError = await upload.uploadBugReport(bundle, { gatewayUrl: gateway, token: "radius-secret" }).then(() => "uploaded", (error) => error.message);

const { SessionManager } = await load("pi-coding-agent/core/session-manager.js");
const { shareSession } = await load("pi-coding-agent/modes/interactive/session-share.js");
const touched = [];
const errors = [];
const statuses = [];
const refuse = (name) => () => { touched.push(name); throw new Error("touched " + name); };
const context = {
  session: {
    sessionManager: SessionManager.inMemory(),
    state: { systemPrompt: "system", tools: [] },
    settingsManager: { getSessionShareDestination: () => "radius" },
    modelRuntime: new Proxy({}, { get: (_, name) => refuse("modelRuntime." + String(name)) }),
    exportToHtml: refuse("exportToHtml"),
  },
  ui: new Proxy({}, { get: (_, name) => refuse("ui." + String(name)) }),
  editorContainer: new Proxy({}, { get: (_, name) => refuse("editorContainer." + String(name)) }),
  showError: (message) => errors.push(message),
  showStatus: (message) => statuses.push(message),
};
out.shareError = await shareSession(context).then(() => "", (error) => error.message);
out.shareErrors = errors;
out.shareStatuses = statuses;
out.touched = touched;
out.fetchCalls = fetchCalls;
console.log(JSON.stringify(out));
`

// pig divergence (D26, D62, D64): "The bug-report upload refuses, because PiG never uploads bug reports. The Radius share
// upload returns 'unavailable', because PiG never sends a Radius token to Radius's share gateway." Calling either from the
// vendored Pi JavaScript makes no network request (no fetch, nothing at a loopback gateway) and never reads the Radius
// credential. Red when the patch in automation/gen/pi-identity-patches.mjs for uploadBugReport or tryShareViaRadius is gone.
func TestVendoredRadiusUploadsRefuseAndMakeNoRequest(t *testing.T) {
	var gatewayRequests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		gatewayRequests.Add(1)
		http.Error(w, "unexpected request", http.StatusTeapot)
	}))
	defer gateway.Close()
	root, err := filepath.Abs(filepath.Join("runtime-node", "shims", "pi-dist"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", radiusUploadScript, root, gateway.URL)
	cmd.Env = append(os.Environ(), "PI_RADIUS_GATEWAY="+gateway.URL, "PI_OFFLINE=")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, output)
	}
	var got struct {
		BugReportError string
		ShareError     string
		ShareErrors    []string
		ShareStatuses  []string
		Touched        []string
		FetchCalls     []string
	}
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	const refusal = "PiG does not upload bug reports (D62): export the report and attach it to a PiG issue instead."
	if got.BugReportError != refusal {
		t.Errorf("uploadBugReport = %q, want the refusal %q", got.BugReportError, refusal)
	}
	if got.ShareError != "" {
		t.Errorf("shareSession failed: %s", got.ShareError)
	}
	// With the Radius destination and no Radius upload, the user is told Radius is not configured: the share is
	// unavailable and nothing was uploaded, shown or exported to HTML for the gist route.
	if want := []string{"Radius authentication is not configured. Run '/login radius' first."}; !slices.Equal(got.ShareErrors, want) || len(got.ShareStatuses) != 0 {
		t.Errorf("shareSession errors %q statuses %q, want only %q", got.ShareErrors, got.ShareStatuses, want)
	}
	if len(got.Touched) != 0 {
		t.Errorf("the share read the Radius provider, credential or UI before declining: %q", got.Touched)
	}
	if len(got.FetchCalls) != 0 {
		t.Errorf("fetch was called: %q", got.FetchCalls)
	}
	if n := gatewayRequests.Load(); n != 0 {
		t.Errorf("the loopback gateway received %d requests, want none", n)
	}
}
