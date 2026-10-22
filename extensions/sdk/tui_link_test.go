package sdk

import (
	"encoding/json"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// connectedContext is a Context over an in-memory pipe whose far end the test plays the host on.
func connectedContext(t *testing.T, hasUI bool) (Context, *mockHost) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	connection := newConn(client)
	connection.start()
	t.Cleanup(func() { _ = client.Close() })
	return Context{ext: &Extension{conn: connection, hasUI: hasUI}}, &mockHost{nc: server}
}

// Pi's `tui.invalidate()` and `tui.requestRender(force)` (tui.ts:366, :990) reach the host as the two calls, with the force the caller gave.
func TestContextTUISendsInvalidateAndRequestRender(t *testing.T) {
	ctx, host := connectedContext(t, true)
	result := make(chan error, 1)
	go func() {
		tui := ctx.TUI()
		if err := tui.Invalidate(); err != nil {
			result <- err
			return
		}
		if err := tui.RequestRender(false); err != nil {
			result <- err
			return
		}
		result <- tui.RequestRender(true)
	}()
	for _, want := range []struct {
		method string
		args   string
	}{{"ui.tui.invalidate", `{}`}, {"ui.tui.requestRender", `{"force":false}`}, {"ui.tui.requestRender", `{"force":true}`}} {
		call := host.readEnvelope(t)
		if call.Type != msgCall || call.Call == nil || call.Call.Method != want.method || string(call.Call.Args) != want.args {
			t.Fatalf("call = %+v (args %s), want %s %s", call.Call, argsOf(call), want.method, want.args)
		}
		host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{}})
	}
	if err := <-result; err != nil {
		t.Fatalf("TUI calls: %v", err)
	}
}

func argsOf(e envelope) string {
	if e.Call == nil {
		return ""
	}
	return string(e.Call.Args)
}

// With no UI there is no TUI: both calls return at once and send nothing (net.Pipe blocks a write nobody reads).
func TestContextTUIWithNoUIMakesNoHostCall(t *testing.T) {
	ctx, _ := connectedContext(t, false)
	done := make(chan error, 1)
	go func() {
		tui := ctx.TUI()
		if err := tui.Invalidate(); err != nil {
			done <- err
			return
		}
		done <- tui.RequestRender(true)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("TUI calls with no UI: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a TUI call with no UI waited for the host")
	}
}

func TestContextTUISurfacesHostError(t *testing.T) {
	ctx, host := connectedContext(t, true)
	result := make(chan error, 1)
	go func() { result <- ctx.TUI().Invalidate() }()
	call := host.readEnvelope(t)
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{Error: &errorInfo{Code: "ui_error", Message: "no tui"}}})
	if err := <-result; err == nil || err.Error() != "ui_error: no tui" {
		t.Fatalf("Invalidate error = %v, want ui_error: no tui", err)
	}
}

// answerLinkCall reads one host call from the extension and answers it with the result.
func answerLinkCall(t *testing.T, host *mockHost, wantMethod, wantScheme string, result *callResultMsg) {
	t.Helper()
	call := host.readEnvelope(t)
	if call.Type != msgCall || call.Call == nil || call.Call.Method != wantMethod {
		t.Fatalf("call = %+v, want %s", call.Call, wantMethod)
	}
	var args map[string]string
	if err := json.Unmarshal(call.Call.Args, &args); err != nil || !reflect.DeepEqual(args, map[string]string{"scheme": wantScheme}) {
		t.Fatalf("%s args = %s, want the scheme %q", wantMethod, call.Call.Args, wantScheme)
	}
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: result})
}

