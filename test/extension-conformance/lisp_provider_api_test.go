package extensionconformance

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// The Lisp twins of the Python SDK's Pi 0.99.1 rows for MCP servers, virtual models and provider configs
// (python_extension_api_test.go, mcp_server_order_test.go): each binds its assertion to a value only the Host or the Lisp SDK's answer can produce.

var lispMcpFixture = lispAPIFixture("lispapi-mcp", `
(defun listing (ctx)
  (csv (sort (map 'list (lambda (s) (format nil "~A@~A" (pig:jget s "name") (pig:jget s "extensionPath"))) (pig:ctx-get-mcp-servers ctx))
             #'string<)))
`, `
    (pig:register-mcp-server e "lisp-load" (pig:from-json "{\"url\":\"https://mcp.example/lisp-load\"}"))
    (pig:register-mcp-server e "lisp-dropped" (pig:from-json "{\"url\":\"https://mcp.example/dropped\"}"))
    (pig:unregister-mcp-server e "lisp-dropped")
    (pig:command e "add" "" (lambda (ctx args) (declare (ignore args))
      (pig:ctx-register-mcp-server ctx "lisp-late" (pig:from-json "{\"command\":\"lisp-mcp\",\"args\":[\"--stdio\"]}"))
      (lines ctx "after-add" (listing ctx))))
    (pig:command e "drop" "" (lambda (ctx args) (declare (ignore args))
      (pig:ctx-unregister-mcp-server ctx "lisp-late")
      (lines ctx "after-drop" (listing ctx))))
    (pig:command e "steal" "" (lambda (ctx args) (declare (ignore args))
      (handler-case (progn (pig:ctx-register-mcp-server ctx "owned-by-sibling" (pig:from-json "{\"url\":\"https://mcp.example/stolen\"}"))
                           (lines ctx "steal" "no error"))
        (pig:host-call-error (c) (lines ctx "steal" (pig:host-call-error-message c))))))
    (pig:command e "invalid" "" (lambda (ctx args) (declare (ignore args))
      (handler-case (progn (pig:ctx-register-mcp-server ctx "lisp-invalid" (pig:obj))
                           (lines ctx "invalid" "no error"))
        (pig:host-call-error (c) (lines ctx "invalid" (pig:host-call-error-message c))))))
    (pig:command e "show" "" (lambda (ctx args) (declare (ignore args)) (lines ctx "show" (listing ctx))))
`)

var lispMcpSiblingFixture = lispAPIFixture("lispapi-sibling", "", `
    (pig:register-mcp-server e "owned-by-sibling" (pig:from-json "{\"url\":\"https://mcp.example/sibling\"}"))
`)

// loader.ts:456-478 and 259-262: servers registered while the factory ran reach the runtime owned by the extension's path, a queued unregister removes its server, and after load the calls apply at once. getMcpServers lists the registry, including the path the Host assigned.
func TestLispSDKMcpServerRegistration(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newLispAPIRig(t, isolation, nil, lispMcpFixture, lispMcpSiblingFixture)
		mcpPath, siblingPath := rig.exts["lispapi-mcp"].Path, rig.exts["lispapi-sibling"].Path
		registered := func() []string {
			var out []string
			for _, s := range rig.host.Runtime().McpServers().List() {
				out = append(out, s.Name+"@"+s.ExtensionPath)
			}
			slices.Sort(out)
			return out
		}
		want := []string{"lisp-load@" + mcpPath, "owned-by-sibling@" + siblingPath}
		if got := registered(); !slices.Equal(got, want) {
			t.Fatalf("after load: %v, want %v", got, want)
		}
		rig.command("lispapi-mcp", "show", "")
		rig.waitNotification("show|" + strings.Join(want, ","))

		rig.command("lispapi-mcp", "add", "")
		want = []string{"lisp-late@" + mcpPath, "lisp-load@" + mcpPath, "owned-by-sibling@" + siblingPath}
		rig.waitNotification("after-add|" + strings.Join(want, ","))
		if got := registered(); !slices.Equal(got, want) {
			t.Fatalf("after add: %v, want %v", got, want)
		}
		var late extension.RegisteredMcpServer
		for _, s := range rig.host.Runtime().McpServers().List() {
			if s.Name == "lisp-late" {
				late = s
			}
		}
		if late.Config.Command != "lisp-mcp" || !slices.Equal(late.Config.Args, []string{"--stdio"}) {
			t.Fatalf("lisp-late config = %+v", late.Config)
		}

		rig.command("lispapi-mcp", "drop", "")
		want = []string{"lisp-load@" + mcpPath, "owned-by-sibling@" + siblingPath}
		rig.waitNotification("after-drop|" + strings.Join(want, ","))

		// loader.ts:463-470: a name another extension owns and an invalid config throw to the registering extension.
		rig.command("lispapi-mcp", "steal", "")
		rig.waitNotification(`steal|MCP server "owned-by-sibling" is already registered by extension "` + siblingPath + `"`)
		rig.command("lispapi-mcp", "invalid", "")
		waitFor(t, func() bool {
			for _, n := range rig.notifications() {
				if strings.HasPrefix(n, "invalid|Invalid MCP server registered by extension") {
					return true
				}
			}
			return false
		})
		if got := registered(); !slices.Equal(got, want) {
			t.Fatalf("after rejected registrations: %v, want %v", got, want)
		}
	})
}

