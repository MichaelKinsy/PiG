package host

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Row 32 (B12). Pi's `#report` reads `target.readinessCause` for a restarted object, and for a retried one whose old attempt is still open, when
// it starts: synchronously after `#commit` returned (host.ts:823, 1047, 1073), that is after `#startFates`, the host-lifetime starts and
// `#unbound.wake(names)` (host.ts:995-1001), with no microtask between. The Go host read it inside `startFates`, before the host-lifetime
// starts and before the wake. The wake re-places the requests waiting for a name this reload serves, a restart's readiness binding
// among them, and a binding that binds there moves at once: the readiness's cause is then another. Pi probe (packages/chord at
// f6600b58f347, a vitest case of the same steps): the second report is `[{ object: "app.pure", kind: "retried" }]`; the first, `parked`
// `waiting for app.missing: app.missing is not served here`. Go-only: Pi has no case that reloads over a parked restart's open old attempt
// with a plugin serving what it waits for.

// reloadReportMissing is `Missing`: a host-lifetime type no plugin serves until a reload adds it.
var reloadReportMissing = mustContract(declareLocal("app.missing", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}}))

// reloadReportNeedsMissing serves `Pure` whose factory needs `Missing`: a restart to it parks (its readiness finds `Missing` not served).
func reloadReportNeedsMissing(h FacetHost) error {
	return h.Serve(reloadPure, func(_ *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
		self.Get(reloadReportMissing, jsonObject())
		return contracts.MembersImplementation{
			State:   map[string]chord.Watchable[delta.JsonValue]{"n": constant(9.0), "label": constant("v2")},
			Methods: map[string]contracts.MethodFunction{"bump": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return 0.0, nil }},
		}, nil
	}, ServeOptions{})
}

// reloadReportParkedOverAnOpenOldAttempt is a host whose `Pure` was restarted to code that needs `Missing` while a call still runs on the old
// attempt (its drain is open): the object is parked, and its readiness holds the request for `Missing`, waiting in the unbound index.
func reloadReportParkedOverAnOpenOldAttempt(t *testing.T) (host Host, pure *Reference, held *reloadGate, bumping *reloadCall) {
	held = newReloadGate()
	closeTimeout := time.Minute
	host, _ = loaded(t, func(h FacetHost, _ FacetSelf) error { return reloadServePure(t, h, "v1", 1, reloadGates(held)) },
		loadedOptions{CloseTimeout: &closeTimeout})
	pure = mustGet(t, host, reloadPure, jsonObject())
	bumping = startReloadCall(pure, "bump", nil)
	held.entered.wait(t)
	first := reloadMust(t)(reloadApp(host, reloadReportNeedsMissing))
	if len(first.Objects) != 1 || first.Objects[0].Object != "app.pure" || first.Objects[0].Kind != ReloadParked ||
		first.Objects[0].Cause.Kind != data.FailureWaiting || !strings.Contains(first.Objects[0].Cause.Error.Message, "app.missing") {
		t.Fatalf("first report objects = %+v, want app.pure parked waiting for app.missing", first.Objects)
	}
	return host, pure, held, bumping
}

func TestReloadReportARetriedObjectWithAnOpenOldAttemptReportsWhatItsReadinessFindsAfterTheCommitsPublicationNotBefore(t *testing.T) {
	host, pure, held, bumping := reloadReportParkedOverAnOpenOldAttempt(t)
	defer closeHosts(t, host)
	defer held.open.fire()
	// The same reload retries the parked object on code that needs nothing and adds the plugin that serves `Missing`: the publication wakes
	// the readiness's request, which binds, so nothing is waited for any more when the report reads it.
	second := reloadMust(t)(host.Reload([]HostFacet{
		{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return reloadServePure(t, h, "v3", 1, nil) })},
		{Name: "@test/missing", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(reloadReportMissing, func(*delta.JsonObject, Self) (contracts.MembersImplementation, error) {
				return contracts.MembersImplementation{}, nil
			}, ServeOptions{})
		})},
	}, background, ReloadOptions{}))
	reloadWantObjects(t, second, ReloadedObject{Object: "app.pure", Kind: ReloadRetried})
	held.open.fire()
	if got := bumping.wait(t); got != 1.0 {
		t.Fatalf("old call = %v, want 1", got)
	}
	untilKind(t, pure, RefStateLive)
	reloadUntilLabel(t, pure, "v3")
}

func TestReloadReportARetriedObjectWithAnOpenOldAttemptStillReportsTheCauseOfItsReadinessWhenNothingServesWhatItWaitsFor(t *testing.T) {
	host, pure, held, bumping := reloadReportParkedOverAnOpenOldAttempt(t)
	defer closeHosts(t, host)
	defer held.open.fire()
	second := reloadMust(t)(reloadApp(host, func(h FacetHost) error { return reloadServePure(t, h, "v3", 1, nil) }))
	if len(second.Objects) != 1 || second.Objects[0].Object != "app.pure" || second.Objects[0].Kind != ReloadParked ||
		second.Objects[0].Cause.Kind != data.FailureWaiting || !strings.Contains(second.Objects[0].Cause.Error.Message, "app.missing") {
		t.Fatalf("second report objects = %+v, want app.pure parked waiting for app.missing", second.Objects)
	}
	held.open.fire()
	if got := bumping.wait(t); got != 1.0 {
		t.Fatalf("old call = %v, want 1", got)
	}
	untilKind(t, pure, RefStateLive)
	reloadUntilLabel(t, pure, "v3")
}
