package wiring

import (
	"fmt"
	"go/ast"
	"go/types"
	"maps"
	"slices"
	"strings"
	"testing"
)

// ledger collects one check's violations and compares them with the reviewed
// lists: expectedRed (open #201 defects) and an exemption table whose entries
// each name why the mismatch is not a defect. Both lists rot loudly: an entry
// whose violation no longer exists fails, so a fix deletes its entry.
type ledger struct {
	t      *testing.T
	check  string
	found  map[string]string
	exempt map[string]string
}

func newLedger(t *testing.T, check string, exempt map[string]string) *ledger {
	return &ledger{t: t, check: check, found: map[string]string{}, exempt: exempt}
}

func (l *ledger) add(subject, detail string) {
	l.found[l.check+" "+subject] = detail
}

func (l *ledger) finish() {
	t := l.t
	t.Helper()
	for _, id := range slices.Sorted(maps.Keys(l.found)) {
		detail := l.found[id]
		subject := strings.TrimPrefix(id, l.check+" ")
		switch {
		case expectedRed[id] != "":
			t.Logf("expected red (%s): %s", expectedRed[id], detail)
		case routedFindings[id] != "":
			t.Logf("routed finding (%s): %s", routedFindings[id], detail)
		case l.exempt[subject] != "":
			t.Logf("exempt (%s): %s", l.exempt[subject], detail)
		default:
			t.Errorf("%s", detail)
		}
	}
	for id, reason := range expectedRed {
		if strings.HasPrefix(id, l.check+" ") && l.found[id] == "" {
			t.Errorf("expectedRed[%q] (%s) no longer fails: delete the entry", id, reason)
		}
	}
	for id, reason := range routedFindings {
		if strings.HasPrefix(id, l.check+" ") && l.found[id] == "" {
			t.Errorf("routedFindings[%q] (%s) no longer fails: delete the entry", id, reason)
		}
	}
	for subject, reason := range l.exempt {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("exemption %q has no reason", subject)
		}
		if l.found[l.check+" "+subject] == "" {
			t.Errorf("exemption %q (%s) matches no violation: delete it", subject, reason)
		}
	}
}

// expectedRed lists known violations open on this branch; an entry whose
// violation no longer exists fails the check that owns it. It is empty since
// public issue #201 was fixed.
// routedFindings lists the violations outside #201 that these checks found and
// that need a change larger than this lane makes. Each is reported to the lead
// in handoffs/wiring-checks.md; the entry fails once its violation is fixed.
var routedFindings = map[string]string{
	"provider-callback-handled transformHeaders sent by go": "F4: Pi strips transformHeaders before Provider.stream (model-runtime.ts:671), so the SDKs' transformHeaders callback is unreachable",
}

func init() {
}

var expectedRed = map[string]string{}

type sdkSet struct {
	name string
	set  methodSet
}

// callSenders returns every SDK's sent call methods, Go first.
func callSenders(t *testing.T) []sdkSet {
	t.Helper()
	sdk := goPkg(t, "extensions/sdk")
	out := []sdkSet{{"go", sdk.sentMethods(sdk.envelopeFieldType(t, "call"))}}
	for _, text := range sdkSources(t) {
		out = append(out, sdkSet{text.name, text.sentMethods()})
	}
	for _, s := range out {
		if len(s.set) == 0 {
			t.Fatalf("%s SDK: derived no call methods; update this check's extraction", s.name)
		}
	}
	return out
}

// TestSDKCallsHaveHostHandlers: every method an SDK can send in a call frame is
// one the host dispatches on.
func TestSDKCallsHaveHostHandlers(t *testing.T) {
	host := goPkg(t, "coding/extension/host/subprocess")
	handled, _ := host.handledMethods(host.envelopeFieldType(t, "call"))
	l := newLedger(t, "call-handled", nil)
	for _, sdk := range callSenders(t) {
		for _, method := range sdk.set.sorted() {
			if !handled.has(method) {
				l.add(method+" sent by "+sdk.name, fmt.Sprintf("the %s SDK sends call %q (%s), which no host dispatcher handles", sdk.name, method, sdk.set[method]))
			}
		}
	}
	l.finish()
}

// hostOnlyCalls are host call handlers no SDK sends, each with the mechanism
// that makes it unreachable or reaches it another way.
var hostOnlyCalls = map[string]string{
	"ui.getEditorComponent": "the editor component is a host-side factory that cannot cross the process boundary",
}

