package chord

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type snapshotGateSubscription struct{ snapshot ServiceSubscriptionSnapshot }

func (s snapshotGateSubscription) Snapshot() ServiceSubscriptionSnapshot { return s.snapshot }
func (snapshotGateSubscription) Activate() error                         { return nil }
func (snapshotGateSubscription) Close(context.Context) error             { return nil }

type snapshotGateTransport struct {
	gate    chan struct{}
	members string
}

func (snapshotGateTransport) Invoke(context.Context, ServiceCall) (json.RawMessage, error) {
	return nil, nil
}
func (t snapshotGateTransport) Subscribe(_ context.Context, serviceId string, mode ServiceMode, _ UpdateListener) (ServiceSubscription, error) {
	<-t.gate
	var instance ServiceInstanceSnapshot
	if err := json.Unmarshal([]byte(`{"members":`+t.members+`}`), &instance); err != nil {
		return nil, err
	}
	return snapshotGateSubscription{ServiceSubscriptionSnapshot{ServiceId: serviceId, Mode: mode, Instances: []ServiceInstanceSnapshot{instance}}}, nil
}

// Pi packages/chord/src/services/consumer.ts:175-197 (ServiceFacade.install): install describes and hydrates one member at a time in snapshot order, so a member-kind error leaves the earlier state members hydrated, and a hydration error is reported before a later member's kind error. Each expected line is what Pi's binding reports for the same snapshot under Node (oracle /tmp/facade1.mts, with service ID t.a renamed to fio.a because test service views are package-global).
func TestServiceFacadeInstallOrderMatchesPi(t *testing.T) {
	var got []string
	st := func(name string, value int) string {
		return fmt.Sprintf(`{"name":%q,"kind":"state","sequence":0,"ops":[["r",%d]]}`, name, value)
	}
	bad := func(name string) string {
		return fmt.Sprintf(`{"name":%q,"kind":"state","sequence":0,"ops":[["s",["x"],1]]}`, name)
	}
	m := func(name string) string { return fmt.Sprintf(`{"name":%q,"kind":"method"}`, name) }
	list := func(items ...string) string { return "[" + strings.Join(items, ",") + "]" }
	run := func(label, members string, pre func(*RemoteService)) {
		var mu sync.Mutex
		errs := []string{}
		transport := snapshotGateTransport{gate: make(chan struct{}), members: members}
		binding, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{Services: ServiceIDs("fio.a"), Transport: transport, OnError: func(err error) { mu.Lock(); errs = append(errs, err.Error()); mu.Unlock() }})
		if err != nil {
			t.Fatal(err)
		}
		svc, err := binding.Use("fio.a")
		if err != nil {
			t.Fatal(err)
		}
		pre(svc)
		close(transport.gate)
		ready := "ok"
		if err := binding.Ready(t.Context()); err != nil {
			ready = err.Error()
		}
		value := func(name string) string {
			replica, err := svc.State(name)
			if err != nil {
				return "!" + err.Error()
			}
			v, ok := replica.Value()
			if !ok {
				return "undefined"
			}
			raw, _ := json.Marshal(v)
			return string(raw)
		}
		mu.Lock()
		e, _ := json.Marshal(errs)
		mu.Unlock()
		got = append(got, fmt.Sprintf("%s | ready: %s | errors: %s | s1: %s | s3: %s", label, ready, e, value("s1"), value("s3")))
	}
	run("A", list(st("s1", 1), m("m")), func(s *RemoteService) { _, _ = s.State("m") })
	run("B", list(st("s1", 1), m("m"), st("s3", 3)), func(s *RemoteService) { _, _ = s.State("m") })
	run("C", list(bad("s1"), st("s2", 2)), func(s *RemoteService) { _, _ = s.Call(t.Context(), "s2") })
	run("D", list(st("s1", 1), st("s2", 2), st("s3", 3)), func(s *RemoteService) { _, _ = s.Call(t.Context(), "s2") })
	run("E", list(st("s1", 1), m("m"), st("s3", 3)), func(*RemoteService) {})
	run("F", list(st("s1", 1)), func(s *RemoteService) { _, _ = s.Call(t.Context(), "gone") })
	want := []string{
		`A | ready: Remote service member fio.a.m is method, not state | errors: ["Remote service member fio.a.m is method, not state"] | s1: 1 | s3: undefined`,
		`B | ready: Remote service member fio.a.m is method, not state | errors: ["Remote service member fio.a.m is method, not state"] | s1: 1 | s3: undefined`,
		`C | ready: Replicated state snapshot is not a base operation batch | errors: ["Replicated state snapshot is not a base operation batch"] | s1: undefined | s3: undefined`,
		`D | ready: Remote service member fio.a.s2 is state, not method | errors: ["Remote service member fio.a.s2 is state, not method"] | s1: 1 | s3: undefined`,
		`E | ready: ok | errors: [] | s1: 1 | s3: 3`,
		`F | ready: Unknown remote service member fio.a.gone | errors: ["Unknown remote service member fio.a.gone"] | s1: undefined | s3: undefined`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("install:\n got %s\nwant %s", strings.Join(got, "\n     "), strings.Join(want, "\n     "))
	}
}