// D193: the claim is a host call; a click then reaches the handler with its address, in the order the host sent them, and the unsubscribe
// gives the scheme back once.
func TestOnLinkClickClaimsTheSchemeDeliversClicksInOrderAndUnsubscribes(t *testing.T) {
	ctx, host := connectedContext(t, true)
	var mu sync.Mutex
	var got []string
	unsubscribe := make(chan func(), 1)
	failed := make(chan error, 1)
	go func() {
		off, err := ctx.OnLinkClick("probe", func(_ Context, url string) {
			mu.Lock()
			got = append(got, url)
			mu.Unlock()
		})
		failed <- err
		unsubscribe <- off
	}()
	answerLinkCall(t, host, "ui.onLinkClick", "probe", &callResultMsg{})
	if err := <-failed; err != nil {
		t.Fatalf("OnLinkClick: %v", err)
	}
	off := <-unsubscribe

	for _, url := range []string{"probe:1", "probe:2", "probe:3"} {
		ctx.ext.notifyLinkClick("probe", url)
	}
	ctx.ext.notifyLinkClick("other", "other:1") // another scheme: nobody's click
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("clicks delivered = %v, want three", got)
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	if !reflect.DeepEqual(got, []string{"probe:1", "probe:2", "probe:3"}) {
		t.Fatalf("clicks = %v, want them in the order the host sent them", got)
	}
	mu.Unlock()

	done := make(chan struct{})
	go func() { off(); off(); close(done) }()
	answerLinkCall(t, host, "ui.offLinkClick", "probe", &callResultMsg{})
	<-done
	ctx.ext.notifyLinkClick("probe", "probe:4")
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("a click after the unsubscribe reached the handler: %v", got)
	}
}

// The host refuses a reserved scheme or one another extension holds; the SDK then keeps no handler, and an earlier claim of the scheme stays.
func TestOnLinkClickRefusalRestoresTheEarlierClaim(t *testing.T) {
	ctx, host := connectedContext(t, true)
	first := make(chan error, 1)
	calls := make(chan string, 4)
	go func() {
		_, err := ctx.OnLinkClick("probe", func(_ Context, url string) { calls <- "first:" + url })
		first <- err
	}()
	answerLinkCall(t, host, "ui.onLinkClick", "probe", &callResultMsg{})
	if err := <-first; err != nil {
		t.Fatal(err)
	}

	second := make(chan error, 1)
	go func() {
		_, err := ctx.OnLinkClick("probe", func(_ Context, url string) { calls <- "second:" + url })
		second <- err
	}()
	answerLinkCall(t, host, "ui.onLinkClick", "probe", &callResultMsg{Error: &errorInfo{Code: "scheme_in_use", Message: "held"}})
	if err := <-second; err == nil || !strings.Contains(err.Error(), "scheme_in_use") {
		t.Fatalf("a refused claim returned %v", err)
	}
	ctx.ext.notifyLinkClick("probe", "probe:9")
	select {
	case got := <-calls:
		if got != "first:probe:9" {
			t.Fatalf("click went to %q, want the claim the host kept", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the earlier claim was lost when the second was refused")
	}
}

func TestOnLinkClickWithNoUIClaimsNothingAndNilHandlerIsAnError(t *testing.T) {
	ctx, _ := connectedContext(t, false)
	off, err := ctx.OnLinkClick("probe", func(Context, string) {})
	if err != nil {
		t.Fatalf("with no UI: %v", err)
	}
	off()
	if len(ctx.ext.linkClickFuncs) != 0 {
		t.Fatalf("a claim with no UI left a handler: %v", ctx.ext.linkClickFuncs)
	}
	if _, err := ctx.OnLinkClick("probe", nil); err == nil {
		t.Fatal("a nil handler was accepted")
	}
}

// A handler that panics is recovered; the next click still reaches the handler.
func TestOnLinkClickHandlerPanicDoesNotStopTheNextClick(t *testing.T) {
	ctx, host := connectedContext(t, true)
	calls := make(chan string, 2)
	claimed := make(chan error, 1)
	go func() {
		_, err := ctx.OnLinkClick("probe", func(_ Context, url string) {
			if url == "probe:boom" {
				panic("handler exploded")
			}
			calls <- url
		})
		claimed <- err
	}()
	answerLinkCall(t, host, "ui.onLinkClick", "probe", &callResultMsg{})
	if err := <-claimed; err != nil {
		t.Fatal(err)
	}
	out := captureStderr(t, func() {
		ctx.ext.notifyLinkClick("probe", "probe:boom")
		ctx.ext.notifyLinkClick("probe", "probe:after")
		select {
		case got := <-calls:
			if got != "probe:after" {
				t.Errorf("delivered %q", got)
			}
		case <-time.After(5 * time.Second):
			t.Error("the click after the panic never arrived")
		}
	})
	if !strings.Contains(out, "link click handler") || !strings.Contains(out, "handler exploded") {
		t.Fatalf("stderr = %q, want the recovered panic named and its value", out)
	}
}