var lispMcpOrderFixture = lispAPIFixture("lispapi-mcp-order", `
(defun listing (ctx)
  (pig:to-json (map 'vector (lambda (s) (vector (pig:jget s "name") (pig:jget s "config"))) (pig:ctx-get-mcp-servers ctx))))
`, `
    (pig:register-mcp-server e "ordered" (pig:from-json "{\"timeout\":5,\"url\":\"https://mcp.example/x\",\"toolExposure\":{\"b*\":\"direct\",\"a\":\"hidden\",\"2\":\"direct\"},\"headers\":{\"X-Z\":\"1\",\"A\":\"2\"},\"enabled\":true,\"exposure\":\"direct\",\"foo\":{\"zz\":1,\"aa\":[{\"y\":1,\"b\":2}]},\"oauth\":{\"scope\":\"s\",\"clientId\":\"c\"}}"))
    (pig:register-mcp-server e "stdio_one" (pig:from-json "{\"args\":[\"--x\"],\"env\":{\"Z\":\"1\",\"A\":\"2\"},\"cwd\":\".\",\"command\":\"mcp-bin\",\"type\":\"stdio\"}"))
    (pig:command e "order" "" (lambda (ctx args) (declare (ignore args)) (lines ctx "order" (listing ctx))))
`)

// loader.ts:456-478 and mcp-servers.ts:151-222, 283: an extension that reads getMcpServers sees each config as registered, in the order it wrote the members, with the members the host does not know.
func TestLispGetMcpServersKeepsRegisteredMemberOrder(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newLispAPIRig(t, isolation, nil, lispMcpOrderFixture)
		rig.command("lispapi-mcp-order", "order", "")
		rig.waitNotification("order|" + mcpServerOrderWant)
	})
}

var lispMcpAliasFixture = lispAPIFixture("lispapi-mcp-alias", `
(defun listing (ctx)
  (pig:to-json (map 'vector (lambda (s) (vector (pig:jget s "name") (pig:jget s "config"))) (pig:ctx-get-mcp-servers ctx))))
`, `
    (pig:register-mcp-server e "aliased" (pig:from-json "{\"command\":\"mcp-bin\",\"exposure\":\"codemode-deferred\",\"toolExposure\":{\"b*\":\"codemode-deferred\",\"a\":\"direct\"},\"enabled\":false}"))
    (pig:command e "alias" "" (lambda (ctx args) (declare (ignore args)) (lines ctx "alias" (listing ctx))))
`)

// mcp-servers.ts:151-166,192-196 and loader.ts:479 (0.99.2): an extension that reads getMcpServers sees the exposure an alias stands for, never the alias.
func TestLispGetMcpServersReportsResolvedExposureAliases(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newLispAPIRig(t, isolation, nil, lispMcpAliasFixture)
		rig.command("lispapi-mcp-alias", "alias", "")
		rig.waitNotification("alias|" + mcpServerAliasWant)
	})
}

