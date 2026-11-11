package host

// pi: packages/chord/test/next/examples/07-failures.ts

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// --- 07-failures.ts ------------------------------------------------------------------------------------------------

func TestExample07Failures(t *testing.T) {
	remote := exLocal("example.remote", contracts.Params{}, contracts.Mem("status", exStr()))
	inbox := exLocal("example.inbox", contracts.Params{exProp("user", exStr())}, contracts.Mem("status", exStr()))
	mirror := exLocal("example.mirror", contracts.Params{exProp("id", exInt())}, contracts.Mem("status", exStr()))
	stuck := exLocal("example.stuck", contracts.Params{exProp("id", exInt())}, contracts.Mem("status", exStr()))
	var attempts int
	var reported exCollector
	errs := &hostErrors{}
	readyTimeout := 500 * time.Millisecond
	host := newHost(t, HostOptions{ReadyTimeout: &readyTimeout, OnError: func(err HostError) {
		if err.Kind == ErrActivationFailed {
			reported.add(fmt.Sprintf("%s, %s: %s", err.Object, err.Failure.Kind, err.Failure.Error.Message))
			return
		}
		// Pi's onError prints activation_failed only; an attempt parked longer than readyTimeout on any blocker is
		// reported too now (`parked_long`, audit A11): a diagnostic nothing prints.
		if err.Kind == ErrParkedLong {
			return
		}
		errs.push(err)
	}})
	mustLoad(t)(host.Load([]HostFacet{{Name: "@example/failures", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(remote, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			attempts++
			attempt := attempts
			self.Open(func(context.Context) (any, error) {
				// A throwing open is transient: Chord retries it after 1 s, then 2 s, 4 s, ... up to 60 s.
				if attempt <= 2 {
					return nil, errors.New("connection refused")
				}
				return true, nil
			}, func(any, NativeCloseReason, context.Context) error { return nil })
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"status": exConstant("connected")}, nil), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(inbox, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			user := exParam(params, "user")
			// A dependency that fails parks this object (`failed { waiting }`); it goes on once the dependency is live.
			remoteRef := self.Get(remote, jsonObject())
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"status": self.Derive([]any{remoteRef.Member("status")}, func(values []any) (delta.JsonValue, error) {
					return fmt.Sprintf("%s: %v", user, values[0]), nil
				}),
			}, nil), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		if err := h.Serve(mirror, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) {
				// broken(error) says retrying is pointless: no retry, and getters are rejected at once.
				return nil, data.Broken(errors.New("no mirror configured"))
			}, func(any, NativeCloseReason, context.Context) error { return nil })
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"status": exConstant("mirroring")}, nil), nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(stuck, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(ctx context.Context) (any, error) { <-ctx.Done(); return nil, context.Cause(ctx) }, func(any, NativeCloseReason, context.Context) error { return nil })
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{"status": exConstant("never")}, nil), nil
		}, ServeOptions{})
	})}}, background))
	// The mirror: a broken open rejects the get at once.
	_, err := host.Get(background, mirror, jsonObject("id", float64(1)), GetOptions{})
	if err == nil || !strings.Contains(err.Error(), "no mirror configured") {
		t.Fatalf("mirror: %v", err)
	}
	// The stuck native never opens: the attempt times out at readyTimeout; the get (700 ms) rejects with the cause.
	stuckTimeout := 700 * time.Millisecond
	_, err = host.Get(background, stuck, jsonObject("id", float64(1)), GetOptions{ReadyTimeout: &stuckTimeout})
	if err == nil {
		t.Fatal("stuck: ok")
	}
	// The inbox waits through the remote's transient failures (1 s, then 2 s) and goes live: a getter's deadline of 10 s.
	inboxTimeout := 10 * time.Second
	reference, err := host.Get(background, inbox, jsonObject("user", "mario"), GetOptions{ReadyTimeout: &inboxTimeout})
	must(t, err)
	exEqual(t, "inbox status", exValue(t, reference, "status"), "mario: connected")
	if attempts != 3 {
		t.Fatalf("remote attempts = %d, want 3", attempts)
	}
	got := reported.all()
	for _, want := range []string{
		"example.mirror(1), broken: example.mirror(1): no mirror configured",
		"example.remote, transient: example.remote: connection refused",
	} {
		if !slices_Contains(got, want) {
			t.Fatalf("reported %v lacks %q", got, want)
		}
	}
	exCloseHost(t, host)
	if errs.Len() > 0 {
		t.Fatalf("background errors: %v", errs.All())
	}
}

func slices_Contains(list []string, item string) bool {
	return slices.Contains(list, item)
}

// exNotesVariants is 08-remote.ts's variants: an editor with `add`, a reader without.
func exTwoPartyHosts(t *testing.T, server, client Host, principal data.Principal) *link.MemoryLink {
	t.Helper()
	up, down := link.MemoryLinkPair()
	must(t, server.Accept(up, LinkOptions{Principal: principal}, background))
	must(t, client.Connect(down, LinkOptions{Principal: serverPrincipal()}, background))
	return up
}
