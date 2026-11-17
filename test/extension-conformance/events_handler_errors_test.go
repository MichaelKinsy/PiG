package extensionconformance

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Pi 18e99051be24 (loader.ts pi.events.on, runner.ts constructor, types.ts ExtensionRuntimeState.reportError): a pi.events handler that throws is an
// extension error like the error of a pi.on handler, with the extension's path, event `events:<channel>`, the failure's message and its stack. The
// bus used to print it with console.error, which draws over the interactive TUI (#11264). The emit does not see it, the channel's other listeners
// still run (event-bus.ts:19-25), and the report is dropped before a runner exists and once its runner is stale.
//
// Every SDK realm runs the row isolated and in a shared cell, the node realm is the control that runs Pi's own wrapper. The channel and the failure's
// text are the fixture's, which no SDK defaults to.

const busRowChannel = "row:events/boom-ü"

// busErrorsOn returns the pi.events handler errors of channel among errs.
func busErrorsOn(errs []extension.ExtensionError, channel string) []extension.ExtensionError {
	var out []extension.ExtensionError
	for _, e := range errs {
		if e.Event == "events:"+channel {
			out = append(out, e)
		}
	}
	return out
}

// waitForBusErrors waits until the rig has n pi.events errors on channel and returns them.
func waitForBusErrors(t *testing.T, rig *busRig, channel string, n int) []extension.ExtensionError {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if got := busErrorsOn(rig.reported(), channel); len(got) >= n || time.Now().After(deadline) {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A throwing handler reaches the error listener with the extension's path, `events:<channel>`, its message and, where the runtime has one, its stack; the emitter returns normally, the next listener runs and nothing is written to stderr.
func TestConformance_PiEventsHandlerErrorIsAnExtensionError(t *testing.T) {
	eachBusRealmSerial(t, true, func(t *testing.T, language, isolation string) {
		emitters := []string{"node"}
		if language != "node" {
			emitters = append(emitters, language)
		}
		for _, emitter := range emitters {
			t.Run("emitter-"+emitter, func(t *testing.T) {
				stop := captureStderr(t)
				rig := newBusRig(t,
					busSpec(emitter, isolation, "e"),
					busSpec(language, isolation, "failing", busListenerAt{busRowChannel, "fail"}),
					busSpec(language, "isolated", "later", busListenerAt{busRowChannel, "record"}),
				)
				rig.must("e", "emit", busRowChannel+` json {"ok":true}`)
				rig.expect(`later|` + busRowChannel + `|{"ok":true}`)
				got := waitForBusErrors(t, rig, busRowChannel, 1)
				if len(got) != 1 {
					t.Fatalf("errors on %s = %+v, want exactly one", busRowChannel, rig.reported())
				}
				e := got[0]
				if e.ExtensionPath != rig.exts["failing"].Path || e.ExtensionPath == "" {
					t.Errorf("extensionPath = %q, want the failing extension's own %q", e.ExtensionPath, rig.exts["failing"].Path)
				}
				if e.Event != "events:"+busRowChannel || !strings.Contains(e.Error, "listener-failed") {
					t.Errorf("error = %+v, want event events:%s and the handler's message listener-failed", e, busRowChannel)
				}
				if language == "node" && !strings.Contains(e.Stack, "listener-failed") {
					t.Errorf("a thrown Error's stack is carried: stack = %q", e.Stack)
				}
				if out := stop(); out != "" {
					t.Errorf("the Host wrote %q to the terminal, want nothing", out)
				}
			})
		}
	})
}

// Pi's second regression: a handler that fails after its session was replaced reports nothing, and Pi's note that the case also fails with only the stale check removed is this row without the runner's guard. A runner a failed reload left stale (InvalidateForReload: it still delivers events, only its ctx throws) drops the error too, as Pi's reportError looks at staleMessage, which a reload sets right after session_shutdown.
func TestConformance_PiEventsHandlerErrorAfterTheRunnerWentStaleIsDropped(t *testing.T) {
	for _, state := range []string{"retired", "stale"} {
		t.Run(state, func(t *testing.T) {
			eachBusRealmSerial(t, true, func(t *testing.T, language, isolation string) {
				stop := captureStderr(t)
				rig := newBusRig(t,
					busSpec("node", isolation, "e"),
					busSpec(language, isolation, "failing", busListenerAt{busRowChannel, "hang+fail"}),
				)
				done := make(chan error, 1)
				go func() { done <- rig.run("e", "emit", busRowChannel+" json 1") }()
				// The handler is running: end the runner's life, then let the handler fail.
				time.Sleep(300 * time.Millisecond)
				switch state {
				case "retired":
					rig.runner.Invalidate("")
				case "stale":
					rig.runner.InvalidateForReload("")
				}
				if err := os.WriteFile(rig.log+".release", nil, 0o600); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("the emit returned %v, want nil: the failure never reaches the emitter", err)
					}
				case <-time.After(30 * time.Second):
					t.Fatal("the emit never returned")
				}
				time.Sleep(300 * time.Millisecond)
				if got := rig.reported(); len(got) != 0 {
					t.Fatalf("errors after the runner was %s = %+v, want none", state, got)
				}
				if out := stop(); out != "" {
					t.Fatalf("the Host wrote %q to the terminal, want nothing", out)
				}
			})
		})
	}
}

