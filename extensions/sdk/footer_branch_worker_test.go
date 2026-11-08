package sdk

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// Every host call waits for the state_update that precedes its result (callHostAfterBegin), and the main loop marks a state_update handled
// only after handleNotify returned. A footer branch callback that ran inside that state_update, on the loop, and made a host call, waited
// for the state_update it ran in: the loop never served another request. The callbacks run on a worker (branchFeed), so a callback
// can make any host call, as Pi's can, and the loop goes on. The SDK's own report of a panicking callback is such a call (ui.notify).

// readFrameWithin reads the next frame the extension sent (skipping request-state frames), failing the test with what when none arrives.
func readFrameWithin(t *testing.T, host *mockHost, what string) envelope {
	t.Helper()
	for {
		if err := host.nc.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var hdr [4]byte
		if _, err := io.ReadFull(host.nc, hdr[:]); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		data := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(host.nc, data); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		var env envelope
		if err := json.Unmarshal(data, &env); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if env.Type != msgRequestState {
			return env
		}
	}
}

func TestFooterBranchCallbackMayMakeHostCalls(t *testing.T) {
	for _, tc := range []struct {
		name     string
		callback func(ext *Extension) func()
		wantCall string
		wantArgs string
	}{
		{"a panicking callback is reported with ui.notify", func(*Extension) func() { return func() { panic("boom") } }, "ui.notify", "footer branch subscriber failed: boom"},
		{"a callback that reads the context usage", func(ext *Extension) func() {
			return func() { _, _ = Context{ext: ext}.GetContextUsage() }
		}, "getContextUsage", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ext := New("footer-branch-worker")
			ext.Command("subscribe", "subscribe", func(ctx Context, _ string) error {
				ctx.FooterData().OnBranchChange(tc.callback(ext))
				return nil
			})
			ext.Command("next", "next", func(Context, string) error { return nil })
			host, _, done := surfaceHost(t, ext, nil)
			defer surfaceShutdown(t, host, done)

			if _, resp := runSurfaceCommand(t, host, "subscribe", func(call *callMsg) *callResultMsg {
				t.Errorf("subscribing sent the host call %s", call.Method)
				return &callResultMsg{Result: json.RawMessage(`null`)}
			}); resp.Error != nil {
				t.Fatalf("subscribe failed: %+v", resp.Error)
			}

			// The branch changes: the callback runs and makes its host call, which the host answers.
			sendState(t, host, `{"footerData":{"gitBranch":"main","extensionStatuses":{},"availableProviderCount":1}}`)
			var call *callMsg
			for call == nil {
				env := readFrameWithin(t, host, "the branch callback's host call never arrived")
				if env.Type != msgCall {
					continue
				}
				call = env.Call
				host.writeEnvelope(t, envelope{Type: msgCallResult, ID: env.ID, CallResult: &callResultMsg{Result: json.RawMessage(`null`)}})
			}
			if call.Method != tc.wantCall || !strings.Contains(string(call.Args), tc.wantArgs) {
				t.Fatalf("the callback sent %s %s, want %s containing %q", call.Method, call.Args, tc.wantCall, tc.wantArgs)
			}

			// The message loop is free: the next command is served.
			host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-next", Request: &requestMsg{Method: "command", Tool: "next"}})
			for {
				env := readFrameWithin(t, host, "the main loop never served the next command")
				if env.Type == msgResponse && env.ID == "req-next" {
					if env.Response.Error != nil {
						t.Fatalf("next command failed: %+v", env.Response.Error)
					}
					return
				}
			}
		})
	}
}

// The callbacks run in the order the branch changed and the callbacks subscribed, one change at a time.
func TestFooterBranchCallbacksRunInOrder(t *testing.T) {
	ext := New("footer-branch-order")
	ran := make(chan string, 8)
	ext.Command("subscribe", "subscribe", func(ctx Context, _ string) error {
		footer := ctx.FooterData()
		footer.OnBranchChange(func() { ran <- "a" })
		footer.OnBranchChange(func() { panic("b fails") })
		footer.OnBranchChange(func() { ran <- "c" })
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	if _, resp := runSurfaceCommand(t, host, "subscribe", func(*callMsg) *callResultMsg { return &callResultMsg{Result: json.RawMessage(`null`)} }); resp.Error != nil {
		t.Fatalf("subscribe failed: %+v", resp.Error)
	}

	for _, branch := range []string{"main", "dev"} {
		sendState(t, host, `{"footerData":{"gitBranch":"`+branch+`","extensionStatuses":{},"availableProviderCount":1}}`)
	}
	// Each change runs a, the failing b (reported with ui.notify, not fatal) and c; the host answers the reports.
	for reports := 0; reports < 2; {
		env := readFrameWithin(t, host, "a failing callback was not reported")
		if env.Type == msgCall && env.Call.Method == "ui.notify" {
			reports++
			host.writeEnvelope(t, envelope{Type: msgCallResult, ID: env.ID, CallResult: &callResultMsg{Result: json.RawMessage(`null`)}})
		}
	}
	var got []string
	for range 4 {
		got = append(got, recv(t, ran))
	}
	if want := "acac"; strings.Join(got, "") != want {
		t.Fatalf("callbacks ran %v, want a c per change in subscription order (%s)", got, want)
	}
}