var lispVirtualModelFixture = lispAPIFixture("lispapi-vm", `
(defun route (ctx request)
  (declare (ignore ctx))
  (let* ((state (pig:jget request "state")) (n (if (hash-table-p state) (or (pig:jget state "n") 0) 0)))
    (pig:obj "model" (pig:obj "provider" "anthropic" "id" (concatenate 'string "lisp-picked-" (pig:jget request "reason")))
             "thinkingLevel" (pig:jget request "thinkingLevel")
             "state" (pig:obj "n" (+ n 5) "saw" (length (pig:jget request "messages"))
                              "cancelled" (if (pig:abort-signal-set-p (pig:jget request "signal")) t :false)))))
`, `
    (pig:register-virtual-model e (pig:make-virtual-model :provider "lisprouter" :id "auto" :name "Lisp Auto" :route #'route
                                  :thinking-levels '("off" "low") :context-window 123456 :max-tokens 4321 :input '("text")))
    (pig:register-virtual-model e (pig:make-virtual-model :provider "lisprouter" :id "dropped" :name "Dropped" :route #'route))
    (pig:unregister-virtual-model e "lisprouter" "dropped")
    (pig:command e "add" "" (lambda (ctx args) (declare (ignore args))
      (pig:ctx-register-virtual-model ctx (pig:make-virtual-model :provider "lisprouter" :id "late" :name "Late" :route #'route))))
    (pig:command e "drop" "" (lambda (ctx args) (declare (ignore args)) (pig:ctx-unregister-virtual-model ctx "lisprouter" "auto")))
`)

// pi.registerVirtualModel (loader.ts:500-509) and unregisterVirtualModel (loader.ts:511-514), with virtual-models.ts:87-101: the declaration reaches the runtime queue without its route, the route runs in the extension against the request the Host sends, and the route names the physical model by provider and id for the model runtime to resolve (model-runtime.ts:1000-1009). The value in the state is computed by the router from the state it was sent.
func TestLispSDKVirtualModelRegistrationAndRouting(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newLispAPIRig(t, isolation, &subprocess.HostCallbacks{}, lispVirtualModelFixture)
		pending := func() map[string]extension.PendingVirtualModelRegistration {
			out := map[string]extension.PendingVirtualModelRegistration{}
			for _, p := range rig.host.Runtime().PendingVirtualModelRegistrations() {
				out[p.Definition.Provider+"/"+p.Definition.ID] = p
			}
			return out
		}
		got := pending()
		if len(got) != 1 {
			t.Fatalf("registerVirtualModel/unregisterVirtualModel: registered %v, want only lisprouter/auto (dropped was unregistered while the factory ran)", got)
		}
		p := got["lisprouter/auto"]
		def := p.Definition
		if def.Name != "Lisp Auto" || def.ContextWindow != 123456 || def.MaxTokens != 4321 || len(def.ThinkingLevels) != 2 || def.ThinkingLevels[1] != ai.ThinkingLow || !slices.Equal(def.Input, []string{"text"}) || def.Route == nil {
			t.Fatalf("definition = %+v", def)
		}
		if p.ExtensionPath != rig.exts["lispapi-vm"].Path {
			t.Fatalf("extension path = %q, want %q", p.ExtensionPath, rig.exts["lispapi-vm"].Path)
		}
		route, err := def.Route(t.Context(), extension.ModelRouteRequest{
			Model: &ai.Model{ID: "auto"}, ThinkingLevel: ai.ThinkingLow, Reason: extension.ModelRouteReasonUser,
			State:    json.RawMessage(`{"n":4}`),
			Messages: []ai.Message{ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: "hi"}}}, ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: "again"}}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if route.Model == nil || route.Model.ProviderMeta.ProviderID != "anthropic" || route.Model.ID != "lisp-picked-user" || route.ThinkingLevel != ai.ThinkingLow || string(route.State) != `{"n":9,"saw":2,"cancelled":false}` {
			t.Fatalf("route = model %v thinking %q state %s", route.Model, route.ThinkingLevel, route.State)
		}
		// model-runtime.ts:1000-1009: a router that names a model the registry does not have still returns it as answered.
		unknown, err := def.Route(t.Context(), extension.ModelRouteRequest{Model: &ai.Model{ID: "auto"}, Reason: extension.ModelRouteReasonRetry})
		if err != nil || unknown.Model == nil || unknown.Model.ProviderMeta.ProviderID != "anthropic" || unknown.Model.ID != "lisp-picked-retry" {
			t.Fatalf("unknown physical model route = %+v, %v", unknown, err)
		}

		rig.command("lispapi-vm", "add", "")
		if got := pending(); len(got) != 2 || got["lisprouter/late"].Definition.Name != "Late" {
			t.Fatalf("after add: %v", got)
		}
		rig.command("lispapi-vm", "drop", "")
		if got := pending(); len(got) != 1 || got["lisprouter/late"].Definition.Name != "Late" {
			t.Fatalf("after drop: %v", got)
		}
	})
}

