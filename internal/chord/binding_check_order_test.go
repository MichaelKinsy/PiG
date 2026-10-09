package chord

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Pi packages/chord/src/services/consumer.ts:457-465 (constructor), 467-518 (use, observe), 520-593 (ready, rebind,
// dispose) and 636-660 (#assertHandleAccess, #assertRemotable, #assertAvailable): the process-local check precedes the
// disposed check, the first use or observe fixes a service's mode, a handle checks access only when it is called, and a
// singleton subscription the provider rejects fails ready but not dispose. Each expected value is what Pi's binding
// reports for the same calls under Node over a loopback transport (oracle /tmp/bind1.mts, with service IDs t.* renamed to bco.* because test service views are package-global).
func TestRemoteServiceBindingChecksMatchPi(t *testing.T) {
	a, k := defineReader("bco.a"), defineReader("bco.k")
	local := DefineService[Reader]("bco.a", ServiceOptions{Local: true})
	other := defineReader("bco.o")
	provider, err := NewRemoteServiceProvider(ServiceProviderDefinition{Id: a.Id()}, ServiceProviderDefinition{Id: k.Id(), Mode: ServiceKeyed})
	if err != nil {
		t.Fatal(err)
	}
	if err := Provide(provider, a, Reader(readerFunc(func(context.Context) (string, error) { return "A", nil }))); err != nil {
		t.Fatal(err)
	}
	transport := NewLoopbackTransport(provider)
	describe := func(err error) string {
		if err == nil {
			return "ok"
		}
		if remote, ok := errors.AsType[*RemoteServiceError](err); ok {
			return string(remote.Code) + " " + remote.Message
		}
		return "- " + err.Error()
	}
	var got []string
	step := func(label string, err error) { got = append(got, label+" "+describe(err)) }
	_, err = CreateRemoteServiceBinding(RemoteServiceBindingOptions{Services: ServiceIDs("bco.a", "bco.k", "bco.a"), Transport: transport})
	step("duplicate ids", err)
	_, err = CreateRemoteServiceBinding(RemoteServiceBindingOptions{Services: ServiceIDs(), Transport: transport})
	step("empty services", err)
	denied := false
	binding, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{Services: ServiceIDs("bco.a", "bco.k"), Transport: transport, AssertAccess: func() error {
		if denied {
			return errors.New("revoked")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	use := func(def ServiceDefinition[Reader]) error { _, err := UseRemote(binding, def); return err }
	observe := func(def ServiceDefinition[Reader]) error {
		_, err := ObserveRemote(binding, def, func(context.Context, *RemoteService) error { return nil })
		return err
	}
	step("use local", use(local))
	step("use other", use(other))
	step("observe other", observe(other))
	step("use a", use(a))
	step("observe a after use", observe(a))
	step("use a", use(a))
	step("use k", use(k))
	step("observe k", observe(k))
	step("use k after observe", use(k))
	step("ready", binding.Ready(t.Context()))
	denied = true
	step("use a denied", use(a))
	service, err := UseRemote(binding, a)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Call(t.Context(), "read")
	step("call a denied", err)
	denied = false
	result, err := service.Call(t.Context(), "read")
	if string(result) != `"A"` {
		t.Errorf("call a returned %s, want \"A\"", result)
	}
	step("call a", err)
	step("dispose", binding.Dispose(t.Context()))
	step("dispose again", binding.Dispose(t.Context()))
	step("use local disposed", use(local))
	step("use other disposed", use(other))
	step("use a disposed", use(a))
	step("observe k disposed", observe(k))
	step("ready disposed", binding.Ready(t.Context()))
	step("rebind disposed", binding.Rebind(t.Context(), true))
	want := []string{
		"duplicate ids - Remote service binding has duplicate service IDs",
		"empty services ok",
		"use local service_not_allowed Service bco.a is process-local",
		"use other service_not_allowed Remote service bco.o is not allowlisted",
		"observe other service_not_allowed Remote service bco.o is not allowlisted",
		"use a ok",
		"observe a after use service_mode_mismatch Remote service bco.a is already used as singleton",
		"use a ok",
		"use k ok",
		"observe k service_mode_mismatch Remote service bco.k is already used as singleton",
		"use k after observe ok",
		"ready service_mode_mismatch Remote service bco.k is keyed, not singleton",
		"use a denied ok",
		"call a denied - revoked",
		"call a ok",
		"dispose ok",
		"dispose again ok",
		"use local disposed service_not_allowed Service bco.a is process-local",
		"use other disposed - Remote service binding is disposed",
		"use a disposed - Remote service binding is disposed",
		"observe k disposed - Remote service binding is disposed",
		"ready disposed - Remote service binding is disposed",
		"rebind disposed - Remote service binding is disposed",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("binding checks:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}
}
