// SPDX-License-Identifier: MIT

package sdkscenarios

// lispPhase2 is every row the Lisp SDK does not serve yet, with the exact observation it produces today. The Lisp SDK fails loudly for each
// unserved member (it signals unsupported-capability naming the gap lane, which the host reports as the load or command error recorded here), so a
// row listed here fails the moment the Lisp SDK silently grows or loses behavior: the row must still produce exactly Got. The lane that
// serves the member deletes the entry.

const lispGapLane = "SdkLispPhase2"

var lispPhase2 = map[string]Red{
	"context/model-registry-answers-from-the-host-state": {Lane: lispGapLane, Why: "native Provider objects (registerNativeProvider), OAuth and model streams are not served by the Lisp SDK yet", Got: []string{
		"<the extension failed to load: extension \"scenario-driver-lisp\": register:register_failed: scenario-driver-lisp: native Provider objects (registerNativeProvider) is not served by the Lisp SDK yet (gap lane SdkLispPhase2; see docs/extension-api-parity.md)>",
	}},
	"host-events/provider-superseded-drops-the-old-registration": {Lane: lispGapLane, Why: "native Provider objects (registerNativeProvider), OAuth and model streams are not served by the Lisp SDK yet", Got: []string{
		"<the extension failed to load: extension \"scenario-driver-lisp\": register:register_failed: scenario-driver-lisp: native Provider objects (registerNativeProvider) is not served by the Lisp SDK yet (gap lane SdkLispPhase2; see docs/extension-api-parity.md)>",
	}},
	"providers-auth/apikey-own-login": {Lane: lispGapLane, Why: "native Provider objects (registerNativeProvider), OAuth and model streams are not served by the Lisp SDK yet", Got: []string{
		"<the extension failed to load: extension \"scenario-driver-lisp\": register:register_failed: scenario-driver-lisp: native Provider objects (registerNativeProvider) is not served by the Lisp SDK yet (gap lane SdkLispPhase2; see docs/extension-api-parity.md)>",
	}},
	"providers-auth/credential-key-order-is-the-logins": {Lane: lispGapLane, Why: "native Provider objects (registerNativeProvider), OAuth and model streams are not served by the Lisp SDK yet", Got: []string{
		"<the extension failed to load: extension \"scenario-driver-lisp\": register:register_failed: scenario-driver-lisp: native Provider objects (registerNativeProvider) is not served by the Lisp SDK yet (gap lane SdkLispPhase2; see docs/extension-api-parity.md)>",
	}},
	"providers-auth/every-prompt-and-event-variant": {Lane: lispGapLane, Why: "native Provider objects (registerNativeProvider), OAuth and model streams are not served by the Lisp SDK yet", Got: []string{
		"<the extension failed to load: extension \"scenario-driver-lisp\": register:register_failed: scenario-driver-lisp: native Provider objects (registerNativeProvider) is not served by the Lisp SDK yet (gap lane SdkLispPhase2; see docs/extension-api-parity.md)>",
	}},
	"providers-auth/methods-survive-registration": {Lane: lispGapLane, Why: "native Provider objects (registerNativeProvider), OAuth and model streams are not served by the Lisp SDK yet", Got: []string{
		"<the extension failed to load: extension \"scenario-driver-lisp\": register:register_failed: scenario-driver-lisp: native Provider objects (registerNativeProvider) is not served by the Lisp SDK yet (gap lane SdkLispPhase2; see docs/extension-api-parity.md)>",
	}},
	"providers-auth/oauth-login-asks-for-the-device-id-only-when-it-needs-it": {Lane: lispGapLane, Why: "native Provider objects (registerNativeProvider), OAuth and model streams are not served by the Lisp SDK yet", Got: []string{
		"<the extension failed to load: extension \"scenario-driver-lisp\": register:register_failed: scenario-driver-lisp: native Provider objects (registerNativeProvider) is not served by the Lisp SDK yet (gap lane SdkLispPhase2; see docs/extension-api-parity.md)>",
	}},
	"providers-auth/oauth-login-gets-the-device-id": {Lane: lispGapLane, Why: "native Provider objects (registerNativeProvider), OAuth and model streams are not served by the Lisp SDK yet", Got: []string{
		"<the extension failed to load: extension \"scenario-driver-lisp\": register:register_failed: scenario-driver-lisp: native Provider objects (registerNativeProvider) is not served by the Lisp SDK yet (gap lane SdkLispPhase2; see docs/extension-api-parity.md)>",
	}},
	"providers-auth/oauth-login-select": {Lane: lispGapLane, Why: "native Provider objects (registerNativeProvider), OAuth and model streams are not served by the Lisp SDK yet", Got: []string{
		"<the extension failed to load: extension \"scenario-driver-lisp\": register:register_failed: scenario-driver-lisp: native Provider objects (registerNativeProvider) is not served by the Lisp SDK yet (gap lane SdkLispPhase2; see docs/extension-api-parity.md)>",
	}},
	"providers-auth/oauth-login-select-cancel": {Lane: lispGapLane, Why: "native Provider objects (registerNativeProvider), OAuth and model streams are not served by the Lisp SDK yet", Got: []string{
		"<the extension failed to load: extension \"scenario-driver-lisp\": register:register_failed: scenario-driver-lisp: native Provider objects (registerNativeProvider) is not served by the Lisp SDK yet (gap lane SdkLispPhase2; see docs/extension-api-parity.md)>",
	}},
	"providers-auth/oauth-login-without-a-device-id": {Lane: lispGapLane, Why: "native Provider objects (registerNativeProvider), OAuth and model streams are not served by the Lisp SDK yet", Got: []string{
		"<the extension failed to load: extension \"scenario-driver-lisp\": register:register_failed: scenario-driver-lisp: native Provider objects (registerNativeProvider) is not served by the Lisp SDK yet (gap lane SdkLispPhase2; see docs/extension-api-parity.md)>",
	}},
	"providers-auth/oauth-refresh-persists": {Lane: lispGapLane, Why: "native Provider objects (registerNativeProvider), OAuth and model streams are not served by the Lisp SDK yet", Got: []string{
		"<the extension failed to load: extension \"scenario-driver-lisp\": register:register_failed: scenario-driver-lisp: native Provider objects (registerNativeProvider) is not served by the Lisp SDK yet (gap lane SdkLispPhase2; see docs/extension-api-parity.md)>",
	}},
	"providers-auth/provider-without-auth-methods-registers": {Lane: lispGapLane, Why: "native Provider objects (registerNativeProvider), OAuth and model streams are not served by the Lisp SDK yet", Got: []string{
		"<the extension failed to load: extension \"scenario-driver-lisp\": register:register_failed: scenario-driver-lisp: native Provider objects (registerNativeProvider) is not served by the Lisp SDK yet (gap lane SdkLispPhase2; see docs/extension-api-parity.md)>",
	}},
	"providers-auth/refreshed-credential-keeps-its-key-order": {Lane: lispGapLane, Why: "native Provider objects (registerNativeProvider), OAuth and model streams are not served by the Lisp SDK yet", Got: []string{
		"<the extension failed to load: extension \"scenario-driver-lisp\": register:register_failed: scenario-driver-lisp: native Provider objects (registerNativeProvider) is not served by the Lisp SDK yet (gap lane SdkLispPhase2; see docs/extension-api-parity.md)>",
	}},
	"providers-auth/runtime-composes-the-provider-object": {Lane: lispGapLane, Why: "native Provider objects (registerNativeProvider), OAuth and model streams are not served by the Lisp SDK yet", Got: []string{
		"<the extension failed to load: extension \"scenario-driver-lisp\": register:register_failed: scenario-driver-lisp: native Provider objects (registerNativeProvider) is not served by the Lisp SDK yet (gap lane SdkLispPhase2; see docs/extension-api-parity.md)>",
	}},
	"providers-auth/stored-key-resolves-and-checks": {Lane: lispGapLane, Why: "native Provider objects (registerNativeProvider), OAuth and model streams are not served by the Lisp SDK yet", Got: []string{
		"<the extension failed to load: extension \"scenario-driver-lisp\": register:register_failed: scenario-driver-lisp: native Provider objects (registerNativeProvider) is not served by the Lisp SDK yet (gap lane SdkLispPhase2; see docs/extension-api-parity.md)>",
	}},
	"providers-auth/unknown-event-type-is-an-error": {Lane: lispGapLane, Why: "native Provider objects (registerNativeProvider), OAuth and model streams are not served by the Lisp SDK yet", Got: []string{
		"<the extension failed to load: extension \"scenario-driver-lisp\": register:register_failed: scenario-driver-lisp: native Provider objects (registerNativeProvider) is not served by the Lisp SDK yet (gap lane SdkLispPhase2; see docs/extension-api-parity.md)>",
	}},
	"providers-auth/unknown-prompt-type-is-an-error": {Lane: lispGapLane, Why: "native Provider objects (registerNativeProvider), OAuth and model streams are not served by the Lisp SDK yet", Got: []string{
		"<the extension failed to load: extension \"scenario-driver-lisp\": register:register_failed: scenario-driver-lisp: native Provider objects (registerNativeProvider) is not served by the Lisp SDK yet (gap lane SdkLispPhase2; see docs/extension-api-parity.md)>",
	}},
	"registrations/late-registrations-reach-the-host": {Lane: lispGapLane, Why: "message and entry renderers (registerMessageRenderer, registerEntryRenderer) are not served by the Lisp SDK yet", Got: []string{
		"command add-command registered",
		"command add-shortcut registered",
		"command add-flag registered",
		"command add-message-renderer error: message-renderer is not served by the Lisp SDK yet (gap lane SdkLispPhase2; see docs/extension-api-parity.md)",
		"command add-entry-renderer error: entry-renderer is not served by the Lisp SDK yet (gap lane SdkLispPhase2; see docs/extension-api-parity.md)",
		"command add-command \"Adds a command\"",
		"command add-shortcut \"Adds a shortcut\"",
		"command add-flag \"Adds a flag\"",
		"command add-message-renderer \"Adds a message renderer\"",
		"command add-entry-renderer \"Adds an entry renderer\"",
		"command late-command \"Registered late\"",
		"shortcut ctrl+alt+l \"Registered late\"",
		"flag late-flag type=string default=\"late\" \"Registered late\"",
		"command late-command ran late",
		"shortcut ctrl+alt+l fired late",
	}},
}

// applyLispGaps marks the scenarios the Lisp SDK has not ported as Red for Lisp.
func applyLispGaps(all []Scenario) []Scenario {
	for i := range all {
		if red, ok := lispPhase2[all[i].Group+"/"+all[i].ID]; ok {
			if all[i].Red == nil {
				all[i].Red = map[SDK]Red{}
			}
			all[i].Red[Lisp] = red
		}
	}
	return all
}