var lispProviderConfigFixture = lispAPIFixture("lispapi-provider", "", `
    (pig:register-provider e "lispapi-multi" (pig:from-json "{\"baseUrl\":\"https://api.example/v1\",\"models\":[{\"id\":\"chat-1\",\"name\":\"Chat\",\"api\":\"openai-completions\",\"reasoning\":false,\"input\":[\"text\"],\"cost\":{\"input\":0,\"output\":0,\"cacheRead\":0,\"cacheWrite\":0},\"contextWindow\":1000,\"maxTokens\":100},{\"type\":\"image\",\"id\":\"img-1\",\"name\":\"Image\",\"api\":\"openai-images\",\"output\":[\"image\",\"text\"],\"input\":[\"text\"]},{\"type\":\"classifier\",\"id\":\"cls-1\",\"name\":\"Classifier\",\"api\":\"llama-cpp-classify\",\"input\":[\"text\"],\"contextWindow\":512}]}"))
`)

// A provider config the extension registers carries chat, image and classifier model entries to the host with their types, APIs and limits.
func TestLispSDKProviderConfigWithImageAndClassifierModels(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newLispAPIRig(t, isolation, nil, lispProviderConfigFixture)
		var config *extension.ProviderConfig
		for _, p := range rig.host.Runtime().PendingProviderRegistrations() {
			if p.Name == "lispapi-multi" {
				config = &p.Config
			}
		}
		if config == nil || len(config.Models) != 3 {
			t.Fatalf("provider registration = %+v", config)
		}
		chat, image, classifier := config.Models[0], config.Models[1], config.Models[2]
		if chat.ID != "chat-1" || (chat.Type != "" && chat.Type != ai.ModelTypeChat) || chat.ContextWindow != 1000 {
			t.Fatalf("chat entry = %+v", chat)
		}
		if image.ID != "img-1" || image.Type != ai.ModelTypeImage || string(image.API) != "openai-images" || !slices.Equal(image.Output, []string{"image", "text"}) {
			t.Fatalf("image entry = %+v", image)
		}
		if classifier.ID != "cls-1" || classifier.Type != ai.ModelTypeClassifier || string(classifier.API) != "llama-cpp-classify" || classifier.ContextWindow != 512 {
			t.Fatalf("classifier entry = %+v", classifier)
		}
	})
}

// oauth.cb.onAuth, onDeviceCode, onProgress, onPrompt, onSelect and onManualCodeInput: a login flow of the extension asks the host's login UI for each interaction and
// carries the answers into the credentials it returns, which no SDK can produce without making the calls.
func TestLispOAuthLoginCallbacksReachTheHost(t *testing.T) {
	h := makeSubprocessLispHarness(t)
	t.Cleanup(func() { h.host.Shutdown("test done") })
	provider, ok := ai.GetOAuthProvider("conformance-oauth-callbacks")
	if !ok {
		t.Fatal("conformance-oauth-callbacks provider not registered by the fixture")
	}
	var seen []string
	callbacks := ai.OAuthLoginCallbacks{
		OnAuth: func(info ai.OAuthAuthInfo) { seen = append(seen, "auth "+info.URL+" "+info.Instructions) },
		OnDeviceCode: func(info ai.OAuthDeviceCodeInfo) {
			seen = append(seen, fmt.Sprintf("device %s %s browser=%v copy=%v interval=%v expires=%v", info.UserCode, info.VerificationURI, *info.OpenBrowserOnConfirm, *info.CopyCodeOnConfirm, info.IntervalSeconds, info.ExpiresInSeconds))
		},
		OnProgress: func(message string) { seen = append(seen, "progress "+message) },
		OnPrompt: func(prompt ai.OAuthPrompt) (string, error) {
			seen = append(seen, fmt.Sprintf("prompt %s %s %v", prompt.Message, prompt.Placeholder, prompt.AllowEmpty))
			return "typed", nil
		},
		OnSelect: func(prompt ai.OAuthSelectPrompt) (string, error) {
			seen = append(seen, fmt.Sprintf("select %s %v", prompt.Message, prompt.Options))
			return "blue", nil
		},
		OnManualCodeInput: func() (string, error) {
			seen = append(seen, "manual")
			return "pasted", nil
		},
	}
	credentials, err := provider.Login(callbacks)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"auth https://auth.example/start Open the link",
		"device ABCD https://auth.example/device browser=false copy=true interval=5 expires=600",
		"progress Waiting",
		"prompt Account: acme true",
		"select Team: [{red Red team} {blue Blue team}]",
		"manual",
	}
	if !slices.Equal(seen, want) {
		t.Fatalf("interactions = %q, want %q", seen, want)
	}
	if credentials.Access != "typed/blue/pasted" {
		t.Fatalf("credentials.Access = %q", credentials.Access)
	}
	cancelled := callbacks
	cancelled.OnSelect = func(ai.OAuthSelectPrompt) (string, error) { return "", errors.New("Login cancelled") }
	if _, err := provider.Login(cancelled); err == nil || err.Error() != "Login cancelled" {
		t.Fatalf("a login whose select the user cancelled failed with %v, want Login cancelled", err)
	}
}