// TestHostCallHandlersAreReachable: every call the host dispatches on is one
// some SDK can send. A handler no SDK reaches is either dead or a capability
// every SDK lost.
func TestHostCallHandlersAreReachable(t *testing.T) {
	host := goPkg(t, "coding/extension/host/subprocess")
	handled, _ := host.handledMethods(host.envelopeFieldType(t, "call"))
	senders := callSenders(t)
	l := newLedger(t, "call-reachable", hostOnlyCalls)
	for _, method := range handled.sorted() {
		if !slices.ContainsFunc(senders, func(s sdkSet) bool { return s.set.has(method) }) {
			l.add(method, fmt.Sprintf("the host dispatches call %q (%s), which no SDK sends", method, handled[method]))
		}
	}
	l.finish()
}

// undeliveredHostFrames are request and notify methods the host sends that an
// SDK does not handle, each with why the host never sends it to that SDK or why the SDK needs no handler.
var undeliveredHostFrames = map[string]string{}

func init() {
	// The cross-extension object realm exists only between Node runtimes: the
	// host routes xref operations and event-bus lockstep and migration only to
	// a member whose nodeRealm is set (event_bus.go migrateEventBus, xref.go).
	for _, sdk := range []string{"go", "py", "rust"} {
		for _, frame := range []string{"request xref.op", "notify xref.unpin", "request events.migrate", "notify events.proceed"} {
			undeliveredHostFrames[frame+" by "+sdk] = "Node realm only: the host sends it only to a member whose nodeRealm is set"
		}
		// FacetBridge starts its driver as a Node member (facet_bridge.go NewFacetBridge, facet-bridge.mjs).
		undeliveredHostFrames["request facet by "+sdk] = "facet drivers run only in the Node runtime"
		undeliveredHostFrames["request facet_sync by "+sdk] = "facet drivers run only in the Node runtime"
		// The host answers ui.editor.submit with ui.editor.submitted; only the Node editor component sends ui.editor.submit.
		undeliveredHostFrames["notify ui.editor.submitted by "+sdk] = "a reply to ui.editor.submit, which only the Node editor component sends"
	}
	for _, sdk := range []string{"go", "py", "rust"} {
		// The host sends both to every connected generation, but they answer a Node-only wait (protocol.go NotifyReloadStarted: "the Go, Rust and
		// Python SDKs, which have no such wait, ignore it"; NotifyPayload: runtime_input_end removes stdin as a Node keepalive and releases the
		// request_state "suspended" that only the Node runtime reports).
		undeliveredHostFrames["notify reload_started by "+sdk] = "ends a Node runtime's in-place wait for the host's next step; a native SDK has no such wait"
		undeliveredHostFrames["notify runtime_input_end by "+sdk] = "removes stdin as a Node keepalive and releases a Node-only suspended command window; a native SDK has neither"
		// admitSurface reports a retired surface only when it has an id, which only the Node runtime assigns.
		undeliveredHostFrames["notify ui.surface_retired by "+sdk] = "sent only for a surface with an id, which only the Node runtime assigns"
	}
	for _, request := range []string{"oauth_credential_status", "oauth_store_credentials", "oauth_delete_credentials"} {
		// oauth_bridge.go wraps a store proxy only for has_credential_store, which the Node runtime never declares.
		undeliveredHostFrames["request "+request+" by node"] = "sent only to an OAuth registration with has_credential_store, which the Node runtime never declares"
	}
	// view_wire.go: a Node frame carries the lines Pi's components rendered, so the host routes every key to ui.custom.input.
	undeliveredHostFrames["notify ui.view.event by node"] = "a Node view frame is rendered lines; keys go to ui.custom.input (TestEverySDKReceivesEveryHostNotifyCapability)"
}

// TestHostFramesHaveSDKHandlers: every request and notify the host sends is
// handled by every SDK. The Go SDK's dispatcher is read from its code; the
// other SDKs must at least name the method.
func TestHostFramesHaveSDKHandlers(t *testing.T) {
	host := goPkg(t, "coding/extension/host/subprocess")
	sdk := goPkg(t, "extensions/sdk")
	texts := sdkSources(t)
	l := newLedger(t, "host-frame-handled", undeliveredHostFrames)
	for _, frame := range []string{"request", "notify"} {
		sent := host.sentMethods(host.envelopeFieldType(t, frame))
		goHandled, _ := sdk.handledMethods(sdk.envelopeFieldType(t, frame))
		for _, method := range sent.sorted() {
			if !goHandled.has(method) {
				l.add(frame+" "+method+" by go", fmt.Sprintf("the host sends %s %q (%s), which the Go SDK does not dispatch on", frame, method, sent[method]))
			}
			for _, text := range texts {
				if !text.contains(method) {
					l.add(frame+" "+method+" by "+text.name, fmt.Sprintf("the host sends %s %q (%s), which the %s SDK never names", frame, method, sent[method], text.name))
				}
			}
		}
	}
	l.finish()
}

