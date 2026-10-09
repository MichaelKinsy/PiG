package chord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Pi packages/chord/src/services/provider.ts:214-237 (invoke), 377-409 (#resolveInstance), 239-255 (subscribe),
// 113-170 (provide, withdraw, replace), 183-212 (spawn) and 278-317 (dispose): the errors invoke and subscribe report,
// and the updates a subscription receives, buffered until activate. Each expected value is what Pi's provider reports
// for the same calls under Node (oracle /tmp/prov4.mts, with service IDs t.* renamed to pis.* because test service views are package-global). Pi's provide after a withdraw publishes no update.
func TestProviderInvokeSubscribeAndUpdatesMatchPi(t *testing.T) {
	a, k := defineReader("pis.a"), defineReader("pis.k")
	impl := func(tag string) Reader {
		return readerFunc(func(context.Context) (string, error) { return tag, nil })
	}
	provider, err := NewRemoteServiceProvider(ServiceProviderDefinition{Id: a.Id()}, ServiceProviderDefinition{Id: k.Id(), Mode: ServiceKeyed})
	if err != nil {
		t.Fatal(err)
	}
	describe := func(result json.RawMessage, err error) string {
		if err == nil {
			return "ok " + string(result)
		}
		if remote, ok := errors.AsType[*RemoteServiceError](err); ok {
			return string(remote.Code) + " " + remote.Message
		}
		return "- " + err.Error()
	}
	invoke := func(service, member string, instance *ServiceInstanceAddress) string {
		return describe(provider.Invoke(t.Context(), ServiceCall{ServiceId: service, Member: member, Instance: instance, Args: []json.RawMessage{}}))
	}
	subscribe := func(service string, mode ServiceMode) string {
		_, err := provider.Subscribe(service, mode, func(context.Context, ServiceProviderUpdate) {})
		return describe(nil, err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	at := func(key string, generation int) *ServiceInstanceAddress {
		return &ServiceInstanceAddress{Key: key, Generation: generation}
	}
	var got []string
	step := func(label, outcome string) { got = append(got, label+" "+outcome) }
	step("invoke notallowed", invoke("pis.o", "read", nil))
	step("invoke singleton none", invoke("pis.a", "read", nil))
	step("invoke singleton addr", invoke("pis.a", "read", at("x", 1)))
	step("invoke keyed noaddr", invoke("pis.k", "read", nil))
	step("invoke keyed missing", invoke("pis.k", "read", at("x", 1)))
	must(Provide(provider, a, impl("A")))
	step("invoke singleton", invoke("pis.a", "read", nil))
	step("invoke singleton nomember", invoke("pis.a", "nope", nil))
	closeFirst, err := Spawn(provider, k, "x", impl("X1"))
	must(err)
	must(closeFirst())
	_, err = Spawn(provider, k, "x", impl("X2"))
	must(err)
	step("invoke keyed stale", invoke("pis.k", "read", at("x", 1)))
	step("invoke keyed", invoke("pis.k", "read", at("x", 2)))
	step("invoke keyed nomember", invoke("pis.k", "nope", at("x", 2)))
	step("subscribe notallowed", subscribe("pis.o", ServiceSingleton))
	step("subscribe singleton as keyed", subscribe("pis.a", ServiceKeyed))
	step("subscribe keyed as singleton", subscribe("pis.k", ServiceSingleton))
	must(Withdraw(provider, a))
	step("subscribe singleton none", subscribe("pis.a", ServiceSingleton))
	must(provider.Dispose())
	step("invoke disposed", invoke("pis.o", "read", nil))
	step("subscribe disposed", subscribe("pis.o", ServiceSingleton))
	want := []string{
		"invoke notallowed service_not_allowed Remote service pis.o is not allowlisted",
		"invoke singleton none service_not_found Remote service pis.a has no provider",
		"invoke singleton addr service_mode_mismatch Remote service pis.a is singleton",
		"invoke keyed noaddr service_mode_mismatch Remote service pis.k is keyed",
		"invoke keyed missing service_instance_not_found Remote service pis.k has no instance x",
		`invoke singleton ok "A"`,
		"invoke singleton nomember service_member_not_found Unknown remote service member pis.a.nope",
		"invoke keyed stale service_stale_instance Remote service pis.k instance x is stale",
		`invoke keyed ok "X2"`,
		"invoke keyed nomember service_member_not_found Unknown remote service member pis.k.nope",
		"subscribe notallowed service_not_allowed Remote service pis.o is not allowlisted",
		"subscribe singleton as keyed service_mode_mismatch Remote service pis.a is singleton, not keyed",
		"subscribe keyed as singleton service_mode_mismatch Remote service pis.k is keyed, not singleton",
		"subscribe singleton none service_not_found Remote service pis.a has no provider",
		"invoke disposed - Remote service provider is disposed",
		"subscribe disposed - Remote service provider is disposed",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("invoke/subscribe:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}

	provider, err = NewRemoteServiceProvider(ServiceProviderDefinition{Id: a.Id()}, ServiceProviderDefinition{Id: k.Id(), Mode: ServiceKeyed})
	must(err)
	var log []string
	format := func(update ServiceProviderUpdate) string {
		address := update.Instance
		if update.Snapshot != nil && update.Snapshot.Instance != nil {
			address = update.Snapshot.Instance
		}
		if address == nil {
			return string(update.Type)
		}
		return fmt.Sprintf("%s(%s@%d)", update.Type, address.Key, address.Generation)
	}
	must(Provide(provider, a, impl("A")))
	singleton, err := provider.Subscribe("pis.a", ServiceSingleton, func(_ context.Context, update ServiceProviderUpdate) { log = append(log, "a:"+format(update)) })
	must(err)
	keyed, err := provider.Subscribe("pis.k", ServiceKeyed, func(_ context.Context, update ServiceProviderUpdate) { log = append(log, "k:"+format(update)) })
	must(err)
	log = append(log, fmt.Sprintf("snap a=%d k=%d", len(singleton.Snapshot().Instances), len(keyed.Snapshot().Instances)))
	must(Replace(provider, a, impl("A2")))
	closeY, err := Spawn(provider, k, "y", impl("Y"))
	must(err)
	_, err = Spawn(provider, k, "b", impl("B"))
	must(err)
	log = append(log, "activate")
	must(singleton.Activate())
	must(keyed.Activate())
	must(closeY())
	must(Withdraw(provider, a))
	must(Provide(provider, a, impl("A3")))
	_, err = Spawn(provider, k, "y", impl("Y2"))
	must(err)
	log = append(log, "dispose")
	must(provider.Dispose())
	const wantLog = "snap a=1 k=0 | activate | a:replaced | k:spawned(y@1) | k:spawned(b@1) | k:closed(y@1) | a:unavailable | k:spawned(y@2) | dispose | a:unavailable | k:closed(b@1) | k:closed(y@2)"
	if got := strings.Join(log, " | "); got != wantLog {
		t.Errorf("updates:\n got %s\nwant %s", got, wantLog)
	}
}