// The provider value types the Lisp SDK offers (provider objects, auth methods, stream and operation options, render contexts) hold what they were built with.
func TestLispProviderValueTypes(t *testing.T) {
	h := makeSubprocessLispHarness(t)
	t.Cleanup(func() { h.host.Shutdown("test done") })
	command, ok := findCommand(h.runner, "provider_surface")
	if !ok {
		t.Fatal("provider_surface command not found")
	}
	if err := command.Handler(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	want := "surface|" + strings.Repeat("T|", 20) + "env:X|T|T|T|Key name|OAuth name|T|Sign in|device-7|agent-7|https://provider|NIL|NIL|NIL|NIL|NIL|2|T|1|2|T|T|self|T|call-1|/cwd|T|T|T|T|T|3|1|invalidated|Login cancelled"
	waitFor(t, func() bool {
		for _, line := range h.ui.Recorded() {
			if strings.TrimSuffix(strings.TrimSuffix(line, ":info"), ":") == want {
				return true
			}
		}
		return false
	})
}

var lispVirtualModelOwnerFixture = lispAPIFixture("lispapi-vm-owner", `
(defun route (ctx request)
  (declare (ignore ctx request))
  (pig:obj "model" (pig:obj "provider" "anthropic" "id" "x") "thinkingLevel" "off"))
`, `
    (pig:register-virtual-model e (pig:make-virtual-model :provider "lisprouter" :id "victim" :name "Victim" :route #'route))
    (pig:register-virtual-model e (pig:make-virtual-model :provider "lisprouter" :id "kept" :name "Kept" :route #'route))
`)

var lispVirtualModelRemoverFixture = lispAPIFixture("lispapi-vm-remover", "", `
    (pig:unregister-virtual-model e "lisprouter" "victim")
`)

// pi.unregisterVirtualModel (loader.ts:511-514) reaches loader.ts:229-233: before the runner binds, unregisterVirtualModel filters the runtime-wide pending list, so a virtual model an earlier-loaded extension queued is removed too.
func TestLispSDKUnregisterVirtualModelBeforeBindFiltersTheRuntimeWidePendingList(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newLispAPIRig(t, isolation, nil, lispVirtualModelOwnerFixture, lispVirtualModelRemoverFixture)
		var pending []string
		for _, p := range rig.host.Runtime().PendingVirtualModelRegistrations() {
			pending = append(pending, p.Definition.Provider+"/"+p.Definition.ID)
		}
		if want := []string{"lisprouter/kept"}; !slices.Equal(pending, want) {
			t.Fatalf("unregisterVirtualModel: pending virtual models %v, want %v", pending, want)
		}
	})
}

var lispVirtualStateIdentityFixture = lispAPIFixture("lispapi-identity", `
(defun route (ctx request)
  (declare (ignore ctx))
  (pig:obj "model" (pig:obj "provider" "anthropic" "id" "lisp-picked-user") "thinkingLevel" "off" "state" (pig:jget request "state")))
`, `
    (pig:register-virtual-model e (pig:make-virtual-model :provider "lisprouter" :id "identity" :name "Identity" :route #'route))
`)

// agent-session.ts:788: a router that returns the state it was given keeps it. The SDK encodes the returned value with its own JSON bytes, which differ from the bytes the Host sent, so the Host compares the JSON values.
func TestLispSDKVirtualModelRouterReturningItsRequestStateKeepsTheRequestStateBytes(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newLispAPIRig(t, isolation, &subprocess.HostCallbacks{}, lispVirtualStateIdentityFixture)
		def := rig.host.Runtime().PendingVirtualModelRegistrations()[0].Definition
		for _, state := range []string{`{"a":"\u003cb\u003e\u0026","n":1}`, `{"z":1,"a":[1,2,{"k":null}]}`, `"\u003c"`, `7`} {
			route, err := def.Route(t.Context(), extension.ModelRouteRequest{Model: &ai.Model{ID: "identity"}, Reason: extension.ModelRouteReasonContinuation, State: json.RawMessage(state)})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(route.State, json.RawMessage(state)) {
				t.Fatalf("a router that returned its request state %s produced %s, which the session would store as a new state", state, route.State)
			}
		}
	})
}