// TestSDKFramesAreSentOrHandled: the Go SDK dispatches on no request or notify
// the host never sends, and every notify it sends is one the host handles.
func TestSDKFramesAreSentOrHandled(t *testing.T) {
	host := goPkg(t, "coding/extension/host/subprocess")
	sdk := goPkg(t, "extensions/sdk")
	l := newLedger(t, "sdk-frame", nil)
	for _, frame := range []string{"request", "notify"} {
		sent := host.sentMethods(host.envelopeFieldType(t, frame))
		goHandled, _ := sdk.handledMethods(sdk.envelopeFieldType(t, frame))
		for _, method := range goHandled.sorted() {
			if !sent.has(method) {
				l.add(frame+" "+method+" dispatched by go", fmt.Sprintf("the Go SDK dispatches on %s %q (%s), which the host never sends", frame, method, goHandled[method]))
			}
		}
	}
	hostHandled, _ := host.handledMethods(host.envelopeFieldType(t, "notify"))
	goSent := sdk.sentMethods(sdk.envelopeFieldType(t, "notify"))
	for _, method := range goSent.sorted() {
		if !hostHandled.has(method) {
			l.add("notify "+method+" sent by go", fmt.Sprintf("the Go SDK sends notify %q (%s), which no host notify handler dispatches on", method, goSent[method]))
		}
	}
	l.finish()
}

// TestTextSDKNotifiesHaveHostHandlers: every notify the Node runtime, the
// Python SDK and the Rust SDK send is one the host dispatches on. A notify has
// no reply, so a method the host does not handle is dropped without a trace,
// as the editor components' ui.notify was.
func TestTextSDKNotifiesHaveHostHandlers(t *testing.T) {
	host := goPkg(t, "coding/extension/host/subprocess")
	hostHandled, _ := host.handledMethods(host.envelopeFieldType(t, "notify"))
	l := newLedger(t, "text-notify-handled", nil)
	for _, text := range sdkSources(t) {
		sent := text.sentNotifies()
		if len(sent) == 0 {
			t.Fatalf("%s SDK: derived no notify methods; update this check's extraction", text.name)
		}
		for _, method := range sent.sorted() {
			if !hostHandled.has(method) {
				l.add(method+" sent by "+text.name, fmt.Sprintf("the %s SDK sends notify %q (%s), which no host notify handler dispatches on", text.name, method, sent[method]))
			}
		}
	}
	l.finish()
}

// hostObjectMethods derives the provider object methods the host invokes: the
// constants that reach the "method" entry of the arguments of the native
// provider proxy's request.
func hostObjectMethods(t *testing.T) methodSet {
	t.Helper()
	host := goPkg(t, "coding/extension/host/subprocess")
	proxy := host.named(t, "nativeProviderProxy")
	var request *types.Func
	for i := range proxy.NumMethods() {
		if proxy.Method(i).Name() == "call" {
			request = proxy.Method(i)
		}
	}
	if request == nil {
		t.Fatal("nativeProviderProxy has no call method; update this check's anchor")
	}
	invoked, _ := host.constantsReaching(func(_ *ast.FuncDecl, _ *types.Func, node ast.Node) []ast.Expr {
		call, ok := node.(*ast.CallExpr)
		if !ok || host.callee(call) != request {
			return nil
		}
		var values []ast.Expr
		for _, arg := range call.Args {
			values = append(values, host.mapMethodValues(arg)...)
		}
		return values
	})
	// A local args map built before the request carries its method the same way.
	streamed, _ := host.constantsReaching(func(fn *ast.FuncDecl, _ *types.Func, node ast.Node) []ast.Expr {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || !callsFunc(host, fn, request) {
			return nil
		}
		var values []ast.Expr
		for _, rhs := range assign.Rhs {
			values = append(values, host.mapMethodValues(rhs)...)
		}
		return values
	})
	for method, at := range streamed {
		invoked.add(method, at)
	}
	if len(invoked) == 0 {
		t.Fatal("derived no host-invoked provider object methods")
	}
	return invoked
}

