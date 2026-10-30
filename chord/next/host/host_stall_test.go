package host

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// stallAfter is how long a section of these tests waits for its handler before the report: no test of the package has a
// handler whose synchronous part takes that long.
const stallAfter = 2 * time.Second

// TestMain makes the misuse of the handler contract visible in every test of the package: a method handler that blocks on a
// raw channel or a lock before its first host call holds the section that admitted its call (host_ctx.go, "handler
// authoring"), which Pi's `await` never does, and the default build says nothing about it. Here the report fails the test
// binary with the stacks of every goroutine.
func TestMain(m *testing.M) {
	parking.Watch(stallAfter, failOnStall)
	os.Exit(m.Run())
}

// failOnStall is the reporter of the package's tests: the report is the panic, so the binary ends with the text and stacks.
func failOnStall(text string) { panic(text) }

// A handler that waits on a raw channel before its first host call is reported with its stack, and the report does not end
// the wait: the handler goes on once the channel is released. (The package's reporter, failOnStall, fails the binary; this
// test installs a reporter that hands the text over.)
func TestStallABlockOnARawChannelBeforeTheFirstHostCallIsReported(t *testing.T) {
	reports := make(chan string, 4)
	defer parking.Watch(10*time.Millisecond, func(text string) { reports <- text })()
	gate := make(chan struct{})
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(attemptPage, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"label": constant("")},
				Methods: map[string]contracts.MethodFunction{"slow": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					<-gate // a raw receive before any host call: a synchronous block of the section
					return nil, nil
				}},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	page := mustGet(t, host, attemptPage, jsonObject("id", 1.0))
	called := make(chan error, 1)
	go func() {
		_, err := refCall(page, "slow", nil)
		called <- err
	}()
	var text string
	select {
	case text = <-reports:
	case <-time.After(10 * time.Second):
		close(gate)
		t.Fatal("no stall report for a handler that blocks on a raw channel before its first host call")
	}
	if !strings.Contains(text, "held the section") || !strings.Contains(text, "host.TestStallABlockOnARawChannelBeforeTheFirstHostCallIsReported") {
		close(gate)
		t.Fatalf("report = %q, want the stall and the handler's stack", text)
	}
	select {
	case err := <-called:
		close(gate)
		t.Fatalf("the report ended the call: %v", err)
	default:
	}
	close(gate)
	if err := <-called; err != nil {
		t.Fatal(err)
	}
}

// The package's reporter fails the binary with what it is given.
func TestStallTheReporterOfThePackageTestsPanicsWithTheReport(t *testing.T) {
	defer func() {
		if got := recover(); got != "the stacks" {
			t.Fatalf("recovered %v, want the report", got)
		}
	}()
	failOnStall("the stacks")
}
