package extensionconformance

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The host's call dispatcher is the denominator for what an extension can ask
// the host to do. Every SDK must be able to reach every capability in it,
// because an extension is portable between SDKs by rewriting it, not by
// dropping a feature.
//
// This gate proves reachability, not agreement. A capability answered with a
// plausible constant is reachable and wrong, and only the runtime conformance
// comparison in this package catches that. Both are required: this one fails
// when an SDK cannot express a capability at all, that one fails when two SDKs
// answer the same question differently.
//
// The gate is written to rot loudly. Every entry below is checked in both
// directions, so an exemption whose capability disappears, or whose SDK has
// since gained the call, fails rather than lingering. Two hand-maintained
// upstream parity lists in tests/upstream-parity went fifteen versions stale
// while reporting green, which is the failure this structure exists to avoid.

// nodeStateBackedCapabilities are capabilities the Node runtime answers from
// its replicated state instead of a host call, because the upstream API is
// synchronous and a subprocess call is not. The value is the accessor that must
// exist in runtime.mjs, so the exemption names a specific mechanism rather than
// granting Node a blanket difference.
var nodeStateBackedCapabilities = map[string]string{
	"getActiveTools":         "getActiveTools",
	"getAllTools":            "getAllTools",
	"getCommands":            "getCommands",
	"getContextUsage":        "getContextUsage",
	"getFlag":                "getFlag",
	"getLeafID":              "getLeafId",
	"getModel":               "find",
	"getSessionFile":         "getSessionFile",
	"getSessionID":           "getSessionId",
	"getSessionName":         "getSessionName",
	"getSystemPrompt":        "getSystemPrompt",
	"getSystemPromptOptions": "getSystemPromptOptions",
	"getThinkingLevel":       "getThinkingLevel",
	"hasPendingMessages":     "hasPendingMessages",
	"isIdle":                 "isIdle",
	"isProjectTrusted":       "isProjectTrusted",
	"ui.getAllThemes":        "getAllThemes",
	"ui.getEditorText":       "getEditorText",
	"ui.getTheme":            "getTheme",
	"ui.getToolsExpanded":    "getToolsExpanded",
}

// sessionMirroredCapabilities are capabilities all SDKs answer from a local
// session mirror rather than a host call. The mirror is kept in sync by
// incremental appends in the state_update notify, so the capability is
// reachable but the wire call string does not appear in SDK source.
var sessionMirroredCapabilities = map[string]bool{
	"getBranch":  true,
	"getEntries": true,
}

// hostOnlyCapabilities are dispatcher entries no SDK is expected to call.
// Each names why the capability reaches extensions another way, or why it
// cannot cross the process boundary at all.
var hostOnlyCapabilities = map[string]string{
	"refreshTools":          "internal runtime plumbing: runner.ts:333 wires it from ExtensionActions and loader.ts calls it, so it is not extension-facing",
	"ui.theme":              "the active theme reaches extensions through the theme_change notify and the ready payload, not a query",
	"ui.getEditorComponent": "the editor component is a host-side factory that cannot be serialized back over RPC",
}

// knownCapabilityGaps records capabilities an SDK cannot reach today, so they
// stay visible instead of passing as an exemption. Closing one requires
// deleting its entry here, which the gate enforces by failing when a listed SDK
// has since gained the call.
var knownCapabilityGaps = map[string]map[string]string{
	"complete": {
		"node": "host-backed completion is unavailable to Node extensions",
	},
	"getModelInfo": {
		"node": "host model introspection is unavailable to Node extensions",
	},
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Dir(wd))
}