func callsFunc(p *goPackage, fn *ast.FuncDecl, target *types.Func) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok && p.callee(call) == target {
			found = true
		}
		return !found
	})
	return found
}

// declaredObjectMethods returns every SDK's provider declaration methods, Go first.
func declaredObjectMethods(t *testing.T) []sdkSet {
	t.Helper()
	sdk := goPkg(t, "extensions/sdk")
	goDeclared := sdk.sliceFieldConstants(sdk.jsonStringSliceField(t, "methods"))
	if len(goDeclared) == 0 {
		t.Fatal("go SDK: derived no provider declaration methods")
	}
	out := []sdkSet{{"go", goDeclared}}
	for _, text := range sdkSources(t) {
		out = append(out, sdkSet{text.name, text.declaredObjectMethods(t)})
	}
	return out
}

// TestDeclaredProviderObjectMethodsAreInvoked: a provider object method an SDK
// declares is one the host calls. A declared member the host never calls is
// silently replaced by host behavior, as #201's auth.apiKey.login was by the
// generic API-key prompt.
func TestDeclaredProviderObjectMethodsAreInvoked(t *testing.T) {
	invoked := hostObjectMethods(t)
	l := newLedger(t, "object-method-invoked", nil)
	for _, sdk := range declaredObjectMethods(t) {
		for _, method := range sdk.set.sorted() {
			if !invoked.has(method) {
				l.add(method+" declared by "+sdk.name, fmt.Sprintf("the %s SDK declares provider object method %q (%s), which the host never invokes", sdk.name, method, sdk.set[method]))
			}
		}
	}
	l.finish()
}

// TestInvokedProviderObjectMethodsAreAnswerable: a provider object method the
// host invokes is one some SDK declares, or a protocol method every SDK
// answers without declaring it (the publication update).
func TestInvokedProviderObjectMethodsAreAnswerable(t *testing.T) {
	invoked := hostObjectMethods(t)
	declared := declaredObjectMethods(t)
	texts := sdkSources(t)
	goSDK := goPkg(t, "extensions/sdk")
	l := newLedger(t, "object-method-answerable", nil)
	for _, method := range invoked.sorted() {
		if slices.ContainsFunc(declared, func(s sdkSet) bool { return s.set.has(method) }) {
			continue
		}
		var missing []string
		if !goNames(goSDK, method) {
			missing = append(missing, "go")
		}
		for _, text := range texts {
			if !text.contains(method) {
				missing = append(missing, text.name)
			}
		}
		if len(missing) > 0 {
			l.add(method, fmt.Sprintf("the host invokes provider object method %q (%s), which no SDK declares and %v never answer", method, invoked[method], missing))
		}
	}
	l.finish()
}

// goNames reports whether the Go package compares or switches on the constant.
func goNames(p *goPackage, value string) bool {
	found := false
	p.inspect(func(_ *ast.FuncDecl, _ *types.Func, node ast.Node) {
		if expr, ok := node.(ast.Expr); ok && !found {
			if s, ok := p.constString(expr); ok && s == value {
				found = true
			}
		}
	})
	return found
}

// TestProviderCallbacksAreHandled: every reverse-callback method the Go SDK
// sends inside a call (provider.callback's prompt, notify, env; sessionRead's
// getters) is one the host dispatches on by name. A callback the host answers
// without reading its name is the silent mis-routing #201's select prompt hit.
func TestProviderCallbacksAreHandled(t *testing.T) {
	host := goPkg(t, "coding/extension/host/subprocess")
	agent := goPkg(t, "internal/codingagent")
	sdk := goPkg(t, "extensions/sdk")
	handled := host.methodFieldConstants()
	for method, at := range agent.methodFieldConstants() {
		handled.add(method, at)
	}
	namespaces := sdk.subMethods(sdk.envelopeFieldType(t, "call"))
	if len(namespaces["provider.callback"]) == 0 {
		t.Fatal("derived no provider.callback methods from the Go SDK; update this check's extraction")
	}
	l := newLedger(t, "provider-callback-handled", nil)
	for _, namespace := range slices.Sorted(maps.Keys(namespaces)) {
		for _, method := range namespaces[namespace].sorted() {
			if !handled.has(method) {
				l.add(method+" sent by go", fmt.Sprintf("the Go SDK sends %s method %q (%s), which no host dispatcher names", namespace, method, namespaces[namespace][method]))
			}
		}
	}
	l.finish()
}