// Before a runner exists the report is dropped (loader.ts createExtensionRuntime's `reportError: () => {}`), with nothing written to the terminal; the runner of the rig reports once it exists, before any core is bound (runner.ts:411-414 binds it in the constructor).
func TestConformance_PiEventsHandlerErrorBeforeTheRunnerExistsIsDropped(t *testing.T) {
	eachBusRealmSerial(t, true, func(t *testing.T, language, isolation string) {
		stop := captureStderr(t)
		rig := newBusRig(t,
			busSpec("node", isolation, "e"),
			busSpec(language, isolation, "failing", busListenerAt{busRowChannel, "fail"}),
		)
		rig.host.Runtime().SetRuntimeErrorReporter(nil)
		rig.must("e", "emit", busRowChannel+" json 1")
		time.Sleep(300 * time.Millisecond)
		if got := rig.reported(); len(got) != 0 {
			t.Fatalf("errors with no runner bound = %+v, want none", got)
		}
		if out := stop(); out != "" {
			t.Fatalf("the Host wrote %q to the terminal, want nothing", out)
		}
	})
}

// Several listeners of ONE extension (one connection) on one channel each fail with a message of their own: every failure is reported, in registration
// order, each with its own message (and, where the runtime has one, its own stack), and all of them before the emit returns, so the rig reads them with no
// wait. A node connection reports each through report_error before it answers the next frame; a native SDK's host dispatches per listener, so each
// response carries its own failure. The listener between the failing ones still runs.
func TestConformance_PiEventsEveryFailingListenerOfOneConnectionIsReportedInOrderBeforeTheEmitReturns(t *testing.T) {
	eachBusRealmSerial(t, true, func(t *testing.T, language, isolation string) {
		stop := captureStderr(t)
		rig := newBusRig(t,
			busSpec("node", isolation, "e"),
			busSpec(language, isolation, "many",
				busListenerAt{busRowChannel, "fail=first-ü"},
				busListenerAt{busRowChannel, "record"},
				busListenerAt{busRowChannel, "fail=second"},
				busListenerAt{busRowChannel, "panic"},
				busListenerAt{busRowChannel, "fail=fourth"},
			),
		)
		rig.must("e", "emit", busRowChannel+` json {"n":7}`)
		got := busErrorsOn(rig.reported(), busRowChannel)
		var messages []string
		for _, e := range got {
			messages = append(messages, e.Error)
		}
		want := []string{"first-ü", "second", "listener-panicked", "fourth"}
		if len(got) != len(want) {
			t.Fatalf("errors on %s = %q right after the emit, want %q", busRowChannel, messages, want)
		}
		for i, e := range got {
			if !strings.Contains(e.Error, want[i]) || e.ExtensionPath != rig.exts["many"].Path {
				t.Errorf("error %d = %+v, want a message holding %q from %s", i, e, want[i], rig.exts["many"].Path)
			}
			if language == "node" && want[i] != "listener-panicked" && (!strings.Contains(e.Stack, want[i]) || strings.Count(e.Stack, "\n") < 1) {
				t.Errorf("error %d stack = %q, want its own thrown Error's stack", i, e.Stack)
			}
		}
		rig.expect(`many|` + busRowChannel + `|{"n":7}`)
		if out := stop(); out != "" {
			t.Errorf("the Host wrote %q to the terminal, want nothing", out)
		}
	})
}