func readRepoFile(t *testing.T, root string, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{root}, parts...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

var dispatcherCaseRE = regexp.MustCompile(`case "([^"]+)"`)

// wireCapabilities returns the capability names the host call dispatcher
// accepts, which is the set an extension can invoke.
func wireCapabilities(t *testing.T, root string) []string {
	t.Helper()
	src := readRepoFile(t, root, "coding", "extension", "host", "subprocess", "ui_bridge.go")
	_, after, ok := strings.Cut(src, "func (b *UIBridge) handleCall(")
	if !ok {
		t.Fatal("handleCall dispatcher not found in ui_bridge.go; update this gate's extraction")
	}
	body, _, ok := strings.Cut(after, "\nfunc ")
	if !ok {
		t.Fatal("could not bound the handleCall body")
	}
	var caps []string
	for _, m := range dispatcherCaseRE.FindAllStringSubmatch(body, -1) {
		caps = append(caps, m[1])
	}
	if len(caps) == 0 {
		t.Fatal("extracted zero capabilities; the dispatcher shape changed")
	}
	slices.Sort(caps)
	return slices.Compact(caps)
}

// sdkSources maps each SDK to the source that must contain a capability's wire
// name for that SDK to be able to invoke it.
func sdkSources(t *testing.T, root string) map[string]string {
	t.Helper()
	return map[string]string{
		"go": readRepoFile(t, root, "extensions", "sdk", "context.go") +
			readRepoFile(t, root, "extensions", "sdk", "extension.go"),
		"rust": readRepoFile(t, root, "extensions", "sdk-rs", "src", "context.rs"),
		"py":   readRepoFile(t, root, "extensions", "sdk-py", "pig_sdk", "__init__.py"),
		"node": readRepoFile(t, root, "coding", "extension", "host", "subprocess", "runtime-node", "runtime.mjs"),
	}
}

func sdkNames() []string { return []string{"go", "node", "py", "rust"} }

func TestModelAuthUsesOneWireShapeAcrossSDKs(t *testing.T) {
	root := repoRoot(t)
	sources := sdkSources(t, root)
	checks := map[string]struct {
		required  string
		forbidden string
	}{
		"go":   {required: `"provider": providerID`, forbidden: `"providerId": providerID`},
		"node": {required: `{ provider: providerIDFromModel(model), modelId: modelIDFromModel(model) }`, forbidden: `{ providerId: providerIDFromModel(model)`},
		"py":   {required: `"provider": provider_id`, forbidden: `"providerId": provider_id`},
		"rust": {required: `"provider": provider_id`, forbidden: `"providerId": provider_id`},
	}
	for _, sdk := range sdkNames() {
		check := checks[sdk]
		if !strings.Contains(sources[sdk], check.required) {
			t.Errorf("the %s SDK does not send the getModelAuth provider field", sdk)
		}
		if strings.Contains(sources[sdk], check.forbidden) {
			t.Errorf("the %s SDK sends providerId; getModelAuth requires the common provider field", sdk)
		}
	}
}

func TestEverySDKCanReachEveryWireCapability(t *testing.T) {
	root := repoRoot(t)
	caps := wireCapabilities(t, root)
	sources := sdkSources(t, root)

	for _, capability := range caps {
		if reason, hostOnly := hostOnlyCapabilities[capability]; hostOnly {
			t.Logf("%s: host-only (%s)", capability, reason)
			continue
		}
		if sessionMirroredCapabilities[capability] {
			t.Logf("%s: answered from session mirror in all SDKs", capability)
			continue
		}
		for _, sdk := range sdkNames() {
			if strings.Contains(sources[sdk], fmt.Sprintf("%q", capability)) {
				continue
			}
			if sdk == "node" {
				if _, stateBacked := nodeStateBackedCapabilities[capability]; stateBacked {
					continue
				}
			}
			if reason, known := knownCapabilityGaps[capability][sdk]; known {
				t.Logf("%s: %s gap recorded (%s)", capability, sdk, reason)
				continue
			}
			t.Errorf("the %s SDK cannot reach %q, which the host dispatcher accepts. "+
				"Add the call so an extension is portable to this SDK, or record it in "+
				"knownCapabilityGaps with what is missing.", sdk, capability)
		}
	}
}

// A Node exemption must name an accessor that exists. Without this, deleting
// getEditorText from the runtime would leave the capability exempted and
// unreachable at once, which is the shape of the stub bugs this gate follows.
func TestNodeStateBackedCapabilitiesHaveAnAccessor(t *testing.T) {
	root := repoRoot(t)
	runtime := sdkSources(t, root)["node"]

	for capability, accessor := range nodeStateBackedCapabilities {
		pattern := regexp.MustCompile(`(?m)^\s+(?:async\s+)?` + regexp.QuoteMeta(accessor) + `\s*[(:]`)
		if !pattern.MatchString(runtime) {
			t.Errorf("%q is exempted as Node state-backed via %q, but runtime.mjs defines no such accessor. "+
				"Either restore it or move the capability to knownCapabilityGaps.", capability, accessor)
		}
	}
}

// Every entry must still describe reality. An exemption for a capability the
// dispatcher no longer accepts, or a gap an SDK has since closed, has to be
// deleted rather than left to accumulate.
func TestCapabilityExemptionsAreCurrent(t *testing.T) {
	root := repoRoot(t)
	caps := wireCapabilities(t, root)
	sources := sdkSources(t, root)
	known := func(c string) bool { return slices.Contains(caps, c) }

	for capability := range nodeStateBackedCapabilities {
		if !known(capability) {
			t.Errorf("nodeStateBackedCapabilities names %q, which the host dispatcher no longer accepts", capability)
		}
	}
	for capability := range hostOnlyCapabilities {
		if !known(capability) {
			t.Errorf("hostOnlyCapabilities names %q, which the host dispatcher no longer accepts", capability)
		}
	}
	for capability := range sessionMirroredCapabilities {
		if !known(capability) {
			t.Errorf("sessionMirroredCapabilities names %q, which the host dispatcher no longer accepts", capability)
		}
	}
	for capability, gaps := range knownCapabilityGaps {
		if !known(capability) {
			t.Errorf("knownCapabilityGaps names %q, which the host dispatcher no longer accepts", capability)
			continue
		}
		for sdk, reason := range gaps {
			if !slices.Contains(sdkNames(), sdk) {
				t.Errorf("knownCapabilityGaps[%q] names unknown SDK %q", capability, sdk)
				continue
			}
			if strings.TrimSpace(reason) == "" {
				t.Errorf("knownCapabilityGaps[%q][%q] has no reason", capability, sdk)
			}
			if strings.Contains(sources[sdk], fmt.Sprintf("%q", capability)) {
				t.Errorf("knownCapabilityGaps records %q missing from the %s SDK, but it now calls it. "+
					"Delete the entry so the gap count stays honest.", capability, sdk)
			}
		}
	}
}