// A node handler that returns a rejecting promise is awaited by Pi's wrapper: its failure is reported although the emit already returned, once, with its
// stack, and the listener after it still ran. The native SDKs' handlers are synchronous (a Go handler returns an error, a Python, Rust or Lisp one returns
// or raises), so there is no later failure to carry in them: the row exists for the realm that can author one.
func TestConformance_PiEventsNodeAsyncHandlerRejectionIsReportedAfterTheEmitReturned(t *testing.T) {
	eachBusRealmSerial(t, true, func(t *testing.T, language, isolation string) {
		if language != "node" {
			t.Skip("the native SDKs' event handlers are synchronous: no failure can come after the dispatch was answered")
		}
		stop := captureStderr(t)
		rig := newBusRig(t,
			busSpec("node", isolation, "e"),
			busSpec("node", isolation, "late", busListenerAt{busRowChannel, "afail=rejected-later"}, busListenerAt{busRowChannel, "record"}),
		)
		rig.must("e", "emit", busRowChannel+" json 1")
		rig.expect(`late|` + busRowChannel + `|1`)
		waitForBusErrors(t, rig, busRowChannel, 1)
		time.Sleep(300 * time.Millisecond)
		got := busErrorsOn(rig.reported(), busRowChannel)
		if len(got) != 1 || got[0].Error != "rejected-later" || !strings.Contains(got[0].Stack, "rejected-later") || got[0].ExtensionPath != rig.exts["late"].Path {
			t.Fatalf("errors on %s = %+v, want one {rejected-later with its stack, the extension's path}", busRowChannel, got)
		}
		if out := stop(); out != "" {
			t.Errorf("the Host wrote %q to the terminal, want nothing", out)
		}
	})
}

// A Go extension fused into the Host's own process (a Piglet Binary runs it over an in-memory pipe, the form no isolated or packed row reaches) reports a failing listener
// on the same route as the isolated and packed SDK realms: the Host's dispatch sees the handler's error reply and sends it to the runner's error listeners with the fused
// extension's path; nothing is printed, the emit returns, and the fused extension's next listener still runs. The path and the message are the row's own values.
func TestConformance_PiEventsFusedGoHandlerErrorIsAnExtensionError(t *testing.T) {
	stop := captureStderr(t)
	rig := newBusRig(t, busSpec("node", "isolated", "e"))
	const fusedPath = "builtin:fused-bus-error-ü"
	fused := sdk.New("fused")
	ran := make(chan any, 1)
	if _, err := fused.Events().On(busRowChannel, func(sdk.Context, any) error { return errors.New("fused-listener-failed") }); err != nil {
		t.Fatal(err)
	}
	if _, err := fused.Events().On(busRowChannel, func(_ sdk.Context, data any) error { ran <- data; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: "fused", Enabled: true, Path: fusedPath}, fused.RunWithConn); err != nil {
		t.Fatal(err)
	}
	rig.must("e", "emit", busRowChannel+` json {"ok":true}`)
	select {
	case data := <-ran:
		if got := fmt.Sprint(data); got != "map[ok:true]" {
			t.Fatalf("the listener after the failing one received %s", got)
		}
	default:
		t.Fatal("the listener after the failing one did not run before the emit returned")
	}
	got := waitForBusErrors(t, rig, busRowChannel, 1)
	if len(got) != 1 || got[0].ExtensionPath != fusedPath || got[0].Event != "events:"+busRowChannel || !strings.Contains(got[0].Error, "fused-listener-failed") {
		t.Fatalf("errors on %s = %+v, want one {%s, events:%s, fused-listener-failed}", busRowChannel, rig.reported(), fusedPath, busRowChannel)
	}
	if out := stop(); out != "" {
		t.Errorf("the Host wrote %q to the terminal, want nothing", out)
	}
}
